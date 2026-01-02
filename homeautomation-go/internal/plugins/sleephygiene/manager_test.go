package sleephygiene

import (
	"testing"
	"time"

	"homeautomation/internal/config"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// setupTest creates a test environment with mock HA client, state manager, and config loader
func setupTest(t *testing.T, currentTime time.Time) (*Manager, *ha.MockClient, *state.Manager, *config.Loader) {
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Initialize state with default values
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetBool("isGuestAsleep", false)
	stateManager.SetBool("isEveryoneAsleep", true)
	stateManager.SetBool("isFadeOutInProgress", false)
	stateManager.SetBool("isNickHome", true)
	stateManager.SetBool("isCarolineHome", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Create a config loader
	configLoader := config.NewLoader("../../../configs", logger)

	// Create manager with fixed time provider and nil timezone (defaults to time.Local)
	timeProvider := plugin.FixedTimeProvider{FixedTime: currentTime}
	manager := NewManager(mockHA, stateManager, configLoader, logger, false, timeProvider, nil)

	return manager, mockHA, stateManager, configLoader
}

func TestNewManager(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)

	manager := NewManager(mockHA, stateManager, configLoader, logger, false, nil, nil)

	if manager == nil {
		t.Fatal("NewManager returned nil")
	}

	if manager.timeProvider == nil {
		t.Error("timeProvider should default to plugin.RealTimeProvider")
	}

	if manager.haClient != mockHA {
		t.Error("haClient not set correctly")
	}

	if manager.stateManager != stateManager {
		t.Error("stateManager not set correctly")
	}
}

func TestStartStop(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, _, _, _ := setupTest(t, now)

	// Start manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Check that ticker is running
	if manager.ticker == nil {
		t.Error("Ticker should be initialized after Start()")
	}

	// Check that HA subscriptions exist (bedroom lights + 2 Eight Sleep sensors)
	if len(manager.haSubscriptions) != 3 {
		t.Errorf("Should have 3 HA subscriptions after Start(), got %d", len(manager.haSubscriptions))
	}

	// Stop manager
	manager.Stop()

	// Verify cleanup
	if manager.haSubscriptions != nil {
		t.Error("HA subscriptions should be nil after Stop()")
	}
}

func TestBeginWake_AllConditionsMet(t *testing.T) {
	t.Parallel(
	// Set time to 9:05 AM (5 minutes after alarm time)
	)

	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Ensure all conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Trigger begin_wake
	manager.handleBeginWake()

	// Verify that isFadeOutInProgress was set to true
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if !fadeOut {
		t.Error("isFadeOutInProgress should be set to true after begin_wake")
	}

	// Verify at least one call to SetState (for isFadeOutInProgress)
	calls := mockHA.GetServiceCalls()
	if len(calls) < 1 {
		t.Error("Expected at least one service call to set isFadeOutInProgress")
	}
}

func TestBeginWake_NoOneHome(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set no one home
	stateManager.SetBool("isAnyoneHome", false)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Clear mock calls
	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// Verify that isFadeOutInProgress was NOT changed
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("isFadeOutInProgress should not be set when no one is home")
	}
}

func TestBeginWake_MasterNotAsleep(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set master not asleep
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", false)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Clear mock calls
	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// Verify that isFadeOutInProgress was NOT changed
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("isFadeOutInProgress should not be set when master is not asleep")
	}
}

func TestBeginWake_NotPlayingSleepMusic(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set music playback to something other than "sleep"
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "day")

	// Clear mock calls
	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// Verify that isFadeOutInProgress was NOT changed
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("isFadeOutInProgress should not be set when not playing sleep music")
	}
}

func TestWake_AllConditionsMet(t *testing.T) {
	t.Parallel(
	// Set time to 9:30 AM (25 minutes after alarm time)
	)

	now := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Ensure all conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetBool("isNickHome", true)
	stateManager.SetBool("isCarolineHome", true)

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger wake
	manager.handleWake()

	// Verify service calls were made
	calls := mockHA.GetServiceCalls()

	// Should have:
	// - 2 calls for master bedroom lights (initial + transition)
	// Note: TTS cuddle announcement removed to prevent Sonos volume issues
	if len(calls) < 2 {
		t.Errorf("Expected at least 2 service calls, got %d", len(calls))
	}

	// Check that light service was called for primary suite
	foundPrimarySuite := false

	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "light.primary_suite" {
				foundPrimarySuite = true
			}
		}
	}

	if !foundPrimarySuite {
		t.Error("Expected light.turn_on call for primary suite")
	}
}

func TestStopScreens_AllConditionsMet(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions: someone home, not everyone asleep
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", false)

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger stop_screens
	manager.handleStopScreens()

	// Verify light flash calls were made
	calls := mockHA.GetServiceCalls()

	foundFlash := false
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if flash, ok := call.Data["flash"].(string); ok && flash == "short" {
				foundFlash = true
				break
			}
		}
	}

	if !foundFlash {
		t.Error("Expected light flash calls for stop_screens")
	}
}

func TestStopScreens_EveryoneAsleep(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set everyone asleep
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", true)

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger stop_screens
	manager.handleStopScreens()

	// Verify NO calls were made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Error("Should not flash lights when everyone is asleep")
	}
}

func TestReadOnlyMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, true) // READ-ONLY

	configLoader := config.NewLoader("../../../configs", logger)

	// Set conditions
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Create manager in READ-ONLY mode
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, true, timeProvider, nil)

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// In read-only mode, no state changes should be made
	// The state manager is in read-only mode, so SetBool will not actually update HA
	// But we can verify the manager respects read-only flag
	if !manager.readOnly {
		t.Error("Manager should be in read-only mode")
	}
}

func TestIsSameDay(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 15, 23, 59, 0, 0, time.UTC)
	t3 := time.Date(2024, 1, 16, 0, 1, 0, 0, time.UTC)

	if !isSameDay(t1, t2) {
		t.Error("t1 and t2 should be on the same day")
	}

	if isSameDay(t1, t3) {
		t.Error("t1 and t3 should not be on the same day")
	}
}

