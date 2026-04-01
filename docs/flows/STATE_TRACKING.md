# State Tracking Flow

This document describes the state tracking automation flow, which computes derived presence and sleep states from individual sensor inputs.

## Overview

The state tracking plugin provides:
1. Derived presence states (isAnyoneHome, isAnyOwnerHome)
2. Derived sleep states (isAnyoneAsleep, isEveryoneAsleep)
3. Automatic sleep/wake detection from lights and doors
4. Arrival announcements via TTS
5. Owner return detection for garage automation

## Derived State Computation

```mermaid
flowchart TD
    subgraph Inputs["Source States"]
        nick["isNickHome"]
        caroline["isCarolineHome"]
        tori["isToriHere"]
        master["isMasterAsleep"]
        guest["isGuestAsleep"]
    end

    subgraph Derived["Derived States"]
        anyOwner["isAnyOwnerHome<br/>= Nick OR Caroline"]
        anyone["isAnyoneHome<br/>= anyOwner OR Tori"]
        anyAsleep["isAnyoneAsleep<br/>= Master OR Guest"]
        everyAsleep["isEveryoneAsleep<br/>= Master AND Guest"]
    end

    nick --> anyOwner
    caroline --> anyOwner
    anyOwner --> anyone
    tori --> anyone
    master --> anyAsleep
    guest --> anyAsleep
    master --> everyAsleep
    guest --> everyAsleep

    style anyOwner fill:#3498db,color:#fff
    style anyone fill:#27ae60,color:#fff
    style anyAsleep fill:#6c5ce7,color:#fff
    style everyAsleep fill:#1a1a2e,color:#fff
```

### Boolean Logic Table

| Nick | Caroline | Tori | Master Asleep | Guest Asleep | isAnyOwnerHome | isAnyoneHome | isAnyoneAsleep | isEveryoneAsleep |
|------|----------|------|---------------|--------------|----------------|--------------|----------------|------------------|
| Y | N | N | N | N | Y | Y | N | N |
| N | Y | N | N | N | Y | Y | N | N |
| N | N | Y | N | N | N | Y | N | N |
| N | N | N | Y | N | N | N | Y | N |
| Y | Y | N | Y | Y | Y | Y | Y | Y |

## Automatic Sleep Detection

### Master Asleep Detection

```mermaid
flowchart TD
    subgraph Trigger["Trigger: Primary Suite Lights Off"]
        lightsOff["light.primary_suite<br/>state = off"]
    end

    subgraph Timer["1-Minute Timer"]
        start["Start timer"]
        cancel["Cancel timer<br/>(lights back on)"]
        expire["Timer expires"]
    end

    subgraph Check["Condition Check"]
        checkHome{"isAnyoneHome?"}
        checkAsleep{"Already<br/>isMasterAsleep?"}
    end

    subgraph Action["Action"]
        setAsleep["Set isMasterAsleep = true"]
        skip["No action"]
    end

    lightsOff --> start
    start -->|Lights turn on| cancel
    start -->|1 minute passes| expire
    expire --> checkHome
    checkHome -->|No| skip
    checkHome -->|Yes| checkAsleep
    checkAsleep -->|Yes| skip
    checkAsleep -->|No| setAsleep

    style setAsleep fill:#6c5ce7,color:#fff
    style cancel fill:#e74c3c,color:#fff
```

### Master Awake Detection

```mermaid
flowchart TD
    subgraph Trigger["Trigger: Bedroom Door Opens"]
        doorOpen["input_boolean.primary_bedroom_door_open<br/>state = on"]
    end

    subgraph Timer["20-Second Timer"]
        start["Start timer"]
        cancel["Cancel timer<br/>(door closes)"]
        expire["Timer expires"]
    end

    subgraph Action["Action"]
        setAwake["Set isMasterAsleep = false"]
    end

    doorOpen --> start
    start -->|Door closes| cancel
    start -->|20 seconds pass| expire
    expire --> setAwake

    style setAwake fill:#ffd93d,color:#000
    style cancel fill:#e74c3c,color:#fff
```

## Arrival Announcements

When someone arrives home and others are present, a TTS announcement plays:

