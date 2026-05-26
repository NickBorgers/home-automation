package lighting

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
)

// createTestConfig creates a test hue configuration using the new conditions format
func createTestConfig() *HueConfig {
	transition3 := 3
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
			{
				HueGroup:   "Kitchen",
				HASSAreaID: "kitchen",
				Conditions: []LightingCondition{
					{Action: "off", Variable: "isAnyoneHome", Value: false},
					{Action: "on", Variable: "isAnyoneHome", Value: true},
				},
				IncreaseBrightnessIfTrue: nil,
				TransitionSeconds:        &transition3,
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
				_ = env.StateMgr.SetBool(k, v)
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

func TestActivateScene(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		readOnly      bool
		dayPhase      string
		trigger       string
		roomIndex     int // index into createTestConfig().Rooms
		expectedCalls int
		expectedScene string
		expectedTrans int
	}{
		{
			name:          "Read-only mode makes no service calls",
			readOnly:      true,
			dayPhase:      "Morning",
			trigger:       "test_trigger",
			expectedCalls: 0,
		},
		{
			name:          "Morning scene activates correctly",
			readOnly:      false,
			dayPhase:      "Morning",
			trigger:       "test_trigger",
			expectedCalls: 1,
			expectedScene: "scene.living_room_morning",
			expectedTrans: 30,
		},
		{
			// Regression test for PR #169: area_id must NOT be passed
			name:          "Dusk scene activates correctly (not energize)",
			readOnly:      false,
			dayPhase:      "Dusk",
			trigger:       "test_trigger",
			expectedCalls: 1,
			expectedScene: "scene.living_room_dusk",
			expectedTrans: 30,
		},
		{
			// Issue #913: presence trigger with moderate transition gets capped
			name:          "Presence trigger caps moderate transition",
			readOnly:      false,
			dayPhase:      "Morning",
			trigger:       "isAnyoneHome",
			roomIndex:     0, // Living Room: 30s transition
			expectedCalls: 1,
			expectedScene: "scene.living_room_morning",
			expectedTrans: 5, // capped to maxReturnHomeTransition
		},
		{
			// Issue #913: presence trigger with long transition gets capped
			name:          "Presence trigger caps long transition to avoid Hue bridge failure",
			readOnly:      false,
			dayPhase:      "Morning",
			trigger:       "isAnyoneHomeAndAwake",
			roomIndex:     1, // Primary Suite: 180s transition
			expectedCalls: 1,
			expectedScene: "scene.primary_suite_morning",
			expectedTrans: 5, // capped to maxReturnHomeTransition
		},
		{
			// Issue #913: non-presence trigger preserves long transition
			name:          "Day phase trigger preserves long transition",
			readOnly:      false,
			dayPhase:      "Morning",
			trigger:       "dayPhase",
			roomIndex:     1, // Primary Suite: 180s transition
			expectedCalls: 1,
			expectedScene: "scene.primary_suite_morning",
			expectedTrans: 180, // not capped
		},
		{
			// Issue #913: presence trigger with transition <= cap keeps original
			name:          "Presence trigger with short transition keeps original",
			readOnly:      false,
			dayPhase:      "Morning",
			trigger:       "isHaveGuests",
			roomIndex:     2, // Kitchen: 3s transition (below 5s cap)
			expectedCalls: 1,
			expectedScene: "scene.kitchen_morning",
			expectedTrans: 3, // not capped — already below maxReturnHomeTransition
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			config := createTestConfig()
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, tt.readOnly, nil)

			room := &config.Rooms[tt.roomIndex]
			manager.activateScene(context.Background(), room, tt.dayPhase, tt.trigger)

			calls := env.MockHA.GetServiceCalls()
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

func TestIsPresenceTrigger(t *testing.T) {
	t.Parallel()
	assert.True(t, isPresenceTrigger("isAnyoneHome"))
	assert.True(t, isPresenceTrigger("isAnyoneHomeAndAwake"))
	assert.True(t, isPresenceTrigger("isNickHome"))
	assert.True(t, isPresenceTrigger("isCarolineHome"))
	assert.True(t, isPresenceTrigger("isHaveGuests"))
	assert.False(t, isPresenceTrigger("dayPhase"))
	assert.False(t, isPresenceTrigger("sunevent"))
	assert.False(t, isPresenceTrigger("isTVPlaying"))
	assert.False(t, isPresenceTrigger("reset"))
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
			env := testutil.NewEnv(t)
			config := createTestConfig()
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, tt.readOnly, nil)

			room := &config.Rooms[0]
			manager.turnOffRoom(context.Background(), room, "test_trigger")

			calls := env.MockHA.GetServiceCalls()
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
	t.Parallel()
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
			env := testutil.NewEnv(t)
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

			_ = env.StateMgr.SetBool("isAnyoneHome", tt.isAnyoneHome)
			_ = env.StateMgr.SetBool("isEveryoneAsleep", tt.isEveryoneAsleep)
			_ = env.StateMgr.SetBool("isTVPlaying", tt.isTVPlaying)

			room := &config.Rooms[0]
			manager.evaluateAndActivateRoom(room, tt.dayPhase, "")

			calls := env.MockHA.GetServiceCalls()
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

func TestEvaluateAndActivateRoomSkipClearsLastRoomActionForSkipReactivationRoom(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Kitchen",
				HASSAreaID: "kitchen",
				Conditions: []LightingCondition{
					{Action: "skip", Variable: "isTVPlaying", Value: true},
					{Action: "on", Variable: "isKitchenOccupied", Value: true},
				},
				SkipReactivationWhenOn: true,
			},
		},
	}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)
	room := &config.Rooms[0]

	assert.NoError(t, env.StateMgr.SetBool("isTVPlaying", true))
	assert.NoError(t, env.StateMgr.SetBool("isKitchenOccupied", true))
	manager.setLastRoomAction(room.HueGroup, "on")

	snapshot := env.MockHA.ServiceCallCount()
	manager.evaluateAndActivateRoom(room, "Day", "isTVPlaying")
	assert.Equal(t, 0, len(env.MockHA.GetServiceCallsSince(snapshot)), "skip should not call Home Assistant")

	assert.NoError(t, env.StateMgr.SetBool("isTVPlaying", false))
	snapshot = env.MockHA.ServiceCallCount()
	manager.evaluateAndActivateRoom(room, "Day", "isKitchenOccupied")

	calls := env.MockHA.GetServiceCallsSince(snapshot)
	if !assert.Len(t, calls, 1) {
		return
	}
	assert.Equal(t, "scene", calls[0].Domain)
	assert.Equal(t, "turn_on", calls[0].Service)
	assert.Equal(t, "scene.kitchen_day", calls[0].Data["entity_id"])
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
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetString("dayPhase", "morning")

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

