# Incident Report: Living Room Lights Stuck at Minimum Brightness

**Date:** 2026-03-07
**Duration:** ~2 hours (approx 02:00 - 04:25 UTC)
**Severity:** Low (cosmetic/comfort impact only)
**Resolution:** Hue bridge reboot

## Summary

After deploying PR #782 (sync box recovery force-notify) and PR #788 (TV entity fix + Bravia reload fix), the living room lights became stuck at minimum brightness and could not be corrected via HA scenes, Hue app scenes, or manual brightness adjustments. The root cause was a stuck state on the Hue bridge, likely caused by rapid entertainment area activation/deactivation cycles from the Hue Sync Box.

## Timeline (all times UTC)

| Time | Event |
|------|-------|
| 00:58 | PR #782 merged and deployed (sync box recovery force-notify) |
| 01:47 | Owner arrives home, `isAnyoneHome=true`, dusk scenes activated |
| 01:54 | `scene.living_room_dusk` applied successfully |
| 02:08-02:38 | Go app crash-loops every ~7 min due to WebSocket disconnects to HA. Each restart sees `isTVPlaying=false` (unchanged), so lighting plugin is never re-triggered. DayPhase timer cannot advance. |
| 03:50 | PR #788 deployed (fixes `TVRemoteEntity` to `media_player.sony_xr_65a80k`, switches Bravia reload to REST API) |
| 03:53 | Sync box turns on, `isTVon=true`. Bravia staleness detected. Bravia reload attempted via REST API. |
| 03:56:36 | `isTVPlaying=true` - Hue light sync turned on. Lights synced to TV content. |
| 03:56:42 | `isTVPlaying=false` (AppleTV paused after 6 seconds). Lighting plugin applied `scene.living_room_dusk`. |
| 04:04:58 | DayPhase transitions dusk -> winddown. `scene.living_room_winddown` applied. |
| 04:11:35-39 | `binary_sensor.tv_with_tubes` (Hue entertainment area) rapidly toggles on/off. Lights go dim. |
| 04:12-04:22 | Multiple attempts to fix via Hue app scenes, HA UI brightness changes - all revert to minimum within seconds. |
| 04:13 | Sync box powered off via Z-Wave plug. Go app detects unavailable, initiates recovery power cycle. |
| 04:14:39 | Sync box recovered, `scene.living_room_winddown` re-applied. Lights still dim. |
| ~04:22 | Hue bridge rebooted. Lights remain at brightness 0 (bulbs retained state). |
| ~04:23 | After bridge reboot, Hue scene applied via Hue app - brightness holds correctly. Issue resolved. |

## Root Cause

The Hue bridge had a stuck internal state, likely caused by the entertainment area "TV with Tubes" rapidly activating and deactivating (observed at 04:11:35-39). The entertainment area streaming protocol (UDP-based, outside normal Hue API) set all living room bulbs to brightness 0. After the streaming session ended, the bridge continued to enforce this brightness level, overriding all scene activations from both HA and the Hue app.

### Evidence

- HA logbook showed no commands being sent to the individual light entities after the scene activations
- `light.living_cans` and all 4 individual downlights reported `brightness: 0, state: on` with `context.user_id: null` (updates from Hue bridge polling, not HA commands)
- The Hue Sync Box was physically powered off, ruling it out as the active cause
- Hue app showed the entertainment area as "ready" (not active), yet lights remained dim
- Setting brightness via Hue app, HA UI, or HA scenes all reverted within seconds
- Only a Hue bridge reboot resolved the issue

## Bugs Found and Fixed (PR #788)

During investigation, two bugs were discovered that prevented `isTVPlaying` from ever becoming `true`:

1. **Wrong TV entity**: `TVRemoteEntity` was `remote.big_beautiful_oled` (an AppleTV integration entity) instead of `media_player.sony_xr_65a80k` (the actual Sony TV). Since the code subscribed to a non-existent entity, `tvRemoteOn` was always `false`, causing `calculateTVPlaying()` to always bail out.

2. **Broken Bravia reload**: The WebSocket command `config_entries/reload` returned `unknown_command`. Fixed to use the REST API (`POST /api/config/config_entries/entry/{id}/reload`).

3. **Media player state handling**: Updated on/off detection from `== "on"` to handle media_player states (`playing`, `paused`, `idle` = on; `off`, `standby`, `unavailable`, `unknown` = off).

## Additional Issue: WebSocket Crash Loop

The Go app experienced WebSocket disconnections every ~7 minutes (`read tcp ... use of closed network connection`), causing repeated restarts. This prevented:
- The dayPhase timer from advancing (dusk stayed for over an hour)
- Lighting scenes from being re-applied (state values unchanged on restart = no notifications)

This is a separate networking issue (possibly Tailscale-related) and was not addressed in this incident.

## Lessons Learned

1. **Hue entertainment area can corrupt bridge state**: Rapid activation/deactivation of the entertainment area can leave the bridge in a state where it actively overrides all brightness commands. Only a bridge reboot resolves this.

2. **Entity names can drift**: The Sony TV entity was renamed from `remote.big_beautiful_oled` to `media_player.sony_xr_65a80k` at some point, silently breaking TV detection. Entity IDs should be validated against HA periodically.

3. **HA syslog doesn't capture brightness changes**: The Gravwell `home-assistant` tag only logs on/off state transitions, not attribute-only changes like brightness. This made diagnosis difficult. Consider logging brightness changes for key entities.

4. **Reactive-only lighting has no self-healing**: The lighting plugin only applies scenes in response to state changes. If all states are unchanged (e.g., after app restart), lights are never corrected. Consider adding periodic scene re-application or a manual trigger.

## Action Items

- [x] PR #788: Fix TV entity name and Bravia reload API
- [ ] Investigate WebSocket disconnect pattern (separate issue)
- [ ] Consider adding a "re-apply current scenes" API endpoint or periodic re-application
- [ ] Consider monitoring entertainment area state (`binary_sensor.tv_with_tubes`) and alerting on rapid toggling
