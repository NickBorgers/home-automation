package energy

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// CalibrationState represents the current calibration phase for a device
type CalibrationState int

const (
	// CalibrationStateNormal - normal operation, using baseline lux for brightness
	CalibrationStateNormal CalibrationState = iota
	// CalibrationStateDimmed - LED has been dimmed, waiting for fresh lux reading
	CalibrationStateDimmed
)

// Manager handles energy state calculations and updates
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	config       *EnergyConfig
	logger       *zap.Logger
	readOnly     bool
	timezone     *time.Location

	// Control for free energy checker
	stopChecker chan struct{}

	// Control for baseline calibration
	stopCalibration chan struct{}

	// Startup synchronization for tests - signals when async goroutines complete initial work
	startupWg sync.WaitGroup

	// Shadow state tracking
	shadowTracker *shadowstate.EnergyTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Mutex protecting indicator light and lux sensor state
	indicatorMu sync.RWMutex
	// Discovered indicator light entities (Apollo sensors with "Radar" in friendly_name)
	indicatorLightEntities []string

	// Lux sensor tracking (protected by indicatorMu)
	// Maps light entity ID -> lux sensor entity ID
	lightToLuxSensor map[string]string
	// Maps lux sensor entity ID -> current lux value
	currentLuxValues map[string]float64
	// Maps light entity ID -> last brightness update time (for debouncing)
	lastBrightnessUpdate map[string]time.Time
	// Maps light entity ID -> last brightness percentage (for hysteresis)
	lastBrightnessLevel map[string]int

	// Baseline calibration tracking (protected by indicatorMu)
	// Maps light entity ID -> baseline lux value (true ambient light, measured with LED dimmed)
	baselineLuxValues map[string]float64
	// Maps light entity ID -> time of last successful calibration
	lastCalibrationTime map[string]time.Time
	// Maps light entity ID -> current calibration state
	calibrationState map[string]CalibrationState
}

// NewManager creates a new Energy State manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, config *EnergyConfig, logger *zap.Logger, readOnly bool, timezone *time.Location, registry *shadowstate.SubscriptionRegistry) *Manager {
	// Default to UTC if no timezone provided
	if timezone == nil {
		timezone = time.UTC
	}

	shadowTracker := shadowstate.NewEnergyTracker()

	m := &Manager{
		haClient:             haClient,
		stateManager:         stateManager,
		config:               config,
		logger:               logger.Named("energy"),
		readOnly:             readOnly,
		timezone:             timezone,
		stopChecker:          make(chan struct{}),
		stopCalibration:      make(chan struct{}),
		shadowTracker:        shadowTracker,
		subHelper:            shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "energy", logger.Named("energy")),
		lightToLuxSensor:     make(map[string]string),
		currentLuxValues:     make(map[string]float64),
		lastBrightnessUpdate: make(map[string]time.Time),
		lastBrightnessLevel:  make(map[string]int),
		baselineLuxValues:    make(map[string]float64),
		lastCalibrationTime:  make(map[string]time.Time),
		calibrationState:     make(map[string]CalibrationState),
	}

	return m
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.EnergyShadowState {
	return m.shadowTracker.GetState()
}

// Start begins monitoring energy state
func (m *Manager) Start() error {
	m.logger.Info("Starting Energy State Manager")

	// Subscribe to battery level changes (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToSensor("sensor.span_panel_span_storage_battery_percentage_2", m.handleBatteryChange); err != nil {
		return fmt.Errorf("failed to subscribe to battery sensor: %w", err)
	}

	// Subscribe to this hour solar generation
	if err := m.subHelper.SubscribeToSensor("sensor.energy_next_hour", m.handleThisHourSolarChange); err != nil {
		return fmt.Errorf("failed to subscribe to this hour solar sensor: %w", err)
	}

	// Subscribe to remaining solar generation
	if err := m.subHelper.SubscribeToSensor("sensor.energy_production_today_remaining", m.handleRemainingSolarChange); err != nil {
		return fmt.Errorf("failed to subscribe to remaining solar sensor: %w", err)
	}

	// Subscribe to grid availability changes
	if err := m.subHelper.SubscribeToState("isGridAvailable", m.handleGridAvailabilityChange); err != nil {
		return fmt.Errorf("failed to subscribe to grid availability: %w", err)
	}

	// Subscribe to battery and solar energy level changes to recalculate overall level
	if err := m.subHelper.SubscribeToState("batteryEnergyLevel", m.handleIntermediateLevelChange); err != nil {
		return fmt.Errorf("failed to subscribe to battery energy level: %w", err)
	}

	if err := m.subHelper.SubscribeToState("solarProductionEnergyLevel", m.handleIntermediateLevelChange); err != nil {
		return fmt.Errorf("failed to subscribe to solar production energy level: %w", err)
	}

	if err := m.subHelper.SubscribeToState("isFreeEnergyAvailable", m.handleIntermediateLevelChange); err != nil {
		return fmt.Errorf("failed to subscribe to free energy available: %w", err)
	}

	// Discover indicator light entities (Apollo sensors) BEFORE starting the
	// free energy checker goroutine. This ensures indicatorLightEntities is
	// populated before any state change handlers call updateIndicatorLights().
	m.discoverIndicatorLights()

	// Discover lux sensors and associate with indicator lights (for adaptive brightness)
	m.discoverLuxSensors()

	// Subscribe to lux sensor changes (if adaptive brightness is enabled)
	if err := m.subscribeLuxSensors(); err != nil {
		return fmt.Errorf("failed to subscribe to lux sensors: %w", err)
	}

	// Start free energy check timer (check every minute)
	m.startupWg.Add(1)
	go m.runFreeEnergyChecker()

	// Start baseline calibration if enabled
	if m.config.Energy.IndicatorLights.AdaptiveBrightness.BaselineCalibration.Enabled {
		m.startupWg.Add(1)
		go m.runBaselineCalibration()
	}

	// Capture initial shadow state inputs after all subscriptions are registered
	m.captureInitialInputs()

	m.logger.Info("Energy State Manager started successfully")
	return nil
}

