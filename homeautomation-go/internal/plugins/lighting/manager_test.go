package lighting

import (
	"context"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/testutil"

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
	env := testutil.NewEnv(t)
	config := createTestConfig()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	assert.NotNil(t, manager)
	assert.Equal(t, env.MockHA, manager.haClient)
	assert.Equal(t, env.StateMgr, manager.stateManager)
	assert.Equal(t, config, manager.config)
	assert.False(t, manager.readOnly)
}

func TestEvaluateConditions(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	tests := []struct {
		name           string
		setupState     func()
		roomIndex      int
		expectedAction string
		expectedVar    string
	}{
		{
			name: "Living room - TV playing -> SKIP (highest priority)",
			setupState: func() {
				_ = env.StateMgr.SetBool("isAnyoneHome", true)
				_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
				_ = env.StateMgr.SetBool("isTVPlaying", true)
			},
			roomIndex:      0,
			expectedAction: "skip",
			expectedVar:    "isTVPlaying",
		},
		{
			name: "Living room - no one home -> OFF (first match)",
			setupState: func() {
				_ = env.StateMgr.SetBool("isAnyoneHome", false)
				_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
				_ = env.StateMgr.SetBool("isTVPlaying", false)
			},
			roomIndex:      0,
			expectedAction: "off",
			expectedVar:    "isAnyoneHome",
		},
		{
			name: "Living room - everyone asleep -> OFF (second priority)",
			setupState: func() {
				_ = env.StateMgr.SetBool("isAnyoneHome", true)
				_ = env.StateMgr.SetBool("isEveryoneAsleep", true)
				_ = env.StateMgr.SetBool("isTVPlaying", false)
			},
			roomIndex:      0,
			expectedAction: "off",
			expectedVar:    "isEveryoneAsleep",
		},
		{
			name: "Living room - someone home and awake -> ON",
			setupState: func() {
				_ = env.StateMgr.SetBool("isAnyoneHome", true)
				_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
				_ = env.StateMgr.SetBool("isTVPlaying", false)
			},
			roomIndex:      0,
			expectedAction: "on",
			expectedVar:    "isAnyoneHome",
		},
		{
			name: "Living room - TV not playing -> ON (last priority)",
			setupState: func() {
				_ = env.StateMgr.SetBool("isAnyoneHome", true)
				_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
				_ = env.StateMgr.SetBool("isTVPlaying", false)
			},
			roomIndex:      0,
			expectedAction: "on",
			expectedVar:    "isAnyoneHome", // First matching condition wins
		},
		{
			name: "Primary suite - nick not home -> OFF",
			setupState: func() {
				_ = env.StateMgr.SetBool("isNickHome", false)
				_ = env.StateMgr.SetBool("isMasterAsleep", false)
			},
			roomIndex:      1,
			expectedAction: "off",
			expectedVar:    "isNickHome",
		},
		{
			name: "Primary suite - master asleep -> OFF",
			setupState: func() {
				_ = env.StateMgr.SetBool("isNickHome", true)
				_ = env.StateMgr.SetBool("isMasterAsleep", true)
			},
			roomIndex:      1,
			expectedAction: "off",
			expectedVar:    "isMasterAsleep",
		},
		{
			name: "Primary suite - master not asleep -> ON",
			setupState: func() {
				_ = env.StateMgr.SetBool("isNickHome", true)
				_ = env.StateMgr.SetBool("isMasterAsleep", false)
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
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	room := &config.Rooms[0]
	action, matchedVar := manager.evaluateConditions(room)
	assert.Equal(t, "", action, "No action expected when no conditions")
	assert.Equal(t, "", matchedVar, "No matched variable when no conditions")
}

func TestActivateSceneReadOnly(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil) // Read-only mode

	room := &config.Rooms[0]
	dayPhase := "Morning"

	// Should not call service in read-only mode
	manager.activateScene(room, dayPhase, "test_trigger")

	// Verify no service calls were made
	calls := env.MockHA.GetServiceCalls()
	assert.Equal(t, 0, len(calls))
}

func TestActivateScene(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil) // Not read-only

	room := &config.Rooms[0]
	dayPhase := "Morning"

	manager.activateScene(room, dayPhase, "test_trigger")

	// Verify service call was made
	calls := env.MockHA.GetServiceCalls()
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
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	room := &config.Rooms[0] // Living Room
	dayPhase := "Dusk"

	manager.activateScene(room, dayPhase, "reset_trigger")

	// Verify correct scene is activated
	calls := env.MockHA.GetServiceCalls()
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
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil) // Read-only mode

	room := &config.Rooms[0]

	// Should not call service in read-only mode
	manager.turnOffRoom(room, "test_trigger")

	// Verify no service calls were made
	calls := env.MockHA.GetServiceCalls()
	assert.Equal(t, 0, len(calls))
}

