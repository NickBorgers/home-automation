package statetracking

import (
	"context"
	"testing"

	"homeautomation/internal/alert"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

func TestSleepDetection_HandlersInitialized(t *testing.T) {
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

	// One light subscription, three arrival subscriptions, and two near-home subscriptions.
	if len(manager.subHelper.GetHASubscriptions()) != 6 {
		t.Errorf("Expected 6 HA subscriptions, got %d", len(manager.subHelper.GetHASubscriptions()))
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

	manager.timerMutex.Lock()
	if manager.masterSleepTimer != nil {
		t.Error("Expected no sleep timer initially")
	}
	manager.timerMutex.Unlock()

	lightOffState := &ha.State{EntityID: "light.primary_suite", State: "off"}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", nil, lightOffState)
	manager.timerMutex.Lock()
	if manager.masterSleepTimer == nil {
		t.Error("Expected sleep timer to be started after lights off")
	}
	manager.timerMutex.Unlock()

	lightOnState := &ha.State{EntityID: "light.primary_suite", State: "on"}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", lightOffState, lightOnState)
}

func TestDetectMasterAsleep_Conditions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isNickHome        bool
		setAnyoneHome     bool
		isMasterAsleep    bool
		primarySuiteState string
		expectedAsleep    bool
	}{
		{name: "Skips when nobody home", setAnyoneHome: true},
		{name: "Skips when already asleep", isNickHome: true, isMasterAsleep: true, expectedAsleep: true},
		{name: "Sets sleep when conditions met and lights still off", isNickHome: true, primarySuiteState: "off", expectedAsleep: true},
		{name: "Aborts if lights turned on during delay", isNickHome: true, primarySuiteState: "on"},
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
			} else if err := stateMgr.SetBool("isNickHome", tt.isNickHome); err != nil {
				t.Fatalf("Failed to set isNickHome: %v", err)
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

// TestScenario_UserStory_EightSleepFlappingDoesNotWakeMaster reproduces issue #1240.
// Invariant: Eight Sleep presence and stage data never changes the authoritative master sleep state.
func TestScenario_UserStory_EightSleepFlappingDoesNotWakeMaster(t *testing.T) {
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

	t.Log("GIVEN: The master is asleep while both Eight Sleep sides report in-bed sleep stages")
	for entityID, value := range map[string]string{
		"binary_sensor.nick_s_eight_sleep_side_bed_presence":     "on",
		"binary_sensor.caroline_s_eight_sleep_side_bed_presence": "on",
		"sensor.nick_s_eight_sleep_side_sleep_stage":             "deep",
		"sensor.caroline_s_eight_sleep_side_sleep_stage":         "rem",
	} {
		mockHA.SetState(entityID, value, nil)
	}

	t.Log("WHEN: Presence and stage sensors flap, disagree, and become unavailable")
	updates := map[string][]string{
		"binary_sensor.nick_s_eight_sleep_side_bed_presence":     {"off", "unknown", "unavailable", "on"},
		"binary_sensor.caroline_s_eight_sleep_side_bed_presence": {"on", "off", "unavailable", "on"},
		"sensor.nick_s_eight_sleep_side_sleep_stage":             {"awake", "out", "unknown", "deep"},
		"sensor.caroline_s_eight_sleep_side_sleep_stage":         {"rem", "awake", "unavailable", "rem"},
	}
	for entityID, states := range updates {
		for _, sensorState := range states {
			mockHA.SetState(entityID, sensorState, nil)
		}
	}

	t.Log("THEN: The authoritative master sleep state remains asleep")
	isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
	if err != nil {
		t.Fatalf("Failed to get isMasterAsleep: %v", err)
	}
	if !isMasterAsleep {
		t.Error("Eight Sleep sensor changes must not mark the master awake")
	}
}