// captureInitialInputs captures the current input values at startup.
// This ensures shadow state shows inputs immediately, not just after first event.
func (m *Manager) captureInitialInputs() {
	if m.subHelper == nil {
		return
	}
	// The SubscriptionHelper automatically captures inputs before each handler,
	// but we need to capture them once on startup so the shadow state isn't empty
	// until the first event fires.
	m.subHelper.CaptureInitialInputs()
}

// Stop stops the Energy State Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Energy State Manager")

	// Stop the free energy checker goroutine
	close(m.stopChecker)

	// Stop the baseline calibration goroutine if enabled
	if m.config.Energy.IndicatorLights.AdaptiveBrightness.BaselineCalibration.Enabled {
		close(m.stopCalibration)
	}

	// Unsubscribe from all subscriptions via helper
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Energy State Manager stopped")
}

// WaitForStartup blocks until all startup goroutines have completed their initial work.
// This is primarily intended for use in tests to avoid arbitrary time.Sleep() calls.
// In production code, callers typically don't need to wait for startup.
func (m *Manager) WaitForStartup() {
	m.startupWg.Wait()
}

// handleBatteryChange processes battery percentage changes
func (m *Manager) handleBatteryChange(percentage float64) {
	m.logger.Info("Battery level changed",
		zap.Float64("percentage", percentage))

	// Validate percentage is finite
	if math.IsNaN(percentage) || math.IsInf(percentage, 0) {
		m.logger.Warn("Battery percentage is not finite, ignoring",
			zap.Float64("percentage", percentage))
		return
	}

	// Update shadow state sensor reading for battery
	m.shadowTracker.UpdateBatteryPercentage(percentage)

	// Determine battery energy level
	level := m.determineBatteryEnergyLevel(percentage)
	if level == "" {
		m.logger.Warn("No battery energy level determined",
			zap.Float64("percentage", percentage))
		return
	}

	m.logger.Info("Determined battery energy level",
		zap.Float64("percentage", percentage),
		zap.String("level", level))

	// Update state variable
	if err := m.stateManager.SetString("batteryEnergyLevel", level); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping batteryEnergyLevel update in read-only mode",
				zap.String("level", level))
		} else {
			m.logger.Error("Failed to set batteryEnergyLevel", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateBatteryLevel(level)
}

// handleThisHourSolarChange processes this hour solar generation changes
func (m *Manager) handleThisHourSolarChange(kw float64) {
	m.logger.Info("This hour solar generation changed",
		zap.Float64("kw", kw))

	// Validate kw is finite
	if math.IsNaN(kw) || math.IsInf(kw, 0) {
		m.logger.Warn("This hour solar generation is not finite, ignoring",
			zap.Float64("kw", kw))
		return
	}

	// Update shadow state sensor reading
	m.shadowTracker.UpdateThisHourSolarKW(kw)

	// Update state variable
	if err := m.stateManager.SetNumber("thisHourSolarGeneration", kw); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping thisHourSolarGeneration update in read-only mode",
				zap.Float64("kw", kw))
		} else {
			m.logger.Error("Failed to set thisHourSolarGeneration", zap.Error(err))
		}
	}

	// Trigger recalculation
	m.recalculateSolarProductionLevel()
}

// handleRemainingSolarChange processes remaining solar generation changes
func (m *Manager) handleRemainingSolarChange(kwh float64) {
	m.logger.Info("Remaining solar generation changed",
		zap.Float64("kwh", kwh))

	// Validate kwh is finite
	if math.IsNaN(kwh) || math.IsInf(kwh, 0) {
		m.logger.Warn("Remaining solar generation is not finite, ignoring",
			zap.Float64("kwh", kwh))
		return
	}

	// Update shadow state sensor reading
	m.shadowTracker.UpdateRemainingSolarKWH(kwh)

	// Update state variable
	if err := m.stateManager.SetNumber("remainingSolarGeneration", kwh); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping remainingSolarGeneration update in read-only mode",
				zap.Float64("kwh", kwh))
		} else {
			m.logger.Error("Failed to set remainingSolarGeneration", zap.Error(err))
		}
	}

	// Trigger recalculation
	m.recalculateSolarProductionLevel()
}

