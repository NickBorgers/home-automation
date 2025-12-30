package sexmode

import (
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// TestSexModeManager_ActivationSetsMusic tests that activating sex mode sets music to "sex" type
func TestSexModeManager_ActivationSetsMusic(t *testing.T) {
	// Setup
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
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager (not read-only)
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager (not read-only)
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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

	// Verify Eight Sleep was set to coldest
	calls := mockHA.GetServiceCalls()
	nickFound := false
	carolineFound := false

	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, ok := call.Data["entity_id"].(string)
			if !ok {
				continue
			}
			temp, tempOk := call.Data["temperature"].(int)
			if !tempOk {
				continue
			}

			if entityID == EightSleepNickEntity && temp == EightSleepMinTemp {
				nickFound = true
			}
			if entityID == EightSleepCarolineEntity && temp == EightSleepMinTemp {
				carolineFound = true
			}
		}
	}

	if !nickFound {
		t.Errorf("Expected Nick's Eight Sleep to be set to coldest (%d), but service was not called correctly. Calls: %+v", EightSleepMinTemp, calls)
	}
	if !carolineFound {
		t.Errorf("Expected Caroline's Eight Sleep to be set to coldest (%d), but service was not called correctly. Calls: %+v", EightSleepMinTemp, calls)
	}
}

// TestSexModeManager_DeactivationRestoresMusic tests that deactivating sex mode restores previous music type
func TestSexModeManager_DeactivationRestoresMusic(t *testing.T) {
	// Setup
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
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
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
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
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
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.sex", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, true) // Read-only state manager
	stateManager.SyncFromHA()

	// Create sex mode manager in read-only mode
	manager := NewManager(mockHA, stateManager, logger, true, nil)
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
	// Setup
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
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
	// Setup
	mockHA := ha.NewMockClient()
	// Sex mode is ON in HA but manager doesn't know about it
	mockHA.SetState("input_boolean.sex", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create sex mode manager
	manager := NewManager(mockHA, stateManager, logger, false, nil)
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
