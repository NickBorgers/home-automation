# TV and Hue Sync Brightness

**Status:** Built 2026-11-15. The mapping endpoints are guesses. Tune them by eye.

## Summary

The TV picture brightness and the Hue Sync box brightness follow the Living room's target lux. The target is the same lux curve the ceiling cans use ([LUX_LIGHTING.md](LUX_LIGHTING.md)). In the day both are bright. Through the evening both dim with the room.

This replaces the Node-RED flow, which set the TV to one of three fixed levels at day phase changes.

## How it works

```mermaid
flowchart LR
    Curve[Lux curve<br/>Living room] -->|every 60 s| Var[tvRoomTargetLux]
    Var --> TVPlugin[TV plugin]
    TVPlugin -->|on?| Map[Map lux to brightness<br/>log scale]
    Map -->|REST, X-Auth-PSK| TV[Bravia TV<br/>brightness 0-50]
    Map -->|number.set_value| Sync[number.sync_box_brightness<br/>1-100]
```

1. The lighting plugin computes the Living target lux every minute. This includes `lux_scale` and the guest boost. It publishes the value as the local-only variable `tvRoomTargetLux`. It does this even while the room is in `skip` because Hue Sync owns the lights.
2. The TV plugin maps the target to a share between 0 and 1, on a log scale. 2.5 lux or less gives 0. 150 lux or more gives 1.
3. The TV gets `min + (0.3 + 0.7 × share) × (max − min)`, using the range the TV reports. On the A80K that is 15 at night and 50 by day.
4. The sync box gets `20 + 80 × share`. That is 20% at night and 100% by day.

| Target lux | Share | TV (0-50) | Sync box |
|-----------:|------:|----------:|---------:|
| 150 (day) | 1.00 | 50 | 100 |
| 50 | 0.73 | 41 | 79 |
| 20 | 0.51 | 33 | 61 |
| 7 | 0.25 | 24 | 40 |
| 2.5 (night) | 0.00 | 15 | 20 |

The plugin acts only while the TV is on (`media_player.sony_xr_65a80k`). It reads each device first, and it sends a value only when the value changed. The TV changes in steps of 1. The sync box changes in steps of at least 2. The curve moves slowly, so a step is rare and small.

## Turning on

When the TV turns on, the plugin waits 10 seconds. The Bravia API answers "not power-on" while the panel starts. Then it sets both devices to the current target.

## Manual changes

A person can change the TV brightness with the remote, or the sync box brightness in the Hue app. The plugin keeps that choice until the TV turns off and on again.

- Before each change, the plugin reads the device. If the value is not the last value it set, a person changed it. The plugin holds that device.
- The TV and the sync box are held separately.
- A picture mode change can also change the TV brightness. For example, Dolby Vision content uses its own picture mode. The plugin sees this as a manual change and holds the TV. This is on purpose: the plugin does not fight the TV.

## Setup

The TV brightness needs the TV's pre-shared key. Home Assistant's `braviatv` integration does not expose picture settings. So the app calls the TV's REST API directly: `POST http://<BRAVIA_IP>/sony/video` with `getPictureQualitySettings` and `setPictureQualitySettings`. These are the same calls as pybravia's `get_picture_setting` and `set_picture_setting`.

1. On the TV, set Settings > Network > Home Network Setup > IP Control > Pre-Shared Key.
2. Give the TV a fixed LAN address.
3. Set `BRAVIA_IP` and `BRAVIA_PSK` in the app's environment. See `homeautomation-go/.env.example` for quoting a key with special characters.
4. At startup, check the log line `TV brightness control enabled`. Its `psk_length` must match your key's length.

Without these variables, the sync box still follows the room, and the TV brightness is left alone.

Only one lux room may set `tv_room: true` in `hue_config.yaml`. The Living room sets it.

## Tuning

The constants are in `homeautomation-go/internal/plugins/tv/brightness.go`:

| Constant | Value | Meaning |
|----------|------:|---------|
| `BrightnessDarkLux` | 2.5 | Target at or below which both devices are at their dimmest |
| `BrightnessBrightLux` | 150 | Target at or above which both devices are at their brightest |
| `TVBrightnessDarkFraction` | 0.3 | TV's dimmest setting, as a share of its range |
| `SyncBoxBrightnessDark` / `Bright` | 20 / 100 | Sync box range |

The two lux ends match the Living curve with `lux_scale: 0.5`. If `lux_scale` changes, change them too.

## Shadow state

`/api/shadow/tv` shows the last decision in `outputs.brightness`:

| Field | Meaning |
|-------|---------|
| `trigger` | `target_lux` or `tv_power_on` |
| `targetLux`, `fraction` | The target and its share on the log scale |
| `decision` | `applied`, `TV off` or `no target lux yet` |
| `tv`, `syncBox` | Per device: `goal`, `current`, `held`, `heldReason`, `decision` |

## Debugging

```bash
scripts/loki-query.sh '{service="home-automation"} | json | logger="tv" |~ "(?i)brightness"' 6h
```
