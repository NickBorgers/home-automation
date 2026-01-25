package sensorhealth

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

	// Temperature sensor lockup detection
	// A sensor is considered locked up if it has the same reading for this long
	TemperatureLockupThreshold = 12 * time.Hour
	// How often to check for locked up sensors
	TemperatureLockupCheckInterval = 1 * time.Hour
	// Rate limiting for lockup notifications (per sensor)
	TemperatureLockupNotificationRateLimit = 24 * time.Hour
)

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

// TemperatureSensor represents a discovered temperature sensor for lockup monitoring
type TemperatureSensor struct {
	EntityID                 string
	DeviceID                 string
	FriendlyName             string
	Value                    float64
	Valid                    bool
	LastValueChange          time.Time // When the value last changed
	LastValue                float64   // Previous value for comparison
	IsLockedUp               bool      // Whether sensor is currently in lockup state
	LastNotification         time.Time // When we last notified about this sensor's lockup
	LastRecoveryNotification time.Time // When we last notified about this sensor's recovery
}

// Manager handles sensor health monitoring: low batteries, stale sensors, and temperature lockup
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock
	ntfyClient   ntfy.Notifier

	// Shadow state tracking
	shadowTracker *shadowstate.SensorHealthTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Discovered sensors
	mu             sync.Mutex
	batterySensors map[string]*BatterySensor
	tempSensors    map[string]*TemperatureSensor

	// Configuration
	lowBatteryThreshold int
	stalenessThreshold  time.Duration

	// Staleness checker control
	stopStalenessChecker chan struct{}

	// Temperature lockup checker control
	stopLockupChecker chan struct{}
}

// NewManager creates a new sensor health manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier) *Manager {
	shadowTracker := shadowstate.NewSensorHealthTracker()

	return &Manager{
		haClient:            haClient,
		stateManager:        stateManager,
		logger:              logger.Named("sensorhealth"),
		readOnly:            readOnly,
		clock:               clock.NewRealClock(),
		ntfyClient:          ntfyClient,
		shadowTracker:       shadowTracker,
		subHelper:           shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "sensorhealth", logger.Named("sensorhealth")),
		batterySensors:      make(map[string]*BatterySensor),
		tempSensors:         make(map[string]*TemperatureSensor),
		lowBatteryThreshold: DefaultLowBatteryThreshold,
		stalenessThreshold:  DefaultStalenessThreshold,
	}
}

