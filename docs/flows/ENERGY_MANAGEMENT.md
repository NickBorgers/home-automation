# Energy Management Flow

This document describes the energy management automation flow, which monitors battery and solar levels to optimize home energy usage and provide visual indicators.

## Overview

The energy plugin manages home energy by:
1. Monitoring battery percentage and solar production
2. Calculating overall energy availability levels
3. Displaying energy state on indicator lights throughout the home
4. Triggering load shedding when battery is low

## Energy Level Calculation

The system combines battery level and solar production to determine overall energy availability:

```mermaid
flowchart TD
    subgraph Inputs["Sensor Inputs"]
        battery["Battery %<br/>(SPAN Panel)"]
        thisHourSolar["This Hour Solar<br/>(kW)"]
        remainingSolar["Remaining Solar<br/>(kWh today)"]
    end

    subgraph Intermediate["Intermediate Levels"]
        batteryLevel["Battery Energy Level"]
        solarLevel["Solar Production Level"]
    end

    subgraph Output["Overall Energy Level"]
        overallLevel["currentEnergyLevel"]
    end

    battery --> batteryLevel
    thisHourSolar --> solarLevel
    remainingSolar --> solarLevel

    batteryLevel --> overallLevel
    solarLevel --> overallLevel

    style overallLevel fill:#27ae60,color:#fff
```

### Energy State Hierarchy

Energy states are ordered from lowest to highest availability:

| State | Color | Battery Min | Solar Min | Meaning |
|-------|-------|-------------|-----------|---------|
| `red` | Red | 0% | 0 kW | Critical - load shed (HVAC band + non-HVAC off) |
| `yellow` | Yellow | 15% | 0.1 kW | Hysteresis band covering normal arbitrage operation |
| `green` | Green | 60% | 1.0 kW / 5 kWh remaining | Good - energy available |
| `white` | White | 80% | 4.0 kW / 20 kWh remaining | Abundant solar / stored energy |

### Overall Level Algorithm

```mermaid
flowchart TD
    subgraph Inputs["Inputs"]
        battLevel["Get batteryEnergyLevel"]
        solarLevel["Get solarProductionLevel"]
    end

    subgraph Calculate["Level Calculation"]
        compare["Compare battery and solar levels"]
        weaker["Find weaker input"]
        stronger["Find stronger input"]
        cap["Cap output at<br/>weaker + 1 level"]
    end

    subgraph Result["Final Level"]
        calculated["Final output level"]
    end

    battLevel --> compare
    solarLevel --> compare
    compare --> weaker
    compare --> stronger
    weaker --> cap
    stronger --> cap
    cap --> calculated

    style calculated fill:#27ae60,color:#fff
```

The overall level follows the stronger of battery and solar, but is capped at one level above the weaker input. This prevents a high battery from showing `white` when solar production is weak or absent, while still allowing strong solar to boost a lower battery level.

- Output starts as the stronger of battery and solar
- Maximum output is the weaker input + 1 level

Examples:
- Battery: white (80%+), Solar: yellow (weak production) → Result: green (not white)
- Battery: green (60%), Solar: red (0 kW) → Result: yellow (capped by weak solar)
- Battery: yellow (15-59%), Solar: green (1+ kW, 5+ kWh) → Result: green (boosted by 1)
- Battery: red (<15%), Solar: green (1+ kW, 5+ kWh) → Result: yellow (boosted by 1, not green)

## Free Energy Detection

Scheduled free metered grid energy is no longer used. The `white` energy level now comes from the configured battery and solar thresholds, so abundant solar production can still enable unrestricted-energy behavior.

## Indicator Light Updates

Energy state is displayed on RGB indicator lights (Apollo MTR-2 sensors):

