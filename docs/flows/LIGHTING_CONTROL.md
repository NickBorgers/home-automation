# Lighting Control Flow

This document describes the lighting automation flow, which manages whole-home lighting based on day phase, presence, and sleep state.

## Overview

The lighting plugin provides intelligent lighting control by:
1. Calculating scenes based on day phase and context
2. Respecting manual overrides and protection modes
3. Managing transitions for natural light progression
4. Supporting special scenarios (wake-up, away mode)

## Lighting Scene Selection

```mermaid
flowchart TD
    subgraph Triggers["State Change Triggers"]
        dayPhase["dayPhase changed"]
        sunevent["sunevent changed"]
        isAnyoneHome["isAnyoneHome changed"]
        isMasterAsleep["isMasterAsleep changed"]
    end

    subgraph Priority["Priority Checks (in order)"]
        checkWake{"isWakeSequenceActive?"}
        checkHome{"isAnyoneHome?"}
        checkSleep{"isMasterAsleep?"}
        applyScene["Apply scene for dayPhase"]
    end

    subgraph Actions["Lighting Actions"]
        protect["Protect bedroom lights<br/>(wake sequence)"]
        awayMode["Turn off lights<br/>or away lighting"]
        nightScene["Night/sleep scene<br/>(dim master)"]
        normalScene["Apply normal scene"]
    end

    dayPhase --> checkWake
    sunevent --> checkWake
    isAnyoneHome --> checkWake
    isMasterAsleep --> checkWake

    checkWake -->|Yes| protect
    checkWake -->|No| checkHome
    checkHome -->|No| awayMode
    checkHome -->|Yes| checkSleep
    checkSleep -->|Yes| nightScene
    checkSleep -->|No| applyScene
    applyScene --> normalScene

    style protect fill:#f39c12,color:#fff
    style awayMode fill:#95a5a6,color:#fff
    style nightScene fill:#1a1a2e,color:#fff
    style normalScene fill:#3498db,color:#fff
```

## Day Phase to Scene Mapping

Each day phase corresponds to a pre-configured lighting scene:

```mermaid
flowchart TD
    subgraph DayPhase["Day Phase Input"]
        morning["morning"]
        day["day"]
        sunset["sunset"]
        dusk["dusk"]
        winddown["winddown"]
        night["night"]
    end

    subgraph Context["Context Modifiers"]
        sunEvent{"sunevent<br/>recent?"}
        energy{"currentEnergyLevel<br/>is red?"}
    end

    subgraph Scenes["Lighting Scenes"]
        S_morning["Morning Scene<br/>(bright, cool white)"]
        S_day["Day Scene<br/>(natural light)"]
        S_sunset["Sunset Scene<br/>(warm transition)"]
        S_dusk["Dusk Scene<br/>(dim warm)"]
        S_winddown["Winddown Scene<br/>(very dim, warm)"]
        S_night["Night Scene<br/>(minimal/off)"]
    end

    morning --> S_morning
    day --> sunEvent
    sunEvent -->|sunrise| S_morning
    sunEvent -->|other| S_day

    sunset --> S_sunset
    dusk --> S_dusk
    winddown --> S_winddown
    night --> S_night

    style S_morning fill:#ffd93d,color:#000
    style S_day fill:#fff5cc,color:#000
    style S_sunset fill:#ff8c42,color:#fff
    style S_dusk fill:#6c5ce7,color:#fff
    style S_winddown fill:#a855f7,color:#fff
    style S_night fill:#1a1a2e,color:#fff
```

### Scene Details

| Day Phase | Scene | Brightness | Color Temp | Notes |
|-----------|-------|------------|------------|-------|
| morning | Morning | 100% | 4000K (cool) | Energizing wake-up light |
| day | Day | 80-100% | 4500K (natural) | Full daylight simulation |
| sunset | Sunset | 60-80% | 3000K (warm) | Golden hour transition |
| dusk | Dusk | 40-60% | 2700K (warm) | Evening relaxation |
| winddown | Winddown | 20-40% | 2200K (candle) | Pre-sleep dimming |
| night | Night | 1-10% | 2000K (dim red) | Minimal for navigation |

