package shadowstate

import (
	"time"

	pkgshadow "homeautomation/pkg/shadowstate"
)

// Re-export public types from pkg/shadowstate for internal use.
// This allows internal code to continue importing from internal/shadowstate
// while the actual interface types are defined in the public pkg/shadowstate.
type (
	// PluginShadowState is re-exported from pkg/shadowstate.
	// See pkg/shadowstate.PluginShadowState for documentation.
	PluginShadowState = pkgshadow.PluginShadowState

	// StateMetadata is re-exported from pkg/shadowstate.
	// See pkg/shadowstate.StateMetadata for documentation.
	StateMetadata = pkgshadow.StateMetadata

	// InputSnapshot is re-exported from pkg/shadowstate.
	// See pkg/shadowstate.InputSnapshot for documentation.
	InputSnapshot = pkgshadow.InputSnapshot
)

// ============================================================================
// Generic Shadow State Types
// ============================================================================

// ShadowInputs is the interface that input types must implement.
type ShadowInputs interface {
	// GetCurrent returns the current input values.
	GetCurrent() map[string]interface{}
	// GetAtLastAction returns the input values at the time of the last action.
	// For read-heavy plugins, this returns the same as GetCurrent.
	GetAtLastAction() map[string]interface{}
}

// ActionInputs tracks current and last-action input values for action-heavy plugins.
type ActionInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// GetCurrent implements ShadowInputs.
func (a ActionInputs) GetCurrent() map[string]interface{} { return a.Current }

// GetAtLastAction implements ShadowInputs.
func (a ActionInputs) GetAtLastAction() map[string]interface{} { return a.AtLastAction }

// ReadOnlyInputs tracks current input values for read-heavy plugins
// that don't have action snapshots.
type ReadOnlyInputs struct {
	Current map[string]interface{} `json:"current"`
}

// GetCurrent implements ShadowInputs.
func (r ReadOnlyInputs) GetCurrent() map[string]interface{} { return r.Current }

// GetAtLastAction implements ShadowInputs. Returns Current (no action tracking).
func (r ReadOnlyInputs) GetAtLastAction() map[string]interface{} { return r.Current }

// ShadowState is a generic shadow state container for any plugin.
// I is the inputs type (ActionInputs or ReadOnlyInputs).
// O is the plugin-specific outputs type.
type ShadowState[I ShadowInputs, O any] struct {
	Plugin   string        `json:"plugin"`
	Inputs   I             `json:"inputs"`
	Outputs  O             `json:"outputs"`
	Metadata StateMetadata `json:"metadata"`
}

// GetCurrentInputs implements PluginShadowState.
func (s *ShadowState[I, O]) GetCurrentInputs() map[string]interface{} {
	return s.Inputs.GetCurrent()
}

// GetLastActionInputs implements PluginShadowState.
func (s *ShadowState[I, O]) GetLastActionInputs() map[string]interface{} {
	return s.Inputs.GetAtLastAction()
}

// GetOutputs implements PluginShadowState.
func (s *ShadowState[I, O]) GetOutputs() interface{} {
	return s.Outputs
}

// GetMetadata implements PluginShadowState.
func (s *ShadowState[I, O]) GetMetadata() StateMetadata {
	return s.Metadata
}

// ============================================================================
// Plugin Shadow State Type Aliases
// ============================================================================

// Action-heavy plugin shadow states (have AtLastAction snapshot)
type LightingShadowState = ShadowState[ActionInputs, LightingOutputs]
type MusicShadowState = ShadowState[ActionInputs, MusicOutputs]
type SecurityShadowState = ShadowState[ActionInputs, SecurityOutputs]
type LoadSheddingShadowState = ShadowState[ActionInputs, LoadSheddingOutputs]
type SleepHygieneShadowState = ShadowState[ActionInputs, SleepHygieneOutputs]
type SexModeShadowState = ShadowState[ActionInputs, SexModeOutputs]
type ChristmasShadowState = ShadowState[ActionInputs, ChristmasOutputs]

// Read-heavy plugin shadow states (current inputs only, no action snapshot)
type EnergyShadowState = ShadowState[ReadOnlyInputs, EnergyOutputs]
type StateTrackingShadowState = ShadowState[ReadOnlyInputs, StateTrackingOutputs]
type DayPhaseShadowState = ShadowState[ReadOnlyInputs, DayPhaseOutputs]
type TVShadowState = ShadowState[ReadOnlyInputs, TVOutputs]
type SystemShadowState = ShadowState[ReadOnlyInputs, SystemOutputs]
type EnvironmentalShadowState = ShadowState[ReadOnlyInputs, EnvironmentalOutputs]
type SensorHealthShadowState = ShadowState[ReadOnlyInputs, SensorHealthOutputs]
type InfrastructureShadowState = ShadowState[ReadOnlyInputs, InfrastructureOutputs]
type SensorConfigShadowState = ShadowState[ReadOnlyInputs, SensorConfigOutputs]
type WaterFlowShadowState = ShadowState[ReadOnlyInputs, WaterFlowOutputs]
type EVChargerShadowState = ShadowState[ReadOnlyInputs, EVChargerOutputs]

// SystemInputs is used by cmd/app/run.go for system plugin shadow state.
type SystemInputs = ReadOnlyInputs

// ============================================================================
// Constructors
// ============================================================================

// newActionShadowState creates a new shadow state for action-heavy plugins.
func newActionShadowState[O any](pluginName string, outputs O) *ShadowState[ActionInputs, O] {
	return &ShadowState[ActionInputs, O]{
		Plugin: pluginName,
		Inputs: ActionInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: outputs,
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  pluginName,
		},
	}
}

// newReadOnlyShadowState creates a new shadow state for read-heavy plugins.
func newReadOnlyShadowState[O any](pluginName string, outputs O) *ShadowState[ReadOnlyInputs, O] {
	return &ShadowState[ReadOnlyInputs, O]{
		Plugin: pluginName,
		Inputs: ReadOnlyInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: outputs,
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  pluginName,
		},
	}
}

// NewLightingShadowState creates a new lighting shadow state.
func NewLightingShadowState() *LightingShadowState {
	return newActionShadowState("lighting", LightingOutputs{
		Rooms:          make(map[string]RoomState),
		LastActionTime: time.Time{},
	})
}

