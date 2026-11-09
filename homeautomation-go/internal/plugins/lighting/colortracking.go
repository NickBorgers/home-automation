package lighting

import (
	"fmt"
	"reflect"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

func (m *Manager) startColorTracking() error {
	config := m.config.ColorTracking
	if config == nil {
		return nil
	}

	if err := m.subHelper.SubscribeToEntity(config.SourceEntity, m.handleColorSourceChange); err != nil {
		return fmt.Errorf("subscribe to source %s: %w", config.SourceEntity, err)
	}
	if err := m.subHelper.SubscribeToEntity(config.TargetEntity, m.handleColorTargetChange); err != nil {
		return fmt.Errorf("subscribe to target %s: %w", config.TargetEntity, err)
	}

	m.updateColorTrackingShadow(nil)
	if source, err := m.haClient.GetState(config.SourceEntity); err == nil {
		m.cacheSourceColor(source)
	}
	return nil
}

func (m *Manager) handleColorSourceChange(_ string, _, newState *ha.State) {
	if !m.cacheSourceColor(newState) || !m.colorTargetIsOn() {
		return
	}
	m.applyCachedColor("source color changed while target is on")
}

func (m *Manager) handleColorTargetChange(_ string, oldState, newState *ha.State) {
	if newState == nil || newState.State != "on" || (oldState != nil && oldState.State == "on") {
		return
	}
	m.applyCachedColor("target turned on")
}

func (m *Manager) cacheSourceColor(source *ha.State) bool {
	color, ok := colorFromState(source)
	if !ok {
		return false
	}
	m.colorTrackingMu.Lock()
	if reflect.DeepEqual(m.cachedColor, color) {
		m.colorTrackingMu.Unlock()
		return false
	}
	m.cachedColor = color
	m.colorTrackingMu.Unlock()
	m.updateColorTrackingShadow(nil)
	m.logger.Debug("Cached source light color",
		zap.String("source", m.config.ColorTracking.SourceEntity),
		zap.String("color_mode", color.Mode))
	return true
}

func (m *Manager) colorTargetIsOn() bool {
	target, err := m.haClient.GetState(m.config.ColorTracking.TargetEntity)
	return err == nil && target.State == "on"
}

func (m *Manager) applyCachedColor(reason string) {
	m.colorTrackingMu.Lock()
	color := copyLightColor(m.cachedColor)
	m.colorTrackingMu.Unlock()
	if color == nil {
		m.logger.Debug("No cached source color to apply", zap.String("reason", reason))
		return
	}

	data := map[string]interface{}{"entity_id": m.config.ColorTracking.TargetEntity}
	switch color.Mode {
	case "color_temp":
		data["color_temp_kelvin"] = color.ColorTempKelvin
	case "xy":
		data["xy_color"] = color.XYColor
	case "hs":
		data["hs_color"] = color.HSColor
	default:
		return
	}

	if m.readOnly {
		m.logger.Info("Read-only mode: would apply tracked light color", zap.String("reason", reason), zap.Any("data", data))
		return
	}
	if err := m.haClient.CallService(m.ctx, "light", "turn_on", data); err != nil {
		m.logger.Error("Failed to apply tracked light color", zap.String("reason", reason), zap.Error(err))
		return
	}
	m.updateColorTrackingShadow(color)
	m.shadowTracker.SnapshotInputsForAction()
	m.logger.Info("Applied tracked light color",
		zap.String("target", m.config.ColorTracking.TargetEntity),
		zap.String("color_mode", color.Mode),
		zap.String("reason", reason))
}

func (m *Manager) updateColorTrackingShadow(lastApplied *shadowstate.TrackedLightColor) {
	m.colorTrackingMu.Lock()
	if lastApplied != nil {
		m.lastAppliedColor = copyLightColor(lastApplied)
	}
	cached := copyLightColor(m.cachedColor)
	applied := copyLightColor(m.lastAppliedColor)
	m.colorTrackingMu.Unlock()

	m.shadowTracker.UpdateColorTracking(shadowstate.ColorTrackingState{
		SourceEntity: m.config.ColorTracking.SourceEntity,
		TargetEntity: m.config.ColorTracking.TargetEntity,
		CachedColor:  cached,
		LastApplied:  applied,
	})
}

// colorFromState uses HA's active color_mode first. If it is absent, the fallback precedence is
// color_temp_kelvin, xy_color, then hs_color. Exactly one representation is sent to light.turn_on.
func colorFromState(state *ha.State) (*shadowstate.TrackedLightColor, bool) {
	if state == nil || state.State != "on" {
		return nil, false
	}
	mode, _ := state.Attributes["color_mode"].(string)
	if mode == "color_temp" {
		if kelvin, ok := positiveInt(state.Attributes["color_temp_kelvin"]); ok {
			return &shadowstate.TrackedLightColor{Mode: mode, ColorTempKelvin: kelvin}, true
		}
	}
	if mode == "xy" {
		if xy, ok := twoFloats(state.Attributes["xy_color"]); ok {
			return &shadowstate.TrackedLightColor{Mode: mode, XYColor: xy}, true
		}
	}
	if mode == "hs" {
		if hs, ok := twoFloats(state.Attributes["hs_color"]); ok {
			return &shadowstate.TrackedLightColor{Mode: mode, HSColor: hs}, true
		}
	}
	if kelvin, ok := positiveInt(state.Attributes["color_temp_kelvin"]); ok {
		return &shadowstate.TrackedLightColor{Mode: "color_temp", ColorTempKelvin: kelvin}, true
	}
	if xy, ok := twoFloats(state.Attributes["xy_color"]); ok {
		return &shadowstate.TrackedLightColor{Mode: "xy", XYColor: xy}, true
	}
	if hs, ok := twoFloats(state.Attributes["hs_color"]); ok {
		return &shadowstate.TrackedLightColor{Mode: "hs", HSColor: hs}, true
	}
	return nil, false
}

func positiveInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), v > 0
	case int:
		return v, v > 0
	default:
		return 0, false
	}
}

func twoFloats(value interface{}) ([]float64, bool) {
	if values, ok := value.([]float64); ok && len(values) == 2 {
		return append([]float64(nil), values...), true
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != 2 {
		return nil, false
	}
	result := make([]float64, 2)
	for i, value := range values {
		result[i], ok = value.(float64)
		if !ok {
			return nil, false
		}
	}
	return result, true
}

func copyLightColor(color *shadowstate.TrackedLightColor) *shadowstate.TrackedLightColor {
	if color == nil {
		return nil
	}
	result := *color
	result.XYColor = append([]float64(nil), color.XYColor...)
	result.HSColor = append([]float64(nil), color.HSColor...)
	return &result
}
