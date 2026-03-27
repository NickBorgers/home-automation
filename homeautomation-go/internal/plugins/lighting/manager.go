package lighting

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Manager handles lighting control and scene activation
type Manager struct {
	haClient      ha.HAClient
	stateManager  *state.Manager
	config        *HueConfig
	logger        *zap.Logger
	readOnly      bool
	shadowTracker *shadowstate.LightingTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Context for graceful shutdown
	ctx context.Context

	// Per-room context cancellation to prevent stale commands from
	// retry loops overriding newer actions for the same room.
	roomContexts   map[string]context.CancelFunc
	roomContextsMu sync.Mutex
}

// NewManager creates a new Lighting Control manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, config *HueConfig, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewLightingTracker()

	return &Manager{
		haClient:      haClient,
		stateManager:  stateManager,
		config:        config,
		logger:        logger.Named("lighting"),
		readOnly:      readOnly,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "lighting", logger.Named("lighting")),
		ctx:           ctx,
		roomContexts:  make(map[string]context.CancelFunc),
	}
}

// Start begins monitoring lighting state and triggers
func (m *Manager) Start() error {
	m.logger.Info("Starting Lighting Control Manager")

	// Subscribe to day phase changes (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToState("dayPhase", m.handleDayPhaseChange); err != nil {
		return fmt.Errorf("failed to subscribe to dayPhase: %w", err)
	}

	// Subscribe to sun event changes
	if err := m.subHelper.SubscribeToState("sunevent", m.handleSunEventChange); err != nil {
		return fmt.Errorf("failed to subscribe to sunevent: %w", err)
	}

	// Subscribe to presence changes that might affect lighting
	if err := m.subHelper.SubscribeToState("isAnyoneHome", m.handlePresenceChange); err != nil {
		return fmt.Errorf("failed to subscribe to isAnyoneHome: %w", err)
	}

	// Subscribe to TV state for brightness adjustments
	if err := m.subHelper.SubscribeToState("isTVPlaying", m.handleTVStateChange); err != nil {
		return fmt.Errorf("failed to subscribe to isTVPlaying: %w", err)
	}

	// Subscribe to sleep state changes
	if err := m.subHelper.SubscribeToState("isEveryoneAsleep", m.handleSleepStateChange); err != nil {
		return fmt.Errorf("failed to subscribe to isEveryoneAsleep: %w", err)
	}

	if err := m.subHelper.SubscribeToState("isMasterAsleep", m.handleSleepStateChange); err != nil {
		return fmt.Errorf("failed to subscribe to isMasterAsleep: %w", err)
	}

	// Subscribe to guest presence
	if err := m.subHelper.SubscribeToState("isHaveGuests", m.handlePresenceChange); err != nil {
		return fmt.Errorf("failed to subscribe to isHaveGuests: %w", err)
	}

	// Subscribe to all occupancy and condition variables from room configs
	occupancyVars := m.collectConditionVariables()
	for _, varName := range occupancyVars {
		if err := m.subHelper.SubscribeToState(varName, m.handleOccupancyChange); err != nil {
			// Log warning but don't fail - variable might not exist yet
			m.logger.Warn("Failed to subscribe to condition variable",
				zap.String("variable", varName),
				zap.Error(err))
			continue
		}
		m.logger.Debug("Subscribed to condition variable",
			zap.String("variable", varName))
	}

	// Initialize shadow state with current input values (after all subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("Lighting Control Manager started successfully")
	return nil
}

// Stop stops the Lighting Control Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Lighting Control Manager")

	// Cancel all in-flight room operations
	m.cancelAllRoomContexts()

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Lighting Control Manager stopped")
}