// handleGridAvailabilityChange processes grid availability changes
func (m *Manager) handleGridAvailabilityChange(key string, oldValue, newValue interface{}) {
	m.logger.Info("Grid availability changed",
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Sync grid availability to Home Assistant
	// Convert newValue to bool
	gridAvailable, ok := newValue.(bool)
	if !ok {
		m.logger.Error("Grid availability value is not a boolean",
			zap.Any("value", newValue))
		return
	}

	// Update shadow state sensor reading
	m.shadowTracker.UpdateGridAvailable(gridAvailable)

	// Skip HA sync in read-only mode
	if m.readOnly {
		m.logger.Debug("Skipping grid availability sync in read-only mode",
			zap.Bool("grid_available", gridAvailable))
	} else {
		// Explicitly sync to Home Assistant to ensure bidirectional consistency
		if err := m.haClient.SetInputBoolean("grid_available", gridAvailable); err != nil {
			m.logger.Error("Failed to sync grid availability to Home Assistant",
				zap.Bool("grid_available", gridAvailable),
				zap.Error(err))
			// Continue processing even if sync fails
		} else {
			m.logger.Debug("Successfully synced grid availability to Home Assistant",
				zap.Bool("grid_available", gridAvailable))
		}
	}

	// Trigger free energy recalculation
	m.checkFreeEnergy()
}

// handleIntermediateLevelChange recalculates overall energy level when intermediate levels change
func (m *Manager) handleIntermediateLevelChange(key string, oldValue, newValue interface{}) {
	m.logger.Debug("Intermediate energy level changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Recalculate overall energy level
	m.recalculateOverallEnergyLevel()
}

// determineBatteryEnergyLevel determines the battery energy level based on percentage
func (m *Manager) determineBatteryEnergyLevel(percentage float64) string {
	// Build sorted list of levels by battery threshold
	type levelThreshold struct {
		name      string
		threshold float64
	}

	var levels []levelThreshold
	for _, state := range m.config.Energy.EnergyStates {
		if !math.IsNaN(state.BatteryMinimumPercentage) && !math.IsInf(state.BatteryMinimumPercentage, 0) {
			levels = append(levels, levelThreshold{
				name:      state.ConditionName,
				threshold: state.BatteryMinimumPercentage,
			})
		}
	}

	// Sort by threshold ascending
	sort.Slice(levels, func(i, j int) bool {
		return levels[i].threshold < levels[j].threshold
	})

	// Find highest level where percentage >= threshold
	var chosen string
	for _, level := range levels {
		if percentage >= level.threshold {
			chosen = level.name
		}
	}

	m.logger.Debug("Determined battery energy level",
		zap.Float64("percentage", percentage),
		zap.String("level", chosen))

	return chosen
}

// recalculateSolarProductionLevel recalculates the solar production energy level
func (m *Manager) recalculateSolarProductionLevel() {
	thisHourKW, err := m.stateManager.GetNumber("thisHourSolarGeneration")
	if err != nil {
		m.logger.Error("Failed to get thisHourSolarGeneration", zap.Error(err))
		return
	}

	remainingKWH, err := m.stateManager.GetNumber("remainingSolarGeneration")
	if err != nil {
		m.logger.Error("Failed to get remainingSolarGeneration", zap.Error(err))
		return
	}

	level := m.determineSolarEnergyLevel(thisHourKW, remainingKWH)

	m.logger.Info("Determined solar production energy level",
		zap.Float64("this_hour_kw", thisHourKW),
		zap.Float64("remaining_kwh", remainingKWH),
		zap.String("level", level))

	// Update state variable
	if err := m.stateManager.SetString("solarProductionEnergyLevel", level); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping solarProductionEnergyLevel update in read-only mode",
				zap.String("level", level))
		} else {
			m.logger.Error("Failed to set solarProductionEnergyLevel", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateSolarLevel(level)
}

// determineSolarEnergyLevel determines the solar energy level
func (m *Manager) determineSolarEnergyLevel(thisHourKW, remainingKWH float64) string {
	// Default to black
	level := "black"

	// Check each energy state in order (they should already be ordered in config)
	for _, state := range m.config.Energy.EnergyStates {
		// Both conditions must be met
		if thisHourKW >= state.EnergyProductionMinimumKW &&
			remainingKWH >= state.RemainingEnergyProductionMinimumKWH {
			level = state.ConditionName
		}
	}

	m.logger.Debug("Determined solar energy level",
		zap.Float64("this_hour_kw", thisHourKW),
		zap.Float64("remaining_kwh", remainingKWH),
		zap.String("level", level))

	return level
}

// recalculateOverallEnergyLevel calculates the overall energy level
func (m *Manager) recalculateOverallEnergyLevel() {
	// Check for free energy override
	isFreeEnergy, err := m.stateManager.GetBool("isFreeEnergyAvailable")
	if err != nil {
		m.logger.Error("Failed to get isFreeEnergyAvailable", zap.Error(err))
		return
	}

	if isFreeEnergy {
		m.logger.Info("Free energy is available, setting current energy level to white")
		if err := m.stateManager.SetString("currentEnergyLevel", "white"); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping currentEnergyLevel update in read-only mode",
					zap.String("level", "white"))
			} else {
				m.logger.Error("Failed to set currentEnergyLevel", zap.Error(err))
			}
		}
		// Update shadow state
		m.shadowTracker.UpdateOverallLevel("white")
		// Update indicator lights
		m.updateIndicatorLights("white")
		return
	}

	// Get battery and solar levels
	batteryLevel, err := m.stateManager.GetString("batteryEnergyLevel")
	if err != nil {
		m.logger.Error("Failed to get batteryEnergyLevel", zap.Error(err))
		return
	}

	solarLevel, err := m.stateManager.GetString("solarProductionEnergyLevel")
	if err != nil {
		m.logger.Error("Failed to get solarProductionEnergyLevel", zap.Error(err))
		return
	}

	overallLevel := m.determineOverallEnergyLevel(batteryLevel, solarLevel)

	m.logger.Info("Determined overall energy level",
		zap.String("battery_level", batteryLevel),
		zap.String("solar_level", solarLevel),
		zap.String("overall_level", overallLevel))

	// Update state variable
	if err := m.stateManager.SetString("currentEnergyLevel", overallLevel); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping currentEnergyLevel update in read-only mode",
				zap.String("level", overallLevel))
		} else {
			m.logger.Error("Failed to set currentEnergyLevel", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateOverallLevel(overallLevel)

	// Update indicator lights
	m.updateIndicatorLights(overallLevel)
}

// determineOverallEnergyLevel combines battery and solar levels
func (m *Manager) determineOverallEnergyLevel(batteryLevel, solarLevel string) string {
	// Extract ordered list of level names
	var levelNames []string
	for _, state := range m.config.Energy.EnergyStates {
		levelNames = append(levelNames, state.ConditionName)
	}

	// Find indexes of battery and solar levels
	batteryIndex := -1
	solarIndex := -1

	for i, name := range levelNames {
		if name == batteryLevel {
			batteryIndex = i
		}
		if name == solarLevel {
			solarIndex = i
		}
	}

	if batteryIndex == -1 || solarIndex == -1 {
		m.logger.Warn("Invalid battery or solar level",
			zap.String("battery_level", batteryLevel),
			zap.String("solar_level", solarLevel))
		return "black"
	}

	// Find min and max indexes
	minIndex := batteryIndex
	if solarIndex < minIndex {
		minIndex = solarIndex
	}

	maxIndex := batteryIndex
	if solarIndex > maxIndex {
		maxIndex = solarIndex
	}

	// Maximum allowed output is at most one level higher than the lowest input
	maxAllowedIndex := minIndex + 1
	if maxAllowedIndex >= len(levelNames) {
		maxAllowedIndex = len(levelNames) - 1
	}

	// Final output is the lesser of maxIndex and maxAllowedIndex
	outputIndex := maxIndex
	if maxAllowedIndex < outputIndex {
		outputIndex = maxAllowedIndex
	}

	result := levelNames[outputIndex]

	m.logger.Debug("Calculated overall energy level",
		zap.String("battery_level", batteryLevel),
		zap.Int("battery_index", batteryIndex),
		zap.String("solar_level", solarLevel),
		zap.Int("solar_index", solarIndex),
		zap.Int("min_index", minIndex),
		zap.Int("max_index", maxIndex),
		zap.Int("max_allowed_index", maxAllowedIndex),
		zap.Int("output_index", outputIndex),
		zap.String("result", result))

	return result
}

// discoverIndicatorLights discovers light entities that should be used as energy indicators.
// It finds all light entities whose friendly_name matches the configured pattern (default: "Radar").
func (m *Manager) discoverIndicatorLights() {
	pattern := m.config.Energy.IndicatorLights.FriendlyNamePattern
	if pattern == "" {
		pattern = "Radar" // Default pattern for Apollo MTR sensors
	}

	regex, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		m.logger.Error("Invalid indicator lights pattern",
			zap.String("pattern", pattern),
			zap.Error(err))
		return
	}

	states, err := m.haClient.GetAllStates()
	if err != nil {
		m.logger.Error("Failed to get states for indicator light discovery", zap.Error(err))
		return
	}

	var discovered []string
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "light.") {
			continue
		}
		if friendlyName, ok := state.Attributes["friendly_name"].(string); ok {
			if regex.MatchString(friendlyName) {
				discovered = append(discovered, state.EntityID)
			}
		}
	}

	m.indicatorMu.Lock()
	m.indicatorLightEntities = discovered
	m.indicatorMu.Unlock()

	m.logger.Info("Discovered indicator light entities",
		zap.Int("count", len(discovered)),
		zap.Strings("entities", discovered))

	// Update shadow state with discovered entities
	m.shadowTracker.UpdateDiscoveredIndicatorLights(discovered)
}

