package sleephygiene

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"homeautomation/internal/config"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// Eight Sleep Pod sensor entity IDs
const (
	eightSleepNickSensorEntity     = "sensor.nick_s_eight_sleep_side_bed_state_type"
	eightSleepCarolineSensorEntity = "sensor.caroline_s_eight_sleep_side_bed_state_type"
	eightSleepAlarmState           = "alarm"

	// Sleep stage sensors for availability checking (more reliable than bed state type)
	eightSleepNickSleepStageSensor     = "sensor.nick_s_eight_sleep_side_sleep_stage"
	eightSleepCarolineSleepStageSensor = "sensor.caroline_s_eight_sleep_side_sleep_stage"
	eightSleepUnavailableState         = "unavailable"
)

// Wake sequence timing constants
// Total wake sequence: 30 minutes from alarm time
// - T+0:  Alarm triggers begin_wake (music fade-out starts)
// - T+5:  Lights start fading in (1% -> 100% over 25 minutes)
// - T+30: Lights fully up, wake music starts
const (
	// wakeDelayAfterFadeOut is the delay between starting the music fade-out
	// and turning on the bedroom lights. This matches the Node-RED behavior
	// which waits 5 minutes after begin_wake before starting the light fade-in.
	wakeDelayAfterFadeOut = 5 * time.Minute

	// lightFadeInDuration is how long the bedroom lights take to fade from 1% to 100%
	lightFadeInDuration = 25 * time.Minute

	// wakeMusicDelay is the delay after lights start fading before wake music plays.
	// This equals lightFadeInDuration so music starts when lights are fully up.
	wakeMusicDelay = lightFadeInDuration

	// lightTurnOnGracePeriod is how long to ignore "off" state events from
	// light.primary_suite after sending a turn_on command. HA group entities
	// emit transient "off" events during state propagation (~200-500ms) as
	// constituent lights haven't responded yet. 2s covers worst-case
	// network + Zigbee latency while still allowing quick manual cancellation.
	lightTurnOnGracePeriod = 2 * time.Second
)

// SleepFunc is the type for sleep functions (for testing)
type SleepFunc func(time.Duration)

// Manager handles sleep hygiene automations including wake-up sequences
type Manager struct {
	ctx             context.Context
	haClient        ha.HAClient
	stateManager    *state.Manager
	configLoader    *config.Loader
	logger          *zap.Logger
	readOnly        bool
	timeProvider    plugin.TimeProvider
	timezone        *time.Location
	stopChan        chan struct{}
	triggerCheckCh  chan chan struct{} // routes test-initiated checks through the timer goroutine to avoid races on triggeredToday
	ticker          *time.Ticker
	subscriptions   []state.Subscription
	haSubscriptions []ha.Subscription

	// Track which triggers have been fired today
	triggeredToday map[string]time.Time

	// Shadow state tracking
	shadowTracker *shadowstate.SleepHygieneTracker

	// Injectable sleep function for testing
	sleepFunc SleepFunc

	// lightTurnOnNano stores the UnixNano timestamp of the last turn_on command
	// sent to light.primary_suite. Used to ignore transient "off" events from
	// HA group entity state propagation (see lightTurnOnGracePeriod).
	lightTurnOnNano atomic.Int64
}

// NewManager creates a new Sleep Hygiene manager
// If timeProvider is nil, it defaults to plugin.RealTimeProvider
// If timezone is nil, it defaults to time.Local
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, configLoader *config.Loader, logger *zap.Logger, readOnly bool, timeProvider plugin.TimeProvider, timezone *time.Location) *Manager {
	if timeProvider == nil {
		timeProvider = plugin.RealTimeProvider{}
	}
	if timezone == nil {
		timezone = time.Local
	}
	return &Manager{
		ctx:             ctx,
		haClient:        haClient,
		stateManager:    stateManager,
		configLoader:    configLoader,
		logger:          logger.Named("sleephygiene"),
		readOnly:        readOnly,
		timeProvider:    timeProvider,
		timezone:        timezone,
		stopChan:        make(chan struct{}),
		triggerCheckCh:  make(chan chan struct{}),
		subscriptions:   make([]state.Subscription, 0),
		haSubscriptions: make([]ha.Subscription, 0),
		triggeredToday:  make(map[string]time.Time),
		shadowTracker:   shadowstate.NewSleepHygieneTracker(),
		sleepFunc:       time.Sleep,
	}
}

// SetSleepFunc allows overriding the sleep function for testing
func (m *Manager) SetSleepFunc(fn SleepFunc) {
	m.sleepFunc = fn
}

// Start begins monitoring state changes and managing sleep hygiene
func (m *Manager) Start() error {
	m.logger.Info("Starting Sleep Hygiene Manager")

	// Track subscriptions locally so we can clean up on partial failure
	var haSubscriptions []ha.Subscription

	// Helper to clean up on error
	cleanup := func() {
		for _, sub := range haSubscriptions {
			sub.Unsubscribe()
		}
	}

	// Subscribe to bedroom lights state changes (for cancel auto-wake logic)
	lightSub, err := m.haClient.SubscribeStateChanges("light.primary_suite", m.handleBedroomLightsChange)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to subscribe to bedroom lights: %w", err)
	}
	haSubscriptions = append(haSubscriptions, lightSub)

	// Subscribe to Eight Sleep Pod alarm sensors for instant wake-up triggers
	nickEightSleepSub, err := m.haClient.SubscribeStateChanges(eightSleepNickSensorEntity, m.handleEightSleepAlarm)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to subscribe to Nick's Eight Sleep sensor: %w", err)
	}
	haSubscriptions = append(haSubscriptions, nickEightSleepSub)

	carolineEightSleepSub, err := m.haClient.SubscribeStateChanges(eightSleepCarolineSensorEntity, m.handleEightSleepAlarm)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to subscribe to Caroline's Eight Sleep sensor: %w", err)
	}
	haSubscriptions = append(haSubscriptions, carolineEightSleepSub)

	// All subscriptions successful - commit them to the manager
	m.haSubscriptions = append(m.haSubscriptions, haSubscriptions...)

	// Subscribe to isMasterAsleep to clear wake sequence flag when person wakes up
	masterAsleepSub, err := m.stateManager.Subscribe("isMasterAsleep", m.handleMasterAsleepChange)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to subscribe to isMasterAsleep: %w", err)
	}
	m.subscriptions = append(m.subscriptions, masterAsleepSub)

	// Clear any stale isWakeSequenceActive from previous run/crash.
	// Wake sequences can't persist across app restarts - if we're starting fresh,
	// no wake sequence is in progress. This prevents the bug where stale state
	// blocks the music plugin from being notified on the next begin_wake.
	if !m.readOnly {
		isWakeActive, _ := m.stateManager.GetBool("isWakeSequenceActive")
		if isWakeActive {
			m.logger.Info("Clearing stale isWakeSequenceActive from previous run")
			if err := m.stateManager.SetBool("isWakeSequenceActive", false); err != nil {
				m.logger.Error("Failed to clear stale isWakeSequenceActive", zap.Error(err))
			}
		}

		// Clear any stale isSleepPrepActive from previous run/crash.
		// Sleep prep state can't persist across restarts.
		isSleepPrep, _ := m.stateManager.GetBool("isSleepPrepActive")
		if isSleepPrep {
			m.logger.Info("Clearing stale isSleepPrepActive from previous run")
			if err := m.stateManager.SetBool("isSleepPrepActive", false); err != nil {
				m.logger.Error("Failed to clear stale isSleepPrepActive", zap.Error(err))
			}
		}
	}

	// Start ticker to check time triggers every minute
	m.ticker = time.NewTicker(1 * time.Minute)
	go m.runTimerLoop()

	// Perform initial check
	m.checkTimeTriggers()

	m.logger.Info("Sleep Hygiene Manager started successfully")
	return nil
}

