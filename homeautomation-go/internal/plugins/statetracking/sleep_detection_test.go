package statetracking

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

func TestSleepDetection_HandlersInitialized(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state (use base variable for presence, not derived isAnyoneHome)
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify HA subscriptions were created (1 for sleep + 2 for wake presence + 2 for wake sleep stage + 3 for arrival announcements + 2 for near_home)
	if len(manager.subHelper.GetHASubscriptions()) != 10 {
		t.Errorf("Expected 10 HA subscriptions, got %d", len(manager.subHelper.GetHASubscriptions()))
	}
}

func TestSleepDetection_LightsTimerControl(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify no timer initially
	manager.timerMutex.Lock()
	if manager.masterSleepTimer != nil {
		t.Error("Expected no sleep timer initially")
	}
	manager.timerMutex.Unlock()

	// Lights off starts timer
	lightOffState := &ha.State{EntityID: "light.primary_suite", State: "off"}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", nil, lightOffState)

	manager.timerMutex.Lock()
	if manager.masterSleepTimer == nil {
		t.Error("Expected sleep timer to be started after lights off")
	}
	manager.timerMutex.Unlock()

	// Lights on cancels timer
	lightOnState := &ha.State{EntityID: "light.primary_suite", State: "on"}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", lightOffState, lightOnState)
}

func TestWakeDetection_BedPresenceTimerControl(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	hasTimer := func() bool {
		manager.timerMutex.Lock()
		defer manager.timerMutex.Unlock()
		return manager.masterWakeTimer != nil
	}

	if hasTimer() {
		t.Error("Expected no wake timer initially")
	}

	off := &ha.State{EntityID: nickBedPresenceEntity, State: "off"}
	on := &ha.State{EntityID: nickBedPresenceEntity, State: "on"}

	manager.handleBedPresenceChange(nickBedPresenceEntity, on, off)
	if !hasTimer() {
		t.Error("Expected wake timer after presence went off")
	}

	manager.handleBedPresenceChange(nickBedPresenceEntity, off, on)
	if hasTimer() {
		t.Error("Expected wake timer canceled after presence came back")
	}
}

func TestDetectMasterAsleep_Conditions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isNickHome        bool
		setAnyoneHome     bool // if true, sets isAnyoneHome directly instead of isNickHome
		isMasterAsleep    bool
		primarySuiteState string // HA entity state for light.primary_suite; empty = not set (early-return tests)
		expectedAsleep    bool
	}{
		{
			name:           "Skips when nobody home",
			setAnyoneHome:  true, // sets isAnyoneHome=false directly
			isMasterAsleep: false,
			// primarySuiteState not set - function returns early before the light re-check
			expectedAsleep: false,
		},
		{
			name:           "Skips when already asleep",
			isNickHome:     true,
			isMasterAsleep: true,
			// primarySuiteState not set - function returns early before the light re-check
			expectedAsleep: true,
		},
		{
			name:              "Sets sleep when conditions met and lights still off",
			isNickHome:        true,
			isMasterAsleep:    false,
			primarySuiteState: "off",
			expectedAsleep:    true,
		},
		{
			name:              "Aborts if lights turned on during delay",
			isNickHome:        true,
			isMasterAsleep:    false,
			primarySuiteState: "on",
			expectedAsleep:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			if tt.setAnyoneHome {
				if err := stateMgr.SetBool("isAnyoneHome", false); err != nil {
					t.Fatalf("Failed to set isAnyoneHome: %v", err)
				}
			} else {
				if err := stateMgr.SetBool("isNickHome", tt.isNickHome); err != nil {
					t.Fatalf("Failed to set isNickHome: %v", err)
				}
			}
			if err := stateMgr.SetBool("isMasterAsleep", tt.isMasterAsleep); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if tt.primarySuiteState != "" {
				mockHA.SetState("light.primary_suite", tt.primarySuiteState, nil)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			manager.detectMasterAsleep()

			isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
			if err != nil {
				t.Fatalf("Failed to get isMasterAsleep: %v", err)
			}
			if isMasterAsleep != tt.expectedAsleep {
				t.Errorf("Expected isMasterAsleep=%v, got %v", tt.expectedAsleep, isMasterAsleep)
			}
		})
	}
}

