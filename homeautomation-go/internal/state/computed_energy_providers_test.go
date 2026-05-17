package state

import (
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"go.uber.org/zap"
)

// setupTestManagerForEnergyRegistry sets up a test manager with energy-related state variables.
func setupTestManagerForEnergyRegistry(t *testing.T) (*Manager, *ha.MockClient) {
	t.Helper()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Set up all required entities with default values
	mockClient.SetState("input_number.this_hour_solar_generation", "0", map[string]interface{}{})
	mockClient.SetState("input_number.remaining_solar_generation", "0", map[string]interface{}{})
	mockClient.SetState("input_boolean.free_energy_available", "off", map[string]interface{}{})
	mockClient.SetState("input_text.battery_energy_level", "black", map[string]interface{}{})
	mockClient.SetState("input_text.solar_production_energy_level", "black", map[string]interface{}{})
	mockClient.SetState("input_text.current_energy_level", "black", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	if err := manager.SyncFromHA(); err != nil {
		t.Fatalf("Failed to sync from HA: %v", err)
	}

	return manager, mockClient
}

// testEnergyStates returns standard test energy state configurations.
// This matches the ordering in the production energy config.
func testEnergyStates() []EnergyStateConfig {
	return []EnergyStateConfig{
		{
			ConditionName:                       "black",
			BatteryMinimumPercentage:            0,
			EnergyProductionMinimumKW:           0,
			RemainingEnergyProductionMinimumKWH: 0,
		},
		{
			ConditionName:                       "red",
			BatteryMinimumPercentage:            10,
			EnergyProductionMinimumKW:           0.1,
			RemainingEnergyProductionMinimumKWH: 0.5,
		},
		{
			ConditionName:                       "yellow",
			BatteryMinimumPercentage:            30,
			EnergyProductionMinimumKW:           0.5,
			RemainingEnergyProductionMinimumKWH: 2,
		},
		{
			ConditionName:                       "green",
			BatteryMinimumPercentage:            70,
			EnergyProductionMinimumKW:           2,
			RemainingEnergyProductionMinimumKWH: 10,
		},
	}
}

func TestRegisterSolarEnergyLevelProvider(t *testing.T) {
	manager, _ := setupTestManagerForEnergyRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)
	energyStates := testEnergyStates()

	var solarLevelReceived string
	callbacks := &EnergyComputedStateCallback{
		OnSolarLevelUpdate: func(level string) {
			solarLevelReceived = level
		},
	}

	err := RegisterSolarEnergyLevelProvider(registry, energyStates, callbacks)
	if err != nil {
		t.Fatalf("Failed to register solar energy level provider: %v", err)
	}

	err = registry.Start()
	if err != nil {
		t.Fatalf("Failed to start registry: %v", err)
	}
	defer registry.Stop()

	// Initial value should be black (no solar generation)
	level, err := manager.GetString("solarProductionEnergyLevel")
	if err != nil {
		t.Fatalf("Failed to get solarProductionEnergyLevel: %v", err)
	}
	if level != "black" {
		t.Errorf("Expected initial level 'black', got '%s'", level)
	}
	if solarLevelReceived != "black" {
		t.Errorf("Expected callback with 'black', got '%s'", solarLevelReceived)
	}
}

func TestSolarEnergyLevelComputation(t *testing.T) {
	tests := []struct {
		name          string
		thisHourKW    float64
		remainingKWH  float64
		expectedLevel string
	}{
		{
			name:          "zero solar",
			thisHourKW:    0,
			remainingKWH:  0,
			expectedLevel: "black",
		},
		{
			name:          "minimal solar - red",
			thisHourKW:    0.15,
			remainingKWH:  1.0,
			expectedLevel: "red",
		},
		{
			name:          "moderate solar - yellow",
			thisHourKW:    0.8,
			remainingKWH:  5.0,
			expectedLevel: "yellow",
		},
		{
			name:          "high solar - green",
			thisHourKW:    3.0,
			remainingKWH:  15.0,
			expectedLevel: "green",
		},
		{
			name:          "high thisHourKW but low remaining - still black",
			thisHourKW:    5.0,
			remainingKWH:  0,
			expectedLevel: "black",
		},
		{
			name:          "low thisHourKW but high remaining - still black",
			thisHourKW:    0,
			remainingKWH:  20.0,
			expectedLevel: "black",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manager, _ := setupTestManagerForEnergyRegistry(t)
			logger := testlogger.New()

			// Set test values
			_ = manager.SetNumber("thisHourSolarGeneration", tc.thisHourKW)
			_ = manager.SetNumber("remainingSolarGeneration", tc.remainingKWH)

			registry := NewComputedStateRegistry(manager, logger)
			energyStates := testEnergyStates()

			err := RegisterSolarEnergyLevelProvider(registry, energyStates, nil)
			if err != nil {
				t.Fatalf("Failed to register provider: %v", err)
			}

			err = registry.Start()
			if err != nil {
				t.Fatalf("Failed to start registry: %v", err)
			}
			defer registry.Stop()

			level, err := manager.GetString("solarProductionEnergyLevel")
			if err != nil {
				t.Fatalf("Failed to get solarProductionEnergyLevel: %v", err)
			}
			if level != tc.expectedLevel {
				t.Errorf("Expected level '%s', got '%s'", tc.expectedLevel, level)
			}
		})
	}
}