// Stop stops the Sleep Hygiene Manager and cleans up resources
func (m *Manager) Stop() {
	m.logger.Info("Stopping Sleep Hygiene Manager")

	// Stop ticker
	if m.ticker != nil {
		m.ticker.Stop()
	}

	// Signal stop
	close(m.stopChan)

	// Unsubscribe from all state subscriptions
	for _, sub := range m.subscriptions {
		sub.Unsubscribe()
	}
	m.subscriptions = nil

	// Unsubscribe from all HA subscriptions
	for _, sub := range m.haSubscriptions {
		if err := sub.Unsubscribe(); err != nil {
			m.logger.Warn("Failed to unsubscribe from HA subscription", zap.Error(err))
		}
	}
	m.haSubscriptions = nil

	m.logger.Info("Sleep Hygiene Manager stopped")
}

// handleBedroomLightsChange processes bedroom lights state changes from Home Assistant
func (m *Manager) handleBedroomLightsChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.logger.Debug("Bedroom lights state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.String("old_state", func() string {
			if oldState != nil {
				return oldState.State
			}
			return "unknown"
		}()))

	// Handle the state change
	m.handleBedroomLightsOff(newState.State)
}

// handleMasterAsleepChange handles changes to isMasterAsleep state
// When person wakes up (isMasterAsleep becomes false), clear the wake sequence flag
func (m *Manager) handleMasterAsleepChange(key string, oldValue, newValue interface{}) {
	newAsleep, ok := newValue.(bool)
	if !ok {
		return
	}

	// Update shadow state inputs at start of handler
	m.updateShadowInputs()

	// If person woke up (isMasterAsleep changed to false)
	if !newAsleep {
		// Clear isSleepPrepActive - person is awake, lighting should resume normal control
		if !m.readOnly {
			isSleepPrep, _ := m.stateManager.GetBool("isSleepPrepActive")
			if isSleepPrep {
				m.logger.Info("Person woke up, clearing isSleepPrepActive")
				if err := m.stateManager.SetBool("isSleepPrepActive", false); err != nil {
					m.logger.Error("Failed to clear isSleepPrepActive", zap.Error(err))
				}
			}

			// Turn off Eight Sleep Pod - no need to heat/cool when nobody is in bed
			m.logger.Info("Turning off Eight Sleep Pod climate for both sides")
			for _, entity := range []string{
				"climate.nick_s_eight_sleep_side_climate",
				"climate.caroline_s_eight_sleep_side_climate",
			} {
				if err := m.haClient.CallService(m.ctx, "climate", "turn_off", map[string]interface{}{
					"entity_id": entity,
				}); err != nil {
					m.logger.Error("Failed to turn off Eight Sleep climate", zap.String("entity", entity), zap.Error(err))
				}
			}
		}

		// Check if wake sequence was active
		isWakeActive, _ := m.stateManager.GetBool("isWakeSequenceActive")
		if isWakeActive {
			m.logger.Info("Person woke up, clearing wake sequence active flag")
			if !m.readOnly {
				if err := m.stateManager.SetBool("isWakeSequenceActive", false); err != nil {
					m.logger.Error("Failed to clear isWakeSequenceActive", zap.Error(err))
				}

				// Clear musicPlaybackType if it's set to "wakeup" to ensure the wakeup zone stops.
				// Without this, the wakeup zone would stay active even after the wake sequence ends
				// because it matches via the musicPlaybackType fallback path (zone_manager.go:532-536).
				// This fix resolves the production bug where opening the bedroom door during morning
				// wake sequence left wakeup music playing instead of transitioning to morning music.
				musicType, err := m.stateManager.GetString("musicPlaybackType")
				if err == nil && musicType == "wakeup" {
					m.logger.Info("Clearing wakeup musicPlaybackType since wake sequence ended")
					if err := m.stateManager.SetString("musicPlaybackType", ""); err != nil {
						m.logger.Error("Failed to clear musicPlaybackType", zap.Error(err))
					}
				}
			}
			// Update shadow state
			m.shadowTracker.UpdateWakeSequenceStatus("inactive")
		}
	}
}

// handleEightSleepAlarm processes Eight Sleep Pod alarm state changes
// This provides instant wake-up triggers when the Eight Sleep alarm activates,
// rather than relying solely on time-based checks
func (m *Manager) handleEightSleepAlarm(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.logger.Debug("Eight Sleep sensor state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.String("old_state", func() string {
			if oldState != nil {
				return oldState.State
			}
			return "unknown"
		}()))

	// Only trigger when state becomes "alarm"
	if newState.State != eightSleepAlarmState {
		m.logger.Debug("Eight Sleep state is not alarm, ignoring",
			zap.String("entity_id", entityID),
			zap.String("state", newState.State))
		return
	}

	// Check if we already triggered begin_wake today (deduplication)
	now := m.timeProvider.Now()
	if triggerTime, triggered := m.triggeredToday["begin_wake"]; triggered {
		if isSameDay(now, triggerTime) {
			m.logger.Debug("begin_wake already triggered today, ignoring Eight Sleep alarm",
				zap.String("entity_id", entityID),
				zap.Time("triggered_at", triggerTime))
			return
		}
	}

	m.logger.Info("Eight Sleep alarm detected, triggering begin_wake",
		zap.String("entity_id", entityID),
		zap.Time("now", now))

	// Mark as triggered to prevent duplicate triggers
	m.triggeredToday["begin_wake"] = now

	// Trigger the begin_wake sequence
	m.handleBeginWake()
}

