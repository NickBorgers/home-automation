package energy

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
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

	// Shadow state tracking
	shadowTracker *shadowstate.EnergyTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Discovered indicator light entities (Apollo sensors with "Radar" in friendly_name)
	indicatorLightEntities []string
}

// NewManager creates a new Energy State manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, config *EnergyConfig, logger *zap.Logger, readOnly bool, timezone *time.Location, registry *shadowstate.SubscriptionRegistry) *Manager {
	// Default to UTC if no timezone provided
	if timezone == nil {
		timezone = time.UTC
	}

	shadowTracker := shadowstate.NewEnergyTracker()

	m := &Manager{
		haClient:      haClient,
		stateManager:  stateManager,
		config:        config,
		logger:        logger.Named("energy"),
		readOnly:      readOnly,
		timezone:      timezone,
		stopChecker:   make(chan struct{}),
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "energy", logger.Named("energy")),
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

	// Start free energy check timer (check every minute)
	go m.runFreeEnergyChecker()

	// Discover indicator light entities (Apollo sensors)
	m.discoverIndicatorLights()

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

	// Unsubscribe from all subscriptions via helper
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Energy State Manager stopped")
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

	m.indicatorLightEntities = discovered
	m.logger.Info("Discovered indicator light entities",
		zap.Int("count", len(discovered)),
		zap.Strings("entities", discovered))

	// Update shadow state with discovered entities
	m.shadowTracker.UpdateDiscoveredIndicatorLights(discovered)
}

// updateIndicatorLights updates the indicator light entities to reflect the current energy level.
func (m *Manager) updateIndicatorLights(energyLevel string) {
	if len(m.indicatorLightEntities) == 0 {
		m.logger.Debug("No indicator light entities discovered, skipping LED update")
		return
	}

	// Find the LightConfig for this energy level
	var lightConfig *LightConfig
	for _, state := range m.config.Energy.EnergyStates {
		if state.ConditionName == energyLevel {
			lightConfig = &state.LightConfig
			break
		}
	}

	if lightConfig == nil {
		m.logger.Warn("No light config found for energy level",
			zap.String("level", energyLevel))
		return
	}

	rgbColor := []int{lightConfig.Red, lightConfig.Green, lightConfig.Blue}

	// Update shadow state with the action we're about to take (or would take in read-only mode)
	m.shadowTracker.UpdateIndicatorLightsAction(energyLevel, rgbColor, lightConfig.BrightnessPct, m.indicatorLightEntities)

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would update indicator lights",
			zap.String("energy_level", energyLevel),
			zap.Ints("rgb_color", rgbColor),
			zap.Int("brightness_pct", lightConfig.BrightnessPct),
			zap.Strings("entities", m.indicatorLightEntities))
		return
	}

	// Call Home Assistant light.turn_on service
	err := m.haClient.CallService("light", "turn_on", map[string]interface{}{
		"entity_id":      m.indicatorLightEntities,
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
		zap.Int("entity_count", len(m.indicatorLightEntities)))
}

// runFreeEnergyChecker runs the free energy checker every minute
func (m *Manager) runFreeEnergyChecker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Check immediately on start
	m.checkFreeEnergy()

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
