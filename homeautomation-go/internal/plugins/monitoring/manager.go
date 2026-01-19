package monitoring

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Configuration constants
const (
	// Low battery threshold (percentage)
	DefaultLowBatteryThreshold = 20

	// Staleness threshold - sensor considered stale if no updates for this long
	DefaultStalenessThreshold = 24 * time.Hour

	// How often to check for stale sensors
	StalenessCheckInterval = 1 * time.Hour
)

// WaterLeakSensor represents a discovered water leak sensor
type WaterLeakSensor struct {
	EntityID     string
	DeviceID     string
	FriendlyName string
	State        string // "on" = leak, "off" = no leak
	LastChanged  time.Time
	// Track if we've sent notification for current leak
	NotificationSent bool
}

// BatterySensor represents a discovered battery sensor
type BatterySensor struct {
	EntityID         string
	DeviceID         string
	FriendlyName     string
	BatteryLevel     float64
	IsLow            bool
	IsUnavailable    bool
	LastReported     time.Time
	IsStale          bool
	NotificationSent bool // Track if we've sent notification for current low/stale state
}

// Manager handles monitoring for water leaks, low batteries, and stale sensors
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock
	ntfyClient   ntfy.Notifier

	// Shadow state tracking
	shadowTracker *shadowstate.MonitoringTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Discovered sensors
	mu               sync.Mutex
	waterLeakSensors map[string]*WaterLeakSensor
	batterySensors   map[string]*BatterySensor

	// Configuration
	lowBatteryThreshold int
	stalenessThreshold  time.Duration

	// Staleness checker control
	stopStalenessChecker chan struct{}
}

// NewManager creates a new monitoring manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier) *Manager {
	shadowTracker := shadowstate.NewMonitoringTracker()

	return &Manager{
		haClient:            haClient,
		stateManager:        stateManager,
		logger:              logger.Named("monitoring"),
		readOnly:            readOnly,
		clock:               clock.NewRealClock(),
		ntfyClient:          ntfyClient,
		shadowTracker:       shadowTracker,
		subHelper:           shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "monitoring", logger.Named("monitoring")),
		waterLeakSensors:    make(map[string]*WaterLeakSensor),
		batterySensors:      make(map[string]*BatterySensor),
		lowBatteryThreshold: DefaultLowBatteryThreshold,
		stalenessThreshold:  DefaultStalenessThreshold,
	}
}

