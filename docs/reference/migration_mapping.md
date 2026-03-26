# Node Red to Home Assistant Variable Mapping

This document maps Node Red global state variables to their Home Assistant entity equivalents.

**Last Updated:** 2026-01-02

## Migration Summary

- **Total state variables in Go implementation**: 42
- **Synced with Home Assistant**: 40
- **Local-only (memory only)**: 2
- **Booleans**: 29
- **Numbers**: 3
- **Text/String**: 8
- **JSON (local-only)**: 1

---

## Complete State Variable Reference

All 42 state variables currently implemented in the Go application, organized by type.

### Boolean Variables (29)

| Variable | Home Assistant Entity | Description | Flags |
|----------|----------------------|-------------|-------|
| isNickHome | input_boolean.nick_home | Nick's presence status | - |
| isCarolineHome | input_boolean.caroline_home | Caroline's presence status | - |
| isToriHere | input_boolean.tori_here | Tori's presence status | - |
| isAnyOwnerHome | input_boolean.any_owner_home | Whether any owner is home | ComputedOutput |
| isAnyoneHome | input_boolean.anyone_home | Whether anyone is home | ComputedOutput |
| isAnyoneHomeAndAwake | input_boolean.anyone_home_and_awake | Anyone home and not asleep | ComputedOutput |
| isMasterAsleep | input_boolean.master_asleep | Master bedroom sleep status | - |
| isGuestAsleep | input_boolean.guest_asleep | Guest bedroom sleep status | ComputedOutput |
| isAnyoneAsleep | input_boolean.anyone_asleep | Whether anyone is asleep | ComputedOutput |
| isEveryoneAsleep | input_boolean.everyone_asleep | Everyone asleep status | ComputedOutput |
| isGuestBedroomDoorOpen | input_boolean.guest_bedroom_door_open | Guest bedroom door state | - |
| isHaveGuests | input_boolean.have_guests | Guest presence flag | - |
| isAppleTVPlaying | input_boolean.apple_tv_playing | Apple TV playback status | - |
| isTVPlaying | input_boolean.tv_playing | TV playback status | - |
| isTVon | input_boolean.tv_on | TV power status | - |
| isFadeOutInProgress | input_boolean.fade_out_in_progress | Music fade out status | - |
| isWakeSequenceActive | input_boolean.wake_sequence_active | Wake lights fade-in active | - |
| isSleepPrepActive | input_boolean.sleep_prep_active | Sleep prep in progress (go_to_bed fired, waiting for sleep detection) | - |
| isFreeEnergyAvailable | input_boolean.free_energy_available | Free energy availability | - |
| isGridAvailable | input_boolean.grid_available | Grid power availability | - |
| isExpectingSomeone | input_boolean.expecting_someone | Expecting a visitor | - |
| isNickOfficeOccupied | input_boolean.nick_office_occupied | Nick's office occupancy sensor | - |
| isKitchenOccupied | input_boolean.kitchen_occupied | Kitchen occupancy sensor | - |
| isPrimaryBedroomDoorOpen | input_boolean.primary_bedroom_door_open | Primary bedroom door state | - |
| isNickNearHome | input_boolean.nick_near_home | Nick proximity geofence trigger | - |
| isCarolineNearHome | input_boolean.caroline_near_home | Caroline proximity geofence trigger | - |
| isLockdown | input_boolean.lockdown | Security lockdown momentary trigger | - |
| reset | input_boolean.reset | System reset trigger | - |
| isFrontOfHousePersonPresent | input_boolean.front_of_house_person_present | Front porch presence | - |

### Numeric Variables (3)

| Variable | Home Assistant Entity | Description | Range | Unit |
|----------|----------------------|-------------|-------|------|
| alarmTime | input_number.alarm_time | Wake-up alarm timestamp | 0 - 2147483647 | ms |
| remainingSolarGeneration | input_number.remaining_solar_generation | Remaining solar for day | 0 - 100000 | kWh |
| thisHourSolarGeneration | input_number.this_hour_solar_generation | Current hour solar | 0 - 100000 | kW |

### Text/String Variables (8)

| Variable | Home Assistant Entity | Description | Flags |
|----------|----------------------|-------------|-------|
| dayPhase | input_text.day_phase | Current day phase (morning, day, sunset, dusk, winddown, night) | - |
| sunevent | input_text.sun_event | Current sun event | - |
| musicPlaybackType | input_text.music_playback_type | Current music mode (day, evening, sleep, etc.) | - |
| currentlyPlayingMusicUri | input_text.currently_playing_music_uri | URI of currently playing music | - |
| musicPlaylistRotation | input_text.music_playlist_rotation | Playlist rotation state (JSON) | - |
| batteryEnergyLevel | input_text.battery_energy_level | Battery level category | ComputedOutput |
| currentEnergyLevel | input_text.current_energy_level | Overall energy level | ComputedOutput |
| solarProductionEnergyLevel | input_text.solar_production_energy_level | Solar production level | ComputedOutput |

### Local-Only Variables (2)

These variables exist only in memory and are not synced with Home Assistant.

| Variable | Type | Description |
|----------|------|-------------|
| didOwnerJustReturnHome | Boolean | Flag for owner arrival announcements |
| currentlyPlayingMusic | JSON | Current music playback metadata |

---

## Variable Flags Explained

