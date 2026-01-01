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

// ActionRecord represents a single action taken by a plugin
type ActionRecord struct {
	Timestamp  time.Time              `json:"timestamp"`
	ActionType string                 `json:"actionType"`
	Reason     string                 `json:"reason"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

// LightingShadowState represents the shadow state for the lighting plugin
type LightingShadowState struct {
	Plugin   string          `json:"plugin"`
	Inputs   LightingInputs  `json:"inputs"`
	Outputs  LightingOutputs `json:"outputs"`
	Metadata StateMetadata   `json:"metadata"`
}

// LightingInputs tracks current and last-action input values
type LightingInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

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

// GetCurrentInputs implements PluginShadowState
func (l *LightingShadowState) GetCurrentInputs() map[string]interface{} {
	return l.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (l *LightingShadowState) GetLastActionInputs() map[string]interface{} {
	return l.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (l *LightingShadowState) GetOutputs() interface{} {
	return l.Outputs
}

// GetMetadata implements PluginShadowState
func (l *LightingShadowState) GetMetadata() StateMetadata {
	return l.Metadata
}

// NewLightingShadowState creates a new lighting shadow state
func NewLightingShadowState() *LightingShadowState {
	return &LightingShadowState{
		Plugin: "lighting",
		Inputs: LightingInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: LightingOutputs{
			Rooms:          make(map[string]RoomState),
			LastActionTime: time.Time{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "lighting",
		},
	}
}

// MusicShadowState represents the shadow state for the music plugin
type MusicShadowState struct {
	Plugin   string        `json:"plugin"`
	Inputs   MusicInputs   `json:"inputs"`
	Outputs  MusicOutputs  `json:"outputs"`
	Metadata StateMetadata `json:"metadata"`
}

// MusicInputs tracks current and last-action input values
type MusicInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// SecurityShadowState represents the shadow state for the security plugin
type SecurityShadowState struct {
	Plugin   string          `json:"plugin"`
	Inputs   SecurityInputs  `json:"inputs"`
	Outputs  SecurityOutputs `json:"outputs"`
	Metadata StateMetadata   `json:"metadata"`
}

// SecurityInputs tracks current and last-action input values
type SecurityInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// MusicOutputs tracks the state of music control outputs
type MusicOutputs struct {
	CurrentMode          string                      `json:"currentMode,omitempty"` // e.g., "morning", "working", "evening"
	ActivePlaylist       PlaylistInfo                `json:"activePlaylist,omitempty"`
	SpeakerGroup         []SpeakerState              `json:"speakerGroup,omitempty"`
	FadeState            string                      `json:"fadeState"`        // "idle", "fading_in", "fading_out"
	PlaylistRotation     map[string]int              `json:"playlistRotation"` // Music type -> playlist number
	LastActionTime       time.Time                   `json:"lastActionTime"`
	LastActionType       string                      `json:"lastActionType,omitempty"` // "select_mode", "start_playback", "fade_out", etc.
	LastActionReason     string                      `json:"lastActionReason,omitempty"`
	PlaybackVerification *PlaybackVerificationStatus `json:"playbackVerification,omitempty"`
}

// PlaybackVerificationStatus tracks whether playback was verified to start
type PlaybackVerificationStatus struct {
	Verified       bool      `json:"verified"`             // Whether playback was confirmed to start
	AttemptsNeeded int       `json:"attemptsNeeded"`       // Number of attempts needed (1 = first try worked)
	FinalState     string    `json:"finalState,omitempty"` // The media_player state when verified
	VerifiedAt     time.Time `json:"verifiedAt"`           // When verification completed
	LeadSpeaker    string    `json:"leadSpeaker"`          // Which speaker was checked
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

// GetCurrentInputs implements PluginShadowState
func (m *MusicShadowState) GetCurrentInputs() map[string]interface{} {
	return m.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (m *MusicShadowState) GetLastActionInputs() map[string]interface{} {
	return m.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (m *MusicShadowState) GetOutputs() interface{} {
	return m.Outputs
}

// GetMetadata implements PluginShadowState
func (m *MusicShadowState) GetMetadata() StateMetadata {
	return m.Metadata
}

// NewMusicShadowState creates a new music shadow state
func NewMusicShadowState() *MusicShadowState {
	return &MusicShadowState{
		Plugin: "music",
		Inputs: MusicInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: MusicOutputs{
			SpeakerGroup:     make([]SpeakerState, 0),
			PlaylistRotation: make(map[string]int),
			FadeState:        "idle",
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "music",
		},
	}
}

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

// GetCurrentInputs implements PluginShadowState
func (s *SecurityShadowState) GetCurrentInputs() map[string]interface{} {
	return s.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (s *SecurityShadowState) GetLastActionInputs() map[string]interface{} {
	return s.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (s *SecurityShadowState) GetOutputs() interface{} {
	return s.Outputs
}

// GetMetadata implements PluginShadowState
func (s *SecurityShadowState) GetMetadata() StateMetadata {
	return s.Metadata
}

// NewSecurityShadowState creates a new security shadow state
func NewSecurityShadowState() *SecurityShadowState {
	return &SecurityShadowState{
		Plugin: "security",
		Inputs: SecurityInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: SecurityOutputs{
			Lockdown:       LockdownState{},
			LastActionTime: time.Time{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "security",
		},
	}
}

// LoadSheddingShadowState represents the shadow state for the load shedding plugin
type LoadSheddingShadowState struct {
	Plugin   string              `json:"plugin"`
	Inputs   LoadSheddingInputs  `json:"inputs"`
	Outputs  LoadSheddingOutputs `json:"outputs"`
	Metadata StateMetadata       `json:"metadata"`
}

// LoadSheddingInputs tracks current and last-action input values
type LoadSheddingInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// SleepHygieneShadowState represents the shadow state for the sleep hygiene plugin
type SleepHygieneShadowState struct {
	Plugin   string              `json:"plugin"`
	Inputs   SleepHygieneInputs  `json:"inputs"`
	Outputs  SleepHygieneOutputs `json:"outputs"`
	Metadata StateMetadata       `json:"metadata"`
}

// SleepHygieneInputs tracks current and last-action input values
type SleepHygieneInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// LoadSheddingOutputs tracks the state of load shedding control outputs
type LoadSheddingOutputs struct {
	Active             bool               `json:"active"`
	LastActionType     string             `json:"lastActionType,omitempty"` // "enable" or "disable"
	LastActionReason   string             `json:"lastActionReason,omitempty"`
	ThermostatSettings ThermostatSettings `json:"thermostatSettings,omitempty"`
	LastActionTime     time.Time          `json:"lastActionTime"`
}

// ThermostatSettings represents thermostat configuration
type ThermostatSettings struct {
	HoldMode bool    `json:"holdMode"`
	TempLow  float64 `json:"tempLow,omitempty"`
	TempHigh float64 `json:"tempHigh,omitempty"`
}

// GetCurrentInputs implements PluginShadowState
func (ls *LoadSheddingShadowState) GetCurrentInputs() map[string]interface{} {
	return ls.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (ls *LoadSheddingShadowState) GetLastActionInputs() map[string]interface{} {
	return ls.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (ls *LoadSheddingShadowState) GetOutputs() interface{} {
	return ls.Outputs
}

// GetMetadata implements PluginShadowState
func (ls *LoadSheddingShadowState) GetMetadata() StateMetadata {
	return ls.Metadata
}

// NewLoadSheddingShadowState creates a new load shedding shadow state
func NewLoadSheddingShadowState() *LoadSheddingShadowState {
	return &LoadSheddingShadowState{
		Plugin: "loadshedding",
		Inputs: LoadSheddingInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: LoadSheddingOutputs{
			Active:         false,
			LastActionTime: time.Time{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "loadshedding",
		},
	}
}

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
	SpeakerEntityID string    `json:"speakerEntityID"`
	CurrentVolume   int       `json:"currentVolume"`
	StartVolume     int       `json:"startVolume"`
	IsActive        bool      `json:"isActive"` // Is fade-out currently in progress
	StartTime       time.Time `json:"startTime"`
	LastUpdate      time.Time `json:"lastUpdate"`
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

// GetCurrentInputs implements PluginShadowState
func (s *SleepHygieneShadowState) GetCurrentInputs() map[string]interface{} {
	return s.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (s *SleepHygieneShadowState) GetLastActionInputs() map[string]interface{} {
	return s.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (s *SleepHygieneShadowState) GetOutputs() interface{} {
	return s.Outputs
}

// GetMetadata implements PluginShadowState
func (s *SleepHygieneShadowState) GetMetadata() StateMetadata {
	return s.Metadata
}

// NewSleepHygieneShadowState creates a new sleep hygiene shadow state
func NewSleepHygieneShadowState() *SleepHygieneShadowState {
	return &SleepHygieneShadowState{
		Plugin: "sleephygiene",
		Inputs: SleepHygieneInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: SleepHygieneOutputs{
			WakeSequenceStatus: "inactive",
			FadeOutProgress:    make(map[string]SpeakerFadeOut),
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "sleephygiene",
		},
	}
}

// ============================================================================
// Phase 6: Read-Heavy Plugin Shadow States
// ============================================================================

// EnergyShadowState represents the shadow state for the energy plugin
type EnergyShadowState struct {
	Plugin   string        `json:"plugin"`
	Inputs   EnergyInputs  `json:"inputs"`
	Outputs  EnergyOutputs `json:"outputs"`
	Metadata StateMetadata `json:"metadata"`
}

// EnergyInputs tracks current sensor values (no at-last-action for read-heavy plugins)
type EnergyInputs struct {
	Current map[string]interface{} `json:"current"`
}

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

// GetCurrentInputs implements PluginShadowState
func (e *EnergyShadowState) GetCurrentInputs() map[string]interface{} {
	return e.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
// For read-heavy plugins, this returns the same as current (no actions to track)
func (e *EnergyShadowState) GetLastActionInputs() map[string]interface{} {
	return e.Inputs.Current
}

// GetOutputs implements PluginShadowState
func (e *EnergyShadowState) GetOutputs() interface{} {
	return e.Outputs
}

// GetMetadata implements PluginShadowState
func (e *EnergyShadowState) GetMetadata() StateMetadata {
	return e.Metadata
}

// NewEnergyShadowState creates a new energy shadow state
func NewEnergyShadowState() *EnergyShadowState {
	return &EnergyShadowState{
		Plugin: "energy",
		Inputs: EnergyInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: EnergyOutputs{
			LastComputations: EnergyComputations{},
			SensorReadings:   EnergySensorReadings{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "energy",
		},
	}
}

// StateTrackingShadowState represents the shadow state for the state tracking plugin
type StateTrackingShadowState struct {
	Plugin   string               `json:"plugin"`
	Inputs   StateTrackingInputs  `json:"inputs"`
	Outputs  StateTrackingOutputs `json:"outputs"`
	Metadata StateMetadata        `json:"metadata"`
}

// StateTrackingInputs tracks current input sensor states
type StateTrackingInputs struct {
	Current map[string]interface{} `json:"current"`
}

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

// GetCurrentInputs implements PluginShadowState
func (s *StateTrackingShadowState) GetCurrentInputs() map[string]interface{} {
	return s.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (s *StateTrackingShadowState) GetLastActionInputs() map[string]interface{} {
	return s.Inputs.Current
}

// GetOutputs implements PluginShadowState
func (s *StateTrackingShadowState) GetOutputs() interface{} {
	return s.Outputs
}

// GetMetadata implements PluginShadowState
func (s *StateTrackingShadowState) GetMetadata() StateMetadata {
	return s.Metadata
}

// NewStateTrackingShadowState creates a new state tracking shadow state
func NewStateTrackingShadowState() *StateTrackingShadowState {
	return &StateTrackingShadowState{
		Plugin: "statetracking",
		Inputs: StateTrackingInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: StateTrackingOutputs{
			DerivedStates: DerivedStates{},
			TimerStates:   StateTrackingTimers{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "statetracking",
		},
	}
}

// DayPhaseShadowState represents the shadow state for the day phase plugin
type DayPhaseShadowState struct {
	Plugin   string          `json:"plugin"`
	Inputs   DayPhaseInputs  `json:"inputs"`
	Outputs  DayPhaseOutputs `json:"outputs"`
	Metadata StateMetadata   `json:"metadata"`
}

// DayPhaseInputs tracks current time and sun position inputs
type DayPhaseInputs struct {
	Current map[string]interface{} `json:"current"`
}

// DayPhaseOutputs tracks computed day phase and sun event values
type DayPhaseOutputs struct {
	SunEvent            string    `json:"sunevent"`
	DayPhase            string    `json:"dayPhase"`
	LastSunEventCalc    time.Time `json:"lastSunEventCalc,omitempty"`
	LastDayPhaseCalc    time.Time `json:"lastDayPhaseCalc,omitempty"`
	NextTransitionTime  time.Time `json:"nextTransitionTime,omitempty"`
	NextTransitionPhase string    `json:"nextTransitionPhase,omitempty"`
}

// GetCurrentInputs implements PluginShadowState
func (d *DayPhaseShadowState) GetCurrentInputs() map[string]interface{} {
	return d.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (d *DayPhaseShadowState) GetLastActionInputs() map[string]interface{} {
	return d.Inputs.Current
}

// GetOutputs implements PluginShadowState
func (d *DayPhaseShadowState) GetOutputs() interface{} {
	return d.Outputs
}

// GetMetadata implements PluginShadowState
func (d *DayPhaseShadowState) GetMetadata() StateMetadata {
	return d.Metadata
}

// NewDayPhaseShadowState creates a new day phase shadow state
func NewDayPhaseShadowState() *DayPhaseShadowState {
	return &DayPhaseShadowState{
		Plugin: "dayphase",
		Inputs: DayPhaseInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: DayPhaseOutputs{},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "dayphase",
		},
	}
}

// TVShadowState represents the shadow state for the TV plugin
type TVShadowState struct {
	Plugin   string        `json:"plugin"`
	Inputs   TVInputs      `json:"inputs"`
	Outputs  TVOutputs     `json:"outputs"`
	Metadata StateMetadata `json:"metadata"`
}

// TVInputs tracks current TV-related entity states
type TVInputs struct {
	Current map[string]interface{} `json:"current"`
}

// TVOutputs tracks computed TV states
type TVOutputs struct {
	IsAppleTVPlaying  bool      `json:"isAppleTVPlaying"`
	IsTVOn            bool      `json:"isTVOn"`
	IsTVPlaying       bool      `json:"isTVPlaying"`
	CurrentHDMIInput  string    `json:"currentHDMIInput,omitempty"`
	AppleTVState      string    `json:"appleTVState,omitempty"`
	LastUpdate        time.Time `json:"lastUpdate"`
	SyncBoxAvailable  bool      `json:"syncBoxAvailable"`
	LastSyncBoxReboot time.Time `json:"lastSyncBoxReboot,omitempty"`
	DailyRebootCount  int       `json:"dailyRebootCount"`
}

// GetCurrentInputs implements PluginShadowState
func (t *TVShadowState) GetCurrentInputs() map[string]interface{} {
	return t.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (t *TVShadowState) GetLastActionInputs() map[string]interface{} {
	return t.Inputs.Current
}

// GetOutputs implements PluginShadowState
func (t *TVShadowState) GetOutputs() interface{} {
	return t.Outputs
}

// GetMetadata implements PluginShadowState
func (t *TVShadowState) GetMetadata() StateMetadata {
	return t.Metadata
}

// NewTVShadowState creates a new TV shadow state
func NewTVShadowState() *TVShadowState {
	return &TVShadowState{
		Plugin: "tv",
		Inputs: TVInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: TVOutputs{
			SyncBoxAvailable: true, // Assume available until proven otherwise
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "tv",
		},
	}
}

// SexModeShadowState represents the shadow state for the sex mode plugin
type SexModeShadowState struct {
	Plugin   string         `json:"plugin"`
	Inputs   SexModeInputs  `json:"inputs"`
	Outputs  SexModeOutputs `json:"outputs"`
	Metadata StateMetadata  `json:"metadata"`
}

// ChristmasShadowState represents the shadow state for the christmas plugin
type ChristmasShadowState struct {
	Plugin   string           `json:"plugin"`
	Inputs   ChristmasInputs  `json:"inputs"`
	Outputs  ChristmasOutputs `json:"outputs"`
	Metadata StateMetadata    `json:"metadata"`
}

// ChristmasInputs tracks current and last-action input values
type ChristmasInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// ChristmasOutputs tracks the state of christmas outputs
type ChristmasOutputs struct {
	LastActivationTime time.Time `json:"lastActivationTime,omitempty"`
	LightsActivated    int       `json:"lightsActivated"`
	LastActionReason   string    `json:"lastActionReason,omitempty"`
}

// GetCurrentInputs implements PluginShadowState
func (c *ChristmasShadowState) GetCurrentInputs() map[string]interface{} {
	return c.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (c *ChristmasShadowState) GetLastActionInputs() map[string]interface{} {
	return c.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (c *ChristmasShadowState) GetOutputs() interface{} {
	return c.Outputs
}

// GetMetadata implements PluginShadowState
func (c *ChristmasShadowState) GetMetadata() StateMetadata {
	return c.Metadata
}

// NewChristmasShadowState creates a new christmas shadow state
func NewChristmasShadowState() *ChristmasShadowState {
	return &ChristmasShadowState{
		Plugin: "christmas",
		Inputs: ChristmasInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: ChristmasOutputs{
			LightsActivated: 0,
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "christmas",
		},
	}
}

// SexModeInputs tracks current and last-action input values
type SexModeInputs struct {
	Current      map[string]interface{} `json:"current"`
	AtLastAction map[string]interface{} `json:"atLastAction"`
}

// ============================================================================
// System Shadow State - Connection Health Metrics
// ============================================================================

// SystemShadowState represents system-level metrics including connection health
type SystemShadowState struct {
	Plugin   string        `json:"plugin"`
	Inputs   SystemInputs  `json:"inputs"`
	Outputs  SystemOutputs `json:"outputs"`
	Metadata StateMetadata `json:"metadata"`
}

// SystemInputs tracks system-level inputs (mostly empty, just for consistency)
type SystemInputs struct {
	Current map[string]interface{} `json:"current"`
}

// SystemOutputs tracks system-level metrics
type SystemOutputs struct {
	ConnectionHealth ConnectionHealthMetrics `json:"connectionHealth"`
}

// ConnectionHealthMetrics tracks Home Assistant connection health
type ConnectionHealthMetrics struct {
	IsConnected    bool      `json:"isConnected"`
	IsHealthy      bool      `json:"isHealthy"`
	ReconnectCount int       `json:"reconnectCount"`
	LastCheck      time.Time `json:"lastCheck"`
}

// GetCurrentInputs implements PluginShadowState
func (s *SystemShadowState) GetCurrentInputs() map[string]interface{} {
	return s.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (s *SystemShadowState) GetLastActionInputs() map[string]interface{} {
	return s.Inputs.Current // System doesn't have actions
}

// GetOutputs implements PluginShadowState
func (s *SystemShadowState) GetOutputs() interface{} {
	return s.Outputs
}

// GetMetadata implements PluginShadowState
func (s *SystemShadowState) GetMetadata() StateMetadata {
	return s.Metadata
}

// NewSystemShadowState creates a new system shadow state
func NewSystemShadowState() *SystemShadowState {
	return &SystemShadowState{
		Plugin: "system",
		Inputs: SystemInputs{
			Current: make(map[string]interface{}),
		},
		Outputs: SystemOutputs{
			ConnectionHealth: ConnectionHealthMetrics{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "system",
		},
	}
}

// SexModeOutputs tracks the state of sex mode outputs
type SexModeOutputs struct {
	IsActive         bool      `json:"isActive"`
	PreSexMusicType  string    `json:"preSexMusicType,omitempty"`
	ActivatedAt      time.Time `json:"activatedAt,omitempty"`
	LastActionTime   time.Time `json:"lastActionTime"`
	LastActionType   string    `json:"lastActionType,omitempty"` // "activate" or "deactivate"
	LastActionReason string    `json:"lastActionReason,omitempty"`
}

// GetCurrentInputs implements PluginShadowState
func (s *SexModeShadowState) GetCurrentInputs() map[string]interface{} {
	return s.Inputs.Current
}

// GetLastActionInputs implements PluginShadowState
func (s *SexModeShadowState) GetLastActionInputs() map[string]interface{} {
	return s.Inputs.AtLastAction
}

// GetOutputs implements PluginShadowState
func (s *SexModeShadowState) GetOutputs() interface{} {
	return s.Outputs
}

// GetMetadata implements PluginShadowState
func (s *SexModeShadowState) GetMetadata() StateMetadata {
	return s.Metadata
}

// NewSexModeShadowState creates a new sex mode shadow state
func NewSexModeShadowState() *SexModeShadowState {
	return &SexModeShadowState{
		Plugin: "sexmode",
		Inputs: SexModeInputs{
			Current:      make(map[string]interface{}),
			AtLastAction: make(map[string]interface{}),
		},
		Outputs: SexModeOutputs{
			IsActive:       false,
			LastActionTime: time.Time{},
		},
		Metadata: StateMetadata{
			LastUpdated: time.Now(),
			PluginName:  "sexmode",
		},
	}
}
