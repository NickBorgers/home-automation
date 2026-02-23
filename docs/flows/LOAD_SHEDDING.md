# Load Shedding Flow

This document describes the load shedding automation flow, which manages HVAC thermostat control and EV charger based on energy availability.

## Overview

The load shedding plugin controls thermostats and EV charger based on energy levels to:
1. Restrict HVAC and disable EV charger when battery is low (red/black)
2. Return to normal schedules and re-enable EV charger when energy is available (green/white)
3. **Thermal battery**: Pre-condition the house by shifting HVAC setpoints when energy is abundant (white)
4. Maintain hysteresis to prevent rapid toggling (yellow)

## Energy Level Response

```mermaid
flowchart TD
    subgraph Input["Energy Level Input"]
        currentLevel["currentEnergyLevel changed"]
    end

    subgraph Decision["Load Shedding Decision"]
        checkLevel{"Energy level?"}
        enableLS["Enable load shedding"]
        disableLS["Disable load shedding"]
        maintain["Maintain current state<br/>(hysteresis)"]
    end

    currentLevel --> checkLevel
    checkLevel -->|red, black| enableLS
    checkLevel -->|green| disableLS
    checkLevel -->|white| thermalBattery["Disable load shedding<br/>+ Activate thermal battery"]
    checkLevel -->|yellow| maintain

    style enableLS fill:#e74c3c,color:#fff
    style disableLS fill:#27ae60,color:#fff
    style thermalBattery fill:#3498db,color:#fff
    style maintain fill:#f39c12,color:#fff
```

## Thermostat Control

### Enable Load Shedding (Low Energy)

```mermaid
flowchart TD
    subgraph Check["Pre-Action Checks"]
        alreadyOn{"Load shedding<br/>already enabled?"}
        checkHold{"Thermostat holds<br/>already on?"}
        rateLimit{"Rate limit<br/>(< 1 hour)?"}
    end

    subgraph Skip["Skip Actions"]
        skipOn["Skip<br/>(already enabled)"]
        skipHold["Skip<br/>(holds already on)"]
        skipRate["Skip<br/>(rate limited)"]
    end

    subgraph Actions["Enable Actions"]
        turnOnHold["Turn on thermostat hold<br/>(both zones)"]
        setTemp["Set temperature range<br/>65°F - 80°F"]
        turnOffEV["Turn off EV charger"]
    end

    alreadyOn -->|Yes| skipOn
    alreadyOn -->|No| checkHold
    checkHold -->|Yes| skipHold
    checkHold -->|No| rateLimit
    rateLimit -->|Yes| skipRate
    rateLimit -->|No| turnOnHold
    turnOnHold --> setTemp
    setTemp --> turnOffEV

    style skipOn fill:#95a5a6,color:#fff
    style skipHold fill:#95a5a6,color:#fff
    style skipRate fill:#f39c12,color:#fff
    style setTemp fill:#e74c3c,color:#fff
    style turnOffEV fill:#e74c3c,color:#fff
```

### Disable Load Shedding (Energy Restored)

```mermaid
flowchart TD
    subgraph Check["Pre-Action Checks"]
        alreadyOff{"Load shedding<br/>already disabled?"}
        checkHold{"Thermostat holds<br/>already off?"}
        rateLimit{"Rate limit<br/>(< 1 hour)?"}
    end

    subgraph Skip["Skip Actions"]
        skipOff["Skip<br/>(already disabled)"]
        skipHold["Skip<br/>(holds already off)"]
        skipRate["Skip<br/>(rate limited)"]
    end

    subgraph Actions["Disable Actions"]
        turnOffHold["Turn off thermostat hold<br/>(both zones)"]
        turnOnEV["Turn on EV charger"]
    end

    alreadyOff -->|Yes| skipOff
    alreadyOff -->|No| checkHold
    checkHold -->|Yes| skipHold
    checkHold -->|No| rateLimit
    rateLimit -->|Yes| skipRate
    rateLimit -->|No| turnOffHold
    turnOffHold --> turnOnEV

    style skipOff fill:#95a5a6,color:#fff
    style skipHold fill:#95a5a6,color:#fff
    style skipRate fill:#f39c12,color:#fff
    style turnOffHold fill:#27ae60,color:#fff
    style turnOnEV fill:#27ae60,color:#fff
```

## Thermal Battery (Energy Level White)

When energy is abundant (white level), the plugin pre-conditions the house by shifting HVAC setpoints, storing thermal energy in the building mass. This reduces HVAC demand later if energy levels drop.

### Activation Guards

