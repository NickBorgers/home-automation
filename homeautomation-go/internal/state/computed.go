package state

import "go.uber.org/zap"

// SetupComputedState initializes computed state variables and sets up
// subscriptions to automatically recompute them when dependencies change.
//
// Computed state variables are derived from other state variables:
// - isAnyoneHomeAndAwake = (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
//
// Note: Tori doesn't have a sleep state tracked, so her presence means someone
// is home AND awake by definition. The formula accounts for this by including
// isToriHere as an independent condition.
func (m *Manager) SetupComputedState() error {
	// Compute initial value
	if err := m.recomputeAnyoneHomeAndAwake(); err != nil {
		return err
	}

	// Subscribe to dependency changes
	_, err := m.Subscribe("isAnyOwnerHome", func(key string, oldValue, newValue interface{}) {
		if err := m.recomputeAnyoneHomeAndAwake(); err != nil {
			m.logger.Error("Failed to recompute isAnyoneHomeAndAwake",
				zap.String("trigger", key),
				zap.Error(err))
		}
	})
	if err != nil {
		return err
	}

	_, err = m.Subscribe("isAnyoneAsleep", func(key string, oldValue, newValue interface{}) {
		if err := m.recomputeAnyoneHomeAndAwake(); err != nil {
			m.logger.Error("Failed to recompute isAnyoneHomeAndAwake",
				zap.String("trigger", key),
				zap.Error(err))
		}
	})
	if err != nil {
		return err
	}

	_, err = m.Subscribe("isToriHere", func(key string, oldValue, newValue interface{}) {
		if err := m.recomputeAnyoneHomeAndAwake(); err != nil {
			m.logger.Error("Failed to recompute isAnyoneHomeAndAwake",
				zap.String("trigger", key),
				zap.Error(err))
		}
	})
	if err != nil {
		return err
	}

	m.logger.Info("Computed state initialized",
		zap.Strings("variables", []string{"isAnyoneHomeAndAwake"}))

	return nil
}

// recomputeAnyoneHomeAndAwake computes isAnyoneHomeAndAwake from its dependencies.
// Formula: isAnyoneHomeAndAwake = (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
//
// This formula correctly handles the case where Tori arrives while owners are asleep.
// Since Tori doesn't have a sleep state tracked, her presence means someone is
// home AND awake by definition.
func (m *Manager) recomputeAnyoneHomeAndAwake() error {
	isAnyOwnerHome, err := m.GetBool("isAnyOwnerHome")
	if err != nil {
		return err
	}

	isAnyoneAsleep, err := m.GetBool("isAnyoneAsleep")
	if err != nil {
		return err
	}

	isToriHere, err := m.GetBool("isToriHere")
	if err != nil {
		return err
	}

	newValue := (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere

	// Get current value to check if it changed
	currentValue, _ := m.GetBool("isAnyoneHomeAndAwake")
	if currentValue != newValue {
		m.logger.Debug("Recomputing isAnyoneHomeAndAwake",
			zap.Bool("isAnyOwnerHome", isAnyOwnerHome),
			zap.Bool("isAnyoneAsleep", isAnyoneAsleep),
			zap.Bool("isToriHere", isToriHere),
			zap.Bool("result", newValue))
	}

	return m.SetBool("isAnyoneHomeAndAwake", newValue)
}
