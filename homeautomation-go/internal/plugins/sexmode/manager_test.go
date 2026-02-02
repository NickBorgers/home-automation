package sexmode

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// TestSexModeManager_ActivationSetsMusic tests that activating sex mode sets music to "sex" type
func TestSexModeManager_ActivationSetsMusic(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Set initial music type
	if err := stateManager.SetString("musicPlaybackType", "day"); err != nil {
		t.Fatalf("Failed to set initial musicPlaybackType: %v", err)
	}

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate sex mode being turned on
	mockHA.SimulateStateChange("input_boolean.sex", "on")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify music type was changed to "sex"
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "sex" {
		t.Errorf("Expected musicPlaybackType to be 'sex', got '%s'", musicType)
	}

	// Verify the previous music type was saved
	manager.mu.Lock()
	savedType := manager.preSexMusicType
	manager.mu.Unlock()

	if savedType != "day" {
		t.Errorf("Expected preSexMusicType to be 'day', got '%s'", savedType)
	}
}

// TestSexModeManager_ActivationSetsLighting tests that activating sex mode sets Primary Suite to night scene
func TestSexModeManager_ActivationSetsLighting(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate sex mode being turned on
	mockHA.SimulateStateChange("input_boolean.sex", "on")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify night scene was activated for Primary Suite
	calls := mockHA.GetServiceCalls()
	foundSceneCall := false
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "scene.primary_suite_night" {
				foundSceneCall = true
				break
			}
		}
	}

	if !foundSceneCall {
		t.Errorf("Expected scene.primary_suite_night to be activated, but service was not called. Calls: %+v", calls)
	}
}

// TestSexModeManager_ActivationSetsEightSleep tests that activating sex mode sets Eight Sleep to coldest
func TestSexModeManager_ActivationSetsEightSleep(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	// Set up Eight Sleep entities with min_temp attribute (auto-detection)
	mockHA.SetState(EightSleepNickEntity, "heat_cool", map[string]interface{}{
		"min_temp": float64(55),
		"max_temp": float64(110),
	})
	mockHA.SetState(EightSleepCarolineEntity, "heat_cool", map[string]interface{}{
		"min_temp": float64(55),
		"max_temp": float64(110),
	})
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate sex mode being turned on
	mockHA.SimulateStateChange("input_boolean.sex", "on")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify Eight Sleep was set to coldest (auto-detected min_temp)
	calls := mockHA.GetServiceCalls()
	nickFound := false
	carolineFound := false

	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, ok := call.Data["entity_id"].(string)
			if !ok {
				continue
			}
			// Temperature is now sent as float64 (auto-detected from entity attributes)
			temp, tempOk := call.Data["temperature"].(float64)
			if !tempOk {
				continue
			}

			// Should use auto-detected min_temp of 55
			if entityID == EightSleepNickEntity && temp == 55 {
				nickFound = true
			}
			if entityID == EightSleepCarolineEntity && temp == 55 {
				carolineFound = true
			}
		}
	}

	if !nickFound {
		t.Errorf("Expected Nick's Eight Sleep to be set to coldest (55), but service was not called correctly. Calls: %+v", calls)
	}
	if !carolineFound {
		t.Errorf("Expected Caroline's Eight Sleep to be set to coldest (55), but service was not called correctly. Calls: %+v", calls)
	}
}

// TestSexModeManager_DeactivationRestoresMusic tests that deactivating sex mode restores previous music type
func TestSexModeManager_DeactivationRestoresMusic(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.SetState("input_text.day_phase", "day", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Set initial music type and day phase
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set initial musicPlaybackType: %v", err)
	}
	if err := stateManager.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateManager.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Activate sex mode first
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Verify it was set to "sex"
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "sex" {
		t.Errorf("Expected musicPlaybackType to be 'sex' after activation, got '%s'", musicType)
	}

	// Clear service calls before deactivation
	mockHA.ClearServiceCalls()

	// Deactivate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "off")
	time.Sleep(100 * time.Millisecond)

	// Verify music type was restored to "evening"
	musicType, err = stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType after deactivation: %v", err)
	}
	if musicType != "evening" {
		t.Errorf("Expected musicPlaybackType to be restored to 'evening', got '%s'", musicType)
	}
}

// TestSexModeManager_DeactivationReEvaluatesLighting tests that deactivating sex mode re-evaluates lighting
func TestSexModeManager_DeactivationReEvaluatesLighting(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Set day phase to "sunset" and master is not asleep
	if err := stateManager.SetString("dayPhase", "sunset"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateManager.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Activate sex mode first
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Clear service calls before deactivation
	mockHA.ClearServiceCalls()

	// Deactivate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "off")
	time.Sleep(100 * time.Millisecond)

	// Verify Primary Suite scene was activated based on day phase (sunset)
	calls := mockHA.GetServiceCalls()
	foundSceneCall := false
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "scene.primary_suite_sunset" {
				foundSceneCall = true
				break
			}
		}
	}

	if !foundSceneCall {
		t.Errorf("Expected scene.primary_suite_sunset to be activated on deactivation, but service was not called. Calls: %+v", calls)
	}
}