// getRoomContext cancels any in-flight operation for the room and returns a fresh context.
// Follows the pattern from music plugin's startFadeInWithContext (fadein.go:570-586).
func (m *Manager) getRoomContext(roomName string) context.Context {
	m.roomContextsMu.Lock()
	defer m.roomContextsMu.Unlock()
	if cancel, exists := m.roomContexts[roomName]; exists {
		cancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.roomContexts[roomName] = cancel
	return ctx
}

// cancelAllRoomContexts cancels all in-flight room operations.
func (m *Manager) cancelAllRoomContexts() {
	m.roomContextsMu.Lock()
	defer m.roomContextsMu.Unlock()
	for _, cancel := range m.roomContexts {
		cancel()
	}
	m.roomContexts = make(map[string]context.CancelFunc)
}

// handleDayPhaseChange processes day phase changes and activates scenes
func (m *Manager) handleDayPhaseChange(key string, oldValue, newValue interface{}) {
	newPhase, ok := newValue.(string)
	if !ok {
		m.logger.Warn("Day phase value is not a string", zap.Any("value", newValue))
		return
	}

	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	m.logger.Info("Day phase changed, activating scenes",
		zap.Any("old", oldValue),
		zap.String("new", newPhase))

	// Activate scenes for all rooms based on new day phase
	// dayPhase changes always affect all rooms (like "reset" in Node-RED)
	m.activateScenesForAllRooms(newPhase, key)
}

// handleSunEventChange processes sun event changes
func (m *Manager) handleSunEventChange(key string, oldValue, newValue interface{}) {
	newEvent, ok := newValue.(string)
	if !ok {
		m.logger.Warn("Sun event value is not a string", zap.Any("value", newValue))
		return
	}

	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	m.logger.Info("Sun event changed",
		zap.Any("old", oldValue),
		zap.String("new", newEvent))

	// Sun events might trigger scene changes
	// Get current day phase and reactivate scenes
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	// sunevent changes always affect all rooms (like "reset" in Node-RED)
	m.activateScenesForAllRooms(dayPhase, key)
}

// handlePresenceChange processes presence changes
func (m *Manager) handlePresenceChange(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	m.logger.Info("Presence state changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Re-evaluate all rooms, filtering by relevance
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	m.evaluateAllRooms(dayPhase, key)
}

// handleTVStateChange processes TV state changes
func (m *Manager) handleTVStateChange(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	m.logger.Info("TV state changed",
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Re-evaluate rooms that depend on TV state, filtering by relevance
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	m.evaluateAllRooms(dayPhase, key)
}

// handleSleepStateChange processes sleep state changes
func (m *Manager) handleSleepStateChange(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	m.logger.Info("Sleep state changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Re-evaluate all rooms, filtering by relevance
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	m.evaluateAllRooms(dayPhase, key)
}

// collectConditionVariables collects all unique variables from room conditions
// These are variables like isNickOfficeOccupied, isKitchenOccupied that need subscriptions
func (m *Manager) collectConditionVariables() []string {
	// Use a map to collect unique variables
	varMap := make(map[string]bool)

	// Standard variables that are already subscribed to via explicit handlers
	alreadySubscribed := map[string]bool{
		"dayPhase":         true,
		"sunevent":         true,
		"isAnyoneHome":     true,
		"isTVPlaying":      true,
		"isEveryoneAsleep": true,
		"isMasterAsleep":   true,
		"isHaveGuests":     true,
	}

	for _, room := range m.config.Rooms {
		// Collect all condition variables from this room
		for _, varName := range room.GetConditionVariables() {
			if varName != "" && !alreadySubscribed[varName] {
				varMap[varName] = true
			}
		}
	}

	// Convert map to slice
	result := make([]string, 0, len(varMap))
	for varName := range varMap {
		result = append(result, varName)
	}

	return result
}

// handleOccupancyChange processes occupancy and other room-specific condition changes
func (m *Manager) handleOccupancyChange(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	m.logger.Info("Occupancy/condition state changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Get current day phase for scene activation
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	// Evaluate rooms that use this variable
	m.evaluateAllRooms(dayPhase, key)
}

// activateScenesForAllRooms activates scenes for all configured rooms
func (m *Manager) activateScenesForAllRooms(dayPhase string, trigger string) {
	for _, room := range m.config.Rooms {
		m.evaluateAndActivateRoom(&room, dayPhase, trigger)
	}
}

// evaluateAllRooms re-evaluates all rooms and activates scenes as needed
// Only evaluates rooms where the trigger variable is relevant (matches Node-RED)
func (m *Manager) evaluateAllRooms(dayPhase string, trigger string) {
	for _, room := range m.config.Rooms {
		// Check if this trigger is relevant to this room
		if m.isTopicRelevant(&room, trigger) {
			m.evaluateAndActivateRoom(&room, dayPhase, trigger)
		} else {
			m.logger.Debug("Skipping room evaluation - trigger not relevant",
				zap.String("room", room.HueGroup),
				zap.String("trigger", trigger))
		}
	}
}

// evaluateAndActivateRoom evaluates a room's conditions and activates the appropriate scene
func (m *Manager) evaluateAndActivateRoom(room *RoomConfig, dayPhase string, trigger string) {
	// Cancel any in-flight operation for this room - new evaluation supersedes
	roomCtx := m.getRoomContext(room.HueGroup)

	m.logger.Debug("Evaluating room",
		zap.String("room", room.HueGroup),
		zap.String("area_id", room.HASSAreaID),
		zap.String("day_phase", dayPhase),
		zap.String("trigger", trigger))

	// Evaluate conditions in order - first matching condition wins
	action, matchedVar := m.evaluateConditions(room)

	m.logger.Debug("Room evaluation result",
		zap.String("room", room.HueGroup),
		zap.String("action", action),
		zap.String("matched_variable", matchedVar))

	switch action {
	case "on":
		m.logger.Info("Room should be turned on with scene",
			zap.String("room", room.HueGroup),
			zap.String("day_phase", dayPhase),
			zap.String("matched_condition", matchedVar))
		m.activateScene(roomCtx, room, dayPhase, trigger)
	case "off":
		m.logger.Info("Room should be turned off",
			zap.String("room", room.HueGroup),
			zap.String("matched_condition", matchedVar))
		m.turnOffRoom(roomCtx, room, trigger)
	case "skip":
		// Skip action: do nothing for this room (e.g., when Hue Sync is controlling the lights)
		// Previous operation cancelled by getRoomContext, no new action needed
		m.logger.Info("Skipping room - external control active",
			zap.String("room", room.HueGroup),
			zap.String("matched_condition", matchedVar))
	default:
		m.logger.Debug("No action needed for room",
			zap.String("room", room.HueGroup))
	}
}

// isTopicRelevant checks if a state variable change is relevant to a room's conditions
// Matches Node-RED behavior: dayPhase and sunevent always relevant, otherwise check if variable is used in conditions
func (m *Manager) isTopicRelevant(room *RoomConfig, trigger string) bool {
	// dayPhase and sunevent changes always affect all rooms (like "reset" in Node-RED)
	if trigger == "dayPhase" || trigger == "sunevent" || trigger == "" || trigger == "reset" {
		return true
	}

	// Check if trigger appears in any of the room's conditions
	for _, cond := range room.Conditions {
		if cond.Variable == trigger {
			return true
		}
	}

	return false
}

// evaluateConditions evaluates room conditions in order and returns the action to take
// Returns the action ("on", "off", or "" for no action) and the variable that matched
func (m *Manager) evaluateConditions(room *RoomConfig) (action string, matchedVariable string) {
	for _, cond := range room.Conditions {
		if cond.Variable == "" {
			continue
		}

		// Get the current value of the state variable
		currentValue, err := m.getStateValue(cond.Variable)
		if err != nil {
			m.logger.Warn("Failed to get state value for condition",
				zap.String("room", room.HueGroup),
				zap.String("variable", cond.Variable),
				zap.Error(err))
			continue
		}

		// Check if the condition matches using flexible type comparison
		if valuesMatch(currentValue, cond.Value) {
			m.logger.Debug("Condition matched",
				zap.String("room", room.HueGroup),
				zap.String("variable", cond.Variable),
				zap.Any("expected", cond.Value),
				zap.Any("actual", currentValue),
				zap.String("action", cond.Action))
			return cond.Action, cond.Variable
		}
	}

	// No condition matched
	return "", ""
}

// getStateValue retrieves a state variable value, trying different types
func (m *Manager) getStateValue(variable string) (interface{}, error) {
	// Try boolean first (most common for lighting conditions)
	if val, err := m.stateManager.GetBool(variable); err == nil {
		return val, nil
	}

	// Try string
	if val, err := m.stateManager.GetString(variable); err == nil {
		return val, nil
	}

	// Try number
	if val, err := m.stateManager.GetNumber(variable); err == nil {
		return val, nil
	}

	return nil, fmt.Errorf("failed to get value for variable %s", variable)
}

// toSnakeCase converts a string to snake_case format
// Matches the Node-RED implementation that converts "Primary Suite evening" to "primary_suite_evening"
func toSnakeCase(str string) string {
	// Simple approach: lowercase, replace spaces with underscores
	// This matches the Node-RED behavior for room names like "Primary Suite evening" -> "primary_suite_evening"
	result := strings.ToLower(str)
	result = strings.ReplaceAll(result, " ", "_")

	// Also handle multiple consecutive underscores
	re := regexp.MustCompile(`_+`)
	result = re.ReplaceAllString(result, "_")

	return strings.Trim(result, "_")
}

// maxReturnHomeTransition is the maximum transition (in seconds) when activating
// scenes after someone returns home. Long transitions on lights that are off can
// silently fail on Hue bridges, and users want lights up quickly when arriving.
const maxReturnHomeTransition = 5

// isPresenceTrigger returns true if the trigger variable indicates a presence change
// (someone arriving/leaving), which means lights may be transitioning from off to on.
func isPresenceTrigger(trigger string) bool {
	switch trigger {
	case "isAnyoneHome", "isAnyoneHomeAndAwake", "isNickHome", "isCarolineHome", "isHaveGuests":
		return true
	default:
		return false
	}
}

// activateScene activates a Hue scene for a room
func (m *Manager) activateScene(ctx context.Context, room *RoomConfig, dayPhase string, trigger string) {
	// Construct scene entity ID: scene.{snake_case(hue_group + " " + day_phase)}
	sceneName := room.HueGroup + " " + dayPhase
	sceneEntityID := "scene." + toSnakeCase(sceneName)

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would activate scene",
			zap.String("room", room.HueGroup),
			zap.String("area_id", room.HASSAreaID),
			zap.String("scene", dayPhase),
			zap.String("entity_id", sceneEntityID),
			zap.String("trigger", trigger))
		// Record shadow state even in read-only mode for consistency with music plugin
		m.recordAction(room.HueGroup, "activate_scene",
			fmt.Sprintf("Would activate scene '%s'", dayPhase),
			dayPhase, false, trigger)
		return
	}

	m.logger.Info("Activating scene",
		zap.String("room", room.HueGroup),
		zap.String("area_id", room.HASSAreaID),
		zap.String("scene", dayPhase),
		zap.String("entity_id", sceneEntityID),
		zap.String("trigger", trigger),
		zap.Any("transition_seconds", room.TransitionSeconds))

	// Call Home Assistant scene.turn_on service (matches Node-RED)
	// Note: Only pass entity_id for scenes. Passing area_id alongside entity_id
	// can cause Home Assistant to activate unexpected scenes.
	serviceData := map[string]interface{}{
		"entity_id": sceneEntityID,
	}

	// Add transition if specified.
	// When triggered by a presence change (someone returning home), cap the transition
	// to avoid Hue bridge issues where long transitions on off lights silently fail.
	if room.TransitionSeconds != nil {
		transition := *room.TransitionSeconds
		if isPresenceTrigger(trigger) && transition > maxReturnHomeTransition {
			m.logger.Info("Capping transition for presence-triggered scene activation",
				zap.String("room", room.HueGroup),
				zap.Int("original_transition", transition),
				zap.Int("capped_transition", maxReturnHomeTransition),
				zap.String("trigger", trigger))
			transition = maxReturnHomeTransition
		}
		serviceData["transition"] = transition
	}

	// The Nook doesn't do well with dynamics because of its lights
	if room.HueGroup == "Nook" {
		serviceData["dynamic"] = false
	}

	// Call the service with the room-scoped context (cancelled if room is re-evaluated)
	err := m.haClient.CallService(ctx, "scene", "turn_on", serviceData)
	if err != nil {
		if ctx.Err() != nil {
			m.logger.Info("Scene activation superseded by newer evaluation",
				zap.String("room", room.HueGroup),
				zap.String("scene", dayPhase),
				zap.String("entity_id", sceneEntityID))
			return
		}
		m.logger.Error("Failed to activate scene",
			zap.String("room", room.HueGroup),
			zap.String("scene", dayPhase),
			zap.String("entity_id", sceneEntityID),
			zap.Error(err))
		return
	}

	m.logger.Info("Scene activated successfully",
		zap.String("room", room.HueGroup),
		zap.String("scene", dayPhase),
		zap.String("entity_id", sceneEntityID))

	// Record action in shadow state
	m.recordAction(room.HueGroup, "activate_scene",
		fmt.Sprintf("Activated scene '%s'", dayPhase),
		dayPhase, false, trigger)
}

// turnOffRoom turns off lights in a room
func (m *Manager) turnOffRoom(ctx context.Context, room *RoomConfig, trigger string) {
	if m.readOnly {
		m.logger.Info("READ-ONLY: Would turn off room",
			zap.String("room", room.HueGroup),
			zap.String("area_id", room.HASSAreaID),
			zap.String("trigger", trigger))
		// Record shadow state even in read-only mode for consistency with music plugin
		m.recordAction(room.HueGroup, "turn_off", "Would turn off room", "", true, trigger)
		return
	}

	m.logger.Info("Turning off room",
		zap.String("room", room.HueGroup),
		zap.String("area_id", room.HASSAreaID),
		zap.String("trigger", trigger))

	// Use light.turn_off with area_id
	// Note: We intentionally do NOT apply transition_seconds to turn_off commands.
	// The transition setting is designed for pleasant scene changes during waking hours,
	// but when turning off (especially for sleep), users expect immediate darkness.
	serviceData := map[string]interface{}{
		"area_id": room.HASSAreaID,
	}

	// Call the service with the room-scoped context (cancelled if room is re-evaluated)
	err := m.haClient.CallService(ctx, "light", "turn_off", serviceData)
	if err != nil {
		if ctx.Err() != nil {
			m.logger.Info("Room turn-off superseded by newer evaluation",
				zap.String("room", room.HueGroup),
				zap.String("area_id", room.HASSAreaID))
			return
		}
		m.logger.Error("Failed to turn off room",
			zap.String("room", room.HueGroup),
			zap.String("area_id", room.HASSAreaID),
			zap.Error(err))
		return
	}

	m.logger.Info("Room turned off successfully",
		zap.String("room", room.HueGroup))

	// Record action in shadow state
	m.recordAction(room.HueGroup, "turn_off", "Turned off room", "", true, trigger)
}

// Reset re-applies lighting scenes for all rooms based on current day phase
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Lighting Control - re-applying scenes for all rooms")

	// Get current day phase
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		return fmt.Errorf("failed to get dayPhase: %w", err)
	}

	m.logger.Info("Re-activating scenes for current day phase",
		zap.String("day_phase", dayPhase))

	// Re-apply scenes for all rooms (like the comment says: "like reset in Node-RED")
	m.activateScenesForAllRooms(dayPhase, "reset")

	m.logger.Info("Successfully reset Lighting Control")
	return nil
}

// addTriggerToInputs adds the trigger field to the current shadow state inputs
// Note: Other inputs are automatically captured by SubscriptionHelper before handlers run
func (m *Manager) addTriggerToInputs(trigger string) {
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{
		"trigger": trigger,
	})
}

// recordAction captures the current inputs and records an action in shadow state
func (m *Manager) recordAction(roomName string, actionType string, reason string, activeScene string, turnedOff bool, trigger string) {
	// Add trigger to inputs (other inputs already captured by SubscriptionHelper)
	m.addTriggerToInputs(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the action
	m.shadowTracker.RecordRoomAction(roomName, actionType, reason, activeScene, turnedOff)
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.LightingShadowState {
	return m.shadowTracker.GetState()
}
