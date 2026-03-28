package loadshedding

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

type forecastEntry struct {
	Temperature float64 `json:"temperature"`
	TempLow     float64 `json:"templow"`
}

// getForecastHighLow fetches the forecast daily high and low for today.
// Returns (high, low, true) if forecast is available, or (0, 0, false) if unavailable.
// Results are cached for forecastCacheDuration. Failures are negative-cached for
// forecastNegativeCacheDuration to avoid spamming HA when the service is durably down.
// Tries forecastWeatherEntityPrimary first, then forecastWeatherEntitySecondary.
func (m *Manager) getForecastHighLow() (float64, float64, bool) {
	m.forecastMu.Lock()
	defer m.forecastMu.Unlock()

	// Return cached forecast if still fresh
	if !m.forecastCachedAt.IsZero() && time.Since(m.forecastCachedAt) < forecastCacheDuration {
		return m.forecastHigh, m.forecastLow, true
	}

	// Negative cache: don't retry if we failed recently
	if !m.forecastFailedAt.IsZero() && time.Since(m.forecastFailedAt) < forecastNegativeCacheDuration {
		m.logger.Debug("Skipping forecast fetch: within negative cache window",
			zap.Duration("since_failure", time.Since(m.forecastFailedAt)))
		return 0, 0, false
	}

	// Try each forecast entity in priority order
	entities := []string{forecastWeatherEntityPrimary, forecastWeatherEntitySecondary}
	for _, entity := range entities {
		high, low, ok := m.tryFetchForecast(entity)
		if ok {
			m.forecastHigh = high
			m.forecastLow = low
			m.forecastCachedAt = time.Now()
			m.forecastFailedAt = time.Time{} // clear negative cache on success

			m.logger.Info("Fetched weather forecast for thermal battery",
				zap.String("source", entity),
				zap.Float64("forecast_high", high),
				zap.Float64("forecast_low", low))

			return high, low, true
		}
	}

	// All sources failed — set negative cache
	m.forecastFailedAt = time.Now()
	m.logger.Warn("All forecast sources failed, will fall back to current outdoor temp")
	return 0, 0, false
}