// TestSexModeManager_DeactivationTurnsOffLightsWhenAsleep tests that lights turn off if master is asleep
func TestSexModeManager_DeactivationTurnsOffLightsWhenAsleep(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Set master as asleep
	if err := stateManager.SetString("dayPhase", "night"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Activate sex mode first
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Clear service calls before deactivation
	mockHA.ClearServiceCalls()

	// Deactivate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "off")
	time.Sleep(100 * time.Millisecond)

	// Verify Primary Suite lights were turned off
	calls := mockHA.GetServiceCalls()
	foundTurnOff := false
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_off" {
			if areaID, ok := call.Data["area_id"].(string); ok && areaID == "master_bedroom" {
				foundTurnOff = true
				break
			}
		}
	}

	if !foundTurnOff {
		t.Errorf("Expected Primary Suite lights to be turned off when master is asleep, but service was not called. Calls: %+v", calls)
	}
}

// TestSexModeManager_DuplicateActivationIgnored tests that duplicate activations are ignored
func TestSexModeManager_DuplicateActivationIgnored(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Activate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Clear calls
	mockHA.ClearServiceCalls()

	// Try to activate again (simulate duplicate)
	manager.handleSexModeOn()
	time.Sleep(100 * time.Millisecond)

	// Verify no additional service calls were made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls on duplicate activation, got %d calls", len(calls))
	}
}

// TestSexModeManager_ReadOnlyMode tests that read-only mode prevents service calls
func TestSexModeManager_ReadOnlyMode(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, true) // Read-only state manager
	stateManager.SyncFromHA()

	// Create sex mode manager in read-only mode
	manager := NewManager(context.Background(), mockHA, stateManager, logger, true, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Activate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Verify no service calls were made (all should be read-only)
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls in read-only mode, got %d calls: %+v", len(calls), calls)
	}
}

