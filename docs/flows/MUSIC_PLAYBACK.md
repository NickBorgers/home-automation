# Music Playback Flow

This document describes the music playback automation flow, which manages whole-home audio based on day phase, sleep state, and room occupancy.

## Overview

The music plugin coordinates Sonos speaker playback by:
1. Selecting appropriate music mode based on day phase and sleep state
2. Building speaker groups with the right participants
3. Applying room-specific volume and mute conditions
4. Managing playlist rotation for variety

## Music Mode Selection

```mermaid
flowchart TD
    subgraph Triggers["State Change Triggers"]
        dayPhase["dayPhase changed"]
        isAnyoneAsleep["isAnyoneAsleep changed"]
        isAnyoneHome["isAnyoneHome changed"]
    end

    subgraph Priority["Priority Checks (in order)"]
        checkHome{"isAnyoneHome?"}
        checkSleep{"isAnyoneAsleep?"}
        checkPhase["Evaluate dayPhase"]
    end

    subgraph Actions["Music Mode Actions"]
        stopMusic["Stop music<br/>(set type = empty)"]
        sleepMode["Sleep mode<br/>(rain sounds)"]
        phaseMode["Phase-based mode"]
    end

    dayPhase --> checkHome
    isAnyoneAsleep --> checkHome
    isAnyoneHome --> checkHome

    checkHome -->|No| stopMusic
    checkHome -->|Yes| checkSleep
    checkSleep -->|Yes| sleepMode
    checkSleep -->|No| checkPhase
    checkPhase --> phaseMode

    style stopMusic fill:#e74c3c,color:#fff
    style sleepMode fill:#1a1a2e,color:#fff
    style phaseMode fill:#3498db,color:#fff
```

## Day Phase to Music Mode Mapping

The music mode is determined by the current day phase with additional context:

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

    morning --> wakeup
    wakeup -->|Yes| sunday
    wakeup -->|No| MM_day
    sunday -->|Yes| MM_day
    sunday -->|No| MM_morning

    day --> MM_day

    sunset --> MM_evening
    dusk --> MM_evening

    winddown --> sleeping
    night --> sleeping
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
| `winddown` | winddown, night | Floating through space, ambient relaxation | Pre-sleep relaxation |
| `sleep` | (triggered by sleep state) | Rain sounds | Triggered by isAnyoneAsleep, not day phase |
| `wakeup` | (alarm trigger) | Ambient relaxation | Gentle wake-up music (set by sleep hygiene plugin) |

## Playback Orchestration

When a music mode is selected, the following sequence executes:

```mermaid
sequenceDiagram
    participant SM as State Manager
    participant Music as Music Plugin
    participant Sonos as Sonos Speakers
    participant HA as Home Assistant

    SM->>Music: musicPlaybackType changed

    Note over Music: Rate limit check<br/>(max 1 per 10 sec)

    Music->>Music: Select playlist (rotation)

    Note over Music: Filter speakers by<br/>exclude_if conditions

    Note over Music,Sonos: Quick fade-out (500ms)<br/>Prevents jarring audio cutoff
    Music->>Sonos: Fade volume to 0

    Music->>Sonos: Break existing groups (unjoin)
    Music->>Sonos: Build new speaker group

    Music->>Sonos: Set all volumes to 0
    Music->>Sonos: Start playback on lead
    Music->>Sonos: Enable shuffle & repeat

    loop For each speaker
        alt Should unmute (occupancy)
            Music->>Sonos: Fade in volume (0→target)
        else Leave muted
            Music->>Sonos: Set target volume (stays muted)
        end
    end

    Music->>HA: Update currentlyPlayingMusicUri
```

## Speaker Zone Management

Speakers are filtered by `exclude_if` conditions at orchestration time, then muted/unmuted based on `leave_muted_if` conditions:

