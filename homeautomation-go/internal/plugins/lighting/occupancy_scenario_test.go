package lighting

// Occupancy-based lighting scenario tests.
// Validates that the Lighting Manager responds correctly to room occupancy changes.

import (
	"context"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// createOccupancyTestConfig creates a configuration matching the actual hue_config.yaml
// with Nick Office and Kitchen rooms that respond to occupancy.
func createOccupancyTestConfig() *HueConfig {
	transition2 := 2
	transition5 := 5

	return &HueConfig{
		Rooms: []RoomConfig{
			{
				HueGroup:   "N Office",
				HASSAreaID: "n_office",
				Conditions: []LightingCondition{
					{Action: "off", Variable: "isNickOfficeOccupied", Value: false},
					{Action: "on", Variable: "isNickOfficeOccupied", Value: true},
				},
				TransitionSeconds: &transition2,
			},
			{
				HueGroup:   "Kitchen",
				HASSAreaID: "kitchen",
				Conditions: []LightingCondition{
					{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
					{Action: "on", Variable: "isKitchenOccupied", Value: true},
				},
				TransitionSeconds:      &transition5,
				SkipReactivationWhenOn: true,
			},
		},
	}
}

// setupOccupancyManager creates a manager with standard occupancy state initialized.
func setupOccupancyManager(t *testing.T, config *HueConfig, overrides map[string]bool) (*Manager, *ha.MockClient, int) {
	t.Helper()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	defaults := map[string]bool{
		"isAnyoneHome":         true,
		"isTVPlaying":          false,
		"isEveryoneAsleep":     false,
		"isMasterAsleep":       false,
		"isHaveGuests":         false,
		"isNickOfficeOccupied": false,
		"isKitchenOccupied":    false,
		"isAnyoneHomeAndAwake": true,
	}
	for k, v := range overrides {
		defaults[k] = v
	}

	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("sunevent", "day")
	for k, v := range defaults {
		_ = stateManager.SetBool(k, v)
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil)
	err := manager.Start()
	assert.NoError(t, err)
	snapshot := mockClient.ServiceCallCount()

	return manager, mockClient, snapshot
}

func TestScenario_OccupancyLighting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		dayPhase      string
		overrides     map[string]bool
		changeVar     string
		changeVal     bool
		expectDomain  string
		expectService string
		expectMatch   string // entity_id or area_id to match
		matchField    string // "entity_id" or "area_id"
		expectTrans   *int   // expected transition value (nil = check it's nil)
	}{
		{
			name:          "Nick office occupied turns on lights",
			dayPhase:      "day",
			overrides:     map[string]bool{"isNickOfficeOccupied": false},
			changeVar:     "isNickOfficeOccupied",
			changeVal:     true,
			expectDomain:  "scene",
			expectService: "turn_on",
			expectMatch:   "scene.n_office_day",
			matchField:    "entity_id",
			expectTrans:   intPtr(2),
		},
		{
			name:          "Nick office unoccupied turns off lights",
			overrides:     map[string]bool{"isNickOfficeOccupied": true},
			changeVar:     "isNickOfficeOccupied",
			changeVal:     false,
			expectDomain:  "light",
			expectService: "turn_off",
			expectMatch:   "n_office",
			matchField:    "area_id",
		},
		{
			name:          "Kitchen occupied turns on lights with evening scene",
			dayPhase:      "evening",
			overrides:     map[string]bool{"isKitchenOccupied": false},
			changeVar:     "isKitchenOccupied",
			changeVal:     true,
			expectDomain:  "scene",
			expectService: "turn_on",
			expectMatch:   "scene.kitchen_evening",
			matchField:    "entity_id",
			expectTrans:   intPtr(5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createOccupancyTestConfig()
			manager, mockClient, snapshot := setupOccupancyManager(t, config, tt.overrides)
			defer manager.Stop()

			if tt.dayPhase != "" {
				_ = manager.stateManager.SetString("dayPhase", tt.dayPhase)
			}

			err := manager.stateManager.SetBool(tt.changeVar, tt.changeVal)
			assert.NoError(t, err)

			calls := mockClient.GetServiceCallsSince(snapshot)
			found := false
			for _, call := range calls {
				if call.Domain == tt.expectDomain && call.Service == tt.expectService {
					val, ok := call.Data[tt.matchField].(string)
					if ok && val == tt.expectMatch {
						found = true
						if tt.expectDomain == "scene" {
							assert.Nil(t, call.Data["area_id"], "Scene activation should NOT include area_id")
						}
						if tt.expectTrans != nil {
							assert.Equal(t, *tt.expectTrans, call.Data["transition"])
						} else {
							assert.Nil(t, call.Data["transition"], "turn_off should not include transition")
						}
					}
				}
			}
			assert.True(t, found, "Expected %s.%s for %s=%s. Calls: %+v",
				tt.expectDomain, tt.expectService, tt.matchField, tt.expectMatch, calls)
		})
	}
}

