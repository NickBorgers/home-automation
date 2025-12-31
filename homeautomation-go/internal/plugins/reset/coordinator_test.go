package reset

import (
	"errors"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
)

// mockResettable is a mock plugin that tracks Reset() calls
type mockResettable struct {
	resetCalled bool
	resetError  error
}

func (m *mockResettable) Reset() error {
	m.resetCalled = true
	return m.resetError
}

// createTestManager creates a state manager for testing
func createTestManager(t *testing.T) *state.Manager {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.reset", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := state.NewManager(mockClient, logger, false)
	if err := manager.SyncFromHA(); err != nil {
		t.Fatalf("Failed to sync state: %v", err)
	}
	return manager
}

// TestCoordinator_Start tests coordinator startup
func TestCoordinator_Start(t *testing.T) {
	logger := testlogger.New()
	stateManager := createTestManager(t)

	plugins := []PluginWithName{
		{Name: "TestPlugin", Plugin: &mockResettable{}},
	}

	coordinator := NewCoordinator(stateManager, logger, false, plugins)

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}

	coordinator.Stop()
}

// TestCoordinator_ResetTrigger tests that reset triggers plugin Reset() calls
func TestCoordinator_ResetTrigger(t *testing.T) {
	logger := testlogger.New()
	stateManager := createTestManager(t)

	// Create mock plugins
	plugin1 := &mockResettable{}
	plugin2 := &mockResettable{}

	plugins := []PluginWithName{
		{Name: "Plugin1", Plugin: plugin1},
		{Name: "Plugin2", Plugin: plugin2},
	}

	coordinator := NewCoordinator(stateManager, logger, false, plugins)

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}
	defer coordinator.Stop()

	// Set reset to true
	if err := stateManager.SetBool("reset", true); err != nil {
		t.Fatalf("Failed to set reset: %v", err)
	}

	// Give time for subscription callback to execute
	time.Sleep(100 * time.Millisecond)

	// Verify both plugins had Reset() called
	if !plugin1.resetCalled {
		t.Error("Plugin1.Reset() was not called")
	}
	if !plugin2.resetCalled {
		t.Error("Plugin2.Reset() was not called")
	}

	// Verify reset was turned back off
	resetValue, err := stateManager.GetBool("reset")
	if err != nil {
		t.Fatalf("Failed to get reset value: %v", err)
	}
	if resetValue {
		t.Error("Reset boolean was not turned off after trigger")
	}
}

// TestCoordinator_ResetError tests that coordinator continues on plugin errors
func TestCoordinator_ResetError(t *testing.T) {
	logger := testlogger.New()
	stateManager := createTestManager(t)

	// Create plugins - one that fails, one that succeeds
	failingPlugin := &mockResettable{resetError: errors.New("reset failed")}
	successPlugin := &mockResettable{}

	plugins := []PluginWithName{
		{Name: "FailingPlugin", Plugin: failingPlugin},
		{Name: "SuccessPlugin", Plugin: successPlugin},
	}

	coordinator := NewCoordinator(stateManager, logger, false, plugins)

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}
	defer coordinator.Stop()

	// Trigger reset
	if err := stateManager.SetBool("reset", true); err != nil {
		t.Fatalf("Failed to set reset: %v", err)
	}

	// Give time for subscription callback to execute
	time.Sleep(100 * time.Millisecond)

	// Verify both plugins had Reset() called (coordinator continues on error)
	if !failingPlugin.resetCalled {
		t.Error("FailingPlugin.Reset() was not called")
	}
	if !successPlugin.resetCalled {
		t.Error("SuccessPlugin.Reset() was not called despite earlier plugin failure")
	}

	// Verify reset was turned off despite errors
	resetValue, err := stateManager.GetBool("reset")
	if err != nil {
		t.Fatalf("Failed to get reset value: %v", err)
	}
	if resetValue {
		t.Error("Reset boolean was not turned off after trigger with errors")
	}
}

// TestCoordinator_ReadOnlyMode tests coordinator in read-only mode
func TestCoordinator_ReadOnlyMode(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_boolean.reset", "off", map[string]interface{}{})
	mockClient.Connect()

	// Create manager in read-only mode
	stateManager := state.NewManager(mockClient, logger, true)
	if err := stateManager.SyncFromHA(); err != nil {
		t.Fatalf("Failed to sync state: %v", err)
	}

	plugin := &mockResettable{}
	plugins := []PluginWithName{
		{Name: "TestPlugin", Plugin: plugin},
	}

	// Create coordinator in read-only mode
	coordinator := NewCoordinator(stateManager, logger, true, plugins)

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}
	defer coordinator.Stop()

	// Trigger reset (should fail in read-only mode but that's expected)
	mockClient.SetState("input_boolean.reset", "on", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

	// In read-only mode, plugins should still be reset
	if !plugin.resetCalled {
		t.Error("Plugin.Reset() was not called in read-only mode")
	}
}

// TestCoordinator_NoPlugins tests coordinator with no plugins
func TestCoordinator_NoPlugins(t *testing.T) {
	logger := testlogger.New()
	stateManager := createTestManager(t)

	// Create coordinator with no plugins
	coordinator := NewCoordinator(stateManager, logger, false, []PluginWithName{})

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}
	defer coordinator.Stop()

	// Trigger reset - should not crash
	if err := stateManager.SetBool("reset", true); err != nil {
		t.Fatalf("Failed to set reset: %v", err)
	}

	// Give time for subscription callback to execute
	time.Sleep(100 * time.Millisecond)

	// Verify reset was turned off
	resetValue, err := stateManager.GetBool("reset")
	if err != nil {
		t.Fatalf("Failed to get reset value: %v", err)
	}
	if resetValue {
		t.Error("Reset boolean was not turned off")
	}
}

// TestCoordinator_ResetFalse tests that setting reset to false doesn't trigger
func TestCoordinator_ResetFalse(t *testing.T) {
	logger := testlogger.New()
	stateManager := createTestManager(t)

	plugin := &mockResettable{}
	plugins := []PluginWithName{
		{Name: "TestPlugin", Plugin: plugin},
	}

	coordinator := NewCoordinator(stateManager, logger, false, plugins)

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}
	defer coordinator.Stop()

	// Set reset to false (should not trigger)
	if err := stateManager.SetBool("reset", false); err != nil {
		t.Fatalf("Failed to set reset: %v", err)
	}

	// Give time for subscription callback to execute
	time.Sleep(100 * time.Millisecond)

	// Verify plugin was NOT reset
	if plugin.resetCalled {
		t.Error("Plugin.Reset() was called when reset set to false")
	}
}

// TestCoordinator_Stop tests coordinator shutdown
func TestCoordinator_Stop(t *testing.T) {
	logger := testlogger.New()
	stateManager := createTestManager(t)

	plugins := []PluginWithName{
		{Name: "TestPlugin", Plugin: &mockResettable{}},
	}

	coordinator := NewCoordinator(stateManager, logger, false, plugins)

	if err := coordinator.Start(); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}

	// Stop should not panic
	coordinator.Stop()

	// Multiple stops should be safe
	coordinator.Stop()
}
