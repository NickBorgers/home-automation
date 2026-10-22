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

	// Verify HA subscriptions were created (1 for sleep + 2 for wake detection + 3 for arrival announcements + 2 for near_home)
	if len(manager.subHelper.GetHASubscriptions()) != 8 {
		t.Errorf("Expected 8 HA subscriptions, got %d", len(manager.subHelper.GetHASubscriptions()))
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
		expectedAsleep bool
	}{
		{name: "Wakes when both sides off", nickPresence: "off", carolinePres: "off", expectedAsleep: false},
		{name: "Stays asleep when Nick in bed", nickPresence: "on", carolinePres: "off", expectedAsleep: true},
		{name: "Stays asleep when Caroline in bed", nickPresence: "off", carolinePres: "on", expectedAsleep: true},
		{name: "Stays asleep when a sensor is unavailable", nickPresence: "off", carolinePres: "unavailable", expectedAsleep: true},
		{name: "Stays asleep when a sensor cannot be read", nickPresence: "off", carolinePres: "", expectedAsleep: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if tt.nickPresence != "" {
				mockHA.SetState(nickBedPresenceEntity, tt.nickPresence, nil)
			}
			if tt.carolinePres != "" {
				mockHA.SetState(carolineBedPresenceEntity, tt.carolinePres, nil)
			}

			manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, nil, "", &alert.MockAlerter{})
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			manager.detectMasterAwake()

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