// updateIndicatorLights updates the indicator light entities to reflect the current energy level.
// When adaptive brightness is enabled, each light is updated with its own brightness based on lux.
// Otherwise, all lights are updated with the static brightness from config.
func (m *Manager) updateIndicatorLights(energyLevel string) {
	m.indicatorMu.RLock()
	entities := m.indicatorLightEntities
	adaptiveEnabled := m.config.Energy.IndicatorLights.AdaptiveBrightness.Enabled
	m.indicatorMu.RUnlock()

	if len(entities) == 0 {
		m.logger.Debug("No indicator light entities discovered, skipping LED update")
		return
	}

	lightConfig := m.getLightConfigForLevel(energyLevel)
	if lightConfig == nil {
		m.logger.Warn("No light config found for energy level",
			zap.String("level", energyLevel))
		return
	}

	rgbColor := []int{lightConfig.Red, lightConfig.Green, lightConfig.Blue}

	// If adaptive brightness is enabled, update each light individually with per-device brightness
	if adaptiveEnabled {
		calibrationEnabled := m.config.Energy.IndicatorLights.AdaptiveBrightness.BaselineCalibration.Enabled

		m.logger.Debug("Updating indicator lights with adaptive brightness",
			zap.String("energy_level", energyLevel),
			zap.Int("entity_count", len(entities)),
			zap.Bool("calibration_enabled", calibrationEnabled))

		for _, entity := range entities {
			brightness := lightConfig.BrightnessPct // default static brightness

			m.indicatorMu.RLock()
			luxSensor, hasLux := m.lightToLuxSensor[entity]
			calibState := m.calibrationState[entity]
			baselineLux, hasBaseline := m.baselineLuxValues[entity]
			currentLux := m.currentLuxValues[luxSensor]
			m.indicatorMu.RUnlock()

			// Skip lights that are currently being calibrated (dimmed)
			if calibState == CalibrationStateDimmed {
				m.logger.Debug("Skipping light update - currently calibrating",
					zap.String("entity", entity))
				continue
			}

			if hasLux {
				// When calibration is enabled, prefer baseline lux over real-time lux
				// because real-time readings are contaminated by the LED's own emission
				var luxForBrightness float64
				if calibrationEnabled && hasBaseline {
					luxForBrightness = baselineLux
					m.logger.Debug("Using baseline lux for brightness",
						zap.String("entity", entity),
						zap.Float64("baseline_lux", baselineLux),
						zap.Float64("current_lux_ignored", currentLux))
				} else {
					luxForBrightness = currentLux
				}

				brightness = m.calculateAdaptiveBrightness(luxForBrightness, entity)

				// Update last brightness level (for hysteresis tracking)
				m.indicatorMu.Lock()
				m.lastBrightnessLevel[entity] = brightness
				m.indicatorMu.Unlock()
			}

			m.updateSingleIndicatorLight(entity, energyLevel, brightness)
		}

		// Update shadow state with overall action info
		m.shadowTracker.UpdateIndicatorLightsAction(energyLevel, rgbColor, -1, entities) // -1 indicates per-device brightness
		return
	}

	// Non-adaptive mode: update all lights with the same static brightness
	m.shadowTracker.UpdateIndicatorLightsAction(energyLevel, rgbColor, lightConfig.BrightnessPct, entities)

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would update indicator lights",
			zap.String("energy_level", energyLevel),
			zap.Ints("rgb_color", rgbColor),
			zap.Int("brightness_pct", lightConfig.BrightnessPct),
			zap.Strings("entities", entities))
		return
	}

	// Call Home Assistant light.turn_on service for all lights at once
	err := m.haClient.CallService("light", "turn_on", map[string]interface{}{
		"entity_id":      entities,
		"rgb_color":      rgbColor,
		"brightness_pct": lightConfig.BrightnessPct,
	})

	if err != nil {
		m.logger.Error("Failed to update indicator lights",
			zap.String("energy_level", energyLevel),
			zap.Error(err))
		return
	}

	m.logger.Info("Updated indicator lights",
		zap.String("energy_level", energyLevel),
		zap.Ints("rgb_color", rgbColor),
		zap.Int("brightness_pct", lightConfig.BrightnessPct),
		zap.Int("entity_count", len(entities)))
}

