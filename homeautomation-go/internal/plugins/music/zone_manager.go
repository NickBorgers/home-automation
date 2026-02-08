package music

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Zone represents an active music zone with its own Sonos group
type Zone struct {
	Name         string
	MusicType    string
	Priority     int
	LeadSpeaker  string
	Participants []ParticipantWithVolume
	PlaylistURI  string
	MediaType    string
	StartedAt    time.Time

	// Playback monitoring (managed by Manager)
	monitorCancel context.CancelFunc
}

// ZoneEvaluation captures the result of evaluating a zone's triggers for audit logging
type ZoneEvaluation struct {
	Zone              string               `json:"zone"`
	Matched           bool                 `json:"matched"`
	MatchedVia        string               `json:"matchedVia,omitempty"`        // "triggers", "trigger_group", "musicPlaybackType", "default"
	MatchedGroupIndex int                  `json:"matchedGroupIndex,omitempty"` // Which trigger group matched (1-indexed, 0 = N/A)
	Reason            string               `json:"reason,omitempty"`            // Why it didn't match
	TriggerResults    []TriggerResult      `json:"triggerResults,omitempty"`    // Individual trigger evaluations
	GroupResults      []TriggerGroupResult `json:"groupResults,omitempty"`      // For zones with trigger_groups - all groups evaluated
	FailedConditions  []FailedCondition    `json:"failedConditions,omitempty"`  // Which conditions failed
}

// TriggerResult captures the evaluation of a single trigger condition
type TriggerResult struct {
	Variable      string      `json:"variable"`
	ExpectedValue interface{} `json:"expectedValue"`
	ActualValue   interface{} `json:"actualValue"`
	Matched       bool        `json:"matched"`
}

// FailedCondition records a trigger condition that prevented zone activation
type FailedCondition struct {
	Variable      string      `json:"variable"`
	ExpectedValue interface{} `json:"expectedValue"`
	ActualValue   interface{} `json:"actualValue"`
}

// TriggerGroupResult captures the evaluation of a trigger group
type TriggerGroupResult struct {
	GroupIndex int             `json:"groupIndex"` // 1-indexed
	Matched    bool            `json:"matched"`
	Triggers   []TriggerResult `json:"triggers"`
}

// ZoneResolutionAudit captures a complete zone resolution cycle for audit logging.
// This consolidates state snapshot, zone evaluations, and speaker assignments
// into a single log entry for easier debugging via Gravwell queries.
type ZoneResolutionAudit struct {
	// CorrelationID links this resolution to the triggering state change event.
	// Format: {timestamp_ms}-{counter} for uniqueness and chronological ordering.
	CorrelationID string `json:"correlationId,omitempty"`

	// Trigger identifies what caused this zone resolution (e.g., "trigger:isWakeSequenceActive")
	Trigger string `json:"trigger"`

	// Timestamp when zone resolution started
	Timestamp time.Time `json:"timestamp"`

	// StateSnapshot captures the values of all zone-relevant variables at evaluation time
	StateSnapshot map[string]interface{} `json:"stateSnapshot"`

	// ZoneEvaluations contains detailed evaluation results for each zone
	ZoneEvaluations []ZoneEvaluation `json:"zoneEvaluations"`

	// SpeakerAssignments maps zone names to their assigned speakers
	ZoneToSpeakers map[string][]string `json:"zoneToSpeakers"`

	// SpeakersToTurnOff lists speakers that will be stopped (were playing but now unassigned)
	SpeakersToTurnOff []string `json:"speakersToTurnOff,omitempty"`

	// ZoneChanges summarizes what zones will start, stop, or update
	ZoneChanges ZoneChangesSummary `json:"zoneChanges"`
}

// ZoneChangesSummary summarizes the zone changes resulting from resolution
type ZoneChangesSummary struct {
	Start  []string            `json:"start,omitempty"`
	Stop   []string            `json:"stop,omitempty"`
	Update map[string][]string `json:"update,omitempty"` // zone -> new speakers
}

