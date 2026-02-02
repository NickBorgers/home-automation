# Day Phase, Music Mode, and Lighting Scene Relationships

This document describes how the `dayPhase` state variable controls music playback and lighting scenes throughout the home automation system.

## Overview

The system uses a cascading relationship:

1. **Sun Events** (astronomical) - Calculated from solar position
2. **Day Phases** (schedule-adjusted) - Sun events modified by user schedule
3. **Music Modes** - Mapped from day phases with context
4. **Lighting Scenes** - Directly use day phase names

```mermaid
flowchart TB
    subgraph Astronomical["Astronomical Events (suncalc)"]
        dawn["dawn"]
        goldenHourEnd["goldenHourEnd"]
        goldenHour["goldenHour"]
        dusk["dusk"]
        night["night (astronomical)"]
    end

    subgraph SunEvents["Sun Events (simplified)"]
        SE_night["night"]
        SE_morning["morning"]
        SE_day["day"]
        SE_sunset["sunset"]
        SE_dusk["dusk"]
    end

    subgraph DayPhases["Day Phases (schedule-adjusted)"]
        DP_night["night"]
        DP_morning["morning"]
        DP_day["day"]
        DP_sunset["sunset"]
        DP_dusk["dusk"]
        DP_winddown["winddown"]
    end

    dawn --> SE_morning
    noon["noon (12:00)"] --> SE_day
    goldenHour --> SE_sunset
    dusk --> SE_dusk
    night --> SE_night

    SE_night --> DP_night
    SE_morning --> DP_morning
    SE_day --> DP_day
    SE_sunset --> DP_sunset
    SE_dusk --> DP_dusk
    SE_night --> DP_winddown

    style Astronomical fill:#1a1a2e
    style SunEvents fill:#16213e
    style DayPhases fill:#0f3460
```

## Day Phase Cycle

The daily cycle of phases based on astronomical events and schedule configuration:

```mermaid
flowchart LR
    night["night<br/>(00:00-dawn)"]
    morning["morning<br/>(dawn-noon)"]
    day["day<br/>(noon-goldenHour)"]
    sunset["sunset<br/>(goldenHour-schedule.dusk)"]
    dusk["dusk<br/>(schedule.dusk-schedule.winddown)"]
    winddown["winddown<br/>(schedule.winddown-schedule.night)"]

    night --> morning
    morning --> day
    day --> sunset
    sunset --> dusk
    dusk --> winddown
    winddown --> night

    style night fill:#1a1a2e,color:#fff
    style morning fill:#ff6b6b,color:#fff
    style day fill:#ffd93d,color:#000
    style sunset fill:#ff8c42,color:#fff
    style dusk fill:#6c5ce7,color:#fff
    style winddown fill:#2d3436,color:#fff
```

## Day Phase to Music Mode Mapping

The music plugin maps day phases to music modes with additional context awareness:

```mermaid
flowchart TD
    subgraph DayPhase["Day Phase Input"]
        DP_morning["morning"]
        DP_day["day"]
        DP_sunset["sunset"]
        DP_dusk["dusk"]
        DP_winddown["winddown"]
        DP_night["night"]
    end

    subgraph Context["Context Checks"]
        wakeup{"Wake-up<br/>event?"}
        sunday{"Sunday?"}
        sleeping{"Sleep music<br/>already playing?"}
    end

    subgraph MusicMode["Music Mode Output"]
        MM_morning["morning<br/>(energetic instrumental)"]
        MM_day["day<br/>(upbeat/chill)"]
        MM_evening["evening<br/>(jazz/classical)"]
        MM_winddown["winddown<br/>(ambient/relaxing)"]
        MM_sleep["sleep<br/>(rain sounds)"]
    end

    DP_morning --> wakeup
    wakeup -->|Yes| sunday
    wakeup -->|No| MM_day
    sunday -->|Yes| MM_day
    sunday -->|No| MM_morning

    DP_day --> MM_day

    DP_sunset --> MM_evening
    DP_dusk --> MM_evening

    DP_winddown --> sleeping
    DP_night --> sleeping
    sleeping -->|Yes| MM_sleep
    sleeping -->|No| MM_winddown

    style MM_morning fill:#ff6b6b,color:#fff
    style MM_day fill:#ffd93d,color:#000
    style MM_evening fill:#ff8c42,color:#fff
    style MM_winddown fill:#6c5ce7,color:#fff
    style MM_sleep fill:#1a1a2e,color:#fff
```

### Music Mode Details

| Music Mode | Day Phases | Playlist Style | Notes |
|------------|------------|----------------|-------|
| `morning` | morning (wake-up only) | Instrumental melodic house/techno, epic classical | Only plays on wake-up events; skipped on Sundays |
| `day` | morning (no wake-up), day | Kygo, chill tracks, soft house, country | Default daytime music |
| `evening` | sunset, dusk | Instrumental study, jazz, classical, chilled jazz | Calmer evening music |
| `winddown` | winddown, night | Floating through space, ambient relaxation, sleep prep | Pre-sleep relaxation |
| `sleep` | (manual trigger) | Rain sounds | Triggered by sleep state, not day phase |
| `wakeup` | (alarm trigger) | Ambient relaxation | Gentle wake-up music |

