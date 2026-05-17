package energy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel(
	// Create a temporary config file
	)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "energy_config.yaml")

	configContent := `---
energy:
  energy_states:
    - condition_name: white
      battery_minimum_percentage: 80.0
      energy_production_minimum_kw: 2.0
      remaining_energy_production_minimum_kwh: 10.0
      light_config:
        red: 255
        green: 255
        blue: 255
        brightness_pct: 100
    - condition_name: green
      battery_minimum_percentage: 60.0
      energy_production_minimum_kw: 1.0
      remaining_energy_production_minimum_kwh: 5.0
      light_config:
        red: 0
        green: 255
        blue: 0
        brightness_pct: 80
    - condition_name: red
      battery_minimum_percentage: 30.0
      energy_production_minimum_kw: 0.5
      remaining_energy_production_minimum_kwh: 2.0
      light_config:
        red: 255
        green: 0
        blue: 0
        brightness_pct: 60
    - condition_name: black
      battery_minimum_percentage: 0.0
      energy_production_minimum_kw: 0.0
      remaining_energy_production_minimum_kwh: 0.0
      light_config:
        red: 0
        green: 0
        blue: 0
        brightness_pct: 40
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the config
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify energy states count
	if len(config.Energy.EnergyStates) != 4 {
		t.Errorf("Expected 4 energy states, got %d", len(config.Energy.EnergyStates))
	}

	// Check white state (first)
	white := config.Energy.EnergyStates[0]
	if white.ConditionName != "white" {
		t.Errorf("Expected ConditionName 'white', got '%s'", white.ConditionName)
	}
	if white.BatteryMinimumPercentage != 80.0 {
		t.Errorf("Expected BatteryMinimumPercentage 80.0, got %f", white.BatteryMinimumPercentage)
	}
	if white.EnergyProductionMinimumKW != 2.0 {
		t.Errorf("Expected EnergyProductionMinimumKW 2.0, got %f", white.EnergyProductionMinimumKW)
	}
	if white.RemainingEnergyProductionMinimumKWH != 10.0 {
		t.Errorf("Expected RemainingEnergyProductionMinimumKWH 10.0, got %f", white.RemainingEnergyProductionMinimumKWH)
	}

	// Verify white state light config
	if white.LightConfig.Red != 255 {
		t.Errorf("Expected Red 255, got %d", white.LightConfig.Red)
	}
	if white.LightConfig.Green != 255 {
		t.Errorf("Expected Green 255, got %d", white.LightConfig.Green)
	}
	if white.LightConfig.Blue != 255 {
		t.Errorf("Expected Blue 255, got %d", white.LightConfig.Blue)
	}
	if white.LightConfig.BrightnessPct != 100 {
		t.Errorf("Expected BrightnessPct 100, got %d", white.LightConfig.BrightnessPct)
	}

	// Check green state
	green := config.Energy.EnergyStates[1]
	if green.ConditionName != "green" {
		t.Errorf("Expected ConditionName 'green', got '%s'", green.ConditionName)
	}
	if green.LightConfig.Green != 255 {
		t.Errorf("Expected Green 255, got %d", green.LightConfig.Green)
	}
	if green.LightConfig.Red != 0 {
		t.Errorf("Expected Red 0, got %d", green.LightConfig.Red)
	}

	// Check red state
	red := config.Energy.EnergyStates[2]
	if red.ConditionName != "red" {
		t.Errorf("Expected ConditionName 'red', got '%s'", red.ConditionName)
	}
	if red.LightConfig.Red != 255 {
		t.Errorf("Expected Red 255, got %d", red.LightConfig.Red)
	}
	if red.LightConfig.Green != 0 {
		t.Errorf("Expected Green 0, got %d", red.LightConfig.Green)
	}

	// Check black state
	black := config.Energy.EnergyStates[3]
	if black.ConditionName != "black" {
		t.Errorf("Expected ConditionName 'black', got '%s'", black.ConditionName)
	}
	if black.BatteryMinimumPercentage != 0.0 {
		t.Errorf("Expected BatteryMinimumPercentage 0.0, got %f", black.BatteryMinimumPercentage)
	}
	if black.LightConfig.BrightnessPct != 40 {
		t.Errorf("Expected BrightnessPct 40, got %d", black.LightConfig.BrightnessPct)
	}
}

func TestLoadConfigInvalidPath(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig("/nonexistent/path/energy_config.yaml")
	if err == nil {
		t.Error("Expected error for nonexistent config file, got nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidContent := `this is not: valid: yaml: content: definitely: broken`
	if err := os.WriteFile(configPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

func TestLoadConfigEmptyEnergyStates(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty_states.yaml")

	emptyContent := `---
energy:
  energy_states: []
`

	if err := os.WriteFile(configPath, []byte(emptyContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// This should successfully load even with empty energy states
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config with empty energy states: %v", err)
	}

	if len(config.Energy.EnergyStates) != 0 {
		t.Errorf("Expected 0 energy states, got %d", len(config.Energy.EnergyStates))
	}
}

func TestLoadConfigMinimalLightConfig(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "minimal_light.yaml")

	minimalContent := `---
energy:
  energy_states:
    - condition_name: test
      battery_minimum_percentage: 50.0
      energy_production_minimum_kw: 1.0
      remaining_energy_production_minimum_kwh: 5.0
      light_config:
        red: 100
        green: 100
        blue: 100
        brightness_pct: 50
`

	if err := os.WriteFile(configPath, []byte(minimalContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load minimal config: %v", err)
	}

	if len(config.Energy.EnergyStates) != 1 {
		t.Fatalf("Expected 1 energy state, got %d", len(config.Energy.EnergyStates))
	}

	state := config.Energy.EnergyStates[0]
	if state.ConditionName != "test" {
		t.Errorf("Expected ConditionName 'test', got '%s'", state.ConditionName)
	}
	if state.LightConfig.Red != 100 {
		t.Errorf("Expected Red 100, got %d", state.LightConfig.Red)
	}
}
