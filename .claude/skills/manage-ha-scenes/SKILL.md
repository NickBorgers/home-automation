---
name: manage-ha-scenes
description: Inspect, rename, and re-name Home Assistant scenes safely, so the lighting plugin finds the right scene entity IDs. Use when a scene fires the wrong lights, two scenes collide on a name (for example a `_2` suffix), or scenes need new entity IDs or display names.
---

# Manage Home Assistant Scenes

The lighting plugin does not look scenes up. It builds the entity ID from the room and the day phase:

```
scene.{snake_case(hue_group + " " + dayPhase)}      # Kitchen + morning -> scene.kitchen_morning
```

(`activateScene` in `homeautomation-go/internal/plugins/lighting/manager.go`.) A room can also list
`companion_scenes` in `configs/hue_config.yaml`. Each entry builds a second ID the same way and runs
right after the main scene. Home Assistant cannot nest scenes and Hue scenes cannot be edited there,
so a companion is how one phase activates two scenes.

Scene names must match these IDs exactly. A mismatch fails quietly: the wrong scene runs, or none does.

## Rules

- **Read before you write.** Every rename changes the live system. Show the user the plan first.
- **Write only what the user asked for.** Renames are reversible. Deletes are not. Do not delete scenes.
- **Never print the token.** Check lengths only.
- **Keep the repo in step.** After a rename, update `configs/hue_config.yaml` and any test that names the old ID.

## Helper

`scripts/ha-ws.mjs` sends WebSocket commands to Home Assistant. The REST API cannot rename entities.
Run it from `homeautomation-go` so `.env` loads. Node 20 needs the flag.

```bash
cd homeautomation-go && set -a && . ./.env && set +a
node --experimental-websocket ../scripts/ha-ws.mjs '[{"type":"config/entity_registry/list"}]'
```

Pipe the output through `python3 -I -c` to filter it. Replies come back in the order you sent them.

## Step 1: Find out what each scene is

The registry `platform` tells you the owner. `homeassistant` is a native scene. `hue` is a Hue scene.

```bash
# list scene entries: id, platform, name
node --experimental-websocket ../scripts/ha-ws.mjs '[{"type":"config/entity_registry/list"}]' \
  | python3 -I -c 'import sys,json
for e in sorted(json.load(sys.stdin)[0]["result"], key=lambda e: e["entity_id"]):
    if e["entity_id"].startswith("scene.kitchen"): print(e["entity_id"], e["platform"], e.get("name") or e.get("original_name"))'
```

To see which lights a native scene sets, read its state. Hue scenes show `group_name` instead.

```bash
BASE=$(printf '%s' "$HA_URL" | sed -E 's#^wss://#https://#; s#/api/websocket/?$##; s#/+$##')
curl -s -H "Authorization: Bearer $HA_TOKEN" "$BASE/api/states/scene.kitchen_morning" | jq '.attributes'
```

Why two scenes share a name: Home Assistant adds `_2` to the second one. The native scene usually
took the plain name first, so the Hue scene became `<name>_2` and the plugin never reaches it.

## Step 2: Check what depends on a scene

```bash
node --experimental-websocket ../scripts/ha-ws.mjs \
  '[{"type":"search/related","item_type":"entity","item_id":"scene.kitchen_morning"}]'
```

An empty `result` means nothing in Home Assistant uses it. Home Assistant does **not** rewrite
references when you rename an entity, so any automation, script, or group listed here will break.

This search cannot see YAML dashboards, HomeKit, or the private downstream repo. Tell the user so.

## Step 3: Rename entity IDs

Free the target name before you take it. Rename the scene that holds the name first.

```bash
node --experimental-websocket ../scripts/ha-ws.mjs '[
  {"type":"config/entity_registry/update","entity_id":"scene.kitchen_morning","new_entity_id":"scene.kitchen_under_cabinet_morning"},
  {"type":"config/entity_registry/update","entity_id":"scene.kitchen_morning_2","new_entity_id":"scene.kitchen_morning"}
]'
```

Check that every reply has `"success": true`. A target that already exists fails with an error. Stop and look.

## Step 4: Match the display names

A registry `name` overrides the display name. The scene's own `name:` in `scenes.yaml` stays the same.

```bash
node --experimental-websocket ../scripts/ha-ws.mjs \
  '[{"type":"config/entity_registry/update","entity_id":"scene.kitchen_under_cabinet_morning","name":"Kitchen Under Cabinet Morning"}]'
```

Hue scenes already show `<Room> <Scene>`, because Hue adds the room name. They usually need no change.
Rewriting `scenes.yaml` is a bigger change. Do it only when the user asks.

## Step 5: Verify

Read the states back. Confirm each ID, its `friendly_name`, and its lights or `group_name`.

```bash
curl -s -H "Authorization: Bearer $HA_TOKEN" "$BASE/api/states" \
  | jq -r '.[] | select(.entity_id | startswith("scene.kitchen")) | [.entity_id, .attributes.friendly_name] | @tsv'
```

Then confirm the plugin chose the scene you expect:

```bash
scripts/loki-query.sh '{service="home-automation"} |= "Activating scene"' 1h
```

## Undo

Run the same `config/entity_registry/update` commands with the old and new IDs swapped. Undo in
reverse order, so each name is free before you take it. Set `"name": null` to remove a display override.

## Gotchas

- A new entity ID briefly carries the old state timestamp. This is cosmetic.
- Deploy order matters. If the plugin config names a companion scene that does not exist yet, the plugin
  logs a warning for it and carries on. If you rename a scene before the plugin change ships, the plugin
  runs only the scene that now holds the plain name.
- `HA_URL` in `homeautomation-go/.env` is an `https://` base. The helper converts it to `wss://`.
- `READ_ONLY=true` stops the plugin from sending commands. It does not limit this helper. The token has full access.
