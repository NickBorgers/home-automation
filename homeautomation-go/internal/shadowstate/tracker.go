package shadowstate

import (
	"sync"
	"time"
)

// Tracker manages shadow state for all plugins
type Tracker struct {
	mu             sync.RWMutex
	pluginStates   map[string]PluginShadowState
	stateProviders map[string]func() PluginShadowState
}

// NewTracker creates a new shadow state tracker
func NewTracker() *Tracker {
	return &Tracker{
		pluginStates:   make(map[string]PluginShadowState),
		stateProviders: make(map[string]func() PluginShadowState),
	}
}

// RegisterPlugin registers a plugin's shadow state
func (t *Tracker) RegisterPlugin(pluginName string, state PluginShadowState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pluginStates[pluginName] = state
}

// RegisterPluginProvider registers a function that provides a plugin's shadow state dynamically
func (t *Tracker) RegisterPluginProvider(pluginName string, provider func() PluginShadowState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stateProviders[pluginName] = provider
}

// GetPluginState retrieves a plugin's shadow state
func (t *Tracker) GetPluginState(pluginName string) (PluginShadowState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Check provider first (dynamic state)
	if provider, ok := t.stateProviders[pluginName]; ok {
		return provider(), true
	}

	// Fall back to static state
	state, ok := t.pluginStates[pluginName]
	return state, ok
}

// GetAllPluginStates retrieves all plugin shadow states
func (t *Tracker) GetAllPluginStates() map[string]PluginShadowState {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Create a copy to avoid race conditions
	// Include both static states and provider states
	totalSize := len(t.pluginStates) + len(t.stateProviders)
	states := make(map[string]PluginShadowState, totalSize)

	// Add static states
	for k, v := range t.pluginStates {
		states[k] = v
	}

	// Add provider states (these take precedence if there's a name collision)
	for k, provider := range t.stateProviders {
		states[k] = provider()
	}

	return states
}

// ============================================================================
// Generic Base Tracker Types
// ============================================================================

// ActionTracker provides common tracker functionality for action-heavy plugins.
// Embed this in plugin-specific trackers to get UpdateCurrentInputs,
// SnapshotInputsForAction, and thread-safe state access for free.
type ActionTracker[O any] struct {
	mu    sync.RWMutex
	state *ShadowState[ActionInputs, O]
}

// NewActionTracker creates a new action tracker with the given initial state.
func NewActionTracker[O any](state *ShadowState[ActionInputs, O]) ActionTracker[O] {
	return ActionTracker[O]{state: state}
}

// UpdateCurrentInputs updates the current input values.
func (t *ActionTracker[O]) UpdateCurrentInputs(inputs map[string]interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for key, value := range inputs {
		t.state.Inputs.Current[key] = value
	}
	t.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot.
func (t *ActionTracker[O]) SnapshotInputsForAction() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range t.state.Inputs.Current {
		t.state.Inputs.AtLastAction[key] = value
	}
}

// Lock acquires the write lock. Used by plugin-specific methods.
func (t *ActionTracker[O]) Lock() { t.mu.Lock() }

// Unlock releases the write lock.
func (t *ActionTracker[O]) Unlock() { t.mu.Unlock() }

// RLock acquires the read lock. Used by GetState methods.
func (t *ActionTracker[O]) RLock() { t.mu.RLock() }

// RUnlock releases the read lock.
func (t *ActionTracker[O]) RUnlock() { t.mu.RUnlock() }

// State returns the underlying state. Caller must hold the lock.
func (t *ActionTracker[O]) State() *ShadowState[ActionInputs, O] { return t.state }

// ReadOnlyTracker provides common tracker functionality for read-heavy plugins.
// Embed this in plugin-specific trackers to get UpdateCurrentInputs for free.
type ReadOnlyTracker[O any] struct {
	mu    sync.RWMutex
	state *ShadowState[ReadOnlyInputs, O]
}

// NewReadOnlyTracker creates a new read-only tracker with the given initial state.
func NewReadOnlyTracker[O any](state *ShadowState[ReadOnlyInputs, O]) ReadOnlyTracker[O] {
	return ReadOnlyTracker[O]{state: state}
}

// UpdateCurrentInputs updates the current input values.
func (t *ReadOnlyTracker[O]) UpdateCurrentInputs(inputs map[string]interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for key, value := range inputs {
		t.state.Inputs.Current[key] = value
	}
	t.state.Metadata.LastUpdated = time.Now()
}

// Lock acquires the write lock. Used by plugin-specific methods.
func (t *ReadOnlyTracker[O]) Lock() { t.mu.Lock() }

// Unlock releases the write lock.
func (t *ReadOnlyTracker[O]) Unlock() { t.mu.Unlock() }

// RLock acquires the read lock. Used by GetState methods.
func (t *ReadOnlyTracker[O]) RLock() { t.mu.RLock() }

// RUnlock releases the read lock.
func (t *ReadOnlyTracker[O]) RUnlock() { t.mu.RUnlock() }

// State returns the underlying state. Caller must hold the lock.
func (t *ReadOnlyTracker[O]) State() *ShadowState[ReadOnlyInputs, O] { return t.state }

// copyInputMap creates a shallow copy of an input map for thread-safe deep copies.
func copyInputMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ============================================================================
// Plugin-Specific Trackers
// ============================================================================

// LightingTracker manages shadow state specifically for the lighting plugin
type LightingTracker struct {
	ActionTracker[LightingOutputs]
}

// NewLightingTracker creates a new lighting shadow state tracker
func NewLightingTracker() *LightingTracker {
	return &LightingTracker{
		ActionTracker: NewActionTracker(NewLightingShadowState()),
	}
}

