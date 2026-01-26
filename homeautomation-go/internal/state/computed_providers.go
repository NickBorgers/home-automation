package state

import (
	"go.uber.org/zap"
)

// ComputedStateCallback is called when computed states are updated.
// This allows plugins to track changes for shadow state updates.
type ComputedStateCallback struct {
	// OnDerivedStatesUpdate is called when any derived presence/sleep state updates
	// Parameters: isAnyOwnerHome, isAnyoneHome, isAnyoneAsleep, isEveryoneAsleep
	OnDerivedStatesUpdate func(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep bool)

	// OnAnyoneHomeAndAwakeUpdate is called when isAnyoneHomeAndAwake updates
	OnAnyoneHomeAndAwakeUpdate func(anyoneHomeAndAwake bool)
}

// RegisterPresenceProviders registers all presence-related computed state providers.
// Level 1 computed states:
//   - isAnyOwnerHome = isNickHome OR isCarolineHome
//   - isAnyoneHome = isAnyOwnerHome OR isToriHere
//
// These are the foundational presence states that other computations depend on.
func RegisterPresenceProviders(registry *ComputedStateRegistry, callbacks *ComputedStateCallback) error {
	// isAnyOwnerHome = isNickHome OR isCarolineHome
	if err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyOwnerHome",
		Dependencies: []string{"isNickHome", "isCarolineHome"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			isNickHome, err := ctx.GetBool("isNickHome")
			if err != nil {
				return nil, err
			}
			isCarolineHome, err := ctx.GetBool("isCarolineHome")
			if err != nil {
				return nil, err
			}

			result := isNickHome || isCarolineHome

			ctx.Logger().Debug("Computed isAnyOwnerHome",
				zap.Bool("isNickHome", isNickHome),
				zap.Bool("isCarolineHome", isCarolineHome),
				zap.Bool("result", result))

			return result, nil
		},
		UpdateMode: UpdateOnDependencyChange,
		OnComputed: func(newValue interface{}) {
			if callbacks != nil && callbacks.OnDerivedStatesUpdate != nil {
				// We need to call the callback with all four values
				// but we only have isAnyOwnerHome here. The callback
				// will be invoked by the central callback mechanism.
			}
		},
	}); err != nil {
		return err
	}

	// isAnyoneHome = isAnyOwnerHome OR isToriHere
	if err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHome",
		Dependencies: []string{"isAnyOwnerHome", "isToriHere"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			isAnyOwnerHome, err := ctx.GetBool("isAnyOwnerHome")
			if err != nil {
				return nil, err
			}
			isToriHere, err := ctx.GetBool("isToriHere")
			if err != nil {
				return nil, err
			}

			result := isAnyOwnerHome || isToriHere

			ctx.Logger().Debug("Computed isAnyoneHome",
				zap.Bool("isAnyOwnerHome", isAnyOwnerHome),
				zap.Bool("isToriHere", isToriHere),
				zap.Bool("result", result))

			return result, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	}); err != nil {
		return err
	}

	return nil
}