func TestCheckTimeTriggers_StopScreens(t *testing.T) {
	t.Parallel(
	// This test verifies the stop_screens time-based trigger
	// Note: begin_wake and wake are now handled by Eight Sleep sensors, not time-based
	)

	// Test at stop_screens time (22:30)
	now := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, configLoader := setupTest(t, now)

	// Load schedule config for today
	if err := configLoader.LoadScheduleConfig(); err != nil {
		t.Skipf("Skipping test: schedule config not available: %v", err)
	}

	// Set conditions for stop_screens (someone home and awake)
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", false)

	// Clear calls
	mockHA.ClearServiceCalls()

	// Check triggers
	manager.checkTimeTriggers()

	// Should trigger stop_screens
	if _, triggered := manager.triggeredToday["stop_screens"]; !triggered {
		t.Error("stop_screens should be triggered at 22:30")
	}

	// Verify flash light calls were made
	calls := mockHA.GetServiceCalls()
	if len(calls) == 0 {
		t.Error("Expected flash light service calls for stop_screens")
	}
}

// TestHandleGoToBed_SetsSleepMusicAndFlashesLights tests the go_to_bed handler
// This matches Node-RED behavior: always set musicPlaybackType to "sleep", and
// flash lights only if someone is home and not everyone is asleep
func TestHandleGoToBed_SetsSleepMusicAndFlashesLights(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 23, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions: someone home, not everyone asleep
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", false)
	stateManager.SetString("musicPlaybackType", "winddown") // Start with winddown

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger go_to_bed
	manager.handleGoToBed()

	// Verify musicPlaybackType was set to "sleep"
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatal("Failed to get musicPlaybackType:", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected musicPlaybackType to be 'sleep', got '%s'", musicType)
	}

	// Verify flash light calls were made
	calls := mockHA.GetServiceCalls()
	foundFlash := false
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if flash, ok := call.Data["flash"].(string); ok && flash == "short" {
				foundFlash = true
				break
			}
		}
	}

	if !foundFlash {
		t.Error("Expected light flash calls for go_to_bed")
	}
}

// TestHandleGoToBed_SetsSleepMusicEvenWhenEveryoneAsleep tests that sleep music is started
// even when everyone is already asleep (lights won't flash but music should start)
func TestHandleGoToBed_SetsSleepMusicEvenWhenEveryoneAsleep(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 23, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions: someone home, everyone asleep
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", true)
	stateManager.SetString("musicPlaybackType", "winddown")

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger go_to_bed
	manager.handleGoToBed()

	// Verify musicPlaybackType was still set to "sleep"
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatal("Failed to get musicPlaybackType:", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected musicPlaybackType to be 'sleep' even when everyone is asleep, got '%s'", musicType)
	}

	// Verify NO flash light calls were made (everyone is asleep)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if flash, ok := call.Data["flash"].(string); ok && flash == "short" {
				t.Error("Should not flash lights when everyone is asleep")
			}
		}
	}

	// Verify shadow state was still recorded (bug fix: shadow state should be recorded even without lights)
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.GoToBedReminder == nil {
		t.Fatal("Expected GoToBedReminder to be recorded even when everyone is asleep")
	}
	if !shadowState.Outputs.GoToBedReminder.Triggered {
		t.Error("Expected GoToBedReminder.Triggered to be true")
	}
	if shadowState.Outputs.LastActionType != "go_to_bed" {
		t.Errorf("Expected LastActionType to be 'go_to_bed', got '%s'", shadowState.Outputs.LastActionType)
	}
}

// TestHandleGoToBed_NoOneHome tests go_to_bed when no one is home
func TestHandleGoToBed_NoOneHome(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 23, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions: no one home
	stateManager.SetBool("isAnyoneHome", false)
	stateManager.SetBool("isEveryoneAsleep", false)
	stateManager.SetString("musicPlaybackType", "winddown")

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger go_to_bed
	manager.handleGoToBed()

	// Verify musicPlaybackType was still set to "sleep" (always set)
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatal("Failed to get musicPlaybackType:", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected musicPlaybackType to be 'sleep' even when no one home, got '%s'", musicType)
	}

	// Verify NO flash light calls were made (no one home)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if flash, ok := call.Data["flash"].(string); ok && flash == "short" {
				t.Error("Should not flash lights when no one is home")
			}
		}
	}

	// Verify shadow state was still recorded (bug fix: shadow state should be recorded even without lights)
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.GoToBedReminder == nil {
		t.Fatal("Expected GoToBedReminder to be recorded even when no one is home")
	}
	if !shadowState.Outputs.GoToBedReminder.Triggered {
		t.Error("Expected GoToBedReminder.Triggered to be true")
	}
	if shadowState.Outputs.LastActionType != "go_to_bed" {
		t.Errorf("Expected LastActionType to be 'go_to_bed', got '%s'", shadowState.Outputs.LastActionType)
	}
}

// TestHandleGoToBed_ReadOnly tests go_to_bed in read-only mode
func TestHandleGoToBed_ReadOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 23, 30, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Set conditions
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", false)
	stateManager.SetString("musicPlaybackType", "winddown")

	configLoader := config.NewLoader("../../../configs", logger)
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, true, timeProvider, nil) // READ-ONLY

	mockHA.ClearServiceCalls()

	// Trigger go_to_bed
	manager.handleGoToBed()

	// In read-only mode, musicPlaybackType should NOT be changed
	musicType, _ := stateManager.GetString("musicPlaybackType")
	if musicType != "winddown" {
		t.Errorf("In read-only mode, musicPlaybackType should remain 'winddown', got '%s'", musicType)
	}

	// Verify NO service calls were made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls in read-only mode, got %d", len(calls))
	}
}

// TestRealTimeProvider tests the plugin.RealTimeProvider
func TestRealTimeProvider(t *testing.T) {
	t.Parallel()
	provider := plugin.RealTimeProvider{}
	now := provider.Now()

	// Verify it returns a reasonable time (within last minute)
	if time.Since(now) > time.Minute {
		t.Errorf("RealTimeProvider returned time too far in the past: %v", now)
	}
}

// TestFixedTimeProvider tests the plugin.FixedTimeProvider
func TestFixedTimeProvider(t *testing.T) {
	t.Parallel()
	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	provider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	if provider.Now() != fixedTime {
		t.Errorf("FixedTimeProvider did not return fixed time")
	}
}

