package lighting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_AwayNightLight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		block   string
		wantErr string
	}{
		{"valid", "lights: [light.example_can]\n      brightness_pct: 1\n      kelvin: 3000", ""},
		{"valid without kelvin", "lights: [light.example_can]\n      brightness_pct: 5", ""},
		{"no lights", "lights: []\n      brightness_pct: 1", "lights is empty"},
		{"not a light", "lights: [switch.example]\n      brightness_pct: 1", "not a light entity"},
		{"brightness zero", "lights: [light.example_can]\n      brightness_pct: 0", "brightness_pct must be 1-100"},
		{"brightness too high", "lights: [light.example_can]\n      brightness_pct: 101", "brightness_pct must be 1-100"},
		{"negative kelvin", "lights: [light.example_can]\n      brightness_pct: 1\n      kelvin: -1", "kelvin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			content := "rooms:\n  - hue_group: Example\n    hass_area_id: example\n" +
				"    away_night_light:\n      " + tt.block + "\n"
			path := filepath.Join(t.TempDir(), "hue.yaml")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if n := cfg.Rooms[0].AwayNightLight; n == nil || n.Lights[0] != "light.example_can" {
					t.Fatalf("away_night_light not parsed: %+v", n)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
