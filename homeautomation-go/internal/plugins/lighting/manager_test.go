package lighting

import (
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// createTestConfig creates a test hue configuration
// Note: Only using state variables that exist in the state manager
func createTestConfig() *HueConfig {
	transition30 := 30
	transition180 := 180

	return &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:                 "Living Room",
				HASSAreaID:               "living_room_2",
				OnIfTrue:                 "isAnyoneHome",
				OnIfFalse:                "isTVPlaying",
				OffIfTrue:                "isEveryoneAsleep",
				OffIfFalse:               "isAnyoneHome",
				IncreaseBrightnessIfTrue: "isHaveGuests",
				TransitionSeconds:        &transition30,
			},
			{
				HueGroup:                 "Primary Suite",
				HASSAreaID:               "master_bedroom",
				OnIfTrue:                 nil,
				OnIfFalse:                "isMasterAsleep",
				OffIfTrue:                "isMasterAsleep",
				OffIfFalse:               "isNickHome",
				IncreaseBrightnessIfTrue: nil,
				TransitionSeconds:        &transition180,
			},
		},
	}
}

func TestNewManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
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

func TestEvaluateCondition(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Set test conditions
	err := stateManager.SetBool("isAnyoneHome", true)
	assert.NoError(t, err)

	err = stateManager.SetBool("isEveryoneAsleep", false)
	assert.NoError(t, err)

	tests := []struct {
		name      string
		condition string
		expected  bool
	}{
		{"Empty condition", "", false},
		{"True condition", "isAnyoneHome", true},
		{"False condition", "isEveryoneAsleep", false},
		{"Nonexistent condition", "nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.evaluateCondition(tt.condition)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateOnConditions(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name           string
		setupState     func()
		roomIndex      int
		expectedResult bool
	}{
		{
			name: "Living room - on_if_true is true",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", true)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:      0,
			expectedResult: true,
		},
		{
			name: "Living room - on_if_false is false",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", false)
				_ = stateManager.SetBool("isTVPlaying", false)
			},
			roomIndex:      0,
			expectedResult: true,
		},
		{
			name: "Living room - neither condition met",
			setupState: func() {
				_ = stateManager.SetBool("isAnyoneHome", false)
				_ = stateManager.SetBool("isTVPlaying", true)
			},
			roomIndex:      0,
			expectedResult: false,
		},
		{
			name: "Primary suite - on_if_false is false",
			setupState: func() {
				_ = stateManager.SetBool("isMasterAsleep", false)
			},
			roomIndex:      1,
			expectedResult: true,
		},
		{
			name: "Primary suite - on_if_false is true",
			setupState: func() {
				_ = stateManager.SetBool("isMasterAsleep", true)
			},
			roomIndex:      1,
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupState()
			room := &config.Rooms[tt.roomIndex]
			result := manager.evaluateOnConditions(room)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestEvaluateOffConditions(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name           string
		setupState     func()
		roomIndex      int
		expectedResult bool
	}{
		{
			name: "Living room - off_if_true is true",
			setupState: func() {
				_ = stateManager.SetBool("isEveryoneAsleep", true)
				_ = stateManager.SetBool("isAnyoneHome", true)
			},
			roomIndex:      0,
			expectedResult: true,
		},
		{
			name: "Living room - off_if_false is false",
			setupState: func() {
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isAnyoneHome", false)
			},
			roomIndex:      0,
			expectedResult: true,
		},
		{
			name: "Living room - neither condition met",
			setupState: func() {
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isAnyoneHome", true)
			},
			roomIndex:      0,
			expectedResult: false,
		},
		{
			name: "Primary suite - off_if_true is true",
			setupState: func() {
				_ = stateManager.SetBool("isMasterAsleep", true)
			},
			roomIndex:      1,
			expectedResult: true,
		},
		{
			name: "Primary suite - off_if_true is false, off_if_false is false",
			setupState: func() {
				_ = stateManager.SetBool("isMasterAsleep", false)
				_ = stateManager.SetBool("isNickHome", false)
			},
			roomIndex:      1,
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupState()
			room := &config.Rooms[tt.roomIndex]
			result := manager.evaluateOffConditions(room)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestActivateSceneReadOnly(t *testing.T) {
	logger, _ := zap.NewDevelopment()
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
	logger, _ := zap.NewDevelopment()
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
	logger, _ := zap.NewDevelopment()
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
	logger, _ := zap.NewDevelopment()
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
	logger, _ := zap.NewDevelopment()
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
	logger, _ := zap.NewDevelopment()
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
			name: "Room should turn off",
			setupState: func() {
				// Set OFF condition to true
				_ = stateManager.SetBool("isEveryoneAsleep", true)
				// Make sure ON conditions are false
				_ = stateManager.SetBool("isAnyoneHome", false)
				_ = stateManager.SetBool("isTVPlaying", true) // OnIfFalse should be false
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
				_ = stateManager.SetBool("isEveryoneAsleep", false)
				_ = stateManager.SetBool("isAnyoneHome", true)
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
	logger, _ := zap.NewDevelopment()
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
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Create minimal config
	config := &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "Living Room",
				HASSAreaID: "living_room",
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

	// Verify subscriptions were created (7 subscriptions)
	assert.Equal(t, 7, len(manager.subscriptions), "Should have 7 subscriptions")

	// Stop manager
	manager.Stop()

	// Verify subscriptions were cleaned up
	assert.Nil(t, manager.subscriptions, "Subscriptions should be nil after Stop")
}

func TestManagerReset(t *testing.T) {
	logger, _ := zap.NewDevelopment()
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