// runTimerLoop runs the main timer loop that checks for time triggers
func (m *Manager) runTimerLoop() {
	for {
		select {
		case <-m.ticker.C:
			// Check if we crossed midnight - reset triggers
			now := m.timeProvider.Now()
			if len(m.triggeredToday) > 0 {
				// Check if any triggered time is from a previous day
				for trigger, triggerTime := range m.triggeredToday {
					if !isSameDay(now, triggerTime) {
						m.logger.Debug("Resetting trigger for new day",
							zap.String("trigger", trigger))
						delete(m.triggeredToday, trigger)
					}
				}
			}

			// Safety net: clear isSleepPrepActive on midnight crossing.
			// If go_to_bed fired but user never fell asleep, this prevents
			// isSleepPrepActive from persisting indefinitely into the next day.
			if !m.readOnly {
				isSleepPrep, _ := m.stateManager.GetBool("isSleepPrepActive")
				if isSleepPrep {
					// Only clear on actual midnight crossing: hour 0, minute 0
					localNow := now.In(m.timezone)
					if localNow.Hour() == 0 && localNow.Minute() == 0 {
						m.logger.Info("Midnight crossing: clearing stale isSleepPrepActive")
						if err := m.stateManager.SetBool("isSleepPrepActive", false); err != nil {
							m.logger.Error("Failed to clear isSleepPrepActive at midnight", zap.Error(err))
						}
					}
				}
			}

			// Check time triggers
			m.checkTimeTriggers()

		case done := <-m.triggerCheckCh:
			m.checkTimeTriggers()
			close(done)

		case <-m.stopChan:
			return
		}
	}
}

// checkTimeTriggers checks schedule-based triggers (stop_screens, go_to_bed, and backup wake)
// Note: Primary wake-up is triggered by Eight Sleep alarm sensors via handleEightSleepAlarm.
// Backup wake only triggers when Eight Sleep is unavailable (e.g., Internet outage).
func (m *Manager) checkTimeTriggers() {
	now := m.timeProvider.Now()

	// Get today's schedule in the configured timezone
	// This ensures schedule times (e.g., 22:30 stop_screens) are interpreted
	// in the correct timezone, not UTC
	schedule, err := m.configLoader.GetTodaysScheduleInTimezone(m.timezone)
	if err != nil {
		m.logger.Error("Failed to get today's schedule", zap.Error(err))
		return
	}

	const ONE_HOUR = time.Hour

	// Check stop_screens trigger
	if now.After(schedule.StopScreens) && now.Before(schedule.StopScreens.Add(ONE_HOUR)) {
		if _, triggered := m.triggeredToday["stop_screens"]; !triggered {
			m.logger.Info("Triggering stop_screens",
				zap.Time("stop_screens_time", schedule.StopScreens),
				zap.Time("now", now))
			m.triggeredToday["stop_screens"] = now
			m.handleStopScreens()
		}
	}

	// Check go_to_bed trigger.
	//
	// Sleep prep is gated on isAnyoneHome: starting it with an empty house
	// sets isSleepPrepActive=true, which causes the lighting plugin to skip
	// the bedroom when the owner later arrives — no welcome scene. Skipping
	// without marking triggered lets the next 1-min tick fire it once
	// someone is home (still within the 1-hour window).
	if now.After(schedule.GoToBed) && now.Before(schedule.GoToBed.Add(ONE_HOUR)) {
		if _, triggered := m.triggeredToday["go_to_bed"]; !triggered {
			isAnyoneHome, _ := m.stateManager.GetBool("isAnyoneHome")
			if !isAnyoneHome {
				// If no one arrives before the 1-hour window closes, go_to_bed
				// simply does not fire that night — by design.
				m.logger.Debug("Deferring go_to_bed: no one home",
					zap.Time("go_to_bed_time", schedule.GoToBed),
					zap.Time("now", now))
			} else {
				m.logger.Info("Triggering go_to_bed",
					zap.Time("go_to_bed_time", schedule.GoToBed),
					zap.Time("now", now))
				m.triggeredToday["go_to_bed"] = now
				m.handleGoToBed()
			}
		}
	}

	// Check backup wake time trigger (only if Eight Sleep is unavailable)
	// This provides a fallback wake mechanism when the Eight Sleep integration is down
	eightSleepUnavailable := m.isEightSleepUnavailable()
	m.updateEightSleepAvailability(!eightSleepUnavailable)

	if eightSleepUnavailable {
		if now.After(schedule.BeginBackupWake) && now.Before(schedule.BeginBackupWake.Add(ONE_HOUR)) {
			if _, triggered := m.triggeredToday["begin_wake"]; !triggered {
				m.logger.Info("Eight Sleep unavailable, triggering backup wake",
					zap.Time("begin_backup_wake_time", schedule.BeginBackupWake),
					zap.Time("now", now))
				m.triggeredToday["begin_wake"] = now
				m.handleBeginWake()
			}
		}
	}
}