// NewManagerWithClock creates a new monitoring manager with a custom clock (for testing)
func NewManagerWithClock(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier, c clock.Clock) *Manager {
	m := NewManager(haClient, stateManager, logger, readOnly, registry, ntfyClient)
	m.clock = c
	return m
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.MonitoringShadowState {
	return m.shadowTracker.GetState()
}

// Start begins monitoring
func (m *Manager) Start() error {
	m.logger.Info("Starting Monitoring Manager")

	// Discover water leak sensors
	if err := m.discoverWaterLeakSensors(); err != nil {
		m.logger.Warn("Failed to discover some water leak sensors", zap.Error(err))
	}

	m.mu.Lock()
	waterLeakCount := len(m.waterLeakSensors)
	m.mu.Unlock()

	if waterLeakCount == 0 {
		m.logger.Warn("No water leak sensors discovered")
	} else {
		m.logger.Info("Water leak sensors discovered", zap.Int("count", waterLeakCount))
	}

	// Discover battery sensors
	if err := m.discoverBatterySensors(); err != nil {
		m.logger.Warn("Failed to discover some battery sensors", zap.Error(err))
	}

	m.mu.Lock()
	batteryCount := len(m.batterySensors)
	m.mu.Unlock()

	if batteryCount == 0 {
		m.logger.Warn("No battery sensors discovered")
	} else {
		m.logger.Info("Battery sensors discovered", zap.Int("count", batteryCount))
		// Start periodic staleness checker
		m.startStalenessChecker()
	}

	// Record discovery time
	m.shadowTracker.SetLastDiscoveryRefresh(m.clock.Now())

	// Initialize shadow state with current input values
	m.subHelper.CaptureInitialInputs()

	// Update shadow state with discovered sensors
	m.updateShadowState()

	// Evaluate initial state (check for existing leaks or low batteries)
	m.evaluateWaterLeaks()
	m.evaluateLowBatteries()

	m.logger.Info("Monitoring Manager started successfully")
	return nil
}

// Stop stops the monitoring manager
func (m *Manager) Stop() {
	m.logger.Info("Stopping Monitoring Manager")

	// Stop staleness checker
	m.stopStalenessCheckerFunc()

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Monitoring Manager stopped")
}

// discoverWaterLeakSensors discovers all water leak binary sensors
func (m *Manager) discoverWaterLeakSensors() error {
	var errs []error

	// Get all entity states
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get entity states: %w", err)
	}

	// Filter for water leak sensors (binary_sensor with device_class: moisture or entity_id containing "water_leak")
	var waterLeakStates []*ha.State
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "binary_sensor.") {
			continue
		}
		// Check device_class first
		if deviceClass, ok := state.Attributes["device_class"].(string); ok {
			if deviceClass == "moisture" {
				waterLeakStates = append(waterLeakStates, state)
				continue
			}
		}
		// Also match by entity_id pattern
		if strings.Contains(strings.ToLower(state.EntityID), "water_leak") {
			waterLeakStates = append(waterLeakStates, state)
		}
	}

	m.logger.Info("Found water leak sensors", zap.Int("count", len(waterLeakStates)))

	if len(waterLeakStates) == 0 {
		return nil
	}

	// Get entity registry for device IDs
	entityRegistry, err := m.haClient.GetEntityRegistry()
	if err != nil {
		m.logger.Warn("Failed to get entity registry", zap.Error(err))
		errs = append(errs, err)
	}

	entityToDevice := make(map[string]string)
	for _, entry := range entityRegistry {
		entityToDevice[entry.EntityID] = entry.DeviceID
	}

	// Create sensor structs and subscribe
	m.mu.Lock()
	for _, state := range waterLeakStates {
		deviceID := entityToDevice[state.EntityID]

		friendlyName := state.EntityID
		if name, ok := state.Attributes["friendly_name"].(string); ok {
			friendlyName = name
		}

		sensor := &WaterLeakSensor{
			EntityID:     state.EntityID,
			DeviceID:     deviceID,
			FriendlyName: friendlyName,
			State:        state.State,
			LastChanged:  state.LastChanged,
		}

		m.waterLeakSensors[state.EntityID] = sensor

		m.logger.Debug("Discovered water leak sensor",
			zap.String("entity_id", state.EntityID),
			zap.String("friendly_name", friendlyName),
			zap.String("state", state.State))
	}
	m.mu.Unlock()

	// Subscribe to each sensor
	for entityID := range m.waterLeakSensors {
		if err := m.subHelper.SubscribeToEntity(entityID, m.handleWaterLeakChange); err != nil {
			m.logger.Warn("Failed to subscribe to water leak sensor",
				zap.String("entity_id", entityID),
				zap.Error(err))
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// discoverBatterySensors discovers all battery sensors
func (m *Manager) discoverBatterySensors() error {
	var errs []error

	// Get all entity states
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get entity states: %w", err)
	}

	// Filter for battery sensors (device_class: battery with %)
	var batteryStates []*ha.State
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "sensor.") {
			continue
		}
		// Check device_class
		deviceClass, hasDeviceClass := state.Attributes["device_class"].(string)
		if !hasDeviceClass || deviceClass != "battery" {
			continue
		}
		// Check unit_of_measurement
		uom, hasUOM := state.Attributes["unit_of_measurement"].(string)
		if !hasUOM || uom != "%" {
			continue
		}
		batteryStates = append(batteryStates, state)
	}

	m.logger.Info("Found battery sensors", zap.Int("count", len(batteryStates)))

	if len(batteryStates) == 0 {
		return nil
	}

	// Get entity registry for device IDs
	entityRegistry, err := m.haClient.GetEntityRegistry()
	if err != nil {
		m.logger.Warn("Failed to get entity registry", zap.Error(err))
		errs = append(errs, err)
	}

	entityToDevice := make(map[string]string)
	for _, entry := range entityRegistry {
		entityToDevice[entry.EntityID] = entry.DeviceID
	}

	// Create sensor structs and subscribe
	m.mu.Lock()
	for _, state := range batteryStates {
		deviceID := entityToDevice[state.EntityID]

		friendlyName := state.EntityID
		if name, ok := state.Attributes["friendly_name"].(string); ok {
			friendlyName = name
		}

		batteryLevel, err := parseBatteryLevel(state.State)
		isUnavailable := state.State == "unavailable" || state.State == "unknown"

		sensor := &BatterySensor{
			EntityID:      state.EntityID,
			DeviceID:      deviceID,
			FriendlyName:  friendlyName,
			BatteryLevel:  batteryLevel,
			IsLow:         err == nil && batteryLevel < float64(m.lowBatteryThreshold),
			IsUnavailable: isUnavailable,
			LastReported:  state.LastUpdated,
		}

		m.batterySensors[state.EntityID] = sensor

		m.logger.Debug("Discovered battery sensor",
			zap.String("entity_id", state.EntityID),
			zap.String("friendly_name", friendlyName),
			zap.Float64("battery_level", batteryLevel),
			zap.Bool("is_low", sensor.IsLow),
			zap.Bool("is_unavailable", isUnavailable))
	}
	m.mu.Unlock()

	// Subscribe to each sensor
	for entityID := range m.batterySensors {
		if err := m.subHelper.SubscribeToEntity(entityID, m.handleBatteryChange); err != nil {
			m.logger.Warn("Failed to subscribe to battery sensor",
				zap.String("entity_id", entityID),
				zap.Error(err))
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// handleWaterLeakChange processes water leak sensor state changes
func (m *Manager) handleWaterLeakChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.mu.Lock()
	sensor, ok := m.waterLeakSensors[entityID]
	if !ok {
		m.mu.Unlock()
		m.logger.Warn("Received update for unknown water leak sensor", zap.String("entity_id", entityID))
		return
	}

	oldSensorState := sensor.State
	sensor.State = newState.State
	sensor.LastChanged = newState.LastChanged

	// If leak is cleared, reset notification flag
	if newState.State == "off" && oldSensorState == "on" {
		sensor.NotificationSent = false
	}
	m.mu.Unlock()

	m.logger.Debug("Water leak sensor changed",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.String("old_state", oldSensorState),
		zap.String("new_state", newState.State))

	// Update shadow state
	m.updateShadowState()

	// Evaluate and potentially send notification
	m.evaluateWaterLeaks()
}

// handleBatteryChange processes battery sensor state changes
func (m *Manager) handleBatteryChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.mu.Lock()
	sensor, ok := m.batterySensors[entityID]
	if !ok {
		m.mu.Unlock()
		m.logger.Warn("Received update for unknown battery sensor", zap.String("entity_id", entityID))
		return
	}

	batteryLevel, err := parseBatteryLevel(newState.State)
	isUnavailable := newState.State == "unavailable" || newState.State == "unknown"

	wasLow := sensor.IsLow
	sensor.BatteryLevel = batteryLevel
	sensor.IsLow = err == nil && batteryLevel < float64(m.lowBatteryThreshold)
	sensor.IsUnavailable = isUnavailable
	sensor.LastReported = m.clock.Now()
	sensor.IsStale = false // Update received, not stale

	// If battery recovered above threshold, reset notification flag
	if !sensor.IsLow && wasLow {
		sensor.NotificationSent = false
	}
	m.mu.Unlock()

	m.logger.Debug("Battery sensor changed",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Float64("battery_level", batteryLevel),
		zap.Bool("is_low", sensor.IsLow))

	// Update shadow state
	m.updateShadowState()

	// Evaluate and potentially send notification
	m.evaluateLowBatteries()
}