// ZoneManager coordinates multiple concurrent music zones
type ZoneManager struct {
	mu          sync.RWMutex
	activeZones map[string]*Zone  // keyed by zone name
	speakerZone map[string]string // speaker -> zone name

	manager *Manager
	config  *MusicConfig
	logger  *zap.Logger
}

// NewZoneManager creates a new ZoneManager
func NewZoneManager(manager *Manager, config *MusicConfig, logger *zap.Logger) *ZoneManager {
	return &ZoneManager{
		activeZones: make(map[string]*Zone),
		speakerZone: make(map[string]string),
		manager:     manager,
		config:      config,
		logger:      logger.Named("zone_manager"),
	}
}

// GetActiveZones returns a copy of all active zones
func (zm *ZoneManager) GetActiveZones() []*Zone {
	zm.mu.RLock()
	defer zm.mu.RUnlock()

	zones := make([]*Zone, 0, len(zm.activeZones))
	for _, zone := range zm.activeZones {
		// Return a copy to avoid external mutations
		zoneCopy := *zone
		zones = append(zones, &zoneCopy)
	}
	return zones
}

// GetZone returns a zone by name, if it exists
func (zm *ZoneManager) GetZone(name string) (*Zone, bool) {
	zm.mu.RLock()
	defer zm.mu.RUnlock()

	zone, exists := zm.activeZones[name]
	if !exists {
		return nil, false
	}

	// Return a copy
	zoneCopy := *zone
	return &zoneCopy, true
}

// GetSpeakerZone returns which zone a speaker is assigned to
func (zm *ZoneManager) GetSpeakerZone(speaker string) (string, bool) {
	zm.mu.RLock()
	defer zm.mu.RUnlock()

	zoneName, exists := zm.speakerZone[speaker]
	return zoneName, exists
}

// StopAllZones stops all active zones
func (zm *ZoneManager) StopAllZones(reason string) {
	zm.mu.Lock()
	defer zm.mu.Unlock()

	zm.logger.Info("Stopping all zones", zap.String("reason", reason))

	for name, zone := range zm.activeZones {
		zm.logger.Info("Stopping zone",
			zap.String("zone", name),
			zap.String("reason", reason))

		// Cancel playback monitor if running
		if zone.monitorCancel != nil {
			zone.monitorCancel()
		}
	}

	// Clear all state
	zm.activeZones = make(map[string]*Zone)
	zm.speakerZone = make(map[string]string)
}

// evaluateTriggers checks if trigger conditions for a zone are met.
// Supports two modes:
// - Legacy: zone.Triggers (AND logic between all conditions)
// - New: zone.TriggerGroups (OR between groups, AND within each group)
func (zm *ZoneManager) evaluateTriggers(zone ZoneConfig) bool {
	// New: trigger_groups (OR between groups, AND within each)
	if len(zone.TriggerGroups) > 0 {
		for _, group := range zone.TriggerGroups {
			if zm.evaluateTriggerList(group.Triggers) {
				return true // Any group matching activates zone
			}
		}
		return false
	}

	// Legacy: single trigger list (AND logic)
	if len(zone.Triggers) > 0 {
		return zm.evaluateTriggerList(zone.Triggers)
	}

	// No triggers = activated by musicPlaybackType only
	return false
}

// evaluateTriggerList checks if all trigger conditions in a list are met (AND logic)
func (zm *ZoneManager) evaluateTriggerList(triggers []TriggerCondition) bool {
	if len(triggers) == 0 {
		return false
	}

	for _, trigger := range triggers {
		value, err := zm.manager.getStateValue(trigger.Variable)
		if err != nil {
			zm.logger.Debug("Failed to get trigger variable",
				zap.String("variable", trigger.Variable),
				zap.Error(err))
			return false
		}

		if !zm.manager.valuesMatch(value, trigger.Value) {
			return false
		}
	}

	return true
}