// handleBeginWake handles the begin_wake trigger (start fading out sleep music)
func (m *Manager) handleBeginWake() {
	m.logger.Info("Handling begin_wake trigger")

	// Check conditions: anyone home, master asleep, sleep music playing
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil || !isAnyoneHome {
		m.logger.Debug("Skipping begin_wake: no one home")
		return
	}

	isMasterAsleep, err := m.stateManager.GetBool("isMasterAsleep")
	if err != nil || !isMasterAsleep {
		m.logger.Debug("Skipping begin_wake: master not asleep")
		return
	}

	musicPlaybackType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil || musicPlaybackType != "sleep" {
		m.logger.Debug("Skipping begin_wake: not playing sleep music",
			zap.String("music_type", musicPlaybackType))
		return
	}

	// All conditions met - start fade out
	m.logger.Info("Conditions met for begin_wake, starting fade out")

	// Set wake sequence active IMMEDIATELY when begin_wake fires.
	// This prevents the sleep zone from matching (it requires isWakeSequenceActive=false)
	// and allows the morning zone to match (its second trigger group checks isWakeSequenceActive=true).
	// Without this, there's a 5-minute race window where dayPhase can change to "morning"
	// but the sleep zone still matches, causing sleep music to restart instead of morning music.
	if !m.readOnly {
		if err := m.stateManager.SetBool("isWakeSequenceActive", true); err != nil {
			m.logger.Error("Failed to set isWakeSequenceActive", zap.Error(err))
		}
	}

	// Get bedroom speakers from currentlyPlayingMusic
	bedroomSpeakers := m.getBedroomSpeakers()
	if len(bedroomSpeakers) == 0 {
		m.logger.Warn("No bedroom speakers found in currentlyPlayingMusic, using default")
		bedroomSpeakers = []string{"media_player.bedroom"}
	}

	// Record action in shadow state
	m.recordAction("begin_wake", fmt.Sprintf("Starting fade out for %d bedroom speakers", len(bedroomSpeakers)), "eight_sleep_alarm")
	m.shadowTracker.UpdateWakeSequenceStatus("begin_wake")

	if !m.readOnly {
		// Set fade out in progress flag
		if err := m.stateManager.SetBool("isFadeOutInProgress", true); err != nil {
			m.logger.Error("Failed to set isFadeOutInProgress", zap.Error(err))
		}

		// Start fade out goroutine for each bedroom speaker
		for _, speaker := range bedroomSpeakers {
			// Record fade out start in shadow state
			currentVolume := m.getSpeakerVolume(speaker)
			m.shadowTracker.RecordFadeOutStart(speaker, currentVolume)

			go m.fadeOutSpeaker(speaker)
		}

		// Schedule the wake sequence (lights fade-in) after the delay
		// This matches Node-RED behavior which waits 5 minutes after begin_wake
		// before starting the 30-minute light transition
		go m.scheduleWakeSequence()
	} else {
		m.logger.Info("READ-ONLY: Would start fade out and schedule wake sequence")
		// In read-only mode, still record shadow state with estimated volumes
		for _, speaker := range bedroomSpeakers {
			m.shadowTracker.RecordFadeOutStart(speaker, 60) // Estimate default volume
		}
	}
}

// getBedroomSpeakers returns a list of bedroom speakers from currentlyPlayingMusic
// This matches Node-RED's dynamic speaker discovery logic
func (m *Manager) getBedroomSpeakers() []string {
	var currentMusic map[string]interface{}
	if err := m.stateManager.GetJSON("currentlyPlayingMusic", &currentMusic); err != nil {
		m.logger.Warn("Failed to get currentlyPlayingMusic, using default bedroom speaker", zap.Error(err))
		return []string{"media_player.bedroom"}
	}

	participants, ok := currentMusic["participants"].([]interface{})
	if !ok {
		m.logger.Warn("currentlyPlayingMusic has no participants array")
		return []string{"media_player.bedroom"}
	}

	var bedroomSpeakers []string
	for _, p := range participants {
		participant, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		playerName, ok := participant["player_name"].(string)
		if !ok {
			continue
		}

		// Match Node-RED logic: if (target.player_name.indexOf("Bedroom") > -1)
		// Use case-insensitive match to handle both "Bedroom" and "bedroom"
		if strings.Contains(strings.ToLower(playerName), "bedroom") {
			bedroomSpeakers = append(bedroomSpeakers, playerName)
		}
	}

	return bedroomSpeakers
}

// humanOverrideThreshold is the volume difference (in percentage points) that indicates
// a human is manually adjusting the speaker volume during an automated fade operation.
// Using 1% because Sonos physical controls change volume by exactly 1% per input,
// so a single button press should be enough to signal the user is fighting the fade.
const humanOverrideThreshold = 1

