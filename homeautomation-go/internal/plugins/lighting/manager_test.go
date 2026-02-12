package lighting

import (
	"context"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
)

// createTestConfig creates a test hue configuration using the new conditions format
func createTestConfig() *HueConfig {
	transition30 := 30
	transition180 := 180

	return &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Living Room",
				HASSAreaID: "living_room_2",
				Conditions: []LightingCondition{
					// Priority 0: TV playing -> SKIP (Hue Sync controls)
					{Action: "skip", Variable: "isTVPlaying", Value: true},
					// Priority 1: No one home -> OFF
					{Action: "off", Variable: "isAnyoneHome", Value: false},
					// Priority 2: Everyone asleep -> OFF
					{Action: "off", Variable: "isEveryoneAsleep", Value: true},
					// Priority 3: Someone home -> ON
					{Action: "on", Variable: "isAnyoneHome", Value: true},
					// Priority 4: TV not playing -> ON
					{Action: "on", Variable: "isTVPlaying", Value: false},
				},
				IncreaseBrightnessIfTrue: "isHaveGuests",
				TransitionSeconds:        &transition30,
			},
			{
				HueGroup:   "Primary Suite",
				HASSAreaID: "master_bedroom",
				Conditions: []LightingCondition{
					// Priority 1: No one home -> OFF
					{Action: "off", Variable: "isNickHome", Value: false},
					// Priority 2: Master asleep -> OFF
					{Action: "off", Variable: "isMasterAsleep", Value: true},
					// Priority 3: Master not asleep -> ON
					{Action: "on", Variable: "isMasterAsleep", Value: false},
				},
				IncreaseBrightnessIfTrue: nil,
				TransitionSeconds:        &transition180,
			},
		},
	}
}

func TestNewManager(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.haClient)
	assert.Equal(t, stateManager, manager.stateManager)
	assert.Equal(t, config, manager.config)
	assert.False(t, manager.readOnly)
}

func TestEvaluateConditions(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name           string
		stateOverrides map[string]bool
		roomIndex      int
		expectedAction string
		expectedVar    string
	}{
		{"Living room TV playing -> SKIP", map[string]bool{"isAnyoneHome": true, "isEveryoneAsleep": false, "isTVPlaying": true}, 0, "skip", "isTVPlaying"},
		{"Living room no one home -> OFF", map[string]bool{"isAnyoneHome": false, "isEveryoneAsleep": false, "isTVPlaying": false}, 0, "off", "isAnyoneHome"},
		{"Living room everyone asleep -> OFF", map[string]bool{"isAnyoneHome": true, "isEveryoneAsleep": true, "isTVPlaying": false}, 0, "off", "isEveryoneAsleep"},
		{"Living room someone home awake -> ON", map[string]bool{"isAnyoneHome": true, "isEveryoneAsleep": false, "isTVPlaying": false}, 0, "on", "isAnyoneHome"},
		{"Primary suite nick not home -> OFF", map[string]bool{"isNickHome": false, "isMasterAsleep": false}, 1, "off", "isNickHome"},
		{"Primary suite master asleep -> OFF", map[string]bool{"isNickHome": true, "isMasterAsleep": true}, 1, "off", "isMasterAsleep"},
		{"Primary suite master awake -> ON", map[string]bool{"isNickHome": true, "isMasterAsleep": false}, 1, "on", "isMasterAsleep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.stateOverrides {
				_ = stateManager.SetBool(k, v)
			}
			room := &config.Rooms[tt.roomIndex]
			action, matchedVar := manager.evaluateConditions(room)
			assert.Equal(t, tt.expectedAction, action)
			assert.Equal(t, tt.expectedVar, matchedVar)
		})
	}
}

func TestEvaluateConditionsNoMatch(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Create a room with no conditions
	config := &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Empty Room",
				HASSAreaID: "empty_room",
				Conditions: []LightingCondition{},
			},
		},
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	room := &config.Rooms[0]
	action, matchedVar := manager.evaluateConditions(room)
	assert.Equal(t, "", action, "No action expected when no conditions")
	assert.Equal(t, "", matchedVar, "No matched variable when no conditions")
}

