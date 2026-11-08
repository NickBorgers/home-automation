# Current Behavior and Authority

Start here when diagnosing what the running system should do and which evidence to trust. This document describes
the current Go system. Documents under [`archive/`](./archive/) explain history only and are not evidence of current
behavior.

## Where Current Behavior Is Documented

Use documentation in this order:

1. [`flows/`](./flows/) describes current user-visible automation behavior and decision logic.
2. [`architecture/ARCHITECTURE.md`](./architecture/ARCHITECTURE.md) describes current components and data paths.
3. [`reference/`](./reference/) describes implementation patterns and interfaces, not the behavior of one automation.
4. [`human/VISUAL_ARCHITECTURE.md`](./human/VISUAL_ARCHITECTURE.md) provides a visual overview.
5. [`archive/`](./archive/) records Node-RED lineage and old proposals. Consult it only to understand historical intent
   when current code and documentation are insufficient.

The deployed commit and current configuration determine production behavior. If current documentation disagrees with
the implementation, verify the deployed version, inspect the current code and configuration, and correct the docs.

## Separate the Kinds of Authority

An integration can use different systems for commands, device observations, automation decisions, and persistence.
Do not treat one signal as authoritative for all four.

| Question | Primary evidence | What it establishes |
|----------|------------------|---------------------|
| What did the automation intend to do? | `/api/shadow/{plugin}` | Inputs, decisions, selected outputs, and orchestration state |
| Was a command attempted and accepted? | Application logs and the command transport's response | Command outcome, retries, and verification performed by that transport |
| What state did a device report? | The active integration's read path | Observed device state at the time of that read |
| What persistent automation state is stored? | Home Assistant helpers and the Go state cache | Cross-plugin state restored and synchronized through Home Assistant |
| What code is running? | Startup log `commit`, `branch`, `build_time`, and `dirty` fields | Whether an investigation matches the deployed build |

Shadow state explains the automation; it is not a replacement for device telemetry. Logs establish command outcomes;
they are not durable state. A Home Assistant entity is authoritative only for paths that currently use Home Assistant
for that device or state. Corroborate timestamps before comparing evidence from different paths.

## Current Integration Boundaries

| System | Command path | Device/state read path | Operational evidence |
|--------|--------------|------------------------|----------------------|
| Most Home Assistant devices and helpers | Home Assistant WebSocket service calls | Home Assistant WebSocket events/state | Plugin shadow state plus application and Home Assistant logs |
| Sonos music | SoCo-CLI HTTP API (direct UPnP) when `SOCO_CLI_URL` is set; Home Assistant fallback when it is not | SoCo-CLI for volume and playback reads when configured; startup reconciliation still reads Home Assistant | `/api/shadow/music`, application logs, and SoCo-CLI responses/logs |
| Tesla vehicle and Powerwall | Tesla Fleet API; vehicle commands are signed where required | Tesla Fleet API polling | `/api/shadow/tesla` and Tesla plugin logs |
| Frigate room presence | No device command path; derived occupancy may be written to Home Assistant outside `log_only` mode | MQTT messages delivered through Home Assistant's `mqtt/subscribe` WebSocket command | `/api/shadow/roompresence` and room-presence logs |

These exceptions are why “Home Assistant says unavailable” is not, by itself, proof that a direct command failed. For
music with SoCo-CLI configured, a successful direct playback verification and contemporaneous music shadow state are
stronger evidence about that command than a stale or unavailable Home Assistant media-player entity.

## Diagnostic Starting Point

1. Verify the deployed version from the `Home Automation starting` log entry.
2. Open the relevant document in [`flows/`](./flows/) to identify the expected behavior and active integration.
3. Read the plugin's shadow state to see its inputs, decision, and orchestration state.
4. Check application logs for the command result and any verification or retry.
5. Check the active integration's device state; use Home Assistant state only when it is the current read path or useful
   corroboration.

See [`AGENTS.md`](../AGENTS.md#production-debugging) for production endpoints and Loki query examples.