// evaluateWaterLeaks checks for active water leaks and sends notifications
func (m *Manager) evaluateWaterLeaks() {
	m.mu.Lock()
	var activeLeaks []shadowstate.WaterLeakAlert
	var toNotify []*WaterLeakSensor

	for _, sensor := range m.waterLeakSensors {
		if sensor.State == "on" {
			alert := shadowstate.WaterLeakAlert{
				EntityID:         sensor.EntityID,
				FriendlyName:     sensor.FriendlyName,
				DetectedAt:       sensor.LastChanged,
				NotificationSent: sensor.NotificationSent,
			}
			activeLeaks = append(activeLeaks, alert)

			if !sensor.NotificationSent {
				toNotify = append(toNotify, sensor)
			}
		}
	}
	m.mu.Unlock()

	// Update shadow state with active leaks
	m.shadowTracker.UpdateActiveWaterLeaks(activeLeaks)

	// Send notifications for new leaks
	for _, sensor := range toNotify {
		m.sendWaterLeakNotification(sensor)
	}
}

// evaluateLowBatteries checks for low batteries and sends notifications
func (m *Manager) evaluateLowBatteries() {
	m.mu.Lock()
	var lowBatteryAlerts []shadowstate.LowBatteryAlert
	var toNotify []*BatterySensor

	for _, sensor := range m.batterySensors {
		if sensor.IsLow && !sensor.IsUnavailable {
			alert := shadowstate.LowBatteryAlert{
				EntityID:         sensor.EntityID,
				FriendlyName:     sensor.FriendlyName,
				BatteryLevel:     sensor.BatteryLevel,
				DetectedAt:       sensor.LastReported,
				NotificationSent: sensor.NotificationSent,
			}
			lowBatteryAlerts = append(lowBatteryAlerts, alert)

			if !sensor.NotificationSent {
				toNotify = append(toNotify, sensor)
			}
		}
	}
	m.mu.Unlock()

	// Update shadow state with low battery alerts
	m.shadowTracker.UpdateLowBatteryAlerts(lowBatteryAlerts)

	// Send notifications for new low batteries
	for _, sensor := range toNotify {
		m.sendLowBatteryNotification(sensor)
	}
}