| Flag | Description |
|------|-------------|
| **ComputedOutput** | Variable is computed from other inputs. Can be written even in read-only mode. |
| **LocalOnly** | Variable exists only in Go application memory, not synced with Home Assistant. |

---

## ⏭️ SKIPPED - Variables Only in Disabled Flows (25)

These variables are only referenced in disabled Node Red flows and will NOT be migrated.

| Node Red Variable | Disabled Flow | Reason |
|------------------|---------------|--------|
| currentClimate | Air Condition | Flow disabled |
| desiredHumidityOfMasterBedroom | Air Condition | Flow disabled |
| formaldehydeOfBedroom | Air Condition | Flow disabled |
| formaldehydeOfLivingRoom | Air Condition | Flow disabled |
| formaldehydeOfMasterBedroom | Air Condition | Flow disabled |
| humidityOfBedroom | Air Condition | Flow disabled |
| humidityOfLivingRoomCenter | Air Condition | Flow disabled |
| humidityOfLivingRoomWindow | Air Condition | Flow disabled |
| humidityOfMasterBedroom | Air Condition | Flow disabled |
| humidityOfOutside | Air Condition | Flow disabled |
| isHumidifierOn | Air Condition | Flow disabled |
| keepPoolPumpOnFor24Hours | Pool Pump | Flow disabled |
| lastVacuumingTimestamp | Vacuum | Flow disabled |
| outdoorTemperature | Air Condition | Flow disabled |
| pm25OfBedroom | Air Condition | Flow disabled |
| pm25OfLivingRoom | Air Condition | Flow disabled |
| pm25OfMasterBedroom | Air Condition | Flow disabled |
| temperatureOfBedroom | Air Condition | Flow disabled |
| temperatureOfLivingRoomCenter | Air Condition | Flow disabled |
| temperatureOfLivingRoomWindow | Air Condition | Flow disabled |
| temperatureOfMasterBedroom | Air Condition | Flow disabled |
| temperatureOfOutside | Air Condition | Flow disabled |
| vocOfBedroom | Air Condition | Flow disabled |
| vocOfLivingRoom | Air Condition | Flow disabled |
| vocOfMasterBedroom | Air Condition | Flow disabled |

---

## ⚠️ Special Variable Behaviors

### isLockdown - Momentary Security Trigger

**Behavior**: Acts as a momentary "pulse" trigger for security actions

- **Trigger**: Automatically activated when `isEveryoneAsleep` becomes `true`
- **Auto-Reset**: Stays `true` for **5 seconds**, then automatically resets to `false`
- **Purpose**: Triggers security measures (garage door close, door locks, etc.) when everyone goes to sleep
- **Implementation**: Node-RED uses a 5-second delay before auto-resetting
- **Flow**: Security flow (`7097dab4eb91af0f`)

### isNickNearHome / isCarolineNearHome - Proximity Geofence

**Behavior**: Geofence triggers that activate home presence

- **Trigger**: Set by Home Assistant proximity/geofence sensors
- **Effect**: When `true`, these variables set `isNickHome` / `isCarolineHome` to `true`
- **Important**: Automations (announcements, lights, music) trigger on `isHome`, **NOT** `isNearHome`
- **Purpose**: Provides advance warning before someone arrives home, allowing preparation time
- **Implementation**: `isNearHome` is input-only, `isHome` is the computed output used by automations
- **Flow**: State Tracking flow (`d7a3510d.e93d98`)

### Room Occupancy Sensors

**Behavior**: Direct sensor inputs for room presence

- **isNickOfficeOccupied**: Used by lighting plugin to control N Office lights (2-second transition)
- **isKitchenOccupied**: Used by lighting plugin to control Kitchen lights (5-second transition)
- **Purpose**: Enable instant lighting control based on room occupancy
- **Configuration**: See `configs/hue_config.yaml` for room-specific lighting rules

### Eight Sleep Pod Alarm Sensors

**Behavior**: Direct HA sensor inputs for Eight Sleep mattress alarm state

| Sensor Entity | Description |
|--------------|-------------|
| `sensor.nick_s_eight_sleep_side_bed_state_type` | Nick's Eight Sleep bed state (off, awake, alarm, etc.) |
| `sensor.caroline_s_eight_sleep_side_bed_state_type` | Caroline's Eight Sleep bed state |

- **Purpose**: Provides instant wake-up trigger when Eight Sleep Pod alarm activates
- **Trigger State**: When sensor state becomes `"alarm"`, triggers `begin_wake` sequence immediately
- **Plugin**: Sleep Hygiene (`internal/plugins/sleephygiene/manager.go`)
- **Fallback**: Time-based triggers from `alarmTime` state variable remain as backup
- **Deduplication**: Only triggers once per day; subsequent alarms (from either sensor) are ignored

---

## Implementation Notes

### Entity Creation
- Entities will be created via Home Assistant REST API
- input_boolean: Simple on/off entities
- input_number: Numeric entities with appropriate min/max/step values
- input_text: Text entities for strings and JSON-serialized objects

### Synchronization Strategy
1. **On Go application startup**: Read all 39 synced variables from Home Assistant → initialize state cache
2. **On state variable change**: Write value to corresponding Home Assistant entity
3. **For local-only variables**: Maintain only in memory, not synced with HA
4. **For computed outputs**: Can be written even in read-only mode to publish derived state