// evaluateTriggerListWithDetails returns detailed results for each trigger condition
func (zm *ZoneManager) evaluateTriggerListWithDetails(triggers []TriggerCondition) (bool, []TriggerResult, []FailedCondition) {
	results := make([]TriggerResult, 0, len(triggers))
	failed := make([]FailedCondition, 0)

	if len(triggers) == 0 {
		return false, results, failed
	}

	allMatched := true
	for _, trigger := range triggers {
		value, err := zm.manager.getStateValue(trigger.Variable)
		if err != nil {
			results = append(results, TriggerResult{
				Variable:      trigger.Variable,
				ExpectedValue: trigger.Value,
				ActualValue:   nil,
				Matched:       false,
			})
			failed = append(failed, FailedCondition{
				Variable:      trigger.Variable,
				ExpectedValue: trigger.Value,
				ActualValue:   nil,
			})
			allMatched = false
			continue
		}

		matched := zm.manager.valuesMatch(value, trigger.Value)
		results = append(results, TriggerResult{
			Variable:      trigger.Variable,
			ExpectedValue: trigger.Value,
			ActualValue:   value,
			Matched:       matched,
		})

		if !matched {
			failed = append(failed, FailedCondition{
				Variable:      trigger.Variable,
				ExpectedValue: trigger.Value,
				ActualValue:   value,
			})
			allMatched = false
		}
	}

	return allMatched, results, failed
}

// evaluateTriggersWithDetails performs zone evaluation with detailed results for audit logging
func (zm *ZoneManager) evaluateTriggersWithDetails(zone ZoneConfig) ZoneEvaluation {
	eval := ZoneEvaluation{
		Zone: zone.Name,
	}

	// Check trigger_groups first (OR between groups, AND within each)
	if len(zone.TriggerGroups) > 0 {
		// Evaluate ALL groups to provide complete audit data
		allGroupResults := make([]TriggerGroupResult, 0, len(zone.TriggerGroups))
		matchedGroupIndex := -1

		for i, group := range zone.TriggerGroups {
			matched, triggers, _ := zm.evaluateTriggerListWithDetails(group.Triggers)
			groupResult := TriggerGroupResult{
				GroupIndex: i + 1, // 1-indexed for readability
				Matched:    matched,
				Triggers:   triggers,
			}
			allGroupResults = append(allGroupResults, groupResult)

			// Track first matching group
			if matched && matchedGroupIndex < 0 {
				matchedGroupIndex = i
			}
		}

		// Always populate GroupResults for complete audit trail
		eval.GroupResults = allGroupResults

		if matchedGroupIndex >= 0 {
			eval.Matched = true
			eval.MatchedVia = "trigger_group"
			eval.MatchedGroupIndex = matchedGroupIndex + 1
			// Collect trigger results for the matched group
			eval.TriggerResults = allGroupResults[matchedGroupIndex].Triggers
		} else {
			// No group matched - collect all failed conditions from all groups
			eval.Matched = false
			eval.Reason = "no trigger group conditions met"
			for _, gr := range allGroupResults {
				for _, tr := range gr.Triggers {
					if !tr.Matched {
						eval.FailedConditions = append(eval.FailedConditions, FailedCondition{
							Variable:      tr.Variable,
							ExpectedValue: tr.ExpectedValue,
							ActualValue:   tr.ActualValue,
						})
					}
				}
			}
		}
		return eval
	}

	// Legacy: single trigger list (AND logic)
	if len(zone.Triggers) > 0 {
		matched, triggers, failed := zm.evaluateTriggerListWithDetails(zone.Triggers)
		eval.TriggerResults = triggers
		if matched {
			eval.Matched = true
			eval.MatchedVia = "triggers"
		} else {
			eval.Matched = false
			eval.Reason = "trigger conditions not met"
			eval.FailedConditions = failed
		}
		return eval
	}

	// No triggers = can only be activated by musicPlaybackType
	eval.Matched = false
	eval.Reason = "no triggers defined (musicPlaybackType activation only)"
	return eval
}