// sendWaterLeakNotification sends a notification for a water leak
func (m *Manager) sendWaterLeakNotification(sensor *WaterLeakSensor) {
	message := fmt.Sprintf("Water leak detected at %s", sensor.FriendlyName)

	m.logger.Warn("Water leak detected",
		zap.String("entity_id", sensor.EntityID),
		zap.String("friendly_name", sensor.FriendlyName))

	// Record notification in shadow state
	m.shadowTracker.RecordNotification("water_leak", sensor.EntityID, message)

	// Mark as notified
	m.mu.Lock()
	sensor.NotificationSent = true
	m.mu.Unlock()

	if m.ntfyClient == nil {
		m.logger.Warn("ntfy client not configured, cannot send water leak notification",
			zap.String("entity_id", sensor.EntityID))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Water Leak Detected",
		Body:     message,
		Priority: ntfy.PriorityUrgent,
		Tags:     []string{"warning", "droplet"},
	}); err != nil {
		m.logger.Error("Failed to send water leak notification",
			zap.String("entity_id", sensor.EntityID),
			zap.Error(err))
	} else {
		m.logger.Info("Water leak notification sent", zap.String("message", message))
	}
}

// sendLowBatteryNotification sends a notification for a low battery
func (m *Manager) sendLowBatteryNotification(sensor *BatterySensor) {
	message := fmt.Sprintf("Low battery: %s at %.0f%%", sensor.FriendlyName, sensor.BatteryLevel)

	m.logger.Warn("Low battery detected",
		zap.String("entity_id", sensor.EntityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Float64("battery_level", sensor.BatteryLevel))

	// Record notification in shadow state
	m.shadowTracker.RecordNotification("low_battery", sensor.EntityID, message)

	// Mark as notified
	m.mu.Lock()
	sensor.NotificationSent = true
	m.mu.Unlock()

	if m.ntfyClient == nil {
		m.logger.Warn("ntfy client not configured, cannot send low battery notification",
			zap.String("entity_id", sensor.EntityID))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Low Battery",
		Body:     message,
		Priority: ntfy.PriorityDefault,
		Tags:     []string{"battery"},
	}); err != nil {
		m.logger.Error("Failed to send low battery notification",
			zap.String("entity_id", sensor.EntityID),
			zap.Error(err))
	} else {
		m.logger.Info("Low battery notification sent", zap.String("message", message))
	}
}