// runFreeEnergyChecker runs the free energy checker every minute
func (m *Manager) runFreeEnergyChecker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Check immediately on start
	m.checkFreeEnergy()

	// Signal that startup initialization is complete
	m.startupWg.Done()

	for {
		select {
		case <-ticker.C:
			m.checkFreeEnergy()
		case <-m.stopChecker:
			m.logger.Info("Stopping free energy checker")
			return
		}
	}
}

// checkFreeEnergy checks if free energy is currently available
func (m *Manager) checkFreeEnergy() {
	isGridAvailable, err := m.stateManager.GetBool("isGridAvailable")
	if err != nil {
		m.logger.Error("Failed to get isGridAvailable", zap.Error(err))
		return
	}

	isFreeEnergy := m.isFreeEnergyTime(isGridAvailable)

	// Get current state
	currentFreeEnergy, err := m.stateManager.GetBool("isFreeEnergyAvailable")
	if err != nil {
		m.logger.Error("Failed to get isFreeEnergyAvailable", zap.Error(err))
		return
	}

	// Only log changes
	if isFreeEnergy != currentFreeEnergy {
		m.logger.Info("Free energy availability changed",
			zap.Bool("is_free_energy", isFreeEnergy),
			zap.Bool("is_grid_available", isGridAvailable))
	}

	// Update state
	if err := m.stateManager.SetBool("isFreeEnergyAvailable", isFreeEnergy); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping isFreeEnergyAvailable update in read-only mode",
				zap.Bool("is_free_energy", isFreeEnergy))
		} else {
			m.logger.Error("Failed to set isFreeEnergyAvailable", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateFreeEnergyAvailable(isFreeEnergy)
}

// isFreeEnergyTime checks if current time is within free energy window
func (m *Manager) isFreeEnergyTime(isGridAvailable bool) bool {
	if !isGridAvailable {
		m.logger.Debug("Grid is not available, no free energy")
		return false
	}

	// Get current time in configured timezone
	now := time.Now().In(m.timezone)

	// Parse times (format: "21:00")
	startTime, err := time.Parse("15:04", m.config.Energy.FreeEnergyTime.Start)
	if err != nil {
		m.logger.Error("Failed to parse free energy start time", zap.Error(err))
		return false
	}

	endTime, err := time.Parse("15:04", m.config.Energy.FreeEnergyTime.End)
	if err != nil {
		m.logger.Error("Failed to parse free energy end time", zap.Error(err))
		return false
	}

	// Set the times to today in configured timezone
	todayStart := time.Date(now.Year(), now.Month(), now.Day(),
		startTime.Hour(), startTime.Minute(), 0, 0, m.timezone)

	todayEnd := time.Date(now.Year(), now.Month(), now.Day(),
		endTime.Hour(), endTime.Minute(), 0, 0, m.timezone)

	// If end time is before start time, it spans midnight
	if todayEnd.Before(todayStart) {
		// Free energy is from start time yesterday to end time today
		// OR from start time today to end time tomorrow
		if now.After(todayStart) || now.Before(todayEnd) {
			m.logger.Debug("Within free energy time (spans midnight)",
				zap.Time("now", now),
				zap.Time("start", todayStart),
				zap.Time("end", todayEnd))
			return true
		}
	} else {
		// Normal case: start and end on same day
		if now.After(todayStart) && now.Before(todayEnd) {
			m.logger.Debug("Within free energy time",
				zap.Time("now", now),
				zap.Time("start", todayStart),
				zap.Time("end", todayEnd))
			return true
		}
	}

	return false
}

// Reset re-calculates overall energy level
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Energy State - re-calculating overall energy level")

	// Re-check free energy availability
	m.checkFreeEnergy()

	// Recalculate overall energy level based on current battery and solar levels
	m.recalculateOverallEnergyLevel()

	m.logger.Info("Successfully reset Energy State")
	return nil
}

// ============================================================================
// Baseline Calibration for LED Self-Interference Detection
// ============================================================================

// runBaselineCalibration runs periodic calibration cycles to measure true ambient light.
// The LED's own emission can overwhelm the lux sensor, so we periodically dim the LED
// to get an accurate baseline reading.
func (m *Manager) runBaselineCalibration() {
	calibConfig := m.config.Energy.IndicatorLights.AdaptiveBrightness.BaselineCalibration

	// Get calibration interval (default 5 minutes)
	intervalSec := calibConfig.CalibrationIntervalSec
	if intervalSec <= 0 {
		intervalSec = 300
	}

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	m.logger.Info("Starting baseline calibration",
		zap.Int("interval_sec", intervalSec),
		zap.Int("brightness_pct", calibConfig.CalibrationBrightnessPct),
		zap.Int("wait_sec", calibConfig.CalibrationWaitSec))

	// Signal that the goroutine has started (tests can proceed)
	m.startupWg.Done()

	// Run initial calibration cycle shortly after startup
	// Give the system time to stabilize first, but check for shutdown
	startupDelay := time.NewTimer(10 * time.Second)
	select {
	case <-startupDelay.C:
		m.runCalibrationCycle()
	case <-m.stopCalibration:
		startupDelay.Stop()
		m.logger.Info("Stopping baseline calibration (during startup delay)")
		return
	}

	for {
		select {
		case <-ticker.C:
			m.runCalibrationCycle()
		case <-m.stopCalibration:
			m.logger.Info("Stopping baseline calibration")
			return
		}
	}
}