func TestRegisterCurrentEnergyLevelProvider(t *testing.T) {
	manager, _ := setupTestManagerForEnergyRegistry(t)
	logger := testlogger.New()

	// Set test values
	_ = manager.SetBool("isFreeEnergyAvailable", false)
	_ = manager.SetString("batteryEnergyLevel", "green")
	_ = manager.SetString("solarProductionEnergyLevel", "yellow")

	registry := NewComputedStateRegistry(manager, logger)
	energyStates := testEnergyStates()

	var overallLevelReceived string
	callbacks := &EnergyComputedStateCallback{
		OnOverallLevelUpdate: func(level string) {
			overallLevelReceived = level
		},
	}

	err := RegisterCurrentEnergyLevelProvider(registry, energyStates, callbacks)
	if err != nil {
		t.Fatalf("Failed to register current energy level provider: %v", err)
	}

	err = registry.Start()
	if err != nil {
		t.Fatalf("Failed to start registry: %v", err)
	}
	defer registry.Stop()

	// Battery green, solar yellow -> overall green (solar can't boost beyond battery)
	level, err := manager.GetString("currentEnergyLevel")
	if err != nil {
		t.Fatalf("Failed to get currentEnergyLevel: %v", err)
	}
	if level != "green" {
		t.Errorf("Expected level 'green', got '%s'", level)
	}
	if overallLevelReceived != "green" {
		t.Errorf("Expected callback with 'green', got '%s'", overallLevelReceived)
	}
}

func TestCurrentEnergyLevelComputation(t *testing.T) {
	tests := []struct {
		name          string
		isFreeEnergy  bool
		batteryLevel  string
		solarLevel    string
		expectedLevel string
		description   string
	}{
		{
			name:          "retired free energy flag ignored",
			isFreeEnergy:  true,
			batteryLevel:  "black",
			solarLevel:    "black",
			expectedLevel: "black",
			description:   "Legacy free energy flag no longer overrides the battery and solar levels",
		},
		{
			name:          "battery and solar same",
			isFreeEnergy:  false,
			batteryLevel:  "yellow",
			solarLevel:    "yellow",
			expectedLevel: "yellow",
			description:   "Same levels result in that level",
		},
		{
			name:          "battery higher than solar",
			isFreeEnergy:  false,
			batteryLevel:  "green",
			solarLevel:    "red",
			expectedLevel: "green",
			description:   "Solar can't drag down battery",
		},
		{
			name:          "solar higher by 1 - boosts",
			isFreeEnergy:  false,
			batteryLevel:  "yellow",
			solarLevel:    "green",
			expectedLevel: "green",
			description:   "Solar can boost by 1 level",
		},
		{
			name:          "solar much higher - only boosts by 1",
			isFreeEnergy:  false,
			batteryLevel:  "black",
			solarLevel:    "green",
			expectedLevel: "red",
			description:   "Solar can only boost by max 1 level",
		},
		{
			name:          "battery at max, solar same",
			isFreeEnergy:  false,
			batteryLevel:  "green",
			solarLevel:    "green",
			expectedLevel: "green",
			description:   "Already at max level",
		},
		{
			name:          "both at minimum",
			isFreeEnergy:  false,
			batteryLevel:  "black",
			solarLevel:    "black",
			expectedLevel: "black",
			description:   "Lowest possible level",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manager, _ := setupTestManagerForEnergyRegistry(t)
			logger := testlogger.New()

			// Set test values
			_ = manager.SetBool("isFreeEnergyAvailable", tc.isFreeEnergy)
			_ = manager.SetString("batteryEnergyLevel", tc.batteryLevel)
			_ = manager.SetString("solarProductionEnergyLevel", tc.solarLevel)

			registry := NewComputedStateRegistry(manager, logger)
			energyStates := testEnergyStates()

			err := RegisterCurrentEnergyLevelProvider(registry, energyStates, nil)
			if err != nil {
				t.Fatalf("Failed to register provider: %v", err)
			}

			err = registry.Start()
			if err != nil {
				t.Fatalf("Failed to start registry: %v", err)
			}
			defer registry.Stop()

			level, err := manager.GetString("currentEnergyLevel")
			if err != nil {
				t.Fatalf("Failed to get currentEnergyLevel: %v", err)
			}
			if level != tc.expectedLevel {
				t.Errorf("%s: Expected level '%s', got '%s'", tc.description, tc.expectedLevel, level)
			}
		})
	}
}