// NewManagerWithClock creates a new sensor health manager with a custom clock (for testing)
func NewManagerWithClock(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier, c clock.Clock) *Manager {
	m := NewManager(haClient, stateManager, logger, readOnly, registry, ntfyClient)
	m.clock = c
	return m
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.SensorHealthShadowState {
	return m.shadowTracker.GetState()
}

// Start begins sensor health monitoring
func (m *Manager) Start() error {
	m.logger.Info("Starting Sensor Health Manager")

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

	// Discover temperature sensors for lockup detection
	if err := m.discoverTemperatureSensors(); err != nil {
		m.logger.Warn("Failed to discover some temperature sensors", zap.Error(err))
	}

	m.mu.Lock()
	tempSensorCount := len(m.tempSensors)
	m.mu.Unlock()

	if tempSensorCount == 0 {
		m.logger.Warn("No temperature sensors discovered - lockup detection will be inactive")
	} else {
		m.logger.Info("Temperature sensors discovered for lockup monitoring",
			zap.Int("total", tempSensorCount))
		// Start the periodic lockup checker
		m.startLockupChecker()
	}

	// Record discovery time
	m.shadowTracker.SetLastDiscoveryRefresh(m.clock.Now())

	// Initialize shadow state with current input values
	m.subHelper.CaptureInitialInputs()

	// Update shadow state with discovered sensors
	m.updateShadowState()

	// Evaluate initial state (check for existing low batteries)
	m.evaluateLowBatteries()

	m.logger.Info("Sensor Health Manager started successfully")
	return nil
}

// Stop stops the sensor health manager
func (m *Manager) Stop() {
	m.logger.Info("Stopping Sensor Health Manager")

	// Stop staleness checker
	m.stopStalenessCheckerFunc()

	// Stop lockup checker
	m.stopLockupCheckerFunc()

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Sensor Health Manager stopped")
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

	// Get device registry for label checking
	devices, err := m.haClient.GetDevices()
	if err != nil {
		m.logger.Warn("Failed to get device registry", zap.Error(err))
		errs = append(errs, err)
	}

	labelChecker := ha.NewDeviceLabelChecker(devices)

	// Create sensor structs and subscribe
	m.mu.Lock()
	for _, state := range batteryStates {
		deviceID := entityToDevice[state.EntityID]

		// Check if device has the monitoring ignore label
		if labelChecker.ShouldIgnoreForMonitoring(deviceID) {
			m.logger.Info("Skipping battery sensor with monitoring_ignore label on device",
				zap.String("entity_id", state.EntityID),
				zap.String("device_id", deviceID))
			continue
		}

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

// discoverTemperatureSensors discovers all temperature sensors for lockup monitoring.
func (m *Manager) discoverTemperatureSensors() error {
	var errs []error

	// Get all entity states
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get entity states: %w", err)
	}

	// Filter for temperature sensors (device_class: temperature)
	var tempSensors []*ha.State
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "sensor.") {
			continue
		}
		if deviceClass, ok := state.Attributes["device_class"].(string); ok {
			if deviceClass == "temperature" {
				tempSensors = append(tempSensors, state)
			}
		}
	}

	m.logger.Info("Found temperature sensors", zap.Int("count", len(tempSensors)))

	if len(tempSensors) == 0 {
		return nil
	}

	// Get entity registry to map entity_id -> device_id
	entityRegistry, err := m.haClient.GetEntityRegistry()
	if err != nil {
		m.logger.Warn("Failed to get entity registry for temperature sensors",
			zap.Error(err))
		errs = append(errs, err)
	}

	entityToDevice := make(map[string]string)
	for _, entry := range entityRegistry {
		entityToDevice[entry.EntityID] = entry.DeviceID
	}

	// Get device registry for label checking
	devices, err := m.haClient.GetDevices()
	if err != nil {
		m.logger.Warn("Failed to get device registry for temperature sensors",
			zap.Error(err))
		errs = append(errs, err)
	}

	labelChecker := ha.NewDeviceLabelChecker(devices)

	now := m.clock.Now()

	// Create TemperatureSensor structs and subscribe to each
	m.mu.Lock()
	for _, state := range tempSensors {
		deviceID := entityToDevice[state.EntityID]

		// Check if device has the monitoring ignore label
		if labelChecker.ShouldIgnoreForMonitoring(deviceID) {
			m.logger.Info("Skipping temperature sensor with monitoring_ignore label on device",
				zap.String("entity_id", state.EntityID),
				zap.String("device_id", deviceID))
			continue
		}

		friendlyName := state.EntityID
		if name, ok := state.Attributes["friendly_name"].(string); ok {
			friendlyName = name
		}

		sensor := &TemperatureSensor{
			EntityID:        state.EntityID,
			DeviceID:        deviceID,
			FriendlyName:    friendlyName,
			Valid:           false,
			LastValueChange: now, // Initialize to now - we'll track from here
		}

		// Parse initial value
		if value, err := parseTemperature(state.State); err == nil {
			sensor.Value = value
			sensor.LastValue = value
			sensor.Valid = true
		}

		m.tempSensors[state.EntityID] = sensor

		m.logger.Debug("Discovered temperature sensor",
			zap.String("entity_id", state.EntityID),
			zap.String("friendly_name", friendlyName),
			zap.String("device_id", deviceID),
			zap.Float64("current_value", sensor.Value))
	}
	m.mu.Unlock()

	// Subscribe to each sensor (outside the lock to avoid deadlock)
	for entityID := range m.tempSensors {
		if err := m.subHelper.SubscribeToEntity(entityID, m.handleTemperatureChange); err != nil {
			m.logger.Warn("Failed to subscribe to temperature sensor",
				zap.String("entity_id", entityID),
				zap.Error(err))
			errs = append(errs, err)
		}
	}

	// Update shadow state with all discovered temperature sensors
	m.updateShadowTemperatureSensors()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
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

// handleTemperatureChange processes changes to any temperature sensor
func (m *Manager) handleTemperatureChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Parse temperature value
	temp, err := parseTemperature(newState.State)
	if err != nil {
		m.logger.Debug("Invalid temperature value",
			zap.String("entity_id", entityID),
			zap.String("state", newState.State),
			zap.Error(err))
		m.mu.Lock()
		if sensor, ok := m.tempSensors[entityID]; ok {
			sensor.Valid = false
		}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	sensor, ok := m.tempSensors[entityID]
	if !ok {
		m.mu.Unlock()
		m.logger.Warn("Received update for unknown temperature sensor", zap.String("entity_id", entityID))
		return
	}

	// Check if value actually changed
	if sensor.Valid && temp != sensor.LastValue {
		// Value changed - update tracking
		sensor.LastValueChange = m.clock.Now()
		sensor.LastValue = temp
		// If sensor was marked as locked up but now has a new reading, it's recovered
		if sensor.IsLockedUp {
			sensor.IsLockedUp = false
			m.logger.Info("Temperature sensor recovered from lockup",
				zap.String("entity_id", entityID),
				zap.String("friendly_name", sensor.FriendlyName),
				zap.Float64("new_value", temp))
			// Send recovery notification (unlocks mutex internally)
			m.mu.Unlock()
			m.sendTemperatureRecoveryNotification(sensor, temp)
			m.mu.Lock()
		}
	}

	sensor.Value = temp
	sensor.Valid = true
	m.mu.Unlock()

	m.logger.Debug("Temperature sensor changed",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Float64("temperature", temp))

	// Update shadow state
	m.updateShadowTemperatureSensors()
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

// startLockupChecker starts the periodic lockup checker goroutine
func (m *Manager) startLockupChecker() {
	m.mu.Lock()
	m.stopLockupChecker = make(chan struct{})
	stopCh := m.stopLockupChecker
	m.mu.Unlock()

	go m.runLockupChecker(stopCh)
}

// stopLockupCheckerFunc stops the temperature lockup checker goroutine
func (m *Manager) stopLockupCheckerFunc() {
	m.mu.Lock()
	if m.stopLockupChecker != nil {
		close(m.stopLockupChecker)
		m.stopLockupChecker = nil
	}
	m.mu.Unlock()
}

// runLockupChecker periodically checks for locked up temperature sensors
func (m *Manager) runLockupChecker(stopCh chan struct{}) {
	// Check immediately on start
	m.checkTemperatureLockup()

	ticker := m.clock.NewTicker(TemperatureLockupCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			m.logger.Info("Temperature lockup checker stopped")
			return
		case <-ticker.C():
			m.checkTemperatureLockup()
		}
	}
}

// checkTemperatureLockup checks all temperature sensors for lockup and sends notifications
func (m *Manager) checkTemperatureLockup() {
	m.mu.Lock()
	now := m.clock.Now()
	var lockedUpSensors []*TemperatureSensor

	for _, sensor := range m.tempSensors {
		if !sensor.Valid {
			continue
		}

		// Check if sensor has had the same value for too long
		timeSinceChange := now.Sub(sensor.LastValueChange)
		if timeSinceChange >= TemperatureLockupThreshold {
			sensor.IsLockedUp = true
			lockedUpSensors = append(lockedUpSensors, sensor)
		}
	}
	m.mu.Unlock()

	// Update shadow state with lockup status
	m.updateShadowTemperatureSensors()

	// Send notifications for locked up sensors
	for _, sensor := range lockedUpSensors {
		m.sendTemperatureLockupNotification(sensor)
	}
}

// sendTemperatureLockupNotification sends a notification for a locked up temperature sensor
func (m *Manager) sendTemperatureLockupNotification(sensor *TemperatureSensor) {
	m.mu.Lock()
	now := m.clock.Now()

	// Check rate limiting (per sensor)
	if !sensor.LastNotification.IsZero() && now.Sub(sensor.LastNotification) < TemperatureLockupNotificationRateLimit {
		m.mu.Unlock()
		m.logger.Debug("Skipping lockup notification due to rate limit",
			zap.String("entity_id", sensor.EntityID),
			zap.Duration("time_since_last", now.Sub(sensor.LastNotification)))
		return
	}

	// Update last notification time
	sensor.LastNotification = now

	// Capture values while holding the lock to avoid race conditions
	lastValueChange := sensor.LastValueChange
	sensorValue := sensor.Value
	friendlyName := sensor.FriendlyName
	entityID := sensor.EntityID
	m.mu.Unlock()

	// Calculate how long the sensor has been locked up
	timeSinceChange := now.Sub(lastValueChange)
	hoursLocked := int(timeSinceChange.Hours())

	message := fmt.Sprintf("Temperature sensor '%s' appears to be locked up. "+
		"It has reported the same value (%.1f) for %d+ hours. "+
		"The sensor may need to be reset or replaced.",
		friendlyName, sensorValue, hoursLocked)

	m.logger.Info("Sending temperature lockup notification",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", friendlyName),
		zap.Float64("stuck_value", sensorValue),
		zap.Int("hours_locked", hoursLocked))

	// Record in shadow state
	m.shadowTracker.RecordTemperatureLockupNotification(entityID, friendlyName, message)

	if m.readOnly {
		m.logger.Info("Skipping lockup notification send in read-only mode",
			zap.String("entity_id", entityID),
			zap.String("message", message))
		return
	}

	// Check if ntfy client is configured
	if m.ntfyClient == nil {
		m.logger.Warn("Cannot send temperature lockup notification - ntfy client not configured",
			zap.String("entity_id", entityID))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Temperature Sensor Locked Up",
		Body:     message,
		Priority: ntfy.PriorityHigh,
		Tags:     []string{"warning", "thermometer"},
	}); err != nil {
		m.logger.Error("Failed to send temperature lockup notification",
			zap.String("entity_id", entityID),
			zap.Error(err))
	}
}

