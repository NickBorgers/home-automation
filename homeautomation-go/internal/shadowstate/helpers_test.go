package shadowstate

import (
	"fmt"
	"testing"

	"homeautomation/internal/ha"
)

// mockStateManager implements the StateManager interface for testing
type mockStateManager struct {
	boolValues   map[string]bool
	stringValues map[string]string
	numberValues map[string]float64
}

func newMockStateManager() *mockStateManager {
	return &mockStateManager{
		boolValues:   make(map[string]bool),
		stringValues: make(map[string]string),
		numberValues: make(map[string]float64),
	}
}

func (m *mockStateManager) GetBool(key string) (bool, error) {
	if val, ok := m.boolValues[key]; ok {
		return val, nil
	}
	return false, fmt.Errorf("variable %s not found", key)
}

func (m *mockStateManager) GetString(key string) (string, error) {
	if val, ok := m.stringValues[key]; ok {
		return val, nil
	}
	return "", fmt.Errorf("variable %s not found", key)
}

func (m *mockStateManager) GetNumber(key string) (float64, error) {
	if val, ok := m.numberValues[key]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("variable %s not found", key)
}

func (m *mockStateManager) SetBool(key string, value bool) {
	m.boolValues[key] = value
}

func (m *mockStateManager) SetString(key string, value string) {
	m.stringValues[key] = value
}

func (m *mockStateManager) SetNumber(key string, value float64) {
	m.numberValues[key] = value
}

func TestNewInputCaptureHelper(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	helper := NewInputCaptureHelper(registry, mockHA, mockState)

	if helper == nil {
		t.Fatal("expected non-nil helper")
	}

	if helper.registry != registry {
		t.Error("registry not set correctly")
	}

	if helper.haClient != mockHA {
		t.Error("haClient not set correctly")
	}

	if helper.stateManager != mockState {
		t.Error("stateManager not set correctly")
	}
}

func TestCaptureInputs_HAEntities(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up HA states
	mockHA.SetState("sensor.battery", "85", nil)
	mockHA.SetState("sensor.solar", "2.5", nil)

	// Register subscriptions
	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterHASubscription("energy", "sensor.solar")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("energy")

	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}

	if inputs["sensor.battery"] != "85" {
		t.Errorf("expected battery=85, got %v", inputs["sensor.battery"])
	}

	if inputs["sensor.solar"] != "2.5" {
		t.Errorf("expected solar=2.5, got %v", inputs["sensor.solar"])
	}
}

func TestCaptureInputs_StateVariables_Bool(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up state values
	mockState.SetBool("isGridAvailable", true)
	mockState.SetBool("isFreeEnergyAvailable", false)

	// Register subscriptions
	registry.RegisterStateSubscription("energy", "isGridAvailable")
	registry.RegisterStateSubscription("energy", "isFreeEnergyAvailable")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("energy")

	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}

	if inputs["isGridAvailable"] != true {
		t.Errorf("expected isGridAvailable=true, got %v", inputs["isGridAvailable"])
	}

	if inputs["isFreeEnergyAvailable"] != false {
		t.Errorf("expected isFreeEnergyAvailable=false, got %v", inputs["isFreeEnergyAvailable"])
	}
}

func TestCaptureInputs_StateVariables_String(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up state values
	mockState.SetString("dayPhase", "morning")
	mockState.SetString("sunevent", "sunrise")

	// Register subscriptions
	registry.RegisterStateSubscription("lighting", "dayPhase")
	registry.RegisterStateSubscription("lighting", "sunevent")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("lighting")

	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}

	if inputs["dayPhase"] != "morning" {
		t.Errorf("expected dayPhase=morning, got %v", inputs["dayPhase"])
	}

	if inputs["sunevent"] != "sunrise" {
		t.Errorf("expected sunevent=sunrise, got %v", inputs["sunevent"])
	}
}

func TestCaptureInputs_StateVariables_Number(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up state values
	mockState.SetNumber("batteryPercentage", 85.5)

	// Register subscriptions
	registry.RegisterStateSubscription("energy", "batteryPercentage")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("energy")

	if len(inputs) != 1 {
		t.Fatalf("expected 1 input, got %d", len(inputs))
	}

	if inputs["batteryPercentage"] != 85.5 {
		t.Errorf("expected batteryPercentage=85.5, got %v", inputs["batteryPercentage"])
	}
}

func TestCaptureInputs_MixedTypes(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up HA states
	mockHA.SetState("sensor.battery", "80", nil)

	// Set up state values
	mockState.SetBool("isGridAvailable", false)
	mockState.SetString("batteryEnergyLevel", "green")

	// Register subscriptions
	registry.RegisterHASubscription("energy", "sensor.battery")
	registry.RegisterStateSubscription("energy", "isGridAvailable")
	registry.RegisterStateSubscription("energy", "batteryEnergyLevel")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("energy")

	if len(inputs) != 3 {
		t.Fatalf("expected 3 inputs, got %d", len(inputs))
	}

	if inputs["sensor.battery"] != "80" {
		t.Errorf("expected sensor.battery=80, got %v", inputs["sensor.battery"])
	}

	if inputs["isGridAvailable"] != false {
		t.Errorf("expected isGridAvailable=false, got %v", inputs["isGridAvailable"])
	}

	if inputs["batteryEnergyLevel"] != "green" {
		t.Errorf("expected batteryEnergyLevel=green, got %v", inputs["batteryEnergyLevel"])
	}
}

