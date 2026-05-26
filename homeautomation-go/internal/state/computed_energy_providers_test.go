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
	mockClient.SetState("input_text.battery_energy_level", "red", map[string]interface{}{})
	mockClient.SetState("input_text.solar_production_energy_level", "red", map[string]interface{}{})
	mockClient.SetState("input_text.current_energy_level", "red", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	if err := manager.SyncFromHA(); err != nil {
		t.Fatalf("Failed to sync from HA: %v", err)
	}

	return manager, mockClient
}

// testEnergyStates returns standard test energy state configurations.
// This matches the 4-level scheme in the production energy config.
func testEnergyStates() []EnergyStateConfig {
	return []EnergyStateConfig{
		{
			ConditionName:                       "red",
			BatteryMinimumPercentage:            0,
			EnergyProductionMinimumKW:           0,
			RemainingEnergyProductionMinimumKWH: 0,
		},
		{
			ConditionName:                       "yellow",
			BatteryMinimumPercentage:            15,
			EnergyProductionMinimumKW:           0.1,
			RemainingEnergyProductionMinimumKWH: 0,
		},
		{
			ConditionName:                       "green",
			BatteryMinimumPercentage:            60,
			EnergyProductionMinimumKW:           1,
			RemainingEnergyProductionMinimumKWH: 5,
		},
		{
			ConditionName:                       "white",
			BatteryMinimumPercentage:            80,
			EnergyProductionMinimumKW:           4,
			RemainingEnergyProductionMinimumKWH: 20,
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

	// Initial value should be red (lowest level — no solar generation)
	level, err := manager.GetString("solarProductionEnergyLevel")
	if err != nil {
		t.Fatalf("Failed to get solarProductionEnergyLevel: %v", err)
	}
	if level != "red" {
		t.Errorf("Expected initial level 'red', got '%s'", level)
	}
	if solarLevelReceived != "red" {
		t.Errorf("Expected callback with 'red', got '%s'", solarLevelReceived)
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
			name:          "zero solar - red",
			thisHourKW:    0,
			remainingKWH:  0,
			expectedLevel: "red",
		},
		{
			name:          "minimal current production - yellow",
			thisHourKW:    0.15,
			remainingKWH:  1.0,
			expectedLevel: "yellow",
		},
		{
			name:          "moderate solar - green",
			thisHourKW:    1.5,
			remainingKWH:  10.0,
			expectedLevel: "green",
		},
		{
			name:          "high solar - white",
			thisHourKW:    5.0,
			remainingKWH:  25.0,
			expectedLevel: "white",
		},
		{
			name:          "high thisHourKW but low remaining - capped at yellow",
			thisHourKW:    5.0,
			remainingKWH:  0,
			expectedLevel: "yellow",
		},
		{
			name:          "low thisHourKW but high remaining - stays at red",
			thisHourKW:    0,
			remainingKWH:  20.0,
			expectedLevel: "red",
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
			batteryLevel:  "red",
			solarLevel:    "red",
			expectedLevel: "red",
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
			name:          "battery higher than solar capped by weak solar",
			isFreeEnergy:  false,
			batteryLevel:  "green",
			solarLevel:    "red",
			expectedLevel: "yellow",
			description:   "Overall can be at most one level above the weaker input",
		},
		{
			name:          "white battery with weak solar does not produce white",
			isFreeEnergy:  false,
			batteryLevel:  "white",
			solarLevel:    "yellow",
			expectedLevel: "green",
			description:   "High battery alone should not trigger white-level behavior when solar is weak",
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
			batteryLevel:  "red",
			solarLevel:    "white",
			expectedLevel: "yellow",
			description:   "Solar can only boost by max 1 level",
		},
		{
			name:          "battery at max, solar same",
			isFreeEnergy:  false,
			batteryLevel:  "white",
			solarLevel:    "white",
			expectedLevel: "white",
			description:   "Already at max level",
		},
		{
			name:          "both at minimum",
			isFreeEnergy:  false,
			batteryLevel:  "red",
			solarLevel:    "red",
			expectedLevel: "red",
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
	_ = manager.SetNumber("thisHourSolarGeneration", 1.5)
	_ = manager.SetNumber("remainingSolarGeneration", 10.0)
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

	// Solar level: thisHourKW=1.5, remainingKWH=10.0 -> green
	solarLevel, _ := manager.GetString("solarProductionEnergyLevel")
	if solarLevel != "green" {
		t.Errorf("Expected solar level 'green', got '%s'", solarLevel)
	}

	// Overall level: battery=yellow, solar=green -> green (boost by 1)
	overallLevel, _ := manager.GetString("currentEnergyLevel")
	if overallLevel != "green" {
		t.Errorf("Expected overall level 'green', got '%s'", overallLevel)
	}

	// Check callbacks were called
	if callbacksCalled["solar"] != "green" {
		t.Errorf("Expected solar callback 'green', got '%s'", callbacksCalled["solar"])
	}
	if callbacksCalled["overall"] != "green" {
		t.Errorf("Expected overall callback 'green', got '%s'", callbacksCalled["overall"])
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

	// Invalid levels should fall back to the lowest configured level ("red")
	level, _ := manager.GetString("currentEnergyLevel")
	if level != "red" {
		t.Errorf("Invalid levels should fallback to 'red', got '%s'", level)
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
		{"red", "yellow", "yellow"},  // boost by 1
		{"red", "green", "yellow"},   // boost limited to 1
		{"red", "white", "yellow"},   // boost limited to 1
		{"yellow", "green", "green"}, // boost by 1
		{"yellow", "white", "green"}, // boost limited to 1
		{"green", "white", "white"},  // boost by 1
		{"white", "white", "white"},  // already at max
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