```mermaid
flowchart TD
    whiteLevel["Energy level = white"]
    checkLS{"Load shedding<br/>active?"}
    checkHome{"Anyone<br/>home?"}
    checkSleep{"Everyone<br/>asleep?"}
    checkHVAC{"Thermostats<br/>on?"}
    activate["Activate thermal battery<br/>Shift setpoints ±3°F"]
    skip["Skip activation"]

    whiteLevel --> checkLS
    checkLS -->|Yes| skip
    checkLS -->|No| checkHome
    checkHome -->|No| skip
    checkHome -->|Yes| checkSleep
    checkSleep -->|Yes| skip
    checkSleep -->|No| checkHVAC
    checkHVAC -->|Off| skip
    checkHVAC -->|On| activate

    style activate fill:#3498db,color:#fff
    style skip fill:#95a5a6,color:#fff
```

### Setpoint Shifting

| HVAC Mode | Offset Direction | Effect |
|-----------|-----------------|--------|
| `cool` | Setpoint **down** 3°F | Pre-cool the house |
| `heat` | Setpoint **up** 3°F | Pre-heat the house |
| `heat_cool`/`auto` | Low **up** 3°F, High **down** 3°F | Shift both bounds inward |

Original setpoints are saved and restored on deactivation.

### Deactivation Triggers

Thermal battery deactivates (reverting to original setpoints) when:
- Energy level drops below white (green, yellow, red, or black)
- Nobody is home (`isAnyoneHome` → false)
- Everyone falls asleep (`isEveryoneAsleep` → true)

## Yellow State Hysteresis

The yellow (moderate) energy state acts as a buffer to prevent rapid toggling:

```mermaid
sequenceDiagram
    participant Battery as Battery Level
    participant EM as Energy Manager
    participant LS as Load Shedding
    participant HVAC as Thermostats

    Note over Battery: Battery drops to 25%

    Battery->>EM: Battery = 25% (red)
    EM->>LS: currentEnergyLevel = red
    LS->>HVAC: Enable hold mode<br/>Set 65-80°F range

    Note over Battery: Battery recovers to 35%

    Battery->>EM: Battery = 35% (yellow)
    EM->>LS: currentEnergyLevel = yellow
    LS->>LS: Maintain current state<br/>(still restricted)

    Note over Battery: Battery recovers to 65%

    Battery->>EM: Battery = 65% (green)
    EM->>LS: currentEnergyLevel = green
    LS->>HVAC: Disable hold mode<br/>(resume schedule)
```

Without hysteresis, the system would rapidly toggle at threshold boundaries (e.g., 29%↔31% repeatedly enabling/disabling).

## Controlled Entities

| Entity | Type | Description |
|--------|------|-------------|
| `switch.most_of_house_thermostat_hold` | Thermostat | Main zone hold mode |
| `switch.primary_suite_thermostat_hold` | Thermostat | Primary suite hold mode |
| `climate.most_of_house_thermostat` | Thermostat | Main zone climate control |
| `climate.primary_suite_thermostat` | Thermostat | Primary suite climate control |
| `switch.leaf_charger` | EV Charger | Nissan Leaf charger plug |

## Temperature Range

| Mode | Low | High | Description |
|------|-----|------|-------------|
| Normal | (schedule) | (schedule) | Follow programmed schedule |
| Load Shedding | 65°F | 80°F | Wider range = less HVAC runtime |

The 65-80°F range allows:
- Winter: Let home cool to 65°F before heating
- Summer: Let home warm to 80°F before cooling

## Rate Limiting

A 1-hour minimum interval between actions prevents:
- Rapid toggling from energy level fluctuations
- Excessive wear on HVAC equipment
- User frustration from constant changes

```mermaid
flowchart LR
    subgraph RateCheck["Rate Limit Check"]
        lastAction["Last action time"]
        now["Current time"]
        compare{"now - lastAction<br/>< 1 hour?"}
    end

    subgraph Result["Result"]
        blocked["Action blocked"]
        allowed["Action allowed"]
    end

    lastAction --> compare
    now --> compare
    compare -->|Yes| blocked
    compare -->|No| allowed

    style blocked fill:#e74c3c,color:#fff
    style allowed fill:#27ae60,color:#fff
```

## State Variables

### Inputs

| Variable | Type | Source | Description |
|----------|------|--------|-------------|
| `currentEnergyLevel` | string | Energy Plugin | Overall energy availability |
| `isAnyoneHome` | bool | State Tracking Plugin | Whether anyone is home (thermal battery guard) |
| `isEveryoneAsleep` | bool | State Tracking Plugin | Whether everyone is asleep (thermal battery guard) |

### Internal State

| Variable | Type | Description |
|----------|------|-------------|
| `loadSheddingOn` | bool | Current load shedding state |
| `lastAction` | time | Timestamp of last action (rate limiting) |
| `thermalBatteryActive` | bool | Whether thermal battery is currently active |
| `savedSetpoints` | map | Original thermostat setpoints saved for restoration |

## Related Documentation

- [ENERGY_MANAGEMENT.md](./ENERGY_MANAGEMENT.md) - Energy level calculation
- [DAY_PHASE_MODES.md](./DAY_PHASE_MODES.md) - Schedule configuration
- [PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md) - Plugin architecture