func TestHandleSunEventChange(t *testing.T) {
	t.Parallel()
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
			env := testutil.NewEnv(t)
			config := createTestConfig()
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

			_ = env.StateMgr.SetString("dayPhase", "Morning")
			_ = env.StateMgr.SetString("sunevent", "sunrise")
			_ = env.StateMgr.SetBool("isAnyoneHome", true)
			_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
			_ = env.StateMgr.SetBool("isTVPlaying", false)

			manager.handleSunEventChange("sunevent", "sunrise", tt.newVal)

			calls := env.MockHA.GetServiceCalls()
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
	t.Parallel()
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
			env := testutil.NewEnv(t)
			config := createTestConfig()
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

			_ = env.StateMgr.SetString("dayPhase", "Evening")
			_ = env.StateMgr.SetBool("isAnyoneHome", true)
			_ = env.StateMgr.SetBool("isEveryoneAsleep", false)
			_ = env.StateMgr.SetBool("isTVPlaying", tt.tvPlaying)
			snapshot := env.MockHA.ServiceCallCount()

			manager.handleTVStateChange("isTVPlaying", tt.oldVal, tt.newVal)

			calls := env.MockHA.GetServiceCallsSince(snapshot)
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
	t.Parallel()
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
			env := testutil.NewEnv(t)
			config := createTestConfig()
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

			_ = env.StateMgr.SetString("dayPhase", tt.dayPhase)
			for k, v := range tt.sleepState {
				_ = env.StateMgr.SetBool(k, v)
			}
			snapshot := env.MockHA.ServiceCallCount()

			manager.handleSleepStateChange(tt.triggerVar, tt.oldVal, tt.newVal)

			calls := env.MockHA.GetServiceCallsSince(snapshot)
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

func TestGetRoomContextCancelsPrevious(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Get first context for Living Room
	ctx1 := manager.getRoomContext("Living Room")
	assert.NoError(t, ctx1.Err(), "First context should be active")

	// Get second context for Living Room - should cancel the first
	ctx2 := manager.getRoomContext("Living Room")
	assert.Error(t, ctx1.Err(), "First context should be cancelled after second getRoomContext")
	assert.NoError(t, ctx2.Err(), "Second context should be active")

	// Get context for a different room - should not affect Living Room
	ctx3 := manager.getRoomContext("Primary Suite")
	assert.NoError(t, ctx2.Err(), "Living Room context should still be active")
	assert.NoError(t, ctx3.Err(), "Primary Suite context should be active")
}

func TestTurnOffRoomContextCancellation(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Call turnOffRoom with a pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	room := &config.Rooms[0]
	manager.turnOffRoom(ctx, room, "test_trigger")

	// No service calls should be made since context was already cancelled
	calls := env.MockHA.GetServiceCalls()
	assert.Equal(t, 0, len(calls), "No service calls expected with cancelled context")
}

func TestActivateSceneContextCancellation(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil)

	// Call activateScene with a pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	room := &config.Rooms[0]
	manager.activateScene(ctx, room, "Morning", "test_trigger")

	// No service calls should be made since context was already cancelled
	calls := env.MockHA.GetServiceCalls()
	assert.Equal(t, 0, len(calls), "No service calls expected with cancelled context")
}