// NewMusicShadowState creates a new music shadow state.
func NewMusicShadowState() *MusicShadowState {
	return newActionShadowState("music", MusicOutputs{
		SpeakerGroup:     make([]SpeakerState, 0),
		PlaylistRotation: make(map[string]int),
		FadeState:        "idle",
		FadeInProgress:   make(map[string]SpeakerFadeIn),
	})
}

// NewSecurityShadowState creates a new security shadow state.
func NewSecurityShadowState() *SecurityShadowState {
	return newActionShadowState("security", SecurityOutputs{
		Lockdown:       LockdownState{},
		LastActionTime: time.Time{},
	})
}

// NewLoadSheddingShadowState creates a new load shedding shadow state.
func NewLoadSheddingShadowState() *LoadSheddingShadowState {
	return newActionShadowState("loadshedding", LoadSheddingOutputs{
		Active:         false,
		LastActionTime: time.Time{},
	})
}

// NewSleepHygieneShadowState creates a new sleep hygiene shadow state.
func NewSleepHygieneShadowState() *SleepHygieneShadowState {
	return newActionShadowState("sleephygiene", SleepHygieneOutputs{
		WakeSequenceStatus: "inactive",
		FadeOutProgress:    make(map[string]SpeakerFadeOut),
	})
}

// NewSexModeShadowState creates a new sex mode shadow state.
func NewSexModeShadowState() *SexModeShadowState {
	return newActionShadowState("sexmode", SexModeOutputs{
		IsActive:       false,
		LastActionTime: time.Time{},
	})
}

// NewChristmasShadowState creates a new christmas shadow state.
func NewChristmasShadowState() *ChristmasShadowState {
	return newActionShadowState("christmas", ChristmasOutputs{
		LightsActivated: 0,
	})
}

// NewEnergyShadowState creates a new energy shadow state.
func NewEnergyShadowState() *EnergyShadowState {
	return newReadOnlyShadowState("energy", EnergyOutputs{
		LastComputations: EnergyComputations{},
		SensorReadings:   EnergySensorReadings{},
	})
}

// NewStateTrackingShadowState creates a new state tracking shadow state.
func NewStateTrackingShadowState() *StateTrackingShadowState {
	return newReadOnlyShadowState("statetracking", StateTrackingOutputs{
		DerivedStates: DerivedStates{},
		TimerStates:   StateTrackingTimers{},
	})
}

// NewDayPhaseShadowState creates a new day phase shadow state.
func NewDayPhaseShadowState() *DayPhaseShadowState {
	return newReadOnlyShadowState("dayphase", DayPhaseOutputs{})
}

// NewTVShadowState creates a new TV shadow state.
func NewTVShadowState() *TVShadowState {
	return newReadOnlyShadowState("tv", TVOutputs{
		SyncBoxAvailable: true, // Assume available until proven otherwise
	})
}

// NewSystemShadowState creates a new system shadow state.
func NewSystemShadowState() *SystemShadowState {
	return newReadOnlyShadowState("system", SystemOutputs{})
}

// NewEnvironmentalShadowState creates a new environmental shadow state.
func NewEnvironmentalShadowState() *EnvironmentalShadowState {
	return newReadOnlyShadowState("environmental", EnvironmentalOutputs{
		HumiditySensors:  make([]HumiditySensorData, 0),
		WaterLeakSensors: make([]WaterLeakSensorData, 0),
		ActiveWaterLeaks: make([]WaterLeakAlert, 0),
		AlertLevel:       "none",
	})
}

// NewSensorHealthShadowState creates a new sensor health shadow state.
func NewSensorHealthShadowState() *SensorHealthShadowState {
	return newReadOnlyShadowState("sensorhealth", SensorHealthOutputs{
		BatterySensors:     make([]BatterySensorData, 0),
		TemperatureSensors: make([]TemperatureSensorData, 0),
		NodeStatuses:       make([]NodeStatusData, 0),
		LowBatteryAlerts:   make([]LowBatteryAlert, 0),
		DeadDeviceAlerts:   make([]DeadDeviceAlert, 0),
		Config: SensorHealthConfig{
			LowBatteryThreshold: 20,
		},
	})
}

// NewInfrastructureShadowState creates a new infrastructure shadow state.
func NewInfrastructureShadowState() *InfrastructureShadowState {
	return newReadOnlyShadowState("infrastructure", InfrastructureOutputs{
		SepticSystemStatus: SepticSystemStatus{
			SystemState: "normal",
		},
		ThermostatStatus: ThermostatStatus{},
		ActiveAlerts:     make([]InfrastructureAlert, 0),
	})
}

// NewSensorConfigShadowState creates a new sensor config shadow state.
func NewSensorConfigShadowState() *SensorConfigShadowState {
	return newReadOnlyShadowState("sensorconfig", SensorConfigOutputs{
		Configurations: make([]ThresholdConfiguration, 0),
	})
}

// NewWaterFlowShadowState creates a new water flow shadow state.
func NewWaterFlowShadowState() *WaterFlowShadowState {
	return newReadOnlyShadowState("waterflow", WaterFlowOutputs{
		AlertLevel:   "none",
		ActiveAlerts: make([]WaterFlowAlert, 0),
	})
}

// NewEVChargerShadowState creates a new EV charger shadow state.
func NewEVChargerShadowState() *EVChargerShadowState {
	return newReadOnlyShadowState("evcharger", EVChargerOutputs{})
}

// ============================================================================
// Lighting Plugin Output Types
// ============================================================================

// LightingOutputs tracks the state of lighting control outputs
type LightingOutputs struct {
	Rooms          map[string]RoomState `json:"rooms"`
	LastActionTime time.Time            `json:"lastActionTime"`
}

// RoomState represents the state of a single room
type RoomState struct {
	ActiveScene string    `json:"activeScene,omitempty"`
	TurnedOff   bool      `json:"turnedOff,omitempty"`
	LastAction  time.Time `json:"lastAction"`
	ActionType  string    `json:"actionType"` // "activate_scene" or "turn_off"
	Reason      string    `json:"reason"`
}