// sendTemperatureRecoveryNotification sends a notification when a temperature sensor recovers from lockup
func (m *Manager) sendTemperatureRecoveryNotification(sensor *TemperatureSensor, newValue float64) {
	m.mu.Lock()
	now := m.clock.Now()

	// Check rate limiting (per sensor) - use same rate limit as lockup notifications
	if !sensor.LastRecoveryNotification.IsZero() && now.Sub(sensor.LastRecoveryNotification) < TemperatureLockupNotificationRateLimit {
		m.mu.Unlock()
		m.logger.Debug("Skipping recovery notification due to rate limit",
			zap.String("entity_id", sensor.EntityID),
			zap.Duration("time_since_last", now.Sub(sensor.LastRecoveryNotification)))
		return
	}

	// Update last recovery notification time
	sensor.LastRecoveryNotification = now
	m.mu.Unlock()

	message := fmt.Sprintf("Temperature sensor '%s' has recovered from lockup. "+
		"It is now reporting a new value (%.1f). The sensor appears to be working again.",
		sensor.FriendlyName, newValue)

	m.logger.Info("Sending temperature recovery notification",
		zap.String("entity_id", sensor.EntityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Float64("new_value", newValue))

	// Record in shadow state
	m.shadowTracker.RecordTemperatureRecoveryNotification(sensor.EntityID, sensor.FriendlyName, message)

	if m.readOnly {
		m.logger.Info("Skipping recovery notification send in read-only mode",
			zap.String("entity_id", sensor.EntityID),
			zap.String("message", message))
		return
	}

	// Check if ntfy client is configured
	if m.ntfyClient == nil {
		m.logger.Warn("Cannot send temperature recovery notification - ntfy client not configured",
			zap.String("entity_id", sensor.EntityID))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Temperature Sensor Recovered",
		Body:     message,
		Priority: ntfy.PriorityDefault,
		Tags:     []string{"white_check_mark", "thermometer"},
	}); err != nil {
		m.logger.Error("Failed to send temperature recovery notification",
			zap.String("entity_id", sensor.EntityID),
			zap.Error(err))
	}
}