// captureStateSnapshot captures the current state of all zone-relevant variables for audit logging
func (zm *ZoneManager) captureStateSnapshot() map[string]interface{} {
	snapshot := make(map[string]interface{})

	// Capture core state variables
	coreVars := []string{
		"dayPhase",
		"isWakeSequenceActive",
		"isMasterAsleep",
		"isAnyoneHome",
		"isAnyoneAsleep",
		"isEveryoneAsleep",
		"musicPlaybackType",
	}

	for _, varName := range coreVars {
		if val, err := zm.manager.getStateValue(varName); err == nil {
			snapshot[varName] = val
		}
	}

	// Also capture any custom trigger variables from zone configs
	for _, zone := range zm.config.GetZones() {
		for _, trigger := range zone.Triggers {
			if _, exists := snapshot[trigger.Variable]; !exists {
				if val, err := zm.manager.getStateValue(trigger.Variable); err == nil {
					snapshot[trigger.Variable] = val
				}
			}
		}
		for _, group := range zone.TriggerGroups {
			for _, trigger := range group.Triggers {
				if _, exists := snapshot[trigger.Variable]; !exists {
					if val, err := zm.manager.getStateValue(trigger.Variable); err == nil {
						snapshot[trigger.Variable] = val
					}
				}
			}
		}
	}

	return snapshot
}

