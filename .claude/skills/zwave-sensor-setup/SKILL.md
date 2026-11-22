---
name: zwave-sensor-setup
description: Add a new Z-Wave sensor to Home Assistant, or check an existing one, so that home-automation monitors it. Covers naming the device and its entity IDs, the area, the `monitoring_ignore` label, the battery report threshold, the restart, and an end-to-end test. Use when someone pairs a new Z-Wave sensor (water leak, door, temperature), renames one, or asks why a sensor does not alert.
---

# Z-Wave Sensor Setup

A new Z-Wave sensor is not done when it pairs. It is done when home-automation watches it and a real
test produces the expected alert. Follow the steps in order.

All names below are made-up examples. This is a public repo. Do not write real device names, rooms or
inventory into repo files. Real names go only into Home Assistant.

## Helper

`scripts/ha-ws.mjs` sends WebSocket commands to Home Assistant. Run it from `homeautomation-go`.
A git worktree has no `.env`, so point `--env-file` at the main checkout's file. Never print the token.

```bash
cd homeautomation-go
ENVF=.env   # in a worktree: use the main checkout's homeautomation-go/.env
ws() { node --env-file=$ENVF --experimental-websocket ../scripts/ha-ws.mjs "$1" 2>/dev/null; }
```

Steps 3 and later change the live system. Ask the user before you do them, unless they already asked
for the change.

## Step 1: Confirm the sensor is paired

A sensor that is not paired sends nothing. Wetting its leads or opening its door does nothing at all.

- Look for the device in the device registry (Step 2). Check its `created_at` and its `last_seen` entity.
- Do not assume an old device with a similar name is the new one. A device whose `sensor.<x>_last_seen`
  is months old is not the sensor the user is testing.
- If the device is missing, the user must pair it: Home Assistant → Z-Wave JS → Add device, then the
  sensor's inclusion action. For Zooz ZSE41/ZSE42 sensors, press the button 3 times quickly.

## Step 2: Read the device and its entities

```bash
ws '[{"type":"config/device_registry/list"},{"type":"config/entity_registry/list"},{"type":"get_states"}]' \
  | python3 -I -c 'import sys,json
devs,ents,states=[r["result"] for r in json.load(sys.stdin)]
st={s["entity_id"]:s for s in states}
q=sys.argv[1].lower()
for d in devs:
  n=d.get("name_by_user") or d.get("name") or ""
  if q in n.lower() or q==d.get("model","").lower():
    print("DEVICE",d["id"],d.get("model"),"|",n,"| area",d.get("area_id"),"| labels",d.get("labels"))
    for e in ents:
      if e.get("device_id")==d["id"]:
        s=st.get(e["entity_id"],{})
        print("  ",e["entity_id"],"| name",e.get("name"),"| disabled",e.get("disabled_by"),
              "|",s.get("state"),"|",s.get("attributes",{}).get("device_class"))
' "example sink"
```

Pass part of the device name, or a model such as `ZSE42`.

## Step 3: Name the device and its entities

**Device name:** `<Place> <Kind> Sensor`, for example `Example Sink Water Leak Sensor` or
`Example Door Open`. Set it as `name_by_user`.

**Entity IDs:** `<domain>.<place>_<kind>_sensor_<entity>`, for example
`binary_sensor.example_sink_water_leak_sensor_water_leak_detected`. Home Assistant often builds IDs from
the area plus the name, which doubles words such as `utility_utility_sink_`. Rename those.

**Entity names:** leave them empty (`name: null`). Home Assistant then shows the device name followed by
the entity's own name, once. A custom entity name that already contains the device name makes the
friendly name repeat it. Alerts and the dashboard show that friendly name.

**Area:** set `area_id` to the room the sensor is in.

This command renames the device and rewrites entity IDs from an old prefix to a new one:

```bash
DEV=<device id>; OLD=<old prefix_>; NEW=example_sink_water_leak_sensor_; NAME="Example Sink Water Leak Sensor"
CMDS=$(ws '[{"type":"config/entity_registry/list"}]' | python3 -I -c 'import sys,json
dev,old,new,name=sys.argv[1:5]
ents=json.load(sys.stdin)[0]["result"]
cmds=[{"type":"config/device_registry/update","device_id":dev,"name_by_user":name}]
for e in ents:
  if e.get("device_id")==dev:
    dom,obj=e["entity_id"].split(".",1)
    c={"type":"config/entity_registry/update","entity_id":e["entity_id"],"name":None}
    if obj.startswith(old): c["new_entity_id"]=dom+"."+new+obj.removeprefix(old)
    cmds.append(c)
print(json.dumps(cmds))' "$DEV" "$OLD" "$NEW" "$NAME")
ws "$CMDS" | python3 -I -c 'import sys,json; r=json.load(sys.stdin); print(all(x.get("success") for x in r))'
```