// sendStaleSensorNotification sends a notification for a stale sensor
func (m *Manager) sendStaleSensorNotification(sensor *BatterySensor) {
	duration := m.clock.Since(sensor.LastReported)
	message := fmt.Sprintf("Sensor %s has not reported for %.0f hours", sensor.FriendlyName, duration.Hours())

	m.logger.Warn("Stale sensor detected",
		zap.String("entity_id", sensor.EntityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Duration("since_last_report", duration))

	// Record notification in shadow state
	m.shadowTracker.RecordNotification("stale_sensor", sensor.EntityID, message)

	// Mark as notified
	m.mu.Lock()
	sensor.NotificationSent = true
	m.mu.Unlock()

	if m.ntfyClient == nil {
		m.logger.Warn("ntfy client not configured, cannot send stale sensor notification",
			zap.String("entity_id", sensor.EntityID))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Stale Sensor",
		Body:     message,
		Priority: ntfy.PriorityLow,
		Tags:     []string{"warning"},
	}); err != nil {
		m.logger.Error("Failed to send stale sensor notification",
			zap.String("entity_id", sensor.EntityID),
			zap.Error(err))
	} else {
		m.logger.Info("Stale sensor notification sent", zap.String("message", message))
	}
}

// startStalenessChecker starts the periodic staleness checker
func (m *Manager) startStalenessChecker() {
	m.mu.Lock()
	if m.stopStalenessChecker != nil {
		m.mu.Unlock()
		return // Already running
	}
	m.stopStalenessChecker = make(chan struct{})
	stopCh := m.stopStalenessChecker
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(StalenessCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				m.checkStaleSensors()
			}
		}
	}()
}

// stopStalenessCheckerFunc stops the staleness checker goroutine
func (m *Manager) stopStalenessCheckerFunc() {
	m.mu.Lock()
	if m.stopStalenessChecker != nil {
		close(m.stopStalenessChecker)
		m.stopStalenessChecker = nil
	}
	m.mu.Unlock()
}

// checkStaleSensors checks for stale battery sensors
func (m *Manager) checkStaleSensors() {
	m.mu.Lock()
	var staleAlerts []shadowstate.StaleSensorAlert
	var toNotify []*BatterySensor

	now := m.clock.Now()
	for _, sensor := range m.batterySensors {
		if sensor.IsUnavailable {
			continue // Skip unavailable sensors
		}

		timeSinceReport := now.Sub(sensor.LastReported)
		wasStale := sensor.IsStale
		sensor.IsStale = timeSinceReport > m.stalenessThreshold

		if sensor.IsStale {
			alert := shadowstate.StaleSensorAlert{
				EntityID:         sensor.EntityID,
				FriendlyName:     sensor.FriendlyName,
				LastReported:     sensor.LastReported,
				DetectedAt:       now,
				NotificationSent: sensor.NotificationSent,
			}
			staleAlerts = append(staleAlerts, alert)

			// Only notify if newly stale
			if !wasStale && !sensor.NotificationSent {
				toNotify = append(toNotify, sensor)
			}
		} else if wasStale {
			// Sensor recovered from stale state
			sensor.NotificationSent = false
		}
	}
	m.mu.Unlock()

	// Update shadow state with stale alerts
	m.shadowTracker.UpdateStaleSensorAlerts(staleAlerts)

	// Send notifications for newly stale sensors
	for _, sensor := range toNotify {
		m.sendStaleSensorNotification(sensor)
	}

	// Update shadow state
	m.updateShadowState()
}

// updateShadowState updates the shadow state with all current sensor data
func (m *Manager) updateShadowState() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update water leak sensors
	waterLeakData := make([]shadowstate.WaterLeakSensorData, 0, len(m.waterLeakSensors))
	for _, sensor := range m.waterLeakSensors {
		waterLeakData = append(waterLeakData, shadowstate.WaterLeakSensorData{
			EntityID:     sensor.EntityID,
			FriendlyName: sensor.FriendlyName,
			DeviceID:     sensor.DeviceID,
			State:        sensor.State,
			LastChanged:  sensor.LastChanged,
		})
	}
	m.shadowTracker.UpdateWaterLeakSensors(waterLeakData)

	// Update battery sensors
	batteryData := make([]shadowstate.BatterySensorData, 0, len(m.batterySensors))
	for _, sensor := range m.batterySensors {
		batteryData = append(batteryData, shadowstate.BatterySensorData{
			EntityID:      sensor.EntityID,
			FriendlyName:  sensor.FriendlyName,
			DeviceID:      sensor.DeviceID,
			BatteryLevel:  sensor.BatteryLevel,
			IsLow:         sensor.IsLow,
			LastChanged:   sensor.LastReported,
			LastReported:  sensor.LastReported,
			IsStale:       sensor.IsStale,
			IsUnavailable: sensor.IsUnavailable,
		})
	}
	m.shadowTracker.UpdateBatterySensors(batteryData)

	// Update current inputs with sensor states
	inputs := make(map[string]interface{})
	for entityID, sensor := range m.waterLeakSensors {
		inputs[entityID] = sensor.State
	}
	for entityID, sensor := range m.batterySensors {
		if !sensor.IsUnavailable {
			inputs[entityID] = fmt.Sprintf("%.1f", sensor.BatteryLevel)
		} else {
			inputs[entityID] = "unavailable"
		}
	}
	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// Reset re-evaluates monitoring conditions
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Monitoring - re-evaluating all conditions")

	// Re-evaluate water leaks
	m.evaluateWaterLeaks()

	// Re-evaluate low batteries
	m.evaluateLowBatteries()

	// Check for stale sensors
	m.checkStaleSensors()

	m.logger.Info("Successfully reset Monitoring")
	return nil
}