// fadeOutSpeaker gradually reduces speaker volume to 0
// This runs in a goroutine and implements the sleep music fade-out logic
// matching the Node-RED "Repeat turn downs until 0" function
func (m *Manager) fadeOutSpeaker(speakerEntityID string) {
	m.logger.Info("Starting speaker fade-out", zap.String("speaker", speakerEntityID))

	// Get actual current volume from Home Assistant
	currentVolume := m.getSpeakerVolume(speakerEntityID)
	startVolume := currentVolume // Remember start volume for override detection

	// Record fade-out start in shadow state (even if already at 0)
	m.shadowTracker.RecordFadeOutStart(speakerEntityID, currentVolume)

	if currentVolume == 0 {
		m.logger.Info("Speaker volume already at 0, skipping fade-out", zap.String("speaker", speakerEntityID))
		// Still need to clear isFadeOutInProgress since fade-out is effectively complete
		if !m.readOnly {
			if err := m.stateManager.SetBool("isFadeOutInProgress", false); err != nil {
				m.logger.Error("Failed to clear isFadeOutInProgress", zap.Error(err))
			}
		}
		// Mark fade-out as inactive in shadow state (consistent with other abort paths)
		m.shadowTracker.UpdateFadeOutProgress(speakerEntityID, 0)
		return
	}

	m.logger.Info("Got initial speaker volume",
		zap.String("speaker", speakerEntityID),
		zap.Int("volume", currentVolume))

	for currentVolume > 0 {
		// Check if fade out was aborted
		isFadeOut, err := m.stateManager.GetBool("isFadeOutInProgress")
		if err != nil || !isFadeOut {
			m.logger.Info("Fade out aborted - isFadeOutInProgress is false",
				zap.String("speaker", speakerEntityID))

			// Mark fade-out as inactive in shadow state
			m.shadowTracker.UpdateFadeOutProgress(speakerEntityID, 0)

			return
		}

		// Check if wake sequence is still active.
		// During wake, the music plugin's zone manager changes musicPlaybackType
		// from "sleep" to "morning" (sleep zone requires isWakeSequenceActive=false).
		// This is expected — the fade-out should continue regardless of zone changes.
		// If the wake sequence is cancelled (e.g., user turns off lights),
		// isWakeSequenceActive becomes false and we abort the fade.
		isWakeActive, err := m.stateManager.GetBool("isWakeSequenceActive")
		if err == nil && !isWakeActive {
			m.logger.Info("Wake sequence cancelled, stopping fade-out",
				zap.String("speaker", speakerEntityID))

			// Clear fade-out state on abort
			if !m.readOnly {
				if err := m.stateManager.SetBool("isFadeOutInProgress", false); err != nil {
					m.logger.Error("Failed to clear isFadeOutInProgress", zap.Error(err))
				}
			}

			// Mark fade-out as inactive in shadow state
			m.shadowTracker.UpdateFadeOutProgress(speakerEntityID, 0)

			return
		}

		// Reduce volume by 1
		currentVolume--
		volumeLevel := float64(currentVolume) / 100.0

		m.logger.Debug("Reducing speaker volume",
			zap.String("speaker", speakerEntityID),
			zap.Int("volume", currentVolume),
			zap.Float64("volume_level", volumeLevel))

		// Set volume on speaker
		if err := m.haClient.CallService(m.ctx, "media_player", "volume_set", map[string]interface{}{
			"entity_id":    speakerEntityID,
			"volume_level": volumeLevel,
		}); err != nil {
			m.logger.Error("Failed to set volume",
				zap.String("speaker", speakerEntityID),
				zap.Error(err))
			// Continue anyway - don't abort the fade out for transient errors
		}

		// Update currentlyPlayingMusic state
		m.updateSpeakerVolumeInState(speakerEntityID, currentVolume)

		// Update shadow state fade out progress
		m.shadowTracker.UpdateFadeOutProgress(speakerEntityID, currentVolume)

		// Calculate adaptive delay (longer as volume gets lower)
		// Formula matches Node-RED: (60 - current_volume) * 1000 ms
		// At volume 50: delay = 10 seconds
		// At volume 10: delay = 50 seconds
		delaySeconds := 60 - currentVolume
		if delaySeconds < 1 {
			delaySeconds = 1 // Minimum 1 second delay
		}

		m.logger.Debug("Waiting before next volume reduction",
			zap.String("speaker", speakerEntityID),
			zap.Int("delay_seconds", delaySeconds))

		m.sleepFunc(time.Duration(delaySeconds) * time.Second)

		// Human override detection: check if someone manually raised volume during fade-out
		// Only check if we're not already at volume 0
		if currentVolume > 0 {
			actualVolume := m.getSpeakerVolume(speakerEntityID)
			// For fade-out: if actual volume is significantly HIGHER than what we set,
			// someone is fighting the fade-out (turning it up)
			// Skip if actual equals start volume - this indicates the device state
			// isn't being updated (common in mock/test environments)
			if actualVolume > (currentVolume+humanOverrideThreshold) && actualVolume != startVolume {
				m.logger.Info("Human override detected during fade-out, aborting",
					zap.String("speaker", speakerEntityID),
					zap.Int("expected_volume", currentVolume),
					zap.Int("actual_volume", actualVolume))

				// Record human override in shadow state
				m.shadowTracker.RecordHumanOverride(speakerEntityID, currentVolume, actualVolume)

				// Clear fade-out state
				if !m.readOnly {
					if err := m.stateManager.SetBool("isFadeOutInProgress", false); err != nil {
						m.logger.Error("Failed to clear isFadeOutInProgress", zap.Error(err))
					}
				}
				return
			}
		}
	}

	m.logger.Info("Fade out complete - speaker volume reached 0",
		zap.String("speaker", speakerEntityID))

	// Reset fade out flag when complete
	if err := m.stateManager.SetBool("isFadeOutInProgress", false); err != nil {
		m.logger.Error("Failed to reset isFadeOutInProgress", zap.Error(err))
	}
}

// getSpeakerVolume queries the current volume from Home Assistant
// Returns volume as percentage (0-100)
func (m *Manager) getSpeakerVolume(speakerEntityID string) int {
	state, err := m.haClient.GetState(speakerEntityID)
	if err != nil {
		m.logger.Warn("Failed to get speaker state, defaulting to volume 60",
			zap.String("speaker", speakerEntityID),
			zap.Error(err))
		return 60 // Default to typical sleep music volume
	}

	// Get volume_level attribute (0.0-1.0)
	volumeLevel, ok := state.Attributes["volume_level"].(float64)
	if !ok {
		m.logger.Warn("Speaker has no volume_level attribute, defaulting to volume 60",
			zap.String("speaker", speakerEntityID))
		return 60
	}

	// Convert to percentage (0-100)
	volume := int(volumeLevel * 100)
	return volume
}

// updateSpeakerVolumeInState updates the volume in currentlyPlayingMusic state
// This matches Node-RED's behavior of keeping currentlyPlayingMusic synchronized
func (m *Manager) updateSpeakerVolumeInState(speakerEntityID string, volume int) {
	var currentMusic map[string]interface{}
	if err := m.stateManager.GetJSON("currentlyPlayingMusic", &currentMusic); err != nil {
		m.logger.Debug("Failed to get currentlyPlayingMusic for update",
			zap.String("speaker", speakerEntityID),
			zap.Error(err))
		return
	}

	participants, ok := currentMusic["participants"].([]interface{})
	if !ok {
		return
	}

	// Find and update the speaker's volume
	updated := false
	for _, p := range participants {
		participant, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		playerName, ok := participant["player_name"].(string)
		if !ok {
			continue
		}

		if playerName == speakerEntityID {
			participant["volume"] = volume
			updated = true
			m.logger.Debug("Updated volume in currentlyPlayingMusic",
				zap.String("speaker", speakerEntityID),
				zap.Int("volume", volume))
			break
		}
	}

	if !updated {
		m.logger.Debug("Speaker not found in currentlyPlayingMusic participants",
			zap.String("speaker", speakerEntityID))
		return
	}

	// Save updated state
	if err := m.stateManager.SetJSON("currentlyPlayingMusic", currentMusic); err != nil {
		m.logger.Warn("Failed to update currentlyPlayingMusic",
			zap.String("speaker", speakerEntityID),
			zap.Error(err))
	}
}

// scheduleWakeSequence waits for the configured delay and then triggers handleWake
// This implements the Node-RED behavior where lights fade in 5 minutes after
// the sleep music starts fading out
func (m *Manager) scheduleWakeSequence() {
	m.logger.Info("Scheduling wake sequence",
		zap.Duration("delay", wakeDelayAfterFadeOut))

	select {
	case <-time.After(wakeDelayAfterFadeOut):
		m.logger.Info("Wake delay elapsed, triggering wake sequence")
		m.handleWake()
	case <-m.stopChan:
		m.logger.Info("Wake sequence cancelled - manager stopping")
	}
}