func TestTurnOffRoom(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil) // Not read-only

	room := &config.Rooms[0]

	manager.turnOffRoom(room, "test_trigger")

	// Verify service call was made
	calls := env.MockHA.GetServiceCalls()
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
	config := createTestConfig()

	tests := []struct {
		name              string
		setupState        func(*state.Manager)
		roomIndex         int
		dayPhase          string
		expectedService   string
		expectedDomain    string
		shouldCallService bool
	}{
		{
			name: "Room should turn off - no one home",
			setupState: func(sm *state.Manager) {
				_ = sm.SetBool("isAnyoneHome", false)
				_ = sm.SetBool("isEveryoneAsleep", false)
				_ = sm.SetBool("isTVPlaying", false)
			},
			roomIndex:         0,
			dayPhase:          "Night",
			expectedService:   "turn_off",
			expectedDomain:    "light",
			shouldCallService: true,
		},
		{
			name: "Room should turn off - everyone asleep",
			setupState: func(sm *state.Manager) {
				_ = sm.SetBool("isAnyoneHome", true)
				_ = sm.SetBool("isEveryoneAsleep", true)
				_ = sm.SetBool("isTVPlaying", false)
			},
			roomIndex:         0,
			dayPhase:          "Night",
			expectedService:   "turn_off",
			expectedDomain:    "light",
			shouldCallService: true,
		},
		{
			name: "Room should turn on with scene",
			setupState: func(sm *state.Manager) {
				_ = sm.SetBool("isAnyoneHome", true)
				_ = sm.SetBool("isEveryoneAsleep", false)
				_ = sm.SetBool("isTVPlaying", false)
			},
			roomIndex:         0,
			dayPhase:          "Morning",
			expectedService:   "turn_on",
			expectedDomain:    "scene",
			shouldCallService: true,
		},
		{
			name: "Room should skip - TV playing",
			setupState: func(sm *state.Manager) {
				_ = sm.SetBool("isAnyoneHome", true)
				_ = sm.SetBool("isEveryoneAsleep", false)
				_ = sm.SetBool("isTVPlaying", true)
			},
			roomIndex:         0,
			dayPhase:          "Morning",
			expectedService:   "",
			expectedDomain:    "",
			shouldCallService: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Create fresh mocks for each test case
			env := testutil.NewEnv(t)
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

			tt.setupState(env.StateMgr)
			room := &config.Rooms[tt.roomIndex]
			manager.evaluateAndActivateRoom(room, tt.dayPhase, "")

			calls := env.MockHA.GetServiceCalls()

			// Filter out state manager calls (input_boolean.* calls from SetBool)
			// We only care about lighting-related calls (scene.*, light.*)
			lightingCalls := []ha.ServiceCall{}
			for _, call := range calls {
				if call.Domain == "scene" || call.Domain == "light" {
					lightingCalls = append(lightingCalls, call)
				}
			}

			if tt.shouldCallService {
				assert.GreaterOrEqual(t, len(lightingCalls), 1, "Expected at least one lighting service call")
				call := testutil.FindServiceCall(lightingCalls, tt.expectedDomain, tt.expectedService)
				assert.NotNil(t, call, "Expected to find %s.%s call", tt.expectedDomain, tt.expectedService)
			} else {
				assert.Equal(t, 0, len(lightingCalls), "Expected no lighting service calls")
			}
		})
	}
}

func TestStart(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Start manager
	err := manager.Start()
	assert.NoError(t, err)

	// Set initial state
	err = env.StateMgr.SetString("dayPhase", "Morning")
	assert.NoError(t, err)

	err = env.StateMgr.SetBool("isAnyoneHome", true)
	assert.NoError(t, err)

	// Change day phase - this should trigger scene activation
	err = env.StateMgr.SetString("dayPhase", "Day")
	assert.NoError(t, err)

	// Verify that scenes were activated (service calls were made)
	calls := env.MockHA.GetServiceCalls()
	assert.Greater(t, len(calls), 0, "Expected service calls after day phase change")
}