// runCalibrationCycle runs one calibration cycle for all indicator lights.
// IMPORTANT: Devices are calibrated one at a time (staggered) so that users can
// always see the energy state on at least some devices while others calibrate.
func (m *Manager) runCalibrationCycle() {
	calibConfig := m.config.Energy.IndicatorLights.AdaptiveBrightness.BaselineCalibration

	// Get calibration parameters with defaults
	calibBrightness := calibConfig.CalibrationBrightnessPct
	if calibBrightness <= 0 {
		calibBrightness = 5
	}

	waitSec := calibConfig.CalibrationWaitSec
	if waitSec <= 0 {
		waitSec = 65
	}

	m.indicatorMu.RLock()
	lights := make([]string, len(m.indicatorLightEntities))
	copy(lights, m.indicatorLightEntities)
	m.indicatorMu.RUnlock()

	if len(lights) == 0 {
		return
	}

	m.logger.Info("Starting calibration cycle",
		zap.Int("device_count", len(lights)))

	// Calibrate each device one at a time so other devices remain visible
	for i, lightEntity := range lights {
		// Check if we should stop
		select {
		case <-m.stopCalibration:
			m.logger.Info("Calibration cycle interrupted by shutdown")
			return
		default:
		}

		m.indicatorMu.RLock()
		luxSensor, hasLux := m.lightToLuxSensor[lightEntity]
		m.indicatorMu.RUnlock()

		if !hasLux {
			continue // Skip lights without lux sensors
		}

		m.logger.Debug("Calibrating device (staggered)",
			zap.Int("device_num", i+1),
			zap.Int("total_devices", len(lights)),
			zap.String("light", lightEntity))

		// Phase 1: Dim this single light
		m.indicatorMu.Lock()
		m.calibrationState[lightEntity] = CalibrationStateDimmed
		m.indicatorMu.Unlock()

		m.logger.Debug("Dimming light for calibration",
			zap.String("light", lightEntity),
			zap.String("lux_sensor", luxSensor),
			zap.Int("calibration_brightness", calibBrightness))

		m.setLightBrightness(lightEntity, calibBrightness)

		// Phase 2: Wait for fresh lux reading
		// Use a select to allow early exit if shutdown requested
		waitTimer := time.NewTimer(time.Duration(waitSec) * time.Second)
		select {
		case <-waitTimer.C:
			// Normal wait completed
		case <-m.stopCalibration:
			waitTimer.Stop()
			m.logger.Info("Calibration wait interrupted by shutdown")
			// Restore this light before exiting
			m.restoreLightAfterCalibration(lightEntity)
			return
		}

		// Phase 3: Record baseline and restore this light
		m.restoreLightAfterCalibration(lightEntity)
	}

	m.logger.Info("Calibration cycle complete")
}

// restoreLightAfterCalibration completes calibration for a single light entity.
func (m *Manager) restoreLightAfterCalibration(lightEntity string) {
	m.indicatorMu.RLock()
	luxSensor, hasLux := m.lightToLuxSensor[lightEntity]
	state := m.calibrationState[lightEntity]
	m.indicatorMu.RUnlock()

	if !hasLux || state != CalibrationStateDimmed {
		return
	}

	// Get the current lux reading - this is our baseline
	m.indicatorMu.Lock()
	currentLux := m.currentLuxValues[luxSensor]
	m.baselineLuxValues[lightEntity] = currentLux
	m.lastCalibrationTime[lightEntity] = time.Now()
	m.calibrationState[lightEntity] = CalibrationStateNormal
	m.indicatorMu.Unlock()

	m.logger.Debug("Calibration complete - recorded baseline lux",
		zap.String("light", lightEntity),
		zap.Float64("baseline_lux", currentLux))

	// Update shadow state
	m.shadowTracker.UpdateBaselineLux(lightEntity, currentLux)

	// Calculate and apply new brightness based on baseline
	newBrightness := m.calculateAdaptiveBrightness(currentLux, lightEntity)

	m.indicatorMu.Lock()
	m.lastBrightnessLevel[lightEntity] = newBrightness
	m.indicatorMu.Unlock()

	// Get current energy level for color
	currentLevel, err := m.stateManager.GetString("currentEnergyLevel")
	if err != nil {
		currentLevel = "black"
	}

	m.logger.Debug("Restoring brightness after calibration",
		zap.String("light", lightEntity),
		zap.Float64("baseline_lux", currentLux),
		zap.Int("new_brightness", newBrightness))

	m.updateSingleIndicatorLight(lightEntity, currentLevel, newBrightness)
}

// setLightBrightness sets only the brightness of a light, preserving its current color.
func (m *Manager) setLightBrightness(entity string, brightness int) {
	if m.readOnly {
		m.logger.Debug("READ-ONLY: Would set light brightness",
			zap.String("entity", entity),
			zap.Int("brightness_pct", brightness))
		return
	}

	// Get current energy level for color
	currentLevel, err := m.stateManager.GetString("currentEnergyLevel")
	if err != nil {
		currentLevel = "black"
	}

	lightConfig := m.getLightConfigForLevel(currentLevel)
	if lightConfig == nil {
		return
	}

	rgbColor := []int{lightConfig.Red, lightConfig.Green, lightConfig.Blue}

	err = m.haClient.CallService("light", "turn_on", map[string]interface{}{
		"entity_id":      entity,
		"rgb_color":      rgbColor,
		"brightness_pct": brightness,
	})

	if err != nil {
		m.logger.Error("Failed to set light brightness for calibration",
			zap.String("entity", entity),
			zap.Int("brightness", brightness),
			zap.Error(err))
	}
}

// isCalibrationEnabled returns true if baseline calibration is enabled
func (m *Manager) isCalibrationEnabled() bool {
	return m.config.Energy.IndicatorLights.AdaptiveBrightness.BaselineCalibration.Enabled
}

// getBaselineLux returns the baseline lux value for a light entity if available.
// Returns the value and true if a baseline exists, or 0 and false otherwise.
func (m *Manager) getBaselineLux(lightEntity string) (float64, bool) {
	m.indicatorMu.RLock()
	defer m.indicatorMu.RUnlock()

	lux, exists := m.baselineLuxValues[lightEntity]
	return lux, exists
}

// ============================================================================
// Adaptive Brightness for Indicator LEDs
// ============================================================================

