package loadshedding

import (
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// activateThermalBattery shifts HVAC setpoints to pre-condition the house when energy is abundant (white level).
// It reads current thermostat state, saves the original setpoints, and shifts by thermalBatteryOffset°F
// in the comfort direction (cooler in summer/cool mode, warmer in winter/heat mode).
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

	// Apply offset to each thermostat
	for entityID, sp := range savedSetpoints {
		if m.readOnly {
			m.logger.Info("READ-ONLY: Would shift thermostat setpoint for thermal battery",
				zap.String("entity", entityID),
				zap.String("hvac_mode", sp.HVACMode),
				zap.Float64("offset", thermalBatteryOffset))
			continue
		}

		data := map[string]interface{}{
			"entity_id": entityID,
		}

		switch sp.HVACMode {
		case "cool":
			// In cooling mode, shift setpoint DOWN to pre-cool the house
			data["temperature"] = sp.TargetTemp - thermalBatteryOffset
			m.logger.Info("Thermal battery: lowering cooling setpoint",
				zap.String("entity", entityID),
				zap.Float64("original", sp.TargetTemp),
				zap.Float64("shifted", sp.TargetTemp-thermalBatteryOffset))
		case "heat":
			// In heating mode, shift setpoint UP to pre-heat the house
			data["temperature"] = sp.TargetTemp + thermalBatteryOffset
			m.logger.Info("Thermal battery: raising heating setpoint",
				zap.String("entity", entityID),
				zap.Float64("original", sp.TargetTemp),
				zap.Float64("shifted", sp.TargetTemp+thermalBatteryOffset))
		case "heat_cool", "auto":
			// In heat_cool mode, shift both bounds outward (more aggressive conditioning)
			data["target_temp_low"] = sp.TargetLow + thermalBatteryOffset
			data["target_temp_high"] = sp.TargetHigh - thermalBatteryOffset
			m.logger.Info("Thermal battery: shifting heat_cool setpoints",
				zap.String("entity", entityID),
				zap.Float64("original_low", sp.TargetLow),
				zap.Float64("original_high", sp.TargetHigh),
				zap.Float64("shifted_low", sp.TargetLow+thermalBatteryOffset),
				zap.Float64("shifted_high", sp.TargetHigh-thermalBatteryOffset))
		}

		if err := m.haClient.CallService(m.ctx, "climate", "set_temperature", data); err != nil {
			m.logger.Error("Failed to set thermal battery temperature",
				zap.String("entity", entityID), zap.Error(err))
			return
		}
	}

	m.thermalBatteryActive = true
	m.savedSetpoints = savedSetpoints

	m.logger.Info("=== THERMAL BATTERY ACTIVATED ===",
		zap.Float64("offset_degrees_f", thermalBatteryOffset),
		zap.Int("thermostats_adjusted", len(savedSetpoints)))

	m.shadowTracker.RecordThermalBatteryActivation(thermalBatteryOffset, savedSetpoints)
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

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would revert thermostat setpoints for thermal battery deactivation")
		m.thermalBatteryActive = false
		m.savedSetpoints = nil
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

	m.thermalBatteryActive = false
	m.savedSetpoints = nil

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
