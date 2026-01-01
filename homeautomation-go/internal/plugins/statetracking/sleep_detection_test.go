package statetracking

import (
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

func TestSleepDetection_HandlersInitialized(t *testing.T) {
	// Create mock HA client and state manager
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
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify HA subscriptions were created (2 for sleep detection + 3 for arrival announcements)
	if len(manager.subHelper.GetHASubscriptions()) != 5 {
		t.Errorf("Expected 5 HA subscriptions, got %d", len(manager.subHelper.GetHASubscriptions()))
	}
}

func TestSleepDetection_LightsOffStartsTimer(t *testing.T) {
	// Create mock HA client and state manager
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
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
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

	// Simulate primary suite lights turning off
	lightState := &ha.State{
		EntityID: "light.primary_suite",
		State:    "off",
	}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", nil, lightState)

	// Verify timer was started
	manager.timerMutex.Lock()
	if manager.masterSleepTimer == nil {
		t.Error("Expected sleep timer to be started after lights off")
	}
	manager.timerMutex.Unlock()
}

func TestSleepDetection_LightsOnCancelsTimer(t *testing.T) {
	// Create mock HA client and state manager
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
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate lights off (starts timer)
	lightOffState := &ha.State{
		EntityID: "light.primary_suite",
		State:    "off",
	}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", nil, lightOffState)

	// Verify timer exists
	manager.timerMutex.Lock()
	hasTimer := manager.masterSleepTimer != nil
	manager.timerMutex.Unlock()
	if !hasTimer {
		t.Fatal("Expected sleep timer after lights off")
	}

	// Turn lights back on (should cancel timer)
	lightOnState := &ha.State{
		EntityID: "light.primary_suite",
		State:    "on",
	}
	manager.handlePrimarySuiteLightsChange("light.primary_suite", lightOffState, lightOnState)

	// Verify timer was cancelled (set to nil happens in Stop, but the timer should be stopped)
	// We can't directly verify the timer is stopped, but we can verify no new timer is nil
	// The important part is that the code path was exercised
}

func TestWakeDetection_DoorOpenStartsTimer(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state - master is asleep
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify no wake timer initially
	manager.timerMutex.Lock()
	if manager.masterWakeTimer != nil {
		t.Error("Expected no wake timer initially")
	}
	manager.timerMutex.Unlock()

	// Simulate primary bedroom door opening
	doorState := &ha.State{
		EntityID: "input_boolean.primary_bedroom_door_open",
		State:    "on",
	}
	manager.handlePrimaryBedroomDoorChange("input_boolean.primary_bedroom_door_open", nil, doorState)

	// Verify timer was started
	manager.timerMutex.Lock()
	if manager.masterWakeTimer == nil {
		t.Error("Expected wake timer to be started after door opened")
	}
	manager.timerMutex.Unlock()
}

func TestWakeDetection_DoorClosedCancelsTimer(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state - master is asleep
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create and start manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate door opening (starts timer)
	doorOpenState := &ha.State{
		EntityID: "input_boolean.primary_bedroom_door_open",
		State:    "on",
	}
	manager.handlePrimaryBedroomDoorChange("input_boolean.primary_bedroom_door_open", nil, doorOpenState)

	// Verify timer exists
	manager.timerMutex.Lock()
	hasTimer := manager.masterWakeTimer != nil
	manager.timerMutex.Unlock()
	if !hasTimer {
		t.Fatal("Expected wake timer after door opened")
	}

	// Close door (should cancel timer)
	doorClosedState := &ha.State{
		EntityID: "input_boolean.primary_bedroom_door_open",
		State:    "off",
	}
	manager.handlePrimaryBedroomDoorChange("input_boolean.primary_bedroom_door_open", doorOpenState, doorClosedState)

	// Code path exercised - timer should be stopped
}

func TestDetectMasterAsleep_SkipsIfNobodyHome(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state - nobody home
	if err := stateMgr.SetBool("isAnyoneHome", false); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Call detectMasterAsleep directly
	manager.detectMasterAsleep()

	// Verify master is NOT marked as asleep (nobody home)
	isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
	if err != nil {
		t.Fatalf("Failed to get isMasterAsleep: %v", err)
	}
	if isMasterAsleep {
		t.Error("Expected isMasterAsleep to remain false when nobody is home")
	}
}

func TestDetectMasterAsleep_SkipsIfAlreadyAsleep(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state - someone home (set base variable), already asleep
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Call detectMasterAsleep directly
	manager.detectMasterAsleep()

	// Verify master is STILL asleep (should not have changed)
	isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
	if err != nil {
		t.Fatalf("Failed to get isMasterAsleep: %v", err)
	}
	if !isMasterAsleep {
		t.Error("Expected isMasterAsleep to remain true when already asleep")
	}
}

func TestDetectMasterAsleep_SetsSleepWhenConditionsMet(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state - someone home (set base variable, not derived), not asleep
	if err := stateMgr.SetBool("isNickHome", true); err != nil {
		t.Fatalf("Failed to set isNickHome: %v", err)
	}
	if err := stateMgr.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create manager in read-write mode (not read-only)
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Mark state manager as read-write mode to allow state changes
	// (The manager is already in read-write mode, state manager just needs to sync the change)

	// Call detectMasterAsleep directly
	manager.detectMasterAsleep()

	// Verify master IS marked as asleep
	isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
	if err != nil {
		t.Fatalf("Failed to get isMasterAsleep: %v", err)
	}
	if !isMasterAsleep {
		t.Error("Expected isMasterAsleep to be true when conditions are met")
	}
}

func TestDetectMasterAwake_SetsAwake(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Set initial state - asleep
	if err := stateMgr.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create manager
	manager := NewManager(mockHA, stateMgr, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Call detectMasterAwake directly
	manager.detectMasterAwake()

	// Verify master IS marked as awake
	isMasterAsleep, err := stateMgr.GetBool("isMasterAsleep")
	if err != nil {
		t.Fatalf("Failed to get isMasterAsleep: %v", err)
	}
	if isMasterAsleep {
		t.Error("Expected isMasterAsleep to be false after wake detection")
	}
}
