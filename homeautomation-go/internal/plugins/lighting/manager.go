package lighting

import (
	"fmt"
	"regexp"
	"strings"

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

	// Subscriptions for cleanup
	subscriptions []state.Subscription

	// Automatic input capture for shadow state
	pluginName  string
	registry    *shadowstate.SubscriptionRegistry
	inputHelper *shadowstate.InputCaptureHelper
}

// NewManager creates a new Lighting Control manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, config *HueConfig, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	m := &Manager{
		haClient:      haClient,
		stateManager:  stateManager,
		config:        config,
		logger:        logger.Named("lighting"),
		readOnly:      readOnly,
		shadowTracker: shadowstate.NewLightingTracker(),
		subscriptions: make([]state.Subscription, 0),
		pluginName:    "lighting",
		registry:      registry,
	}

	// Create input helper if registry provided
	if registry != nil {
		m.inputHelper = shadowstate.NewInputCaptureHelper(registry, haClient, stateManager)
	}

	return m
}

// Start begins monitoring lighting state and triggers
func (m *Manager) Start() error {
	m.logger.Info("Starting Lighting Control Manager")

	// Subscribe to day phase changes
	sub, err := m.stateManager.Subscribe("dayPhase", m.handleDayPhaseChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to dayPhase: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "dayPhase")
	}

	// Subscribe to sun event changes
	sub, err = m.stateManager.Subscribe("sunevent", m.handleSunEventChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to sunevent: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "sunevent")
	}

	// Subscribe to presence changes that might affect lighting
	sub, err = m.stateManager.Subscribe("isAnyoneHome", m.handlePresenceChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isAnyoneHome: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "isAnyoneHome")
	}

	// Subscribe to TV state for brightness adjustments
	sub, err = m.stateManager.Subscribe("isTVPlaying", m.handleTVStateChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isTVPlaying: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "isTVPlaying")
	}

	// Subscribe to sleep state changes
	sub, err = m.stateManager.Subscribe("isEveryoneAsleep", m.handleSleepStateChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isEveryoneAsleep: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "isEveryoneAsleep")
	}

	sub, err = m.stateManager.Subscribe("isMasterAsleep", m.handleSleepStateChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isMasterAsleep: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "isMasterAsleep")
	}

	// Subscribe to guest presence
	sub, err = m.stateManager.Subscribe("isHaveGuests", m.handlePresenceChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isHaveGuests: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)
	if m.registry != nil {
		m.registry.RegisterStateSubscription(m.pluginName, "isHaveGuests")
	}

	// Subscribe to all occupancy and condition variables from room configs
	occupancyVars := m.collectConditionVariables()
	for _, varName := range occupancyVars {
		varNameCopy := varName // Capture loop variable
		sub, err = m.stateManager.Subscribe(varNameCopy, m.handleOccupancyChange)
		if err != nil {
			// Log warning but don't fail - variable might not exist yet
			m.logger.Warn("Failed to subscribe to condition variable",
				zap.String("variable", varNameCopy),
				zap.Error(err))
			continue
		}
		m.subscriptions = append(m.subscriptions, sub)
		if m.registry != nil {
			m.registry.RegisterStateSubscription(m.pluginName, varNameCopy)
		}
		m.logger.Debug("Subscribed to condition variable",
			zap.String("variable", varNameCopy))
	}

	// Initialize shadow state with current input values (after all subscriptions registered)
	m.updateShadowInputs()

	m.logger.Info("Lighting Control Manager started successfully")
	return nil
}

// Stop stops the Lighting Control Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Lighting Control Manager")

	// Unsubscribe from all subscriptions
	for _, sub := range m.subscriptions {
		sub.Unsubscribe()
	}
	m.subscriptions = nil

	m.logger.Info("Lighting Control Manager stopped")
}