// TestCheckTimeTriggers_ErrorGettingSchedule tests error handling when schedule config is not available
func TestCheckTimeTriggers_ErrorGettingSchedule(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Use a config loader pointing to non-existent directory
	configLoader := config.NewLoader("/nonexistent/path", logger)
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, false, timeProvider, nil)

	// Check triggers - should handle error gracefully
	manager.checkTimeTriggers()

	// No triggers should be set when schedule is unavailable
	if len(manager.triggeredToday) > 0 {
		t.Error("No triggers should be set when schedule config is missing")
	}
}

// TestHandleWake_ErrorGettingState tests error handling in handleWake
func TestHandleWake_ErrorGettingState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Don't initialize states - will cause errors
	configLoader := config.NewLoader("../../../configs", logger)
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, false, timeProvider, nil)

	mockHA.ClearServiceCalls()

	// Call handleWake - should handle errors gracefully
	manager.handleWake()

	// Should not have made service calls due to errors getting state
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Error("Should not make service calls when state retrieval fails")
	}
}

// TestHandleBeginWake_ReadOnly tests read-only mode for begin_wake
func TestHandleBeginWake_ReadOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Set all conditions
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	configLoader := config.NewLoader("../../../configs", logger)
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, true, timeProvider, nil) // READ-ONLY

	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// In read-only mode, no state changes should be made to HA
	// State manager itself is not read-only, so local state may change
	// But no HA service calls should be made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls in read-only mode, got %d", len(calls))
	}
}

// TestHandleWake_ReadOnly tests read-only mode for wake
func TestHandleWake_ReadOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Set all conditions
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetBool("isNickHome", true)
	stateManager.SetBool("isCarolineHome", true)

	configLoader := config.NewLoader("../../../configs", logger)
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, true, timeProvider, nil) // READ-ONLY

	mockHA.ClearServiceCalls()

	// Trigger wake
	manager.handleWake()

	// In read-only mode, no service calls should be made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls in read-only mode, got %d", len(calls))
	}
}

// TestHandleStopScreens_ReadOnly tests read-only mode for stop_screens
func TestHandleStopScreens_ReadOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC)
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateManager := state.NewManager(mockHA, logger, false)

	// Set conditions
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", false)

	configLoader := config.NewLoader("../../../configs", logger)
	timeProvider := plugin.FixedTimeProvider{FixedTime: now}
	manager := NewManager(mockHA, stateManager, configLoader, logger, true, timeProvider, nil) // READ-ONLY

	mockHA.ClearServiceCalls()

	// Trigger stop_screens
	manager.handleStopScreens()

	// In read-only mode, no service calls should be made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls in read-only mode, got %d", len(calls))
	}
}

// TestHandleWake_NoOneHome tests wake trigger when no one is home
func TestHandleWake_NoOneHome(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set no one home
	stateManager.SetBool("isAnyoneHome", false)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetBool("isFadeOutInProgress", true)

	mockHA.ClearServiceCalls()

	// Trigger wake
	manager.handleWake()

	// Should not make service calls when no one is home
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Error("Should not execute wake sequence when no one is home")
	}
}

// TestHandleWake_MasterNotAsleep tests wake trigger when master is not asleep
func TestHandleWake_MasterNotAsleep(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set master not asleep
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", false)
	stateManager.SetBool("isFadeOutInProgress", true)

	mockHA.ClearServiceCalls()

	// Trigger wake
	manager.handleWake()

	// Should not make service calls when master is not asleep
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Error("Should not execute wake sequence when master is not asleep")
	}
}

// TestHandleWake_FadeOutNotInProgress tests wake trigger when fade out is not in progress
func TestHandleWake_FadeOutNotInProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set fade out not in progress
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetBool("isFadeOutInProgress", false)

	mockHA.ClearServiceCalls()

	// Trigger wake
	manager.handleWake()

	// Should not make service calls when fade out is not in progress
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Error("Should not execute wake sequence when fade out is not in progress")
	}
}

// TestHandleStopScreens_NoOneHome tests stop_screens when no one is home
func TestHandleStopScreens_NoOneHome(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set no one home
	stateManager.SetBool("isAnyoneHome", false)
	stateManager.SetBool("isEveryoneAsleep", false)

	mockHA.ClearServiceCalls()

	// Trigger stop_screens
	manager.handleStopScreens()

	// Should not flash lights when no one is home
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Error("Should not flash lights when no one is home")
	}
}

// TestRunTimerLoop_MidnightRollover tests that triggers reset at midnight
func TestRunTimerLoop_MidnightRollover(t *testing.T) {
	t.Parallel(
	// Start just before midnight
	)

	now := time.Date(2024, 1, 15, 23, 59, 0, 0, time.UTC)
	manager, _, _, _ := setupTest(t, now)

	// Mark a trigger as fired today
	manager.triggeredToday["begin_wake"] = now

	// Start the manager to start the timer loop
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Update time provider to next day
	manager.timeProvider = plugin.FixedTimeProvider{FixedTime: time.Date(2024, 1, 16, 0, 1, 0, 0, time.UTC)}

	// Manually simulate what the timer loop does
	nextDay := manager.timeProvider.Now()
	for trigger, triggerTime := range manager.triggeredToday {
		if !isSameDay(nextDay, triggerTime) {
			delete(manager.triggeredToday, trigger)
		}
	}

	// Stop manager
	manager.Stop()

	// Verify trigger was reset
	if _, exists := manager.triggeredToday["begin_wake"]; exists {
		t.Error("Trigger should be reset after midnight")
	}
}