// RegisterSleepProviders registers all sleep-related computed state providers.
// Level 1 computed states:
//   - isAnyoneAsleep = isMasterAsleep OR isGuestAsleep
//   - isEveryoneAsleep = isMasterAsleep AND isGuestAsleep
//
// Note: isGuestAsleep itself has complex auto-detection logic that is handled
// separately by the statetracking plugin (auto-sleep when door closes, mirroring
// master when no guests).
func RegisterSleepProviders(registry *ComputedStateRegistry, callbacks *ComputedStateCallback) error {
	// isAnyoneAsleep = isMasterAsleep OR isGuestAsleep
	if err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneAsleep",
		Dependencies: []string{"isMasterAsleep", "isGuestAsleep"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			isMasterAsleep, err := ctx.GetBool("isMasterAsleep")
			if err != nil {
				return nil, err
			}
			isGuestAsleep, err := ctx.GetBool("isGuestAsleep")
			if err != nil {
				return nil, err
			}

			result := isMasterAsleep || isGuestAsleep

			ctx.Logger().Debug("Computed isAnyoneAsleep",
				zap.Bool("isMasterAsleep", isMasterAsleep),
				zap.Bool("isGuestAsleep", isGuestAsleep),
				zap.Bool("result", result))

			return result, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	}); err != nil {
		return err
	}

	// isEveryoneAsleep = isMasterAsleep AND isGuestAsleep
	if err := registry.Register(&ComputedStateProvider{
		Name:         "isEveryoneAsleep",
		Dependencies: []string{"isMasterAsleep", "isGuestAsleep"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			isMasterAsleep, err := ctx.GetBool("isMasterAsleep")
			if err != nil {
				return nil, err
			}
			isGuestAsleep, err := ctx.GetBool("isGuestAsleep")
			if err != nil {
				return nil, err
			}

			result := isMasterAsleep && isGuestAsleep

			ctx.Logger().Debug("Computed isEveryoneAsleep",
				zap.Bool("isMasterAsleep", isMasterAsleep),
				zap.Bool("isGuestAsleep", isGuestAsleep),
				zap.Bool("result", result))

			return result, nil
		},
		UpdateMode: UpdateOnDependencyChange,
	}); err != nil {
		return err
	}

	return nil
}

// RegisterAnyoneHomeAndAwakeProvider registers the isAnyoneHomeAndAwake computed state.
// Level 2 computed state:
//   - isAnyoneHomeAndAwake = (isAnyOwnerHome AND NOT isAnyoneAsleep) OR isToriHere OR wakeSequenceLatch
//
// This is a more complex computed state that includes:
//   - Dependency on Level 1 computed states (isAnyOwnerHome, isAnyoneAsleep)
//   - Special wake sequence latch behavior for morning wake-up
//
// The wake sequence latch is handled via a LatchProvider that tracks the latch state.
func RegisterAnyoneHomeAndAwakeProvider(registry *ComputedStateRegistry, latch *WakeSequenceLatch, callbacks *ComputedStateCallback) error {
	if err := registry.Register(&ComputedStateProvider{
		Name:         "isAnyoneHomeAndAwake",
		Dependencies: []string{"isAnyOwnerHome", "isAnyoneAsleep", "isToriHere"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			isAnyOwnerHome, err := ctx.GetBool("isAnyOwnerHome")
			if err != nil {
				return nil, err
			}
			isAnyoneAsleep, err := ctx.GetBool("isAnyoneAsleep")
			if err != nil {
				return nil, err
			}
			isToriHere, err := ctx.GetBool("isToriHere")
			if err != nil {
				return nil, err
			}

			// Get latch state
			latchActive := latch.IsActive()

			result := (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere || latchActive

			ctx.Logger().Debug("Computed isAnyoneHomeAndAwake",
				zap.Bool("isAnyOwnerHome", isAnyOwnerHome),
				zap.Bool("isAnyoneAsleep", isAnyoneAsleep),
				zap.Bool("isToriHere", isToriHere),
				zap.Bool("wakeSequenceLatch", latchActive),
				zap.Bool("result", result))

			return result, nil
		},
		UpdateMode: UpdateOnDependencyChange,
		OnComputed: func(newValue interface{}) {
			if callbacks != nil && callbacks.OnAnyoneHomeAndAwakeUpdate != nil {
				if val, ok := newValue.(bool); ok {
					callbacks.OnAnyoneHomeAndAwakeUpdate(val)
				}
			}
		},
	}); err != nil {
		return err
	}

	return nil
}

// RegisterAllBasicProviders registers all basic computed state providers.
// This is a convenience function that registers presence and sleep providers.
func RegisterAllBasicProviders(registry *ComputedStateRegistry, callbacks *ComputedStateCallback) error {
	if err := RegisterPresenceProviders(registry, callbacks); err != nil {
		return err
	}
	if err := RegisterSleepProviders(registry, callbacks); err != nil {
		return err
	}
	return nil
}