func TestCaptureInputs_NoSubscriptions(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("nonexistent")

	if len(inputs) != 0 {
		t.Fatalf("expected 0 inputs for nonexistent plugin, got %d", len(inputs))
	}
}

func TestCaptureInputs_MissingHAEntity(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Register subscription but don't set up HA state
	registry.RegisterHASubscription("energy", "sensor.nonexistent")
	mockHA.SetState("sensor.battery", "80", nil)
	registry.RegisterHASubscription("energy", "sensor.battery")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("energy")

	// Should only have the existing entity
	if len(inputs) != 1 {
		t.Fatalf("expected 1 input (missing entity skipped), got %d", len(inputs))
	}

	if _, ok := inputs["sensor.nonexistent"]; ok {
		t.Error("missing entity should not be in inputs")
	}

	if inputs["sensor.battery"] != "80" {
		t.Errorf("expected sensor.battery=80, got %v", inputs["sensor.battery"])
	}
}

func TestCaptureInputs_MissingStateVariable(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Register subscription but don't set up state value
	registry.RegisterStateSubscription("energy", "nonexistent")
	mockState.SetBool("isGridAvailable", true)
	registry.RegisterStateSubscription("energy", "isGridAvailable")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("energy")

	// Should only have the existing variable
	if len(inputs) != 1 {
		t.Fatalf("expected 1 input (missing variable skipped), got %d", len(inputs))
	}

	if _, ok := inputs["nonexistent"]; ok {
		t.Error("missing variable should not be in inputs")
	}

	if inputs["isGridAvailable"] != true {
		t.Errorf("expected isGridAvailable=true, got %v", inputs["isGridAvailable"])
	}
}

func TestCaptureInputsWithAdditional(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up HA states
	mockHA.SetState("sensor.battery", "80", nil)

	// Register subscriptions
	registry.RegisterHASubscription("energy", "sensor.battery")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)

	additional := map[string]interface{}{
		"trigger":  "battery_change",
		"oldValue": "75",
	}

	inputs := helper.CaptureInputsWithAdditional("energy", additional)

	if len(inputs) != 3 {
		t.Fatalf("expected 3 inputs, got %d", len(inputs))
	}

	if inputs["sensor.battery"] != "80" {
		t.Errorf("expected sensor.battery=80, got %v", inputs["sensor.battery"])
	}

	if inputs["trigger"] != "battery_change" {
		t.Errorf("expected trigger=battery_change, got %v", inputs["trigger"])
	}

	if inputs["oldValue"] != "75" {
		t.Errorf("expected oldValue=75, got %v", inputs["oldValue"])
	}
}

func TestCaptureInputsWithAdditional_Override(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set up HA states
	mockHA.SetState("sensor.battery", "80", nil)

	// Register subscriptions
	registry.RegisterHASubscription("energy", "sensor.battery")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)

	// Additional with same key as subscribed entity
	additional := map[string]interface{}{
		"sensor.battery": "overridden_value",
	}

	inputs := helper.CaptureInputsWithAdditional("energy", additional)

	// Additional should override
	if inputs["sensor.battery"] != "overridden_value" {
		t.Errorf("expected override, got %v", inputs["sensor.battery"])
	}
}

func TestCaptureInputsWithAdditional_EmptyAdditional(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	mockHA.SetState("sensor.battery", "80", nil)
	registry.RegisterHASubscription("energy", "sensor.battery")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)

	inputs := helper.CaptureInputsWithAdditional("energy", nil)

	if len(inputs) != 1 {
		t.Fatalf("expected 1 input with nil additional, got %d", len(inputs))
	}

	if inputs["sensor.battery"] != "80" {
		t.Errorf("expected sensor.battery=80, got %v", inputs["sensor.battery"])
	}
}

func TestGetStateValue_TypePrecedence(t *testing.T) {
	t.Parallel()
	registry := NewSubscriptionRegistry()
	mockHA := ha.NewMockClient()
	mockState := newMockStateManager()

	// Set the same key in multiple types - bool should take precedence
	mockState.SetBool("testKey", true)
	mockState.SetString("testKey", "string_value")
	mockState.SetNumber("testKey", 42.0)

	registry.RegisterStateSubscription("test", "testKey")

	helper := NewInputCaptureHelper(registry, mockHA, mockState)
	inputs := helper.CaptureInputs("test")

	// Bool should win due to precedence
	if inputs["testKey"] != true {
		t.Errorf("expected bool value (true), got %v (type %T)", inputs["testKey"], inputs["testKey"])
	}
}