func TestActivateScene(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		readOnly      bool
		dayPhase      string
		expectedCalls int
		expectedScene string
		expectedTrans int
	}{
		{
			name:          "Read-only mode makes no service calls",
			readOnly:      true,
			dayPhase:      "Morning",
			expectedCalls: 0,
		},
		{
			name:          "Morning scene activates correctly",
			readOnly:      false,
			dayPhase:      "Morning",
			expectedCalls: 1,
			expectedScene: "scene.living_room_morning",
			expectedTrans: 30,
		},
		{
			// Regression test for PR #169: area_id must NOT be passed
			name:          "Dusk scene activates correctly (not energize)",
			readOnly:      false,
			dayPhase:      "Dusk",
			expectedCalls: 1,
			expectedScene: "scene.living_room_dusk",
			expectedTrans: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testlogger.New()
			config := createTestConfig()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			manager := NewManager(context.Background(), mockClient, stateManager, config, logger, tt.readOnly, nil)

			room := &config.Rooms[0]
			manager.activateScene(room, tt.dayPhase, "test_trigger")

			calls := mockClient.GetServiceCalls()
			assert.Equal(t, tt.expectedCalls, len(calls))

			if tt.expectedCalls > 0 {
				call := calls[0]
				assert.Equal(t, "scene", call.Domain)
				assert.Equal(t, "turn_on", call.Service)
				assert.Equal(t, tt.expectedScene, call.Data["entity_id"])
				assert.Nil(t, call.Data["area_id"], "area_id must not be passed for scene activation")
				assert.Equal(t, tt.expectedTrans, call.Data["transition"])
			}
		})
	}
}

func TestTurnOffRoom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		readOnly      bool
		expectedCalls int
	}{
		{"Read-only mode makes no service calls", true, 0},
		{"Normal mode turns off lights", false, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testlogger.New()
			config := createTestConfig()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			manager := NewManager(context.Background(), mockClient, stateManager, config, logger, tt.readOnly, nil)

			room := &config.Rooms[0]
			manager.turnOffRoom(room, "test_trigger")

			calls := mockClient.GetServiceCalls()
			assert.Equal(t, tt.expectedCalls, len(calls))

			if tt.expectedCalls > 0 {
				call := calls[0]
				assert.Equal(t, "light", call.Domain)
				assert.Equal(t, "turn_off", call.Service)
				assert.Equal(t, "living_room_2", call.Data["area_id"])
				assert.Nil(t, call.Data["transition"], "turn_off should not include transition")
			}
		})
	}
}

func TestEvaluateAndActivateRoom(t *testing.T) {
	logger := testlogger.New()
	config := createTestConfig()

	tests := []struct {
		name             string
		isAnyoneHome     bool
		isEveryoneAsleep bool
		isTVPlaying      bool
		dayPhase         string
		expectedDomain   string
		expectedService  string
	}{
		{"No one home turns off", false, false, false, "Night", "light", "turn_off"},
		{"Everyone asleep turns off", true, true, false, "Night", "light", "turn_off"},
		{"Someone home and awake turns on scene", true, false, false, "Morning", "scene", "turn_on"},
		{"TV playing skips (no call)", true, false, true, "Morning", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

			_ = stateManager.SetBool("isAnyoneHome", tt.isAnyoneHome)
			_ = stateManager.SetBool("isEveryoneAsleep", tt.isEveryoneAsleep)
			_ = stateManager.SetBool("isTVPlaying", tt.isTVPlaying)

			room := &config.Rooms[0]
			manager.evaluateAndActivateRoom(room, tt.dayPhase, "")

			calls := mockClient.GetServiceCalls()
			var lightingCalls []ha.ServiceCall
			for _, call := range calls {
				if call.Domain == "scene" || call.Domain == "light" {
					lightingCalls = append(lightingCalls, call)
				}
			}

			if tt.expectedDomain != "" {
				found := false
				for _, call := range lightingCalls {
					if call.Domain == tt.expectedDomain && call.Service == tt.expectedService {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected %s.%s call", tt.expectedDomain, tt.expectedService)
			} else {
				assert.Equal(t, 0, len(lightingCalls), "Expected no lighting service calls")
			}
		})
	}
}

func TestStart(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	// Start manager
	err := manager.Start()
	assert.NoError(t, err)

	// Verify subscriptions were created
	// The state manager should have subscriptions for all the lighting-related states
	// We can verify this by triggering a state change and checking if the handler is called

	// Set initial state
	err = stateManager.SetString("dayPhase", "Morning")
	assert.NoError(t, err)

	err = stateManager.SetBool("isAnyoneHome", true)
	assert.NoError(t, err)

	// Change day phase - this should trigger scene activation
	err = stateManager.SetString("dayPhase", "Day")
	assert.NoError(t, err)

	// Verify that scenes were activated (service calls were made)
	calls := mockClient.GetServiceCalls()
	assert.Greater(t, len(calls), 0, "Expected service calls after day phase change")
}

// TestLightingManager_Stop tests the Stop method and subscription cleanup
func TestLightingManager_Stop(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Create minimal config
	config := &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Living Room",
				HASSAreaID: "living_room",
				Conditions: []LightingCondition{},
			},
		},
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	// Initialize required state variables
	_ = stateManager.SetString("dayPhase", "morning")
	_ = stateManager.SetString("sunevent", "sunrise")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isHaveGuests", false)

	// Start manager (creates subscriptions)
	err := manager.Start()
	assert.NoError(t, err)

	// Verify subscriptions were created (7 state subscriptions)
	assert.Equal(t, 7, len(manager.subHelper.GetStateSubscriptions()), "Should have 7 state subscriptions")

	// Stop manager
	manager.Stop()

	// Verify subscriptions were cleaned up
	assert.Equal(t, 0, len(manager.subHelper.GetStateSubscriptions()), "State subscriptions should be empty after Stop")
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	hueConfig := createTestConfig()

	// Set day phase
	stateManager.SetString("dayPhase", "morning")

	manager := NewManager(context.Background(), mockClient, stateManager, hueConfig, logger, false, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-apply lighting scenes for all rooms
	err = manager.Reset()
	assert.NoError(t, err)
}