## Light Group Management

Lights are organized into zones with different behaviors:

```mermaid
flowchart TD
    subgraph Zones["Light Zones"]
        living["Living Areas<br/>(living room, kitchen, dining)"]
        master["Primary Suite<br/>(bedroom, bathroom)"]
        guest["Guest Areas<br/>(guest room, office)"]
        exterior["Exterior<br/>(porch, garage)"]
    end

    subgraph Behaviors["Zone Behaviors"]
        livingBehavior["Follow day phase<br/>Always respond to scene changes"]
        masterBehavior["Sleep-aware<br/>Protected during wake sequence"]
        guestBehavior["Independent<br/>Less aggressive automation"]
        exteriorBehavior["Motion/schedule<br/>Security focused"]
    end

    living --> livingBehavior
    master --> masterBehavior
    guest --> guestBehavior
    exterior --> exteriorBehavior

    style masterBehavior fill:#f39c12,color:#fff
    style exteriorBehavior fill:#27ae60,color:#fff
```

## Wake Sequence Protection

During the wake-up sequence, bedroom lights are protected from normal lighting automation:

```mermaid
sequenceDiagram
    participant SH as Sleep Hygiene
    participant LM as Lighting Plugin
    participant HA as Home Assistant

    Note over SH: Eight Sleep alarm triggers

    SH->>SH: Set isWakeSequenceActive = true

    SH->>HA: Turn on bedroom lights<br/>(25-min fade to 100%)

    Note over LM: Day phase changes during fade

    LM->>LM: Check isWakeSequenceActive
    LM-->>LM: Skip bedroom lights<br/>(protected)

    SH->>SH: Set isWakeSequenceActive = false<br/>(when person wakes)

    Note over LM: Bedroom lights now<br/>follow normal automation
```

## Manual Override Detection

The lighting plugin respects manual changes:

```mermaid
flowchart TD
    subgraph Detection["Override Detection"]
        expected["Expected state<br/>(from last command)"]
        actual["Actual state<br/>(from HA)"]
        compare{"States<br/>match?"}
    end

    subgraph Response["Response"]
        normal["Continue<br/>normal automation"]
        override["Mark as<br/>manually overridden"]
        protect["Skip automation<br/>for this light"]
    end

    subgraph Reset["Override Reset"]
        dayPhaseChange["Day phase<br/>changes"]
        clearOverride["Clear override<br/>flag"]
    end

    expected --> compare
    actual --> compare
    compare -->|Yes| normal
    compare -->|No| override
    override --> protect

    dayPhaseChange --> clearOverride
    clearOverride --> normal

    style override fill:#f39c12,color:#fff
    style protect fill:#e74c3c,color:#fff
```

## Edge-Triggered Activation (skip_reactivation_when_on)

Some rooms use presence sensors (e.g. mmWave radar) that briefly drop detection on stationary people, then re-detect. Without protection, every re-detection re-fires the scene and overrides manual brightness adjustments.

The per-room `skip_reactivation_when_on: true` config flag makes activation **edge-triggered**: if the room's last applied action was already `"on"`, a repeat `"on"` evaluation from an occupancy or condition trigger is skipped, preserving any brightness the user dialed in since last activation.

**Exceptions — triggers that always re-fire regardless of this flag:**
- `dayPhase` changes (different target scene)
- `sunevent` changes
- Empty/reset triggers (startup initialization)

Currently used by: Kitchen (Apollo MTR mmWave radar)

## Transition Timing

Light transitions use appropriate timing for natural progression:

| Transition Type | Duration | Use Case |
|-----------------|----------|----------|
| Immediate | 0s | Emergency/security |
| Quick | 2s | Manual toggles |
| Normal | 60s | Phase transitions |
| Slow | 300s (5 min) | Sunset/dusk |
| Wake | 1500s (25 min) | Morning wake-up |