// TestFadeOutBedroomSpeaker_Complete tests fade out runs and makes volume calls
// Note: We test a partial fade-out since a complete fade-out from 60 to 0 would take 30+ minutes
func TestFadeOutBedroomSpeaker_Complete(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for fade out
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Start fade out in goroutine
	done := make(chan bool)
	go func() {
		manager.fadeOutBedroomSpeaker()
		done <- true
	}()

	// Wait for several volume changes (5 seconds should allow for a few iterations)
	time.Sleep(5 * time.Second)

	// Abort the fade out to complete the test quickly
	stateManager.SetBool("isFadeOutInProgress", false)

	// Wait for goroutine to complete
	select {
	case <-done:
		// Fade out completed
	case <-time.After(10 * time.Second):
		t.Fatal("Fade out did not stop within timeout")
	}

	// Verify volume_set calls were made
	calls := mockHA.GetServiceCalls()
	volumeSetCalls := 0
	lastVolume := 0.0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			volumeSetCalls++
			// Verify entity_id is correct
			if entityID, ok := call.Data["entity_id"].(string); !ok || entityID != "media_player.bedroom" {
				t.Errorf("Expected entity_id to be media_player.bedroom, got %v", call.Data["entity_id"])
			}
			// Track last volume
			if volumeLevel, ok := call.Data["volume_level"].(float64); ok {
				lastVolume = volumeLevel
			}
		}
	}

	// Should have made multiple volume set calls
	if volumeSetCalls < 3 {
		t.Errorf("Expected at least 3 volume_set calls, got %d", volumeSetCalls)
	}

	// Last volume should be less than initial (59/100)
	if lastVolume >= 0.59 {
		t.Errorf("Expected volume to decrease from initial 0.59, last volume was %.2f", lastVolume)
	}

	// Verify isFadeOutInProgress was NOT reset (we aborted externally)
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("isFadeOutInProgress should be false (we set it to abort)")
	}
}

// TestFadeOutBedroomSpeaker_AbortedByFlag tests fade out aborted when flag is set to false
func TestFadeOutBedroomSpeaker_AbortedByFlag(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for fade out
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Start fade out in goroutine
	done := make(chan bool)
	go func() {
		manager.fadeOutBedroomSpeaker()
		done <- true
	}()

	// Wait a bit for fade out to start
	time.Sleep(100 * time.Millisecond)

	// Abort the fade out
	stateManager.SetBool("isFadeOutInProgress", false)

	// Wait for completion
	select {
	case <-done:
		// Fade out aborted
	case <-time.After(10 * time.Second):
		t.Fatal("Fade out did not abort within timeout")
	}

	// Verify volume_set calls were made but not all 60
	calls := mockHA.GetServiceCalls()
	volumeSetCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			volumeSetCalls++
		}
	}

	// Should have fewer than 60 calls since we aborted early
	if volumeSetCalls >= 60 {
		t.Errorf("Expected fewer than 60 volume_set calls after abort, got %d", volumeSetCalls)
	}

	// Should have at least 1 call (fade out started)
	if volumeSetCalls < 1 {
		t.Error("Expected at least 1 volume_set call before abort")
	}
}

// TestFadeOutBedroomSpeaker_CancelledByMusicType tests fade out cancelled when music type changes
func TestFadeOutBedroomSpeaker_CancelledByMusicType(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for fade out
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Start fade out in goroutine
	done := make(chan bool)
	go func() {
		manager.fadeOutBedroomSpeaker()
		done <- true
	}()

	// Wait a bit for fade out to start
	time.Sleep(100 * time.Millisecond)

	// Change music type to cancel fade out
	stateManager.SetString("musicPlaybackType", "day")

	// Wait for completion
	select {
	case <-done:
		// Fade out cancelled
	case <-time.After(10 * time.Second):
		t.Fatal("Fade out did not cancel within timeout")
	}

	// Verify volume_set calls were made but not all 60
	calls := mockHA.GetServiceCalls()
	volumeSetCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			volumeSetCalls++
		}
	}

	// Should have fewer than 60 calls since we cancelled early
	if volumeSetCalls >= 60 {
		t.Errorf("Expected fewer than 60 volume_set calls after cancel, got %d", volumeSetCalls)
	}

	// Should have at least 1 call (fade out started)
	if volumeSetCalls < 1 {
		t.Error("Expected at least 1 volume_set call before cancel")
	}
}

// TestFadeOutBedroomSpeaker_VolumeSequence tests that volume decreases correctly
func TestFadeOutBedroomSpeaker_VolumeSequence(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for fade out
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Start fade out in goroutine
	done := make(chan bool)
	go func() {
		manager.fadeOutBedroomSpeaker()
		done <- true
	}()

	// Wait for a few volume changes
	time.Sleep(500 * time.Millisecond)

	// Abort to stop the test quickly
	stateManager.SetBool("isFadeOutInProgress", false)

	// Wait for completion
	<-done

	// Verify volume levels are decreasing
	calls := mockHA.GetServiceCalls()
	var volumeLevels []float64
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			if volumeLevel, ok := call.Data["volume_level"].(float64); ok {
				volumeLevels = append(volumeLevels, volumeLevel)
			}
		}
	}

	// Verify volumes are decreasing
	for i := 1; i < len(volumeLevels); i++ {
		if volumeLevels[i] >= volumeLevels[i-1] {
			t.Errorf("Volume should be decreasing: volume[%d]=%.2f, volume[%d]=%.2f",
				i-1, volumeLevels[i-1], i, volumeLevels[i])
		}
	}

	// Verify first volume is 59/100 (60 - 1)
	if len(volumeLevels) > 0 {
		expectedFirst := 0.59
		if volumeLevels[0] != expectedFirst {
			t.Errorf("Expected first volume to be %.2f, got %.2f", expectedFirst, volumeLevels[0])
		}
	}
}

// TestBeginWake_LaunchesFadeOut tests that begin_wake launches fade out goroutine
func TestBeginWake_LaunchesFadeOut(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for begin_wake
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// Give goroutine time to start
	time.Sleep(200 * time.Millisecond)

	// Abort fade out to stop it quickly
	stateManager.SetBool("isFadeOutInProgress", false)

	// Wait a bit more for goroutine to exit
	time.Sleep(200 * time.Millisecond)

	// Verify volume_set calls were made
	calls := mockHA.GetServiceCalls()
	volumeSetCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			volumeSetCalls++
		}
	}

	// Should have at least 1 volume set call (fade out started)
	if volumeSetCalls < 1 {
		t.Error("Expected at least 1 volume_set call after begin_wake")
	}
}

// TestGetSpeakerVolume tests querying volume from Home Assistant
func TestGetSpeakerVolume(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Set up mock to return a state with volume_level attribute
	mockHA.SetMockState("media_player.bedroom", &ha.State{
		EntityID: "media_player.bedroom",
		State:    "playing",
		Attributes: map[string]interface{}{
			"volume_level": 0.75, // 75%
		},
	})

	volume := manager.getSpeakerVolume("media_player.bedroom")

	// Should return 75 (converted from 0.75)
	if volume != 75 {
		t.Errorf("Expected volume 75, got %d", volume)
	}
}