// TestLightingManager_Stop tests the Stop method and subscription cleanup
func TestLightingManager_Stop(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Initialize required state variables
	_ = env.StateMgr.SetString("dayPhase", "morning")
	_ = env.StateMgr.SetString("sunevent", "sunrise")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetBool("isHaveGuests", false)

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
	env := testutil.NewEnv(t)
	hueConfig := createTestConfig()

	// Set day phase
	env.StateMgr.SetString("dayPhase", "morning")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, hueConfig, env.Logger, false, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-apply lighting scenes for all rooms
	err = manager.Reset()
	assert.NoError(t, err)
}

func TestIsTopicRelevant(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

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
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Use real registered state variables
	err := env.StateMgr.SetBool("isAnyoneHome", true)
	assert.NoError(t, err)

	err = env.StateMgr.SetString("dayPhase", "morning")
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
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	vars := manager.collectConditionVariables()

	// Should not include variables that are already subscribed to explicitly
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

// TestHandleSunEventChange tests that sun event changes trigger scene activation
func TestHandleSunEventChange(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state
	_ = env.StateMgr.SetString("dayPhase", "Morning")
	_ = env.StateMgr.SetString("sunevent", "sunrise")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isTVPlaying", false)

	// Manually call the handler (simulating a sun event change)
	manager.handleSunEventChange("sunevent", "sunrise", "sunset")

	// Verify that scenes were activated (service calls were made)
	calls := env.MockHA.GetServiceCalls()

	// Filter for scene activations only
	assert.Greater(t, testutil.CountServiceCalls(calls, "scene", "turn_on"), 0,
		"Expected scene service calls after sun event change")
}

// TestHandleSunEventChangeWithInvalidValue tests that invalid sun event values are handled gracefully
func TestHandleSunEventChangeWithInvalidValue(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state
	_ = env.StateMgr.SetString("dayPhase", "Morning")

	// Call with non-string value - should not panic
	manager.handleSunEventChange("sunevent", "sunrise", 123) // Invalid type

	// Should not have made any service calls due to invalid value
	calls := env.MockHA.GetServiceCalls()
	assert.Equal(t, 0, testutil.CountServiceCalls(calls, "scene", "turn_on"),
		"Expected no scene calls with invalid sun event value")
}

// TestHandleTVStateChange tests that TV state changes trigger room re-evaluation
func TestHandleTVStateChange(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state
	_ = env.StateMgr.SetString("dayPhase", "Evening")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isTVPlaying", false)

	// Clear any calls from setup
	env.MockHA.ClearServiceCalls()

	// TV starts playing - rooms with isTVPlaying condition should skip
	_ = env.StateMgr.SetBool("isTVPlaying", true)
	manager.handleTVStateChange("isTVPlaying", false, true)

	// The Living Room config has a "skip" action for isTVPlaying=true
	// So the room should be skipped and no scene calls made for Living Room
	calls := env.MockHA.GetServiceCalls()

	// Verify the handler was processed (by checking logs or service calls)
	// Since Living Room has skip action when TV is playing, only Primary Suite might get scene
	for _, call := range calls {
		if call.Domain == "scene" {
			// Any scene calls should NOT be for Living Room (since it skips when TV playing)
			if entityID, ok := call.Data["entity_id"].(string); ok {
				assert.NotContains(t, entityID, "living_room",
					"Living room scene should be skipped when TV is playing")
			}
		}
	}
}

// TestHandleTVStateChangeTurnsOffTV tests that turning off TV triggers lighting
func TestHandleTVStateChangeTurnsOffTV(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state - TV was playing
	_ = env.StateMgr.SetString("dayPhase", "Evening")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isTVPlaying", false) // TV just turned off

	// Clear any calls from setup
	env.MockHA.ClearServiceCalls()

	// Simulate TV turning off
	manager.handleTVStateChange("isTVPlaying", true, false)

	// Now Living Room should get its scene activated
	calls := env.MockHA.GetServiceCalls()

	// Should have at least one scene call now that TV is off
	assert.Greater(t, testutil.CountServiceCalls(calls, "scene", "turn_on"), 0,
		"Expected scene activation when TV turns off")
}

// TestHandleSleepStateChange tests that sleep state changes trigger room re-evaluation
func TestHandleSleepStateChange(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state - awake
	_ = env.StateMgr.SetString("dayPhase", "Night")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	_ = env.StateMgr.SetBool("isNickHome", true)

	// Clear any calls from setup
	env.MockHA.ClearServiceCalls()

	// Everyone goes to sleep
	_ = env.StateMgr.SetBool("isEveryoneAsleep", true)
	manager.handleSleepStateChange("isEveryoneAsleep", false, true)

	// Rooms with isEveryoneAsleep condition should turn off
	calls := env.MockHA.GetServiceCalls()

	// Living Room has isEveryoneAsleep condition -> should turn off
	assert.Greater(t, testutil.CountServiceCalls(calls, "light", "turn_off"), 0,
		"Expected lights to turn off when everyone is asleep")
}

// TestHandleSleepStateChangeMasterAsleep tests master bedroom specific sleep handling
func TestHandleSleepStateChangeMasterAsleep(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state - master bedroom awake
	_ = env.StateMgr.SetString("dayPhase", "Night")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	_ = env.StateMgr.SetBool("isNickHome", true)

	// Clear any calls from setup
	env.MockHA.ClearServiceCalls()

	// Master goes to sleep - Primary Suite should turn off
	_ = env.StateMgr.SetBool("isMasterAsleep", true)
	manager.handleSleepStateChange("isMasterAsleep", false, true)

	// Primary Suite has isMasterAsleep condition -> should turn off
	calls := env.MockHA.GetServiceCalls()

	// Filter for light.turn_off calls targeting master_bedroom area
	turnOffCalls := []ha.ServiceCall{}
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_off" {
			if areaID, ok := call.Data["area_id"].(string); ok {
				if areaID == "master_bedroom" {
					turnOffCalls = append(turnOffCalls, call)
				}
			}
		}
	}

	assert.Greater(t, len(turnOffCalls), 0, "Expected Primary Suite to turn off when master is asleep")
}