// TestScenario_OccupancyChangeOnlyAffectsRelevantRoom verifies that occupancy
// changes for one room don't affect other rooms.
func TestScenario_OccupancyChangeOnlyAffectsRelevantRoom(t *testing.T) {
	t.Parallel()
	config := createOccupancyTestConfig()
	manager, mockClient, snapshot := setupOccupancyManager(t, config, nil)
	defer manager.Stop()

	// Nick enters his office
	err := manager.stateManager.SetBool("isNickOfficeOccupied", true)
	assert.NoError(t, err)

	calls := mockClient.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			if entityID, ok := call.Data["entity_id"].(string); ok {
				assert.NotEqual(t, "scene.kitchen_day", entityID,
					"Kitchen lights should NOT be affected by Nick Office occupancy change")
			}
		}
		if call.Domain == "light" && call.Service == "turn_off" {
			if areaID, ok := call.Data["area_id"].(string); ok {
				assert.NotEqual(t, "kitchen", areaID,
					"Kitchen lights should NOT be turned off by Nick Office occupancy change")
			}
		}
	}
}

// TestScenario_KitchenSkipsReactivationWhenAlreadyOn validates the fix for the
// "lights go down on someone" bug. The kitchen mmWave radar can briefly drop
// detection on a stationary person and then re-detect them. Without edge-
// triggered activation, every re-detection re-fires the scene and overrides
// any manual brightness the user set. The skip_reactivation_when_on flag
// makes a duplicate on→on evaluation a no-op so manual adjustments stick.
//
// GIVEN: Kitchen scene has been activated (lights on)
// WHEN:  isKitchenOccupied flips false (radar glitch), then back true (re-detect)
// THEN:  No second scene.turn_on for the kitchen is issued
func TestScenario_KitchenSkipsReactivationWhenAlreadyOn(t *testing.T) {
	t.Parallel()
	config := createOccupancyTestConfig()
	manager, mockClient, _ := setupOccupancyManager(t, config, nil)
	defer manager.Stop()

	t.Log("GIVEN: Kitchen is occupied with scene already active")
	err := manager.stateManager.SetBool("isKitchenOccupied", true)
	assert.NoError(t, err)

	// Snapshot AFTER the initial activation so we only observe what happens
	// during the radar glitch sequence.
	// NOTE: MockClient dispatches handlers synchronously, so snapshot is taken
	// only after the GIVEN SetBool handler has fully completed.
	snapshot := mockClient.ServiceCallCount()

	t.Log("WHEN: radar glitches (false) then re-detects (true)")
	err = manager.stateManager.SetBool("isKitchenOccupied", false)
	assert.NoError(t, err)
	err = manager.stateManager.SetBool("isKitchenOccupied", true)
	assert.NoError(t, err)

	t.Log("THEN: no additional scene.turn_on for kitchen — user's brightness preserved")
	calls := mockClient.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			if entityID, ok := call.Data["entity_id"].(string); ok {
				assert.NotContains(t, entityID, "kitchen",
					"Kitchen scene must NOT be re-activated after radar glitch — would override manual brightness. Got: %s", entityID)
			}
		}
	}
}

// TestScenario_KitchenStillRespondsToDayPhaseChange validates that the
// skip-reactivation flag does not break legitimate scene changes. Day phase
// transitions target a different scene (e.g. kitchen_day → kitchen_evening)
// and must always fire even when the room is already on.
//
// GIVEN: Kitchen is occupied with day scene active
// WHEN:  dayPhase changes to evening
// THEN:  scene.kitchen_evening IS activated
func TestScenario_KitchenStillRespondsToDayPhaseChange(t *testing.T) {
	t.Parallel()
	config := createOccupancyTestConfig()
	manager, mockClient, _ := setupOccupancyManager(t, config, nil)
	defer manager.Stop()

	t.Log("GIVEN: Kitchen is occupied with day scene active")
	err := manager.stateManager.SetBool("isKitchenOccupied", true)
	assert.NoError(t, err)

	// NOTE: MockClient dispatches handlers synchronously, so snapshot is taken
	// only after the GIVEN SetBool handler has fully completed.
	snapshot := mockClient.ServiceCallCount()

	t.Log("WHEN: dayPhase changes to evening")
	err = manager.stateManager.SetString("dayPhase", "evening")
	assert.NoError(t, err)

	t.Log("THEN: scene.kitchen_evening is activated (global trigger bypasses skip)")
	calls := mockClient.GetServiceCallsSince(snapshot)
	found := false
	for _, call := range calls {
		if call.Domain == "scene" && call.Service == "turn_on" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "scene.kitchen_evening" {
				found = true
				break
			}
		}
	}
	assert.True(t, found, "Expected scene.kitchen_evening to fire on dayPhase change. Calls: %+v", calls)
}

func intPtr(i int) *int { return &i }
