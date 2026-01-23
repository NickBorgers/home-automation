package state

import "go.uber.org/zap"

// SetupComputedState initializes computed state variables and sets up
// subscriptions to automatically recompute them when dependencies change.
//
// Computed state variables are derived from other state variables:
// - isAnyoneHomeAndAwake = (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere || wakeSequenceLatch
//
// Note: Tori doesn't have a sleep state tracked, so her presence means someone
// is home AND awake by definition. The formula accounts for this by including
// isToriHere as an independent condition.
//
// Wake Sequence Latch:
// When Nick's alarm triggers (isWakeSequenceActive becomes true), the rest of the
// house should wake up (lights on) even though Caroline may still be asleep in
// the bedroom. The latch ensures isAnyoneHomeAndAwake stays true during the wake
// sequence, regardless of the normal formula result.
//
// The latch activates on the rising edge of isWakeSequenceActive (false->true)
// and clears when isMasterAsleep becomes false (person wakes up). This prevents
// the latch from activating if isWakeSequenceActive is already true at startup.
func (m *Manager) SetupComputedState() error {
	// Compute initial value (latch is false at startup, no edge detection on init)
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

	// Subscribe to isWakeSequenceActive for latch activation (rising edge only)
	_, err = m.Subscribe("isWakeSequenceActive", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)
		// Latch activates on rising edge only (false -> true)
		if !oldBool && newBool {
			m.cacheMu.Lock()
			m.wakeSequenceLatch = true
			m.cacheMu.Unlock()
			m.logger.Info("Wake sequence latch activated",
				zap.Bool("isWakeSequenceActive", newBool))
			if err := m.recomputeAnyoneHomeAndAwake(); err != nil {
				m.logger.Error("Failed to recompute isAnyoneHomeAndAwake",
					zap.String("trigger", key),
					zap.Error(err))
			}
		}
		// Note: latch does NOT clear when isWakeSequenceActive becomes false.
		// It only clears when isMasterAsleep becomes false (person wakes up).
	})
	if err != nil {
		return err
	}

	// Subscribe to isMasterAsleep for latch clearing (falling edge only)
	_, err = m.Subscribe("isMasterAsleep", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)
		// Clear latch when person wakes up (true -> false)
		if oldBool && !newBool {
			m.cacheMu.Lock()
			wasLatched := m.wakeSequenceLatch
			m.wakeSequenceLatch = false
			m.cacheMu.Unlock()
			if wasLatched {
				m.logger.Info("Wake sequence latch cleared",
					zap.Bool("isMasterAsleep", newBool))
			}
			// Recompute even if latch wasn't set - isMasterAsleep affects isAnyoneAsleep
			// which is a direct dependency, but the recompute will be triggered by that
			// subscription. We only recompute here if the latch was cleared.
			if wasLatched {
				if err := m.recomputeAnyoneHomeAndAwake(); err != nil {
					m.logger.Error("Failed to recompute isAnyoneHomeAndAwake",
						zap.String("trigger", key),
						zap.Error(err))
				}
			}
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
// Formula: isAnyoneHomeAndAwake = (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere || wakeSequenceLatch
//
// This formula correctly handles the case where Tori arrives while owners are asleep.
// Since Tori doesn't have a sleep state tracked, her presence means someone is
// home AND awake by definition.
//
// The wakeSequenceLatch ensures that when Nick's alarm triggers, the rest of the
// house wakes up (lights on) even though Caroline may still be asleep in the bedroom.
// See SetupComputedState for details on latch activation and clearing.
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

	// Read latch under lock
	m.cacheMu.RLock()
	wakeSequenceLatch := m.wakeSequenceLatch
	m.cacheMu.RUnlock()

	newValue := (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere || wakeSequenceLatch

	// Get current value to check if it changed
	currentValue, _ := m.GetBool("isAnyoneHomeAndAwake")
	if currentValue != newValue {
		m.logger.Debug("Recomputing isAnyoneHomeAndAwake",
			zap.Bool("isAnyOwnerHome", isAnyOwnerHome),
			zap.Bool("isAnyoneAsleep", isAnyoneAsleep),
			zap.Bool("isToriHere", isToriHere),
			zap.Bool("wakeSequenceLatch", wakeSequenceLatch),
			zap.Bool("result", newValue))
	}

	return m.SetBool("isAnyoneHomeAndAwake", newValue)
}