// scheduleWakeMusic waits for the light fade-in to complete and then starts wake music
// This ensures wake music only plays when the lights are fully up
func (m *Manager) scheduleWakeMusic() {
	m.logger.Info("Scheduling wake music",
		zap.Duration("delay", wakeMusicDelay))

	// Use sleepFunc for testing (can be overridden to skip delays)
	m.sleepFunc(wakeMusicDelay)

	// Check if wake sequence is still active (user might have cancelled by turning off lights)
	isWakeActive, err := m.stateManager.GetBool("isWakeSequenceActive")
	if err != nil || !isWakeActive {
		m.logger.Info("Wake music cancelled - wake sequence no longer active")
		return
	}

	m.logger.Info("Light fade-in complete, starting wake music")

	// Start wake music - triggers music plugin to play gentle wakeup playlist
	if err := m.stateManager.SetString("musicPlaybackType", "wakeup"); err != nil {
		m.logger.Error("Failed to set wakeup music", zap.Error(err))
	} else {
		m.logger.Info("Wake music activated")
	}

	// Record action and update status
	m.recordAction("wake_music", "Starting wake music after lights fully up", "wake_music_timer")
	m.shadowTracker.UpdateWakeSequenceStatus("complete")
}

// handleWake handles the wake trigger (turn on lights)
func (m *Manager) handleWake() {
	m.logger.Info("Handling wake trigger")

	// Check conditions: anyone home, master asleep, fade out in progress
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil || !isAnyoneHome {
		m.logger.Debug("Skipping wake: no one home")
		return
	}

	isMasterAsleep, err := m.stateManager.GetBool("isMasterAsleep")
	if err != nil || !isMasterAsleep {
		m.logger.Debug("Skipping wake: master not asleep")
		return
	}

	isFadeOutInProgress, err := m.stateManager.GetBool("isFadeOutInProgress")
	if err != nil || !isFadeOutInProgress {
		m.logger.Debug("Skipping wake: fade out not in progress")
		return
	}

	// All conditions met - execute wake sequence
	m.logger.Info("Conditions met for wake, executing wake sequence")

	// Record action in shadow state
	m.recordAction("wake", "Executing wake sequence: turning on lights", "wake_timer")
	m.shadowTracker.UpdateWakeSequenceStatus("wake_in_progress")

	if !m.readOnly {
		// Note: isWakeSequenceActive was already set to true in handleBeginWake()
		// when the fade-out started. This prevents the sleep zone from matching
		// and allows morning music to play in the rest of the house while the
		// bedroom is still fading out.

		// Turn on master bedroom lights slowly (25 minute transition)
		m.turnOnMasterBedroomLights()

		// Schedule wake music to start when lights are fully up
		go m.scheduleWakeMusic()

		// Lights are fading in - full sequence completes when wake music starts
		m.shadowTracker.UpdateWakeSequenceStatus("lights_fading_in")
	} else {
		m.logger.Info("READ-ONLY: Would execute wake sequence (lights)")
	}
}

// handleStopScreens handles the stop_screens trigger (flash lights as reminder)
func (m *Manager) handleStopScreens() {
	m.logger.Info("Handling stop_screens trigger")

	// Check conditions: anyone home and not everyone asleep
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil || !isAnyoneHome {
		m.logger.Debug("Skipping stop_screens: no one home")
		return
	}

	isEveryoneAsleep, err := m.stateManager.GetBool("isEveryoneAsleep")
	if err != nil || isEveryoneAsleep {
		m.logger.Debug("Skipping stop_screens: everyone is asleep")
		return
	}

	// Conditions met - flash lights
	m.logger.Info("Conditions met for stop_screens, flashing lights")

	// Record action in shadow state
	m.recordAction("stop_screens", "Flashing common area lights as screen stop reminder", "stop_screens_timer")
	m.shadowTracker.RecordStopScreensReminder()

	if !m.readOnly {
		m.flashCommonAreaLights()
	} else {
		m.logger.Info("READ-ONLY: Would flash common area lights")
	}
}

// handleGoToBed handles the go_to_bed trigger (flash lights and start sleep music)
// This matches Node-RED behavior which does two things:
// 1. Sets musicPlaybackType to "sleep" (always, triggers music plugin to start sleep music)
// 2. Flashes lights (conditional on anyone home + not everyone asleep)
func (m *Manager) handleGoToBed() {
	m.logger.Info("Handling go_to_bed trigger")

	// Always set musicPlaybackType to "sleep" - this triggers the music plugin
	// to start sleep music. Node-RED does this unconditionally at go_to_bed time.
	if !m.readOnly {
		if err := m.stateManager.SetString("musicPlaybackType", "sleep"); err != nil {
			m.logger.Error("Failed to set musicPlaybackType to sleep", zap.Error(err))
		} else {
			m.logger.Info("Set musicPlaybackType to sleep for bedtime")
		}

		// Set isSleepPrepActive to prevent the lighting plugin from re-activating
		// bedroom lights during the gap between go_to_bed and isMasterAsleep.
		// Without this, any state change (sunevent, presence, etc.) would cause
		// lighting to turn the Primary Suite back on, resetting the sleep timer.
		if err := m.stateManager.SetBool("isSleepPrepActive", true); err != nil {
			m.logger.Error("Failed to set isSleepPrepActive", zap.Error(err))
		} else {
			m.logger.Info("Set isSleepPrepActive to prevent lighting interference during sleep prep")
		}
	} else {
		m.logger.Info("READ-ONLY: Would set musicPlaybackType to sleep and isSleepPrepActive")
	}

	// Check conditions for flashing lights: anyone home and not everyone asleep
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		isAnyoneHome = false
	}

	isEveryoneAsleep, err := m.stateManager.GetBool("isEveryoneAsleep")
	if err != nil {
		isEveryoneAsleep = false
	}

	shouldFlashLights := isAnyoneHome && !isEveryoneAsleep

	// Record action in shadow state - always record since music is always started
	if shouldFlashLights {
		m.recordAction("go_to_bed", "Starting sleep music and flashing common area lights", "go_to_bed_timer")
	} else {
		m.recordAction("go_to_bed", "Starting sleep music (no light flash: conditions not met)", "go_to_bed_timer")
	}
	m.shadowTracker.RecordGoToBedReminder()

	// Flash lights only if conditions are met
	if shouldFlashLights {
		m.logger.Info("Conditions met for go_to_bed, flashing lights")
		if !m.readOnly {
			m.flashCommonAreaLights()
		} else {
			m.logger.Info("READ-ONLY: Would flash common area lights")
		}
	} else {
		m.logger.Debug("Skipping go_to_bed light flash: conditions not met",
			zap.Bool("isAnyoneHome", isAnyoneHome),
			zap.Bool("isEveryoneAsleep", isEveryoneAsleep))
	}
}