// ============================================================================
// Music Plugin Output Types
// ============================================================================

// MusicOutputs tracks the state of music control outputs
type MusicOutputs struct {
	CurrentMode          string                      `json:"currentMode,omitempty"` // e.g., "morning", "working", "evening"
	ActivePlaylist       PlaylistInfo                `json:"activePlaylist,omitempty"`
	SpeakerGroup         []SpeakerState              `json:"speakerGroup,omitempty"`
	FadeState            string                      `json:"fadeState"` // "idle", "fading_in", "fading_out"
	FadeInProgress       map[string]SpeakerFadeIn    `json:"fadeInProgress,omitempty"`
	PlaylistRotation     map[string]int              `json:"playlistRotation"` // Music type -> playlist number
	LastActionTime       time.Time                   `json:"lastActionTime"`
	LastActionType       string                      `json:"lastActionType,omitempty"` // "select_mode", "start_playback", "fade_out", etc.
	LastActionReason     string                      `json:"lastActionReason,omitempty"`
	PlaybackVerification *PlaybackVerificationStatus `json:"playbackVerification,omitempty"`
	PlaybackHealth       *PlaybackHealthStatus       `json:"playbackHealth,omitempty"`
	// Phase 2: Multi-zone support
	ActiveZones []ZoneShadowState `json:"activeZones,omitempty"`
}

// ZoneShadowState represents a single active zone in shadow state
type ZoneShadowState struct {
	Name         string         `json:"name"`
	MusicType    string         `json:"musicType"`
	Priority     int            `json:"priority"`
	LeadSpeaker  string         `json:"leadSpeaker"`
	Participants []SpeakerState `json:"participants"`
	PlaylistURI  string         `json:"playlistUri"`
	StartedAt    time.Time      `json:"startedAt"`
}

// SpeakerFadeIn represents the fade-in state of a single speaker
type SpeakerFadeIn struct {
	SpeakerName           string    `json:"speakerName"`
	SpeakerEntityID       string    `json:"speakerEntityID"`
	CurrentVolume         int       `json:"currentVolume"`
	TargetVolume          int       `json:"targetVolume"`
	IsActive              bool      `json:"isActive"`
	HumanOverrideDetected bool      `json:"humanOverrideDetected,omitempty"`
	ExpectedVolume        int       `json:"expectedVolume,omitempty"`
	ActualVolume          int       `json:"actualVolume,omitempty"`
	StartTime             time.Time `json:"startTime"`
	LastUpdate            time.Time `json:"lastUpdate"`
}

// PlaybackVerificationStatus tracks whether playback was verified to start
type PlaybackVerificationStatus struct {
	Verified       bool      `json:"verified"`             // Whether playback was confirmed to start
	AttemptsNeeded int       `json:"attemptsNeeded"`       // Number of attempts needed (1 = first try worked)
	FinalState     string    `json:"finalState,omitempty"` // The media_player state when verified
	VerifiedAt     time.Time `json:"verifiedAt"`           // When verification completed
	LeadSpeaker    string    `json:"leadSpeaker"`          // Which speaker was checked
}

// PlaybackHealthStatus tracks post-playback health monitoring for auto-pause detection
type PlaybackHealthStatus struct {
	IsMonitoring      bool      `json:"isMonitoring"`               // Whether monitoring is currently active
	MonitorStartTime  time.Time `json:"monitorStartTime,omitempty"` // When monitoring started
	MonitorEndTime    time.Time `json:"monitorEndTime,omitempty"`   // When monitoring will/did end
	RecoveryAttempted bool      `json:"recoveryAttempted"`          // True after single recovery attempt
	RecoveryTime      time.Time `json:"recoveryTime,omitempty"`     // When recovery was attempted
	RecoveryResult    string    `json:"recoveryResult,omitempty"`   // "success", "failed", or empty
	LastSpeakerState  string    `json:"lastSpeakerState,omitempty"` // Last observed speaker state
	LeadSpeaker       string    `json:"leadSpeaker,omitempty"`      // Which speaker is being monitored
	MusicType         string    `json:"musicType,omitempty"`        // Music type being monitored
}

// PlaylistInfo represents the currently playing playlist
type PlaylistInfo struct {
	URI       string `json:"uri"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"mediaType"`
}

// SpeakerState represents a single speaker's state
type SpeakerState struct {
	PlayerName    string `json:"playerName"`
	Volume        int    `json:"volume"`
	BaseVolume    int    `json:"baseVolume"`
	DefaultVolume int    `json:"defaultVolume"`
	IsLeader      bool   `json:"isLeader"`
	Active        bool   `json:"active"`                  // Whether the speaker successfully joined the group
	FailureReason string `json:"failureReason,omitempty"` // Reason for failure if Active is false
}

// ============================================================================
// Security Plugin Output Types
// ============================================================================

// SecurityOutputs tracks the state of security control outputs
type SecurityOutputs struct {
	Lockdown       LockdownState        `json:"lockdown"`
	LastDoorbell   *DoorbellEvent       `json:"lastDoorbell,omitempty"`
	LastVehicle    *VehicleArrivalEvent `json:"lastVehicle,omitempty"`
	LastGarageOpen *GarageOpenEvent     `json:"lastGarageOpen,omitempty"`
	LastActionTime time.Time            `json:"lastActionTime"`
}

// LockdownState represents the current lockdown status
type LockdownState struct {
	Active      bool      `json:"active"`
	Reason      string    `json:"reason,omitempty"`
	ActivatedAt time.Time `json:"activatedAt,omitempty"`
	WillResetAt time.Time `json:"willResetAt,omitempty"`
}

// DoorbellEvent represents a doorbell press event
type DoorbellEvent struct {
	Timestamp     time.Time `json:"timestamp"`
	RateLimited   bool      `json:"rateLimited"`
	TTSSent       bool      `json:"ttsSent"`
	LightsFlashed bool      `json:"lightsFlashed"`
}

// VehicleArrivalEvent represents a vehicle arrival notification
type VehicleArrivalEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	RateLimited  bool      `json:"rateLimited"`
	TTSSent      bool      `json:"ttsSent"`
	WasExpecting bool      `json:"wasExpecting"`
}