// TestSexModeManager_ShadowState tests that shadow state is properly tracked
func TestSexModeManager_ShadowState(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Set initial states
	if err := stateManager.SetString("musicPlaybackType", "day"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Create sex mode manager
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Get initial shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.IsActive {
		t.Errorf("Expected initial IsActive to be false")
	}

	// Activate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Get shadow state after activation
	shadowState = manager.GetShadowState()
	if !shadowState.Outputs.IsActive {
		t.Errorf("Expected IsActive to be true after activation")
	}
	if shadowState.Outputs.LastActionType != "activate" {
		t.Errorf("Expected LastActionType to be 'activate', got '%s'", shadowState.Outputs.LastActionType)
	}
	if shadowState.Outputs.PreSexMusicType != "day" {
		t.Errorf("Expected PreSexMusicType to be 'day', got '%s'", shadowState.Outputs.PreSexMusicType)
	}
}

// TestSexModeManager_Reset tests the Reset function
func TestSexModeManager_Reset(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	// Sex mode is ON in HA but manager doesn't know about it
	mockHA.SetState("input_boolean.sex", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Verify not active initially (state change not triggered during Start)
	manager.mu.Lock()
	wasActive := manager.isActive
	manager.mu.Unlock()
	if wasActive {
		t.Errorf("Expected isActive to be false before reset")
	}

	// Clear any calls
	mockHA.ClearServiceCalls()

	// Reset should detect the HA state and activate
	if err := manager.Reset(); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify now active
	manager.mu.Lock()
	isActive := manager.isActive
	manager.mu.Unlock()
	if !isActive {
		t.Errorf("Expected isActive to be true after reset")
	}

	// Verify services were called
	calls := mockHA.GetServiceCalls()
	if len(calls) == 0 {
		t.Errorf("Expected service calls after reset, got none")
	}
}

// TestSexModeManager_AutoClearOnWakeUp tests that sex mode is automatically
// cleared when a wake-up event occurs (isAnyoneAsleep: true → false).
// This is the fix for issue #525 using the preferred design.
func TestSexModeManager_AutoClearOnWakeUp(t *testing.T) {
	t.Parallel()

	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.SetState("input_text.day_phase", "morning", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Start with sleep music and everyone asleep
	if err := stateManager.SetString("musicPlaybackType", "sleep"); err != nil {
		t.Fatalf("Failed to set initial musicPlaybackType: %v", err)
	}
	if err := stateManager.SetString("dayPhase", "morning"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateManager.SetBool("isAnyoneAsleep", true); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Activate sex mode - this saves preSexMusicType="sleep"
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Verify sex mode is active
	manager.mu.Lock()
	isActive := manager.isActive
	savedType := manager.preSexMusicType
	manager.mu.Unlock()
	if !isActive {
		t.Fatalf("Expected sex mode to be active")
	}
	if savedType != "sleep" {
		t.Errorf("Expected preSexMusicType to be 'sleep', got '%s'", savedType)
	}

	// Clear service calls to track what happens on wake-up
	mockHA.ClearServiceCalls()

	// Simulate wake-up event: isAnyoneAsleep becomes false
	// The music plugin would normally set musicPlaybackType to "day" here,
	// but we simulate that the sex mode manager auto-clears first
	if err := stateManager.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Wait for the subscription handler to process
	time.Sleep(100 * time.Millisecond)

	// Verify that input_boolean.sex was turned off via service call
	calls := mockHA.GetServiceCalls()
	foundTurnOff := false
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.sex" {
				foundTurnOff = true
				break
			}
		}
	}
	if !foundTurnOff {
		t.Errorf("Expected input_boolean.sex to be turned off on wake-up, but service was not called. Calls: %+v", calls)
	}

	// Simulate the state change callback that would come from HA after we called turn_off
	// (In real scenario, HA would fire this automatically)
	mockHA.SimulateStateChange("input_boolean.sex", "off")
	time.Sleep(100 * time.Millisecond)

	// Verify sex mode is now inactive
	manager.mu.Lock()
	isActive = manager.isActive
	manager.mu.Unlock()
	if isActive {
		t.Errorf("Expected sex mode to be inactive after wake-up auto-clear")
	}

	// Now simulate what the music plugin would do - set music to "day"
	if err := stateManager.SetString("musicPlaybackType", "day"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType to day: %v", err)
	}

	// Verify music stayed at "day" (was not overwritten back to "sleep")
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "day" {
		t.Errorf("Expected musicPlaybackType to be 'day' (set by music plugin after wake-up), got '%s'", musicType)
	}
}

// TestSexModeManager_DeactivationRestoresMusicWhenStillAsleep tests that
// deactivating sex mode DOES restore music when everyone is still asleep.
// This ensures the fix for #525 doesn't break the normal restoration flow.
func TestSexModeManager_DeactivationRestoresMusicWhenStillAsleep(t *testing.T) {
	t.Parallel()

	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.SetState("input_text.day_phase", "night", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Start with sleep music and everyone still asleep
	if err := stateManager.SetString("musicPlaybackType", "sleep"); err != nil {
		t.Fatalf("Failed to set initial musicPlaybackType: %v", err)
	}
	if err := stateManager.SetString("dayPhase", "night"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateManager.SetBool("isAnyoneAsleep", true); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Create sex mode manager (not read-only)
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Activate sex mode - this saves preSexMusicType="sleep"
	mockHA.SimulateStateChange("input_boolean.sex", "on")
	time.Sleep(100 * time.Millisecond)

	// Verify it was set to "sex"
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "sex" {
		t.Errorf("Expected musicPlaybackType to be 'sex' after activation, got '%s'", musicType)
	}

	// isAnyoneAsleep remains true (no wake-up occurred)

	// Clear service calls before deactivation
	mockHA.ClearServiceCalls()

	// Deactivate sex mode
	mockHA.SimulateStateChange("input_boolean.sex", "off")
	time.Sleep(100 * time.Millisecond)

	// Music type SHOULD be restored to "sleep" since everyone is still asleep
	musicType, err = stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType after deactivation: %v", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected musicPlaybackType to be restored to 'sleep' (still asleep), got '%s'", musicType)
	}
}

// TestSexModeManager_WakeUpIgnoredWhenNotActive tests that wake-up events
// are ignored when sex mode is not active. This ensures the subscription
// doesn't cause issues during normal operation.
func TestSexModeManager_WakeUpIgnoredWhenNotActive(t *testing.T) {
	t.Parallel()

	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Start with everyone asleep
	if err := stateManager.SetBool("isAnyoneAsleep", true); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Create sex mode manager (not read-only) but don't activate sex mode
	manager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start sex mode manager: %v", err)
	}
	defer manager.Stop()

	// Verify sex mode is NOT active
	manager.mu.Lock()
	isActive := manager.isActive
	manager.mu.Unlock()
	if isActive {
		t.Fatalf("Expected sex mode to be inactive initially")
	}

	// Clear service calls
	mockHA.ClearServiceCalls()

	// Simulate wake-up event: isAnyoneAsleep becomes false
	if err := stateManager.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Wait for potential subscription handler processing
	time.Sleep(100 * time.Millisecond)

	// Verify that input_boolean.sex was NOT turned off (sex mode wasn't active)
	// Note: Other turn_off calls may occur (e.g., state manager syncing input_boolean.anyone_asleep)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.sex" {
				t.Errorf("Should not have called turn_off on input_boolean.sex when sex mode is not active. Calls: %+v", calls)
			}
		}
	}

	// Verify sex mode is still inactive
	manager.mu.Lock()
	isActive = manager.isActive
	manager.mu.Unlock()
	if isActive {
		t.Errorf("Expected sex mode to remain inactive")
	}
}
