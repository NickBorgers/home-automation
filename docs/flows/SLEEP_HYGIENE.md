# Sleep Hygiene Flow

This document describes the sleep hygiene automation flow, which manages bedtime reminders, wake-up sequences, and sleep music transitions.

## Overview

The sleep hygiene plugin provides:
1. Scheduled reminders for screen stop and bedtime
2. Coordinated wake-up sequence with Eight Sleep integration
3. Gradual sleep music fade-out
4. Slow bedroom light fade-in for gentle waking

## Daily Schedule Triggers

```mermaid
flowchart TD
    subgraph Schedule["Scheduled Events"]
        stopScreens["stop_screens<br/>(~22:30)"]
        goToBed["go_to_bed<br/>(~23:00)"]
        backupWake["begin_backup_wake<br/>(~07:00)"]
    end

    subgraph Actions["Triggered Actions"]
        flashLights1["Flash common area lights"]
        flashLights2["Flash common area lights"]
        startSleep["Set musicPlaybackType = sleep"]
        triggerWake["Trigger wake sequence"]
    end

    stopScreens --> flashLights1
    goToBed --> flashLights2
    goToBed --> startSleep
    backupWake --> triggerWake

    style stopScreens fill:#f39c12,color:#fff
    style goToBed fill:#6c5ce7,color:#fff
    style backupWake fill:#ffd93d,color:#000
```

### Schedule Time Sources

| Trigger | Time Source | Purpose |
|---------|-------------|---------|
| `stop_screens` | `schedule_config.yaml` | Reminder to turn off screens |
| `go_to_bed` | `schedule_config.yaml` | Bedtime reminder + sleep music |
| `begin_backup_wake` | `schedule_config.yaml` | Backup wake if Eight Sleep unavailable |

## Wake-Up Sequence

The primary wake trigger comes from Eight Sleep Pod alarm sensors. A backup time-based trigger activates only when Eight Sleep is unavailable.

```mermaid
sequenceDiagram
    participant ES as Eight Sleep
    participant SH as Sleep Hygiene
    participant Music as Music Plugin
    participant Lights as Home Assistant

    Note over ES: Alarm activates

    ES->>SH: bed_state_type = "alarm"

    SH->>SH: Check conditions:<br/>isAnyoneHome, isMasterAsleep,<br/>musicPlaybackType == "sleep"

    alt Conditions met
        SH->>SH: Set isWakeSequenceActive = true
        SH->>Music: Start fade-out<br/>(volume → 0 gradually)

        Note over SH: Wait 5 minutes

        SH->>Lights: Turn on bedroom lights<br/>(1% brightness, warm white)
        SH->>Lights: Start 25-min fade<br/>(1% → 100%)

        Note over SH: Wait 25 minutes<br/>(lights fading in)

        SH->>Music: Set musicPlaybackType = "wakeup"
        SH->>SH: Wake sequence complete
    else Conditions not met
        Note over SH: Skip wake sequence
    end
```

### Wake Sequence Timeline

```mermaid
gantt
    title Wake-Up Sequence (30 minutes total)
    dateFormat HH:mm
    axisFormat %H:%M

    section Audio
    Sleep music fade-out    :fadeout, 07:00, 5m
    Silence                 :silence, after fadeout, 25m
    Wake music starts       :wakemusic, 07:30, 1m

    section Lights
    Waiting                 :wait, 07:00, 5m
    Light fade-in (1%→100%) :fadein, 07:05, 25m
```

### Eight Sleep Integration

```mermaid
flowchart TD
    subgraph Detection["Alarm Detection"]
        nickSensor["Nick's Eight Sleep<br/>bed_state_type sensor"]
        carolineSensor["Caroline's Eight Sleep<br/>bed_state_type sensor"]
        checkState{"State == 'alarm'?"}
    end

    subgraph Dedup["Deduplication"]
        checkTriggered{"Already triggered<br/>today?"}
        trigger["Trigger begin_wake"]
        skip["Skip (already triggered)"]
    end

    nickSensor --> checkState
    carolineSensor --> checkState
    checkState -->|Yes| checkTriggered
    checkState -->|No| skip
    checkTriggered -->|No| trigger
    checkTriggered -->|Yes| skip

    style trigger fill:#27ae60,color:#fff
    style skip fill:#e74c3c,color:#fff
```

### Backup Wake Trigger

```mermaid
flowchart TD
    subgraph Backup["Backup Wake Check"]
        checkTime{"Current time ><br/>begin_backup_wake?"}
        checkAvail{"Eight Sleep<br/>available?"}
        checkTriggered{"Already triggered<br/>today?"}
    end

    subgraph Actions["Actions"]
        skip1["Use Eight Sleep<br/>(no backup needed)"]
        skip2["Skip<br/>(already triggered)"]
        trigger["Trigger begin_wake"]
    end

    checkTime -->|Yes| checkAvail
    checkTime -->|No| skip1
    checkAvail -->|Yes| skip1
    checkAvail -->|No| checkTriggered
    checkTriggered -->|Yes| skip2
    checkTriggered -->|No| trigger

    style skip1 fill:#3498db,color:#fff
    style trigger fill:#f39c12,color:#fff
```