```mermaid
flowchart TD
    subgraph Config["Speaker Configuration"]
        speaker["Configured Speaker"]
        excludeIf["exclude_if conditions<br/>(zone exclusion)"]
        muteIf["leave_muted_if conditions<br/>(room occupancy)"]
    end

    subgraph Exclusion["Zone Exclusion (Phase 1)"]
        checkExclude{"exclude_if<br/>matches?"}
        excluded["Speaker excluded<br/>from group"]
        included["Speaker included<br/>in group"]
    end

    subgraph Muting["Dynamic Muting"]
        checkMute{"leave_muted_if<br/>matches?"}
        muted["Speaker muted<br/>(in group, silent)"]
        unmuted["Speaker unmuted<br/>(playing)"]
    end

    speaker --> checkExclude
    excludeIf --> checkExclude
    checkExclude -->|Yes| excluded
    checkExclude -->|No| included

    included --> checkMute
    muteIf --> checkMute
    checkMute -->|Yes| muted
    checkMute -->|No| unmuted

    style excluded fill:#e74c3c,color:#fff
    style muted fill:#f39c12,color:#fff
    style unmuted fill:#27ae60,color:#fff
```

### Occupancy-Based Muting Example

```yaml
# From music_config.yaml
participants:
  - player_name: "Office"
    base_volume: 6
    leave_muted_if:
      - variable: "isNickOfficeOccupied"
        value: false
```

When `isNickOfficeOccupied` changes from `false` to `true`:
1. Music plugin detects the mute condition variable change
2. Re-evaluates the speaker's mute conditions
3. Unmutes the Office speaker (already at target volume)

## Volume Fade-In

Speakers fade in gradually to avoid sudden loud sounds:

```mermaid
flowchart LR
    subgraph FadeIn["Fade-In Process"]
        start["Start at 0%"]
        step["Increase by 1%"]
        delay["Wait (adaptive delay)"]
        check{"Target<br/>reached?"}
        done["Fade complete"]
    end

    subgraph AdaptiveDelay["Delay Calculation"]
        formula["delay = (100 - volume) × 250ms"]
        examples["0% → 25s delay<br/>50% → 12.5s delay<br/>90% → 2.5s delay"]
    end

    start --> step
    step --> delay
    delay --> check
    check -->|No| step
    check -->|Yes| done

    formula --> delay
    examples --> formula

    style start fill:#1a1a2e,color:#fff
    style done fill:#27ae60,color:#fff
```

### Human Override Detection

During fade-in, if the actual volume is significantly lower than expected (>2% difference), the system assumes a human manually lowered the volume and aborts the fade.

## Quick Fade-Out on Transitions

When playback mode changes or music stops, speakers perform a quick fade-out (500ms) before ungrouping or stopping. This prevents jarring audio cutoffs when:

- Switching between music types (e.g., day → evening)
- Stopping playback entirely
- Regrouping speakers for a new configuration

The fade-out uses 5 steps at 100ms intervals, smoothly reducing volume from current level to zero before any speaker group changes occur.

> **Note:** This is different from the gradual sleep music fade-out (5+ minutes) used during wake sequences. See [SLEEP_HYGIENE.md](./SLEEP_HYGIENE.md) for the wake sequence fade-out.

## Playlist Rotation

Playlists rotate to provide variety:

```mermaid
flowchart LR
    subgraph Rotation["Playlist Rotation"]
        current["Current Index<br/>per music type"]
        select["Select playlist<br/>at current index"]
        increment["Increment index<br/>(wrap around)"]
        persist["Persist to HA<br/>(musicPlaylistRotation)"]
    end

    current --> select
    select --> increment
    increment --> persist
    persist --> current

    style select fill:#3498db,color:#fff
```

## Related Documentation

- [DAY_PHASE_MODES.md](./DAY_PHASE_MODES.md) - Day phase calculation and schedule configuration
- [SLEEP_HYGIENE.md](./SLEEP_HYGIENE.md) - Wake-up sequence and sleep music triggers
- [PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md) - Plugin architecture
- [SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Shadow state pattern for debugging