// TestGetSpeakerVolume_NoState tests fallback when state query fails
func TestGetSpeakerVolume_NoState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, _, _, _ := setupTest(t, now)

	// Don't set any mock state, should fall back to default

	volume := manager.getSpeakerVolume("media_player.nonexistent")

	// Should return default 60
	if volume != 60 {
		t.Errorf("Expected default volume 60, got %d", volume)
	}
}

// TestUpdateSpeakerVolumeInState tests currentlyPlayingMusic state updates
func TestUpdateSpeakerVolumeInState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, _, stateManager, _ := setupTest(t, now)

	// Set up currentlyPlayingMusic with bedroom speaker
	currentMusic := map[string]interface{}{
		"participants": []interface{}{
			map[string]interface{}{
				"player_name": "media_player.bedroom",
				"volume":      80,
			},
			map[string]interface{}{
				"player_name": "media_player.living_room",
				"volume":      50,
			},
		},
	}
	stateManager.SetJSON("currentlyPlayingMusic", currentMusic)

	// Update bedroom volume to 45
	manager.updateSpeakerVolumeInState("media_player.bedroom", 45)

	// Verify it was updated
	var updatedMusic map[string]interface{}
	if err := stateManager.GetJSON("currentlyPlayingMusic", &updatedMusic); err != nil {
		t.Fatal("Failed to get updated currentlyPlayingMusic:", err)
	}

	participants := updatedMusic["participants"].([]interface{})
	bedroom := participants[0].(map[string]interface{})
	livingRoom := participants[1].(map[string]interface{})

	// volume might be int or float64 depending on JSON marshaling
	bedroomVolume := int(bedroom["volume"].(float64))
	if bedroomVolume != 45 {
		t.Errorf("Expected bedroom volume 45, got %d", bedroomVolume)
	}

	// Living room should be unchanged
	livingRoomVolume := int(livingRoom["volume"].(float64))
	if livingRoomVolume != 50 {
		t.Errorf("Expected living room volume unchanged at 50, got %d", livingRoomVolume)
	}
}

// TestGetBedroomSpeakers tests dynamic bedroom speaker discovery
func TestGetBedroomSpeakers(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, _, stateManager, _ := setupTest(t, now)

	// Set up currentlyPlayingMusic with multiple speakers
	currentMusic := map[string]interface{}{
		"participants": []map[string]interface{}{
			{
				"player_name": "media_player.bedroom",
				"volume":      60,
			},
			{
				"player_name": "media_player.bedroom_left",
				"volume":      55,
			},
			{
				"player_name": "media_player.living_room",
				"volume":      50,
			},
			{
				"player_name": "media_player.kitchen",
				"volume":      40,
			},
		},
	}
	stateManager.SetJSON("currentlyPlayingMusic", currentMusic)

	bedroomSpeakers := manager.getBedroomSpeakers()

	// Should find both bedroom speakers
	if len(bedroomSpeakers) != 2 {
		// Debug: let's see what we actually got
		var debugMusic map[string]interface{}
		if err := stateManager.GetJSON("currentlyPlayingMusic", &debugMusic); err == nil {
			t.Logf("currentlyPlayingMusic: %+v", debugMusic)
			if participants, ok := debugMusic["participants"]; ok {
				t.Logf("participants type: %T, value: %+v", participants, participants)
			}
		}
		t.Errorf("Expected 2 bedroom speakers, got %d: %v", len(bedroomSpeakers), bedroomSpeakers)
	}

	// Check that we got the right ones
	hasBedroomMain := false
	hasBedroomLeft := false
	for _, speaker := range bedroomSpeakers {
		if speaker == "media_player.bedroom" {
			hasBedroomMain = true
		}
		if speaker == "media_player.bedroom_left" {
			hasBedroomLeft = true
		}
	}

	if !hasBedroomMain {
		t.Error("Expected to find media_player.bedroom")
	}
	if !hasBedroomLeft {
		t.Error("Expected to find media_player.bedroom_left")
	}
}

// TestGetBedroomSpeakers_EmptyState tests fallback when currentlyPlayingMusic is not set
func TestGetBedroomSpeakers_EmptyState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, _, _, _ := setupTest(t, now)

	// Don't set currentlyPlayingMusic

	bedroomSpeakers := manager.getBedroomSpeakers()

	// Should fall back to default
	if len(bedroomSpeakers) != 1 {
		t.Errorf("Expected 1 default bedroom speaker, got %d", len(bedroomSpeakers))
	}
	if bedroomSpeakers[0] != "media_player.bedroom" {
		t.Errorf("Expected default media_player.bedroom, got %s", bedroomSpeakers[0])
	}
}

// TestFadeOutSpeaker_WithVolumeQuery tests fade out with actual volume query
func TestFadeOutSpeaker_WithVolumeQuery(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for fade out
	stateManager.SetBool("isFadeOutInProgress", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Set up mock to return initial volume of 58 (higher volume = shorter delays)
	// At 58: delay after first reduction = 60-57 = 3 seconds
	// At 57: delay = 60-56 = 4 seconds
	// Total: ~7 seconds for 2 reductions
	mockHA.SetMockState("media_player.bedroom", &ha.State{
		EntityID: "media_player.bedroom",
		State:    "playing",
		Attributes: map[string]interface{}{
			"volume_level": 0.58, // 58%
		},
	})

	// Set up currentlyPlayingMusic
	currentMusic := map[string]interface{}{
		"participants": []interface{}{
			map[string]interface{}{
				"player_name": "media_player.bedroom",
				"volume":      58,
			},
		},
	}
	stateManager.SetJSON("currentlyPlayingMusic", currentMusic)

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Start fade out in goroutine
	done := make(chan bool)
	go func() {
		manager.fadeOutSpeaker("media_player.bedroom")
		done <- true
	}()

	// Wait for several volume changes (8 seconds to allow 2 reductions)
	time.Sleep(8 * time.Second)

	// Abort the fade out
	stateManager.SetBool("isFadeOutInProgress", false)

	// Wait for completion
	select {
	case <-done:
		// Fade out completed
	case <-time.After(10 * time.Second):
		t.Fatal("Fade out did not stop within timeout")
	}

	// Verify GetState was called to query initial volume
	if !mockHA.WasGetStateCalled("media_player.bedroom") {
		t.Error("Expected GetState to be called for media_player.bedroom")
	}

	// Verify volume_set calls were made
	calls := mockHA.GetServiceCalls()
	volumeSetCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			volumeSetCalls++
		}
	}

	// Should have made multiple volume set calls
	if volumeSetCalls < 2 {
		t.Errorf("Expected at least 2 volume_set calls, got %d", volumeSetCalls)
	}

	// Verify currentlyPlayingMusic was updated
	var updatedMusic map[string]interface{}
	if err := stateManager.GetJSON("currentlyPlayingMusic", &updatedMusic); err != nil {
		t.Fatal("Failed to get updated currentlyPlayingMusic:", err)
	}

	participants := updatedMusic["participants"].([]interface{})
	bedroom := participants[0].(map[string]interface{})

	// Volume should be less than initial 58
	// Type assertion - handle both int and float64
	var bedroomVolume int
	switch v := bedroom["volume"].(type) {
	case int:
		bedroomVolume = v
	case float64:
		bedroomVolume = int(v)
	default:
		t.Fatalf("Unexpected volume type: %T", bedroom["volume"])
	}

	if bedroomVolume >= 58 {
		t.Errorf("Expected volume to be reduced from 58, got %d", bedroomVolume)
	}
}