// getActiveZoneConfigs returns zone configs that should currently be active
func (zm *ZoneManager) getActiveZoneConfigs() []ZoneConfig {
	zoneConfigs := zm.config.GetZones()
	activeConfigs := make([]ZoneConfig, 0)

	// Check explicit musicPlaybackType for backward compatibility
	musicPlaybackType, _ := zm.manager.stateManager.GetString("musicPlaybackType")

	for _, zc := range zoneConfigs {
		// Check if zone should be active via triggers
		if zm.evaluateTriggers(zc) {
			activeConfigs = append(activeConfigs, zc)
			continue
		}

		// Check if zone matches explicit musicPlaybackType (backward compat)
		// ONLY for zones WITHOUT triggers - zones WITH triggers must pass their trigger checks
		hasTriggers := len(zc.Triggers) > 0 || len(zc.TriggerGroups) > 0
		if musicPlaybackType != "" && zc.Name == musicPlaybackType && !hasTriggers {
			activeConfigs = append(activeConfigs, zc)
			continue
		}

		// Check if this is the default zone and no other zone is active
		if zc.Default && len(activeConfigs) == 0 {
			activeConfigs = append(activeConfigs, zc)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(activeConfigs, func(i, j int) bool {
		return activeConfigs[i].Priority > activeConfigs[j].Priority
	})

	return activeConfigs
}

// assignSpeakersToZones determines which speakers go to which zones
// Returns: map of zone name -> speaker names, and list of speakers to turn off
func (zm *ZoneManager) assignSpeakersToZones() (map[string][]string, []string) {
	activeZoneConfigs := zm.getActiveZoneConfigs()

	// Track which speakers are assigned
	assigned := make(map[string]string) // speaker -> zone name
	zoneToSpeakers := make(map[string][]string)

	// Process zones in priority order (already sorted)
	for _, zc := range activeZoneConfigs {
		mode, ok := zm.config.Music[zc.Name]
		if !ok {
			zm.logger.Warn("Zone references unknown music mode",
				zap.String("zone", zc.Name))
			continue
		}

		for _, participant := range mode.Participants {
			// Skip if already assigned to higher-priority zone
			if _, taken := assigned[participant.PlayerName]; taken {
				continue
			}

			// Check exclude_if conditions (Phase 1 logic)
			if !zm.manager.shouldIncludeInZone(participant) {
				continue
			}

			assigned[participant.PlayerName] = zc.Name
			zoneToSpeakers[zc.Name] = append(zoneToSpeakers[zc.Name], participant.PlayerName)
		}
	}

	// Find speakers that should be turned off (were playing but now unassigned)
	zm.mu.RLock()
	speakersToTurnOff := make([]string, 0)
	for speaker := range zm.speakerZone {
		if _, stillAssigned := assigned[speaker]; !stillAssigned {
			speakersToTurnOff = append(speakersToTurnOff, speaker)
		}
	}
	zm.mu.RUnlock()

	return zoneToSpeakers, speakersToTurnOff
}

// ResolveZones evaluates zone triggers and updates active zones accordingly.
// This is a convenience wrapper that calls ResolveZonesWithContext with no event context.
func (zm *ZoneManager) ResolveZones(trigger string) error {
	return zm.ResolveZonesWithContext(nil, trigger)
}

// ResolveZonesWithContext evaluates zone triggers and updates active zones accordingly.
// If an EventContext is provided, it will be included in audit logs for cross-plugin correlation.
func (zm *ZoneManager) ResolveZonesWithContext(eventCtx *state.EventContext, trigger string) error {
	resolutionTime := time.Now()

	// Build base log fields
	logFields := []zap.Field{zap.String("trigger", trigger)}
	if eventCtx != nil {
		logFields = append(logFields, zap.String("correlation_id", eventCtx.CorrelationID))
	}
	zm.logger.Info("Resolving zones", logFields...)

	// Capture state snapshot at evaluation time for audit logging
	stateSnapshot := zm.captureStateSnapshot()

	// Evaluate all zones with detailed results for audit logging
	zoneConfigs := zm.config.GetZones()
	zoneEvaluations := make([]ZoneEvaluation, 0, len(zoneConfigs))
	musicPlaybackType, _ := zm.manager.stateManager.GetString("musicPlaybackType")

	for _, zc := range zoneConfigs {
		eval := zm.evaluateTriggersWithDetails(zc)

		// Check if zone matches via musicPlaybackType (backward compat)
		if !eval.Matched {
			hasTriggers := len(zc.Triggers) > 0 || len(zc.TriggerGroups) > 0
			if musicPlaybackType != "" && zc.Name == musicPlaybackType && !hasTriggers {
				eval.Matched = true
				eval.MatchedVia = "musicPlaybackType"
				eval.Reason = ""
			}
		}

		// Check if zone matches as default
		if !eval.Matched && zc.Default {
			// Default zone logic is more complex - only activates if no other zone is active
			// For now, just note it's a default zone
			eval.Reason = "default zone (activates only if no other zone matches)"
		}

		zoneEvaluations = append(zoneEvaluations, eval)
	}

	zoneToSpeakers, speakersToTurnOff := zm.assignSpeakersToZones()

	// Determine what changed while holding the lock
	var zonesToStop []string
	var zonesToStart []string
	zonesToUpdate := make(map[string][]string) // zone -> new speakers

	zm.mu.Lock()
	// Find zones that should stop (no speakers assigned)
	for zoneName := range zm.activeZones {
		if _, stillActive := zoneToSpeakers[zoneName]; !stillActive {
			zonesToStop = append(zonesToStop, zoneName)
		}
	}

	// Find zones that need to start or update
	for zoneName, speakers := range zoneToSpeakers {
		if len(speakers) == 0 {
			continue // Skip zones with no speakers
		}

		if _, exists := zm.activeZones[zoneName]; !exists {
			zonesToStart = append(zonesToStart, zoneName)
		} else {
			// Check if speaker list changed
			currentSpeakers := zm.getZoneSpeakers(zoneName)
			if !stringSlicesEqual(currentSpeakers, speakers) {
				zonesToUpdate[zoneName] = speakers
			}
		}
	}
	zm.mu.Unlock()

	// Build and log comprehensive zone resolution audit
	// This consolidates state snapshot, zone evaluations, speaker assignments, and zone changes
	// into a single log entry for easier debugging via Gravwell queries
	audit := ZoneResolutionAudit{
		Trigger:           trigger,
		Timestamp:         resolutionTime,
		StateSnapshot:     stateSnapshot,
		ZoneEvaluations:   zoneEvaluations,
		ZoneToSpeakers:    zoneToSpeakers,
		SpeakersToTurnOff: speakersToTurnOff,
		ZoneChanges: ZoneChangesSummary{
			Start:  zonesToStart,
			Stop:   zonesToStop,
			Update: zonesToUpdate,
		},
	}
	if eventCtx != nil {
		audit.CorrelationID = eventCtx.CorrelationID
	}

	// Log comprehensive audit at Debug level for Gravwell queries
	zm.logger.Debug("Zone resolution audit", zap.Any("audit", audit))

	// Also log zone changes at Info level for operational visibility
	changeLogFields := []zap.Field{
		zap.Strings("stop", zonesToStop),
		zap.Strings("start", zonesToStart),
		zap.Any("update", zonesToUpdate),
	}
	if eventCtx != nil {
		changeLogFields = append(changeLogFields, zap.String("correlation_id", eventCtx.CorrelationID))
	}
	zm.logger.Info("Zone changes", changeLogFields...)

	// Stop zones first (releases speakers)
	for _, zoneName := range zonesToStop {
		if err := zm.stopZone(zoneName, fmt.Sprintf("trigger:%s", trigger)); err != nil {
			zm.logger.Error("Failed to stop zone",
				zap.String("zone", zoneName),
				zap.Error(err))
		}
	}

	// Start new zones
	for _, zoneName := range zonesToStart {
		if err := zm.startZone(zoneName, zoneToSpeakers[zoneName], trigger); err != nil {
			zm.logger.Error("Failed to start zone",
				zap.String("zone", zoneName),
				zap.Error(err))
		}
	}

	// Update existing zones with new speaker assignments
	for zoneName, speakers := range zonesToUpdate {
		if err := zm.updateZoneSpeakers(zoneName, speakers, trigger); err != nil {
			zm.logger.Error("Failed to update zone speakers",
				zap.String("zone", zoneName),
				zap.Error(err))
		}
	}

	return nil
}

// getZoneSpeakers returns the speaker names for a zone (must hold mu.RLock)
func (zm *ZoneManager) getZoneSpeakers(zoneName string) []string {
	zone, exists := zm.activeZones[zoneName]
	if !exists {
		return nil
	}

	speakers := make([]string, len(zone.Participants))
	for i, p := range zone.Participants {
		speakers[i] = p.PlayerName
	}
	return speakers
}

// stringSlicesEqual checks if two string slices have the same elements (order-independent)
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	aMap := make(map[string]bool)
	for _, s := range a {
		aMap[s] = true
	}

	for _, s := range b {
		if !aMap[s] {
			return false
		}
	}

	return true
}

// startZone starts playback for a new zone
func (zm *ZoneManager) startZone(zoneName string, speakers []string, trigger string) error {
	zm.logger.Info("Starting zone",
		zap.String("zone", zoneName),
		zap.Strings("speakers", speakers),
		zap.String("trigger", trigger))

	// Get zone config and music mode
	var zoneConfig *ZoneConfig
	for _, zc := range zm.config.GetZones() {
		if zc.Name == zoneName {
			zcCopy := zc
			zoneConfig = &zcCopy
			break
		}
	}
	if zoneConfig == nil {
		return fmt.Errorf("zone config not found: %s", zoneName)
	}

	mode, ok := zm.config.Music[zoneName]
	if !ok {
		return fmt.Errorf("music mode not found: %s", zoneName)
	}

	// Build participant list with volumes
	participants := make([]ParticipantWithVolume, 0, len(speakers))
	speakerSet := make(map[string]bool)
	for _, s := range speakers {
		speakerSet[s] = true
	}

	// Get next playlist
	playlistIndex := zm.manager.getNextPlaylistIndex(zoneName, len(mode.PlaybackOptions))
	playbackOption := mode.PlaybackOptions[playlistIndex]

	for _, p := range mode.Participants {
		if !speakerSet[p.PlayerName] {
			continue
		}

		volume := zm.manager.calculateVolume(p.BaseVolume, playbackOption.VolumeMultiplier)
		participants = append(participants, ParticipantWithVolume{
			PlayerName:    p.PlayerName,
			BaseVolume:    p.BaseVolume,
			Volume:        volume,
			DefaultVolume: volume,
			LeaveMutedIf:  p.LeaveMutedIf,
			ExcludeIf:     p.ExcludeIf,
		})
	}

	if len(participants) == 0 {
		return fmt.Errorf("no participants for zone: %s", zoneName)
	}

	// Select lead speaker (first participant)
	leadSpeaker := participants[0].PlayerName

	// Create zone struct
	zone := &Zone{
		Name:         zoneName,
		MusicType:    zoneName,
		Priority:     zoneConfig.Priority,
		LeadSpeaker:  leadSpeaker,
		Participants: participants,
		PlaylistURI:  playbackOption.URI,
		MediaType:    playbackOption.MediaType,
		StartedAt:    time.Now(),
	}

	// Store zone state
	zm.mu.Lock()
	zm.activeZones[zoneName] = zone
	for _, p := range participants {
		zm.speakerZone[p.PlayerName] = zoneName
	}
	zm.mu.Unlock()

	// Delegate actual playback to manager
	go func() {
		if err := zm.manager.orchestrateZonePlayback(zone, playbackOption, trigger); err != nil {
			zm.logger.Error("Failed to orchestrate zone playback",
				zap.String("zone", zoneName),
				zap.Error(err))
		}
	}()

	return nil
}

// stopZone stops playback for a zone
func (zm *ZoneManager) stopZone(zoneName string, reason string) error {
	zm.mu.Lock()
	zone, exists := zm.activeZones[zoneName]
	if !exists {
		zm.mu.Unlock()
		return nil // Already stopped
	}

	// Cancel monitor
	if zone.monitorCancel != nil {
		zone.monitorCancel()
	}

	// Remove from tracking
	for _, p := range zone.Participants {
		delete(zm.speakerZone, p.PlayerName)
	}
	delete(zm.activeZones, zoneName)
	zm.mu.Unlock()

	zm.logger.Info("Stopped zone",
		zap.String("zone", zoneName),
		zap.String("reason", reason))

	// Fade out speakers (delegate to manager)
	go func() {
		if err := zm.manager.fadeOutZoneSpeakers(zone, reason); err != nil {
			zm.logger.Error("Failed to fade out zone speakers",
				zap.String("zone", zoneName),
				zap.Error(err))
		}
	}()

	return nil
}

// updateZoneSpeakers updates the speaker assignment for an active zone
func (zm *ZoneManager) updateZoneSpeakers(zoneName string, newSpeakers []string, trigger string) error {
	zm.mu.Lock()
	zone, exists := zm.activeZones[zoneName]
	if !exists {
		zm.mu.Unlock()
		return fmt.Errorf("zone not found: %s", zoneName)
	}

	// Find speakers to add/remove
	currentSpeakers := make(map[string]bool)
	for _, p := range zone.Participants {
		currentSpeakers[p.PlayerName] = true
	}

	newSpeakerSet := make(map[string]bool)
	for _, s := range newSpeakers {
		newSpeakerSet[s] = true
	}

	speakersToAdd := make([]string, 0)
	speakersToRemove := make([]string, 0)

	for s := range newSpeakerSet {
		if !currentSpeakers[s] {
			speakersToAdd = append(speakersToAdd, s)
		}
	}

	for s := range currentSpeakers {
		if !newSpeakerSet[s] {
			speakersToRemove = append(speakersToRemove, s)
		}
	}

	// Update tracking
	for _, s := range speakersToRemove {
		delete(zm.speakerZone, s)
	}
	for _, s := range speakersToAdd {
		zm.speakerZone[s] = zoneName
	}
	zm.mu.Unlock()

	zm.logger.Info("Updating zone speakers",
		zap.String("zone", zoneName),
		zap.Strings("add", speakersToAdd),
		zap.Strings("remove", speakersToRemove))

	// Delegate speaker changes to manager
	if len(speakersToRemove) > 0 {
		go zm.manager.removeSpeakersFromZone(zone, speakersToRemove, trigger)
	}
	if len(speakersToAdd) > 0 {
		go zm.manager.addSpeakersToZone(zone, speakersToAdd, trigger)
	}

	return nil
}