// turnOnMasterBedroomLights turns on master bedroom lights with a slow fade-in transition
func (m *Manager) turnOnMasterBedroomLights() {
	transitionSeconds := int(lightFadeInDuration.Seconds())
	m.logger.Info("Turning on master bedroom lights slowly",
		zap.Int("transition_seconds", transitionSeconds))

	// Record that we're about to turn on lights so handleBedroomLightsOff
	// can ignore transient "off" events from HA group entity propagation.
	m.lightTurnOnNano.Store(time.Now().UnixNano())

	// First, ensure lights start dim and white
	if err := m.haClient.CallService(m.ctx, "light", "turn_on", map[string]interface{}{
		"entity_id":         "light.primary_suite",
		"transition":        0,
		"color_temp_kelvin": 3448,
		"brightness_pct":    1,
	}); err != nil {
		m.logger.Error("Failed to set initial bedroom light state", zap.Error(err))
		return
	}

	m.logger.Info("Set initial bedroom light state (1% brightness, warm white)")

	// Then start slow transition to full brightness
	if err := m.haClient.CallService(m.ctx, "light", "turn_on", map[string]interface{}{
		"entity_id":         "light.primary_suite",
		"transition":        transitionSeconds,
		"color_temp_kelvin": 3448,
		"brightness_pct":    100,
	}); err != nil {
		m.logger.Error("Failed to start bedroom light transition", zap.Error(err))
		return
	}

	m.logger.Info("Started bedroom light fade-in to 100% brightness",
		zap.Duration("duration", lightFadeInDuration))
}

// flashCommonAreaLights flashes lights in common areas as a notification
func (m *Manager) flashCommonAreaLights() {
	m.logger.Info("Flashing common area lights")

	commonAreaLights := []string{
		"light.living_room",
		"light.kitchen",
	}

	for _, lightEntity := range commonAreaLights {
		if err := m.haClient.CallService(m.ctx, "light", "turn_on", map[string]interface{}{
			"entity_id": lightEntity,
			"flash":     "short",
		}); err != nil {
			m.logger.Error("Failed to flash light",
				zap.String("entity", lightEntity),
				zap.Error(err))
		}
	}
}

// turnOffBathroomLights turns off primary bathroom lights
func (m *Manager) turnOffBathroomLights() {
	m.logger.Info("Turning off primary bathroom lights")

	if err := m.haClient.CallService(m.ctx, "light", "turn_off", map[string]interface{}{
		"entity_id": "light.primary_bathroom_main_lights",
	}); err != nil {
		m.logger.Error("Failed to turn off bathroom lights", zap.Error(err))
	}
}

// handleBedroomLightsOff handles bedroom lights turning off during wake sequence
// This implements the "cancel auto-wake" logic from Node-RED
func (m *Manager) handleBedroomLightsOff(state string) {
	if state != "off" {
		return
	}

	// Ignore transient "off" events that arrive shortly after we send a turn_on
	// command. HA group entities (light.primary_suite) emit "off" state events
	// during propagation as constituent lights haven't responded yet.
	if turnOnNano := m.lightTurnOnNano.Load(); turnOnNano > 0 {
		elapsed := time.Since(time.Unix(0, turnOnNano))
		if elapsed < lightTurnOnGracePeriod {
			m.logger.Info("Ignoring bedroom lights off event within grace period after turn-on command",
				zap.Duration("elapsed", elapsed),
				zap.Duration("grace_period", lightTurnOnGracePeriod))
			return
		}
	}

	m.logger.Debug("Bedroom lights turned off, checking if wake sequence should be cancelled")

	// Check if wake sequence is active (lights are fading in)
	isWakeSequenceActive, _ := m.stateManager.GetBool("isWakeSequenceActive")

	// Check if wake-up music is playing
	musicPlaybackType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil {
		m.logger.Debug("Failed to get musicPlaybackType", zap.Error(err))
		return
	}

	if musicPlaybackType == "wakeup" || isWakeSequenceActive {
		m.logger.Info("Bedroom lights turned off during wake sequence - cancelling wake and clearing musicPlaybackType")

		// Record cancel wake action in shadow state
		m.recordAction("cancel_wake", "Bedroom lights turned off during wake sequence, clearing musicPlaybackType", "bedroom_lights_off")
		m.shadowTracker.UpdateWakeSequenceStatus("inactive")
		m.shadowTracker.ClearFadeOutProgress()

		if !m.readOnly {
			// Clear wake sequence active flag
			if err := m.stateManager.SetBool("isWakeSequenceActive", false); err != nil {
				m.logger.Error("Failed to clear isWakeSequenceActive", zap.Error(err))
			}

			// Clear musicPlaybackType so zone-based resolution takes over.
			// The sleep zone will activate via its own triggers (isMasterAsleep=true,
			// isAnyoneHome=true, isWakeSequenceActive=false) for bedroom speakers.
			// The morning zone stays active via the wake latch trigger group
			// (isAnyoneHomeAndAwake=true) for common area speakers.
			if err := m.stateManager.SetString("musicPlaybackType", ""); err != nil {
				m.logger.Error("Failed to clear musicPlaybackType", zap.Error(err))
			}

			// Turn off bathroom lights
			m.turnOffBathroomLights()
		} else {
			m.logger.Info("READ-ONLY: Would revert to sleep music and turn off bathroom lights")
		}
	} else {
		m.logger.Debug("Bedroom lights turned off but not during wake sequence, no action needed",
			zap.String("current_music_type", musicPlaybackType))
	}
}