// GarageOpenEvent represents a garage auto-open event
type GarageOpenEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	Reason         string    `json:"reason"`
	GarageWasEmpty bool      `json:"garageWasEmpty"`
}

// ============================================================================
// Load Shedding Plugin Output Types
// ============================================================================

// LoadSheddingOutputs tracks the state of load shedding control outputs
type LoadSheddingOutputs struct {
	Active             bool                `json:"active"`
	LastActionType     string              `json:"lastActionType,omitempty"` // "enable" or "disable"
	LastActionReason   string              `json:"lastActionReason,omitempty"`
	ThermostatSettings ThermostatSettings  `json:"thermostatSettings,omitempty"`
	ThermalBattery     ThermalBatteryState `json:"thermalBattery,omitempty"`
	LastActionTime     time.Time           `json:"lastActionTime"`
}

// ThermostatSettings represents thermostat configuration
type ThermostatSettings struct {
	HoldMode bool    `json:"holdMode"`
	TempLow  float64 `json:"tempLow,omitempty"`
	TempHigh float64 `json:"tempHigh,omitempty"`
}

// ThermalBatteryState tracks the thermal battery pre-conditioning state
type ThermalBatteryState struct {
	Active            bool                     `json:"active"`
	OffsetApplied     float64                  `json:"offsetApplied,omitempty"` // Degrees F shifted so far
	ActivatedAt       time.Time                `json:"activatedAt,omitempty"`
	DeactivatedAt     time.Time                `json:"deactivatedAt,omitempty"`
	SkipReason        string                   `json:"skipReason,omitempty"` // Why activation was skipped
	SavedSetpoints    map[string]SavedSetpoint `json:"savedSetpoints,omitempty"`
	StepsCompleted    int                      `json:"stepsCompleted,omitempty"`
	TotalSteps        int                      `json:"totalSteps,omitempty"`
	StepSize          float64                  `json:"stepSize,omitempty"`
	Stepping          bool                     `json:"stepping,omitempty"`          // true while steps remain
	Deferred          bool                     `json:"deferred,omitempty"`          // true when activation is deferred due to timing
	PlannedActivation time.Time                `json:"plannedActivation,omitempty"` // when we plan to activate
	StressDirection   string                   `json:"stressDirection,omitempty"`   // "up" or "down"
}

// SavedSetpoint records the original thermostat setpoint before thermal battery offset was applied
type SavedSetpoint struct {
	EntityID   string  `json:"entityId"`
	HVACMode   string  `json:"hvacMode"`             // "heat", "cool", "heat_cool", etc.
	TargetTemp float64 `json:"targetTemp,omitempty"` // Single setpoint (heat or cool mode)
	TargetLow  float64 `json:"targetLow,omitempty"`  // Low setpoint (heat_cool mode)
	TargetHigh float64 `json:"targetHigh,omitempty"` // High setpoint (heat_cool mode)
}

// ============================================================================
// Sleep Hygiene Plugin Output Types
// ============================================================================

// SleepHygieneOutputs tracks the state of sleep hygiene outputs
type SleepHygieneOutputs struct {
	WakeSequenceStatus    string                    `json:"wakeSequenceStatus"` // "inactive", "begin_wake", "wake_in_progress", "complete"
	FadeOutProgress       map[string]SpeakerFadeOut `json:"fadeOutProgress"`    // Speaker entity ID -> fade out state
	LastTTSAnnouncement   *TTSAnnouncement          `json:"lastTTSAnnouncement,omitempty"`
	StopScreensReminder   *ReminderTrigger          `json:"stopScreensReminder,omitempty"`
	GoToBedReminder       *ReminderTrigger          `json:"goToBedReminder,omitempty"`
	EightSleepAvailable   bool                      `json:"eightSleepAvailable"`             // Whether Eight Sleep alarm can be used
	BackupWakeEnabled     bool                      `json:"backupWakeEnabled"`               // Whether backup wake is currently enabled (Eight Sleep unavailable)
	LastAvailabilityCheck time.Time                 `json:"lastAvailabilityCheck,omitempty"` // When Eight Sleep availability was last checked
	LastActionTime        time.Time                 `json:"lastActionTime"`
	LastActionType        string                    `json:"lastActionType,omitempty"` // "begin_wake", "wake", "stop_screens", "go_to_bed", "cancel_wake"
	LastActionReason      string                    `json:"lastActionReason,omitempty"`
}

// SpeakerFadeOut represents the fade-out state of a single speaker
type SpeakerFadeOut struct {
	SpeakerEntityID       string    `json:"speakerEntityID"`
	CurrentVolume         int       `json:"currentVolume"`
	StartVolume           int       `json:"startVolume"`
	IsActive              bool      `json:"isActive"` // Is fade-out currently in progress
	HumanOverrideDetected bool      `json:"humanOverrideDetected,omitempty"`
	ExpectedVolume        int       `json:"expectedVolume,omitempty"` // Volume we set, for override detection
	ActualVolume          int       `json:"actualVolume,omitempty"`   // Volume we read back, for override detection
	StartTime             time.Time `json:"startTime"`
	LastUpdate            time.Time `json:"lastUpdate"`
}

// TTSAnnouncement represents a TTS announcement that was made
type TTSAnnouncement struct {
	Message   string    `json:"message"`
	Speaker   string    `json:"speaker"`
	Timestamp time.Time `json:"timestamp"`
}

// ReminderTrigger represents a reminder trigger (screen stop or bedtime)
type ReminderTrigger struct {
	Triggered bool      `json:"triggered"`
	Timestamp time.Time `json:"timestamp"`
}

// ============================================================================
// Energy Plugin Output Types
// ============================================================================

// LuxSensorReading tracks a single lux sensor value
type LuxSensorReading struct {
	EntityID  string    `json:"entityId"`
	Lux       float64   `json:"lux"`
	Timestamp time.Time `json:"timestamp"`
}

// PerDeviceBrightness tracks brightness decisions per indicator light device
type PerDeviceBrightness struct {
	LightEntity     string    `json:"lightEntity"`
	LuxSensorEntity string    `json:"luxSensorEntity,omitempty"`
	CurrentLux      float64   `json:"currentLux,omitempty"`
	BrightnessPct   int       `json:"brightnessPct"`
	IsAdaptive      bool      `json:"isAdaptive"`
	LastUpdate      time.Time `json:"lastUpdate"`
}