// updateShadowState updates the shadow state with all current sensor data
func (m *Manager) updateShadowState() {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	for entityID, sensor := range m.batterySensors {
		if !sensor.IsUnavailable {
			inputs[entityID] = fmt.Sprintf("%.1f", sensor.BatteryLevel)
		} else {
			inputs[entityID] = "unavailable"
		}
	}
	for entityID, sensor := range m.tempSensors {
		if sensor.Valid {
			inputs[entityID] = fmt.Sprintf("%.1f", sensor.Value)
		} else {
			inputs[entityID] = "unavailable"
		}
	}
	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// updateShadowTemperatureSensors updates the shadow state with current temperature sensor data
func (m *Manager) updateShadowTemperatureSensors() {
	m.mu.Lock()
	defer m.mu.Unlock()

	sensorData := make([]shadowstate.TemperatureSensorData, 0, len(m.tempSensors))
	for _, sensor := range m.tempSensors {
		sensorData = append(sensorData, shadowstate.TemperatureSensorData{
			EntityID:        sensor.EntityID,
			FriendlyName:    sensor.FriendlyName,
			DeviceID:        sensor.DeviceID,
			Value:           sensor.Value,
			Valid:           sensor.Valid,
			LastValueChange: sensor.LastValueChange,
			IsLockedUp:      sensor.IsLockedUp,
		})
	}

	m.shadowTracker.UpdateTemperatureSensors(sensorData)
}

// Reset re-evaluates sensor health conditions
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Sensor Health - re-evaluating all conditions")

	// Re-evaluate low batteries
	m.evaluateLowBatteries()

	// Check for stale sensors
	m.checkStaleSensors()

	// Check for temperature lockups
	m.checkTemperatureLockup()

	m.logger.Info("Successfully reset Sensor Health")
	return nil
}