// dynamicScenesConfig returns a config where the room at index 0 ("Primary
// Suite") has HasDynamics=true. Used by the two-step and debounce regression
// tests added for the 2026-05-25 arrival incident.
func dynamicScenesConfig() *HueConfig {
	transition := 180
	return &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Primary Suite",
				HASSAreaID: "master_bedroom",
				Conditions: []LightingCondition{
					{Action: "on", Variable: "isMasterAsleep", Value: false},
				},
				TransitionSeconds: &transition,
				HasDynamics:       true,
			},
			{
				HueGroup:   "Kitchen",
				HASSAreaID: "kitchen",
				Conditions: []LightingCondition{
					{Action: "on", Variable: "isAnyoneHome", Value: true},
				},
				TransitionSeconds: &transition,
				HasDynamics:       false,
			},
		},
	}
}

// TestEvaluateAndActivateRoom_DebounceCoalesces verifies that a burst of
// rapid trigger changes for the same room is coalesced into a single scene
// activation after the debounce delay. Regression test for the 2026-05-25
// arrival bug, where the lighting plugin fired three scene recalls in 474ms
// and destabilized the Hue Bridge.
func TestEvaluateAndActivateRoom_DebounceCoalesces(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	cfg := dynamicScenesConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)

	mockClock := clock.NewMockClock(time.Now())
	manager.SetClock(mockClock)
	manager.SetDebounceDelay(300 * time.Millisecond)

	// State that makes the Primary Suite condition resolve to "on".
	_ = env.StateMgr.SetBool("isMasterAsleep", false)

	// Use the Kitchen (HasDynamics=false) to keep the scene call count
	// straightforward — one call per fired evaluation.
	room := &cfg.Rooms[1]
	_ = env.StateMgr.SetBool("isAnyoneHome", true)

	// Three rapid evaluations, all within the debounce window.
	manager.evaluateAndActivateRoom(room, "Day", "trigger1")
	manager.evaluateAndActivateRoom(room, "Day", "trigger2")
	manager.evaluateAndActivateRoom(room, "Day", "trigger3")

	// Before the debounce fires, no scene service calls.
	calls := filterSceneCalls(env.MockHA.GetServiceCalls())
	assert.Equal(t, 0, len(calls), "no scene calls before debounce delay elapses")

	// Advance past the delay — debounce timer fires the coalesced evaluation.
	mockClock.AdvanceAndProcess(310 * time.Millisecond)

	calls = filterSceneCalls(env.MockHA.GetServiceCalls())
	assert.Equal(t, 1, len(calls), "three rapid evaluations should coalesce to a single scene activation")
}