```mermaid
flowchart LR
    subgraph WakeFade["Wake-Up Fade"]
        start["1% brightness<br/>2000K warm"]
        mid["50% brightness<br/>2500K"]
        end_["100% brightness<br/>3000K cool"]
    end

    start -->|12.5 min| mid
    mid -->|12.5 min| end_

    style start fill:#1a1a2e,color:#fff
    style mid fill:#ff8c42,color:#fff
    style end_ fill:#ffd93d,color:#000
```

## Scene Activation Reliability

The lighting plugin protects against two Hue Bridge interaction quirks that
manifest when a person arrives home from an "away" state:

### Per-room debounce of evaluations

Arrival flips several presence variables in a sub-second burst — `isNickHome`,
`isAnyOwnerHome`, `isAnyoneHome`, `didOwnerJustReturnHome`,
`isAnyoneHomeAndAwake`. Each one triggers a room re-evaluation. Without
coalescing, the lighting plugin fires the same `scene.turn_on` several times
back-to-back, which destabilizes Hue scenes with `auto_dynamic: true`.

`evaluateAndActivateRoom` debounces per-room: subsequent calls within
`defaultLightingDebounceDelay` (300ms) reset the timer and accumulate the
trigger names. When the timer fires, a single evaluation runs against the
most recent state, with a `debounced:...` trigger string for log
observability. This mirrors the music plugin's zone-resolution debounce.

### Two-step recall for dynamic scenes

Rooms with `has_dynamics: true` in `hue_config.yaml` (e.g. Primary Suite)
use Hue scenes whose `auto_dynamic` flag is on — the bridge runs an animated
palette autonomously after recall. The bridge can fail to start the palette
on bulbs that were off pre-recall (still mid-transition from the on
command), leaving each bulb reverted to off at staggered times.

`activateScene` works around this by issuing two calls:

1. `scene.turn_on` with `dynamic: false` — static recall; bulbs settle at the
   scene's base colors and brightness.
2. After `twoStepRecallGap` (500ms), `scene.turn_on` with `dynamic: true` —
   palette starts from a stable known state.

The dynamic phase fires asynchronously via a timer callback. If the
room-scoped context is cancelled during the gap (a newer evaluation came
in), the dynamic phase is skipped.

Rooms without `has_dynamics` make a single recall, with no `dynamic` key,
deferring to the scene's own configuration.

The 2026-05-25 production incident — three rapid scene recalls during
arrival, dynamic palette failure, bedroom lights going off, statetracking
incorrectly marking the occupants as asleep — drove both protections.

## Away Mode Lighting

When no one is home, lighting enters away mode:

```mermaid
flowchart TD
    subgraph AwayDetection["Away Detection"]
        noHome["isAnyoneHome = false"]
        checkTime{"Time of day?"}
    end

    subgraph DayBehavior["Daytime"]
        allOff["All lights off<br/>(save energy)"]
    end

    subgraph NightBehavior["Nighttime"]
        randomLights["Random lights on/off<br/>(simulate occupancy)"]
        porchOn["Exterior lights on<br/>(security)"]
    end

    noHome --> checkTime
    checkTime -->|Day| allOff
    checkTime -->|Night| randomLights
    checkTime -->|Night| porchOn

    style allOff fill:#95a5a6,color:#fff
    style randomLights fill:#3498db,color:#fff
    style porchOn fill:#27ae60,color:#fff
```

## Related Documentation

- [DAY_PHASE_MODES.md](./DAY_PHASE_MODES.md) - Day phase calculation and schedule configuration
- [SLEEP_HYGIENE.md](./SLEEP_HYGIENE.md) - Wake-up sequence that protects bedroom lights
- [ENERGY_MANAGEMENT.md](./ENERGY_MANAGEMENT.md) - Energy-aware lighting adjustments
- [PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md) - Plugin architecture