// parseBatteryLevel parses a battery level string into a float64
func parseBatteryLevel(s string) (float64, error) {
	if s == "unavailable" || s == "unknown" || s == "" {
		return 0, fmt.Errorf("invalid battery state: %s", s)
	}
	return strconv.ParseFloat(s, 64)
}

// parseTemperature parses a temperature value from a state string
func parseTemperature(s string) (float64, error) {
	if s == "" || s == "unknown" || s == "unavailable" {
		return 0, fmt.Errorf("invalid temperature state: %s", s)
	}
	return strconv.ParseFloat(s, 64)
}

// ============================================================================
// Test Helpers - exported for testing only
// ============================================================================

// AddBatterySensor adds a battery sensor for testing
func (m *Manager) AddBatterySensor(sensor *BatterySensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batterySensors[sensor.EntityID] = sensor
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

// GetTemperatureSensors returns a copy of the current temperature sensors map (for testing)
func (m *Manager) GetTemperatureSensors() map[string]*TemperatureSensor {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*TemperatureSensor)
	for k, v := range m.tempSensors {
		// Make a copy
		sensor := *v
		result[k] = &sensor
	}
	return result
}

// AddTemperatureSensor adds a temperature sensor directly (for testing)
func (m *Manager) AddTemperatureSensor(sensor *TemperatureSensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tempSensors[sensor.EntityID] = sensor
}

// SimulateTemperatureChange simulates a temperature sensor value change (for testing)
func (m *Manager) SimulateTemperatureChange(entityID string, temperature float64) {
	m.handleTemperatureChange(entityID, nil, &ha.State{
		EntityID: entityID,
		State:    fmt.Sprintf("%.1f", temperature),
	})
}

// TriggerLockupCheck manually triggers the lockup check (for testing)
func (m *Manager) TriggerLockupCheck() {
	m.checkTemperatureLockup()
}
