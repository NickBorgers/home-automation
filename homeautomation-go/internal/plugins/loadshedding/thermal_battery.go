package loadshedding

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

type forecastEntry struct {
	Temperature float64 `json:"temperature"`
	TempLow     float64 `json:"templow"`
}

// hourlyForecastEntry represents a single hourly forecast entry from HA.
// Only Temperature and DateTime are used; other fields are ignored.
type hourlyForecastEntry struct {
	DateTime    string  `json:"datetime"`
	Temperature float64 `json:"temperature"`
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

	// The HA WebSocket API wraps entity data under a "response" key when using
	// call_service with return_response:true. Unwrap it before parsing entity data.
	var wrapper struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(result, &wrapper); err != nil {
		m.logger.Warn("Weather forecast response JSON malformed",
			zap.String("entity", entity), zap.Error(err))
		return 0, 0, false
	}
	if wrapper.Response == nil {
		m.logger.Warn("Weather forecast response missing 'response' key",
			zap.String("entity", entity))
		return 0, 0, false
	}

	var parsed map[string]struct {
		Forecast []forecastEntry `json:"forecast"`
	}
	if err := json.Unmarshal(wrapper.Response, &parsed); err != nil {
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

// getHourlyForecast fetches hourly forecast entries for thermal battery timing.
// Only entries with non-empty datetimes are returned; entries without datetimes
// (e.g., daily forecast data mistakenly fetched as hourly) are filtered out.
// Cached for forecastCacheDuration; failures negatively cached separately from daily.
// Shares forecastMu with getForecastHighLow.
func (m *Manager) getHourlyForecast() ([]hourlyForecastEntry, bool) {
	m.forecastMu.Lock()
	defer m.forecastMu.Unlock()

	// Return cached entries if still fresh
	if !m.hourlyForecastAt.IsZero() && time.Since(m.hourlyForecastAt) < forecastCacheDuration {
		return m.hourlyForecastEntries, true
	}

	// Negative cache: don't retry if we failed recently
	if !m.hourlyForecastFailedAt.IsZero() && time.Since(m.hourlyForecastFailedAt) < forecastNegativeCacheDuration {
		m.logger.Debug("Skipping hourly forecast fetch: within negative cache window",
			zap.Duration("since_failure", time.Since(m.hourlyForecastFailedAt)))
		return nil, false
	}

	entities := []string{forecastWeatherEntityPrimary, forecastWeatherEntitySecondary}
	for _, entity := range entities {
		entries, ok := m.tryFetchHourlyForecast(entity)
		if ok {
			m.hourlyForecastEntries = entries
			m.hourlyForecastAt = time.Now()
			m.hourlyForecastFailedAt = time.Time{} // clear negative cache on success
			m.logger.Info("Fetched hourly forecast for thermal battery timing",
				zap.String("source", entity),
				zap.Int("entries", len(entries)))
			return entries, true
		}
	}

	m.hourlyForecastFailedAt = time.Now()
	m.logger.Warn("All hourly forecast sources failed, cannot check timing")
	return nil, false
}

// tryFetchHourlyForecast fetches hourly forecast entries from a single weather entity.
// Filters out entries with empty datetimes (e.g., malformed or daily data served as hourly).
func (m *Manager) tryFetchHourlyForecast(entity string) ([]hourlyForecastEntry, bool) {
	result, err := m.haClient.CallServiceWithResponse(m.ctx, "weather", "get_forecasts", map[string]interface{}{
		"entity_id": entity,
		"type":      "hourly",
	})
	if err != nil {
		m.logger.Warn("Failed to fetch hourly weather forecast",
			zap.String("entity", entity), zap.Error(err))
		return nil, false
	}
	if result == nil {
		m.logger.Warn("Hourly weather forecast returned nil response",
			zap.String("entity", entity))
		return nil, false
	}

	var wrapper struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(result, &wrapper); err != nil || wrapper.Response == nil {
		m.logger.Warn("Hourly weather forecast response malformed",
			zap.String("entity", entity))
		return nil, false
	}

	var parsed map[string]struct {
		Forecast []hourlyForecastEntry `json:"forecast"`
	}
	if err := json.Unmarshal(wrapper.Response, &parsed); err != nil {
		m.logger.Warn("Failed to parse hourly forecast response",
			zap.String("entity", entity), zap.Error(err))
		return nil, false
	}

	entityData, ok := parsed[entity]
	if !ok || len(entityData.Forecast) == 0 {
		m.logger.Warn("No hourly forecast entries found",
			zap.String("entity", entity))
		return nil, false
	}

	// Filter out entries without parseable datetimes
	var valid []hourlyForecastEntry
	for _, e := range entityData.Forecast {
		if e.DateTime == "" {
			continue
		}
		valid = append(valid, e)
	}
	if len(valid) == 0 {
		m.logger.Warn("Hourly forecast has no entries with valid datetimes",
			zap.String("entity", entity))
		return nil, false
	}
	return valid, true
}

// findHourlyStressEvent scans hourly forecast entries (forward from now) to find the first
// hour where outdoor temp crosses outside the comfort band: below targetLow-margin or above
// targetHigh+margin. Returns (stressTime, stressTemp, direction, true) if found, or
// (zero, 0, "", false) if not. direction is "up" (pre-heat) or "down" (pre-cool).
func findHourlyStressEvent(entries []hourlyForecastEntry, targetLow, targetHigh, margin float64) (time.Time, float64, string, bool) {
	stressLow := targetLow - margin
	stressHigh := targetHigh + margin
	now := time.Now()

	for _, entry := range entries {
		// Parse datetime (RFC3339 with timezone, or without)
		t, err := time.Parse(time.RFC3339, entry.DateTime)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04:05", entry.DateTime)
			if err != nil {
				continue
			}
		}
		// Skip past entries
		if !t.After(now) {
			continue
		}
		if entry.Temperature < stressLow {
			return t, entry.Temperature, "up", true // cold stress — pre-heat
		}
		if entry.Temperature > stressHigh {
			return t, entry.Temperature, "down", true // hot stress — pre-cool
		}
	}
	return time.Time{}, 0, "", false
}

// formatForecastStress produces a human-readable description of the upcoming
// stress event for logging and shadow state, e.g. "up: 37.0°F at 05:00".
func formatForecastStress(direction string, stressTemp float64, stressTime time.Time) string {
	return fmt.Sprintf("%s: %.1f°F at %s", direction, stressTemp, stressTime.Local().Format("15:04"))
}

// stopDeferredActivationTimer cancels the deferred activation goroutine if running.
// Must be called with thermalBatteryMu held.
func (m *Manager) stopDeferredActivationTimer() {
	if m.thermalBatteryDeferCancel != nil {
		close(m.thermalBatteryDeferCancel)
		m.thermalBatteryDeferCancel = nil
	}
}

// runDeferredActivationTimer periodically re-evaluates whether it is time to activate
// the thermal battery. It exits when the cancel channel is closed or context is done.
// cancelCh is a local copy of the cancel channel, safe to read without holding the lock.
func (m *Manager) runDeferredActivationTimer(cancelCh <-chan struct{}) {
	ticker := time.NewTicker(m.thermalBatteryDeferredRecheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cancelCh:
			m.logger.Info("Deferred thermal battery timer cancelled")
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			// Check if still deferred before re-evaluating.
			// deactivateThermalBattery() may have cleared the deferred state and closed
			// cancelCh between our last select and now. Note: this guard alone does not
			// prevent activateThermalBattery() from running if deactivateThermalBattery()
			// races in after we release the lock below. activateThermalBattery() itself
			// re-checks the energy level under thermalBatteryMu, which closes that window.
			m.thermalBatteryMu.Lock()
			isDeferred := m.thermalBatteryDeferred
			m.thermalBatteryMu.Unlock()

			if !isDeferred {
				return
			}

			// Invalidate the hourly cache so activation re-fetches fresh forecast data.
			m.forecastMu.Lock()
			m.hourlyForecastAt = time.Time{}
			m.forecastMu.Unlock()

			m.activateThermalBattery()
		}
	}
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
		// If we're in hysteresis, energy returning to white means resume preheat:
		// cancel the hysteresis timer and re-apply the most recent step to the same
		// saved setpoints, restoring the shifted band.
		if m.thermalBatteryHysteresisActive {
			m.resumePreheatFromHysteresisLocked()
			return
		}
		m.logger.Info("Thermal battery already active, skipping activation")
		return
	}

	// Guard: energy must still be white. This is the primary trigger condition; if energy
	// dropped between the caller's check and acquiring the lock (TOCTOU), abort safely.
	// This also defends against the deferred-timer race: deactivateThermalBattery() may
	// run after the timer goroutine releases thermalBatteryMu but before we re-acquire it,
	// so energy may no longer be white by the time we get here.
	energyLevel, err := m.stateManager.GetString("currentEnergyLevel")
	if err != nil || energyLevel != "white" {
		m.logger.Info("Skipping thermal battery: energy no longer white",
			zap.String("energy_level", energyLevel))
		m.shadowTracker.RecordThermalBatterySkipped("energy not white")
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
	if !m.clearStaleThermostatHolds() {
		return
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
	usedHourlyForecast := false
	var forecastHigh, forecastLow float64
	var hourlyStressTime time.Time
	var hourlyStressTemp float64
	for _, sp := range savedSetpoints {
		if sp.HVACMode == "heat_cool" || sp.HVACMode == "auto" {
			hasHeatCool = true
			break
		}
	}

	if hasHeatCool {
		// Try hourly forecast first for precise direction + stress detection (issue #1002).
		// If hourly data is available, use it to determine direction AND check whether
		// it is time to activate based on the solar production tail. The goal of the
		// thermal battery is to pre-condition the house using solar, not battery — so
		// activation is deferred while remainingSolarGeneration still exceeds the
		// configured tail threshold. This naturally targets the afternoon solar tail.
		//
		// Falls back to daily forecast / outdoor temp when hourly is unavailable.
		hourlyEntries, hasHourly := m.getHourlyForecast()
		if hasHourly {
			// Use the first heat_cool thermostat's setpoints for the stress scan
			for _, sp := range savedSetpoints {
				if sp.HVACMode != "heat_cool" && sp.HVACMode != "auto" {
					continue
				}

				stressTime, stressTemp, stressDir, hasStress := findHourlyStressEvent(
					hourlyEntries, sp.TargetLow, sp.TargetHigh, thermalBatteryHourlyComfortMargin)

				if !hasStress {
					// No stress in forecast window — thermal battery not needed
					m.logger.Info("Skipping thermal battery: no thermal stress in hourly forecast window",
						zap.Float64("comfort_margin_f", thermalBatteryHourlyComfortMargin))
					m.shadowTracker.RecordThermalBatterySkipped("no thermal stress in hourly forecast window")
					return
				}

				direction = stressDir
				forecastStress := formatForecastStress(stressDir, stressTemp, stressTime)

				// Solar-tail gate: defer activation until the remaining solar
				// production drops below the tail threshold.
				remainingKWh, err := m.stateManager.GetNumber("remainingSolarGeneration")
				if err != nil {
					m.logger.Info("remainingSolarGeneration unavailable, activating without solar-tail gate",
						zap.Error(err))
				}
				if err == nil && remainingKWh > m.thermalBatterySolarTailThresholdKWh {
					deferReason := fmt.Sprintf(
						"solar tail not yet reached (remaining: %.1f kWh, threshold: %.1f kWh)",
						remainingKWh, m.thermalBatterySolarTailThresholdKWh)

					m.thermalBatteryDeferred = true

					// Start re-evaluation timer if not already running
					if m.thermalBatteryDeferCancel == nil {
						cancelCh := make(chan struct{})
						m.thermalBatteryDeferCancel = cancelCh
						go m.runDeferredActivationTimer(cancelCh)
					}

					m.shadowTracker.RecordThermalBatteryDeferred(
						deferReason, stressDir, forecastStress,
						remainingKWh, m.thermalBatterySolarTailThresholdKWh)
					m.logger.Info("Thermal battery deferred: solar tail not yet reached",
						zap.Float64("remaining_solar_kwh", remainingKWh),
						zap.Float64("threshold_kwh", m.thermalBatterySolarTailThresholdKWh),
						zap.String("stress_direction", stressDir),
						zap.String("forecast_stress", forecastStress))
					return
				}

				// Solar tail reached — proceed to activate.
				// Clear any deferred state (timer goroutine will see deferred=false and exit).
				if m.thermalBatteryDeferred {
					m.stopDeferredActivationTimer()
					m.thermalBatteryDeferred = false
				}

				usedHourlyForecast = true
				hourlyStressTime = stressTime
				hourlyStressTemp = stressTemp

				m.logger.Info("Thermal battery: solar tail reached, activating now",
					zap.String("stress_direction", stressDir),
					zap.String("forecast_stress", forecastStress))
				break
			}
		} else {
			// Hourly forecast unavailable — fall back to daily forecast / outdoor temp
			fHigh, fLow, hasForecast := m.getForecastHighLow()

			if hasForecast {
				usedForecast = true
				forecastHigh = fHigh
				forecastLow = fLow

				for _, sp := range savedSetpoints {
					if sp.HVACMode == "heat_cool" || sp.HVACMode == "auto" {
						skipLow := sp.TargetLow - thermalBatterySkipMargin
						skipHigh := sp.TargetHigh + thermalBatterySkipMargin

						if forecastHigh <= skipHigh && forecastLow >= skipLow {
							m.logger.Info("Skipping thermal battery: forecast within skip zone",
								zap.Float64("forecast_high", forecastHigh),
								zap.Float64("forecast_low", forecastLow),
								zap.Float64("skip_low", skipLow),
								zap.Float64("skip_high", skipHigh))
							m.shadowTracker.RecordThermalBatterySkipped(
								fmt.Sprintf("forecast high %.1f°F/low %.1f°F within skip zone (%.0f-%.0f°F)",
									forecastHigh, forecastLow, skipLow, skipHigh))
							return
						}

						if forecastHigh > skipHigh {
							direction = "down" // hot day coming, pre-cool
						} else {
							direction = "up" // cold night coming, pre-heat
						}
						break
					}
				}
			} else {
				// Fallback: use current outdoor temperature
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
								fmt.Sprintf("outdoor temp %.1f°F within skip zone (%.0f-%.0f°F) (fallback)",
									outdoorTemp, skipLow, skipHigh))
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

	// Send push and TTS notification
	if m.alerter != nil && directionLabel != "" {
		body := fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d)", directionLabel, thermalBatteryOffset, totalSteps)
		if hasHeatCool && usedHourlyForecast {
			tempLabel := "low"
			if direction == "down" {
				tempLabel = "high"
			}
			body = fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d, stress at %s, %s %.0f°F)",
				directionLabel, thermalBatteryOffset, totalSteps,
				hourlyStressTime.Local().Format("3:04 PM"), tempLabel, hourlyStressTemp)
		} else if hasHeatCool && usedForecast {
			body = fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d, forecast high: %.0f°F, low: %.0f°F)", directionLabel, thermalBatteryOffset, totalSteps, forecastHigh, forecastLow)
		} else if hasHeatCool {
			body = fmt.Sprintf("Shifting HVAC %s by %.0f°F (step 1/%d, outdoor: %.1f°F)", directionLabel, thermalBatteryOffset, totalSteps, outdoorTemp)
		}
		if err := m.alerter.Send(m.ctx, alert.Alert{
			Title:    "Thermal Battery Activated",
			Body:     body,
			Urgency:  notify.UrgencyDeferable,
			Tags:     []string{"thermometer", "sunny"},
			Priority: ntfy.PriorityDefault,
		}); err != nil {
			m.logger.Error("Failed to send thermal battery notification", zap.Error(err))
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

// deactivateThermalBattery reverts HVAC setpoints to their original values and clears
// any deferred or hysteresis state. Safe to call when not active (no-op) or when deferred.
// Used for hard-stop reasons (yellow/red/black drop, presence change) — the hysteresis-
// expiry path uses completeHysteresis instead, which does not revert setpoints.
func (m *Manager) deactivateThermalBattery(reason string) {
	m.thermalBatteryMu.Lock()
	defer m.thermalBatteryMu.Unlock()

	// Clear deferred state if present (even when not yet active)
	if m.thermalBatteryDeferred {
		m.logger.Info("Clearing deferred thermal battery activation", zap.String("reason", reason))
		m.stopDeferredActivationTimer()
		m.thermalBatteryDeferred = false
		m.shadowTracker.RecordThermalBatteryDeferredCleared()
	}

	// Cancel any in-flight hysteresis. We're doing a hard deactivation, so the
	// setpoint revert below is correct even though hysteresis had widened the band.
	if m.thermalBatteryHysteresisActive {
		m.stopHysteresisTimer()
		m.thermalBatteryHysteresisActive = false
		m.thermalBatteryHysteresisExpiresAt = time.Time{}
	}

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

// enterThermalBatteryHysteresis is called when energy dips white→green while thermal
// battery is active. Instead of reverting setpoints (which can trigger HVAC counter-runs),
// it widens the band so neither heating nor cooling engages, and starts a timer that
// completes hysteresis after thermalBatteryHysteresisDuration. If thermal battery is
// not active or already in hysteresis, this is a no-op.
func (m *Manager) enterThermalBatteryHysteresis() {
	m.thermalBatteryMu.Lock()
	defer m.thermalBatteryMu.Unlock()

	if !m.thermalBatteryActive {
		return
	}
	if m.thermalBatteryHysteresisActive {
		return
	}
	if m.savedSetpoints == nil {
		return
	}

	// Stop any in-flight stepping; we are entering coast mode.
	m.stopThermalBatteryStepping()

	currentOffset := float64(m.thermalBatteryStepsDone) * thermalBatteryStepSize
	if currentOffset > thermalBatteryOffset {
		currentOffset = thermalBatteryOffset
	}

	if !m.readOnly {
		for entityID, sp := range m.savedSetpoints {
			data := map[string]interface{}{
				"entity_id": entityID,
			}

			switch sp.HVACMode {
			case "heat_cool", "auto":
				// Wide band: keep the side we'd been preheating toward at the shifted value,
				// and loosen the opposite side back to the saved (original) value.
				var lowVal, highVal float64
				if m.thermalBatteryDirection == "up" {
					lowVal = sp.TargetLow                   // original (lower) low — stop heating
					highVal = sp.TargetHigh + currentOffset // shifted (higher) high — keep room
				} else {
					lowVal = sp.TargetLow - currentOffset // shifted (lower) low — keep room
					highVal = sp.TargetHigh               // original (higher) high — stop cooling
				}
				data["target_temp_low"] = lowVal
				data["target_temp_high"] = highVal
				m.logger.Info("Thermal battery: widening band for hysteresis",
					zap.String("entity", entityID),
					zap.Float64("wide_low", lowVal),
					zap.Float64("wide_high", highVal))
			case "heat", "cool":
				// Single-stage thermostats have only one setpoint; revert to the saved
				// target so the equipment stops calling for heating/cooling.
				data["temperature"] = sp.TargetTemp
				m.logger.Info("Thermal battery: reverting setpoint for hysteresis",
					zap.String("entity", entityID),
					zap.Float64("target", sp.TargetTemp))
			default:
				continue
			}

			if err := m.haClient.CallService(m.ctx, "climate", "set_temperature", data); err != nil {
				m.logger.Error("Failed to widen thermostat for hysteresis",
					zap.String("entity", entityID), zap.Error(err))
			}
		}
	}

	m.thermalBatteryHysteresisActive = true
	m.thermalBatteryHysteresisExpiresAt = time.Now().Add(m.thermalBatteryHysteresisDuration)

	cancelCh := make(chan struct{})
	m.thermalBatteryHysteresisCancel = cancelCh
	go m.runHysteresisTimer(cancelCh)

	m.logger.Info("=== THERMAL BATTERY HYSTERESIS ENTERED ===",
		zap.Duration("duration", m.thermalBatteryHysteresisDuration),
		zap.Time("expires_at", m.thermalBatteryHysteresisExpiresAt))

	m.shadowTracker.RecordThermalBatteryHysteresisEntered(m.thermalBatteryHysteresisExpiresAt)
}

// runHysteresisTimer waits for the hysteresis duration, then completes hysteresis.
// Exits early on cancellation (energy returned to white, or hard deactivation).
func (m *Manager) runHysteresisTimer(cancelCh <-chan struct{}) {
	timer := time.NewTimer(m.thermalBatteryHysteresisDuration)
	defer timer.Stop()

	select {
	case <-cancelCh:
		return
	case <-m.ctx.Done():
		return
	case <-timer.C:
		m.completeHysteresis()
	}
}

// stopHysteresisTimer cancels the hysteresis goroutine if running.
// Must be called with thermalBatteryMu held.
func (m *Manager) stopHysteresisTimer() {
	if m.thermalBatteryHysteresisCancel != nil {
		close(m.thermalBatteryHysteresisCancel)
		m.thermalBatteryHysteresisCancel = nil
	}
}

// completeHysteresis ends the hysteresis window successfully (timer expired):
// disables thermostat holds so the climate schedule resumes, and clears thermal
// battery active state. Does NOT explicitly revert setpoints — releasing the hold
// returns the thermostat to its schedule, which is more correct than restoring
// a possibly stale "saved" value from N hours ago.
func (m *Manager) completeHysteresis() {
	m.thermalBatteryMu.Lock()
	defer m.thermalBatteryMu.Unlock()

	if !m.thermalBatteryHysteresisActive {
		return
	}

	m.logger.Info("Thermal battery hysteresis window expired, releasing holds")

	if !m.readOnly {
		if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
			"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
		}); err != nil {
			m.logger.Error("Failed to disable thermostat hold mode after hysteresis", zap.Error(err))
		}
	}

	m.thermalBatteryHysteresisActive = false
	m.thermalBatteryHysteresisCancel = nil
	m.thermalBatteryHysteresisExpiresAt = time.Time{}

	m.thermalBatteryActive = false
	m.savedSetpoints = nil
	m.thermalBatteryStepsDone = 0
	m.thermalBatteryTargetSteps = 0
	m.thermalBatteryDirection = ""
	m.thermalBatteryStepStart = time.Time{}

	m.logger.Info("=== THERMAL BATTERY DEACTIVATED ===",
		zap.String("reason", "hysteresis window expired"))

	m.shadowTracker.RecordThermalBatteryDeactivation()
}

// resumePreheatFromHysteresisLocked re-applies the previously-active preheat band
// after energy returns to white during hysteresis. The savedSetpoints, direction,
// and step counter are still valid from the original activation, so we just re-issue
// the most recent step. Must be called with thermalBatteryMu held.
func (m *Manager) resumePreheatFromHysteresisLocked() {
	m.logger.Info("Thermal battery: resuming preheat from hysteresis")

	if m.thermalBatteryHysteresisCancel != nil {
		close(m.thermalBatteryHysteresisCancel)
		m.thermalBatteryHysteresisCancel = nil
	}
	m.thermalBatteryHysteresisActive = false
	m.thermalBatteryHysteresisExpiresAt = time.Time{}

	step := m.thermalBatteryStepsDone
	if step < 1 {
		step = 1
	}
	if err := m.applyThermalBatteryStep(step, m.thermalBatteryDirection); err != nil {
		m.logger.Error("Failed to resume preheat band from hysteresis", zap.Error(err))
		return
	}

	// If steps still remain, restart the stepping goroutine.
	if m.thermalBatteryStepsDone < m.thermalBatteryTargetSteps && m.thermalBatteryStepCancel == nil {
		cancelCh := make(chan struct{})
		m.thermalBatteryStepCancel = cancelCh
		m.thermalBatteryStepStart = time.Now()
		go m.runThermalBatteryStepping(cancelCh)
	}

	m.shadowTracker.RecordThermalBatteryHysteresisResumed()
}

// clearStaleThermostatHolds detects thermostat holds left on from a previous process
// (e.g., service restart mid-thermal-battery) and turns them off so the thermostat
// reverts to its scheduled setpoints. Safe to call when no holds are on (no-op).
//
// Returns true if it is safe to proceed (no error or recovery succeeded), false if
// a service call to turn off holds failed and the caller should abort.
//
// IMPORTANT: this must NOT run while load shedding is active — load shedding
// legitimately uses the same hold switches. Callers must guard against that.
func (m *Manager) clearStaleThermostatHolds() bool {
	holdOn, err := m.checkThermostatHoldState()
	if err != nil {
		m.logger.Warn("Failed to check thermostat hold state", zap.Error(err))
		return true
	}
	if !holdOn {
		return true
	}

	m.logger.Info("Clearing stale thermostat holds (likely from previous session), reverting to schedule",
		zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))

	if m.readOnly {
		return true
	}

	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
	}); err != nil {
		m.logger.Error("Failed to revert stale thermostat holds", zap.Error(err))
		return false
	}
	// Wait for the thermostat to revert to its scheduled setpoints
	time.Sleep(m.thermalBatteryHoldRevertDelay)
	return true
}

// handlePresenceChange handles changes to presence/sleep states.
// If thermal battery is active or deferred and conditions no longer apply, deactivate it.
func (m *Manager) handlePresenceChange(key string, oldValue, newValue interface{}) {
	m.thermalBatteryMu.Lock()
	isActive := m.thermalBatteryActive
	isDeferred := m.thermalBatteryDeferred
	m.thermalBatteryMu.Unlock()

	if !isActive && !isDeferred {
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