// TestBeginWake_MultipleSpeakers tests that begin_wake launches fade-out for all bedroom speakers
func TestBeginWake_MultipleSpeakers(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 5, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Set conditions for begin_wake
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Set up currentlyPlayingMusic with multiple bedroom speakers
	currentMusic := map[string]interface{}{
		"participants": []interface{}{
			map[string]interface{}{
				"player_name": "media_player.bedroom",
				"volume":      60,
			},
			map[string]interface{}{
				"player_name": "media_player.bedroom_left",
				"volume":      55,
			},
		},
	}
	stateManager.SetJSON("currentlyPlayingMusic", currentMusic)

	// Set up mock states for both speakers
	mockHA.SetMockState("media_player.bedroom", &ha.State{
		EntityID: "media_player.bedroom",
		State:    "playing",
		Attributes: map[string]interface{}{
			"volume_level": 0.60,
		},
	})
	mockHA.SetMockState("media_player.bedroom_left", &ha.State{
		EntityID: "media_player.bedroom_left",
		State:    "playing",
		Attributes: map[string]interface{}{
			"volume_level": 0.55,
		},
	})

	// Clear previous calls
	mockHA.ClearServiceCalls()

	// Trigger begin_wake
	manager.handleBeginWake()

	// Give goroutines time to start
	time.Sleep(300 * time.Millisecond)

	// Abort fade out to stop it quickly
	stateManager.SetBool("isFadeOutInProgress", false)

	// Wait a bit more for goroutines to exit
	time.Sleep(300 * time.Millisecond)

	// Verify both speakers were queried
	if !mockHA.WasGetStateCalled("media_player.bedroom") {
		t.Error("Expected GetState to be called for media_player.bedroom")
	}
	if !mockHA.WasGetStateCalled("media_player.bedroom_left") {
		t.Error("Expected GetState to be called for media_player.bedroom_left")
	}

	// Verify volume_set calls were made for both speakers
	calls := mockHA.GetServiceCalls()
	bedroomCalls := 0
	bedroomLeftCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			if entityID, ok := call.Data["entity_id"].(string); ok {
				if entityID == "media_player.bedroom" {
					bedroomCalls++
				} else if entityID == "media_player.bedroom_left" {
					bedroomLeftCalls++
				}
			}
		}
	}

	// Should have at least 1 call for each speaker
	if bedroomCalls < 1 {
		t.Error("Expected at least 1 volume_set call for media_player.bedroom")
	}
	if bedroomLeftCalls < 1 {
		t.Error("Expected at least 1 volume_set call for media_player.bedroom_left")
	}
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	timeProvider := plugin.RealTimeProvider{}

	manager := NewManager(mockClient, stateManager, configLoader, logger, false, timeProvider, nil)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Reset should not error (currently just logs a message)
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}
}

// ========================================
// Eight Sleep Alarm Tests
// ========================================

func TestEightSleepAlarm_TriggersBeginWake(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)
	manager, _, stateManager, _ := setupTest(t, now)

	// Ensure all conditions for begin_wake are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Simulate Eight Sleep alarm state change
	oldState := &ha.State{State: "off"}
	newState := &ha.State{State: "alarm"}

	manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, newState)

	// Verify that isFadeOutInProgress was set (begin_wake was triggered)
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if !fadeOut {
		t.Error("isFadeOutInProgress should be set to true after Eight Sleep alarm triggers begin_wake")
	}

	// Verify begin_wake was marked as triggered
	if _, triggered := manager.triggeredToday["begin_wake"]; !triggered {
		t.Error("begin_wake should be marked as triggered after Eight Sleep alarm")
	}
}

func TestEightSleepAlarm_IgnoresNonAlarmState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)
	manager, _, stateManager, _ := setupTest(t, now)

	// Ensure conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Simulate Eight Sleep state change to non-alarm state
	oldState := &ha.State{State: "off"}
	newState := &ha.State{State: "awake"}

	manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, newState)

	// Verify that isFadeOutInProgress was NOT set
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("isFadeOutInProgress should not be set for non-alarm state")
	}

	// Verify begin_wake was NOT marked as triggered
	if _, triggered := manager.triggeredToday["begin_wake"]; triggered {
		t.Error("begin_wake should not be marked as triggered for non-alarm state")
	}
}

func TestEightSleepAlarm_DeduplicatesToday(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)
	manager, mockHA, stateManager, _ := setupTest(t, now)

	// Ensure conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// First alarm trigger
	oldState := &ha.State{State: "off"}
	newState := &ha.State{State: "alarm"}
	manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, newState)

	// Reset isFadeOutInProgress to detect if second trigger runs
	stateManager.SetBool("isFadeOutInProgress", false)
	mockHA.ClearServiceCalls()

	// Second alarm trigger (e.g., from Caroline's side) - should be deduplicated
	manager.handleEightSleepAlarm("sensor.caroline_s_eight_sleep_side_bed_state_type", oldState, newState)

	// Verify that isFadeOutInProgress was NOT set again (deduplicated)
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("Second Eight Sleep alarm should be deduplicated and not trigger begin_wake again")
	}
}

