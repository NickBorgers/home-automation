package state

import (
	"fmt"

	"go.uber.org/zap"
)

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
// and clears when isAnyoneAsleep becomes false (everyone wakes up). This prevents
// the latch from activating if isWakeSequenceActive is already true at startup.
//
// Note: We clear on isAnyoneAsleep (not isMasterAsleep) to avoid a race condition.
// When isMasterAsleep changes, isAnyoneAsleep (derived from it) needs time to propagate.
// Clearing on isAnyoneAsleep ensures state is consistent before the latch clears.
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
		// It only clears when isAnyoneAsleep becomes false (everyone wakes up).
	})
	if err != nil {
		return err
	}

	// Subscribe to isAnyoneAsleep for latch clearing (falling edge only)
	// NOTE: We subscribe to isAnyoneAsleep instead of isMasterAsleep to avoid a race condition.
	// When isMasterAsleep changes, isAnyoneAsleep (a derived state) needs time to propagate.
	// If we cleared the latch on isMasterAsleep, the recompute would read stale isAnyoneAsleep=true,
	// causing isAnyoneHomeAndAwake to briefly become false (lights flicker off then on).
	// By triggering on isAnyoneAsleep falling edge, we ensure state is consistent before clearing.
	// See GitHub issue #526 for details on this race condition.
	_, err = m.Subscribe("isAnyoneAsleep", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)
		// Clear latch when everyone wakes up (true -> false)
		if oldBool && !newBool {
			m.cacheMu.Lock()
			wasLatched := m.wakeSequenceLatch
			m.wakeSequenceLatch = false
			m.cacheMu.Unlock()
			if wasLatched {
				m.logger.Info("Wake sequence latch cleared",
					zap.Bool("isAnyoneAsleep", newBool))
			}
			// Note: recomputeAnyoneHomeAndAwake is already triggered by the isAnyoneAsleep
			// subscription above (lines 42-48), so we don't need to call it here again.
			// The subscription handler at lines 42-48 handles the recompute.
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

// SetupComputedStateV2 initializes computed state variables using the new
// ComputedStateRegistry. This is the recommended approach for new code.
//
// The registry provides:
// - Centralized dependency tracking
// - Automatic subscription management
// - Topological sorting for proper initialization order
// - Support for both dependency-triggered and periodic updates
//
// This method registers all basic computed states:
// - isAnyOwnerHome = isNickHome OR isCarolineHome
// - isAnyoneHome = isAnyOwnerHome OR isToriHere
// - isAnyoneAsleep = isMasterAsleep OR isGuestAsleep
// - isEveryoneAsleep = isMasterAsleep AND isGuestAsleep
// - isAnyoneHomeAndAwake = (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere || wakeSequenceLatch
//
// Note: This is distinct from SetupComputedState() which uses the legacy
// approach. Both methods are supported during the migration period.
func (m *Manager) SetupComputedStateV2() error {
	return m.SetupComputedStateV2WithEnergy(nil, nil)
}

// SetupComputedStateV2WithEnergy initializes computed state variables using the new
// ComputedStateRegistry, including energy-related computed states.
//
// The energyStates parameter provides the energy level configuration from the
// energy plugin config. If nil, energy computed states are not registered.
//
// The energyCallbacks parameter provides optional callbacks for shadow state
// updates when energy levels change.
//
// This method registers:
// - Basic presence/sleep computed states (isAnyOwnerHome, isAnyoneHome, etc.)
// - isAnyoneHomeAndAwake with wake sequence latch
// - Energy computed states (if energyStates is provided):
//   - solarProductionEnergyLevel = f(thisHourSolarGeneration, remainingSolarGeneration)
//   - currentEnergyLevel = f(isFreeEnergyAvailable, batteryEnergyLevel, solarProductionEnergyLevel)
//
// Note: batteryEnergyLevel is NOT registered as a computed state provider.
// It depends on raw sensor data that comes through HA subscriptions, not state
// variables. The energy plugin handles this directly.
func (m *Manager) SetupComputedStateV2WithEnergy(
	energyStates []EnergyStateConfig,
	energyCallbacks *EnergyComputedStateCallback,
) error {
	registry := m.GetComputedStateRegistry()
	latch := m.GetWakeSequenceLatch()

	// Register basic presence and sleep computed states
	if err := RegisterAllBasicProviders(registry, nil); err != nil {
		return fmt.Errorf("failed to register basic providers: %w", err)
	}

	// Register isAnyoneHomeAndAwake with wake sequence latch support
	if err := RegisterAnyoneHomeAndAwakeProvider(registry, latch, nil); err != nil {
		return fmt.Errorf("failed to register isAnyoneHomeAndAwake provider: %w", err)
	}

	// Register energy computed states if config provided
	if len(energyStates) > 0 {
		if err := RegisterEnergyProviders(registry, energyStates, energyCallbacks); err != nil {
			return fmt.Errorf("failed to register energy providers: %w", err)
		}
	}

	// Start the registry
	if err := registry.Start(); err != nil {
		return fmt.Errorf("failed to start computed state registry: %w", err)
	}

	// Start the wake sequence latch monitoring
	if err := latch.Start(); err != nil {
		registry.Stop()
		return fmt.Errorf("failed to start wake sequence latch: %w", err)
	}

	m.logger.Info("Computed state V2 initialized",
		zap.Strings("providers", registry.GetProviderNames()),
		zap.Any("dependency_graph", registry.GetDependencyGraph()))

	return nil
}