// parseBatteryLevel parses a battery level string into a float64
func parseBatteryLevel(s string) (float64, error) {
	if s == "unavailable" || s == "unknown" || s == "" {
		return 0, fmt.Errorf("invalid battery state: %s", s)
	}
	return strconv.ParseFloat(s, 64)
}

// ============================================================================
// Test Helpers - exported for testing only
// ============================================================================

// AddWaterLeakSensor adds a water leak sensor for testing
func (m *Manager) AddWaterLeakSensor(sensor *WaterLeakSensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waterLeakSensors[sensor.EntityID] = sensor
}

// AddBatterySensor adds a battery sensor for testing
func (m *Manager) AddBatterySensor(sensor *BatterySensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batterySensors[sensor.EntityID] = sensor
}

// SimulateWaterLeakChange simulates a water leak sensor state change for testing
func (m *Manager) SimulateWaterLeakChange(entityID, state string) {
	m.mu.Lock()
	sensor, ok := m.waterLeakSensors[entityID]
	if !ok {
		m.mu.Unlock()
		return
	}
	oldState := sensor.State
	sensor.State = state
	sensor.LastChanged = m.clock.Now()
	if state == "off" && oldState == "on" {
		sensor.NotificationSent = false
	}
	m.mu.Unlock()

	m.updateShadowState()
	m.evaluateWaterLeaks()
}

// SimulateBatteryChange simulates a battery sensor state change for testing
func (m *Manager) SimulateBatteryChange(entityID string, batteryLevel float64) {
	m.mu.Lock()
	sensor, ok := m.batterySensors[entityID]
	if !ok {
		m.mu.Unlock()
		return
	}
	wasLow := sensor.IsLow
	sensor.BatteryLevel = batteryLevel
	sensor.IsLow = batteryLevel < float64(m.lowBatteryThreshold)
	sensor.IsUnavailable = false
	sensor.LastReported = m.clock.Now()
	sensor.IsStale = false
	if !sensor.IsLow && wasLow {
		sensor.NotificationSent = false
	}
	m.mu.Unlock()

	m.updateShadowState()
	m.evaluateLowBatteries()
}

// GetWaterLeakSensors returns all water leak sensors for testing
func (m *Manager) GetWaterLeakSensors() map[string]*WaterLeakSensor {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*WaterLeakSensor, len(m.waterLeakSensors))
	for k, v := range m.waterLeakSensors {
		copy := *v
		result[k] = &copy
	}
	return result
}

// GetBatterySensors returns all battery sensors for testing
func (m *Manager) GetBatterySensors() map[string]*BatterySensor {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*BatterySensor, len(m.batterySensors))
	for k, v := range m.batterySensors {
		copy := *v
		result[k] = &copy
	}
	return result
}

// GetActiveWaterLeakCount returns the count of active water leaks for testing
func (m *Manager) GetActiveWaterLeakCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, sensor := range m.waterLeakSensors {
		if sensor.State == "on" {
			count++
		}
	}
	return count
}

// GetLowBatteryCount returns the count of low battery sensors for testing
func (m *Manager) GetLowBatteryCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, sensor := range m.batterySensors {
		if sensor.IsLow && !sensor.IsUnavailable {
			count++
		}
	}
	return count
}
