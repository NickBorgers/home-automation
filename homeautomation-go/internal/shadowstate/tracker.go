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

// LightingTracker manages shadow state specifically for the lighting plugin
type LightingTracker struct {
	mu    sync.RWMutex
	state *LightingShadowState
}

// NewLightingTracker creates a new lighting shadow state tracker
func NewLightingTracker() *LightingTracker {
	return &LightingTracker{
		state: NewLightingShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (lt *LightingTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	for key, value := range inputs {
		lt.state.Inputs.Current[key] = value
	}
	lt.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot
func (lt *LightingTracker) SnapshotInputsForAction() {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	// Deep copy current inputs to at-last-action
	lt.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range lt.state.Inputs.Current {
		lt.state.Inputs.AtLastAction[key] = value
	}
}

// RecordRoomAction records an action taken on a room
func (lt *LightingTracker) RecordRoomAction(roomName string, actionType string, reason string, activeScene string, turnedOff bool) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	now := time.Now()
	lt.state.Outputs.Rooms[roomName] = RoomState{
		ActiveScene: activeScene,
		TurnedOff:   turnedOff,
		LastAction:  now,
		ActionType:  actionType,
		Reason:      reason,
	}
	lt.state.Outputs.LastActionTime = now
	lt.state.Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (lt *LightingTracker) GetState() *LightingShadowState {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	stateCopy := &LightingShadowState{
		Plugin: lt.state.Plugin,
		Inputs: LightingInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: LightingOutputs{
			Rooms:          make(map[string]RoomState),
			LastActionTime: lt.state.Outputs.LastActionTime,
		},
		Metadata: lt.state.Metadata,
	}

	// Copy current inputs
	for k, v := range lt.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy at-last-action inputs
	for k, v := range lt.state.Inputs.AtLastAction {
		stateCopy.Inputs.AtLastAction[k] = v
	}

	// Copy room states
	for k, v := range lt.state.Outputs.Rooms {
		stateCopy.Outputs.Rooms[k] = v
	}

	return stateCopy
}

// SecurityTracker manages shadow state specifically for the security plugin
type SecurityTracker struct {
	mu    sync.RWMutex
	state *SecurityShadowState
}

// NewSecurityTracker creates a new security shadow state tracker
func NewSecurityTracker() *SecurityTracker {
	return &SecurityTracker{
		state: NewSecurityShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (st *SecurityTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	st.mu.Lock()
	defer st.mu.Unlock()

	for key, value := range inputs {
		st.state.Inputs.Current[key] = value
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot
func (st *SecurityTracker) SnapshotInputsForAction() {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Deep copy current inputs to at-last-action
	st.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range st.state.Inputs.Current {
		st.state.Inputs.AtLastAction[key] = value
	}
}

// RecordLockdownAction records a lockdown activation or deactivation
func (st *SecurityTracker) RecordLockdownAction(active bool, reason string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.state.Outputs.Lockdown.Active = active
	st.state.Outputs.Lockdown.Reason = reason

	if active {
		st.state.Outputs.Lockdown.ActivatedAt = now
		st.state.Outputs.Lockdown.WillResetAt = now.Add(5 * time.Second)
	} else {
		st.state.Outputs.Lockdown.ActivatedAt = time.Time{}
		st.state.Outputs.Lockdown.WillResetAt = time.Time{}
	}

	st.state.Outputs.LastActionTime = now
	st.state.Metadata.LastUpdated = now
}

// RecordDoorbellEvent records a doorbell press event
func (st *SecurityTracker) RecordDoorbellEvent(rateLimited bool, ttsSent bool, lightsFlashed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.state.Outputs.LastDoorbell = &DoorbellEvent{
		Timestamp:     now,
		RateLimited:   rateLimited,
		TTSSent:       ttsSent,
		LightsFlashed: lightsFlashed,
	}
	st.state.Outputs.LastActionTime = now
	st.state.Metadata.LastUpdated = now
}

// RecordVehicleArrivalEvent records a vehicle arrival event
func (st *SecurityTracker) RecordVehicleArrivalEvent(rateLimited bool, ttsSent bool, wasExpecting bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.state.Outputs.LastVehicle = &VehicleArrivalEvent{
		Timestamp:    now,
		RateLimited:  rateLimited,
		TTSSent:      ttsSent,
		WasExpecting: wasExpecting,
	}
	st.state.Outputs.LastActionTime = now
	st.state.Metadata.LastUpdated = now
}

// RecordGarageOpenEvent records a garage auto-open event
func (st *SecurityTracker) RecordGarageOpenEvent(reason string, garageWasEmpty bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.state.Outputs.LastGarageOpen = &GarageOpenEvent{
		Timestamp:      now,
		Reason:         reason,
		GarageWasEmpty: garageWasEmpty,
	}
	st.state.Outputs.LastActionTime = now
	st.state.Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (st *SecurityTracker) GetState() *SecurityShadowState {
	st.mu.RLock()
	defer st.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	stateCopy := &SecurityShadowState{
		Plugin: st.state.Plugin,
		Inputs: SecurityInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: SecurityOutputs{
			Lockdown:       st.state.Outputs.Lockdown,
			LastDoorbell:   st.state.Outputs.LastDoorbell,
			LastVehicle:    st.state.Outputs.LastVehicle,
			LastGarageOpen: st.state.Outputs.LastGarageOpen,
			LastActionTime: st.state.Outputs.LastActionTime,
		},
		Metadata: st.state.Metadata,
	}

	// Copy current inputs
	for k, v := range st.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy at-last-action inputs
	for k, v := range st.state.Inputs.AtLastAction {
		stateCopy.Inputs.AtLastAction[k] = v
	}

	return stateCopy
}

// LoadSheddingTracker manages shadow state specifically for the load shedding plugin
type LoadSheddingTracker struct {
	mu    sync.RWMutex
	state *LoadSheddingShadowState
}

// NewLoadSheddingTracker creates a new load shedding shadow state tracker
func NewLoadSheddingTracker() *LoadSheddingTracker {
	return &LoadSheddingTracker{
		state: NewLoadSheddingShadowState(),
	}
}

// SleepHygieneTracker manages shadow state specifically for the sleep hygiene plugin
type SleepHygieneTracker struct {
	mu    sync.RWMutex
	state *SleepHygieneShadowState
}

// NewSleepHygieneTracker creates a new sleep hygiene shadow state tracker
func NewSleepHygieneTracker() *SleepHygieneTracker {
	return &SleepHygieneTracker{
		state: NewSleepHygieneShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (lst *LoadSheddingTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	lst.mu.Lock()
	defer lst.mu.Unlock()

	for key, value := range inputs {
		lst.state.Inputs.Current[key] = value
	}
	lst.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot
func (lst *LoadSheddingTracker) SnapshotInputsForAction() {
	lst.mu.Lock()
	defer lst.mu.Unlock()

	// Deep copy current inputs to at-last-action
	lst.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range lst.state.Inputs.Current {
		lst.state.Inputs.AtLastAction[key] = value
	}
}

// RecordLoadSheddingAction records a load shedding activation or deactivation
func (lst *LoadSheddingTracker) RecordLoadSheddingAction(active bool, actionType string, reason string, thermostatSettings ThermostatSettings) {
	lst.mu.Lock()
	defer lst.mu.Unlock()

	now := time.Now()
	lst.state.Outputs.Active = active
	lst.state.Outputs.LastActionType = actionType
	lst.state.Outputs.LastActionReason = reason
	lst.state.Outputs.ThermostatSettings = thermostatSettings
	lst.state.Outputs.LastActionTime = now
	lst.state.Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (lst *LoadSheddingTracker) GetState() *LoadSheddingShadowState {
	lst.mu.RLock()
	defer lst.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	stateCopy := &LoadSheddingShadowState{
		Plugin: lst.state.Plugin,
		Inputs: LoadSheddingInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: LoadSheddingOutputs{
			Active:             lst.state.Outputs.Active,
			LastActionType:     lst.state.Outputs.LastActionType,
			LastActionReason:   lst.state.Outputs.LastActionReason,
			ThermostatSettings: lst.state.Outputs.ThermostatSettings,
			LastActionTime:     lst.state.Outputs.LastActionTime,
		},
		Metadata: lst.state.Metadata,
	}

	// Copy current inputs
	for k, v := range lst.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy at-last-action inputs
	for k, v := range lst.state.Inputs.AtLastAction {
		stateCopy.Inputs.AtLastAction[k] = v
	}

	return stateCopy
}

// SleepHygieneTracker methods start here

// UpdateCurrentInputs updates the current input values
func (st *SleepHygieneTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	st.mu.Lock()
	defer st.mu.Unlock()

	for key, value := range inputs {
		st.state.Inputs.Current[key] = value
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot
func (st *SleepHygieneTracker) SnapshotInputsForAction() {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Deep copy current inputs to at-last-action
	st.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range st.state.Inputs.Current {
		st.state.Inputs.AtLastAction[key] = value
	}
}

// RecordAction records a sleep hygiene action
func (st *SleepHygieneTracker) RecordAction(actionType string, reason string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.state.Outputs.LastActionTime = now
	st.state.Outputs.LastActionType = actionType
	st.state.Outputs.LastActionReason = reason
	st.state.Metadata.LastUpdated = now
}

// UpdateWakeSequenceStatus updates the wake sequence status
func (st *SleepHygieneTracker) UpdateWakeSequenceStatus(status string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.WakeSequenceStatus = status
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordFadeOutStart records the start of a speaker fade-out
func (st *SleepHygieneTracker) RecordFadeOutStart(speakerEntityID string, startVolume int) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	st.state.Outputs.FadeOutProgress[speakerEntityID] = SpeakerFadeOut{
		SpeakerEntityID: speakerEntityID,
		CurrentVolume:   startVolume,
		StartVolume:     startVolume,
		IsActive:        true,
		StartTime:       now,
		LastUpdate:      now,
	}
	st.state.Metadata.LastUpdated = now
}

// UpdateFadeOutProgress updates the fade-out progress for a speaker
func (st *SleepHygieneTracker) UpdateFadeOutProgress(speakerEntityID string, currentVolume int) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if fadeOut, exists := st.state.Outputs.FadeOutProgress[speakerEntityID]; exists {
		fadeOut.CurrentVolume = currentVolume
		fadeOut.LastUpdate = time.Now()
		if currentVolume == 0 {
			fadeOut.IsActive = false
		}
		st.state.Outputs.FadeOutProgress[speakerEntityID] = fadeOut
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordHumanOverride records that a human override was detected during fade-out
func (st *SleepHygieneTracker) RecordHumanOverride(speakerEntityID string, expectedVolume, actualVolume int) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if fadeOut, exists := st.state.Outputs.FadeOutProgress[speakerEntityID]; exists {
		fadeOut.HumanOverrideDetected = true
		fadeOut.ExpectedVolume = expectedVolume
		fadeOut.ActualVolume = actualVolume
		fadeOut.IsActive = false
		fadeOut.LastUpdate = time.Now()
		st.state.Outputs.FadeOutProgress[speakerEntityID] = fadeOut
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// ClearFadeOutProgress clears all fade-out progress
func (st *SleepHygieneTracker) ClearFadeOutProgress() {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.FadeOutProgress = make(map[string]SpeakerFadeOut)
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordTTSAnnouncement records a TTS announcement
func (st *SleepHygieneTracker) RecordTTSAnnouncement(message string, speaker string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.LastTTSAnnouncement = &TTSAnnouncement{
		Message:   message,
		Speaker:   speaker,
		Timestamp: time.Now(),
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordStopScreensReminder records a stop screens reminder trigger
func (st *SleepHygieneTracker) RecordStopScreensReminder() {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.StopScreensReminder = &ReminderTrigger{
		Triggered: true,
		Timestamp: time.Now(),
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordGoToBedReminder records a go to bed reminder trigger
func (st *SleepHygieneTracker) RecordGoToBedReminder() {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.GoToBedReminder = &ReminderTrigger{
		Triggered: true,
		Timestamp: time.Now(),
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// UpdateEightSleepAvailability updates the Eight Sleep availability status
func (st *SleepHygieneTracker) UpdateEightSleepAvailability(available bool, checkTime time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.EightSleepAvailable = available
	st.state.Outputs.BackupWakeEnabled = !available
	st.state.Outputs.LastAvailabilityCheck = checkTime
	st.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (st *SleepHygieneTracker) GetState() *SleepHygieneShadowState {
	st.mu.RLock()
	defer st.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	stateCopy := &SleepHygieneShadowState{
		Plugin: st.state.Plugin,
		Inputs: SleepHygieneInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: SleepHygieneOutputs{
			WakeSequenceStatus:    st.state.Outputs.WakeSequenceStatus,
			FadeOutProgress:       make(map[string]SpeakerFadeOut),
			EightSleepAvailable:   st.state.Outputs.EightSleepAvailable,
			BackupWakeEnabled:     st.state.Outputs.BackupWakeEnabled,
			LastAvailabilityCheck: st.state.Outputs.LastAvailabilityCheck,
			LastActionTime:        st.state.Outputs.LastActionTime,
			LastActionType:        st.state.Outputs.LastActionType,
			LastActionReason:      st.state.Outputs.LastActionReason,
		},
		Metadata: st.state.Metadata,
	}

	// Copy current inputs
	for k, v := range st.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy at-last-action inputs
	for k, v := range st.state.Inputs.AtLastAction {
		stateCopy.Inputs.AtLastAction[k] = v
	}

	// Copy fade out progress
	for k, v := range st.state.Outputs.FadeOutProgress {
		stateCopy.Outputs.FadeOutProgress[k] = v
	}

	// Copy TTS announcement if it exists
	if st.state.Outputs.LastTTSAnnouncement != nil {
		announcement := *st.state.Outputs.LastTTSAnnouncement
		stateCopy.Outputs.LastTTSAnnouncement = &announcement
	}

	// Copy stop screens reminder if it exists
	if st.state.Outputs.StopScreensReminder != nil {
		reminder := *st.state.Outputs.StopScreensReminder
		stateCopy.Outputs.StopScreensReminder = &reminder
	}

	// Copy go to bed reminder if it exists
	if st.state.Outputs.GoToBedReminder != nil {
		reminder := *st.state.Outputs.GoToBedReminder
		stateCopy.Outputs.GoToBedReminder = &reminder
	}

	return stateCopy
}

// ============================================================================
// Phase 6: Read-Heavy Plugin Trackers
// ============================================================================

// EnergyTracker manages shadow state for the energy plugin
type EnergyTracker struct {
	mu    sync.RWMutex
	state *EnergyShadowState
}

// NewEnergyTracker creates a new energy shadow state tracker
func NewEnergyTracker() *EnergyTracker {
	return &EnergyTracker{
		state: NewEnergyShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (et *EnergyTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	et.mu.Lock()
	defer et.mu.Unlock()

	for key, value := range inputs {
		et.state.Inputs.Current[key] = value
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateSensorReadings updates the raw sensor readings
func (et *EnergyTracker) UpdateSensorReadings(batteryPct, thisHourKW, remainingKWH float64, gridAvailable bool) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.SensorReadings.BatteryPercentage = batteryPct
	et.state.Outputs.SensorReadings.ThisHourSolarGenerationKW = thisHourKW
	et.state.Outputs.SensorReadings.RemainingSolarGenerationKWH = remainingKWH
	et.state.Outputs.SensorReadings.IsGridAvailable = gridAvailable
	et.state.Outputs.SensorReadings.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateBatteryPercentage updates the battery percentage sensor reading
func (et *EnergyTracker) UpdateBatteryPercentage(pct float64) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.SensorReadings.BatteryPercentage = pct
	et.state.Outputs.SensorReadings.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateThisHourSolarKW updates the this-hour solar generation sensor reading
func (et *EnergyTracker) UpdateThisHourSolarKW(kw float64) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.SensorReadings.ThisHourSolarGenerationKW = kw
	et.state.Outputs.SensorReadings.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateRemainingSolarKWH updates the remaining solar generation sensor reading
func (et *EnergyTracker) UpdateRemainingSolarKWH(kwh float64) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.SensorReadings.RemainingSolarGenerationKWH = kwh
	et.state.Outputs.SensorReadings.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateGridAvailable updates the grid availability sensor reading
func (et *EnergyTracker) UpdateGridAvailable(available bool) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.SensorReadings.IsGridAvailable = available
	et.state.Outputs.SensorReadings.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateBatteryLevel updates the computed battery energy level
func (et *EnergyTracker) UpdateBatteryLevel(level string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.BatteryEnergyLevel = level
	et.state.Outputs.LastComputations.LastBatteryLevelCalc = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateSolarLevel updates the computed solar production energy level
func (et *EnergyTracker) UpdateSolarLevel(level string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.SolarProductionEnergyLevel = level
	et.state.Outputs.LastComputations.LastSolarLevelCalc = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateOverallLevel updates the computed overall energy level
func (et *EnergyTracker) UpdateOverallLevel(level string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.CurrentEnergyLevel = level
	et.state.Outputs.LastComputations.LastOverallLevelCalc = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateFreeEnergyAvailable updates the free energy availability status
func (et *EnergyTracker) UpdateFreeEnergyAvailable(available bool) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.IsFreeEnergyAvailable = available
	et.state.Outputs.LastComputations.LastFreeEnergyCheck = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateDiscoveredIndicatorLights updates the list of discovered indicator light entities
func (et *EnergyTracker) UpdateDiscoveredIndicatorLights(entities []string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.DiscoveredIndicatorLights = entities
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateIndicatorLightsAction updates the last indicator lights action
func (et *EnergyTracker) UpdateIndicatorLightsAction(energyLevel string, rgbColor []int, brightnessPct int, entityIDs []string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.IndicatorLightsAction = &IndicatorLightsAction{
		EnergyLevel:   energyLevel,
		RGBColor:      rgbColor,
		BrightnessPct: brightnessPct,
		EntityIDs:     entityIDs,
		Timestamp:     time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateLuxReading updates a single lux sensor reading
func (et *EnergyTracker) UpdateLuxReading(sensorEntity string, lux float64) {
	et.mu.Lock()
	defer et.mu.Unlock()

	if et.state.Outputs.LuxSensorReadings == nil {
		et.state.Outputs.LuxSensorReadings = make(map[string]LuxSensorReading)
	}

	et.state.Outputs.LuxSensorReadings[sensorEntity] = LuxSensorReading{
		EntityID:  sensorEntity,
		Lux:       lux,
		Timestamp: time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateLightToLuxMapping updates the light-to-lux sensor mapping
func (et *EnergyTracker) UpdateLightToLuxMapping(mapping map[string]string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	// Copy the mapping
	et.state.Outputs.LightToLuxSensorMapping = make(map[string]string)
	for k, v := range mapping {
		et.state.Outputs.LightToLuxSensorMapping[k] = v
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdatePerDeviceBrightness updates the brightness info for a single device
func (et *EnergyTracker) UpdatePerDeviceBrightness(lightEntity, luxEntity string, lux float64, brightness int, isAdaptive bool) {
	et.mu.Lock()
	defer et.mu.Unlock()

	if et.state.Outputs.PerDeviceBrightness == nil {
		et.state.Outputs.PerDeviceBrightness = make(map[string]PerDeviceBrightness)
	}

	et.state.Outputs.PerDeviceBrightness[lightEntity] = PerDeviceBrightness{
		LightEntity:     lightEntity,
		LuxSensorEntity: luxEntity,
		CurrentLux:      lux,
		BrightnessPct:   brightness,
		IsAdaptive:      isAdaptive,
		LastUpdate:      time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateBaselineLux updates the baseline lux calibration for a single device.
// Baseline lux is the true ambient light level measured when the LED is dimmed.
func (et *EnergyTracker) UpdateBaselineLux(lightEntity string, baselineLux float64) {
	et.mu.Lock()
	defer et.mu.Unlock()

	if et.state.Outputs.BaselineCalibrations == nil {
		et.state.Outputs.BaselineCalibrations = make(map[string]BaselineCalibration)
	}

	et.state.Outputs.BaselineCalibrations[lightEntity] = BaselineCalibration{
		LightEntity:         lightEntity,
		BaselineLux:         baselineLux,
		LastCalibrationTime: time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (et *EnergyTracker) GetState() *EnergyShadowState {
	et.mu.RLock()
	defer et.mu.RUnlock()

	// Create a deep copy
	stateCopy := &EnergyShadowState{
		Plugin: et.state.Plugin,
		Inputs: EnergyInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: EnergyOutputs{
			BatteryEnergyLevel:         et.state.Outputs.BatteryEnergyLevel,
			SolarProductionEnergyLevel: et.state.Outputs.SolarProductionEnergyLevel,
			CurrentEnergyLevel:         et.state.Outputs.CurrentEnergyLevel,
			IsFreeEnergyAvailable:      et.state.Outputs.IsFreeEnergyAvailable,
			LastComputations:           et.state.Outputs.LastComputations,
			SensorReadings:             et.state.Outputs.SensorReadings,
			DiscoveredIndicatorLights:  et.state.Outputs.DiscoveredIndicatorLights,
			IndicatorLightsAction:      et.state.Outputs.IndicatorLightsAction,
		},
		Metadata: et.state.Metadata,
	}

	// Copy current inputs
	for k, v := range et.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy lux sensor readings
	if et.state.Outputs.LuxSensorReadings != nil {
		stateCopy.Outputs.LuxSensorReadings = make(map[string]LuxSensorReading)
		for k, v := range et.state.Outputs.LuxSensorReadings {
			stateCopy.Outputs.LuxSensorReadings[k] = v
		}
	}

	// Copy per-device brightness
	if et.state.Outputs.PerDeviceBrightness != nil {
		stateCopy.Outputs.PerDeviceBrightness = make(map[string]PerDeviceBrightness)
		for k, v := range et.state.Outputs.PerDeviceBrightness {
			stateCopy.Outputs.PerDeviceBrightness[k] = v
		}
	}

	// Copy light-to-lux sensor mapping
	if et.state.Outputs.LightToLuxSensorMapping != nil {
		stateCopy.Outputs.LightToLuxSensorMapping = make(map[string]string)
		for k, v := range et.state.Outputs.LightToLuxSensorMapping {
			stateCopy.Outputs.LightToLuxSensorMapping[k] = v
		}
	}

	// Copy baseline calibrations
	if et.state.Outputs.BaselineCalibrations != nil {
		stateCopy.Outputs.BaselineCalibrations = make(map[string]BaselineCalibration)
		for k, v := range et.state.Outputs.BaselineCalibrations {
			stateCopy.Outputs.BaselineCalibrations[k] = v
		}
	}

	return stateCopy
}

// StateTrackingTracker manages shadow state for the state tracking plugin
type StateTrackingTracker struct {
	mu    sync.RWMutex
	state *StateTrackingShadowState
}

// NewStateTrackingTracker creates a new state tracking shadow state tracker
func NewStateTrackingTracker() *StateTrackingTracker {
	return &StateTrackingTracker{
		state: NewStateTrackingShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (stt *StateTrackingTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	stt.mu.Lock()
	defer stt.mu.Unlock()

	for key, value := range inputs {
		stt.state.Inputs.Current[key] = value
	}
	stt.state.Metadata.LastUpdated = time.Now()
}

// UpdateDerivedStates updates the computed derived states
func (stt *StateTrackingTracker) UpdateDerivedStates(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep bool) {
	stt.mu.Lock()
	defer stt.mu.Unlock()

	stt.state.Outputs.DerivedStates.IsAnyOwnerHome = anyOwnerHome
	stt.state.Outputs.DerivedStates.IsAnyoneHome = anyoneHome
	stt.state.Outputs.DerivedStates.IsAnyoneAsleep = anyoneAsleep
	stt.state.Outputs.DerivedStates.IsEveryoneAsleep = everyoneAsleep
	stt.state.Outputs.LastComputation = time.Now()
	stt.state.Metadata.LastUpdated = time.Now()
}

// UpdateSleepDetectionTimer updates the sleep detection timer state
func (stt *StateTrackingTracker) UpdateSleepDetectionTimer(active bool) {
	stt.mu.Lock()
	defer stt.mu.Unlock()

	stt.state.Outputs.TimerStates.SleepDetectionActive = active
	if active {
		stt.state.Outputs.TimerStates.SleepDetectionStarted = time.Now()
	} else {
		stt.state.Outputs.TimerStates.SleepDetectionStarted = time.Time{}
	}
	stt.state.Metadata.LastUpdated = time.Now()
}

// UpdateWakeDetectionTimer updates the wake detection timer state
func (stt *StateTrackingTracker) UpdateWakeDetectionTimer(active bool) {
	stt.mu.Lock()
	defer stt.mu.Unlock()

	stt.state.Outputs.TimerStates.WakeDetectionActive = active
	if active {
		stt.state.Outputs.TimerStates.WakeDetectionStarted = time.Now()
	} else {
		stt.state.Outputs.TimerStates.WakeDetectionStarted = time.Time{}
	}
	stt.state.Metadata.LastUpdated = time.Now()
}

// UpdateOwnerReturnTimer updates the owner return home auto-reset timer state
func (stt *StateTrackingTracker) UpdateOwnerReturnTimer(active bool) {
	stt.mu.Lock()
	defer stt.mu.Unlock()

	stt.state.Outputs.TimerStates.OwnerReturnResetActive = active
	if active {
		stt.state.Outputs.TimerStates.OwnerReturnResetStarted = time.Now()
	} else {
		stt.state.Outputs.TimerStates.OwnerReturnResetStarted = time.Time{}
	}
	stt.state.Metadata.LastUpdated = time.Now()
}

// RecordArrivalAnnouncement records an arrival TTS announcement
func (stt *StateTrackingTracker) RecordArrivalAnnouncement(person, message string) {
	stt.mu.Lock()
	defer stt.mu.Unlock()

	stt.state.Outputs.LastAnnouncement = &ArrivalAnnouncement{
		Person:    person,
		Message:   message,
		Timestamp: time.Now(),
	}
	stt.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (stt *StateTrackingTracker) GetState() *StateTrackingShadowState {
	stt.mu.RLock()
	defer stt.mu.RUnlock()

	// Create a deep copy
	stateCopy := &StateTrackingShadowState{
		Plugin: stt.state.Plugin,
		Inputs: StateTrackingInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: StateTrackingOutputs{
			DerivedStates:   stt.state.Outputs.DerivedStates,
			TimerStates:     stt.state.Outputs.TimerStates,
			LastComputation: stt.state.Outputs.LastComputation,
		},
		Metadata: stt.state.Metadata,
	}

	// Copy current inputs
	for k, v := range stt.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy announcement if exists
	if stt.state.Outputs.LastAnnouncement != nil {
		announcement := *stt.state.Outputs.LastAnnouncement
		stateCopy.Outputs.LastAnnouncement = &announcement
	}

	return stateCopy
}

// DayPhaseTracker manages shadow state for the day phase plugin
type DayPhaseTracker struct {
	mu    sync.RWMutex
	state *DayPhaseShadowState
}

// NewDayPhaseTracker creates a new day phase shadow state tracker
func NewDayPhaseTracker() *DayPhaseTracker {
	return &DayPhaseTracker{
		state: NewDayPhaseShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (dpt *DayPhaseTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	dpt.mu.Lock()
	defer dpt.mu.Unlock()

	for key, value := range inputs {
		dpt.state.Inputs.Current[key] = value
	}
	dpt.state.Metadata.LastUpdated = time.Now()
}

// UpdateSunEvent updates the computed sun event
func (dpt *DayPhaseTracker) UpdateSunEvent(sunEvent string) {
	dpt.mu.Lock()
	defer dpt.mu.Unlock()

	dpt.state.Outputs.SunEvent = sunEvent
	dpt.state.Outputs.LastSunEventCalc = time.Now()
	dpt.state.Metadata.LastUpdated = time.Now()
}

// UpdateDayPhase updates the computed day phase
func (dpt *DayPhaseTracker) UpdateDayPhase(dayPhase string) {
	dpt.mu.Lock()
	defer dpt.mu.Unlock()

	dpt.state.Outputs.DayPhase = dayPhase
	dpt.state.Outputs.LastDayPhaseCalc = time.Now()
	dpt.state.Metadata.LastUpdated = time.Now()
}

// UpdateNextTransition updates the next expected phase transition
func (dpt *DayPhaseTracker) UpdateNextTransition(transitionTime time.Time, nextPhase string) {
	dpt.mu.Lock()
	defer dpt.mu.Unlock()

	dpt.state.Outputs.NextTransitionTime = transitionTime
	dpt.state.Outputs.NextTransitionPhase = nextPhase
	dpt.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (dpt *DayPhaseTracker) GetState() *DayPhaseShadowState {
	dpt.mu.RLock()
	defer dpt.mu.RUnlock()

	// Create a deep copy
	stateCopy := &DayPhaseShadowState{
		Plugin: dpt.state.Plugin,
		Inputs: DayPhaseInputs{
			Current: make(map[string]interface{}),
		},
		Outputs:  dpt.state.Outputs,
		Metadata: dpt.state.Metadata,
	}

	// Copy current inputs
	for k, v := range dpt.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	return stateCopy
}

// TVTracker manages shadow state for the TV plugin
type TVTracker struct {
	mu    sync.RWMutex
	state *TVShadowState
}

// SexModeTracker manages shadow state for the sex mode plugin
type SexModeTracker struct {
	mu    sync.RWMutex
	state *SexModeShadowState
}

// NewSexModeTracker creates a new sex mode shadow state tracker
func NewSexModeTracker() *SexModeTracker {
	return &SexModeTracker{
		state: NewSexModeShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (smt *SexModeTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	smt.mu.Lock()
	defer smt.mu.Unlock()

	for key, value := range inputs {
		smt.state.Inputs.Current[key] = value
	}
	smt.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot
func (smt *SexModeTracker) SnapshotInputsForAction() {
	smt.mu.Lock()
	defer smt.mu.Unlock()

	// Deep copy current inputs to at-last-action
	smt.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range smt.state.Inputs.Current {
		smt.state.Inputs.AtLastAction[key] = value
	}
}

// RecordAction records a sex mode activation or deactivation
func (smt *SexModeTracker) RecordAction(actionType string, reason string, isActive bool, preSexMusicType string, activatedAt time.Time) {
	smt.mu.Lock()
	defer smt.mu.Unlock()

	now := time.Now()
	smt.state.Outputs.IsActive = isActive
	smt.state.Outputs.PreSexMusicType = preSexMusicType
	smt.state.Outputs.ActivatedAt = activatedAt
	smt.state.Outputs.LastActionType = actionType
	smt.state.Outputs.LastActionReason = reason
	smt.state.Outputs.LastActionTime = now
	smt.state.Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (smt *SexModeTracker) GetState() *SexModeShadowState {
	smt.mu.RLock()
	defer smt.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	stateCopy := &SexModeShadowState{
		Plugin: smt.state.Plugin,
		Inputs: SexModeInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: SexModeOutputs{
			IsActive:         smt.state.Outputs.IsActive,
			PreSexMusicType:  smt.state.Outputs.PreSexMusicType,
			ActivatedAt:      smt.state.Outputs.ActivatedAt,
			LastActionTime:   smt.state.Outputs.LastActionTime,
			LastActionType:   smt.state.Outputs.LastActionType,
			LastActionReason: smt.state.Outputs.LastActionReason,
		},
		Metadata: smt.state.Metadata,
	}

	// Copy current inputs
	for k, v := range smt.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy at-last-action inputs
	for k, v := range smt.state.Inputs.AtLastAction {
		stateCopy.Inputs.AtLastAction[k] = v
	}

	return stateCopy
}

// ChristmasTracker manages shadow state for the christmas plugin
type ChristmasTracker struct {
	mu    sync.RWMutex
	state *ChristmasShadowState
}

// NewChristmasTracker creates a new christmas shadow state tracker
func NewChristmasTracker() *ChristmasTracker {
	return &ChristmasTracker{
		state: NewChristmasShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (ct *ChristmasTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	for key, value := range inputs {
		ct.state.Inputs.Current[key] = value
	}
	ct.state.Metadata.LastUpdated = time.Now()
}

// SnapshotInputsForAction captures current inputs as the at-last-action snapshot
func (ct *ChristmasTracker) SnapshotInputsForAction() {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	// Deep copy current inputs to at-last-action
	ct.state.Inputs.AtLastAction = make(map[string]interface{})
	for key, value := range ct.state.Inputs.Current {
		ct.state.Inputs.AtLastAction[key] = value
	}
}

// RecordActivation records a christmas lights activation
func (ct *ChristmasTracker) RecordActivation(lightsActivated int, reason string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()
	ct.state.Outputs.LastActivationTime = now
	ct.state.Outputs.LightsActivated = lightsActivated
	ct.state.Outputs.LastActionReason = reason
	ct.state.Metadata.LastUpdated = now
}

// GetState returns the current shadow state (thread-safe copy)
func (ct *ChristmasTracker) GetState() *ChristmasShadowState {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	stateCopy := &ChristmasShadowState{
		Plugin: ct.state.Plugin,
		Inputs: ChristmasInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: ChristmasOutputs{
			LastActivationTime: ct.state.Outputs.LastActivationTime,
			LightsActivated:    ct.state.Outputs.LightsActivated,
			LastActionReason:   ct.state.Outputs.LastActionReason,
		},
		Metadata: ct.state.Metadata,
	}

	// Copy current inputs
	for k, v := range ct.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy at-last-action inputs
	for k, v := range ct.state.Inputs.AtLastAction {
		stateCopy.Inputs.AtLastAction[k] = v
	}

	return stateCopy
}

// NewTVTracker creates a new TV shadow state tracker
func NewTVTracker() *TVTracker {
	return &TVTracker{
		state: NewTVShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (tvt *TVTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	for key, value := range inputs {
		tvt.state.Inputs.Current[key] = value
	}
	tvt.state.Metadata.LastUpdated = time.Now()
}

// UpdateAppleTVState updates the Apple TV playing state
func (tvt *TVTracker) UpdateAppleTVState(isPlaying bool, state string) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	tvt.state.Outputs.IsAppleTVPlaying = isPlaying
	tvt.state.Outputs.AppleTVState = state
	tvt.state.Outputs.LastUpdate = time.Now()
	tvt.state.Metadata.LastUpdated = time.Now()
}

// UpdateTVPower updates the TV power state
func (tvt *TVTracker) UpdateTVPower(isOn bool) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	tvt.state.Outputs.IsTVOn = isOn
	tvt.state.Outputs.LastUpdate = time.Now()
	tvt.state.Metadata.LastUpdated = time.Now()
}

// UpdateHDMIInput updates the current HDMI input
func (tvt *TVTracker) UpdateHDMIInput(input string) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	tvt.state.Outputs.CurrentHDMIInput = input
	tvt.state.Outputs.LastUpdate = time.Now()
	tvt.state.Metadata.LastUpdated = time.Now()
}

// UpdateTVPlaying updates the computed isTVPlaying state
func (tvt *TVTracker) UpdateTVPlaying(isPlaying bool) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	tvt.state.Outputs.IsTVPlaying = isPlaying
	tvt.state.Outputs.LastUpdate = time.Now()
	tvt.state.Metadata.LastUpdated = time.Now()
}

// UpdateSyncBoxAvailable updates the sync box availability state
func (tvt *TVTracker) UpdateSyncBoxAvailable(available bool) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	tvt.state.Outputs.SyncBoxAvailable = available
	tvt.state.Outputs.LastUpdate = time.Now()
	tvt.state.Metadata.LastUpdated = time.Now()
}

// UpdateLastRecovery updates the last sync box recovery timestamp and daily count
func (tvt *TVTracker) UpdateLastRecovery(rebootTime time.Time, dailyCount int) {
	tvt.mu.Lock()
	defer tvt.mu.Unlock()

	tvt.state.Outputs.LastSyncBoxReboot = rebootTime
	tvt.state.Outputs.DailyRebootCount = dailyCount
	tvt.state.Outputs.LastUpdate = time.Now()
	tvt.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (tvt *TVTracker) GetState() *TVShadowState {
	tvt.mu.RLock()
	defer tvt.mu.RUnlock()

	// Create a deep copy
	stateCopy := &TVShadowState{
		Plugin: tvt.state.Plugin,
		Inputs: TVInputs{
			Current: make(map[string]interface{}),
		},
		Outputs:  tvt.state.Outputs,
		Metadata: tvt.state.Metadata,
	}

	// Copy current inputs
	for k, v := range tvt.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	return stateCopy
}

// ============================================================================
// Environmental Monitoring Tracker
// ============================================================================

// EnvironmentalTracker manages shadow state for the environmental monitoring plugin
type EnvironmentalTracker struct {
	mu    sync.RWMutex
	state *EnvironmentalShadowState
}

// NewEnvironmentalTracker creates a new environmental shadow state tracker
func NewEnvironmentalTracker() *EnvironmentalTracker {
	return &EnvironmentalTracker{
		state: NewEnvironmentalShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (et *EnvironmentalTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	et.mu.Lock()
	defer et.mu.Unlock()

	for key, value := range inputs {
		et.state.Inputs.Current[key] = value
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateHumiditySensors updates the list of humidity sensors and their values
func (et *EnvironmentalTracker) UpdateHumiditySensors(sensors []HumiditySensorData) {
	et.mu.Lock()
	defer et.mu.Unlock()

	// Make a copy of the slice
	et.state.Outputs.HumiditySensors = make([]HumiditySensorData, len(sensors))
	copy(et.state.Outputs.HumiditySensors, sensors)
	et.state.Outputs.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateAlertLevel updates the current alert level and sustained status
func (et *EnvironmentalTracker) UpdateAlertLevel(level string, conditionStartTime time.Time, isSustained bool) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.AlertLevel = level
	et.state.Outputs.ConditionStartTime = conditionStartTime
	et.state.Outputs.IsSustained = isSustained
	et.state.Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (et *EnvironmentalTracker) RecordNotification(level, message string, sensorLocations []string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.LastNotification = &NotificationRecord{
		Level:           level,
		Message:         message,
		SensorLocations: sensorLocations,
		Timestamp:       time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// RecordResolutionNotice records a resolution notification
func (et *EnvironmentalTracker) RecordResolutionNotice(message string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.LastResolutionNotice = &NotificationRecord{
		Level:     "resolved",
		Message:   message,
		Timestamp: time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateWaterLeakSensors updates the list of water leak sensors
func (et *EnvironmentalTracker) UpdateWaterLeakSensors(sensors []WaterLeakSensorData) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.WaterLeakSensors = make([]WaterLeakSensorData, len(sensors))
	copy(et.state.Outputs.WaterLeakSensors, sensors)
	et.state.Outputs.LastUpdate = time.Now()
	et.state.Metadata.LastUpdated = time.Now()
}

// UpdateActiveWaterLeaks updates the list of active water leak alerts
func (et *EnvironmentalTracker) UpdateActiveWaterLeaks(alerts []WaterLeakAlert) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.ActiveWaterLeaks = make([]WaterLeakAlert, len(alerts))
	copy(et.state.Outputs.ActiveWaterLeaks, alerts)
	et.state.Metadata.LastUpdated = time.Now()
}

// RecordWaterLeakNotification records a water leak notification that was sent
func (et *EnvironmentalTracker) RecordWaterLeakNotification(entityID, friendlyName, message string) {
	et.mu.Lock()
	defer et.mu.Unlock()

	et.state.Outputs.LastWaterLeakNotice = &WaterLeakNotification{
		EntityID:     entityID,
		FriendlyName: friendlyName,
		Message:      message,
		Timestamp:    time.Now(),
	}
	et.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (et *EnvironmentalTracker) GetState() *EnvironmentalShadowState {
	et.mu.RLock()
	defer et.mu.RUnlock()

	// Create a deep copy
	stateCopy := &EnvironmentalShadowState{
		Plugin: et.state.Plugin,
		Inputs: EnvironmentalInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: EnvironmentalOutputs{
			HumiditySensors:    make([]HumiditySensorData, len(et.state.Outputs.HumiditySensors)),
			WaterLeakSensors:   make([]WaterLeakSensorData, len(et.state.Outputs.WaterLeakSensors)),
			ActiveWaterLeaks:   make([]WaterLeakAlert, len(et.state.Outputs.ActiveWaterLeaks)),
			AlertLevel:         et.state.Outputs.AlertLevel,
			ConditionStartTime: et.state.Outputs.ConditionStartTime,
			IsSustained:        et.state.Outputs.IsSustained,
			LastUpdate:         et.state.Outputs.LastUpdate,
		},
		Metadata: et.state.Metadata,
	}

	// Copy current inputs
	for k, v := range et.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy humidity sensors
	copy(stateCopy.Outputs.HumiditySensors, et.state.Outputs.HumiditySensors)

	// Copy water leak sensors
	copy(stateCopy.Outputs.WaterLeakSensors, et.state.Outputs.WaterLeakSensors)

	// Copy active water leaks
	copy(stateCopy.Outputs.ActiveWaterLeaks, et.state.Outputs.ActiveWaterLeaks)

	// Copy notification records if they exist
	if et.state.Outputs.LastNotification != nil {
		notification := *et.state.Outputs.LastNotification
		// Copy sensor locations slice
		if notification.SensorLocations != nil {
			notification.SensorLocations = make([]string, len(et.state.Outputs.LastNotification.SensorLocations))
			copy(notification.SensorLocations, et.state.Outputs.LastNotification.SensorLocations)
		}
		stateCopy.Outputs.LastNotification = &notification
	}

	if et.state.Outputs.LastResolutionNotice != nil {
		resolution := *et.state.Outputs.LastResolutionNotice
		stateCopy.Outputs.LastResolutionNotice = &resolution
	}

	if et.state.Outputs.LastWaterLeakNotice != nil {
		waterLeakNotice := *et.state.Outputs.LastWaterLeakNotice
		stateCopy.Outputs.LastWaterLeakNotice = &waterLeakNotice
	}

	return stateCopy
}

// ============================================================================
// Sensor Health Tracker - Battery, Staleness, and Temperature Lockup Monitoring
// ============================================================================

// SensorHealthTracker manages shadow state for the sensor health plugin
type SensorHealthTracker struct {
	mu    sync.RWMutex
	state *SensorHealthShadowState
}

// NewSensorHealthTracker creates a new sensor health shadow state tracker
func NewSensorHealthTracker() *SensorHealthTracker {
	return &SensorHealthTracker{
		state: NewSensorHealthShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (st *SensorHealthTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	st.mu.Lock()
	defer st.mu.Unlock()

	for key, value := range inputs {
		st.state.Inputs.Current[key] = value
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// UpdateBatterySensors updates the list of discovered battery sensors
func (st *SensorHealthTracker) UpdateBatterySensors(sensors []BatterySensorData) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.BatterySensors = make([]BatterySensorData, len(sensors))
	copy(st.state.Outputs.BatterySensors, sensors)
	st.state.Outputs.LastUpdate = time.Now()
	st.state.Metadata.LastUpdated = time.Now()
}

// UpdateTemperatureSensors updates the list of discovered temperature sensors for lockup monitoring
func (st *SensorHealthTracker) UpdateTemperatureSensors(sensors []TemperatureSensorData) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.TemperatureSensors = make([]TemperatureSensorData, len(sensors))
	copy(st.state.Outputs.TemperatureSensors, sensors)
	st.state.Outputs.LastUpdate = time.Now()
	st.state.Metadata.LastUpdated = time.Now()
}

// UpdateLowBatteryAlerts updates the list of low battery alerts
func (st *SensorHealthTracker) UpdateLowBatteryAlerts(alerts []LowBatteryAlert) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.LowBatteryAlerts = make([]LowBatteryAlert, len(alerts))
	copy(st.state.Outputs.LowBatteryAlerts, alerts)
	st.state.Metadata.LastUpdated = time.Now()
}

// UpdateStaleSensorAlerts updates the list of stale sensor alerts
func (st *SensorHealthTracker) UpdateStaleSensorAlerts(alerts []StaleSensorAlert) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.StaleSensorAlerts = make([]StaleSensorAlert, len(alerts))
	copy(st.state.Outputs.StaleSensorAlerts, alerts)
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (st *SensorHealthTracker) RecordNotification(alertType, entityID, message string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.LastNotification = &SensorHealthNotification{
		AlertType: alertType,
		EntityID:  entityID,
		Message:   message,
		Timestamp: time.Now(),
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordTemperatureLockupNotification records a temperature lockup notification that was sent
func (st *SensorHealthTracker) RecordTemperatureLockupNotification(entityID, friendlyName, message string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.LastTemperatureLockupNotice = &TemperatureLockupNotice{
		EntityID:     entityID,
		FriendlyName: friendlyName,
		Message:      message,
		Timestamp:    time.Now(),
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// RecordTemperatureRecoveryNotification records a temperature recovery notification that was sent
func (st *SensorHealthTracker) RecordTemperatureRecoveryNotification(entityID, friendlyName, message string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.LastTemperatureRecoveryNotice = &TemperatureRecoveryNotice{
		EntityID:     entityID,
		FriendlyName: friendlyName,
		Message:      message,
		Timestamp:    time.Now(),
	}
	st.state.Metadata.LastUpdated = time.Now()
}

// SetLastDiscoveryRefresh records when discovery was last refreshed
func (st *SensorHealthTracker) SetLastDiscoveryRefresh(t time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.state.Outputs.LastDiscoveryRefresh = t
	st.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (st *SensorHealthTracker) GetState() *SensorHealthShadowState {
	st.mu.RLock()
	defer st.mu.RUnlock()

	// Create a deep copy
	stateCopy := &SensorHealthShadowState{
		Plugin: st.state.Plugin,
		Inputs: SensorHealthInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: SensorHealthOutputs{
			BatterySensors:       make([]BatterySensorData, len(st.state.Outputs.BatterySensors)),
			TemperatureSensors:   make([]TemperatureSensorData, len(st.state.Outputs.TemperatureSensors)),
			LowBatteryAlerts:     make([]LowBatteryAlert, len(st.state.Outputs.LowBatteryAlerts)),
			StaleSensorAlerts:    make([]StaleSensorAlert, len(st.state.Outputs.StaleSensorAlerts)),
			Config:               st.state.Outputs.Config,
			LastUpdate:           st.state.Outputs.LastUpdate,
			LastDiscoveryRefresh: st.state.Outputs.LastDiscoveryRefresh,
		},
		Metadata: st.state.Metadata,
	}

	// Copy current inputs
	for k, v := range st.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy slices
	copy(stateCopy.Outputs.BatterySensors, st.state.Outputs.BatterySensors)
	copy(stateCopy.Outputs.TemperatureSensors, st.state.Outputs.TemperatureSensors)
	copy(stateCopy.Outputs.LowBatteryAlerts, st.state.Outputs.LowBatteryAlerts)
	copy(stateCopy.Outputs.StaleSensorAlerts, st.state.Outputs.StaleSensorAlerts)

	// Copy notification records if they exist
	if st.state.Outputs.LastNotification != nil {
		notification := *st.state.Outputs.LastNotification
		stateCopy.Outputs.LastNotification = &notification
	}

	if st.state.Outputs.LastTemperatureLockupNotice != nil {
		lockupNotice := *st.state.Outputs.LastTemperatureLockupNotice
		stateCopy.Outputs.LastTemperatureLockupNotice = &lockupNotice
	}

	if st.state.Outputs.LastTemperatureRecoveryNotice != nil {
		recoveryNotice := *st.state.Outputs.LastTemperatureRecoveryNotice
		stateCopy.Outputs.LastTemperatureRecoveryNotice = &recoveryNotice
	}

	return stateCopy
}

// ============================================================================
// Sensor Config Tracker - Zigbee Sensor Threshold Configuration
// ============================================================================

// SensorConfigTracker manages shadow state for the sensor config plugin
type SensorConfigTracker struct {
	mu    sync.RWMutex
	state *SensorConfigShadowState
}

// NewSensorConfigTracker creates a new sensor config shadow state tracker
func NewSensorConfigTracker() *SensorConfigTracker {
	return &SensorConfigTracker{
		state: NewSensorConfigShadowState(),
	}
}

// RecordConfiguration records a threshold configuration
func (sct *SensorConfigTracker) RecordConfiguration(configType, description string, value float64, configuredEntities, failedEntities []string) {
	sct.mu.Lock()
	defer sct.mu.Unlock()

	now := time.Now()
	config := ThresholdConfiguration{
		ConfigType:         configType,
		Description:        description,
		Value:              value,
		ConfiguredEntities: configuredEntities,
		FailedEntities:     failedEntities,
		ConfiguredAt:       now,
	}

	sct.state.Outputs.Configurations = append(sct.state.Outputs.Configurations, config)
	sct.state.Outputs.ConfiguredAt = now
	sct.state.Outputs.LastUpdate = now
	sct.state.Metadata.LastUpdated = now
}

// Clear clears all configuration records (used during Reset)
func (sct *SensorConfigTracker) Clear() {
	sct.mu.Lock()
	defer sct.mu.Unlock()

	sct.state.Outputs.Configurations = make([]ThresholdConfiguration, 0)
	sct.state.Outputs.ConfiguredAt = time.Time{}
	sct.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (sct *SensorConfigTracker) GetState() *SensorConfigShadowState {
	sct.mu.RLock()
	defer sct.mu.RUnlock()

	// Create a deep copy
	stateCopy := &SensorConfigShadowState{
		Plugin: sct.state.Plugin,
		Inputs: SensorConfigInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: SensorConfigOutputs{
			Configurations: make([]ThresholdConfiguration, len(sct.state.Outputs.Configurations)),
			ConfiguredAt:   sct.state.Outputs.ConfiguredAt,
			LastUpdate:     sct.state.Outputs.LastUpdate,
		},
		Metadata: sct.state.Metadata,
	}

	// Copy current inputs
	for k, v := range sct.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy configurations
	for i, config := range sct.state.Outputs.Configurations {
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
// Infrastructure Tracker - Aerobic Septic System Monitoring
// ============================================================================

// InfrastructureTracker manages shadow state for the infrastructure plugin
type InfrastructureTracker struct {
	mu    sync.RWMutex
	state *InfrastructureShadowState
}

// NewInfrastructureTracker creates a new infrastructure shadow state tracker
func NewInfrastructureTracker() *InfrastructureTracker {
	return &InfrastructureTracker{
		state: NewInfrastructureShadowState(),
	}
}

// UpdateCurrentInputs updates the current input values
func (it *InfrastructureTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	it.mu.Lock()
	defer it.mu.Unlock()

	for key, value := range inputs {
		it.state.Inputs.Current[key] = value
	}
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdateSepticPower updates the current septic system power reading
func (it *InfrastructureTracker) UpdateSepticPower(powerW float64) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.SepticSystemStatus.CurrentPowerW = powerW
	it.state.Outputs.LastUpdate = time.Now()
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdateSystemState updates the septic system state
func (it *InfrastructureTracker) UpdateSystemState(systemState string) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.SepticSystemStatus.SystemState = systemState
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdateAeratorFailureStart tracks when low power condition started
func (it *InfrastructureTracker) UpdateAeratorFailureStart(startTime time.Time) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.SepticSystemStatus.AeratorFailureStart = startTime
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdatePumpRunningStart tracks when high power condition started
func (it *InfrastructureTracker) UpdatePumpRunningStart(startTime time.Time) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.SepticSystemStatus.PumpRunningStart = startTime
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdateLastNormalPowerTime tracks when power was last in normal range
func (it *InfrastructureTracker) UpdateLastNormalPowerTime(t time.Time) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.SepticSystemStatus.LastNormalPowerTime = t
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdateIsAlerting tracks whether an alert is currently active
func (it *InfrastructureTracker) UpdateIsAlerting(isAlerting bool) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.SepticSystemStatus.IsAlerting = isAlerting
	it.state.Metadata.LastUpdated = time.Now()
}

// UpdateActiveAlerts updates the list of active alerts
func (it *InfrastructureTracker) UpdateActiveAlerts(alerts []InfrastructureAlert) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.ActiveAlerts = make([]InfrastructureAlert, len(alerts))
	copy(it.state.Outputs.ActiveAlerts, alerts)
	it.state.Metadata.LastUpdated = time.Now()
}

// RecordNotification records a notification that was sent
func (it *InfrastructureTracker) RecordNotification(alertType, message, priority string) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.LastNotification = &InfrastructureNotification{
		AlertType: alertType,
		Message:   message,
		Priority:  priority,
		Timestamp: time.Now(),
	}
	it.state.Metadata.LastUpdated = time.Now()
}

// RecordTTSAnnouncement records a TTS announcement that was made
func (it *InfrastructureTracker) RecordTTSAnnouncement(message string) {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.LastTTSAnnouncement = &InfrastructureTTS{
		Message:   message,
		Timestamp: time.Now(),
	}
	it.state.Metadata.LastUpdated = time.Now()
}

// ClearAlerts clears all active alerts and resets alerting state
func (it *InfrastructureTracker) ClearAlerts() {
	it.mu.Lock()
	defer it.mu.Unlock()

	it.state.Outputs.ActiveAlerts = make([]InfrastructureAlert, 0)
	it.state.Outputs.SepticSystemStatus.IsAlerting = false
	it.state.Outputs.SepticSystemStatus.AeratorFailureStart = time.Time{}
	it.state.Outputs.SepticSystemStatus.PumpRunningStart = time.Time{}
	it.state.Metadata.LastUpdated = time.Now()
}

// GetState returns the current shadow state (thread-safe copy)
func (it *InfrastructureTracker) GetState() *InfrastructureShadowState {
	it.mu.RLock()
	defer it.mu.RUnlock()

	// Create a deep copy
	stateCopy := &InfrastructureShadowState{
		Plugin: it.state.Plugin,
		Inputs: InfrastructureInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: InfrastructureOutputs{
			SepticSystemStatus: it.state.Outputs.SepticSystemStatus,
			ActiveAlerts:       make([]InfrastructureAlert, len(it.state.Outputs.ActiveAlerts)),
			LastUpdate:         it.state.Outputs.LastUpdate,
		},
		Metadata: it.state.Metadata,
	}

	// Copy current inputs
	for k, v := range it.state.Inputs.Current {
		stateCopy.Inputs.Current[k] = v
	}

	// Copy active alerts
	copy(stateCopy.Outputs.ActiveAlerts, it.state.Outputs.ActiveAlerts)

	// Copy notification record if it exists
	if it.state.Outputs.LastNotification != nil {
		notification := *it.state.Outputs.LastNotification
		stateCopy.Outputs.LastNotification = &notification
	}

	// Copy TTS record if it exists
	if it.state.Outputs.LastTTSAnnouncement != nil {
		tts := *it.state.Outputs.LastTTSAnnouncement
		stateCopy.Outputs.LastTTSAnnouncement = &tts
	}

	return stateCopy
}