// TestEvaluateAndActivateRoom_DebounceIsolatesRooms verifies that each room's
// debounce window is independent — coalescing within a room must not affect
// other rooms' evaluations.
func TestEvaluateAndActivateRoom_DebounceIsolatesRooms(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	cfg := dynamicScenesConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)

	mockClock := clock.NewMockClock(time.Now())
	manager.SetClock(mockClock)
	manager.SetDebounceDelay(300 * time.Millisecond)

	// Both rooms resolve to "on".
	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetBool("isAnyoneHome", true)

	primarySuite := &cfg.Rooms[0]
	kitchen := &cfg.Rooms[1]

	manager.evaluateAndActivateRoom(primarySuite, "Day", "trigger-a")
	manager.evaluateAndActivateRoom(primarySuite, "Day", "trigger-b")
	manager.evaluateAndActivateRoom(kitchen, "Day", "trigger-c")

	mockClock.AdvanceAndProcess(310 * time.Millisecond)
	// Both debounces have fired the room evaluation.
	// Primary Suite (HasDynamics=true) only fires its static phase here;
	// the dynamic phase needs another advance past twoStepRecallGap.
	calls := filterSceneCalls(env.MockHA.GetServiceCalls())
	if !assert.Equal(t, 2, len(calls), "each room's debounce fires independently") {
		return
	}

	// Confirm both rooms were targeted (order is not guaranteed).
	seenScenes := map[string]bool{}
	for _, c := range calls {
		seenScenes[c.Data["entity_id"].(string)] = true
	}
	assert.True(t, seenScenes["scene.primary_suite_day"], "Primary Suite scene fired")
	assert.True(t, seenScenes["scene.kitchen_day"], "Kitchen scene fired")
}

// TestActivateScene_TwoStepForDynamicRoom verifies that activating a scene on
// a room with HasDynamics=true issues two scene.turn_on calls: first with
// dynamic=false (static recall), then after twoStepRecallGap with dynamic=true.
func TestActivateScene_TwoStepForDynamicRoom(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	cfg := dynamicScenesConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)

	mockClock := clock.NewMockClock(time.Now())
	manager.SetClock(mockClock)

	room := &cfg.Rooms[0] // Primary Suite, HasDynamics=true
	manager.activateScene(context.Background(), room, "Day", "dayPhase")

	// Static phase should have fired synchronously.
	calls := filterSceneCalls(env.MockHA.GetServiceCalls())
	if !assert.Equal(t, 1, len(calls), "static phase fires immediately") {
		return
	}
	assert.Equal(t, "scene.primary_suite_day", calls[0].Data["entity_id"])
	assert.Equal(t, false, calls[0].Data["dynamic"], "first call is dynamic=false (static recall)")

	// Dynamic phase should NOT have fired yet.
	mockClock.AdvanceAndProcess(twoStepRecallGap - 100*time.Millisecond)
	calls = filterSceneCalls(env.MockHA.GetServiceCalls())
	assert.Equal(t, 1, len(calls), "dynamic phase has not fired before gap elapses")

	// Advance past the gap — dynamic phase fires.
	mockClock.AdvanceAndProcess(200 * time.Millisecond)
	calls = filterSceneCalls(env.MockHA.GetServiceCalls())
	if !assert.Equal(t, 2, len(calls), "dynamic phase fires after twoStepRecallGap") {
		return
	}
	assert.Equal(t, "scene.primary_suite_day", calls[1].Data["entity_id"])
	assert.Equal(t, true, calls[1].Data["dynamic"], "second call is dynamic=true (palette enabled)")
}