// BaselineCalibration tracks the baseline lux calibration for a single device.
// Used to detect and correct for LED self-interference (LED light overwhelming the lux sensor).
type BaselineCalibration struct {
	LightEntity         string    `json:"lightEntity"`
	BaselineLux         float64   `json:"baselineLux"`
	LastCalibrationTime time.Time `json:"lastCalibrationTime"`
}

// EnergyOutputs tracks computed energy state values
type EnergyOutputs struct {
	BatteryEnergyLevel         string                         `json:"batteryEnergyLevel"`
	SolarProductionEnergyLevel string                         `json:"solarProductionEnergyLevel"`
	CurrentEnergyLevel         string                         `json:"currentEnergyLevel"`
	IsFreeEnergyAvailable      bool                           `json:"isFreeEnergyAvailable"`
	LastComputations           EnergyComputations             `json:"lastComputations"`
	SensorReadings             EnergySensorReadings           `json:"sensorReadings"`
	DiscoveredIndicatorLights  []string                       `json:"discoveredIndicatorLights,omitempty"`
	IndicatorLightsAction      *IndicatorLightsAction         `json:"indicatorLightsAction,omitempty"`
	LuxSensorReadings          map[string]LuxSensorReading    `json:"luxSensorReadings,omitempty"`
	PerDeviceBrightness        map[string]PerDeviceBrightness `json:"perDeviceBrightness,omitempty"`
	LightToLuxSensorMapping    map[string]string              `json:"lightToLuxSensorMapping,omitempty"`
	BaselineCalibrations       map[string]BaselineCalibration `json:"baselineCalibrations,omitempty"`
}

// IndicatorLightsAction tracks the last action taken to update indicator lights
type IndicatorLightsAction struct {
	EnergyLevel   string    `json:"energyLevel"`
	RGBColor      []int     `json:"rgbColor"`
	BrightnessPct int       `json:"brightnessPct"`
	EntityIDs     []string  `json:"entityIds"`
	Timestamp     time.Time `json:"timestamp"`
}

// EnergyComputations tracks when various energy calculations were last performed
type EnergyComputations struct {
	LastBatteryLevelCalc time.Time `json:"lastBatteryLevelCalc,omitempty"`
	LastSolarLevelCalc   time.Time `json:"lastSolarLevelCalc,omitempty"`
	LastFreeEnergyCheck  time.Time `json:"lastFreeEnergyCheck,omitempty"`
	LastOverallLevelCalc time.Time `json:"lastOverallLevelCalc,omitempty"`
}

// EnergySensorReadings tracks raw sensor values from Home Assistant
type EnergySensorReadings struct {
	BatteryPercentage           float64   `json:"batteryPercentage"`
	ThisHourSolarGenerationKW   float64   `json:"thisHourSolarGenerationKW"`
	RemainingSolarGenerationKWH float64   `json:"remainingSolarGenerationKWH"`
	IsGridAvailable             bool      `json:"isGridAvailable"`
	LastUpdate                  time.Time `json:"lastUpdate"`
}

// ============================================================================
// State Tracking Plugin Output Types
// ============================================================================

// StateTrackingOutputs tracks computed derived states and timer states
type StateTrackingOutputs struct {
	DerivedStates    DerivedStates        `json:"derivedStates"`
	TimerStates      StateTrackingTimers  `json:"timerStates"`
	LastAnnouncement *ArrivalAnnouncement `json:"lastAnnouncement,omitempty"`
	LastComputation  time.Time            `json:"lastComputation"`
}

// DerivedStates tracks the computed presence/sleep states
type DerivedStates struct {
	IsAnyOwnerHome   bool `json:"isAnyOwnerHome"`
	IsAnyoneHome     bool `json:"isAnyoneHome"`
	IsAnyoneAsleep   bool `json:"isAnyoneAsleep"`
	IsEveryoneAsleep bool `json:"isEveryoneAsleep"`
}

// StateTrackingTimers tracks the status of detection timers
type StateTrackingTimers struct {
	SleepDetectionActive    bool      `json:"sleepDetectionActive"`
	SleepDetectionStarted   time.Time `json:"sleepDetectionStarted,omitempty"`
	WakeDetectionActive     bool      `json:"wakeDetectionActive"`
	WakeDetectionStarted    time.Time `json:"wakeDetectionStarted,omitempty"`
	OwnerReturnResetActive  bool      `json:"ownerReturnResetActive"`
	OwnerReturnResetStarted time.Time `json:"ownerReturnResetStarted,omitempty"`
}