// extractDeviceID extracts the device identifier from an entity ID.
// Example: "light.apollo_msr_2_1294c8_rgb_light" -> "apollo_msr_2_1294c8"
// Example: "sensor.apollo_msr_2_1294c8_ltr390_light" -> "apollo_msr_2_1294c8"
func extractDeviceID(entityID string) string {
	// Remove domain prefix (light., sensor., etc.)
	parts := strings.SplitN(entityID, ".", 2)
	if len(parts) < 2 {
		return ""
	}
	name := parts[1]

	// Extract device ID by removing known suffixes
	// Pattern: apollo_msr_2_{hex_id}_{suffix}
	suffixes := []string{"_rgb_light", "_ltr390_light", "_ltr390_uv_index", "_radar_target", "_online"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}

	// Fallback: return as-is
	return name
}

// discoverLuxSensors discovers lux sensor entities and associates them with indicator lights.
// Each indicator light is matched to its corresponding lux sensor by device ID.
func (m *Manager) discoverLuxSensors() {
	if !m.config.Energy.IndicatorLights.AdaptiveBrightness.Enabled {
		m.logger.Info("Adaptive brightness disabled, skipping lux sensor discovery")
		return
	}

	pattern := m.config.Energy.IndicatorLights.AdaptiveBrightness.LuxSensorPattern
	if pattern == "" {
		pattern = "ltr390_light" // Default pattern for Apollo MSR-2 light sensors
	}

	states, err := m.haClient.GetAllStates()
	if err != nil {
		m.logger.Error("Failed to get states for lux sensor discovery", zap.Error(err))
		return
	}

	// Build a map of device ID -> lux sensor entity ID
	luxSensorsByDevice := make(map[string]string)
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "sensor.") {
			continue
		}
		// Match by pattern in entity_id (not friendly_name)
		if strings.Contains(state.EntityID, pattern) {
			deviceID := extractDeviceID(state.EntityID)
			if deviceID != "" {
				luxSensorsByDevice[deviceID] = state.EntityID
				m.logger.Debug("Found lux sensor",
					zap.String("entity_id", state.EntityID),
					zap.String("device_id", deviceID))
			}
		}
	}

	// Associate each indicator light with its lux sensor
	m.indicatorMu.Lock()
	defer m.indicatorMu.Unlock()

	for _, lightEntity := range m.indicatorLightEntities {
		lightDeviceID := extractDeviceID(lightEntity)
		if luxSensor, found := luxSensorsByDevice[lightDeviceID]; found {
			m.lightToLuxSensor[lightEntity] = luxSensor
			m.logger.Info("Associated light with lux sensor",
				zap.String("light", lightEntity),
				zap.String("lux_sensor", luxSensor),
				zap.String("device_id", lightDeviceID))
		} else {
			m.logger.Warn("No lux sensor found for indicator light",
				zap.String("light", lightEntity),
				zap.String("device_id", lightDeviceID))
		}
	}

	m.logger.Info("Lux sensor discovery complete",
		zap.Int("lights_with_lux", len(m.lightToLuxSensor)),
		zap.Int("total_lights", len(m.indicatorLightEntities)))

	// Update shadow state with light-to-lux mapping
	m.shadowTracker.UpdateLightToLuxMapping(m.lightToLuxSensor)
}

// subscribeLuxSensors subscribes to state changes from discovered lux sensors.
func (m *Manager) subscribeLuxSensors() error {
	if !m.config.Energy.IndicatorLights.AdaptiveBrightness.Enabled {
		return nil
	}

	m.indicatorMu.RLock()
	// Get unique lux sensor entities
	luxSensors := make(map[string]bool)
	for _, luxEntity := range m.lightToLuxSensor {
		luxSensors[luxEntity] = true
	}
	m.indicatorMu.RUnlock()

	for luxEntity := range luxSensors {
		entity := luxEntity // capture for closure
		if err := m.subHelper.SubscribeToSensor(entity, func(lux float64) {
			m.handleLuxChange(entity, lux)
		}); err != nil {
			return fmt.Errorf("failed to subscribe to lux sensor %s: %w", entity, err)
		}
		m.logger.Info("Subscribed to lux sensor", zap.String("entity_id", entity))
	}

	return nil
}

// handleLuxChange processes a lux sensor value change.
func (m *Manager) handleLuxChange(luxEntity string, lux float64) {
	// Validate lux value
	if math.IsNaN(lux) || math.IsInf(lux, 0) {
		m.logger.Warn("Invalid lux value, ignoring",
			zap.String("entity", luxEntity),
			zap.Float64("lux", lux))
		return
	}

	// Store current lux value
	m.indicatorMu.Lock()
	m.currentLuxValues[luxEntity] = lux
	m.indicatorMu.Unlock()

	// Update shadow state
	m.shadowTracker.UpdateLuxReading(luxEntity, lux)

	m.logger.Debug("Lux sensor value updated",
		zap.String("entity", luxEntity),
		zap.Float64("lux", lux))

	// Find lights using this lux sensor and update brightness
	m.indicatorMu.RLock()
	var lightsToUpdate []string
	for lightEntity, sensorEntity := range m.lightToLuxSensor {
		if sensorEntity == luxEntity {
			lightsToUpdate = append(lightsToUpdate, lightEntity)
		}
	}
	m.indicatorMu.RUnlock()

	for _, lightEntity := range lightsToUpdate {
		m.updateLightBrightness(lightEntity, lux)
	}
}

// getDefaultBrightnessCurve returns the default lux-to-brightness curve from issue #194.
func getDefaultBrightnessCurve() []BrightnessCurvePoint {
	return []BrightnessCurvePoint{
		{LuxMax: 10, BrightnessPct: 20},
		{LuxMax: 100, BrightnessPct: 40},
		{LuxMax: 500, BrightnessPct: 60},
		{LuxMax: 1000, BrightnessPct: 80},
	}
}