func TestCurrentEnergyLevelRecomputesOnDependencyChange(t *testing.T) {
	manager, _ := setupTestManagerForEnergyRegistry(t)
	logger := testlogger.New()

	// Set initial values
	_ = manager.SetBool("isFreeEnergyAvailable", false)
	_ = manager.SetString("batteryEnergyLevel", "yellow")
	_ = manager.SetString("solarProductionEnergyLevel", "yellow")

	registry := NewComputedStateRegistry(manager, logger)
	energyStates := testEnergyStates()

	err := RegisterCurrentEnergyLevelProvider(registry, energyStates, nil)
	if err != nil {
		t.Fatalf("Failed to register provider: %v", err)
	}

	err = registry.Start()
	if err != nil {
		t.Fatalf("Failed to start registry: %v", err)
	}
	defer registry.Stop()

	// Initial: yellow + yellow = yellow
	level, _ := manager.GetString("currentEnergyLevel")
	if level != "yellow" {
		t.Errorf("Initial: Expected 'yellow', got '%s'", level)
	}

	// Change batteryEnergyLevel to green - this triggers the subscription
	_ = manager.SetString("batteryEnergyLevel", "green")

	// Should recompute to green
	level, _ = manager.GetString("currentEnergyLevel")
	if level != "green" {
		t.Errorf("After battery change: Expected 'green', got '%s'", level)
	}

	// Enable the retired free energy flag - this is no longer a dependency.
	_ = manager.SetBool("isFreeEnergyAvailable", true)

	// Should remain based on battery and solar.
	level, _ = manager.GetString("currentEnergyLevel")
	if level != "green" {
		t.Errorf("With legacy free energy flag: Expected 'green', got '%s'", level)
	}
}

func TestRegisterEnergyProviders(t *testing.T) {
	manager, _ := setupTestManagerForEnergyRegistry(t)
	logger := testlogger.New()

	// Set test values
	_ = manager.SetNumber("thisHourSolarGeneration", 1.0)
	_ = manager.SetNumber("remainingSolarGeneration", 5.0)
	_ = manager.SetBool("isFreeEnergyAvailable", false)
	_ = manager.SetString("batteryEnergyLevel", "yellow")

	registry := NewComputedStateRegistry(manager, logger)
	energyStates := testEnergyStates()

	callbacksCalled := make(map[string]string)
	callbacks := &EnergyComputedStateCallback{
		OnSolarLevelUpdate: func(level string) {
			callbacksCalled["solar"] = level
		},
		OnOverallLevelUpdate: func(level string) {
			callbacksCalled["overall"] = level
		},
	}

	err := RegisterEnergyProviders(registry, energyStates, callbacks)
	if err != nil {
		t.Fatalf("Failed to register energy providers: %v", err)
	}

	err = registry.Start()
	if err != nil {
		t.Fatalf("Failed to start registry: %v", err)
	}
	defer registry.Stop()

	// Check that both providers registered and computed
	names := registry.GetProviderNames()
	if len(names) != 2 {
		t.Errorf("Expected 2 providers, got %d: %v", len(names), names)
	}

	// Solar level: thisHourKW=1.0, remainingKWH=5.0 -> yellow
	solarLevel, _ := manager.GetString("solarProductionEnergyLevel")
	if solarLevel != "yellow" {
		t.Errorf("Expected solar level 'yellow', got '%s'", solarLevel)
	}

	// Overall level: battery=yellow, solar=yellow -> yellow
	overallLevel, _ := manager.GetString("currentEnergyLevel")
	if overallLevel != "yellow" {
		t.Errorf("Expected overall level 'yellow', got '%s'", overallLevel)
	}

	// Check callbacks were called
	if callbacksCalled["solar"] != "yellow" {
		t.Errorf("Expected solar callback 'yellow', got '%s'", callbacksCalled["solar"])
	}
	if callbacksCalled["overall"] != "yellow" {
		t.Errorf("Expected overall callback 'yellow', got '%s'", callbacksCalled["overall"])
	}
}