// tryFetchForecast attempts to fetch a daily forecast from a single weather entity.
// Returns (high, low, true) on success, or (0, 0, false) on any failure.
func (m *Manager) tryFetchForecast(entity string) (float64, float64, bool) {
	result, err := m.haClient.CallServiceWithResponse(m.ctx, "weather", "get_forecasts", map[string]interface{}{
		"entity_id": entity,
		"type":      "daily",
	})
	if err != nil {
		m.logger.Warn("Failed to fetch weather forecast",
			zap.String("entity", entity), zap.Error(err))
		return 0, 0, false
	}

	if result == nil {
		m.logger.Warn("Weather forecast returned nil response",
			zap.String("entity", entity))
		return 0, 0, false
	}

	var parsed map[string]struct {
		Forecast []forecastEntry `json:"forecast"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		m.logger.Warn("Failed to parse weather forecast response",
			zap.String("entity", entity), zap.Error(err))
		return 0, 0, false
	}

	entityData, ok := parsed[entity]
	if !ok || len(entityData.Forecast) == 0 {
		m.logger.Warn("No forecast entries found in response",
			zap.String("entity", entity))
		return 0, 0, false
	}

	today := entityData.Forecast[0]
	return today.Temperature, today.TempLow, true
}

// activateThermalBattery shifts HVAC setpoints to pre-condition the house when energy is abundant (white level).
// It reads current thermostat state, saves the original setpoints, and applies the first 1°F step.
// If more steps are needed (thermalBatteryOffset > thermalBatteryStepSize), a background goroutine
// polls thermostat current_temperature and applies subsequent steps once the previous target is nearly reached.
// This gradual approach prevents the thermostat from engaging auxiliary heat strips.
func (m *Manager) activateThermalBattery() {
	m.thermalBatteryMu.Lock()
	defer m.thermalBatteryMu.Unlock()

	if m.thermalBatteryActive {
		m.logger.Info("Thermal battery already active, skipping activation")
		return
	}

	// Guard: don't pre-condition if load shedding is active (would conflict)
	m.stateMu.Lock()
	loadSheddingOn := m.loadSheddingOn
	m.stateMu.Unlock()
	if loadSheddingOn {
		m.logger.Info("Skipping thermal battery: load shedding is active")
		m.shadowTracker.RecordThermalBatterySkipped("load shedding active")
		return
	}

	// Restart safety: if holds are on from a previous session (thermal battery or load shedding),
	// turn them off so the thermostat reverts to its scheduled setpoints. This ensures we
	// capture schedule-based values as our baseline, not stale shifted values.
	// The load shedding guard above ensures we only reach here when load shedding is inactive,
	// so we won't interfere with active load shedding holds.
	holdOn, err := m.checkThermostatHoldState()
	if err != nil {
		m.logger.Warn("Failed to check thermostat hold state for thermal battery", zap.Error(err))
	} else if holdOn {
		m.logger.Info("Thermal battery: holds are on (likely stale from previous session), reverting to schedule",
			zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))
		if !m.readOnly {
			if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
				"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
			}); err != nil {
				m.logger.Error("Failed to revert thermostat holds for thermal battery", zap.Error(err))
				return
			}
			// Wait for the thermostat to revert to its scheduled setpoints
			time.Sleep(m.thermalBatteryHoldRevertDelay)
		}
	}

	// Guard: no one is home
	isHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		m.logger.Warn("Failed to check isAnyoneHome for thermal battery", zap.Error(err))
	} else if !isHome {
		m.logger.Info("Skipping thermal battery: no one is home")
		m.shadowTracker.RecordThermalBatterySkipped("no one is home")
		return
	}

	// Guard: everyone is asleep
	everyoneAsleep, err := m.stateManager.GetBool("isEveryoneAsleep")
	if err != nil {
		m.logger.Warn("Failed to check isEveryoneAsleep for thermal battery", zap.Error(err))
	} else if everyoneAsleep {
		m.logger.Info("Skipping thermal battery: everyone is asleep")
		m.shadowTracker.RecordThermalBatterySkipped("everyone is asleep")
		return
	}

	// Read current setpoints from both thermostats
	climateEntities := []string{climateHouse, climateSuite}
	savedSetpoints := make(map[string]shadowstate.SavedSetpoint)

	for _, entityID := range climateEntities {
		state, err := m.haClient.GetState(entityID)
		if err != nil {
			m.logger.Error("Failed to read thermostat state for thermal battery",
				zap.String("entity", entityID), zap.Error(err))
			return
		}

		hvacMode := state.State // "heat", "cool", "heat_cool", "off", "auto"
		if hvacMode == "off" {
			m.logger.Info("Skipping thermal battery: thermostat is off",
				zap.String("entity", entityID))
			m.shadowTracker.RecordThermalBatterySkipped("thermostat " + entityID + " is off")
			return
		}

		sp := shadowstate.SavedSetpoint{
			EntityID: entityID,
			HVACMode: hvacMode,
		}

		switch hvacMode {
		case "heat":
			if temp, ok := state.Attributes["temperature"].(float64); ok {
				sp.TargetTemp = temp
			}
		case "cool":
			if temp, ok := state.Attributes["temperature"].(float64); ok {
				sp.TargetTemp = temp
			}
		case "heat_cool", "auto":
			if low, ok := state.Attributes["target_temp_low"].(float64); ok {
				sp.TargetLow = low
			}
			if high, ok := state.Attributes["target_temp_high"].(float64); ok {
				sp.TargetHigh = high
			}
		default:
			m.logger.Warn("Unknown HVAC mode, skipping thermal battery",
				zap.String("entity", entityID), zap.String("mode", hvacMode))
			m.shadowTracker.RecordThermalBatterySkipped("unknown HVAC mode: " + hvacMode)
			return
		}

		savedSetpoints[entityID] = sp
	}

	// For heat_cool/auto mode, determine shift direction from weather forecast (or fallback to current outdoor temp)
	direction := "" // "up" or "down" - used for all modes
	var outdoorTemp float64
	hasHeatCool := false
	usedForecast := false
	var forecastHigh, forecastLow float64
	for _, sp := range savedSetpoints {
		if sp.HVACMode == "heat_cool" || sp.HVACMode == "auto" {
			hasHeatCool = true
			break
		}
	}

	if hasHeatCool {
		// Try forecast first, fall back to current outdoor temp
		fHigh, fLow, hasForecast := m.getForecastHighLow()

		if hasForecast {
			usedForecast = true
			forecastHigh = fHigh
			forecastLow = fLow

			// Use the first heat_cool thermostat's setpoints to define the skip zone
			for _, sp := range savedSetpoints {
				if sp.HVACMode == "heat_cool" || sp.HVACMode == "auto" {
					skipLow := sp.TargetLow - thermalBatterySkipMargin
					skipHigh := sp.TargetHigh + thermalBatterySkipMargin

					// Skip only if both forecast high AND low are within the comfort band + margin
					if forecastHigh <= skipHigh && forecastLow >= skipLow {
						m.logger.Info("Skipping thermal battery: forecast within skip zone",
							zap.Float64("forecast_high", forecastHigh),
							zap.Float64("forecast_low", forecastLow),
							zap.Float64("skip_low", skipLow),
							zap.Float64("skip_high", skipHigh))
						m.shadowTracker.RecordThermalBatterySkipped(
							fmt.Sprintf("forecast high %.1f°F/low %.1f°F within skip zone (%.0f-%.0f°F)", forecastHigh, forecastLow, skipLow, skipHigh))
						return
					}

					// Direction based on which extreme is outside the comfort band
					if forecastHigh > skipHigh {
						direction = "down" // hot day coming, pre-cool
					} else {
						direction = "up" // cold night coming, pre-heat
					}
					break
				}
			}
		} else {
			// Fallback: use current outdoor temperature (existing behavior)
			m.logger.Warn("Using current outdoor temp as fallback for thermal battery direction")

			outdoorState, err := m.haClient.GetState(outdoorTempSensor)
			if err != nil {
				m.logger.Warn("Skipping thermal battery: outdoor temp sensor unavailable",
					zap.String("sensor", outdoorTempSensor), zap.Error(err))
				m.shadowTracker.RecordThermalBatterySkipped("outdoor temp sensor unavailable")
				return
			}
			parsed, err := strconv.ParseFloat(outdoorState.State, 64)
			if err != nil {
				m.logger.Warn("Skipping thermal battery: could not parse outdoor temp",
					zap.String("sensor", outdoorTempSensor), zap.String("value", outdoorState.State), zap.Error(err))
				m.shadowTracker.RecordThermalBatterySkipped("outdoor temp not parseable")
				return
			}
			outdoorTemp = parsed

			for _, sp := range savedSetpoints {
				if sp.HVACMode == "heat_cool" || sp.HVACMode == "auto" {
					skipLow := sp.TargetLow - thermalBatterySkipMargin
					skipHigh := sp.TargetHigh + thermalBatterySkipMargin
					if outdoorTemp >= skipLow && outdoorTemp <= skipHigh {
						m.logger.Info("Skipping thermal battery: outdoor temp within skip zone (fallback)",
							zap.Float64("outdoor_temp", outdoorTemp),
							zap.Float64("skip_low", skipLow),
							zap.Float64("skip_high", skipHigh))
						m.shadowTracker.RecordThermalBatterySkipped(
							fmt.Sprintf("outdoor temp %.1f°F within skip zone (%.0f-%.0f°F) (fallback)", outdoorTemp, skipLow, skipHigh))
						return
					}
					if outdoorTemp < skipLow {
						direction = "up"
					} else {
						direction = "down"
					}
					break
				}
			}
		}
	} else {
		// For single-mode thermostats, determine direction from HVAC mode
		for _, sp := range savedSetpoints {
			switch sp.HVACMode {
			case "heat":
				direction = "up"
			case "cool":
				direction = "down"
			}
			break
		}
	}

	// Enable thermostat hold mode BEFORE shifting setpoints.
	// Without hold, the thermostat's built-in schedule can immediately override the shifted setpoints.
	if m.readOnly {
		m.logger.Info("READ-ONLY: Would enable thermostat hold mode for thermal battery",
			zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))
	} else {
		m.logger.Info("Thermal battery: enabling thermostat hold mode",
			zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))
		if err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
			"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
		}); err != nil {
			m.logger.Error("Failed to enable thermostat hold mode for thermal battery", zap.Error(err))
			return
		}
	}

	// Calculate total steps needed
	totalSteps := int(math.Ceil(thermalBatteryOffset / thermalBatteryStepSize))
	if totalSteps < 1 {
		totalSteps = 1
	}

	// Save state before applying first step
	m.savedSetpoints = savedSetpoints
	m.thermalBatteryDirection = direction
	m.thermalBatteryTargetSteps = totalSteps
	m.thermalBatteryStepsDone = 0

	// Apply first step
	if err := m.applyThermalBatteryStep(1, direction); err != nil {
		m.logger.Error("Failed to apply first thermal battery step", zap.Error(err))
		return
	}
	m.thermalBatteryStepsDone = 1
	m.thermalBatteryStepStart = time.Now()

	m.thermalBatteryActive = true

	directionLabel := ""
	if direction == "up" {
		directionLabel = "UP (pre-heat)"
	} else {
		directionLabel = "DOWN (pre-cool)"
	}

	m.logger.Info("=== THERMAL BATTERY ACTIVATED ===",
		zap.Float64("step_size_f", thermalBatteryStepSize),
		zap.Int("steps_completed", 1),
		zap.Int("total_steps", totalSteps),
		zap.Float64("full_offset_f", thermalBatteryOffset),
		zap.Int("thermostats_adjusted", len(savedSetpoints)),
		zap.String("direction", directionLabel))

	m.shadowTracker.RecordThermalBatteryActivation(thermalBatteryStepSize, savedSetpoints)
	m.shadowTracker.RecordThermalBatteryStepProgress(1, totalSteps, thermalBatteryStepSize)

	// Send push notification
	if m.ntfyClient != nil && directionLabel != "" {
		body := fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d)", directionLabel, thermalBatteryOffset, totalSteps)
		if hasHeatCool && usedForecast {
			body = fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d, forecast high: %.0f°F, low: %.0f°F)", directionLabel, thermalBatteryOffset, totalSteps, forecastHigh, forecastLow)
		} else if hasHeatCool {
			body = fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d, outdoor: %.1f°F)", directionLabel, thermalBatteryOffset, totalSteps, outdoorTemp)
		}
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    "Thermal Battery Activated",
			Body:     body,
			Priority: ntfy.PriorityDefault,
			Tags:     []string{"thermometer", "sunny"},
		}); err != nil {
			m.logger.Error("Failed to send thermal battery ntfy notification", zap.Error(err))
		}
	}

	// If more steps needed, launch stepping goroutine
	if totalSteps > 1 {
		cancelCh := make(chan struct{})
		m.thermalBatteryStepCancel = cancelCh
		go m.runThermalBatteryStepping(cancelCh)
	}
}

// applyThermalBatteryStep applies a single step offset to all thermostats.
// stepNumber is 1-based. The cumulative offset is stepNumber * thermalBatteryStepSize,
// capped at thermalBatteryOffset. Uses savedSetpoints as the base (original values).
// Must be called with thermalBatteryMu held.
func (m *Manager) applyThermalBatteryStep(stepNumber int, direction string) error {
	offset := float64(stepNumber) * thermalBatteryStepSize
	if offset > thermalBatteryOffset {
		offset = thermalBatteryOffset
	}

	for entityID, sp := range m.savedSetpoints {
		if m.readOnly {
			m.logger.Info("READ-ONLY: Would shift thermostat setpoint for thermal battery step",
				zap.String("entity", entityID),
				zap.Int("step", stepNumber),
				zap.Float64("offset", offset))
			continue
		}

		data := map[string]interface{}{
			"entity_id": entityID,
		}

		switch sp.HVACMode {
		case "cool":
			data["temperature"] = sp.TargetTemp - offset
			m.logger.Info("Thermal battery step: lowering cooling setpoint",
				zap.String("entity", entityID),
				zap.Int("step", stepNumber),
				zap.Float64("original", sp.TargetTemp),
				zap.Float64("shifted", sp.TargetTemp-offset))
		case "heat":
			data["temperature"] = sp.TargetTemp + offset
			m.logger.Info("Thermal battery step: raising heating setpoint",
				zap.String("entity", entityID),
				zap.Int("step", stepNumber),
				zap.Float64("original", sp.TargetTemp),
				zap.Float64("shifted", sp.TargetTemp+offset))
		case "heat_cool", "auto":
			if direction == "up" {
				data["target_temp_low"] = sp.TargetLow + offset
				data["target_temp_high"] = sp.TargetHigh + offset
				m.logger.Info("Thermal battery step: shifting heat_cool band UP",
					zap.String("entity", entityID),
					zap.Int("step", stepNumber),
					zap.Float64("shifted_low", sp.TargetLow+offset),
					zap.Float64("shifted_high", sp.TargetHigh+offset))
			} else {
				data["target_temp_low"] = sp.TargetLow - offset
				data["target_temp_high"] = sp.TargetHigh - offset
				m.logger.Info("Thermal battery step: shifting heat_cool band DOWN",
					zap.String("entity", entityID),
					zap.Int("step", stepNumber),
					zap.Float64("shifted_low", sp.TargetLow-offset),
					zap.Float64("shifted_high", sp.TargetHigh-offset))
			}
		}

		if err := m.haClient.CallService(m.ctx, "climate", "set_temperature", data); err != nil {
			return fmt.Errorf("failed to set thermal battery temperature for %s: %w", entityID, err)
		}
	}
	return nil
}

// runThermalBatteryStepping is a goroutine that polls thermostat current_temperature
// and applies subsequent steps once the HVAC has reached near the current stepped target.
// cancelCh is a local copy of the cancel channel, safe to read without holding the lock.
func (m *Manager) runThermalBatteryStepping(cancelCh <-chan struct{}) {
	pollTicker := time.NewTicker(m.thermalBatteryPollInt)
	defer pollTicker.Stop()

	for {
		select {
		case <-cancelCh:
			m.logger.Info("Thermal battery stepping cancelled")
			return
		case <-m.ctx.Done():
			m.logger.Info("Thermal battery stepping stopped: context cancelled")
			return
		case <-pollTicker.C:
			m.thermalBatteryMu.Lock()

			// Safety: check if deactivated between select and lock acquire
			if !m.thermalBatteryActive {
				m.thermalBatteryMu.Unlock()
				return
			}

			nextStep := m.thermalBatteryStepsDone + 1
			if nextStep > m.thermalBatteryTargetSteps {
				// All steps done
				m.thermalBatteryMu.Unlock()
				return
			}

			// Check if thermostats have reached the current stepped target
			readyToAdvance := m.checkThermostatsReachedTarget()

			// Safety timeout: if we've been waiting too long for the thermostat to reach
			// the target (e.g., sensor offline or stale data), force-advance to avoid being
			// stuck at an intermediate step indefinitely.
			if !readyToAdvance {
				if time.Since(m.thermalBatteryStepStart) < m.thermalBatteryMaxStepWaitDur {
					m.thermalBatteryMu.Unlock()
					continue
				}
				m.logger.Warn("Thermal battery safety timeout: forcing step advancement",
					zap.Int("current_step", m.thermalBatteryStepsDone),
					zap.Int("next_step", nextStep),
					zap.Duration("waited", time.Since(m.thermalBatteryStepStart)),
					zap.Duration("max_wait", m.thermalBatteryMaxStepWaitDur))
			}

			// Apply next step
			if err := m.applyThermalBatteryStep(nextStep, m.thermalBatteryDirection); err != nil {
				m.logger.Error("Failed to apply thermal battery step",
					zap.Int("step", nextStep), zap.Error(err))
				m.thermalBatteryMu.Unlock()
				continue
			}
			m.thermalBatteryStepsDone = nextStep
			m.thermalBatteryStepStart = time.Now()

			m.logger.Info("Thermal battery step applied",
				zap.Int("step", nextStep),
				zap.Int("total_steps", m.thermalBatteryTargetSteps))

			m.shadowTracker.RecordThermalBatteryStepProgress(nextStep, m.thermalBatteryTargetSteps, thermalBatteryStepSize)

			allDone := nextStep >= m.thermalBatteryTargetSteps
			cb := m.thermalBatteryStepDoneCallback

			m.thermalBatteryMu.Unlock()

			// Notify test callback outside lock
			if cb != nil {
				cb(nextStep)
			}

			if allDone {
				m.logger.Info("Thermal battery stepping complete - all steps applied",
					zap.Int("total_steps", nextStep))
				return
			}
		}
	}
}

// checkThermostatsReachedTarget checks if all thermostats' current_temperature
// is within 1°F of the current stepped setpoint target.
// Must be called with thermalBatteryMu held.
func (m *Manager) checkThermostatsReachedTarget() bool {
	currentStepOffset := float64(m.thermalBatteryStepsDone) * thermalBatteryStepSize
	if currentStepOffset > thermalBatteryOffset {
		currentStepOffset = thermalBatteryOffset
	}

	for entityID, sp := range m.savedSetpoints {
		state, err := m.haClient.GetState(entityID)
		if err != nil {
			m.logger.Warn("Failed to read current_temperature for stepping check",
				zap.String("entity", entityID), zap.Error(err))
			return false
		}

		currentTemp, ok := state.Attributes["current_temperature"].(float64)
		if !ok {
			m.logger.Warn("current_temperature not available for stepping check",
				zap.String("entity", entityID))
			return false
		}

		switch sp.HVACMode {
		case "heat":
			target := sp.TargetTemp + currentStepOffset
			if currentTemp < target-1.0 {
				return false
			}
		case "cool":
			target := sp.TargetTemp - currentStepOffset
			if currentTemp > target+1.0 {
				return false
			}
		case "heat_cool", "auto":
			if m.thermalBatteryDirection == "up" {
				// Heating direction: check if current temp reached near the shifted heat setpoint
				target := sp.TargetLow + currentStepOffset
				if currentTemp < target-1.0 {
					return false
				}
			} else {
				// Cooling direction: check if current temp reached near the shifted cool setpoint
				target := sp.TargetHigh - currentStepOffset
				if currentTemp > target+1.0 {
					return false
				}
			}
		}
	}

	return true
}

// stopThermalBatteryStepping cancels the stepping goroutine if running.
// Must be called with thermalBatteryMu held.
func (m *Manager) stopThermalBatteryStepping() {
	if m.thermalBatteryStepCancel != nil {
		close(m.thermalBatteryStepCancel)
		m.thermalBatteryStepCancel = nil
	}
}

// deactivateThermalBattery reverts HVAC setpoints to their original values.
func (m *Manager) deactivateThermalBattery(reason string) {
	m.thermalBatteryMu.Lock()
	defer m.thermalBatteryMu.Unlock()

	if !m.thermalBatteryActive {
		return
	}

	m.logger.Info("Deactivating thermal battery",
		zap.String("reason", reason))

	// Stop stepping goroutine first
	m.stopThermalBatteryStepping()

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would revert thermostat setpoints for thermal battery deactivation")
		m.thermalBatteryActive = false
		m.savedSetpoints = nil
		m.thermalBatteryStepsDone = 0
		m.thermalBatteryTargetSteps = 0
		m.thermalBatteryDirection = ""
		m.thermalBatteryStepStart = time.Time{}
		m.shadowTracker.RecordThermalBatteryDeactivation()
		return
	}

	// Revert each thermostat to its saved setpoint
	for entityID, sp := range m.savedSetpoints {
		data := map[string]interface{}{
			"entity_id": entityID,
		}

		switch sp.HVACMode {
		case "cool", "heat":
			data["temperature"] = sp.TargetTemp
			m.logger.Info("Thermal battery: reverting setpoint",
				zap.String("entity", entityID),
				zap.Float64("restored_temp", sp.TargetTemp))
		case "heat_cool", "auto":
			data["target_temp_low"] = sp.TargetLow
			data["target_temp_high"] = sp.TargetHigh
			m.logger.Info("Thermal battery: reverting heat_cool setpoints",
				zap.String("entity", entityID),
				zap.Float64("restored_low", sp.TargetLow),
				zap.Float64("restored_high", sp.TargetHigh))
		}

		if err := m.haClient.CallService(m.ctx, "climate", "set_temperature", data); err != nil {
			m.logger.Error("Failed to revert thermal battery temperature",
				zap.String("entity", entityID), zap.Error(err))
			// Continue trying other thermostats
		}
	}

	// Disable thermostat hold mode AFTER reverting setpoints,
	// so the schedule resumes with the original temperatures in place.
	m.logger.Info("Thermal battery: disabling thermostat hold mode",
		zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))
	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
	}); err != nil {
		m.logger.Error("Failed to disable thermostat hold mode for thermal battery", zap.Error(err))
		// Continue with deactivation anyway - holds can be manually cleared
	}

	m.thermalBatteryActive = false
	m.savedSetpoints = nil
	m.thermalBatteryStepsDone = 0
	m.thermalBatteryTargetSteps = 0
	m.thermalBatteryDirection = ""
	m.thermalBatteryStepStart = time.Time{}

	m.logger.Info("=== THERMAL BATTERY DEACTIVATED ===",
		zap.String("reason", reason))

	m.shadowTracker.RecordThermalBatteryDeactivation()
}

// handlePresenceChange handles changes to presence/sleep states.
// If thermal battery is active and conditions no longer apply, deactivate it.
func (m *Manager) handlePresenceChange(key string, oldValue, newValue interface{}) {
	m.thermalBatteryMu.Lock()
	isActive := m.thermalBatteryActive
	m.thermalBatteryMu.Unlock()

	if !isActive {
		return
	}

	// Check if conditions still hold
	switch key {
	case "isAnyoneHome":
		if newVal, ok := newValue.(bool); ok && !newVal {
			m.deactivateThermalBattery("no one is home")
		}
	case "isEveryoneAsleep":
		if newVal, ok := newValue.(bool); ok && newVal {
			m.deactivateThermalBattery("everyone is asleep")
		}
	}
}

// IsThermalBatteryActive returns whether thermal battery is currently active (thread-safe)
func (m *Manager) IsThermalBatteryActive() bool {
	m.thermalBatteryMu.Lock()
	defer m.thermalBatteryMu.Unlock()
	return m.thermalBatteryActive
}