## Day Phase to Lighting Scene Mapping

The lighting plugin uses day phases directly as Hue scene names:

```mermaid
flowchart LR
    subgraph DayPhase["Day Phase"]
        DP["dayPhase<br/>state variable"]
    end

    subgraph SceneConstruction["Scene Name Construction"]
        construct["scene.{room}_{dayPhase}"]
    end

    subgraph Examples["Example Scenes"]
        ex1["scene.living_room_morning"]
        ex2["scene.living_room_day"]
        ex3["scene.living_room_sunset"]
        ex4["scene.living_room_dusk"]
        ex5["scene.living_room_winddown"]
        ex6["scene.living_room_night"]
    end

    DP --> construct
    construct --> ex1
    construct --> ex2
    construct --> ex3
    construct --> ex4
    construct --> ex5
    construct --> ex6

    style construct fill:#e17055,color:#fff
```

### Lighting Scene Characteristics

| Day Phase | Typical Scene Characteristics |
|-----------|------------------------------|
| `morning` | Bright, cool white, energizing |
| `day` | Full brightness, natural daylight |
| `sunset` | Warm colors, dimming begins |
| `dusk` | Warmer, lower brightness |
| `winddown` | Very warm, low brightness |
| `night` | Minimal light, very warm tones |

## Complete System Flow

```mermaid
flowchart TB
    subgraph Inputs["External Inputs"]
        sun["Solar Position<br/>(suncalc library)"]
        schedule["Schedule Config<br/>(schedule_config.yaml)"]
        presence["Presence State<br/>(isAnyoneHome, etc.)"]
        sleep["Sleep State<br/>(isMasterAsleep, etc.)"]
    end

    subgraph DayPhasePlugin["Day Phase Plugin"]
        calc["Calculator"]
        merge["Merge sun events<br/>with schedule"]
        dayPhase["dayPhase<br/>state variable"]
    end

    subgraph MusicPlugin["Music Plugin"]
        musicLogic["determineMusicModeFromDayPhase()"]
        musicMode["musicPlaybackType<br/>state variable"]
        speakers["Sonos Speakers"]
    end

    subgraph LightingPlugin["Lighting Plugin"]
        lightLogic["evaluateAndActivateRoom()"]
        scenes["Hue Scenes"]
        lights["Philips Hue Lights"]
    end

    sun --> calc
    schedule --> merge
    calc --> merge
    merge --> dayPhase

    dayPhase --> musicLogic
    presence --> musicLogic
    sleep --> musicLogic
    musicLogic --> musicMode
    musicMode --> speakers

    dayPhase --> lightLogic
    presence --> lightLogic
    sleep --> lightLogic
    lightLogic --> scenes
    scenes --> lights

    style dayPhase fill:#e74c3c,color:#fff
    style musicMode fill:#3498db,color:#fff
    style scenes fill:#f39c12,color:#fff
```

## Schedule Configuration

The schedule config (`configs/schedule_config.yaml`) defines when phase transitions occur:

| Field | Purpose | Weekday Default | Weekend Default |
|-------|---------|-----------------|-----------------|
| `dusk` | Override sunset→dusk transition | 19:30 | 19:30 |
| `winddown` | Override dusk→winddown transition | 22:15 | 22:00 |
| `night` | Force night phase | 23:00 | 23:59 |
| `stop_screens` | Reminder to stop screen time | 22:30 | 22:30 |
| `go_to_bed` | Start sleep music (rain sounds) | 23:30 | 23:59 |
| `begin_backup_wake` | **BACKUP** start wake sequence (if Eight Sleep unavailable) | 08:50 | 09:50 |
| `backup_wake_time` | **BACKUP** target wake time (if Eight Sleep unavailable) | 09:15 | 10:15 |

**Phase Transition Logic:**
- Before `dusk` time: Stay at sunset phase (even if astronomical dusk/night has occurred)
- After `dusk`, before `winddown`: Stay at dusk phase (even if astronomical night has occurred)
- After `winddown`, before `night`: Transition to winddown phase
- After `night`: Force night phase

> **Wake-up behavior**: The primary wake trigger is the Eight Sleep Pod alarm. The `begin_backup_wake` and `backup_wake_time` values are **fallback only** and activate only when the Eight Sleep integration is unavailable (sensor shows `"unavailable"`). This ensures reliable wake-up even during Internet outages.

## Special Music Modes

These modes are not tied to day phases:

```mermaid
flowchart LR
    subgraph Triggers["Special Triggers"]
        alarm["Alarm fires"]
        nightPhase["dayPhase=night +<br/>isMasterAsleep=false"]
        sleepState["isMasterAsleep=true +<br/>isWakeSequenceActive=false"]
        manual["Manual selection"]
    end

    subgraph SpecialModes["Special Music Modes"]
        wakeup["wakeup"]
        sleepPrep["sleep-prep<br/>(whole-house rain)"]
        sleep["sleep<br/>(bedroom-only rain)"]
        sex["sex"]
    end

    alarm --> wakeup
    nightPhase --> sleepPrep
    sleepState --> sleep
    manual --> sex

    style wakeup fill:#ff6b6b,color:#fff
    style sleepPrep fill:#2d2d44,color:#fff
    style sleep fill:#1a1a2e,color:#fff
    style sex fill:#e84393,color:#fff
```

> **Note:** The sleep music system has two stages:
> - **sleep-prep**: Whole-house rain sounds during night phase, before master goes to bed (helps direct people to bed)
> - **sleep**: Bedroom-only rain sounds after master is asleep, but NOT during wake sequence (sleephygiene manages the bedroom fade-out during wake)

## Wake Sequence Multi-Zone Music

When the wake sequence activates (`isWakeSequenceActive=true`) during morning dayPhase, the sleep zone **stops** (because it requires `isWakeSequenceActive=false`). The sleephygiene plugin manages the bedroom's gentle fade-out separately, while morning music plays in the rest of the house:

```mermaid
flowchart TB
    subgraph Initial["Night State"]
        sleepBed["Bedroom: Sleep music<br/>(isMasterAsleep=true)"]
    end

    subgraph WakeSequence["Wake Sequence Active"]
        fadeBed["Bedroom: Fade-out<br/>(managed by sleephygiene)"]
        morningRest["Kitchen, Office: Morning music<br/>(priority 50)"]
    end

    subgraph FullyAwake["Person Wakes Up"]
        morningAll["Morning music everywhere<br/>(isAnyoneAsleep=false)"]
    end

    Initial -->|"isWakeSequenceActive=true<br/>dayPhase=morning"| WakeSequence
    WakeSequence -->|"isMasterAsleep=false<br/>isAnyoneAsleep=false"| FullyAwake

    style sleepBed fill:#1a1a2e,color:#fff
    style fadeBed fill:#4a4a6a,color:#fff
    style morningRest fill:#ff6b6b,color:#fff
    style morningAll fill:#ff6b6b,color:#fff
```

### Zone Priority and Speaker Assignment

| Zone | Priority | Trigger Conditions | Speakers |
|------|----------|-------------------|----------|
| `sleep-prep` | 90 | `dayPhase=night` + `isAnyoneHome=true` + `isMasterAsleep=false` | Bedroom, Kitchen, Office (whole house) |
| `sleep` | 100 | `isMasterAsleep=true` + `isAnyoneHome=true` + `isWakeSequenceActive=false` | Bedroom, Kitchen |
| `morning` | 50 | `isWakeSequenceActive=true` + `dayPhase=morning` + `isAnyoneHome=true` **OR** `dayPhase=morning` + `isAnyoneAsleep=false` + `isAnyoneHome=true` | Kitchen, Office (Bedroom excluded via `exclude_if: isMasterAsleep=true`) |

### Key Behaviors

1. **Sleep zone stops during wake**: The sleep zone requires `isWakeSequenceActive=false`, so it deactivates when the wake sequence starts. This prevents sleep music from restarting during wake.

2. **Bedroom fade-out via sleephygiene**: During wake sequence, the sleephygiene plugin manages the bedroom's gentle audio fade-out (5-minute fade), independent of the music zone system.

3. **OR logic in morning triggers**: The morning zone uses `trigger_groups` with OR logic:
   - Group 1: Normal morning conditions (`isAnyoneAsleep=false`)
   - Group 2: Wake sequence active (`isWakeSequenceActive=true`)

4. **Seamless join**: When `isMasterAsleep` becomes false, the Bedroom speaker joins the existing morning zone rather than starting new playback.

### Timeline Example

```
Time    Event                           Bedroom              Kitchen/Office
────    ─────                           ───────              ──────────────
T0      Night, isMasterAsleep=true      Sleep music          Sleep music
        isWakeSequenceActive=false      (sleep zone)         (sleep zone)

T1      Alarm triggers                  Fade-out             Morning music
        isWakeSequenceActive=true       (sleephygiene)       (morning zone)
        dayPhase=morning                sleep zone STOPS

T2      Person wakes up                 Morning music        Morning music
        isMasterAsleep=false            (joins morning)      (continues)
        isAnyoneAsleep=false
```

## Related Documentation

### Detailed Flow Documentation

- [MUSIC_PLAYBACK.md](./MUSIC_PLAYBACK.md) - Music playback orchestration, speaker grouping, volume fading
- [LIGHTING_CONTROL.md](./LIGHTING_CONTROL.md) - Scene management, manual overrides, wake protection
- [SLEEP_HYGIENE.md](./SLEEP_HYGIENE.md) - Wake sequence, Eight Sleep integration, bedtime triggers

### Reference

- [PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md) - Plugin architecture details
- [SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Shadow state pattern for debugging
- [migration_mapping.md](../reference/migration_mapping.md) - State variable reference
- [VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md) - System architecture diagrams