Add `"area_id":"<area>"` to the device update if the area is wrong.

Before you rename entity IDs, search the repo and the private downstream repo for the old IDs. A renamed
entity breaks any config that lists it. `configs/sensor_config.yaml` lists entity IDs, for example.

## Step 4: Check the label

The `monitoring_ignore` label turns off all health monitoring for a device. For a water-leak sensor it
turns off **leak detection itself**. A new sensor has no label. Check that an existing one does not
carry it. To remove it, follow the `monitoring-coverage` skill. That skill keeps the device's other labels.

## Step 5: Set the battery report threshold

Our practice: battery-powered Z-Wave sensors report battery at every **5%** change. The device default is
often 10%. At 10% the sensor reports so rarely that it looks stale, and `sensorhealth` raises false
"offline" alerts.

1. Find the entity `number.<prefix>battery_report_threshold` in Step 2.
2. If it is disabled (`disabled_by: integration`), enable it. Home Assistant reloads the integration
   after about 30 seconds. The entity reads `unavailable` until then.

   ```bash
   ws '[{"type":"config/entity_registry/update","entity_id":"number.example_sink_water_leak_sensor_battery_report_threshold","disabled_by":null}]'
   ```

3. Set the value:

   ```bash
   ws '[{"type":"call_service","domain":"number","service":"set_value","target":{"entity_id":"number.example_sink_water_leak_sensor_battery_report_threshold"},"service_data":{"value":5}}]'
   ```

4. Read the state back. **A sleeping sensor keeps its old value** until it wakes up. Z-Wave JS holds the
   new setting until then. A leak or door report does not apply it. Ask the user to press the sensor's
   button once to wake it, then read the state again. Otherwise it applies at the next scheduled wake-up.
5. Compare with the other sensors of the same kind:

   ```bash
   ws '[{"type":"get_states"}]' | python3 -I -c 'import sys,json
   for s in json.load(sys.stdin)[0]["result"]:
     if "battery_report_threshold" in s["entity_id"]: print(s["entity_id"], s["state"])'
   ```

The `sensorconfig` plugin also sets thresholds at startup, for the entity IDs listed in
`configs/sensor_config.yaml`. That file is public. Do not add a new sensor's entity ID to it, because
the list reveals the device inventory. Set the value in Home Assistant, as above.

## Step 6: Restart home-automation

Plugins find sensors only at startup. A new or renamed sensor is not watched until the app restarts.
No config is needed: `environmental` finds leak sensors by `device_class: moisture`.

Ask the user to restart, or deploy. Then confirm the restart really happened. Look for a new startup line:

```bash
../scripts/loki-query.sh '{service="home-automation"} |= "Home Automation starting"' 30m
```

If no new line appears, the restart reached a different service.

## Step 7: Verify the app watches the sensor

**These commands are specific to moisture (water leak) sensors.** Door and temperature sensors are
handled by a downstream private plugin; verification there looks different — check that plugin's shadow
state and logs instead of the commands below.

```bash
../scripts/loki-query.sh '{service="home-automation"} |~ "(?i)leak sensors discovered|monitoring_ignore label"' 30m
curl -s https://home-automation.featherback-mermaid.ts.net/api/shadow \
  | python3 -I -c 'import sys,json
for s in json.load(sys.stdin)["plugins"]["environmental"]["outputs"]["waterLeakSensors"]: print(s["entityId"], s["state"])'
```

- The sensor's moisture entity must be in `waterLeakSensors`.
- It must not appear in a `"Skipping ... monitoring_ignore label"` line.
- `"No water leak sensors discovered"` means the app watches no leak sensor at all.

## Step 8: Test end to end

**These log strings are specific to moisture (water leak) sensors.** For other sensor types, adapt the
log search to match the downstream plugin's output.

Ask the user to trigger the sensor: wet a leak sensor's leads. Then check:

```bash
../scripts/loki-query.sh '{service="home-assistant"} |= "<moisture entity id>"' 15m
../scripts/loki-query.sh '{service="home-automation"} |~ "(?i)leak detected|Announcement sent"' 15m
```

- Home Assistant must log the state change `off -> on`.
- home-automation must log `"Water leak detected"` and `"Announcement sent"`.
- The user should hear the announcement and get the push notification.

If Home Assistant logs nothing, go back to Step 1. If Home Assistant logs the change but the app does
not, go back to Steps 4, 6 and 7.

## Rules

- **Read before you write.** Steps 1, 2 and 7 only read.
- **Ask before you change the live system**, unless the user asked for that change.
- **Never print the token.**
- **Keep real names out of the repo.** They go into Home Assistant only.
- **Mention the private downstream repo** when you rename entity IDs. It may read them too.