func TestIsTopicRelevant(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name      string
		roomIndex int
		trigger   string
		expected  bool
	}{
		{"dayPhase is always relevant", 0, "dayPhase", true},
		{"sunevent is always relevant", 0, "sunevent", true},
		{"reset is always relevant", 0, "reset", true},
		{"empty trigger is always relevant", 0, "", true},
		{"isAnyoneHome relevant to Living Room", 0, "isAnyoneHome", true},
		{"isMasterAsleep not relevant to Living Room", 0, "isMasterAsleep", false},
		{"isMasterAsleep relevant to Primary Suite", 1, "isMasterAsleep", true},
		{"unrelated variable not relevant", 0, "isKitchenOccupied", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.isTopicRelevant(&config.Rooms[tt.roomIndex], tt.trigger)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetStateValue(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetString("dayPhase", "morning")

	tests := []struct {
		name      string
		variable  string
		expected  interface{}
		expectErr bool
	}{
		{"Boolean retrieval", "isAnyoneHome", true, false},
		{"String retrieval", "dayPhase", "morning", false},
		{"Nonexistent variable", "nonexistent", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := manager.getStateValue(tt.variable)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, val)
			}
		})
	}
}

func TestCollectConditionVariables(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

	vars := manager.collectConditionVariables()

	// Should not include variables that are already subscribed to explicitly
	// (dayPhase, sunevent, isAnyoneHome, isTVPlaying, isEveryoneAsleep, isMasterAsleep, isHaveGuests)
	for _, v := range vars {
		assert.NotEqual(t, "dayPhase", v)
		assert.NotEqual(t, "sunevent", v)
		assert.NotEqual(t, "isAnyoneHome", v)
		assert.NotEqual(t, "isTVPlaying", v)
		assert.NotEqual(t, "isEveryoneAsleep", v)
		assert.NotEqual(t, "isMasterAsleep", v)
		assert.NotEqual(t, "isHaveGuests", v)
	}

	// Should include isNickHome (from Primary Suite config) which is not in the standard list
	found := false
	for _, v := range vars {
		if v == "isNickHome" {
			found = true
			break
		}
	}
	assert.True(t, found, "Should include isNickHome from Primary Suite config")
}

func TestHandleSunEventChange(t *testing.T) {
	tests := []struct {
		name             string
		newVal           interface{}
		expectSceneCalls bool
	}{
		{"Valid sun event triggers scenes", "sunset", true},
		{"Invalid type is handled gracefully", 123, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testlogger.New()
			config := createTestConfig()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

			_ = stateManager.SetString("dayPhase", "Morning")
			_ = stateManager.SetString("sunevent", "sunrise")
			_ = stateManager.SetBool("isAnyoneHome", true)
			_ = stateManager.SetBool("isEveryoneAsleep", false)
			_ = stateManager.SetBool("isTVPlaying", false)

			manager.handleSunEventChange("sunevent", "sunrise", tt.newVal)

			calls := mockClient.GetServiceCalls()
			sceneCalls := 0
			for _, call := range calls {
				if call.Domain == "scene" {
					sceneCalls++
				}
			}
			if tt.expectSceneCalls {
				assert.Greater(t, sceneCalls, 0, "Expected scene calls after valid sun event change")
			} else {
				assert.Equal(t, 0, sceneCalls, "Expected no scene calls with invalid value")
			}
		})
	}
}

