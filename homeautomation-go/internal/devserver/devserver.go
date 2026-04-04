// Package devserver provides a development server using a mock Home Assistant
// backend for local UI testing without requiring real HA credentials.
package devserver

import (
	"fmt"
	"time"

	"homeautomation/pkg/testutil"

	"go.uber.org/zap"
)

const (
	// DefaultDevPort is the default port for the mock HA server
	DefaultDevPort = 18123
	// DefaultDevToken is the token used for the mock HA server
	DefaultDevToken = "dev_mode_token"
)

// DevServer wraps the mock HA server with sample data for development
type DevServer struct {
	server *testutil.MockHAServer
	logger *zap.Logger
	port   int
}

// NewDevServer creates a new development server
func NewDevServer(logger *zap.Logger, port int) *DevServer {
	if port == 0 {
		port = DefaultDevPort
	}
	return &DevServer{
		logger: logger,
		port:   port,
	}
}

// Start starts the development server with sample data
func (d *DevServer) Start() error {
	addr := fmt.Sprintf("localhost:%d", d.port)
	d.server = testutil.NewMockHAServer(addr, DefaultDevToken)

	// Initialize basic states first
	d.server.InitializeStates()

	// Then populate with more realistic sample data
	d.populateSampleData()

	if err := d.server.Start(); err != nil {
		return fmt.Errorf("failed to start dev server: %w", err)
	}

	d.logger.Info("Development mock HA server started",
		zap.String("addr", addr),
		zap.String("websocket_url", d.GetWebSocketURL()))

	return nil
}

// Stop stops the development server
func (d *DevServer) Stop() error {
	if d.server != nil {
		return d.server.Stop()
	}
	return nil
}

// GetWebSocketURL returns the WebSocket URL for the mock server
func (d *DevServer) GetWebSocketURL() string {
	return fmt.Sprintf("ws://localhost:%d/api/websocket", d.port)
}

// GetToken returns the authentication token for the mock server
func (d *DevServer) GetToken() string {
	return DefaultDevToken
}