// RecordRoomAction records an action taken on a room
func (lt *LightingTracker) RecordRoomAction(roomName string, actionType string, reason string, activeScene string, turnedOff bool) {
	lt.Lock()
	defer lt.Unlock()

	now := time.Now()
	lt.State().Outputs.Rooms[roomName] = RoomState{
		ActiveScene: activeScene,
		TurnedOff:   turnedOff,
		LastAction:  now,
		ActionType:  actionType,
		Reason:      reason,
	}
	lt.State().Outputs.LastActionTime = now
	lt.State().Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (lt *LightingTracker) GetState() *LightingShadowState {
	lt.RLock()
	defer lt.RUnlock()

	s := lt.State()
	stateCopy := &LightingShadowState{
		Plugin: s.Plugin,
		Inputs: ActionInputs{
			Current:      copyInputMap(s.Inputs.Current),
			AtLastAction: copyInputMap(s.Inputs.AtLastAction),
		},
		Outputs: LightingOutputs{
			Rooms:          make(map[string]RoomState),
			LastActionTime: s.Outputs.LastActionTime,
		},
		Metadata: s.Metadata,
	}

	for k, v := range s.Outputs.Rooms {
		stateCopy.Outputs.Rooms[k] = v
	}

	return stateCopy
}

// SecurityTracker manages shadow state specifically for the security plugin
type SecurityTracker struct {
	ActionTracker[SecurityOutputs]
}

// NewSecurityTracker creates a new security shadow state tracker
func NewSecurityTracker() *SecurityTracker {
	return &SecurityTracker{
		ActionTracker: NewActionTracker(NewSecurityShadowState()),
	}
}

// RecordLockdownAction records a lockdown activation or deactivation
func (st *SecurityTracker) RecordLockdownAction(active bool, reason string) {
	st.Lock()
	defer st.Unlock()

	now := time.Now()
	st.State().Outputs.Lockdown.Active = active
	st.State().Outputs.Lockdown.Reason = reason

	if active {
		st.State().Outputs.Lockdown.ActivatedAt = now
		st.State().Outputs.Lockdown.WillResetAt = now.Add(5 * time.Second)
	} else {
		st.State().Outputs.Lockdown.ActivatedAt = time.Time{}
		st.State().Outputs.Lockdown.WillResetAt = time.Time{}
	}

	st.State().Outputs.LastActionTime = now
	st.State().Metadata.LastUpdated = now
}

// RecordDoorbellEvent records a doorbell press event
func (st *SecurityTracker) RecordDoorbellEvent(rateLimited bool, ttsSent bool, lightsFlashed bool) {
	st.Lock()
	defer st.Unlock()

	now := time.Now()
	st.State().Outputs.LastDoorbell = &DoorbellEvent{
		Timestamp:     now,
		RateLimited:   rateLimited,
		TTSSent:       ttsSent,
		LightsFlashed: lightsFlashed,
	}
	st.State().Outputs.LastActionTime = now
	st.State().Metadata.LastUpdated = now
}

// RecordVehicleArrivalEvent records a vehicle arrival event
func (st *SecurityTracker) RecordVehicleArrivalEvent(rateLimited bool, ttsSent bool, wasExpecting bool) {
	st.Lock()
	defer st.Unlock()

	now := time.Now()
	st.State().Outputs.LastVehicle = &VehicleArrivalEvent{
		Timestamp:    now,
		RateLimited:  rateLimited,
		TTSSent:      ttsSent,
		WasExpecting: wasExpecting,
	}
	st.State().Outputs.LastActionTime = now
	st.State().Metadata.LastUpdated = now
}

// RecordGarageOpenEvent records a garage auto-open event
func (st *SecurityTracker) RecordGarageOpenEvent(reason string, garageWasEmpty bool) {
	st.Lock()
	defer st.Unlock()

	now := time.Now()
	st.State().Outputs.LastGarageOpen = &GarageOpenEvent{
		Timestamp:      now,
		Reason:         reason,
		GarageWasEmpty: garageWasEmpty,
	}
	st.State().Outputs.LastActionTime = now
	st.State().Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (st *SecurityTracker) GetState() *SecurityShadowState {
	st.RLock()
	defer st.RUnlock()

	s := st.State()
	stateCopy := &SecurityShadowState{
		Plugin: s.Plugin,
		Inputs: ActionInputs{
			Current:      copyInputMap(s.Inputs.Current),
			AtLastAction: copyInputMap(s.Inputs.AtLastAction),
		},
		Outputs: SecurityOutputs{
			Lockdown:       s.Outputs.Lockdown,
			LastDoorbell:   s.Outputs.LastDoorbell,
			LastVehicle:    s.Outputs.LastVehicle,
			LastGarageOpen: s.Outputs.LastGarageOpen,
			LastActionTime: s.Outputs.LastActionTime,
		},
		Metadata: s.Metadata,
	}

	return stateCopy
}

// LoadSheddingTracker manages shadow state specifically for the load shedding plugin
type LoadSheddingTracker struct {
	ActionTracker[LoadSheddingOutputs]
}

// NewLoadSheddingTracker creates a new load shedding shadow state tracker
func NewLoadSheddingTracker() *LoadSheddingTracker {
	return &LoadSheddingTracker{
		ActionTracker: NewActionTracker(NewLoadSheddingShadowState()),
	}
}

// RecordLoadSheddingAction records a load shedding activation or deactivation
func (lst *LoadSheddingTracker) RecordLoadSheddingAction(active bool, actionType string, reason string, thermostatSettings ThermostatSettings) {
	lst.Lock()
	defer lst.Unlock()

	now := time.Now()
	lst.State().Outputs.Active = active
	lst.State().Outputs.LastActionType = actionType
	lst.State().Outputs.LastActionReason = reason
	lst.State().Outputs.ThermostatSettings = thermostatSettings
	lst.State().Outputs.LastActionTime = now
	lst.State().Metadata.LastUpdated = now
}

// RecordThermalBatteryActivation records a thermal battery activation
func (lst *LoadSheddingTracker) RecordThermalBatteryActivation(offset float64, savedSetpoints map[string]SavedSetpoint) {
	lst.Lock()
	defer lst.Unlock()

	now := time.Now()
	lst.State().Outputs.ThermalBattery = ThermalBatteryState{
		Active:         true,
		OffsetApplied:  offset,
		ActivatedAt:    now,
		SavedSetpoints: savedSetpoints,
	}
	lst.State().Metadata.LastUpdated = now
}

// RecordThermalBatteryDeactivation records a thermal battery deactivation
func (lst *LoadSheddingTracker) RecordThermalBatteryDeactivation() {
	lst.Lock()
	defer lst.Unlock()

	now := time.Now()
	lst.State().Outputs.ThermalBattery = ThermalBatteryState{
		Active:        false,
		DeactivatedAt: now,
	}
	lst.State().Metadata.LastUpdated = now
}

// RecordThermalBatteryStepProgress updates the stepping progress on the thermal battery state
func (lst *LoadSheddingTracker) RecordThermalBatteryStepProgress(stepsCompleted, totalSteps int, stepSize float64) {
	lst.Lock()
	defer lst.Unlock()

	now := time.Now()
	lst.State().Outputs.ThermalBattery.StepsCompleted = stepsCompleted
	lst.State().Outputs.ThermalBattery.TotalSteps = totalSteps
	lst.State().Outputs.ThermalBattery.StepSize = stepSize
	lst.State().Outputs.ThermalBattery.OffsetApplied = float64(stepsCompleted) * stepSize
	lst.State().Outputs.ThermalBattery.Stepping = stepsCompleted < totalSteps
	lst.State().Metadata.LastUpdated = now
}

// RecordThermalBatterySkipped records that thermal battery activation was skipped
func (lst *LoadSheddingTracker) RecordThermalBatterySkipped(reason string) {
	lst.Lock()
	defer lst.Unlock()

	now := time.Now()
	lst.State().Outputs.ThermalBattery.SkipReason = reason
	lst.State().Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (lst *LoadSheddingTracker) GetState() *LoadSheddingShadowState {
	lst.RLock()
	defer lst.RUnlock()

	s := lst.State()

	// Deep copy ThermalBattery.SavedSetpoints
	var savedSetpoints map[string]SavedSetpoint
	if s.Outputs.ThermalBattery.SavedSetpoints != nil {
		savedSetpoints = make(map[string]SavedSetpoint, len(s.Outputs.ThermalBattery.SavedSetpoints))
		for k, v := range s.Outputs.ThermalBattery.SavedSetpoints {
			savedSetpoints[k] = v
		}
	}

	outputsCopy := s.Outputs
	outputsCopy.ThermalBattery.SavedSetpoints = savedSetpoints

	return &LoadSheddingShadowState{
		Plugin: s.Plugin,
		Inputs: ActionInputs{
			Current:      copyInputMap(s.Inputs.Current),
			AtLastAction: copyInputMap(s.Inputs.AtLastAction),
		},
		Outputs:  outputsCopy,
		Metadata: s.Metadata,
	}
}

// SleepHygieneTracker manages shadow state specifically for the sleep hygiene plugin
type SleepHygieneTracker struct {
	ActionTracker[SleepHygieneOutputs]
}

// NewSleepHygieneTracker creates a new sleep hygiene shadow state tracker
func NewSleepHygieneTracker() *SleepHygieneTracker {
	return &SleepHygieneTracker{
		ActionTracker: NewActionTracker(NewSleepHygieneShadowState()),
	}
}

// RecordAction records a sleep hygiene action
func (st *SleepHygieneTracker) RecordAction(actionType string, reason string) {
	st.Lock()
	defer st.Unlock()

	now := time.Now()
	st.State().Outputs.LastActionTime = now
	st.State().Outputs.LastActionType = actionType
	st.State().Outputs.LastActionReason = reason
	st.State().Metadata.LastUpdated = now
}

// UpdateWakeSequenceStatus updates the wake sequence status
func (st *SleepHygieneTracker) UpdateWakeSequenceStatus(status string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.WakeSequenceStatus = status
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordFadeOutStart records the start of a speaker fade-out
func (st *SleepHygieneTracker) RecordFadeOutStart(speakerEntityID string, startVolume int) {
	st.Lock()
	defer st.Unlock()

	now := time.Now()
	st.State().Outputs.FadeOutProgress[speakerEntityID] = SpeakerFadeOut{
		SpeakerEntityID: speakerEntityID,
		CurrentVolume:   startVolume,
		StartVolume:     startVolume,
		IsActive:        true,
		StartTime:       now,
		LastUpdate:      now,
	}
	st.State().Metadata.LastUpdated = now
}

// UpdateFadeOutProgress updates the fade-out progress for a speaker
func (st *SleepHygieneTracker) UpdateFadeOutProgress(speakerEntityID string, currentVolume int) {
	st.Lock()
	defer st.Unlock()

	if fadeOut, exists := st.State().Outputs.FadeOutProgress[speakerEntityID]; exists {
		fadeOut.CurrentVolume = currentVolume
		fadeOut.LastUpdate = time.Now()
		if currentVolume == 0 {
			fadeOut.IsActive = false
		}
		st.State().Outputs.FadeOutProgress[speakerEntityID] = fadeOut
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordHumanOverride records that a human override was detected during fade-out
func (st *SleepHygieneTracker) RecordHumanOverride(speakerEntityID string, expectedVolume, actualVolume int) {
	st.Lock()
	defer st.Unlock()

	if fadeOut, exists := st.State().Outputs.FadeOutProgress[speakerEntityID]; exists {
		fadeOut.HumanOverrideDetected = true
		fadeOut.ExpectedVolume = expectedVolume
		fadeOut.ActualVolume = actualVolume
		fadeOut.IsActive = false
		fadeOut.LastUpdate = time.Now()
		st.State().Outputs.FadeOutProgress[speakerEntityID] = fadeOut
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// ClearFadeOutProgress clears all fade-out progress
func (st *SleepHygieneTracker) ClearFadeOutProgress() {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.FadeOutProgress = make(map[string]SpeakerFadeOut)
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordTTSAnnouncement records a TTS announcement
func (st *SleepHygieneTracker) RecordTTSAnnouncement(message string, speaker string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastTTSAnnouncement = &TTSAnnouncement{
		Message:   message,
		Speaker:   speaker,
		Timestamp: time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordStopScreensReminder records a stop screens reminder trigger
func (st *SleepHygieneTracker) RecordStopScreensReminder() {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.StopScreensReminder = &ReminderTrigger{
		Triggered: true,
		Timestamp: time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordGoToBedReminder records a go to bed reminder trigger
func (st *SleepHygieneTracker) RecordGoToBedReminder() {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.GoToBedReminder = &ReminderTrigger{
		Triggered: true,
		Timestamp: time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// UpdateEightSleepAvailability updates the Eight Sleep availability status
func (st *SleepHygieneTracker) UpdateEightSleepAvailability(available bool, checkTime time.Time) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.EightSleepAvailable = available
	st.State().Outputs.BackupWakeEnabled = !available
	st.State().Outputs.LastAvailabilityCheck = checkTime
	st.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (st *SleepHygieneTracker) GetState() *SleepHygieneShadowState {
	st.RLock()
	defer st.RUnlock()

	s := st.State()
	stateCopy := &SleepHygieneShadowState{
		Plugin: s.Plugin,
		Inputs: ActionInputs{
			Current:      copyInputMap(s.Inputs.Current),
			AtLastAction: copyInputMap(s.Inputs.AtLastAction),
		},
		Outputs: SleepHygieneOutputs{
			WakeSequenceStatus:    s.Outputs.WakeSequenceStatus,
			FadeOutProgress:       make(map[string]SpeakerFadeOut),
			EightSleepAvailable:   s.Outputs.EightSleepAvailable,
			BackupWakeEnabled:     s.Outputs.BackupWakeEnabled,
			LastAvailabilityCheck: s.Outputs.LastAvailabilityCheck,
			LastActionTime:        s.Outputs.LastActionTime,
			LastActionType:        s.Outputs.LastActionType,
			LastActionReason:      s.Outputs.LastActionReason,
		},
		Metadata: s.Metadata,
	}

	for k, v := range s.Outputs.FadeOutProgress {
		stateCopy.Outputs.FadeOutProgress[k] = v
	}

	if s.Outputs.LastTTSAnnouncement != nil {
		announcement := *s.Outputs.LastTTSAnnouncement
		stateCopy.Outputs.LastTTSAnnouncement = &announcement
	}
	if s.Outputs.StopScreensReminder != nil {
		reminder := *s.Outputs.StopScreensReminder
		stateCopy.Outputs.StopScreensReminder = &reminder
	}
	if s.Outputs.GoToBedReminder != nil {
		reminder := *s.Outputs.GoToBedReminder
		stateCopy.Outputs.GoToBedReminder = &reminder
	}

	return stateCopy
}

// ============================================================================
// Read-Heavy Plugin Trackers
// ============================================================================

// EnergyTracker manages shadow state for the energy plugin
type EnergyTracker struct {
	ReadOnlyTracker[EnergyOutputs]
}

// NewEnergyTracker creates a new energy shadow state tracker
func NewEnergyTracker() *EnergyTracker {
	return &EnergyTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewEnergyShadowState()),
	}
}

// UpdateSensorReadings updates the raw sensor readings
func (et *EnergyTracker) UpdateSensorReadings(batteryPct, thisHourKW, remainingKWH float64, gridAvailable bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.SensorReadings.BatteryPercentage = batteryPct
	et.State().Outputs.SensorReadings.ThisHourSolarGenerationKW = thisHourKW
	et.State().Outputs.SensorReadings.RemainingSolarGenerationKWH = remainingKWH
	et.State().Outputs.SensorReadings.IsGridAvailable = gridAvailable
	et.State().Outputs.SensorReadings.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateBatteryPercentage updates the battery percentage sensor reading
func (et *EnergyTracker) UpdateBatteryPercentage(pct float64) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.SensorReadings.BatteryPercentage = pct
	et.State().Outputs.SensorReadings.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateThisHourSolarKW updates the this-hour solar generation sensor reading
func (et *EnergyTracker) UpdateThisHourSolarKW(kw float64) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.SensorReadings.ThisHourSolarGenerationKW = kw
	et.State().Outputs.SensorReadings.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateRemainingSolarKWH updates the remaining solar generation sensor reading
func (et *EnergyTracker) UpdateRemainingSolarKWH(kwh float64) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.SensorReadings.RemainingSolarGenerationKWH = kwh
	et.State().Outputs.SensorReadings.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateGridAvailable updates the grid availability sensor reading
func (et *EnergyTracker) UpdateGridAvailable(available bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.SensorReadings.IsGridAvailable = available
	et.State().Outputs.SensorReadings.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateBatteryLevel updates the computed battery energy level
func (et *EnergyTracker) UpdateBatteryLevel(level string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.BatteryEnergyLevel = level
	et.State().Outputs.LastComputations.LastBatteryLevelCalc = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateSolarLevel updates the computed solar production energy level
func (et *EnergyTracker) UpdateSolarLevel(level string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.SolarProductionEnergyLevel = level
	et.State().Outputs.LastComputations.LastSolarLevelCalc = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateOverallLevel updates the computed overall energy level
func (et *EnergyTracker) UpdateOverallLevel(level string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.CurrentEnergyLevel = level
	et.State().Outputs.LastComputations.LastOverallLevelCalc = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateFreeEnergyAvailable updates the free energy availability status
func (et *EnergyTracker) UpdateFreeEnergyAvailable(available bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.IsFreeEnergyAvailable = available
	et.State().Outputs.LastComputations.LastFreeEnergyCheck = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateDiscoveredIndicatorLights updates the list of discovered indicator light entities
func (et *EnergyTracker) UpdateDiscoveredIndicatorLights(entities []string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.DiscoveredIndicatorLights = entities
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateIndicatorLightsAction updates the last indicator lights action
func (et *EnergyTracker) UpdateIndicatorLightsAction(energyLevel string, rgbColor []int, brightnessPct int, entityIDs []string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.IndicatorLightsAction = &IndicatorLightsAction{
		EnergyLevel:   energyLevel,
		RGBColor:      rgbColor,
		BrightnessPct: brightnessPct,
		EntityIDs:     entityIDs,
		Timestamp:     time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateLuxReading updates a single lux sensor reading
func (et *EnergyTracker) UpdateLuxReading(sensorEntity string, lux float64) {
	et.Lock()
	defer et.Unlock()

	if et.State().Outputs.LuxSensorReadings == nil {
		et.State().Outputs.LuxSensorReadings = make(map[string]LuxSensorReading)
	}

	et.State().Outputs.LuxSensorReadings[sensorEntity] = LuxSensorReading{
		EntityID:  sensorEntity,
		Lux:       lux,
		Timestamp: time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateLightToLuxMapping updates the light-to-lux sensor mapping
func (et *EnergyTracker) UpdateLightToLuxMapping(mapping map[string]string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LightToLuxSensorMapping = make(map[string]string)
	for k, v := range mapping {
		et.State().Outputs.LightToLuxSensorMapping[k] = v
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdatePerDeviceBrightness updates the brightness info for a single device
func (et *EnergyTracker) UpdatePerDeviceBrightness(lightEntity, luxEntity string, lux float64, brightness int, isAdaptive bool) {
	et.Lock()
	defer et.Unlock()

	if et.State().Outputs.PerDeviceBrightness == nil {
		et.State().Outputs.PerDeviceBrightness = make(map[string]PerDeviceBrightness)
	}

	et.State().Outputs.PerDeviceBrightness[lightEntity] = PerDeviceBrightness{
		LightEntity:     lightEntity,
		LuxSensorEntity: luxEntity,
		CurrentLux:      lux,
		BrightnessPct:   brightness,
		IsAdaptive:      isAdaptive,
		LastUpdate:      time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateBaselineLux updates the baseline lux calibration for a single device.
func (et *EnergyTracker) UpdateBaselineLux(lightEntity string, baselineLux float64) {
	et.Lock()
	defer et.Unlock()

	if et.State().Outputs.BaselineCalibrations == nil {
		et.State().Outputs.BaselineCalibrations = make(map[string]BaselineCalibration)
	}

	et.State().Outputs.BaselineCalibrations[lightEntity] = BaselineCalibration{
		LightEntity:         lightEntity,
		BaselineLux:         baselineLux,
		LastCalibrationTime: time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (et *EnergyTracker) GetState() *EnergyShadowState {
	et.RLock()
	defer et.RUnlock()

	s := et.State()
	stateCopy := &EnergyShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: EnergyOutputs{
			BatteryEnergyLevel:         s.Outputs.BatteryEnergyLevel,
			SolarProductionEnergyLevel: s.Outputs.SolarProductionEnergyLevel,
			CurrentEnergyLevel:         s.Outputs.CurrentEnergyLevel,
			IsFreeEnergyAvailable:      s.Outputs.IsFreeEnergyAvailable,
			LastComputations:           s.Outputs.LastComputations,
			SensorReadings:             s.Outputs.SensorReadings,
			DiscoveredIndicatorLights:  s.Outputs.DiscoveredIndicatorLights,
			IndicatorLightsAction:      s.Outputs.IndicatorLightsAction,
		},
		Metadata: s.Metadata,
	}

	if s.Outputs.LuxSensorReadings != nil {
		stateCopy.Outputs.LuxSensorReadings = make(map[string]LuxSensorReading)
		for k, v := range s.Outputs.LuxSensorReadings {
			stateCopy.Outputs.LuxSensorReadings[k] = v
		}
	}
	if s.Outputs.PerDeviceBrightness != nil {
		stateCopy.Outputs.PerDeviceBrightness = make(map[string]PerDeviceBrightness)
		for k, v := range s.Outputs.PerDeviceBrightness {
			stateCopy.Outputs.PerDeviceBrightness[k] = v
		}
	}
	if s.Outputs.LightToLuxSensorMapping != nil {
		stateCopy.Outputs.LightToLuxSensorMapping = make(map[string]string)
		for k, v := range s.Outputs.LightToLuxSensorMapping {
			stateCopy.Outputs.LightToLuxSensorMapping[k] = v
		}
	}
	if s.Outputs.BaselineCalibrations != nil {
		stateCopy.Outputs.BaselineCalibrations = make(map[string]BaselineCalibration)
		for k, v := range s.Outputs.BaselineCalibrations {
			stateCopy.Outputs.BaselineCalibrations[k] = v
		}
	}

	return stateCopy
}

// StateTrackingTracker manages shadow state for the state tracking plugin
type StateTrackingTracker struct {
	ReadOnlyTracker[StateTrackingOutputs]
}

// NewStateTrackingTracker creates a new state tracking shadow state tracker
func NewStateTrackingTracker() *StateTrackingTracker {
	return &StateTrackingTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewStateTrackingShadowState()),
	}
}

// UpdateDerivedStates updates the computed derived states
func (stt *StateTrackingTracker) UpdateDerivedStates(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep bool) {
	stt.Lock()
	defer stt.Unlock()

	stt.State().Outputs.DerivedStates.IsAnyOwnerHome = anyOwnerHome
	stt.State().Outputs.DerivedStates.IsAnyoneHome = anyoneHome
	stt.State().Outputs.DerivedStates.IsAnyoneAsleep = anyoneAsleep
	stt.State().Outputs.DerivedStates.IsEveryoneAsleep = everyoneAsleep
	stt.State().Outputs.LastComputation = time.Now()
	stt.State().Metadata.LastUpdated = time.Now()
}

// UpdateSleepDetectionTimer updates the sleep detection timer state
func (stt *StateTrackingTracker) UpdateSleepDetectionTimer(active bool) {
	stt.Lock()
	defer stt.Unlock()

	stt.State().Outputs.TimerStates.SleepDetectionActive = active
	if active {
		stt.State().Outputs.TimerStates.SleepDetectionStarted = time.Now()
	} else {
		stt.State().Outputs.TimerStates.SleepDetectionStarted = time.Time{}
	}
	stt.State().Metadata.LastUpdated = time.Now()
}

// UpdateWakeDetectionTimer updates the wake detection timer state
func (stt *StateTrackingTracker) UpdateWakeDetectionTimer(active bool) {
	stt.Lock()
	defer stt.Unlock()

	stt.State().Outputs.TimerStates.WakeDetectionActive = active
	if active {
		stt.State().Outputs.TimerStates.WakeDetectionStarted = time.Now()
	} else {
		stt.State().Outputs.TimerStates.WakeDetectionStarted = time.Time{}
	}
	stt.State().Metadata.LastUpdated = time.Now()
}

// UpdateOwnerReturnTimer updates the owner return home auto-reset timer state
func (stt *StateTrackingTracker) UpdateOwnerReturnTimer(active bool) {
	stt.Lock()
	defer stt.Unlock()

	stt.State().Outputs.TimerStates.OwnerReturnResetActive = active
	if active {
		stt.State().Outputs.TimerStates.OwnerReturnResetStarted = time.Now()
	} else {
		stt.State().Outputs.TimerStates.OwnerReturnResetStarted = time.Time{}
	}
	stt.State().Metadata.LastUpdated = time.Now()
}

// RecordArrivalAnnouncement records an arrival TTS announcement
func (stt *StateTrackingTracker) RecordArrivalAnnouncement(person, message string) {
	stt.Lock()
	defer stt.Unlock()

	stt.State().Outputs.LastAnnouncement = &ArrivalAnnouncement{
		Person:    person,
		Message:   message,
		Timestamp: time.Now(),
	}
	stt.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (stt *StateTrackingTracker) GetState() *StateTrackingShadowState {
	stt.RLock()
	defer stt.RUnlock()

	s := stt.State()
	stateCopy := &StateTrackingShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: StateTrackingOutputs{
			DerivedStates:   s.Outputs.DerivedStates,
			TimerStates:     s.Outputs.TimerStates,
			LastComputation: s.Outputs.LastComputation,
		},
		Metadata: s.Metadata,
	}

	if s.Outputs.LastAnnouncement != nil {
		announcement := *s.Outputs.LastAnnouncement
		stateCopy.Outputs.LastAnnouncement = &announcement
	}

	return stateCopy
}

// DayPhaseTracker manages shadow state for the day phase plugin
type DayPhaseTracker struct {
	ReadOnlyTracker[DayPhaseOutputs]
}

// NewDayPhaseTracker creates a new day phase shadow state tracker
func NewDayPhaseTracker() *DayPhaseTracker {
	return &DayPhaseTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewDayPhaseShadowState()),
	}
}

// UpdateSunEvent updates the computed sun event
func (dpt *DayPhaseTracker) UpdateSunEvent(sunEvent string) {
	dpt.Lock()
	defer dpt.Unlock()

	dpt.State().Outputs.SunEvent = sunEvent
	dpt.State().Outputs.LastSunEventCalc = time.Now()
	dpt.State().Metadata.LastUpdated = time.Now()
}

// UpdateDayPhase updates the computed day phase
func (dpt *DayPhaseTracker) UpdateDayPhase(dayPhase string) {
	dpt.Lock()
	defer dpt.Unlock()

	dpt.State().Outputs.DayPhase = dayPhase
	dpt.State().Outputs.LastDayPhaseCalc = time.Now()
	dpt.State().Metadata.LastUpdated = time.Now()
}

// UpdateNextTransition updates the next expected phase transition
func (dpt *DayPhaseTracker) UpdateNextTransition(transitionTime time.Time, nextPhase string) {
	dpt.Lock()
	defer dpt.Unlock()

	dpt.State().Outputs.NextTransitionTime = transitionTime
	dpt.State().Outputs.NextTransitionPhase = nextPhase
	dpt.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (dpt *DayPhaseTracker) GetState() *DayPhaseShadowState {
	dpt.RLock()
	defer dpt.RUnlock()

	s := dpt.State()
	return &DayPhaseShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs:  s.Outputs,
		Metadata: s.Metadata,
	}
}

// TVTracker manages shadow state for the TV plugin
type TVTracker struct {
	ReadOnlyTracker[TVOutputs]
}

// NewTVTracker creates a new TV shadow state tracker
func NewTVTracker() *TVTracker {
	return &TVTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewTVShadowState()),
	}
}

// UpdateAppleTVState updates the Apple TV playing state
func (tvt *TVTracker) UpdateAppleTVState(isPlaying bool, state string) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.IsAppleTVPlaying = isPlaying
	tvt.State().Outputs.AppleTVState = state
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateTVPower updates the TV power state
func (tvt *TVTracker) UpdateTVPower(isOn bool) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.IsTVOn = isOn
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateHDMIInput updates the current HDMI input
func (tvt *TVTracker) UpdateHDMIInput(input string) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.CurrentHDMIInput = input
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateTVPlaying updates the computed isTVPlaying state
func (tvt *TVTracker) UpdateTVPlaying(isPlaying bool) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.IsTVPlaying = isPlaying
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateSyncBoxAvailable updates the sync box availability state
func (tvt *TVTracker) UpdateSyncBoxAvailable(available bool) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.SyncBoxAvailable = available
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateLastRecovery updates the last sync box recovery timestamp and daily count
func (tvt *TVTracker) UpdateLastRecovery(rebootTime time.Time, dailyCount int) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.LastSyncBoxReboot = rebootTime
	tvt.State().Outputs.DailyRebootCount = dailyCount
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateLastBraviaReload updates the last Bravia integration reload timestamp and count
func (tvt *TVTracker) UpdateLastBraviaReload(reloadTime time.Time, reloadCount int) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.LastBraviaReload = reloadTime
	tvt.State().Outputs.BraviaReloadCount = reloadCount
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// UpdateBraviaReloadFailed updates whether the last Bravia reload attempt failed
func (tvt *TVTracker) UpdateBraviaReloadFailed(failed bool) {
	tvt.Lock()
	defer tvt.Unlock()

	tvt.State().Outputs.BraviaReloadFailed = failed
	tvt.State().Outputs.LastUpdate = time.Now()
	tvt.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (tvt *TVTracker) GetState() *TVShadowState {
	tvt.RLock()
	defer tvt.RUnlock()

	s := tvt.State()
	return &TVShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs:  s.Outputs,
		Metadata: s.Metadata,
	}
}

// SexModeTracker manages shadow state for the sex mode plugin
type SexModeTracker struct {
	ActionTracker[SexModeOutputs]
}

// NewSexModeTracker creates a new sex mode shadow state tracker
func NewSexModeTracker() *SexModeTracker {
	return &SexModeTracker{
		ActionTracker: NewActionTracker(NewSexModeShadowState()),
	}
}

// RecordAction records a sex mode activation or deactivation
func (smt *SexModeTracker) RecordAction(actionType string, reason string, isActive bool, preSexMusicType string, activatedAt time.Time) {
	smt.Lock()
	defer smt.Unlock()

	now := time.Now()
	smt.State().Outputs.IsActive = isActive
	smt.State().Outputs.PreSexMusicType = preSexMusicType
	smt.State().Outputs.ActivatedAt = activatedAt
	smt.State().Outputs.LastActionType = actionType
	smt.State().Outputs.LastActionReason = reason
	smt.State().Outputs.LastActionTime = now
	smt.State().Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (smt *SexModeTracker) GetState() *SexModeShadowState {
	smt.RLock()
	defer smt.RUnlock()

	s := smt.State()
	return &SexModeShadowState{
		Plugin: s.Plugin,
		Inputs: ActionInputs{
			Current:      copyInputMap(s.Inputs.Current),
			AtLastAction: copyInputMap(s.Inputs.AtLastAction),
		},
		Outputs:  s.Outputs,
		Metadata: s.Metadata,
	}
}

// ChristmasTracker manages shadow state for the christmas plugin
type ChristmasTracker struct {
	ActionTracker[ChristmasOutputs]
}

// NewChristmasTracker creates a new christmas shadow state tracker
func NewChristmasTracker() *ChristmasTracker {
	return &ChristmasTracker{
		ActionTracker: NewActionTracker(NewChristmasShadowState()),
	}
}

// RecordActivation records a christmas lights activation
func (ct *ChristmasTracker) RecordActivation(lightsActivated int, reason string) {
	ct.Lock()
	defer ct.Unlock()

	now := time.Now()
	ct.State().Outputs.LastActivationTime = now
	ct.State().Outputs.LightsActivated = lightsActivated
	ct.State().Outputs.LastActionReason = reason
	ct.State().Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (ct *ChristmasTracker) GetState() *ChristmasShadowState {
	ct.RLock()
	defer ct.RUnlock()

	s := ct.State()
	return &ChristmasShadowState{
		Plugin: s.Plugin,
		Inputs: ActionInputs{
			Current:      copyInputMap(s.Inputs.Current),
			AtLastAction: copyInputMap(s.Inputs.AtLastAction),
		},
		Outputs:  s.Outputs,
		Metadata: s.Metadata,
	}
}

// ============================================================================
// Environmental Monitoring Tracker
// ============================================================================

// EnvironmentalTracker manages shadow state for the environmental monitoring plugin
type EnvironmentalTracker struct {
	ReadOnlyTracker[EnvironmentalOutputs]
}

// NewEnvironmentalTracker creates a new environmental shadow state tracker
func NewEnvironmentalTracker() *EnvironmentalTracker {
	return &EnvironmentalTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewEnvironmentalShadowState()),
	}
}

// UpdateHumiditySensors updates the list of humidity sensors and their values
func (et *EnvironmentalTracker) UpdateHumiditySensors(sensors []HumiditySensorData) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.HumiditySensors = make([]HumiditySensorData, len(sensors))
	copy(et.State().Outputs.HumiditySensors, sensors)
	et.State().Outputs.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateOutdoorHumidity updates the outdoor reference humidity value
func (et *EnvironmentalTracker) UpdateOutdoorHumidity(humidity float64, valid bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.OutdoorHumidity = humidity
	et.State().Outputs.OutdoorHumidityValid = valid
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateAlertLevel updates the current alert level and sustained status
func (et *EnvironmentalTracker) UpdateAlertLevel(level string, conditionStartTime time.Time, isSustained bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.AlertLevel = level
	et.State().Outputs.ConditionStartTime = conditionStartTime
	et.State().Outputs.IsSustained = isSustained
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (et *EnvironmentalTracker) RecordNotification(level, message string, sensorLocations []string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastNotification = &NotificationRecord{
		Level:           level,
		Message:         message,
		SensorLocations: sensorLocations,
		Timestamp:       time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordResolutionNotice records a resolution notification
func (et *EnvironmentalTracker) RecordResolutionNotice(message string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastResolutionNotice = &NotificationRecord{
		Level:     "resolved",
		Message:   message,
		Timestamp: time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateWaterLeakSensors updates the list of water leak sensors
func (et *EnvironmentalTracker) UpdateWaterLeakSensors(sensors []WaterLeakSensorData) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.WaterLeakSensors = make([]WaterLeakSensorData, len(sensors))
	copy(et.State().Outputs.WaterLeakSensors, sensors)
	et.State().Outputs.LastUpdate = time.Now()
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateActiveWaterLeaks updates the list of active water leak alerts
func (et *EnvironmentalTracker) UpdateActiveWaterLeaks(alerts []WaterLeakAlert) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.ActiveWaterLeaks = make([]WaterLeakAlert, len(alerts))
	copy(et.State().Outputs.ActiveWaterLeaks, alerts)
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordWaterLeakNotification records a water leak notification that was sent
func (et *EnvironmentalTracker) RecordWaterLeakNotification(entityID, friendlyName, message string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastWaterLeakNotice = &WaterLeakNotification{
		EntityID:     entityID,
		FriendlyName: friendlyName,
		Message:      message,
		Timestamp:    time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (et *EnvironmentalTracker) GetState() *EnvironmentalShadowState {
	et.RLock()
	defer et.RUnlock()

	s := et.State()
	stateCopy := &EnvironmentalShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: EnvironmentalOutputs{
			HumiditySensors:      make([]HumiditySensorData, len(s.Outputs.HumiditySensors)),
			WaterLeakSensors:     make([]WaterLeakSensorData, len(s.Outputs.WaterLeakSensors)),
			ActiveWaterLeaks:     make([]WaterLeakAlert, len(s.Outputs.ActiveWaterLeaks)),
			AlertLevel:           s.Outputs.AlertLevel,
			ConditionStartTime:   s.Outputs.ConditionStartTime,
			IsSustained:          s.Outputs.IsSustained,
			OutdoorHumidity:      s.Outputs.OutdoorHumidity,
			OutdoorHumidityValid: s.Outputs.OutdoorHumidityValid,
			LastUpdate:           s.Outputs.LastUpdate,
		},
		Metadata: s.Metadata,
	}

	copy(stateCopy.Outputs.HumiditySensors, s.Outputs.HumiditySensors)
	copy(stateCopy.Outputs.WaterLeakSensors, s.Outputs.WaterLeakSensors)
	copy(stateCopy.Outputs.ActiveWaterLeaks, s.Outputs.ActiveWaterLeaks)

	if s.Outputs.LastNotification != nil {
		notification := *s.Outputs.LastNotification
		if notification.SensorLocations != nil {
			notification.SensorLocations = make([]string, len(s.Outputs.LastNotification.SensorLocations))
			copy(notification.SensorLocations, s.Outputs.LastNotification.SensorLocations)
		}
		stateCopy.Outputs.LastNotification = &notification
	}
	if s.Outputs.LastResolutionNotice != nil {
		resolution := *s.Outputs.LastResolutionNotice
		stateCopy.Outputs.LastResolutionNotice = &resolution
	}
	if s.Outputs.LastWaterLeakNotice != nil {
		waterLeakNotice := *s.Outputs.LastWaterLeakNotice
		stateCopy.Outputs.LastWaterLeakNotice = &waterLeakNotice
	}

	return stateCopy
}

// ============================================================================
// Sensor Health Tracker
// ============================================================================

// SensorHealthTracker manages shadow state for the sensor health plugin
type SensorHealthTracker struct {
	ReadOnlyTracker[SensorHealthOutputs]
}

// NewSensorHealthTracker creates a new sensor health shadow state tracker
func NewSensorHealthTracker() *SensorHealthTracker {
	return &SensorHealthTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewSensorHealthShadowState()),
	}
}

// UpdateBatterySensors updates the list of discovered battery sensors
func (st *SensorHealthTracker) UpdateBatterySensors(sensors []BatterySensorData) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.BatterySensors = make([]BatterySensorData, len(sensors))
	copy(st.State().Outputs.BatterySensors, sensors)
	st.State().Outputs.LastUpdate = time.Now()
	st.State().Metadata.LastUpdated = time.Now()
}

// UpdateTemperatureSensors updates the list of discovered temperature sensors
func (st *SensorHealthTracker) UpdateTemperatureSensors(sensors []TemperatureSensorData) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.TemperatureSensors = make([]TemperatureSensorData, len(sensors))
	copy(st.State().Outputs.TemperatureSensors, sensors)
	st.State().Outputs.LastUpdate = time.Now()
	st.State().Metadata.LastUpdated = time.Now()
}

// UpdateLowBatteryAlerts updates the list of low battery alerts
func (st *SensorHealthTracker) UpdateLowBatteryAlerts(alerts []LowBatteryAlert) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LowBatteryAlerts = make([]LowBatteryAlert, len(alerts))
	copy(st.State().Outputs.LowBatteryAlerts, alerts)
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (st *SensorHealthTracker) RecordNotification(alertType, entityID, message string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastNotification = &SensorHealthNotification{
		AlertType: alertType,
		EntityID:  entityID,
		Message:   message,
		Timestamp: time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordTemperatureLockupNotification records a temperature lockup notification
func (st *SensorHealthTracker) RecordTemperatureLockupNotification(entityID, friendlyName, message string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastTemperatureLockupNotice = &TemperatureLockupNotice{
		EntityID:     entityID,
		FriendlyName: friendlyName,
		Message:      message,
		Timestamp:    time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordTemperatureRecoveryNotification records a temperature recovery notification
func (st *SensorHealthTracker) RecordTemperatureRecoveryNotification(entityID, friendlyName, message string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastTemperatureRecoveryNotice = &TemperatureRecoveryNotice{
		EntityID:     entityID,
		FriendlyName: friendlyName,
		Message:      message,
		Timestamp:    time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// UpdateNodeStatuses updates the list of discovered Z-Wave node status sensors
func (st *SensorHealthTracker) UpdateNodeStatuses(statuses []NodeStatusData) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.NodeStatuses = make([]NodeStatusData, len(statuses))
	copy(st.State().Outputs.NodeStatuses, statuses)
	st.State().Outputs.LastUpdate = time.Now()
	st.State().Metadata.LastUpdated = time.Now()
}

// UpdateDeadDeviceAlerts updates the list of dead device alerts
func (st *SensorHealthTracker) UpdateDeadDeviceAlerts(alerts []DeadDeviceAlert) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.DeadDeviceAlerts = make([]DeadDeviceAlert, len(alerts))
	copy(st.State().Outputs.DeadDeviceAlerts, alerts)
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordDeadDeviceNotification records a dead device notification
func (st *SensorHealthTracker) RecordDeadDeviceNotification(entityID, deviceName, message string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastDeadDeviceNotification = &DeadDeviceNotification{
		EntityID:   entityID,
		DeviceName: deviceName,
		Message:    message,
		Timestamp:  time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// RecordDeviceRecoveryNotification records a device recovery notification
func (st *SensorHealthTracker) RecordDeviceRecoveryNotification(entityID, deviceName, message string) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastDeviceRecoveryNotification = &DeviceRecoveryNotification{
		EntityID:   entityID,
		DeviceName: deviceName,
		Message:    message,
		Timestamp:  time.Now(),
	}
	st.State().Metadata.LastUpdated = time.Now()
}

// SetLastDiscoveryRefresh records when discovery was last refreshed
func (st *SensorHealthTracker) SetLastDiscoveryRefresh(t time.Time) {
	st.Lock()
	defer st.Unlock()

	st.State().Outputs.LastDiscoveryRefresh = t
	st.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (st *SensorHealthTracker) GetState() *SensorHealthShadowState {
	st.RLock()
	defer st.RUnlock()

	s := st.State()
	stateCopy := &SensorHealthShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: SensorHealthOutputs{
			BatterySensors:       make([]BatterySensorData, len(s.Outputs.BatterySensors)),
			TemperatureSensors:   make([]TemperatureSensorData, len(s.Outputs.TemperatureSensors)),
			NodeStatuses:         make([]NodeStatusData, len(s.Outputs.NodeStatuses)),
			LowBatteryAlerts:     make([]LowBatteryAlert, len(s.Outputs.LowBatteryAlerts)),
			DeadDeviceAlerts:     make([]DeadDeviceAlert, len(s.Outputs.DeadDeviceAlerts)),
			Config:               s.Outputs.Config,
			LastUpdate:           s.Outputs.LastUpdate,
			LastDiscoveryRefresh: s.Outputs.LastDiscoveryRefresh,
		},
		Metadata: s.Metadata,
	}

	copy(stateCopy.Outputs.BatterySensors, s.Outputs.BatterySensors)
	copy(stateCopy.Outputs.TemperatureSensors, s.Outputs.TemperatureSensors)
	copy(stateCopy.Outputs.NodeStatuses, s.Outputs.NodeStatuses)
	copy(stateCopy.Outputs.LowBatteryAlerts, s.Outputs.LowBatteryAlerts)
	copy(stateCopy.Outputs.DeadDeviceAlerts, s.Outputs.DeadDeviceAlerts)

	if s.Outputs.LastNotification != nil {
		notification := *s.Outputs.LastNotification
		stateCopy.Outputs.LastNotification = &notification
	}
	if s.Outputs.LastTemperatureLockupNotice != nil {
		lockupNotice := *s.Outputs.LastTemperatureLockupNotice
		stateCopy.Outputs.LastTemperatureLockupNotice = &lockupNotice
	}
	if s.Outputs.LastTemperatureRecoveryNotice != nil {
		recoveryNotice := *s.Outputs.LastTemperatureRecoveryNotice
		stateCopy.Outputs.LastTemperatureRecoveryNotice = &recoveryNotice
	}
	if s.Outputs.LastDeadDeviceNotification != nil {
		deadNotice := *s.Outputs.LastDeadDeviceNotification
		stateCopy.Outputs.LastDeadDeviceNotification = &deadNotice
	}
	if s.Outputs.LastDeviceRecoveryNotification != nil {
		recoveryNotice := *s.Outputs.LastDeviceRecoveryNotification
		stateCopy.Outputs.LastDeviceRecoveryNotification = &recoveryNotice
	}

	return stateCopy
}

// ============================================================================
// Sensor Config Tracker
// ============================================================================

// SensorConfigTracker manages shadow state for the sensor config plugin
type SensorConfigTracker struct {
	ReadOnlyTracker[SensorConfigOutputs]
}

// NewSensorConfigTracker creates a new sensor config shadow state tracker
func NewSensorConfigTracker() *SensorConfigTracker {
	return &SensorConfigTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewSensorConfigShadowState()),
	}
}

// RecordConfiguration records a threshold configuration
func (sct *SensorConfigTracker) RecordConfiguration(configType, description string, value float64, configuredEntities, failedEntities []string) {
	sct.Lock()
	defer sct.Unlock()

	now := time.Now()
	config := ThresholdConfiguration{
		ConfigType:         configType,
		Description:        description,
		Value:              value,
		ConfiguredEntities: configuredEntities,
		FailedEntities:     failedEntities,
		ConfiguredAt:       now,
	}

	sct.State().Outputs.Configurations = append(sct.State().Outputs.Configurations, config)
	sct.State().Outputs.ConfiguredAt = now
	sct.State().Outputs.LastUpdate = now
	sct.State().Metadata.LastUpdated = now
}

// Clear clears all configuration records (used during Reset)
func (sct *SensorConfigTracker) Clear() {
	sct.Lock()
	defer sct.Unlock()

	sct.State().Outputs.Configurations = make([]ThresholdConfiguration, 0)
	sct.State().Outputs.ConfiguredAt = time.Time{}
	sct.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (sct *SensorConfigTracker) GetState() *SensorConfigShadowState {
	sct.RLock()
	defer sct.RUnlock()

	s := sct.State()
	stateCopy := &SensorConfigShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: SensorConfigOutputs{
			Configurations: make([]ThresholdConfiguration, len(s.Outputs.Configurations)),
			ConfiguredAt:   s.Outputs.ConfiguredAt,
			LastUpdate:     s.Outputs.LastUpdate,
		},
		Metadata: s.Metadata,
	}

	for i, config := range s.Outputs.Configurations {
		stateCopy.Outputs.Configurations[i] = ThresholdConfiguration{
			ConfigType:         config.ConfigType,
			Description:        config.Description,
			Value:              config.Value,
			ConfiguredEntities: append([]string{}, config.ConfiguredEntities...),
			FailedEntities:     append([]string{}, config.FailedEntities...),
			ConfiguredAt:       config.ConfiguredAt,
		}
	}

	return stateCopy
}

// ============================================================================
// Infrastructure Tracker
// ============================================================================

// InfrastructureTracker manages shadow state for the infrastructure plugin
type InfrastructureTracker struct {
	ReadOnlyTracker[InfrastructureOutputs]
}

// NewInfrastructureTracker creates a new infrastructure shadow state tracker
func NewInfrastructureTracker() *InfrastructureTracker {
	return &InfrastructureTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewInfrastructureShadowState()),
	}
}

// UpdateSepticPower updates the current septic system power reading
func (it *InfrastructureTracker) UpdateSepticPower(powerW float64) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.SepticSystemStatus.CurrentPowerW = powerW
	it.State().Outputs.LastUpdate = time.Now()
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateSystemState updates the septic system state
func (it *InfrastructureTracker) UpdateSystemState(systemState string) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.SepticSystemStatus.SystemState = systemState
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateAeratorFailureStart tracks when low power condition started
func (it *InfrastructureTracker) UpdateAeratorFailureStart(startTime time.Time) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.SepticSystemStatus.AeratorFailureStart = startTime
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdatePumpRunningStart tracks when high power condition started
func (it *InfrastructureTracker) UpdatePumpRunningStart(startTime time.Time) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.SepticSystemStatus.PumpRunningStart = startTime
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateLastNormalPowerTime tracks when power was last in normal range
func (it *InfrastructureTracker) UpdateLastNormalPowerTime(t time.Time) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.SepticSystemStatus.LastNormalPowerTime = t
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateIsAlerting tracks whether an alert is currently active
func (it *InfrastructureTracker) UpdateIsAlerting(isAlerting bool) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.SepticSystemStatus.IsAlerting = isAlerting
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateActiveAlerts updates the list of active alerts
func (it *InfrastructureTracker) UpdateActiveAlerts(alerts []InfrastructureAlert) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.ActiveAlerts = make([]InfrastructureAlert, len(alerts))
	copy(it.State().Outputs.ActiveAlerts, alerts)
	it.State().Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (it *InfrastructureTracker) RecordNotification(alertType, message, priority string) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.LastNotification = &InfrastructureNotification{
		AlertType: alertType,
		Message:   message,
		Priority:  priority,
		Timestamp: time.Now(),
	}
	it.State().Metadata.LastUpdated = time.Now()
}

// RecordTTSAnnouncement records a TTS announcement that was made
func (it *InfrastructureTracker) RecordTTSAnnouncement(message string) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.LastTTSAnnouncement = &InfrastructureTTS{
		Message:   message,
		Timestamp: time.Now(),
	}
	it.State().Metadata.LastUpdated = time.Now()
}

// ClearAlerts clears all active alerts and resets alerting state
func (it *InfrastructureTracker) ClearAlerts() {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.ActiveAlerts = make([]InfrastructureAlert, 0)
	it.State().Outputs.SepticSystemStatus.IsAlerting = false
	it.State().Outputs.SepticSystemStatus.AeratorFailureStart = time.Time{}
	it.State().Outputs.SepticSystemStatus.PumpRunningStart = time.Time{}
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateWellThermostat updates the well thermostat state
func (it *InfrastructureTracker) UpdateWellThermostat(state ThermostatState) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.ThermostatStatus.WellThermostat = state
	it.State().Outputs.LastUpdate = time.Now()
	it.State().Metadata.LastUpdated = time.Now()
}

// UpdateBarnThermostat updates the barn thermostat state
func (it *InfrastructureTracker) UpdateBarnThermostat(state ThermostatState) {
	it.Lock()
	defer it.Unlock()

	it.State().Outputs.ThermostatStatus.BarnThermostat = state
	it.State().Outputs.LastUpdate = time.Now()
	it.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (it *InfrastructureTracker) GetState() *InfrastructureShadowState {
	it.RLock()
	defer it.RUnlock()

	s := it.State()
	stateCopy := &InfrastructureShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: InfrastructureOutputs{
			SepticSystemStatus: s.Outputs.SepticSystemStatus,
			ThermostatStatus:   s.Outputs.ThermostatStatus,
			ActiveAlerts:       make([]InfrastructureAlert, len(s.Outputs.ActiveAlerts)),
			LastUpdate:         s.Outputs.LastUpdate,
		},
		Metadata: s.Metadata,
	}

	copy(stateCopy.Outputs.ActiveAlerts, s.Outputs.ActiveAlerts)

	if s.Outputs.LastNotification != nil {
		notification := *s.Outputs.LastNotification
		stateCopy.Outputs.LastNotification = &notification
	}
	if s.Outputs.LastTTSAnnouncement != nil {
		tts := *s.Outputs.LastTTSAnnouncement
		stateCopy.Outputs.LastTTSAnnouncement = &tts
	}

	return stateCopy
}

// ============================================================================
// Water Flow Tracker
// ============================================================================

// WaterFlowTracker manages shadow state for the water flow monitoring plugin
type WaterFlowTracker struct {
	ReadOnlyTracker[WaterFlowOutputs]
}

// NewWaterFlowTracker creates a new water flow shadow state tracker
func NewWaterFlowTracker() *WaterFlowTracker {
	return &WaterFlowTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewWaterFlowShadowState()),
	}
}

// UpdateFlowRate updates the current flow rate reading
func (wt *WaterFlowTracker) UpdateFlowRate(flowRateGPM float64) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.CurrentFlowRateGPM = flowRateGPM
	wt.State().Outputs.LastUpdate = time.Now()
	wt.State().Metadata.LastUpdated = time.Now()
}

// UpdateAlertLevel updates the current alert level
func (wt *WaterFlowTracker) UpdateAlertLevel(level string) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.AlertLevel = level
	wt.State().Metadata.LastUpdated = time.Now()
}

// UpdateWarningThresholdStart tracks when warning threshold was exceeded
func (wt *WaterFlowTracker) UpdateWarningThresholdStart(startTime *time.Time) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.WarningThresholdStart = startTime
	wt.State().Metadata.LastUpdated = time.Now()
}

// UpdateUrgentThresholdStart tracks when urgent threshold was exceeded
func (wt *WaterFlowTracker) UpdateUrgentThresholdStart(startTime *time.Time) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.UrgentThresholdStart = startTime
	wt.State().Metadata.LastUpdated = time.Now()
}

// UpdateRecoveryStart tracks when recovery debounce started
func (wt *WaterFlowTracker) UpdateRecoveryStart(startTime *time.Time) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.RecoveryStart = startTime
	wt.State().Metadata.LastUpdated = time.Now()
}

// UpdateConditionsMet updates whether conditions are met for alerts
func (wt *WaterFlowTracker) UpdateConditionsMet(warningMet, urgentMet bool) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.IsWarningConditionMet = warningMet
	wt.State().Outputs.IsUrgentConditionMet = urgentMet
	wt.State().Metadata.LastUpdated = time.Now()
}

// UpdateActiveAlerts updates the list of active alerts
func (wt *WaterFlowTracker) UpdateActiveAlerts(alerts []WaterFlowAlert) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.ActiveAlerts = make([]WaterFlowAlert, len(alerts))
	copy(wt.State().Outputs.ActiveAlerts, alerts)
	wt.State().Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (wt *WaterFlowTracker) RecordNotification(alertType, message, priority string) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.LastNotification = &WaterFlowNotice{
		AlertType: alertType,
		Message:   message,
		Priority:  priority,
		Timestamp: time.Now(),
	}
	wt.State().Metadata.LastUpdated = time.Now()
}

// RecordRecoveryNotification records a recovery notification that was sent
func (wt *WaterFlowTracker) RecordRecoveryNotification(message string) {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.LastRecoveryNotification = &WaterFlowNotice{
		AlertType: "recovery",
		Message:   message,
		Priority:  "default",
		Timestamp: time.Now(),
	}
	wt.State().Metadata.LastUpdated = time.Now()
}

// RecordTTSAnnouncement records a TTS announcement that was made
func (wt *WaterFlowTracker) RecordTTSAnnouncement() {
	wt.Lock()
	defer wt.Unlock()

	now := time.Now()
	wt.State().Outputs.LastTTSAnnouncement = &now
	wt.State().Metadata.LastUpdated = now
}

// ClearAlerts clears all active alerts and resets alerting state
func (wt *WaterFlowTracker) ClearAlerts() {
	wt.Lock()
	defer wt.Unlock()

	wt.State().Outputs.AlertLevel = "none"
	wt.State().Outputs.ActiveAlerts = make([]WaterFlowAlert, 0)
	wt.State().Outputs.WarningThresholdStart = nil
	wt.State().Outputs.UrgentThresholdStart = nil
	wt.State().Outputs.IsWarningConditionMet = false
	wt.State().Outputs.IsUrgentConditionMet = false
	wt.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (wt *WaterFlowTracker) GetState() *WaterFlowShadowState {
	wt.RLock()
	defer wt.RUnlock()

	s := wt.State()
	stateCopy := &WaterFlowShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: WaterFlowOutputs{
			CurrentFlowRateGPM:    s.Outputs.CurrentFlowRateGPM,
			AlertLevel:            s.Outputs.AlertLevel,
			IsWarningConditionMet: s.Outputs.IsWarningConditionMet,
			IsUrgentConditionMet:  s.Outputs.IsUrgentConditionMet,
			ActiveAlerts:          make([]WaterFlowAlert, len(s.Outputs.ActiveAlerts)),
			LastUpdate:            s.Outputs.LastUpdate,
		},
		Metadata: s.Metadata,
	}

	if s.Outputs.WarningThresholdStart != nil {
		t := *s.Outputs.WarningThresholdStart
		stateCopy.Outputs.WarningThresholdStart = &t
	}
	if s.Outputs.UrgentThresholdStart != nil {
		t := *s.Outputs.UrgentThresholdStart
		stateCopy.Outputs.UrgentThresholdStart = &t
	}
	if s.Outputs.RecoveryStart != nil {
		t := *s.Outputs.RecoveryStart
		stateCopy.Outputs.RecoveryStart = &t
	}

	copy(stateCopy.Outputs.ActiveAlerts, s.Outputs.ActiveAlerts)

	if s.Outputs.LastNotification != nil {
		notification := *s.Outputs.LastNotification
		stateCopy.Outputs.LastNotification = &notification
	}
	if s.Outputs.LastRecoveryNotification != nil {
		recovery := *s.Outputs.LastRecoveryNotification
		stateCopy.Outputs.LastRecoveryNotification = &recovery
	}
	if s.Outputs.LastTTSAnnouncement != nil {
		tts := *s.Outputs.LastTTSAnnouncement
		stateCopy.Outputs.LastTTSAnnouncement = &tts
	}

	return stateCopy
}

// ============================================================================
// EV Charger Tracker
// ============================================================================

// EVChargerTracker manages shadow state for the EV charger safety plugin
type EVChargerTracker struct {
	ReadOnlyTracker[EVChargerOutputs]
}

// NewEVChargerTracker creates a new EV charger shadow state tracker
func NewEVChargerTracker() *EVChargerTracker {
	return &EVChargerTracker{
		ReadOnlyTracker: NewReadOnlyTracker(NewEVChargerShadowState()),
	}
}

// UpdateOverheatState updates the overheat sensor state
func (et *EVChargerTracker) UpdateOverheatState(isOverheat bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.IsOverheat = isOverheat
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateOverCurrentState updates the overcurrent sensor state
func (et *EVChargerTracker) UpdateOverCurrentState(isOverCurrent bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.IsOverCurrent = isOverCurrent
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateOverVoltageState updates the overvoltage sensor state
func (et *EVChargerTracker) UpdateOverVoltageState(isOverVoltage bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.IsOverVoltage = isOverVoltage
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdateSwitchState updates the switch state
func (et *EVChargerTracker) UpdateSwitchState(isOn bool) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.IsSwitchOn = isOn
	et.State().Metadata.LastUpdated = time.Now()
}

// UpdatePowerReading updates the current power reading
func (et *EVChargerTracker) UpdatePowerReading(power string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.PowerReading = power
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordSafetyEvent records a safety condition detection
func (et *EVChargerTracker) RecordSafetyEvent(conditionType, sensor string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastSafetyEvent = &EVChargerSafetyEvent{
		ConditionType: conditionType,
		Sensor:        sensor,
		Timestamp:     time.Now(),
	}
	et.State().Outputs.SafetyEventCount++
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordShutoff records an emergency shutoff
func (et *EVChargerTracker) RecordShutoff(reason string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastShutoff = &EVChargerShutoff{
		Reason:    reason,
		Timestamp: time.Now(),
	}
	et.State().Outputs.ShutoffCount++
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (et *EVChargerTracker) RecordNotification(conditionType, message string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastNotification = &EVChargerNotice{
		ConditionType: conditionType,
		Message:       message,
		Timestamp:     time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// RecordTTSAnnouncement records a TTS announcement that was made
func (et *EVChargerTracker) RecordTTSAnnouncement() {
	et.Lock()
	defer et.Unlock()

	now := time.Now()
	et.State().Outputs.LastTTSAnnouncement = &now
	et.State().Metadata.LastUpdated = now
}

// RecordRecovery records a recovery from a safety condition
func (et *EVChargerTracker) RecordRecovery(conditionType string) {
	et.Lock()
	defer et.Unlock()

	et.State().Outputs.LastRecovery = &EVChargerRecovery{
		ConditionType: conditionType,
		Timestamp:     time.Now(),
	}
	et.State().Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (et *EVChargerTracker) GetState() *EVChargerShadowState {
	et.RLock()
	defer et.RUnlock()

	s := et.State()
	stateCopy := &EVChargerShadowState{
		Plugin: s.Plugin,
		Inputs: ReadOnlyInputs{
			Current: copyInputMap(s.Inputs.Current),
		},
		Outputs: EVChargerOutputs{
			IsOverheat:       s.Outputs.IsOverheat,
			IsOverCurrent:    s.Outputs.IsOverCurrent,
			IsOverVoltage:    s.Outputs.IsOverVoltage,
			IsSwitchOn:       s.Outputs.IsSwitchOn,
			PowerReading:     s.Outputs.PowerReading,
			SafetyEventCount: s.Outputs.SafetyEventCount,
			ShutoffCount:     s.Outputs.ShutoffCount,
		},
		Metadata: s.Metadata,
	}

	if s.Outputs.LastSafetyEvent != nil {
		event := *s.Outputs.LastSafetyEvent
		stateCopy.Outputs.LastSafetyEvent = &event
	}
	if s.Outputs.LastShutoff != nil {
		shutoff := *s.Outputs.LastShutoff
		stateCopy.Outputs.LastShutoff = &shutoff
	}
	if s.Outputs.LastNotification != nil {
		notice := *s.Outputs.LastNotification
		stateCopy.Outputs.LastNotification = &notice
	}
	if s.Outputs.LastTTSAnnouncement != nil {
		tts := *s.Outputs.LastTTSAnnouncement
		stateCopy.Outputs.LastTTSAnnouncement = &tts
	}
	if s.Outputs.LastRecovery != nil {
		recovery := *s.Outputs.LastRecovery
		stateCopy.Outputs.LastRecovery = &recovery
	}

	return stateCopy
}