// ArrivalAnnouncement tracks the last TTS arrival announcement made
type ArrivalAnnouncement struct {
	Person    string    `json:"person"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ============================================================================
// Day Phase Plugin Output Types
// ============================================================================

// DayPhaseOutputs tracks computed day phase and sun event values
type DayPhaseOutputs struct {
	SunEvent            string    `json:"sunevent"`
	DayPhase            string    `json:"dayPhase"`
	LastSunEventCalc    time.Time `json:"lastSunEventCalc,omitempty"`
	LastDayPhaseCalc    time.Time `json:"lastDayPhaseCalc,omitempty"`
	NextTransitionTime  time.Time `json:"nextTransitionTime,omitempty"`
	NextTransitionPhase string    `json:"nextTransitionPhase,omitempty"`
}

// ============================================================================
// TV Plugin Output Types
// ============================================================================

// TVOutputs tracks computed TV states
type TVOutputs struct {
	IsAppleTVPlaying   bool      `json:"isAppleTVPlaying"`
	IsTVOn             bool      `json:"isTVOn"`
	IsTVPlaying        bool      `json:"isTVPlaying"`
	CurrentHDMIInput   string    `json:"currentHDMIInput,omitempty"`
	AppleTVState       string    `json:"appleTVState,omitempty"`
	LastUpdate         time.Time `json:"lastUpdate"`
	SyncBoxAvailable   bool      `json:"syncBoxAvailable"`
	LastSyncBoxReboot  time.Time `json:"lastSyncBoxReboot,omitempty"`
	DailyRebootCount   int       `json:"dailyRebootCount"`
	LastBraviaReload   time.Time `json:"lastBraviaReload,omitempty"`
	BraviaReloadCount  int       `json:"braviaReloadCount"`
	BraviaReloadFailed bool      `json:"braviaReloadFailed"`
}

// ============================================================================
// Sex Mode Plugin Output Types
// ============================================================================

// SexModeOutputs tracks the state of sex mode outputs
type SexModeOutputs struct {
	IsActive         bool      `json:"isActive"`
	PreSexMusicType  string    `json:"preSexMusicType,omitempty"`
	ActivatedAt      time.Time `json:"activatedAt,omitempty"`
	LastActionTime   time.Time `json:"lastActionTime"`
	LastActionType   string    `json:"lastActionType,omitempty"` // "activate" or "deactivate"
	LastActionReason string    `json:"lastActionReason,omitempty"`
}

// ============================================================================
// Christmas Plugin Output Types
// ============================================================================

// ChristmasOutputs tracks the state of christmas outputs
type ChristmasOutputs struct {
	LastActivationTime time.Time `json:"lastActivationTime,omitempty"`
	LightsActivated    int       `json:"lightsActivated"`
	LastActionReason   string    `json:"lastActionReason,omitempty"`
}

// ============================================================================
// System Shadow State - Connection Health Metrics
// ============================================================================

// SystemOutputs tracks system-level metrics
type SystemOutputs struct {
	ConnectionHealth ConnectionHealthMetrics `json:"connectionHealth"`
}

// ConnectionHealthMetrics tracks Home Assistant connection health
type ConnectionHealthMetrics struct {
	IsConnected         bool          `json:"isConnected"`
	IsHealthy           bool          `json:"isHealthy"`
	ReconnectCount      int           `json:"reconnectCount"`
	DisconnectCount     int           `json:"disconnectCount"`
	LastDisconnectTime  time.Time     `json:"lastDisconnectTime,omitempty"`
	WriteTimeoutCount   int           `json:"writeTimeoutCount"`
	CurrentConnDuration time.Duration `json:"currentConnDuration"`
	LastCheck           time.Time     `json:"lastCheck"`
}

// ============================================================================
// Environmental Monitoring Output Types
// ============================================================================

// EnvironmentalOutputs tracks computed environmental states and notification history
type EnvironmentalOutputs struct {
	HumiditySensors      []HumiditySensorData   `json:"humiditySensors"`                // All discovered humidity sensors
	WaterLeakSensors     []WaterLeakSensorData  `json:"waterLeakSensors"`               // All discovered water leak sensors
	ActiveWaterLeaks     []WaterLeakAlert       `json:"activeWaterLeaks,omitempty"`     // Active water leak alerts
	AlertLevel           string                 `json:"alertLevel"`                     // Overall humidity level: "none", "warning", "critical"
	ConditionStartTime   time.Time              `json:"conditionStartTime,omitempty"`   // When current condition started
	IsSustained          bool                   `json:"isSustained"`                    // Whether condition is sustained (30+ min)
	OutdoorHumidity      float64                `json:"outdoorHumidity"`                // Current outdoor reference humidity
	OutdoorHumidityValid bool                   `json:"outdoorHumidityValid"`           // Whether outdoor reading is available
	LastNotification     *NotificationRecord    `json:"lastNotification,omitempty"`     // Last alert notification sent
	LastResolutionNotice *NotificationRecord    `json:"lastResolutionNotice,omitempty"` // Last resolution notification sent
	LastWaterLeakNotice  *WaterLeakNotification `json:"lastWaterLeakNotice,omitempty"`  // Last water leak notification sent
	LastUpdate           time.Time              `json:"lastUpdate"`
}

// HumiditySensorData represents a single humidity sensor's state for shadow tracking
type HumiditySensorData struct {
	EntityID        string  `json:"entityId"`
	FriendlyName    string  `json:"friendlyName"`
	DeviceID        string  `json:"deviceId,omitempty"`
	IsIndoor        bool    `json:"isIndoor"`        // true = alerts enabled, false = informational only
	IsUnconditioned bool    `json:"isUnconditioned"` // true = unconditioned space (barn, attic) with relaxed thresholds
	Value           float64 `json:"value"`
	Valid           bool    `json:"valid"`
}

// WaterLeakSensorData represents a discovered water leak sensor
type WaterLeakSensorData struct {
	EntityID     string    `json:"entityId"`
	FriendlyName string    `json:"friendlyName"`
	DeviceID     string    `json:"deviceId,omitempty"`
	State        string    `json:"state"` // "on" = leak detected, "off" = no leak
	LastChanged  time.Time `json:"lastChanged,omitempty"`
}

// WaterLeakAlert represents an active water leak alert
type WaterLeakAlert struct {
	EntityID         string    `json:"entityId"`
	FriendlyName     string    `json:"friendlyName"`
	DetectedAt       time.Time `json:"detectedAt"`
	NotificationSent bool      `json:"notificationSent"`
}

// WaterLeakNotification tracks a water leak notification that was sent
type WaterLeakNotification struct {
	EntityID     string    `json:"entityId"`
	FriendlyName string    `json:"friendlyName"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

// NotificationRecord tracks a notification that was sent
type NotificationRecord struct {
	Level           string    `json:"level"` // "warning", "critical", "resolved"
	Message         string    `json:"message"`
	SensorLocations []string  `json:"sensorLocations,omitempty"` // Which sensors triggered it
	Timestamp       time.Time `json:"timestamp"`
}

// ============================================================================
// Sensor Health Output Types - Battery, Staleness, and Temperature Lockup Monitoring
// ============================================================================

// SensorHealthOutputs tracks discovered sensors, alerts, and notification history
type SensorHealthOutputs struct {
	BatterySensors                 []BatterySensorData         `json:"batterySensors"`
	TemperatureSensors             []TemperatureSensorData     `json:"temperatureSensors,omitempty"` // Temperature sensors for lockup monitoring
	NodeStatuses                   []NodeStatusData            `json:"nodeStatuses,omitempty"`       // Z-Wave node status sensors
	LowBatteryAlerts               []LowBatteryAlert           `json:"lowBatteryAlerts,omitempty"`
	DeadDeviceAlerts               []DeadDeviceAlert           `json:"deadDeviceAlerts,omitempty"` // Dead Z-Wave devices
	LastNotification               *SensorHealthNotification   `json:"lastNotification,omitempty"`
	LastTemperatureLockupNotice    *TemperatureLockupNotice    `json:"lastTemperatureLockupNotice,omitempty"`    // Last temperature lockup notification sent
	LastTemperatureRecoveryNotice  *TemperatureRecoveryNotice  `json:"lastTemperatureRecoveryNotice,omitempty"`  // Last temperature recovery notification sent
	LastDeadDeviceNotification     *DeadDeviceNotification     `json:"lastDeadDeviceNotification,omitempty"`     // Last dead device notification sent
	LastDeviceRecoveryNotification *DeviceRecoveryNotification `json:"lastDeviceRecoveryNotification,omitempty"` // Last device recovery notification sent
	Config                         SensorHealthConfig          `json:"config"`
	LastUpdate                     time.Time                   `json:"lastUpdate"`
	LastDiscoveryRefresh           time.Time                   `json:"lastDiscoveryRefresh,omitempty"`
}

// SensorHealthConfig holds configurable thresholds
type SensorHealthConfig struct {
	LowBatteryThreshold int `json:"lowBatteryThreshold"` // Percentage threshold (default 20)
}

// NodeStatusData represents a Z-Wave node status sensor for shadow state tracking
type NodeStatusData struct {
	EntityID    string    `json:"entityId"`
	DeviceID    string    `json:"deviceId,omitempty"`
	DeviceName  string    `json:"deviceName"`
	Status      string    `json:"status"` // alive, asleep, awake, dead
	LastChanged time.Time `json:"lastChanged,omitempty"`
}

// DeadDeviceAlert represents an active dead device alert
type DeadDeviceAlert struct {
	EntityID         string    `json:"entityId"`
	DeviceName       string    `json:"deviceName"`
	DetectedAt       time.Time `json:"detectedAt"`
	NotificationSent bool      `json:"notificationSent"`
}

// DeadDeviceNotification tracks a dead device notification that was sent
type DeadDeviceNotification struct {
	EntityID   string    `json:"entityId"`
	DeviceName string    `json:"deviceName"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

// DeviceRecoveryNotification tracks a device recovery notification that was sent
type DeviceRecoveryNotification struct {
	EntityID   string    `json:"entityId"`
	DeviceName string    `json:"deviceName"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

// TemperatureSensorData represents a single temperature sensor's state for lockup monitoring
type TemperatureSensorData struct {
	EntityID        string    `json:"entityId"`
	FriendlyName    string    `json:"friendlyName"`
	DeviceID        string    `json:"deviceId,omitempty"`
	Value           float64   `json:"value"`
	Valid           bool      `json:"valid"`
	LastValueChange time.Time `json:"lastValueChange,omitempty"` // When the value last changed
	IsLockedUp      bool      `json:"isLockedUp"`                // Whether sensor is currently locked up
}

// TemperatureLockupNotice tracks a temperature lockup notification that was sent
type TemperatureLockupNotice struct {
	EntityID     string    `json:"entityId"`
	FriendlyName string    `json:"friendlyName"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

// TemperatureRecoveryNotice tracks a temperature recovery notification that was sent
type TemperatureRecoveryNotice struct {
	EntityID     string    `json:"entityId"`
	FriendlyName string    `json:"friendlyName"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

// SensorHealthNotification tracks a notification that was sent by sensor health plugin
type SensorHealthNotification struct {
	AlertType string    `json:"alertType"` // "low_battery", "stale_sensor", "temperature_lockup"
	EntityID  string    `json:"entityId"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// BatterySensorData represents a discovered battery sensor
type BatterySensorData struct {
	EntityID      string    `json:"entityId"`
	FriendlyName  string    `json:"friendlyName"`
	DeviceID      string    `json:"deviceId,omitempty"`
	BatteryLevel  float64   `json:"batteryLevel"` // Percentage 0-100
	IsLow         bool      `json:"isLow"`        // True if below threshold
	LastChanged   time.Time `json:"lastChanged,omitempty"`
	LastReported  time.Time `json:"lastReported,omitempty"`
	IsUnavailable bool      `json:"isUnavailable"` // True if state is "unavailable"
}

// LowBatteryAlert represents an active low battery alert
type LowBatteryAlert struct {
	EntityID         string    `json:"entityId"`
	FriendlyName     string    `json:"friendlyName"`
	BatteryLevel     float64   `json:"batteryLevel"`
	DetectedAt       time.Time `json:"detectedAt"`
	NotificationSent bool      `json:"notificationSent"`
}

// ============================================================================
// Infrastructure Output Types - Aerobic Septic System Monitoring
// ============================================================================

// InfrastructureOutputs tracks septic system state and alerts
type InfrastructureOutputs struct {
	SepticSystemStatus  SepticSystemStatus          `json:"septicSystemStatus"`
	ThermostatStatus    ThermostatStatus            `json:"thermostatStatus"`
	ActiveAlerts        []InfrastructureAlert       `json:"activeAlerts,omitempty"`
	LastNotification    *InfrastructureNotification `json:"lastNotification,omitempty"`
	LastTTSAnnouncement *InfrastructureTTS          `json:"lastTTSAnnouncement,omitempty"`
	LastUpdate          time.Time                   `json:"lastUpdate"`
}

// ThermostatStatus tracks the current state of monitored thermostats
type ThermostatStatus struct {
	WellThermostat ThermostatState `json:"wellThermostat"`
	BarnThermostat ThermostatState `json:"barnThermostat"`
}

// ThermostatState represents a single thermostat's state
type ThermostatState struct {
	EntityID    string    `json:"entityId"`
	HVACAction  string    `json:"hvacAction"`  // "heating", "cooling", "idle", "off"
	CurrentTemp float64   `json:"currentTemp"` // Current temperature
	TargetTemp  float64   `json:"targetTemp"`  // Target temperature
	LastChanged time.Time `json:"lastChanged"`
	IsActive    bool      `json:"isActive"` // true if heating/cooling
}

// SepticSystemStatus tracks the current septic system operational state
type SepticSystemStatus struct {
	CurrentPowerW       float64   `json:"currentPowerW"`
	SystemState         string    `json:"systemState"`                   // "normal", "aerator_failure", "pump_stuck"
	LastNormalPowerTime time.Time `json:"lastNormalPowerTime"`           // Last time power was in normal range (50-300W)
	AeratorFailureStart time.Time `json:"aeratorFailureStart,omitempty"` // When low power (<50W) condition started
	PumpRunningStart    time.Time `json:"pumpRunningStart,omitempty"`    // When high power (>300W) condition started
	IsAlerting          bool      `json:"isAlerting"`                    // Whether an alert is currently active
}

// InfrastructureAlert represents an active infrastructure alert
type InfrastructureAlert struct {
	AlertType  string    `json:"alertType"` // "aerator_failure", "pump_stuck"
	Message    string    `json:"message"`
	DetectedAt time.Time `json:"detectedAt"`
	Severity   string    `json:"severity"` // "warning", "urgent"
}

// InfrastructureNotification records a notification that was sent
type InfrastructureNotification struct {
	AlertType string    `json:"alertType"`
	Message   string    `json:"message"`
	Priority  string    `json:"priority"`
	Timestamp time.Time `json:"timestamp"`
}

// InfrastructureTTS records a TTS announcement that was made
type InfrastructureTTS struct {
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ============================================================================
// Sensor Config Output Types - Zigbee Sensor Threshold Configuration
// ============================================================================

// SensorConfigOutputs tracks what was configured and when
type SensorConfigOutputs struct {
	Configurations []ThresholdConfiguration `json:"configurations"`
	ConfiguredAt   time.Time                `json:"configuredAt,omitempty"`
	LastUpdate     time.Time                `json:"lastUpdate"`
}

// ThresholdConfiguration represents a sensor threshold configuration
type ThresholdConfiguration struct {
	ConfigType         string    `json:"configType"`         // e.g., "temperature_report_threshold"
	Description        string    `json:"description"`        // Human-readable description
	Value              float64   `json:"value"`              // The configured value
	ConfiguredEntities []string  `json:"configuredEntities"` // Successfully configured entities
	FailedEntities     []string  `json:"failedEntities"`     // Entities that failed to configure
	ConfiguredAt       time.Time `json:"configuredAt"`
}

// ============================================================================
// Water Flow Monitoring Output Types
// ============================================================================

// WaterFlowOutputs tracks water flow state and alerts
type WaterFlowOutputs struct {
	CurrentFlowRateGPM       float64          `json:"currentFlowRateGPM"`
	AlertLevel               string           `json:"alertLevel"` // "none", "warning", "urgent"
	WarningThresholdStart    *time.Time       `json:"warningThresholdStart,omitempty"`
	UrgentThresholdStart     *time.Time       `json:"urgentThresholdStart,omitempty"`
	RecoveryStart            *time.Time       `json:"recoveryStart,omitempty"` // When recovery debounce started
	IsWarningConditionMet    bool             `json:"isWarningConditionMet"`
	IsUrgentConditionMet     bool             `json:"isUrgentConditionMet"`
	ActiveAlerts             []WaterFlowAlert `json:"activeAlerts,omitempty"`
	LastNotification         *WaterFlowNotice `json:"lastNotification,omitempty"`
	LastTTSAnnouncement      *time.Time       `json:"lastTTSAnnouncement,omitempty"`
	LastRecoveryNotification *WaterFlowNotice `json:"lastRecoveryNotification,omitempty"`
	LastUpdate               time.Time        `json:"lastUpdate"`
}

// WaterFlowAlert represents an active water flow alert
type WaterFlowAlert struct {
	AlertType       string    `json:"alertType"` // "warning", "urgent"
	Message         string    `json:"message"`
	FlowRateGPM     float64   `json:"flowRateGPM"`
	DurationMinutes int       `json:"durationMinutes"`
	DetectedAt      time.Time `json:"detectedAt"`
}

// WaterFlowNotice records a notification that was sent
type WaterFlowNotice struct {
	AlertType string    `json:"alertType"`
	Message   string    `json:"message"`
	Priority  string    `json:"priority"`
	Timestamp time.Time `json:"timestamp"`
}

// ============================================================================
// EV Charger Output Types - Safety Monitoring
// ============================================================================

// EVChargerOutputs tracks the state of EV charger safety monitoring
type EVChargerOutputs struct {
	// Current sensor states
	IsOverheat    bool   `json:"isOverheat"`
	IsOverCurrent bool   `json:"isOverCurrent"`
	IsOverVoltage bool   `json:"isOverVoltage"`
	IsSwitchOn    bool   `json:"isSwitchOn"`
	PowerReading  string `json:"powerReading,omitempty"`

	// Safety event tracking
	LastSafetyEvent     *EVChargerSafetyEvent `json:"lastSafetyEvent,omitempty"`
	LastShutoff         *EVChargerShutoff     `json:"lastShutoff,omitempty"`
	LastNotification    *EVChargerNotice      `json:"lastNotification,omitempty"`
	LastTTSAnnouncement *time.Time            `json:"lastTTSAnnouncement,omitempty"`
	LastRecovery        *EVChargerRecovery    `json:"lastRecovery,omitempty"`

	// Counters
	SafetyEventCount int `json:"safetyEventCount"`
	ShutoffCount     int `json:"shutoffCount"`
}

// EVChargerSafetyEvent records a safety condition detection
type EVChargerSafetyEvent struct {
	ConditionType string    `json:"conditionType"` // "overheat", "over-current", "over-voltage"
	Sensor        string    `json:"sensor"`
	Timestamp     time.Time `json:"timestamp"`
}

// EVChargerShutoff records an emergency shutoff
type EVChargerShutoff struct {
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

// EVChargerNotice records a notification that was sent
type EVChargerNotice struct {
	ConditionType string    `json:"conditionType"`
	Message       string    `json:"message"`
	Timestamp     time.Time `json:"timestamp"`
}

// EVChargerRecovery records a recovery from a safety condition
type EVChargerRecovery struct {
	ConditionType string    `json:"conditionType"`
	Timestamp     time.Time `json:"timestamp"`
}