// calculateAdaptiveBrightness returns the brightness percentage for a given lux value.
// It applies hysteresis to prevent oscillation at threshold boundaries.
func (m *Manager) calculateAdaptiveBrightness(lux float64, lightEntity string) int {
	curve := m.config.Energy.IndicatorLights.AdaptiveBrightness.BrightnessCurve
	if len(curve) == 0 {
		curve = getDefaultBrightnessCurve()
	}

	// Get hysteresis percentage (default 10%)
	hysteresisPercent := m.config.Energy.IndicatorLights.AdaptiveBrightness.HysteresisPercent
	if hysteresisPercent == 0 {
		hysteresisPercent = 10
	}

	// Get last brightness level for hysteresis check
	m.indicatorMu.RLock()
	lastBrightness := m.lastBrightnessLevel[lightEntity]
	m.indicatorMu.RUnlock()

	// Find new brightness level based on lux value
	newBrightness := 100 // default for lux >= highest threshold
	for _, point := range curve {
		if lux < point.LuxMax {
			newBrightness = point.BrightnessPct
			break
		}
	}

	// Apply hysteresis: only change if we've crossed threshold significantly
	if lastBrightness != 0 && newBrightness != lastBrightness {
		// Find the threshold between old and new brightness levels
		for _, point := range curve {
			threshold := point.LuxMax
			hysteresisBand := threshold * float64(hysteresisPercent) / 100.0

			// Check if we're within the hysteresis band of this threshold
			if lux >= threshold-hysteresisBand && lux <= threshold+hysteresisBand {
				// Stay at current level if we're in the hysteresis band
				m.logger.Debug("Hysteresis: staying at current brightness",
					zap.String("light", lightEntity),
					zap.Float64("lux", lux),
					zap.Float64("threshold", threshold),
					zap.Int("last_brightness", lastBrightness),
					zap.Int("would_be_brightness", newBrightness))
				return lastBrightness
			}
		}
	}

	return newBrightness
}

// updateLightBrightness updates a single light's brightness based on lux, with debouncing.
func (m *Manager) updateLightBrightness(lightEntity string, lux float64) {
	// Check debounce
	debounceSec := m.config.Energy.IndicatorLights.AdaptiveBrightness.DebounceDurationSec
	if debounceSec == 0 {
		debounceSec = 5 // default 5 seconds
	}
	debounceDuration := time.Duration(debounceSec) * time.Second

	m.indicatorMu.Lock()
	lastUpdate, exists := m.lastBrightnessUpdate[lightEntity]
	if exists && time.Since(lastUpdate) < debounceDuration {
		m.indicatorMu.Unlock()
		m.logger.Debug("Skipping brightness update (debounce)",
			zap.String("light", lightEntity),
			zap.Duration("since_last", time.Since(lastUpdate)),
			zap.Duration("debounce", debounceDuration))
		return
	}
	m.indicatorMu.Unlock()

	// Calculate adaptive brightness
	brightness := m.calculateAdaptiveBrightness(lux, lightEntity)

	// Check if brightness actually changed
	m.indicatorMu.Lock()
	oldBrightness := m.lastBrightnessLevel[lightEntity]
	if brightness == oldBrightness && oldBrightness != 0 {
		m.indicatorMu.Unlock()
		m.logger.Debug("Brightness unchanged, skipping update",
			zap.String("light", lightEntity),
			zap.Int("brightness", brightness))
		return
	}
	m.lastBrightnessLevel[lightEntity] = brightness
	m.lastBrightnessUpdate[lightEntity] = time.Now()
	m.indicatorMu.Unlock()

	// Get current energy level for RGB color
	currentLevel, err := m.stateManager.GetString("currentEnergyLevel")
	if err != nil {
		m.logger.Warn("Failed to get current energy level for brightness update",
			zap.Error(err))
		return
	}

	m.logger.Debug("Updating light brightness based on lux",
		zap.String("light", lightEntity),
		zap.Float64("lux", lux),
		zap.Int("old_brightness", oldBrightness),
		zap.Int("new_brightness", brightness))

	// Update the single light with new brightness
	m.updateSingleIndicatorLight(lightEntity, currentLevel, brightness)
}

// getLightConfigForLevel returns the LightConfig for a given energy level.
func (m *Manager) getLightConfigForLevel(energyLevel string) *LightConfig {
	for _, state := range m.config.Energy.EnergyStates {
		if state.ConditionName == energyLevel {
			return &state.LightConfig
		}
	}
	return nil
}

// updateSingleIndicatorLight updates a single indicator light with the given color and brightness.
func (m *Manager) updateSingleIndicatorLight(entity, energyLevel string, brightness int) {
	lightConfig := m.getLightConfigForLevel(energyLevel)
	if lightConfig == nil {
		m.logger.Warn("No light config found for energy level",
			zap.String("level", energyLevel),
			zap.String("entity", entity))
		return
	}

	rgbColor := []int{lightConfig.Red, lightConfig.Green, lightConfig.Blue}

	// Update shadow state
	m.indicatorMu.RLock()
	luxSensor := m.lightToLuxSensor[entity]
	lux := m.currentLuxValues[luxSensor]
	isAdaptive := luxSensor != ""
	m.indicatorMu.RUnlock()

	m.shadowTracker.UpdatePerDeviceBrightness(entity, luxSensor, lux, brightness, isAdaptive)

	if m.readOnly {
		m.logger.Debug("READ-ONLY: Would update single indicator light",
			zap.String("entity", entity),
			zap.String("energy_level", energyLevel),
			zap.Ints("rgb_color", rgbColor),
			zap.Int("brightness_pct", brightness),
			zap.Bool("adaptive", isAdaptive))
		return
	}

	// Call Home Assistant light.turn_on service for single entity
	err := m.haClient.CallService("light", "turn_on", map[string]interface{}{
		"entity_id":      entity,
		"rgb_color":      rgbColor,
		"brightness_pct": brightness,
	})

	if err != nil {
		m.logger.Error("Failed to update single indicator light",
			zap.String("entity", entity),
			zap.String("energy_level", energyLevel),
			zap.Error(err))
		return
	}

	m.logger.Debug("Updated single indicator light",
		zap.String("entity", entity),
		zap.String("energy_level", energyLevel),
		zap.Ints("rgb_color", rgbColor),
		zap.Int("brightness_pct", brightness))
}