// TestActivateScene_SingleStepForNonDynamicRoom verifies that rooms without
// HasDynamics fall back to the previous single-call behavior (no dynamic key
// passed at all, so the Hue scene's own setting takes effect).
func TestActivateScene_SingleStepForNonDynamicRoom(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	cfg := dynamicScenesConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)

	room := &cfg.Rooms[1] // Kitchen, HasDynamics=false
	manager.activateScene(context.Background(), room, "Day", "dayPhase")

	calls := filterSceneCalls(env.MockHA.GetServiceCalls())
	if !assert.Equal(t, 1, len(calls), "non-dynamic room makes a single scene call") {
		return
	}
	_, hasDynamic := calls[0].Data["dynamic"]
	assert.False(t, hasDynamic, "non-dynamic room must not pass `dynamic` key")
}

// TestActivateScene_TwoStepAbortsOnContextCancel verifies that cancelling the
// room-scoped context during the gap suppresses the dynamic phase.
func TestActivateScene_TwoStepAbortsOnContextCancel(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	cfg := dynamicScenesConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)

	mockClock := clock.NewMockClock(time.Now())
	manager.SetClock(mockClock)

	room := &cfg.Rooms[0] // Primary Suite, HasDynamics=true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.activateScene(ctx, room, "Day", "dayPhase")

	// Static phase fired.
	if !assert.Equal(t, 1, len(filterSceneCalls(env.MockHA.GetServiceCalls()))) {
		return
	}

	// Cancel BEFORE the gap elapses; the dynamic phase must not fire.
	cancel()
	mockClock.AdvanceAndProcess(twoStepRecallGap + 100*time.Millisecond)

	calls := filterSceneCalls(env.MockHA.GetServiceCalls())
	assert.Equal(t, 1, len(calls), "dynamic phase suppressed when context cancelled during gap")
}

// TestEvaluateAndActivateRoom_DebounceAndTwoStepEndToEnd is the integration
// of the two mechanisms: a burst of three rapid evaluations on a dynamic room
// should coalesce to a single doEvaluateAndActivateRoom call, which then
// triggers a two-step recall — yielding exactly two scene.turn_on calls
// total (one static + one dynamic). Six recalls in 500ms collapse to two,
// matching the production timing the fix is designed to repair.
func TestEvaluateAndActivateRoom_DebounceAndTwoStepEndToEnd(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	cfg := dynamicScenesConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)

	mockClock := clock.NewMockClock(time.Now())
	manager.SetClock(mockClock)
	manager.SetDebounceDelay(300 * time.Millisecond)

	_ = env.StateMgr.SetBool("isMasterAsleep", false)

	room := &cfg.Rooms[0] // Primary Suite
	// Simulate the arrival burst — three near-simultaneous trigger changes.
	manager.evaluateAndActivateRoom(room, "Day", "isAnyoneHome")
	manager.evaluateAndActivateRoom(room, "Day", "isAnyOwnerHome")
	manager.evaluateAndActivateRoom(room, "Day", "isAnyoneHomeAndAwake")

	// No scene calls yet (debounce still pending).
	assert.Equal(t, 0, len(filterSceneCalls(env.MockHA.GetServiceCalls())))

	// Advance past the debounce — coalesced evaluation runs, static phase fires.
	mockClock.AdvanceAndProcess(310 * time.Millisecond)
	calls := filterSceneCalls(env.MockHA.GetServiceCalls())
	if !assert.Equal(t, 1, len(calls), "exactly one static call after debounce coalesces") {
		return
	}
	assert.Equal(t, false, calls[0].Data["dynamic"])

	// Advance past the two-step gap — dynamic phase fires.
	mockClock.AdvanceAndProcess(twoStepRecallGap + 50*time.Millisecond)
	calls = filterSceneCalls(env.MockHA.GetServiceCalls())
	if !assert.Equal(t, 2, len(calls), "static + dynamic = exactly 2 scene calls total") {
		return
	}
	assert.Equal(t, true, calls[1].Data["dynamic"])
}

// filterSceneCalls returns only the `scene.turn_on` calls from a slice of
// recorded service calls, so tests assert on scene activations without being
// disturbed by unrelated state-sync writes.
func filterSceneCalls(calls []ha.ServiceCall) []ha.ServiceCall {
	out := make([]ha.ServiceCall, 0, len(calls))
	for _, c := range calls {
		if c.Domain == "scene" && c.Service == "turn_on" {
			out = append(out, c)
		}
	}
	return out
}
