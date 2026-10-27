---
name: monitoring-coverage
description: Check that a sensor used by plugin code is not hidden by the Home Assistant `monitoring_ignore` label. Use when you add or change code that reads a sensor, binary_sensor, or Z-Wave node, when a device starts to drive an automation, or when you finish the move-in alert cleanup and need to turn monitoring back on.
---

# Monitoring Coverage

The label `monitoring_ignore` on a Home Assistant **device** turns off all health monitoring for that
device. We apply it in bulk to quiet announcements when sensors are offline, boxed, or broken.
The side effect is silent failure. A device with the label can die and nothing says so.

When code starts to depend on a device, that device must not carry the label.

## What the label silences

| Monitor | Plugin | What stops |
|---------|--------|------------|
| Battery | `sensorhealth` | "Low battery" alerts |
| Node status | `sensorhealth` | "Device offline" and "device online" alerts, and the startup dead-device check |
| Temperature | `sensorhealth` | "Locked up" and "recovered" alerts |
| Water leak | `environmental` | **Leak detection itself**, not only a warning about the sensor |

The label does **not** stop humidity monitoring. `environmental` does not read it for humidity sensors.

The code lives in `homeautomation-go/internal/ha/labels.go`. Each plugin reads the label **once at
startup**. A label change takes effect only after the app restarts.

## Helper

`scripts/ha-ws.mjs` sends WebSocket commands to Home Assistant. Run it from `homeautomation-go`.
A git worktree has no `.env`, so point `--env-file` at the main checkout's file.

```bash
cd homeautomation-go
ENVF=.env   # in a worktree: /home/nborgers/code/home-automation/homeautomation-go/.env
node --env-file=$ENVF --experimental-websocket ../scripts/ha-ws.mjs '[{"type":"config/label_registry/list"}]'
```

Never print the token.

## Step 1: Check the devices your code reads

List every entity ID the new or changed code reads. Then look up the device behind each one.

```bash
node --env-file=$ENVF --experimental-websocket ../scripts/ha-ws.mjs \
  '[{"type":"config/entity_registry/list"},{"type":"config/device_registry/list"}]' \
  | python3 -I -c 'import sys,json
ents,devs=[r["result"] for r in json.load(sys.stdin)]
dev={d["id"]:d for d in devs}
for e in ents:
    if e["entity_id"] in set(sys.argv[1:]):
        d=dev.get(e.get("device_id"),{})
        lb=d.get("labels") or []
        print(e["entity_id"],"|",d.get("name_by_user") or d.get("name"),"|",lb,"|","IGNORED" if "monitoring_ignore" in lb else "ok")
' sensor.example_one binary_sensor.example_two
```

An entity with no device (a helper, a template sensor) has no label. It is never ignored.

## Step 2: Turn monitoring back on

If a device shows `IGNORED`, tell the user which device and why the code needs it.
**Ask before you change the live system.** Then remove only `monitoring_ignore`.

`config/device_registry/update` **replaces** the whole label list. Send the other labels back too.
Labels such as `indoor` and `unconditioned` control humidity alerts. Dropping them breaks those alerts.

```bash
node --env-file=$ENVF --experimental-websocket ../scripts/ha-ws.mjs \
  '[{"type":"config/device_registry/update","device_id":"<device id>","labels":["indoor"]}]'  # Replace ["indoor"] with the Step 1 labels, minus monitoring_ignore
```

The reply must say `"success": true`.

## Step 3: Restart and verify

Deploy or restart the app. Then confirm the plugin no longer skips the device.

```bash
../scripts/loki-query.sh '{service="home-automation"} |= "monitoring_ignore label"' 30m
```

The device's entity ID must **not** appear in that output. The plugin logs one line per skipped entity.

If the device was offline while ignored, expect announcements at the restart. The startup check
reports every dead device. Tell the user before you restart.

## List what is ignored now

```bash
node --env-file=$ENVF --experimental-websocket ../scripts/ha-ws.mjs '[{"type":"config/device_registry/list"}]' \
  | python3 -I -c 'import sys,json
for d in json.load(sys.stdin)[0]["result"]:
    if "monitoring_ignore" in (d.get("labels") or []): print(d.get("name_by_user") or d.get("name"))'
```

## Rules

- **Read before you write.** Step 1 is read-only. Steps 2 and later change the live system.
- **Never add the label to a device that a plugin reads, or to a water-leak sensor,** unless the user says so.
- **Never print the token.** Check lengths only.
- **Mention the private downstream repo.** Plugins there may read devices this repo does not. This check cannot see them.