// TestHandleSleepStateChangeWakesUp tests that waking up triggers scene activation
func TestHandleSleepStateChangeWakesUp(t *testing.T) {
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state - master was asleep
	_ = env.StateMgr.SetString("dayPhase", "Morning")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false) // Just woke up
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	_ = env.StateMgr.SetBool("isNickHome", true)

	// Clear any calls from setup
	env.MockHA.ClearServiceCalls()

	// Master wakes up - Primary Suite should turn on
	manager.handleSleepStateChange("isMasterAsleep", true, false)

	// Primary Suite should get a scene activated (isMasterAsleep=false -> ON)
	calls := env.MockHA.GetServiceCalls()

	// Should have at least one scene call
	assert.Greater(t, testutil.CountServiceCalls(calls, "scene", "turn_on"), 0,
		"Expected scene activation when master wakes up")
}

// TestHandleOccupancyChange tests room-specific occupancy variable changes
func TestHandleOccupancyChange(t *testing.T) {
	env := testutil.NewEnv(t)

	// Create config with occupancy-based conditions using a registered variable
	transition30 := 30
	config := &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Nick Office",
				HASSAreaID: "nick_office",
				Conditions: []LightingCondition{
					{Action: "off", Variable: "isNickOfficeOccupied", Value: false},
					{Action: "on", Variable: "isNickOfficeOccupied", Value: true},
				},
				TransitionSeconds: &transition30,
			},
		},
	}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Set up initial state - use registered variable isNickOfficeOccupied
	_ = env.StateMgr.SetString("dayPhase", "Day")
	_ = env.StateMgr.SetBool("isNickOfficeOccupied", false)

	// Clear any calls from setup
	env.MockHA.ClearServiceCalls()

	// Someone enters the office
	_ = env.StateMgr.SetBool("isNickOfficeOccupied", true)
	manager.handleOccupancyChange("isNickOfficeOccupied", false, true)

	// Office should have a scene activated
	calls := env.MockHA.GetServiceCalls()
	assert.Greater(t, testutil.CountServiceCalls(calls, "scene", "turn_on"), 0,
		"Expected scene activation when office becomes occupied")
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
