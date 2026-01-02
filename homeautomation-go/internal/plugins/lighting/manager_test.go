package lighting

import (
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
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

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name           string
		setupState     func()
		roomIndex      int
		expectedAction string
		expectedVar    string
	}{
		{
			name: "Living room - no one home -> OFF (first match)",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", false)
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:      0,
			expectedAction: "off",
			expectedVar:    "isAnyoneHome",
		},
		{
			name: "Living room - everyone asleep -> OFF (second priority)",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", true)
				_ = stateManager.SetBool("isEveryoneAsleep", true)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:      0,
			expectedAction: "off",
			expectedVar:    "isEveryoneAsleep",
		},
		{
			name: "Living room - someone home and awake -> ON",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", true)
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:      0,
			expectedAction: "on",
			expectedVar:    "isAnyoneHome",
		},
		{
			name: "Living room - TV not playing -> ON (last priority)",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", true)
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isTVPlaying", false)
			},
			roomIndex:      0,
			expectedAction: "on",
			expectedVar:    "isAnyoneHome", // First matching condition wins
		},
		{
			name: "Primary suite - nick not home -> OFF",
			setupState: func() {
				_ = stateManager.SetBool("isNickHome", false)
				_ = stateManager.SetBool("isMasterAsleep", false)
			},
			roomIndex:      1,
			expectedAction: "off",
			expectedVar:    "isNickHome",
		},
		{
			name: "Primary suite - master asleep -> OFF",
			setupState: func() {
				_ = stateManager.SetBool("isNickHome", true)
				_ = stateManager.SetBool("isMasterAsleep", true)
			},
			roomIndex:      1,
			expectedAction: "off",
			expectedVar:    "isMasterAsleep",
		},
		{
			name: "Primary suite - master not asleep -> ON",
			setupState: func() {
				_ = stateManager.SetBool("isNickHome", true)
				_ = stateManager.SetBool("isMasterAsleep", false)
			},
			roomIndex:      1,
			expectedAction: "on",
			expectedVar:    "isMasterAsleep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			tt.setupState()
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

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	room := &config.Rooms[0]
	action, matchedVar := manager.evaluateConditions(room)
	assert.Equal(t, "", action, "No action expected when no conditions")
	assert.Equal(t, "", matchedVar, "No matched variable when no conditions")
}

func TestActivateSceneReadOnly(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, true, nil) // Read-only mode

	room := &config.Rooms[0]
	dayPhase := "Morning"

	// Should not call service in read-only mode
	manager.activateScene(room, dayPhase, "test_trigger")

	// Verify no service calls were made
	calls := mockClient.GetServiceCalls()
	assert.Equal(t, 0, len(calls))
}

func TestActivateScene(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil) // Not read-only

	room := &config.Rooms[0]
	dayPhase := "Morning"

	manager.activateScene(room, dayPhase, "test_trigger")

	// Verify service call was made
	calls := mockClient.GetServiceCalls()
	assert.Equal(t, 1, len(calls))

	call := calls[0]
	assert.Equal(t, "scene", call.Domain)
	assert.Equal(t, "turn_on", call.Service)
	assert.Equal(t, "scene.living_room_morning", call.Data["entity_id"])
	// Note: area_id is intentionally NOT passed for scene.turn_on to match Node-RED behavior
	assert.Nil(t, call.Data["area_id"], "area_id should not be passed for scene activation")
	assert.Equal(t, 30, call.Data["transition"])
}

// TestActivateSceneDusk is a regression test for the bug fixed in PR #169.
// When area_id was incorrectly passed alongside entity_id to scene.turn_on,
// Home Assistant would activate an unexpected scene (e.g., "energize" instead of "dusk").
// This test verifies that only entity_id is passed, ensuring the correct scene activates.
func TestActivateSceneDusk(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	room := &config.Rooms[0] // Living Room
	dayPhase := "Dusk"

	manager.activateScene(room, dayPhase, "reset_trigger")

	// Verify correct scene is activated
	calls := mockClient.GetServiceCalls()
	assert.Equal(t, 1, len(calls))

	call := calls[0]
	assert.Equal(t, "scene", call.Domain)
	assert.Equal(t, "turn_on", call.Service)
	// Critical: verify "dusk" scene is activated, not "energize" or other scene
	assert.Equal(t, "scene.living_room_dusk", call.Data["entity_id"],
		"Expected dusk scene, not energize or other unexpected scene")
	// Critical: area_id must NOT be passed - this was the root cause of the bug
	assert.Nil(t, call.Data["area_id"],
		"area_id must not be passed; it causes Home Assistant to activate wrong scenes")
	assert.Equal(t, 30, call.Data["transition"])
}

