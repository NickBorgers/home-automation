package state

import (
	"go.uber.org/zap"
)

// EnergyStateConfig represents the configuration for a single energy level.
// This is used to decouple the computed state providers from the energy plugin's config.
type EnergyStateConfig struct {
	ConditionName                       string
	BatteryMinimumPercentage            float64
	EnergyProductionMinimumKW           float64
	RemainingEnergyProductionMinimumKWH float64
}

// EnergyComputedStateCallback is called when energy computed states are updated.
// This allows the energy plugin to track changes for shadow state updates.
//
// Note: batteryEnergyLevel is NOT computed by the registry (it depends on sensor
// data), so there is no callback for it here. The energy plugin handles battery
// level computation directly.
type EnergyComputedStateCallback struct {
	// OnSolarLevelUpdate is called when solarProductionEnergyLevel updates
	OnSolarLevelUpdate func(level string)
	// OnOverallLevelUpdate is called when currentEnergyLevel updates
	OnOverallLevelUpdate func(level string)
}

// Note: batteryEnergyLevel is NOT registered as a computed state provider.
// It depends on raw sensor data (battery percentage) that comes through HA
// subscriptions, not state variables. The energy plugin handles this directly
// by computing and setting batteryEnergyLevel when sensor updates arrive.
// See Phase 4/5 decision discussion in issue #535.

// RegisterSolarEnergyLevelProvider registers the solarProductionEnergyLevel computed state.
// Formula: level = determineSolarLevel(thisHourSolarGeneration, remainingSolarGeneration)
//
// The solar level is determined by checking each energy state's thresholds.
// Both conditions must be met (thisHourKW >= threshold AND remainingKWH >= threshold).
//
// Dependencies:
//   - thisHourSolarGeneration (sensor.energy_next_hour)
//   - remainingSolarGeneration (sensor.energy_production_today_remaining)
//   - These are read via HA subscriptions, stored in state variables
func RegisterSolarEnergyLevelProvider(
	registry *ComputedStateRegistry,
	energyStates []EnergyStateConfig,
	callbacks *EnergyComputedStateCallback,
) error {
	return registry.Register(&ComputedStateProvider{
		Name:         "solarProductionEnergyLevel",
		Dependencies: []string{"thisHourSolarGeneration", "remainingSolarGeneration"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			thisHourKW, err := ctx.GetNumber("thisHourSolarGeneration")
			if err != nil {
				return nil, err
			}

			remainingKWH, err := ctx.GetNumber("remainingSolarGeneration")
			if err != nil {
				return nil, err
			}

			// Default to the first configured level (lowest)
			level := ""
			if len(energyStates) > 0 {
				level = energyStates[0].ConditionName
			}

			// Check each energy state in order
			// Level is the highest where both conditions are met
			for _, state := range energyStates {
				if thisHourKW >= state.EnergyProductionMinimumKW &&
					remainingKWH >= state.RemainingEnergyProductionMinimumKWH {
					level = state.ConditionName
				}
			}

			ctx.Logger().Debug("Computed solarProductionEnergyLevel",
				zap.Float64("thisHourKW", thisHourKW),
				zap.Float64("remainingKWH", remainingKWH),
				zap.String("result", level))

			return level, nil
		},
		UpdateMode: UpdateOnDependencyChange,
		OnComputed: func(newValue interface{}) {
			if callbacks != nil && callbacks.OnSolarLevelUpdate != nil {
				if level, ok := newValue.(string); ok {
					callbacks.OnSolarLevelUpdate(level)
				}
			}
		},
	})
}

// RegisterCurrentEnergyLevelProvider registers the currentEnergyLevel computed state.
// Formula: level = combineEnergyLevels(batteryEnergyLevel, solarProductionEnergyLevel)
//
// The combination logic:
//   - Solar can only boost, never drag down the battery level
//   - The overall level is at least the battery level
//   - If solar is higher, it can boost by at most 1 level
//
// Dependencies:
//   - batteryEnergyLevel
//   - solarProductionEnergyLevel
func RegisterCurrentEnergyLevelProvider(
	registry *ComputedStateRegistry,
	energyStates []EnergyStateConfig,
	callbacks *EnergyComputedStateCallback,
) error {
	// Extract ordered list of level names
	var levelNames []string
	for _, state := range energyStates {
		levelNames = append(levelNames, state.ConditionName)
	}

	return registry.Register(&ComputedStateProvider{
		Name:         "currentEnergyLevel",
		Dependencies: []string{"batteryEnergyLevel", "solarProductionEnergyLevel"},
		ComputeFunc: func(ctx *ComputeContext) (interface{}, error) {
			batteryLevel, err := ctx.GetString("batteryEnergyLevel")
			if err != nil {
				return nil, err
			}

			solarLevel, err := ctx.GetString("solarProductionEnergyLevel")
			if err != nil {
				return nil, err
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

			// Handle invalid levels — return the lowest configured level as a safe default
			if batteryIndex == -1 || solarIndex == -1 {
				ctx.Logger().Warn("Invalid battery or solar level",
					zap.String("batteryLevel", batteryLevel),
					zap.String("solarLevel", solarLevel))
				if len(levelNames) > 0 {
					return levelNames[0], nil
				}
				return "", nil
			}

			// Solar can only boost, never drag down the battery level.
			// The overall level is:
			// - At least the battery level (solar never penalizes)
			// - At most battery + 1 (solar can boost by one level if it's higher)
			outputIndex := batteryIndex
			if solarIndex > batteryIndex {
				// Solar is higher, boost by at most 1
				outputIndex = batteryIndex + 1
				if solarIndex < outputIndex {
					outputIndex = solarIndex
				}
			}

			// Clamp to valid range
			if outputIndex >= len(levelNames) {
				outputIndex = len(levelNames) - 1
			}

			result := levelNames[outputIndex]

			ctx.Logger().Debug("Computed currentEnergyLevel",
				zap.String("batteryLevel", batteryLevel),
				zap.Int("batteryIndex", batteryIndex),
				zap.String("solarLevel", solarLevel),
				zap.Int("solarIndex", solarIndex),
				zap.Int("outputIndex", outputIndex),
				zap.String("result", result))

			return result, nil
		},
		UpdateMode: UpdateOnDependencyChange,
		OnComputed: func(newValue interface{}) {
			if callbacks != nil && callbacks.OnOverallLevelUpdate != nil {
				if level, ok := newValue.(string); ok {
					callbacks.OnOverallLevelUpdate(level)
				}
			}
		},
	})
}

// RegisterEnergyProviders registers all energy-related computed state providers.
// This is a convenience function that registers solar and overall energy level providers.
//
// Note: batteryEnergyLevel is NOT registered as a computed state provider because
// it depends on sensor readings that come through HA subscriptions. The energy
// plugin handles this directly.
func RegisterEnergyProviders(
	registry *ComputedStateRegistry,
	energyStates []EnergyStateConfig,
	callbacks *EnergyComputedStateCallback,
) error {
	if err := RegisterSolarEnergyLevelProvider(registry, energyStates, callbacks); err != nil {
		return err
	}
	if err := RegisterCurrentEnergyLevelProvider(registry, energyStates, callbacks); err != nil {
		return err
	}
	return nil
}