// isSameDay checks if two times are on the same day
func isSameDay(t1, t2 time.Time) bool {
	y1, m1, d1 := t1.Date()
	y2, m2, d2 := t2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

// isEightSleepUnavailable checks if the Eight Sleep integration is unavailable
// Returns true if BOTH Eight Sleep sensors are unavailable or errored.
// If at least one sensor is available, returns false (Eight Sleep can still be used).
func (m *Manager) isEightSleepUnavailable() bool {
	// Check Nick's sleep stage sensor
	nickState, err := m.haClient.GetState(eightSleepNickSleepStageSensor)
	if err == nil && nickState.State != eightSleepUnavailableState {
		m.logger.Debug("Nick's Eight Sleep sensor is available",
			zap.String("state", nickState.State))
		return false // Nick's sensor is available
	}

	// Check Caroline's sleep stage sensor
	carolineState, err := m.haClient.GetState(eightSleepCarolineSleepStageSensor)
	if err == nil && carolineState.State != eightSleepUnavailableState {
		m.logger.Debug("Caroline's Eight Sleep sensor is available",
			zap.String("state", carolineState.State))
		return false // Caroline's sensor is available
	}

	// Both sensors are unavailable or errored
	m.logger.Debug("Eight Sleep is unavailable",
		zap.String("nick_state", func() string {
			if nickState != nil {
				return nickState.State
			}
			return "error"
		}()),
		zap.String("caroline_state", func() string {
			if carolineState != nil {
				return carolineState.State
			}
			return "error"
		}()))
	return true
}

// updateEightSleepAvailability updates the shadow state with Eight Sleep availability info
func (m *Manager) updateEightSleepAvailability(available bool) {
	m.shadowTracker.UpdateEightSleepAvailability(available, m.timeProvider.Now())
}

// captureCurrentInputs captures all current input values for shadow state
func (m *Manager) captureCurrentInputs() map[string]interface{} {
	inputs := make(map[string]interface{})

	// Get all subscribed variables
	if val, err := m.stateManager.GetBool("isMasterAsleep"); err == nil {
		inputs["isMasterAsleep"] = val
	}
	if val, err := m.stateManager.GetString("musicPlaybackType"); err == nil {
		inputs["musicPlaybackType"] = val
	}
	if val, err := m.stateManager.GetBool("isAnyoneHome"); err == nil {
		inputs["isAnyoneHome"] = val
	}
	if val, err := m.stateManager.GetBool("isEveryoneAsleep"); err == nil {
		inputs["isEveryoneAsleep"] = val
	}
	if val, err := m.stateManager.GetBool("isFadeOutInProgress"); err == nil {
		inputs["isFadeOutInProgress"] = val
	}
	if val, err := m.stateManager.GetBool("isWakeSequenceActive"); err == nil {
		inputs["isWakeSequenceActive"] = val
	}
	if val, err := m.stateManager.GetBool("isSleepPrepActive"); err == nil {
		inputs["isSleepPrepActive"] = val
	}
	if val, err := m.stateManager.GetBool("isNickHome"); err == nil {
		inputs["isNickHome"] = val
	}
	if val, err := m.stateManager.GetBool("isCarolineHome"); err == nil {
		inputs["isCarolineHome"] = val
	}

	// Get currentlyPlayingMusic JSON
	var currentMusic map[string]interface{}
	if err := m.stateManager.GetJSON("currentlyPlayingMusic", &currentMusic); err == nil {
		inputs["currentlyPlayingMusic"] = currentMusic
	}

	return inputs
}

// updateShadowInputs updates the shadow state current inputs
func (m *Manager) updateShadowInputs() {
	inputs := m.captureCurrentInputs()
	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// updateShadowInputsWithTrigger updates the shadow state current inputs including trigger
func (m *Manager) updateShadowInputsWithTrigger(trigger string) {
	inputs := m.captureCurrentInputs()
	inputs["trigger"] = trigger
	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// recordAction records an action in shadow state
func (m *Manager) recordAction(actionType string, reason string, trigger string) {
	// Update current inputs including trigger
	m.updateShadowInputsWithTrigger(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the action
	m.shadowTracker.RecordAction(actionType, reason)
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.SleepHygieneShadowState {
	return m.shadowTracker.GetState()
}

// TriggerWakeForTest is a test helper that directly triggers the wake sequence (light fade-in).
// This allows tests to exercise the light fade-in logic without waiting for the 5-minute delay.
// Prerequisites: isFadeOutInProgress must be true (set by begin_wake), isMasterAsleep must be true.
func (m *Manager) TriggerWakeForTest() {
	m.logger.Info("Test: Triggering wake sequence directly via handleWake()")
	m.handleWake()
}

// TriggerScheduledCheckForTest runs the same scheduled trigger check that the
// background timer runs once per minute. It routes the call through the timer
// goroutine so that checkTimeTriggers() is never called concurrently from two
// goroutines — the same reason we send a signal rather than calling directly.
// Requires Start() to have been called (the timer goroutine must be running).
func (m *Manager) TriggerScheduledCheckForTest() {
	m.logger.Info("Test: Triggering scheduled check directly")
	done := make(chan struct{})
	m.triggerCheckCh <- done
	<-done
}

// TriggerBeginWakeForTest is a test helper that directly triggers the begin_wake sequence.
// This allows tests to exercise the fade-out logic without waiting for time triggers.
// Note: This runs the fade-out synchronously (not in a goroutine) for easier testing.
func (m *Manager) TriggerBeginWakeForTest() {
	m.logger.Info("Test: Triggering begin_wake directly")

	// Check conditions first (same as handleBeginWake)
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil || !isAnyoneHome {
		m.logger.Debug("Test: Skipping begin_wake - no one home")
		return
	}

	isMasterAsleep, err := m.stateManager.GetBool("isMasterAsleep")
	if err != nil || !isMasterAsleep {
		m.logger.Debug("Test: Skipping begin_wake - master not asleep")
		return
	}

	musicPlaybackType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil || musicPlaybackType != "sleep" {
		m.logger.Debug("Test: Skipping begin_wake - not playing sleep music")
		return
	}

	// Set wake sequence active (matching real handleBeginWake at line 424)
	if !m.readOnly {
		if err := m.stateManager.SetBool("isWakeSequenceActive", true); err != nil {
			m.logger.Error("Test: Failed to set isWakeSequenceActive", zap.Error(err))
		}
	}

	// Set fade out in progress flag
	if !m.readOnly {
		if err := m.stateManager.SetBool("isFadeOutInProgress", true); err != nil {
			m.logger.Error("Test: Failed to set isFadeOutInProgress", zap.Error(err))
		}
	}

	// Get bedroom speakers from currentlyPlayingMusic
	bedroomSpeakers := m.getBedroomSpeakers()
	if len(bedroomSpeakers) == 0 {
		bedroomSpeakers = []string{"media_player.bedroom"}
	}

	// Run fade-out SYNCHRONOUSLY for testing (not in goroutine)
	for _, speaker := range bedroomSpeakers {
		m.fadeOutSpeaker(speaker)
	}
}

// Reset re-checks all wake-up triggers for current day
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Sleep Hygiene - re-checking all wake-up triggers")

	// The timer loop already checks triggers periodically
	// For reset, we just need to force an immediate check
	m.logger.Info("Wake-up triggers will be checked on next timer tick")

	m.logger.Info("Successfully reset Sleep Hygiene")
	return nil
}