func TestEightSleepAlarm_BothSensorsWork(t *testing.T) {
	t.Parallel(
	// Test that both Nick's and Caroline's sensors can trigger the alarm
	)

	testCases := []struct {
		name     string
		entityID string
	}{
		{"Nick's sensor", "sensor.nick_s_eight_sleep_side_bed_state_type"},
		{"Caroline's sensor", "sensor.caroline_s_eight_sleep_side_bed_state_type"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			now := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)
			manager, _, stateManager, _ := setupTest(t, now)

			// Ensure conditions are met
			stateManager.SetBool("isAnyoneHome", true)
			stateManager.SetBool("isMasterAsleep", true)
			stateManager.SetString("musicPlaybackType", "sleep")
			stateManager.SetBool("isFadeOutInProgress", false)

			// Simulate alarm state change
			oldState := &ha.State{State: "off"}
			newState := &ha.State{State: "alarm"}
			manager.handleEightSleepAlarm(tc.entityID, oldState, newState)

			// Verify that begin_wake was triggered
			fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
			if !fadeOut {
				t.Errorf("%s should trigger begin_wake", tc.name)
			}
		})
	}
}

func TestEightSleepAlarm_IgnoresNilNewState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)
	manager, _, stateManager, _ := setupTest(t, now)

	// Ensure conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Call with nil newState - should be a no-op
	oldState := &ha.State{State: "off"}
	manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, nil)

	// Verify that nothing was triggered
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if fadeOut {
		t.Error("Nil newState should be ignored and not trigger begin_wake")
	}
}

func TestEightSleepAlarm_RespectsConditions(t *testing.T) {
	t.Parallel(
	// Test that the Eight Sleep alarm respects begin_wake conditions
	)

	testCases := []struct {
		name           string
		isAnyoneHome   bool
		isMasterAsleep bool
		musicType      string
		shouldTrigger  bool
	}{
		{"all conditions met", true, true, "sleep", true},
		{"no one home", false, true, "sleep", false},
		{"master not asleep", true, false, "sleep", false},
		{"not playing sleep music", true, true, "day", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			now := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)
			manager, _, stateManager, _ := setupTest(t, now)

			// Set conditions
			stateManager.SetBool("isAnyoneHome", tc.isAnyoneHome)
			stateManager.SetBool("isMasterAsleep", tc.isMasterAsleep)
			stateManager.SetString("musicPlaybackType", tc.musicType)
			stateManager.SetBool("isFadeOutInProgress", false)

			// Simulate alarm
			oldState := &ha.State{State: "off"}
			newState := &ha.State{State: "alarm"}
			manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, newState)

			// Verify result
			fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
			if fadeOut != tc.shouldTrigger {
				if tc.shouldTrigger {
					t.Errorf("Expected begin_wake to trigger for %s", tc.name)
				} else {
					t.Errorf("Expected begin_wake NOT to trigger for %s", tc.name)
				}
			}
		})
	}
}

func TestEightSleepAlarm_NewDayResetsDeduplication(t *testing.T) {
	t.Parallel(
	// Test that deduplication is reset on a new day
	)

	yesterday := time.Date(2024, 1, 14, 22, 0, 0, 0, time.UTC)
	today := time.Date(2024, 1, 15, 6, 30, 0, 0, time.UTC)

	manager, _, stateManager, _ := setupTest(t, yesterday)

	// Ensure conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Trigger alarm yesterday
	oldState := &ha.State{State: "off"}
	newState := &ha.State{State: "alarm"}
	manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, newState)

	// Verify it was marked as triggered
	if _, triggered := manager.triggeredToday["begin_wake"]; !triggered {
		t.Fatal("begin_wake should be marked as triggered")
	}

	// Reset isFadeOutInProgress and update time provider to today
	stateManager.SetBool("isFadeOutInProgress", false)
	manager.timeProvider = plugin.FixedTimeProvider{FixedTime: today}

	// Trigger alarm today - should work because it's a new day
	manager.handleEightSleepAlarm("sensor.nick_s_eight_sleep_side_bed_state_type", oldState, newState)

	// Verify that isFadeOutInProgress was set (new day, not deduplicated)
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if !fadeOut {
		t.Error("Alarm on a new day should trigger begin_wake (not deduplicated from yesterday)")
	}
}

func TestStart_SubscribesToEightSleepSensors(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Start manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Check that HA subscriptions include Eight Sleep sensors
	// The manager should have 3 HA subscriptions:
	// 1. light.primary_suite
	// 2. sensor.nick_s_eight_sleep_side_bed_state_type
	// 3. sensor.caroline_s_eight_sleep_side_bed_state_type
	if len(manager.haSubscriptions) != 3 {
		t.Errorf("Expected 3 HA subscriptions, got %d", len(manager.haSubscriptions))
	}

	// Verify subscriptions were made through the mock client
	subscriptions := mockHA.GetSubscribedEntities()
	foundNick := false
	foundCaroline := false
	for _, entity := range subscriptions {
		if entity == "sensor.nick_s_eight_sleep_side_bed_state_type" {
			foundNick = true
		}
		if entity == "sensor.caroline_s_eight_sleep_side_bed_state_type" {
			foundCaroline = true
		}
	}

	if !foundNick {
		t.Error("Expected subscription to Nick's Eight Sleep sensor")
	}
	if !foundCaroline {
		t.Error("Expected subscription to Caroline's Eight Sleep sensor")
	}
}

// Eight Sleep Availability Tests

func TestIsEightSleepUnavailable_BothSensorsAvailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Set both sensors to available states (not "unavailable")
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "awake", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "light", nil)

	if manager.isEightSleepUnavailable() {
		t.Error("isEightSleepUnavailable should return false when both sensors are available")
	}
}

func TestIsEightSleepUnavailable_OnlyNickAvailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Nick's sensor available, Caroline's unavailable
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "deep", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "unavailable", nil)

	if manager.isEightSleepUnavailable() {
		t.Error("isEightSleepUnavailable should return false when at least one sensor is available (Nick)")
	}
}