// populateSampleData sets up realistic sample data for UI development
func (d *DevServer) populateSampleData() {
	// Presence states - simulate Nick is home, Caroline is away
	d.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{
		"friendly_name": "Nick Home",
	})
	d.server.SetState("input_boolean.caroline_home", "off", map[string]interface{}{
		"friendly_name": "Caroline Home",
	})
	d.server.SetState("input_boolean.assistant_here", "off", map[string]interface{}{
		"friendly_name": "Assistant Here",
	})
	d.server.SetState("input_boolean.any_owner_home", "on", map[string]interface{}{
		"friendly_name": "Any Owner Home",
	})
	d.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{
		"friendly_name": "Anyone Home",
	})
	d.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{
		"friendly_name": "Anyone Home And Awake",
	})

	// Sleep states
	d.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{
		"friendly_name": "Master Asleep",
	})
	d.server.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{
		"friendly_name": "Guest Asleep",
	})
	d.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{
		"friendly_name": "Anyone Asleep",
	})
	d.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{
		"friendly_name": "Everyone Asleep",
	})

	// Guest states
	d.server.SetState("input_boolean.guest_bedroom_door_open", "on", map[string]interface{}{
		"friendly_name": "Guest Bedroom Door Open",
	})
	d.server.SetState("input_boolean.have_guests", "off", map[string]interface{}{
		"friendly_name": "Have Guests",
	})

	// TV states - simulate TV is on but not playing
	d.server.SetState("input_boolean.apple_tv_playing", "off", map[string]interface{}{
		"friendly_name": "Apple TV Playing",
	})
	d.server.SetState("input_boolean.tv_playing", "off", map[string]interface{}{
		"friendly_name": "TV Playing",
	})
	d.server.SetState("input_boolean.tv_on", "on", map[string]interface{}{
		"friendly_name": "TV On",
	})

	// Energy states - simulate afternoon with moderate solar
	d.server.SetState("input_boolean.fade_out_in_progress", "off", map[string]interface{}{
		"friendly_name": "Fade Out In Progress",
	})
	d.server.SetState("input_boolean.free_energy_available", "on", map[string]interface{}{
		"friendly_name": "Free Energy Available",
	})
	d.server.SetState("input_boolean.grid_available", "on", map[string]interface{}{
		"friendly_name": "Grid Available",
	})

	// Security states
	d.server.SetState("input_boolean.expecting_someone", "off", map[string]interface{}{
		"friendly_name": "Expecting Someone",
	})
	d.server.SetState("input_boolean.reset", "off", map[string]interface{}{
		"friendly_name": "Reset",
	})

	// Number states with realistic values
	now := time.Now()
	alarmTime := time.Date(now.Year(), now.Month(), now.Day()+1, 7, 30, 0, 0, now.Location())
	d.server.SetState("input_number.alarm_time", fmt.Sprintf("%.0f", float64(alarmTime.Unix())), map[string]interface{}{
		"friendly_name": "Alarm Time",
	})
	d.server.SetState("input_number.remaining_solar_generation", "4500", map[string]interface{}{
		"friendly_name":       "Remaining Solar Generation",
		"unit_of_measurement": "Wh",
	})
	d.server.SetState("input_number.this_hour_solar_generation", "850", map[string]interface{}{
		"friendly_name":       "This Hour Solar Generation",
		"unit_of_measurement": "Wh",
	})

	// Text states with sample values
	d.server.SetState("input_text.day_phase", "afternoon", map[string]interface{}{
		"friendly_name": "Day Phase",
	})
	d.server.SetState("input_text.sun_event", "day", map[string]interface{}{
		"friendly_name": "Sun Event",
	})
	d.server.SetState("input_text.music_playback_type", "background", map[string]interface{}{
		"friendly_name": "Music Playback Type",
	})
	d.server.SetState("input_text.currently_playing_music_uri", "https://tidal.com/browse/playlist/b9c278db-c54b-405c-995c-48542a7e12c2", map[string]interface{}{
		"friendly_name": "Currently Playing Music URI",
	})
	d.server.SetState("input_text.battery_energy_level", "green", map[string]interface{}{
		"friendly_name": "Battery Energy Level",
	})
	d.server.SetState("input_text.current_energy_level", "green", map[string]interface{}{
		"friendly_name": "Current Energy Level",
	})
	d.server.SetState("input_text.solar_production_energy_level", "yellow", map[string]interface{}{
		"friendly_name": "Solar Production Energy Level",
	})

	// Simulate some sensor entities that plugins might monitor
	d.server.SetState("sensor.powerwall_battery_percentage", "75", map[string]interface{}{
		"friendly_name":       "Powerwall Battery",
		"unit_of_measurement": "%",
		"device_class":        "battery",
	})
	d.server.SetState("sensor.solar_power", "2850", map[string]interface{}{
		"friendly_name":       "Solar Power",
		"unit_of_measurement": "W",
		"device_class":        "power",
	})
	d.server.SetState("sensor.home_consumption", "1200", map[string]interface{}{
		"friendly_name":       "Home Consumption",
		"unit_of_measurement": "W",
		"device_class":        "power",
	})

	// Media player entity for music
	d.server.SetState("media_player.sonos_living_room", "playing", map[string]interface{}{
		"friendly_name":      "Sonos Living Room",
		"media_title":        "Today's Top Hits",
		"media_artist":       "Tidal",
		"volume_level":       0.35,
		"source":             "Tidal",
		"supported_features": 65471,
	})

	// Climate entity for thermostat
	d.server.SetState("climate.main_thermostat", "cool", map[string]interface{}{
		"friendly_name":       "Main Thermostat",
		"current_temperature": 74,
		"temperature":         72,
		"hvac_action":         "cooling",
		"preset_mode":         "home",
	})

	// Light entities for rooms
	d.server.SetState("light.living_room", "on", map[string]interface{}{
		"friendly_name":     "Living Room Lights",
		"brightness":        200,
		"color_temp_kelvin": 2857,
	})
	d.server.SetState("light.kitchen", "on", map[string]interface{}{
		"friendly_name":     "Kitchen Lights",
		"brightness":        255,
		"color_temp_kelvin": 3333,
	})
	d.server.SetState("light.bedroom", "off", map[string]interface{}{
		"friendly_name": "Bedroom Lights",
	})

	// Apple TV entity
	d.server.SetState("media_player.living_room_apple_tv", "idle", map[string]interface{}{
		"friendly_name": "Living Room Apple TV",
		"source":        "Home",
	})

	d.logger.Info("Sample data populated for development mode")
}
