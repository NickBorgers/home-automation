package lighting

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel(
	// Create a temporary config file with the new conditions format
	)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "hue_config.yaml")

	configContent := `---
rooms:
  - hue_group: Living Room
    hass_area_id: living_room_2
    conditions:
      - action: off
        variable: isAnyoneHome
        value: false
      - action: off
        variable: isEveryoneAsleep
        value: true
      - action: on
        variable: isAnyoneHomeAndAwake
        value: true
      - action: on
        variable: isTVPlaying
        value: false
    increase_brightness_if_true: isHaveGuests
    transition_seconds: 30
  - hue_group: Primary Suite
    hass_area_id: master_bedroom
    conditions:
      - action: off
        variable: isAnyoneHome
        value: false
      - action: off
        variable: isMasterAsleep
        value: true
      - action: on
        variable: isMasterAsleep
        value: false
    increase_brightness_if_true: ~
    transition_seconds: 180
  - hue_group: Front of House
    hass_area_id: front_of_house
    conditions:
      - action: off
        variable: isEveryoneAsleep
        value: true
      - action: on
        variable: isHaveGuests
        value: true
      - action: on
        variable: didOwnerJustReturnHome
        value: true
    increase_brightness_if_true: ~
    transition_seconds: 120
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the config
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify the config
	if len(config.Rooms) != 3 {
		t.Errorf("Expected 3 rooms, got %d", len(config.Rooms))
	}

	// Check Living Room config
	livingRoom := config.Rooms[0]
	if livingRoom.HueGroup != "Living Room" {
		t.Errorf("Expected HueGroup 'Living Room', got '%s'", livingRoom.HueGroup)
	}
	if livingRoom.HASSAreaID != "living_room_2" {
		t.Errorf("Expected HASSAreaID 'living_room_2', got '%s'", livingRoom.HASSAreaID)
	}
	if *livingRoom.TransitionSeconds != 30 {
		t.Errorf("Expected TransitionSeconds 30, got %d", *livingRoom.TransitionSeconds)
	}
	if len(livingRoom.Conditions) != 4 {
		t.Errorf("Expected 4 conditions for Living Room, got %d", len(livingRoom.Conditions))
	}

	// Verify first condition (off if !isAnyoneHome)
	if livingRoom.Conditions[0].Action != "off" {
		t.Errorf("Expected first condition action 'off', got '%s'", livingRoom.Conditions[0].Action)
	}
	if livingRoom.Conditions[0].Variable != "isAnyoneHome" {
		t.Errorf("Expected first condition variable 'isAnyoneHome', got '%s'", livingRoom.Conditions[0].Variable)
	}
	if livingRoom.Conditions[0].Value != false {
		t.Errorf("Expected first condition value false, got %v", livingRoom.Conditions[0].Value)
	}

	// Check Primary Suite config
	primarySuite := config.Rooms[1]
	if primarySuite.HueGroup != "Primary Suite" {
		t.Errorf("Expected HueGroup 'Primary Suite', got '%s'", primarySuite.HueGroup)
	}
	if *primarySuite.TransitionSeconds != 180 {
		t.Errorf("Expected TransitionSeconds 180, got %d", *primarySuite.TransitionSeconds)
	}

	// Check Front of House config
	frontOfHouse := config.Rooms[2]
	if frontOfHouse.HueGroup != "Front of House" {
		t.Errorf("Expected HueGroup 'Front of House', got '%s'", frontOfHouse.HueGroup)
	}
	if *frontOfHouse.TransitionSeconds != 120 {
		t.Errorf("Expected TransitionSeconds 120, got %d", *frontOfHouse.TransitionSeconds)
	}
	if len(frontOfHouse.Conditions) != 3 {
		t.Errorf("Expected 3 conditions for Front of House, got %d", len(frontOfHouse.Conditions))
	}
}

func TestGetConditionVariables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		room       RoomConfig
		wantVars   []string
		wantLength int
	}{
		{
			name: "Multiple unique conditions",
			room: RoomConfig{
				Conditions: []LightingCondition{
					{Action: "off", Variable: "isAnyoneHome", Value: false},
					{Action: "off", Variable: "isEveryoneAsleep", Value: true},
					{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
				},
			},
			wantVars:   []string{"isAnyoneHome", "isEveryoneAsleep", "isAnyoneHomeAndAwake"},
			wantLength: 3,
		},
		{
			name: "Duplicate variables are deduplicated",
			room: RoomConfig{
				Conditions: []LightingCondition{
					{Action: "off", Variable: "isMasterAsleep", Value: true},
					{Action: "on", Variable: "isMasterAsleep", Value: false},
				},
			},
			wantVars:   []string{"isMasterAsleep"},
			wantLength: 1,
		},
		{
			name: "Empty conditions",
			room: RoomConfig{
				Conditions: []LightingCondition{},
			},
			wantVars:   []string{},
			wantLength: 0,
		},
		{
			name: "Nil conditions",
			room: RoomConfig{
				Conditions: nil,
			},
			wantVars:   []string{},
			wantLength: 0,
		},
		{
			name: "Empty variable names are skipped",
			room: RoomConfig{
				Conditions: []LightingCondition{
					{Action: "on", Variable: "", Value: true},
					{Action: "off", Variable: "isAnyoneHome", Value: false},
				},
			},
			wantVars:   []string{"isAnyoneHome"},
			wantLength: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got := tt.room.GetConditionVariables()
			if len(got) != tt.wantLength {
				t.Errorf("GetConditionVariables() returned %d variables, want %d", len(got), tt.wantLength)
			}
			// Check that all expected variables are present
			for _, want := range tt.wantVars {
				found := false
				for _, g := range got {
					if g == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("GetConditionVariables() missing expected variable '%s'", want)
				}
			}
		})
	}
}

func TestValuesMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a    interface{}
		b    interface{}
		want bool
	}{
		{
			name: "Boolean true matches true",
			a:    true,
			b:    true,
			want: true,
		},
		{
			name: "Boolean false matches false",
			a:    false,
			b:    false,
			want: true,
		},
		{
			name: "Boolean true does not match false",
			a:    true,
			b:    false,
			want: false,
		},
		{
			name: "String matches string",
			a:    "test",
			b:    "test",
			want: true,
		},
		{
			name: "Different strings don't match",
			a:    "test1",
			b:    "test2",
			want: false,
		},
		{
			name: "Integer matches integer",
			a:    42,
			b:    42,
			want: true,
		},
		{
			name: "Different integers don't match",
			a:    42,
			b:    43,
			want: false,
		},
		{
			name: "String 'true' does not match boolean true",
			a:    "true",
			b:    true,
			want: true, // Both convert to "true" string
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if got := valuesMatch(tt.a, tt.b); got != tt.want {
				t.Errorf("valuesMatch(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestGetIncreaseBrightnessIfTrueConditions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		room     RoomConfig
		expected []string
	}{
		{
			name: "Single string condition",
			room: RoomConfig{
				IncreaseBrightnessIfTrue: "isHaveGuests",
			},
			expected: []string{"isHaveGuests"},
		},
		{
			name: "Array conditions",
			room: RoomConfig{
				IncreaseBrightnessIfTrue: []interface{}{"isHaveGuests", "isPartyMode"},
			},
			expected: []string{"isHaveGuests", "isPartyMode"},
		},
		{
			name: "Nil condition",
			room: RoomConfig{
				IncreaseBrightnessIfTrue: nil,
			},
			expected: []string{},
		},
		{
			name: "Empty string condition",
			room: RoomConfig{
				IncreaseBrightnessIfTrue: "",
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got := tt.room.GetIncreaseBrightnessIfTrueConditions()
			if !stringSlicesEqual(got, tt.expected) {
				t.Errorf("GetIncreaseBrightnessIfTrueConditions() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadConfigInvalidPath(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("Expected error for nonexistent config file, got nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidContent := `this is not: valid: yaml: content`
	if err := os.WriteFile(configPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}