func TestDetectMasterAwake_Conditions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		nickPresence   string // empty = entity not set (read fails)
		carolinePres   string
		nickStage      string // empty = entity not set (read fails)
		carolineStage  string
		skipStageCheck bool
		expectedAsleep bool
	}{
		{name: "Wakes when both sides off and awake", nickPresence: "off", carolinePres: "off", nickStage: "awake", carolineStage: "awake", expectedAsleep: false},
		{name: "Wakes when stages are awake and out", nickPresence: "off", carolinePres: "off", nickStage: "out", carolineStage: "awake", expectedAsleep: false},
		{name: "Stays asleep when Nick in bed", nickPresence: "on", carolinePres: "off", nickStage: "awake", carolineStage: "awake", expectedAsleep: true},
		{name: "Stays asleep when Caroline in bed", nickPresence: "off", carolinePres: "on", nickStage: "awake", carolineStage: "awake", expectedAsleep: true},
		{name: "Stays asleep when a sensor is unavailable", nickPresence: "off", carolinePres: "unavailable", nickStage: "awake", carolineStage: "awake", expectedAsleep: true},
		{name: "Stays asleep when a sensor cannot be read", nickPresence: "off", carolinePres: "", nickStage: "awake", carolineStage: "awake", expectedAsleep: true},
		{name: "Stays asleep when Nick stage is light", nickPresence: "off", carolinePres: "off", nickStage: "light", carolineStage: "awake", expectedAsleep: true},
		{name: "Stays asleep when Caroline stage is rem", nickPresence: "off", carolinePres: "off", nickStage: "awake", carolineStage: "rem", expectedAsleep: true},
		{name: "Stays asleep when a stage is deep", nickPresence: "off", carolinePres: "off", nickStage: "deep", carolineStage: "deep", expectedAsleep: true},
		{name: "Stays asleep when a stage is unavailable", nickPresence: "off", carolinePres: "off", nickStage: "awake", carolineStage: "unavailable", expectedAsleep: true},
		{name: "Stays asleep when a stage cannot be read", nickPresence: "off", carolinePres: "off", nickStage: "", carolineStage: "awake", expectedAsleep: true},
		{name: "Fallback wakes despite in-bed stages", nickPresence: "off", carolinePres: "off", nickStage: "light", carolineStage: "rem", skipStageCheck: true, expectedAsleep: false},
		{name: "Fallback still needs both sides off", nickPresence: "off", carolinePres: "on", nickStage: "light", carolineStage: "rem", skipStageCheck: true, expectedAsleep: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			for entityID, value := range map[string]string{
				nickBedPresenceEntity:     tt.nickPresence,
				carolineBedPresenceEntity: tt.carolinePres,
				nickSleepStageEntity:      tt.nickStage,
				carolineSleepStageEntity:  tt.carolineStage,
			} {
				if value != "" {
					mockHA.SetState(entityID, value, nil)
				}
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			manager.detectMasterAwake(manager.wakeGen, tt.skipStageCheck)

			isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
			if err != nil {
				t.Fatalf("Failed to get isMasterAsleep: %v", err)
			}
			if isMasterAsleep != tt.expectedAsleep {
				t.Errorf("Expected isMasterAsleep=%v, got %v", tt.expectedAsleep, isMasterAsleep)
			}
		})
	}
}

func TestDetectMasterAwake_StaleCallbackDoesNothing(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	for _, entityID := range []string{nickBedPresenceEntity, carolineBedPresenceEntity} {
		mockHA.SetState(entityID, "off", nil)
	}
	for _, entityID := range []string{nickSleepStageEntity, carolineSleepStageEntity} {
		mockHA.SetState(entityID, "awake", nil)
	}

	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	staleGen := manager.wakeGen
	manager.handleBedPresenceChange(nickBedPresenceEntity, &ha.State{State: "off"}, &ha.State{State: "on"})

	manager.detectMasterAwake(staleGen, false)

	isMasterAsleep, _ := stateMgr.GetBool("isMasterAsleep")
	if !isMasterAsleep {
		t.Error("Expected a callback from before the latest presence event to be ignored")
	}
}

// wakeScenario starts a manager with the master asleep, both people in bed, and a mock clock.
func wakeScenario(t *testing.T) (*ha.MockClient, *state.Manager, *clock.MockClock) {
	t.Helper()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	mockHA.SetState(nickBedPresenceEntity, "on", nil)
	mockHA.SetState(carolineBedPresenceEntity, "on", nil)
	mockHA.SetState(nickSleepStageEntity, "light", nil)
	mockHA.SetState(carolineSleepStageEntity, "rem", nil)

	mockClock := clock.NewMockClock(time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC))
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
	manager.SetClock(mockClock)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	t.Cleanup(manager.Stop)

	return mockHA, stateMgr, mockClock
}

func requireMasterAsleep(t *testing.T, stateMgr *state.Manager, want bool, when string) {
	t.Helper()
	got, err := stateMgr.GetBool("isMasterAsleep")
	if err != nil {
		t.Fatalf("Failed to get isMasterAsleep: %v", err)
	}
	if got != want {
		t.Errorf("%s: expected isMasterAsleep=%v, got %v", when, want, got)
	}
}