// handleDayPhaseChange processes day phase changes and activates scenes
func (m *Manager) handleDayPhaseChange(key string, oldValue, newValue interface{}) {
	newPhase, ok := newValue.(string)
	if !ok {
		m.logger.Warn("Day phase value is not a string", zap.Any("value", newValue))
		return
	}

	// Update shadow state current inputs immediately
	m.updateShadowInputs()

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

	// Update shadow state current inputs immediately
	m.updateShadowInputs()

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
	// Update shadow state current inputs immediately
	m.updateShadowInputs()

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
	// Update shadow state current inputs immediately
	m.updateShadowInputs()

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
	// Update shadow state current inputs immediately
	m.updateShadowInputs()

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

// collectConditionVariables collects all unique variables from room on/off conditions
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
		// Collect from all condition types
		for _, condition := range room.GetOnIfTrueConditions() {
			if condition != "" && !alreadySubscribed[condition] {
				varMap[condition] = true
			}
		}
		for _, condition := range room.GetOnIfFalseConditions() {
			if condition != "" && !alreadySubscribed[condition] {
				varMap[condition] = true
			}
		}
		for _, condition := range room.GetOffIfTrueConditions() {
			if condition != "" && !alreadySubscribed[condition] {
				varMap[condition] = true
			}
		}
		for _, condition := range room.GetOffIfFalseConditions() {
			if condition != "" && !alreadySubscribed[condition] {
				varMap[condition] = true
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
	// Update shadow state current inputs immediately
	m.updateShadowInputs()

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
	m.logger.Debug("Evaluating room",
		zap.String("room", room.HueGroup),
		zap.String("area_id", room.HASSAreaID),
		zap.String("day_phase", dayPhase),
		zap.String("trigger", trigger))

	// Evaluate on/off conditions
	shouldTurnOn := m.evaluateOnConditions(room)
	shouldTurnOff := m.evaluateOffConditions(room)

	m.logger.Debug("Room evaluation result",
		zap.String("room", room.HueGroup),
		zap.Bool("should_turn_on", shouldTurnOn),
		zap.Bool("should_turn_off", shouldTurnOff))

	// If both are true, prioritize turning ON (matches Node-RED behavior)
	if shouldTurnOn {
		m.logger.Info("Room should be turned on with scene",
			zap.String("room", room.HueGroup),
			zap.String("day_phase", dayPhase))
		if shouldTurnOff {
			m.logger.Debug("ON takes precedence over OFF",
				zap.String("room", room.HueGroup))
		}
		m.activateScene(room, dayPhase, trigger)
		return
	}

	if shouldTurnOff {
		m.logger.Info("Room should be turned off",
			zap.String("room", room.HueGroup))
		m.turnOffRoom(room, trigger)
		return
	}

	m.logger.Debug("No action needed for room",
		zap.String("room", room.HueGroup))
}

// isTopicRelevant checks if a state variable change is relevant to a room's conditions
// Matches Node-RED behavior: dayPhase and sunevent always relevant, otherwise check if variable is used in conditions
func (m *Manager) isTopicRelevant(room *RoomConfig, trigger string) bool {
	// dayPhase and sunevent changes always affect all rooms (like "reset" in Node-RED)
	if trigger == "dayPhase" || trigger == "sunevent" || trigger == "" || trigger == "reset" {
		return true
	}

	// Check if trigger appears in any of the room's conditions
	allConditions := []string{}
	allConditions = append(allConditions, room.GetOnIfTrueConditions()...)
	allConditions = append(allConditions, room.GetOnIfFalseConditions()...)
	allConditions = append(allConditions, room.GetOffIfTrueConditions()...)
	allConditions = append(allConditions, room.GetOffIfFalseConditions()...)

	for _, condition := range allConditions {
		if condition == trigger {
			return true
		}
	}

	return false
}

// evaluateOnConditions evaluates whether a room should turn on
func (m *Manager) evaluateOnConditions(room *RoomConfig) bool {
	// Check on_if_true conditions
	onIfTrueConditions := room.GetOnIfTrueConditions()
	for _, condition := range onIfTrueConditions {
		if m.evaluateCondition(condition) {
			m.logger.Debug("on_if_true condition is true",
				zap.String("room", room.HueGroup),
				zap.String("condition", condition))
			return true
		}
	}

	// Check on_if_false conditions
	onIfFalseConditions := room.GetOnIfFalseConditions()
	for _, condition := range onIfFalseConditions {
		if !m.evaluateCondition(condition) {
			m.logger.Debug("on_if_false condition is false",
				zap.String("room", room.HueGroup),
				zap.String("condition", condition))
			return true
		}
	}

	return false
}

// evaluateOffConditions evaluates whether a room should turn off
func (m *Manager) evaluateOffConditions(room *RoomConfig) bool {
	// Check off_if_true conditions
	offIfTrueConditions := room.GetOffIfTrueConditions()
	for _, condition := range offIfTrueConditions {
		if m.evaluateCondition(condition) {
			m.logger.Debug("off_if_true condition is true",
				zap.String("room", room.HueGroup),
				zap.String("condition", condition))
			return true
		}
	}

	// Check off_if_false conditions
	offIfFalseConditions := room.GetOffIfFalseConditions()
	for _, condition := range offIfFalseConditions {
		if !m.evaluateCondition(condition) {
			m.logger.Debug("off_if_false condition is false",
				zap.String("room", room.HueGroup),
				zap.String("condition", condition))
			return true
		}
	}

	return false
}

// evaluateCondition evaluates a boolean state variable condition
func (m *Manager) evaluateCondition(condition string) bool {
	if condition == "" {
		return false
	}

	value, err := m.stateManager.GetBool(condition)
	if err != nil {
		m.logger.Warn("Failed to evaluate condition",
			zap.String("condition", condition),
			zap.Error(err))
		return false
	}

	return value
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

// activateScene activates a Hue scene for a room
func (m *Manager) activateScene(room *RoomConfig, dayPhase string, trigger string) {
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

	// Add transition if specified
	if room.TransitionSeconds != nil {
		serviceData["transition"] = *room.TransitionSeconds
	}

	// The Nook doesn't do well with dynamics because of its lights
	if room.HueGroup == "Nook" {
		serviceData["dynamic"] = false
	}

	// Call the service with the constructed entity ID
	err := m.haClient.CallService("scene", "turn_on", serviceData)
	if err != nil {
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
func (m *Manager) turnOffRoom(room *RoomConfig, trigger string) {
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

	err := m.haClient.CallService("light", "turn_off", serviceData)
	if err != nil {
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

// updateShadowInputs updates the current shadow state inputs
func (m *Manager) updateShadowInputs() {
	if m.inputHelper == nil {
		// Fall back to manual capture if no registry
		inputs := make(map[string]interface{})
		if val, err := m.stateManager.GetString("dayPhase"); err == nil {
			inputs["dayPhase"] = val
		}
		if val, err := m.stateManager.GetString("sunevent"); err == nil {
			inputs["sunevent"] = val
		}
		if val, err := m.stateManager.GetBool("isAnyoneHome"); err == nil {
			inputs["isAnyoneHome"] = val
		}
		if val, err := m.stateManager.GetBool("isTVPlaying"); err == nil {
			inputs["isTVPlaying"] = val
		}
		if val, err := m.stateManager.GetBool("isEveryoneAsleep"); err == nil {
			inputs["isEveryoneAsleep"] = val
		}
		if val, err := m.stateManager.GetBool("isMasterAsleep"); err == nil {
			inputs["isMasterAsleep"] = val
		}
		if val, err := m.stateManager.GetBool("isHaveGuests"); err == nil {
			inputs["isHaveGuests"] = val
		}
		m.shadowTracker.UpdateCurrentInputs(inputs)
		return
	}

	// Use automatic input capture
	inputs := m.inputHelper.CaptureInputs(m.pluginName)
	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// updateShadowInputsWithTrigger updates the current shadow state inputs including the trigger
func (m *Manager) updateShadowInputsWithTrigger(trigger string) {
	inputs := make(map[string]interface{})

	// Get all subscribed variables
	if val, err := m.stateManager.GetString("dayPhase"); err == nil {
		inputs["dayPhase"] = val
	}
	if val, err := m.stateManager.GetString("sunevent"); err == nil {
		inputs["sunevent"] = val
	}
	if val, err := m.stateManager.GetBool("isAnyoneHome"); err == nil {
		inputs["isAnyoneHome"] = val
	}
	if val, err := m.stateManager.GetBool("isTVPlaying"); err == nil {
		inputs["isTVPlaying"] = val
	}
	if val, err := m.stateManager.GetBool("isEveryoneAsleep"); err == nil {
		inputs["isEveryoneAsleep"] = val
	}
	if val, err := m.stateManager.GetBool("isMasterAsleep"); err == nil {
		inputs["isMasterAsleep"] = val
	}
	if val, err := m.stateManager.GetBool("isHaveGuests"); err == nil {
		inputs["isHaveGuests"] = val
	}

	// Add the trigger field
	inputs["trigger"] = trigger

	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// recordAction captures the current inputs and records an action in shadow state
func (m *Manager) recordAction(roomName string, actionType string, reason string, activeScene string, turnedOff bool, trigger string) {
	// First, update current inputs (includes trigger field)
	m.updateShadowInputsWithTrigger(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the action
	m.shadowTracker.RecordRoomAction(roomName, actionType, reason, activeScene, turnedOff)
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.LightingShadowState {
	return m.shadowTracker.GetState()
}
