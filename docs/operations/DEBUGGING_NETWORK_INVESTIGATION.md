# Prompt: investigate unexplained bedroom-light-off events via network traffic

Hand this prompt to a Claude Code instance that has access to network traffic
logs (packet captures, Hue Bridge HTTP logs, Lutron telnet/LEAP captures, MQTT
broker logs, or similar). It is self-contained — the receiving instance does
not need this conversation's context.

---

You're investigating an unexplained light-off event in a home automation system.
I need you to use network traffic logs to identify what caused specific Hue
lights to turn off, because Home Assistant's event log shows no cause.

# The incident

On 2026-05-25 between 23:01:18 UTC and 23:02:17 UTC (18:01–18:02 CDT), three
specific Philips Hue bulbs in the master bedroom turned off in sequence, with
no Home Assistant automation, script, or service call accounting for it.

Exact HA entity-state changes:

```
23:01:18.478 UTC  scene.primary_suite_day activated (by home automation Go app)
23:01:21      UTC  light.bedroom_closet_corner    off -> on   (scene took effect)
23:01:21      UTC  light.nick_side_of_bed         off -> on
23:01:21      UTC  light.hue_filament_bulb_1      off -> on
23:01:21      UTC  light.hue_filament_bulb_1_2    off -> on
23:01:21      UTC  light.guest_bedroom_lamp       off -> on
23:01:22      UTC  light.primary_suite (group)    off -> on
----- the unexplained off events follow -----
23:01:40      UTC  light.bedroom_closet_corner    on -> off   (+19s)
23:01:54      UTC  light.nick_side_of_bed         on -> off   (+33s)
23:02:16      UTC  light.hue_filament_bulb_1      on -> off   (+55s)
23:02:17      UTC  light.primary_suite (group)    on -> off   (all members now off)
```

`light.hue_filament_bulb_1_2` and `light.guest_bedroom_lamp` did NOT go off in
this window — they went off later at 23:03:20 from a different cause.

These off events do not appear to be brightness fades — HA reports discrete
on/off state transitions, meaning brightness reached 0.

# What's already ruled out (do not redo this work)

From the Home Assistant entity event stream during 23:01:21–23:02:17:

- No HA automation triggered
- No HA script ran
- No `scene.turn_on` / `scene.turn_off` service was called
- No `light.turn_off` service call from the Go home-automation app
- The Go lighting plugin made no calls to Primary Suite between 23:01:18.948
  and 23:03:07
- No `input_boolean`, `input_button`, or `switch` entity changed state
- No bedroom motion / occupancy `binary_sensor` events occurred (only garage
  motion sensors fired in that window)
- Only these three specific bedroom lights went off — every other light in
  the house that came on at 23:01:21 stayed on

The homeowner is confident no Home Assistant automation does this.

# Why network traffic matters

The off commands have to be coming from somewhere. Candidates that bypass the
HA event log:

- Hue Bridge internal rules ("Behaviors"), Hue motion-sensor rules executing
  on the bridge itself, or Hue Routines from the Hue mobile app
- Lutron Pico / Caseta scenes (some Lutron integrations don't surface every
  button press as an HA event)
- Direct REST/CLIP API calls to the Hue Bridge from another controller or app
- Smart wall switches (Inovelli/Zooz/etc.) signaling the Hue Bridge directly
- A Hue Sync Box, Hue Dimmer Switch, or similar physical accessory

# What I need you to find

Inspect network traffic for the window 2026-05-25T23:01:18Z to 2026-05-25T23:02:30Z.
Identify the source and content of the commands that turned off these three
bulbs. Specifically:

1. Look for traffic to/from the Hue Bridge. The bridge typically runs an
   HTTPS API on port 443 (and a legacy HTTP API on port 80). Look for `PUT`
   requests to `/api/<username>/lights/<id>/state` with `"on": false` or
   `"bri": 0`, OR CLIP v2 requests (`/clip/v2/resource/light/<uuid>`) with
   equivalent payloads. Note the source IP and timing.

2. If a Lutron Caseta Smart Bridge or RA2 processor is present, look for
   telnet (port 23) or LEAP (port 8081 / TLS) traffic with button-press or
   scene-recall events around 23:01:40, 23:01:54, and 23:02:16.

3. If any Zigbee2MQTT, Zigbee coordinator, or hub bridges traffic over MQTT,
   search MQTT publishes targeting those entities or their underlying
   Zigbee device IDs.

4. Check for traffic from the Philips Hue mobile app (recognizable user-agent
   or known phone IPs) — Hue app routines can fire scenes without HA seeing it.

# Expected output

Report back, in plain text under 400 words:

- For each of the three off events (23:01:40, 23:01:54, 23:02:16): the source
  IP, destination, protocol, and payload of the matching network request (or
  "no matching traffic" if absent).
- Whether the three events share a common source (same IP / same device).
- Whether you found a Hue Bridge rule, Lutron Pico press, Hue app routine,
  or other identifiable trigger.
- If no traffic accounts for the off events at all, say so explicitly —
  that would suggest the bulbs themselves are responding to a non-network
  signal (mains-power blip, Zigbee mesh command from a battery accessory,
  etc.) and the investigation needs different instrumentation.

Useful entity → likely device mapping (if your traffic logs use device names
or IPs instead of HA entity IDs):

- `light.bedroom_closet_corner` — likely a Hue downlight or accent bulb
- `light.nick_side_of_bed` — likely a Hue bedside lamp
- `light.hue_filament_bulb_1` — Hue Filament E26/E27 bulb (decorative)

Do NOT propose fixes to the home automation Go code — that's tracked separately.
Your job is purely to identify the network source of the off commands.