func TestEnergyLevelInvalidLevelHandling(t *testing.T) {
	manager, _ := setupTestManagerForEnergyRegistry(t)
	logger := testlogger.New()

	// Set invalid level names
	_ = manager.SetBool("isFreeEnergyAvailable", false)
	_ = manager.SetString("batteryEnergyLevel", "invalid_level")
	_ = manager.SetString("solarProductionEnergyLevel", "also_invalid")

	registry := NewComputedStateRegistry(manager, logger)
	energyStates := testEnergyStates()

	err := RegisterCurrentEnergyLevelProvider(registry, energyStates, nil)
	if err != nil {
		t.Fatalf("Failed to register provider: %v", err)
	}

	err = registry.Start()
	if err != nil {
		t.Fatalf("Failed to start registry: %v", err)
	}
	defer registry.Stop()

	// Invalid levels should result in "black" (fallback)
	level, _ := manager.GetString("currentEnergyLevel")
	if level != "black" {
		t.Errorf("Invalid levels should fallback to 'black', got '%s'", level)
	}
}

func TestDependencyGraph(t *testing.T) {
	manager, _ := setupTestManagerForEnergyRegistry(t)
	logger := testlogger.New()

	registry := NewComputedStateRegistry(manager, logger)
	energyStates := testEnergyStates()

	err := RegisterEnergyProviders(registry, energyStates, nil)
	if err != nil {
		t.Fatalf("Failed to register providers: %v", err)
	}

	graph := registry.GetDependencyGraph()

	// Check solarProductionEnergyLevel dependencies
	solarDeps, ok := graph["solarProductionEnergyLevel"]
	if !ok {
		t.Fatal("solarProductionEnergyLevel not in dependency graph")
	}
	expectedSolarDeps := map[string]bool{
		"thisHourSolarGeneration":  true,
		"remainingSolarGeneration": true,
	}
	for _, dep := range solarDeps {
		if !expectedSolarDeps[dep] {
			t.Errorf("Unexpected solar dependency: %s", dep)
		}
	}

	// Check currentEnergyLevel dependencies
	overallDeps, ok := graph["currentEnergyLevel"]
	if !ok {
		t.Fatal("currentEnergyLevel not in dependency graph")
	}
	expectedOverallDeps := map[string]bool{
		"batteryEnergyLevel":         true,
		"solarProductionEnergyLevel": true,
	}
	for _, dep := range overallDeps {
		if !expectedOverallDeps[dep] {
			t.Errorf("Unexpected overall dependency: %s", dep)
		}
	}
}

// TestSolarBoostMaxOneLevel ensures solar can only boost battery by 1 level.
// This is a regression test for the specific boost logic.
func TestSolarBoostMaxOneLevel(t *testing.T) {
	testCases := []struct {
		battery  string
		solar    string
		expected string
	}{
		{"black", "red", "red"},      // boost by 1
		{"black", "yellow", "red"},   // boost limited to 1
		{"black", "green", "red"},    // boost limited to 1
		{"red", "yellow", "yellow"},  // boost by 1
		{"red", "green", "yellow"},   // boost limited to 1
		{"yellow", "green", "green"}, // boost by 1
		{"green", "green", "green"},  // already at max
	}

	for _, tc := range testCases {
		t.Run(tc.battery+"_"+tc.solar, func(t *testing.T) {
			manager, _ := setupTestManagerForEnergyRegistry(t)
			logger := zap.NewNop()

			// Set test values
			_ = manager.SetBool("isFreeEnergyAvailable", false)
			_ = manager.SetString("batteryEnergyLevel", tc.battery)
			_ = manager.SetString("solarProductionEnergyLevel", tc.solar)

			registry := NewComputedStateRegistry(manager, logger)
			energyStates := testEnergyStates()

			_ = RegisterCurrentEnergyLevelProvider(registry, energyStates, nil)
			_ = registry.Start()
			defer registry.Stop()

			level, _ := manager.GetString("currentEnergyLevel")
			if level != tc.expected {
				t.Errorf("battery=%s, solar=%s: expected '%s', got '%s'",
					tc.battery, tc.solar, tc.expected, level)
			}
		})
	}
}