// TestScenario_UserStory_EightSleepGlitchDoesNotWakeMaster replays 2026-10-25 04:05Z: both bed
// presence sensors read "off" for about 7 minutes while both people were in bed. The sleep
// stages never changed. Invariant: a presence outage with in-bed stages must not wake the master.
func TestScenario_UserStory_EightSleepGlitchDoesNotWakeMaster(t *testing.T) {
	t.Parallel()
	mockHA, stateMgr, mockClock := wakeScenario(t)

	t.Log("GIVEN: Both people are asleep in bed (stages light and rem)")

	t.Log("WHEN: Both presence sensors drop to off and the stages stay in bed")
	mockHA.SetState(carolineBedPresenceEntity, "off", nil)
	mockClock.AdvanceAndProcess(time.Minute)
	mockHA.SetState(nickBedPresenceEntity, "off", nil)

	t.Log("THEN: The master is still asleep when the 5-minute timer expires")
	mockClock.AdvanceAndProcess(WakeDetectionDelay + time.Second)
	requireMasterAsleep(t, stateMgr, true, "T+5min")

	t.Log("THEN: The master is still asleep 7 minutes in")
	mockClock.AdvanceAndProcess(2 * time.Minute)
	requireMasterAsleep(t, stateMgr, true, "T+7min")

	t.Log("WHEN: The feed recovers and both people read as in bed")
	mockHA.SetState(carolineBedPresenceEntity, "on", nil)
	mockHA.SetState(nickBedPresenceEntity, "on", nil)

	t.Log("THEN: The fallback never fires")
	mockClock.AdvanceAndProcess(WakeFallbackDelay * 2)
	requireMasterAsleep(t, stateMgr, true, "after recovery")
}

// TestScenario_UserStory_RealExitWakesMaster covers a person who really leaves the bed.
// Invariant: waking is never later than WakeFallbackDelay after the last presence "off".
func TestScenario_UserStory_RealExitWakesMaster(t *testing.T) {
	t.Parallel()

	t.Run("Stage reaches awake before the timer expires", func(t *testing.T) {
		mockHA, stateMgr, mockClock := wakeScenario(t)

		mockHA.SetState(nickBedPresenceEntity, "off", nil)
		mockHA.SetState(carolineBedPresenceEntity, "off", nil)
		mockClock.AdvanceAndProcess(2 * time.Minute)
		mockHA.SetState(nickSleepStageEntity, "awake", nil)
		mockHA.SetState(carolineSleepStageEntity, "awake", nil)
		requireMasterAsleep(t, stateMgr, true, "T+2min")

		mockClock.AdvanceAndProcess(WakeDetectionDelay)
		requireMasterAsleep(t, stateMgr, false, "T+7min")
	})

	t.Run("Stage reaches awake after the timer expires", func(t *testing.T) {
		mockHA, stateMgr, mockClock := wakeScenario(t)

		mockHA.SetState(nickBedPresenceEntity, "off", nil)
		mockHA.SetState(carolineBedPresenceEntity, "off", nil)
		mockClock.AdvanceAndProcess(WakeDetectionDelay + time.Second)
		requireMasterAsleep(t, stateMgr, true, "T+5min")

		mockHA.SetState(nickSleepStageEntity, "awake", nil)
		requireMasterAsleep(t, stateMgr, true, "only Nick's stage is awake")

		mockHA.SetState(carolineSleepStageEntity, "out", nil)
		requireMasterAsleep(t, stateMgr, false, "both stages awake or out")
	})

	t.Run("Stage stays stuck in bed", func(t *testing.T) {
		mockHA, stateMgr, mockClock := wakeScenario(t)

		mockHA.SetState(nickBedPresenceEntity, "off", nil)
		mockHA.SetState(carolineBedPresenceEntity, "off", nil)
		// Step past the 5-minute check first: the fallback starts from when that check ran
		mockClock.AdvanceAndProcess(WakeDetectionDelay + time.Second)
		mockClock.AdvanceAndProcess(WakeFallbackDelay - WakeDetectionDelay - 2*time.Second)
		requireMasterAsleep(t, stateMgr, true, "just before the fallback")

		mockClock.AdvanceAndProcess(2 * time.Second)
		requireMasterAsleep(t, stateMgr, false, "just after the fallback")
	})

	t.Run("A later presence flap restarts the fallback", func(t *testing.T) {
		mockHA, stateMgr, mockClock := wakeScenario(t)

		mockHA.SetState(nickBedPresenceEntity, "off", nil)
		mockHA.SetState(carolineBedPresenceEntity, "off", nil)
		mockClock.AdvanceAndProcess(10 * time.Minute)

		// A flap on one side restarts the timers, so the fallback is measured from here
		mockHA.SetState(nickBedPresenceEntity, "on", nil)
		mockHA.SetState(nickBedPresenceEntity, "off", nil)
		mockClock.AdvanceAndProcess(WakeDetectionDelay + time.Second)
		mockClock.AdvanceAndProcess(WakeFallbackDelay - WakeDetectionDelay - 2*time.Second)
		requireMasterAsleep(t, stateMgr, true, "just before the fallback, measured from the flap")

		mockClock.AdvanceAndProcess(2 * time.Second)
		requireMasterAsleep(t, stateMgr, false, "just after the fallback, measured from the flap")
	})
}