Eight Sleep availability is checked by querying the sleep stage sensors. If both Nick's and Caroline's sensors are "unavailable", the backup wake trigger activates.

## Sleep Music Fade-Out

```mermaid
flowchart TD
    subgraph FadeOut["Fade-Out Process"]
        getVolume["Get current volume<br/>(e.g., 60%)"]
        reduce["Reduce by 1%"]
        delay["Wait (adaptive delay)"]
        checkAbort{"Aborted?<br/>(wake sequence cancelled)"}
        checkZero{"Volume == 0?"}
    end

    subgraph Delays["Adaptive Delay"]
        formula["delay = (60 - volume) seconds"]
        examples["60% → 0s delay<br/>30% → 30s delay<br/>10% → 50s delay"]
    end

    getVolume --> reduce
    reduce --> delay
    delay --> checkAbort
    checkAbort -->|Yes| stop["Stop fade-out"]
    checkAbort -->|No| checkZero
    checkZero -->|No| reduce
    checkZero -->|Yes| done["Fade complete"]

    formula --> delay
    examples --> formula

    style done fill:#27ae60,color:#fff
    style stop fill:#e74c3c,color:#fff
```

### Human Override Detection

During fade-out, if someone manually increases the volume (actual > expected + 2%), the fade-out aborts:

```mermaid
flowchart LR
    expected["Expected: 30%"]
    actual["Actual: 45%"]
    compare{"Actual > Expected + 2%?"}
    abort["Human override<br/>Abort fade-out"]
    continue["Continue fade"]

    expected --> compare
    actual --> compare
    compare -->|Yes| abort
    compare -->|No| continue

    style abort fill:#f39c12,color:#fff
```

## Bedroom Light Fade-In

```mermaid
flowchart LR
    subgraph Initial["Initial State"]
        start["Brightness: 1%<br/>Color: 2000K (warm)"]
    end

    subgraph Transition["25-Minute Fade"]
        mid["Brightness: 50%<br/>Color: 2500K"]
    end

    subgraph Final["Final State"]
        end_["Brightness: 100%<br/>Color: 3000K (natural)"]
    end

    start -->|12.5 min| mid
    mid -->|12.5 min| end_

    style start fill:#1a1a2e,color:#fff
    style mid fill:#ff8c42,color:#fff
    style end_ fill:#ffd93d,color:#000
```

## Wake Sequence Cancellation

If bedroom lights are turned off during the wake sequence:

```mermaid
flowchart TD
    subgraph Detection["Cancel Detection"]
        lightsOff["Bedroom lights<br/>turned off"]
        graceCheck{"Within 2s of<br/>turn_on command?"}
        checkActive{"isWakeSequenceActive<br/>OR musicPlaybackType == 'wakeup'?"}
    end

    subgraph Actions["Cancel Actions"]
        clearFlag["Clear isWakeSequenceActive"]
        revertMusic["Set musicPlaybackType = 'sleep'"]
        offBathroom["Turn off bathroom lights"]
    end

    lightsOff --> graceCheck
    graceCheck -->|Yes| transient["Ignore<br/>(transient HA group event)"]
    graceCheck -->|No| checkActive
    checkActive -->|Yes| clearFlag
    clearFlag --> revertMusic
    revertMusic --> offBathroom
    checkActive -->|No| ignore["No action"]

    style clearFlag fill:#e74c3c,color:#fff
    style ignore fill:#95a5a6,color:#fff
    style transient fill:#95a5a6,color:#fff
```

> **Note:** Home Assistant group entities (e.g., `light.primary_suite`) emit transient "off" events (~200-500ms) after a `turn_on` command while constituent lights are still responding. A 2-second grace period after each turn-on command filters these out, preventing false wake cancellation.

## State Variables

### Inputs

| Variable | Type | Source | Description |
|----------|------|--------|-------------|
| `isAnyoneHome` | bool | State Tracking | Someone is home |
| `isMasterAsleep` | bool | State Tracking | Master bedroom occupied/asleep |
| `musicPlaybackType` | string | Music Plugin | Current music mode |
| `isEveryoneAsleep` | bool | State Tracking | All tracked people asleep |

### Outputs

| Variable | Type | Description |
|----------|------|-------------|
| `isFadeOutInProgress` | bool | Sleep music is fading out |
| `isWakeSequenceActive` | bool | Wake sequence lights are protected |
| `musicPlaybackType` | string | Set to "sleep" at bedtime, "wakeup" after lights up |

## Related Documentation

- [DAY_PHASE_MODES.md](./DAY_PHASE_MODES.md) - Day phase and schedule configuration
- [MUSIC_PLAYBACK.md](./MUSIC_PLAYBACK.md) - Music mode transitions
- [LIGHTING_CONTROL.md](./LIGHTING_CONTROL.md) - Wake sequence light protection
- [STATE_TRACKING.md](./STATE_TRACKING.md) - Sleep detection logic
