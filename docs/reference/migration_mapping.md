# Node Red to Home Assistant Variable Mapping

This document maps Node Red global state variables to their Home Assistant entity equivalents.

## Migration Summary

- **Total Node Red state variables**: 65
- **Variables in disabled flows (SKIP)**: 25
- **Active variables to migrate**: 40
- **Already exist in Home Assistant**: 10
- **Need to create**: 30

---

## ✅ ALREADY EXISTS - Will Sync With Existing Entities (10)

These entities already exist in Home Assistant and will be synchronized with Node Red.

| Node Red Variable | Home Assistant Entity | Type | Action |
|------------------|----------------------|------|--------|
| isCarolineHome | input_boolean.caroline_home | Boolean | Sync only |
| isExpectingSomeone | input_boolean.expecting_someone | Boolean | Sync only |
| isGridAvailable | input_boolean.grid_available | Boolean | Sync only |
| isNickHome | input_boolean.nick_home | Boolean | Sync only |
| isToriHere | input_boolean.tori_here | Boolean | Sync only |
| batteryEnergyLevel | input_text.battery_energy_level | String | Sync only |
| currentEnergyLevel | input_text.current_energy_level | String | Sync only |
| dayPhase | input_text.day_phase | String | Sync only |
| musicPlaybackType | input_text.music_playback_type | String | Sync only |
| solarProductionEnergyLevel | input_text.solar_production_energy_level | String | Sync only |

---

## 🆕 NEED TO CREATE - Boolean Variables (19)

| Node Red Variable | Home Assistant Entity | Description | Action |
|------------------|----------------------|-------------|--------|
| isAnyOwnerHome | input_boolean.any_owner_home | Whether any owner is home | Create & sync |
| isAnyoneAsleep | input_boolean.anyone_asleep | Whether anyone is asleep | Create & sync |
| isAnyoneHome | input_boolean.anyone_home | Whether anyone is home | Create & sync |
| isAppleTVPlaying | input_boolean.apple_tv_playing | Apple TV playback status | Create & sync |
| isEveryoneAsleep | input_boolean.everyone_asleep | Everyone asleep status | Create & sync |
| isFadeOutInProgress | input_boolean.fade_out_in_progress | Music fade out status | Create & sync |
| isFreeEnergyAvailable | input_boolean.free_energy_available | Free energy availability | Create & sync |
| isGuestAsleep | input_boolean.guest_asleep | Guest sleep status | Create & sync |
| isGuestBedroomDoorOpen | input_boolean.guest_bedroom_door_open | Guest bedroom door state | Create & sync |
| isHaveGuests | input_boolean.have_guests | Guest presence | Create & sync |
| isMasterAsleep | input_boolean.master_asleep | Master bedroom sleep status | Create & sync |
| isTVPlaying | input_boolean.tv_playing | TV playback status | Create & sync |
| isTVon | input_boolean.tv_on | TV power status | Create & sync |
| isNickOfficeOccupied | input_boolean.nick_office_occupied | Nick's office occupancy sensor | Create & sync |
| isKitchenOccupied | input_boolean.kitchen_occupied | Kitchen occupancy sensor | Create & sync |
| isPrimaryBedroomDoorOpen | input_boolean.primary_bedroom_door_open | Primary bedroom door state | Create & sync |
| isNickNearHome | input_boolean.nick_near_home | Nick proximity geofence trigger | Create & sync |
| isCarolineNearHome | input_boolean.caroline_near_home | Caroline proximity geofence trigger | Create & sync |
| isLockdown | input_boolean.lockdown | Security lockdown momentary trigger | Create & sync |

---

## 🆕 NEED TO CREATE - Numeric Variables (3)

| Node Red Variable | Home Assistant Entity | Type | Min | Max | Step | Unit | Action |
|------------------|----------------------|------|-----|-----|------|------|--------|
| alarmTime | input_number.alarm_time | Number (timestamp) | 0 | 2147483647 | 1 | ms | Create & sync |
| remainingSolarGeneration | input_number.remaining_solar_generation | Number | 0 | 100000 | 0.1 | kWh | Create & sync |
| thisHourSolarGeneration | input_number.this_hour_solar_generation | Number | 0 | 100000 | 0.1 | kW | Create & sync |

---

## 🆕 NEED TO CREATE - Text Variables (1)

| Node Red Variable | Home Assistant Entity | Description | Example Values | Action |
|------------------|----------------------|-------------|----------------|--------|
| sunevent | input_text.sunevent | Current sun event | "morning", "day", "sunset", "dusk", "night" | Create & sync |

---

## 🆕 NEED TO CREATE - JSON Object Variables (2)

These are complex objects stored as JSON strings in input_text entities.

| Node Red Variable | Home Assistant Entity | Max Length | Description | Action |
|------------------|----------------------|------------|-------------|--------|
| currentlyPlayingMusic | input_text.currently_playing_music | 4096 | Current music playback info (JSON) | Create & sync |
| musicPlaylistRotation | input_text.music_playlist_rotation | 255 | Playlist rotation indices per music type (JSON) | Create & sync |

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
1. **On Node Red startup**: Read all 39 variables from Home Assistant → initialize Node Red state
2. **On Node Red variable change**: Write value to corresponding Home Assistant entity
3. **During migration**: Both systems share state via Home Assistant entities