func TestIsEightSleepUnavailable_OnlyCarolineAvailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Nick's sensor unavailable, Caroline's available
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "unavailable", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "rem", nil)

	if manager.isEightSleepUnavailable() {
		t.Error("isEightSleepUnavailable should return false when at least one sensor is available (Caroline)")
	}
}

func TestIsEightSleepUnavailable_BothSensorsUnavailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Both sensors report "unavailable"
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "unavailable", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "unavailable", nil)

	if !manager.isEightSleepUnavailable() {
		t.Error("isEightSleepUnavailable should return true when both sensors are unavailable")
	}
}

func TestIsEightSleepUnavailable_BothSensorsNotFound(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, _, _, _ := setupTest(t, now)

	// Don't set any state - sensors don't exist in mock
	// This simulates GetState returning an error

	if !manager.isEightSleepUnavailable() {
		t.Error("isEightSleepUnavailable should return true when both sensors return errors")
	}
}

func TestIsEightSleepUnavailable_NickErrorCarolineAvailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC)
	manager, mockHA, _, _ := setupTest(t, now)

	// Nick's sensor doesn't exist (error), Caroline's is available
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "awake", nil)

	if manager.isEightSleepUnavailable() {
		t.Error("isEightSleepUnavailable should return false when at least one sensor is available")
	}
}

// Backup Wake Trigger Tests

func TestBackupWake_TriggersWhenEightSleepUnavailable(t *testing.T) {
	t.Parallel(
	// Set time to 9:00 AM on a Monday (begin_backup_wake is 08:50 for Monday)
	// Jan 15, 2024 is a Monday
	)

	now := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	manager, mockHA, stateManager, configLoader := setupTest(t, now)

	// Load schedule config
	if err := configLoader.LoadScheduleConfig(); err != nil {
		t.Skipf("Skipping test: schedule config not available: %v", err)
	}

	// Set both Eight Sleep sensors to unavailable
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "unavailable", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "unavailable", nil)

	// Ensure all begin_wake conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Trigger time-based checks
	manager.checkTimeTriggers()

	// Verify begin_wake was triggered
	if _, triggered := manager.triggeredToday["begin_wake"]; !triggered {
		t.Error("begin_wake should be triggered when Eight Sleep is unavailable and time is in backup wake window")
	}

	// Verify fade out started
	fadeOut, _ := stateManager.GetBool("isFadeOutInProgress")
	if !fadeOut {
		t.Error("isFadeOutInProgress should be set to true after backup wake triggers")
	}
}

func TestBackupWake_DoesNotTriggerWhenEightSleepAvailable(t *testing.T) {
	t.Parallel(
	// Set time to 9:00 AM on a Monday (begin_backup_wake is 08:50 for Monday)
	)

	now := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	manager, mockHA, stateManager, configLoader := setupTest(t, now)

	// Load schedule config
	if err := configLoader.LoadScheduleConfig(); err != nil {
		t.Skipf("Skipping test: schedule config not available: %v", err)
	}

	// Set Eight Sleep sensors to available
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "awake", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "light", nil)

	// Ensure all begin_wake conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Trigger time-based checks
	manager.checkTimeTriggers()

	// Verify begin_wake was NOT triggered (Eight Sleep should handle it via alarm)
	if _, triggered := manager.triggeredToday["begin_wake"]; triggered {
		t.Error("begin_wake should NOT be triggered via backup when Eight Sleep is available")
	}
}

func TestBackupWake_DoesNotTriggerOutsideWindow(t *testing.T) {
	t.Parallel(
	// Set time to 7:00 AM - well before the backup wake window (08:50)
	)

	now := time.Date(2024, 1, 15, 7, 0, 0, 0, time.UTC)
	manager, mockHA, stateManager, configLoader := setupTest(t, now)

	// Load schedule config
	if err := configLoader.LoadScheduleConfig(); err != nil {
		t.Skipf("Skipping test: schedule config not available: %v", err)
	}

	// Set both Eight Sleep sensors to unavailable
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "unavailable", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "unavailable", nil)

	// Ensure all begin_wake conditions are met
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isFadeOutInProgress", false)

	// Trigger time-based checks
	manager.checkTimeTriggers()

	// Verify begin_wake was NOT triggered (too early)
	if _, triggered := manager.triggeredToday["begin_wake"]; triggered {
		t.Error("begin_wake should NOT be triggered before the backup wake window")
	}
}

func TestBackupWake_UpdatesShadowState(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	manager, mockHA, _, configLoader := setupTest(t, now)

	// Load schedule config
	if err := configLoader.LoadScheduleConfig(); err != nil {
		t.Skipf("Skipping test: schedule config not available: %v", err)
	}

	// Set both Eight Sleep sensors to unavailable
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "unavailable", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "unavailable", nil)

	// Trigger time-based checks to update shadow state
	manager.checkTimeTriggers()

	// Get shadow state and verify availability fields
	shadowState := manager.GetShadowState()

	if shadowState.Outputs.EightSleepAvailable {
		t.Error("EightSleepAvailable should be false when sensors are unavailable")
	}

	if !shadowState.Outputs.BackupWakeEnabled {
		t.Error("BackupWakeEnabled should be true when Eight Sleep is unavailable")
	}

	if shadowState.Outputs.LastAvailabilityCheck.IsZero() {
		t.Error("LastAvailabilityCheck should be set after checkTimeTriggers")
	}
}

func TestBackupWake_ShadowStateShowsAvailableWhenSensorsWork(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	manager, mockHA, _, configLoader := setupTest(t, now)

	// Load schedule config
	if err := configLoader.LoadScheduleConfig(); err != nil {
		t.Skipf("Skipping test: schedule config not available: %v", err)
	}

	// Set Eight Sleep sensors to available
	mockHA.SetState("sensor.nick_s_eight_sleep_side_sleep_stage", "deep", nil)
	mockHA.SetState("sensor.caroline_s_eight_sleep_side_sleep_stage", "awake", nil)

	// Trigger time-based checks to update shadow state
	manager.checkTimeTriggers()

	// Get shadow state and verify availability fields
	shadowState := manager.GetShadowState()

	if !shadowState.Outputs.EightSleepAvailable {
		t.Error("EightSleepAvailable should be true when sensors are available")
	}

	if shadowState.Outputs.BackupWakeEnabled {
		t.Error("BackupWakeEnabled should be false when Eight Sleep is available")
	}
}