func TestTurnOffRoomReadOnly(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, true, nil) // Read-only mode

	room := &config.Rooms[0]

	// Should not call service in read-only mode
	manager.turnOffRoom(room, "test_trigger")

	// Verify no service calls were made
	calls := mockClient.GetServiceCalls()
	assert.Equal(t, 0, len(calls))
}

func TestTurnOffRoom(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil) // Not read-only

	room := &config.Rooms[0]

	manager.turnOffRoom(room, "test_trigger")

	// Verify service call was made
	calls := mockClient.GetServiceCalls()
	assert.Equal(t, 1, len(calls))

	call := calls[0]
	assert.Equal(t, "light", call.Domain)
	assert.Equal(t, "turn_off", call.Service)
	assert.Equal(t, "living_room_2", call.Data["area_id"])
	// Note: turn_off intentionally does NOT include transition_seconds
	// so lights turn off immediately (especially important for sleep scenarios)
	assert.Nil(t, call.Data["transition"], "turn_off should not include transition")
}

func TestEvaluateAndActivateRoom(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name              string
		setupState        func()
		roomIndex         int
		dayPhase          string
		expectedService   string
		expectedDomain    string
		shouldCallService bool
	}{
		{
			name: "Room should turn off - no one home",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", false)
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:         0,
			dayPhase:          "Night",
			expectedService:   "turn_off",
			expectedDomain:    "light",
			shouldCallService: true,
		},
		{
			name: "Room should turn off - everyone asleep",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", true)
				_ = stateManager.SetBool("isEveryoneAsleep", true)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:         0,
			dayPhase:          "Night",
			expectedService:   "turn_off",
			expectedDomain:    "light",
			shouldCallService: true,
		},
		{
			name: "Room should turn on with scene",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", true)
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:         0,
			dayPhase:          "Morning",
			expectedService:   "turn_on",
			expectedDomain:    "scene",
			shouldCallService: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Reset mock client

			mockClient.ClearServiceCalls()

			tt.setupState()
			room := &config.Rooms[tt.roomIndex]
			manager.evaluateAndActivateRoom(room, tt.dayPhase, "")

			calls := mockClient.GetServiceCalls()
			if tt.shouldCallService {
				assert.GreaterOrEqual(t, len(calls), 1, "Expected at least one service call")
				// Find the expected call
				found := false
				for _, call := range calls {
					if call.Domain == tt.expectedDomain && call.Service == tt.expectedService {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected to find %s.%s call", tt.expectedDomain, tt.expectedService)
			} else {
				assert.Equal(t, 0, len(calls))
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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

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
	logger := zap.NewNop()
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

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

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

	manager := NewManager(mockClient, stateManager, hueConfig, logger, false, nil)

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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name     string
		room     *RoomConfig
		trigger  string
		expected bool
	}{
		{
			name:     "dayPhase is always relevant",
			room:     &config.Rooms[0],
			trigger:  "dayPhase",
			expected: true,
		},
		{
			name:     "sunevent is always relevant",
			room:     &config.Rooms[0],
			trigger:  "sunevent",
			expected: true,
		},
		{
			name:     "reset is always relevant",
			room:     &config.Rooms[0],
			trigger:  "reset",
			expected: true,
		},
		{
			name:     "empty trigger is always relevant",
			room:     &config.Rooms[0],
			trigger:  "",
			expected: true,
		},
		{
			name:     "isAnyoneHome is relevant to Living Room",
			room:     &config.Rooms[0],
			trigger:  "isAnyoneHome",
			expected: true,
		},
		{
			name:     "isMasterAsleep is not relevant to Living Room",
			room:     &config.Rooms[0],
			trigger:  "isMasterAsleep",
			expected: false,
		},
		{
			name:     "isMasterAsleep is relevant to Primary Suite",
			room:     &config.Rooms[1],
			trigger:  "isMasterAsleep",
			expected: true,
		},
		{
			name:     "unrelated variable is not relevant",
			room:     &config.Rooms[0],
			trigger:  "isKitchenOccupied",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			result := manager.isTopicRelevant(tt.room, tt.trigger)
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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use real registered state variables
	// Boolean: isAnyoneHome (registered in state manager)
	err := stateManager.SetBool("isAnyoneHome", true)
	assert.NoError(t, err)

	// String: dayPhase (registered in state manager)
	err = stateManager.SetString("dayPhase", "morning")
	assert.NoError(t, err)

	// Test boolean retrieval
	val, err := manager.getStateValue("isAnyoneHome")
	assert.NoError(t, err)
	assert.Equal(t, true, val)

	// Test string retrieval
	val, err = manager.getStateValue("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "morning", val)

	// Test nonexistent variable
	_, err = manager.getStateValue("nonexistent")
	assert.Error(t, err)
}

func TestCollectConditionVariables(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

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