```mermaid
flowchart TD
    subgraph Trigger["Arrival Detection"]
        nickArrives["input_boolean.nick_home<br/>changes to 'on'"]
        carolineArrives["input_boolean.caroline_home<br/>changes to 'on'"]
        toriArrives["input_boolean.tori_here<br/>changes to 'on'"]
    end

    subgraph Debounce["Arrival Debounce (issue #922)"]
        debounceCheck{"Departed < 5min ago?<br/>(sensor bounce)"}
        suppressed["Suppressed<br/>(presence sensor bounce)"]
    end

    subgraph Check["Announcement Check"]
        wasAnyoneHome{"Was anyone else<br/>already home?"}
    end

    subgraph Action["TTS Announcement"]
        announceNick["'Nick is home'"]
        announceCaroline["'Caroline is home'"]
        announceTori["'Tori is here'"]
        skip["No announcement<br/>(no one to hear it)"]
    end

    nickArrives --> debounceCheck
    carolineArrives --> debounceCheck
    toriArrives --> debounceCheck

    debounceCheck -->|Yes| suppressed
    debounceCheck -->|No| wasAnyoneHome

    wasAnyoneHome -->|Yes, Nick arriving| announceNick
    wasAnyoneHome -->|Yes, Caroline arriving| announceCaroline
    wasAnyoneHome -->|Yes, Tori arriving| announceTori
    wasAnyoneHome -->|No| skip

    style suppressed fill:#e74c3c,color:#fff
    style skip fill:#95a5a6,color:#fff
```

### Speaker Selection by Person

| Person | TTS Speakers |
|--------|--------------|
| Nick | Kitchen, Dining Room, Soundbar, Kids Bathroom |
| Caroline | Kitchen, Dining Room, Kids Bathroom, Soundbar, Office |
| Tori | Kitchen, Dining Room, Kids Bathroom, Soundbar, Office |

## Owner Return Detection

The `didOwnerJustReturnHome` state tracks recent owner arrivals for garage automation:

```mermaid
flowchart TD
    subgraph Triggers["Return Triggers"]
        homeChange["isNickHome/isCarolineHome<br/>changes to true"]
        nearHome["nick_near_home/caroline_near_home<br/>triggers while NOT home"]
    end

    subgraph Action["Set didOwnerJustReturnHome"]
        setTrue["Set didOwnerJustReturnHome = true"]
        startTimer["Start 10-minute timer"]
    end

    subgraph Reset["Auto-Reset"]
        timerExpires["10 minutes elapsed"]
        ownerLeaves["Owner leaves home"]
        setFalse["Set didOwnerJustReturnHome = false"]
    end

    homeChange --> setTrue
    nearHome --> setTrue
    setTrue --> startTimer
    startTimer --> timerExpires
    timerExpires --> setFalse
    ownerLeaves --> setFalse

    style setTrue fill:#27ae60,color:#fff
    style setFalse fill:#e74c3c,color:#fff
```

### Near-Home Geofence Logic

The near_home trigger activates for both arrivals and departures. We filter to only act on arrivals:

```mermaid
flowchart TD
    subgraph Trigger["Near-Home Triggers"]
        nearHomeOn["near_home sensor<br/>turns on"]
    end

    subgraph Check["Direction Check"]
        isHome{"Person currently<br/>marked as home?"}
    end

    subgraph Action["Action"]
        arriving["ARRIVING: Set<br/>didOwnerJustReturnHome = true"]
        leaving["LEAVING: Ignore<br/>(no action)"]
    end

    nearHomeOn --> isHome
    isHome -->|No| arriving
    isHome -->|Yes| leaving

    style arriving fill:#27ae60,color:#fff
    style leaving fill:#95a5a6,color:#fff
```

## State Variables

### Source Inputs

| Variable | Source Entity | Description |
|----------|---------------|-------------|
| `isNickHome` | `input_boolean.nick_home` | Nick's presence |
| `isCarolineHome` | `input_boolean.caroline_home` | Caroline's presence |
| `isToriHere` | `input_boolean.tori_here` | Tori's presence |
| `isMasterAsleep` | (computed) | Primary suite occupant asleep |
| `isGuestAsleep` | (computed) | Guest room occupant asleep |

### Derived Outputs

| Variable | Formula | Description |
|----------|---------|-------------|
| `isAnyOwnerHome` | Nick OR Caroline | Any owner present |
| `isAnyoneHome` | anyOwner OR Tori | Anyone present |
| `isAnyoneAsleep` | Master OR Guest | Someone asleep |
| `isEveryoneAsleep` | Master AND Guest | Everyone asleep |
| `didOwnerJustReturnHome` | (tracked) | Owner arrived recently |

### Detection Inputs

| Entity | Timer | Action |
|--------|-------|--------|
| `light.primary_suite` | 1 minute off | Mark master asleep |
| `input_boolean.primary_bedroom_door_open` | 20 seconds open | Mark master awake |
| `input_boolean.nick_near_home` | Immediate | Set didOwnerJustReturnHome |
| `input_boolean.caroline_near_home` | Immediate | Set didOwnerJustReturnHome |

## Related Documentation

- [SECURITY.md](./SECURITY.md) - Uses derived states for lockdown
- [MUSIC_PLAYBACK.md](./MUSIC_PLAYBACK.md) - Uses presence for music control
- [LIGHTING_CONTROL.md](./LIGHTING_CONTROL.md) - Uses sleep state for scenes
- [SLEEP_HYGIENE.md](./SLEEP_HYGIENE.md) - Uses sleep state for wake sequence