func TestHandleTVStateChange(t *testing.T) {
	tests := []struct {
		name               string
		tvPlaying          bool
		oldVal             interface{}
		newVal             interface{}
		expectNoLivingRoom bool // when TV on, living room should be skipped
		expectSceneCall    bool // when TV off, scenes should activate
	}{
		{
			name:               "TV starts playing skips living room",
			tvPlaying:          true,
			oldVal:             false,
			newVal:             true,
			expectNoLivingRoom: true,
		},
		{
			name:            "TV turns off activates scenes",
			tvPlaying:       false,
			oldVal:          true,
			newVal:          false,
			expectSceneCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testlogger.New()
			config := createTestConfig()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

			_ = stateManager.SetString("dayPhase", "Evening")
			_ = stateManager.SetBool("isAnyoneHome", true)
			_ = stateManager.SetBool("isEveryoneAsleep", false)
			_ = stateManager.SetBool("isTVPlaying", tt.tvPlaying)
			mockClient.ClearServiceCalls()

			manager.handleTVStateChange("isTVPlaying", tt.oldVal, tt.newVal)

			calls := mockClient.GetServiceCalls()
			if tt.expectNoLivingRoom {
				for _, call := range calls {
					if call.Domain == "scene" {
						if entityID, ok := call.Data["entity_id"].(string); ok {
							assert.NotContains(t, entityID, "living_room",
								"Living room scene should be skipped when TV is playing")
						}
					}
				}
			}
			if tt.expectSceneCall {
				sceneCalls := 0
				for _, call := range calls {
					if call.Domain == "scene" {
						sceneCalls++
					}
				}
				assert.Greater(t, sceneCalls, 0, "Expected scene activation when TV turns off")
			}
		})
	}
}

func TestHandleSleepStateChange(t *testing.T) {
	tests := []struct {
		name          string
		triggerVar    string
		dayPhase      string
		sleepState    map[string]bool
		oldVal        interface{}
		newVal        interface{}
		expectDomain  string
		expectService string
		expectAreaID  string // empty means don't check area_id
	}{
		{
			name:       "Everyone asleep turns off lights",
			triggerVar: "isEveryoneAsleep",
			dayPhase:   "Night",
			sleepState: map[string]bool{
				"isAnyoneHome": true, "isEveryoneAsleep": true,
				"isMasterAsleep": false, "isTVPlaying": false, "isNickHome": true,
			},
			oldVal: false, newVal: true,
			expectDomain: "light", expectService: "turn_off",
		},
		{
			name:       "Master asleep turns off primary suite",
			triggerVar: "isMasterAsleep",
			dayPhase:   "Night",
			sleepState: map[string]bool{
				"isAnyoneHome": true, "isEveryoneAsleep": false,
				"isMasterAsleep": true, "isTVPlaying": false, "isNickHome": true,
			},
			oldVal: false, newVal: true,
			expectDomain: "light", expectService: "turn_off", expectAreaID: "master_bedroom",
		},
		{
			name:       "Master wakes up activates scene",
			triggerVar: "isMasterAsleep",
			dayPhase:   "Morning",
			sleepState: map[string]bool{
				"isAnyoneHome": true, "isEveryoneAsleep": false,
				"isMasterAsleep": false, "isTVPlaying": false, "isNickHome": true,
			},
			oldVal: true, newVal: false,
			expectDomain: "scene", expectService: "turn_on",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testlogger.New()
			config := createTestConfig()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)

			_ = stateManager.SetString("dayPhase", tt.dayPhase)
			for k, v := range tt.sleepState {
				_ = stateManager.SetBool(k, v)
			}
			mockClient.ClearServiceCalls()

			manager.handleSleepStateChange(tt.triggerVar, tt.oldVal, tt.newVal)

			calls := mockClient.GetServiceCalls()
			found := false
			for _, call := range calls {
				if call.Domain == tt.expectDomain && call.Service == tt.expectService {
					if tt.expectAreaID != "" {
						if areaID, ok := call.Data["area_id"].(string); ok && areaID == tt.expectAreaID {
							found = true
						}
					} else {
						found = true
					}
				}
			}
			assert.True(t, found, "Expected %s.%s call", tt.expectDomain, tt.expectService)
		})
	}
}

// TestToSnakeCase tests the toSnakeCase helper function
func TestToSnakeCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"Primary Suite", "primary_suite"},
		{"Living Room", "living_room"},
		{"Morning", "morning"},
		{"Primary Suite evening", "primary_suite_evening"},
		{"UPPER CASE", "upper_case"},
		{"multiple   spaces", "multiple_spaces"},
		{"_leading_underscore", "leading_underscore"},
		{"trailing_underscore_", "trailing_underscore"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := toSnakeCase(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