```mermaid
flowchart TD
    subgraph Discovery["Light Discovery"]
        findLights["Find lights matching<br/>'Radar' pattern"]
        findLux["Find associated<br/>lux sensors"]
    end

    subgraph Update["Light Update"]
        getLevel["Get currentEnergyLevel"]
        getColor["Get RGB color for level"]
        adaptive{"Adaptive<br/>brightness?"}
        staticBright["Use static brightness"]
        adaptiveBright["Calculate per-device<br/>brightness from lux"]
    end

    subgraph Apply["Apply Settings"]
        callHA["Call light.turn_on<br/>with RGB + brightness"]
    end

    findLights --> findLux
    findLux --> getLevel
    getLevel --> getColor
    getColor --> adaptive
    adaptive -->|No| staticBright
    adaptive -->|Yes| adaptiveBright
    staticBright --> callHA
    adaptiveBright --> callHA

    style callHA fill:#3498db,color:#fff
```

### Adaptive Brightness

When enabled, indicator lights adjust brightness based on ambient light:

```mermaid
flowchart LR
    subgraph Calibration["Baseline Calibration"]
        dim["Dim LED to 5%"]
        wait["Wait 65 seconds"]
        read["Read lux sensor<br/>(true ambient)"]
        restore["Restore LED"]
    end

    subgraph Brightness["Brightness Calculation"]
        baseline["Use baseline lux<br/>(not current)"]
        curve["Apply brightness curve"]
        hysteresis["Apply hysteresis<br/>(prevent oscillation)"]
    end

    dim --> wait
    wait --> read
    read --> restore
    read --> baseline
    baseline --> curve
    curve --> hysteresis

    style baseline fill:#f39c12,color:#fff
```

**Why calibration?** The lux sensor is on the same device as the LED, so the LED's light "contaminates" the reading. By periodically dimming the LED, we get a true ambient reading.

**Brightness Curve:**

| Ambient Lux | Brightness |
|-------------|------------|
| < 10 (very dark) | 20% |
| 10-100 (dim) | 40% |
| > 100 (bright) | 50% (capped) |

## Load Shedding Integration

When energy is low, the load shedding plugin restricts HVAC:

```mermaid
sequenceDiagram
    participant Battery as Battery Sensor
    participant EM as Energy Manager
    participant SM as State Manager
    participant LS as Load Shedding

    Battery->>EM: Battery at 15%
    EM->>EM: Calculate batteryEnergyLevel = red
    EM->>SM: Set batteryEnergyLevel = red
    EM->>EM: Calculate overall level = red
    EM->>SM: Set currentEnergyLevel = red

    SM->>LS: currentEnergyLevel changed to red
    LS->>LS: Enable thermostat hold mode
    LS->>LS: Set wider temp range (65-80°F)

    Note over LS: HVAC restricted until<br/>energy level improves
```

When energy is abundant (white level), the load shedding plugin activates **thermal battery** mode, gradually shifting HVAC setpoints ±2°F (in 1°F steps) to pre-condition the house and store thermal energy in the building mass. The gradual stepping avoids triggering auxiliary heat strips.

See [LOAD_SHEDDING.md](./LOAD_SHEDDING.md) for thermostat control and thermal battery details.

## State Variables

### Inputs (from Home Assistant)

| Variable | Source Entity | Description |
|----------|---------------|-------------|
| Battery % | `sensor.span_panel_span_storage_battery_percentage_2` | SPAN panel battery level |
| This Hour Solar | `sensor.energy_next_hour` | Current solar production (kW) |
| Remaining Solar | `sensor.energy_production_today_remaining` | Solar remaining today (kWh) |

### Outputs (to State Manager)

| Variable | Type | Description |
|----------|------|-------------|
| `batteryEnergyLevel` | string | Battery-based level (red/yellow/green/white) |
| `solarProductionEnergyLevel` | string | Solar-based level |
| `isFreeEnergyAvailable` | bool | Legacy metered-grid flag, retired (always false) |
| `currentEnergyLevel` | string | Overall combined level |
| `thisHourSolarGeneration` | number | Current solar kW |
| `remainingSolarGeneration` | number | Remaining solar kWh |

## Related Documentation

- [LOAD_SHEDDING.md](./LOAD_SHEDDING.md) - Thermostat control based on energy level
- [DAY_PHASE_MODES.md](./DAY_PHASE_MODES.md) - Schedule configuration
- [PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md) - Plugin architecture
