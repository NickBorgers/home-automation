# Incident Report: Sleep Detection False Positive During Departure

**Date:** 2026-05-17
**Duration:** Same day, until the bedroom door opened and wake detection cleared sleep state
**Severity:** Medium (incorrect sleep-mode automation while nobody was home)
**Resolution:** PR #1119 fixes `isAnyoneHomeAndAwake` to derive from debounced `isAnyoneHome` instead of undebounced `isAnyOwnerHome`

## Summary

The house incorrectly entered sleep mode while the owners were leaving, not going to bed. The lighting plugin turned off `light.primary_suite` because `isAnyoneHomeAndAwake` became false. One minute later the state-tracking sleep timer fired, saw `isAnyoneHome=true`, and marked `isMasterAsleep=true`.

The root mismatch: `isAnyoneHomeAndAwake` derived from the raw (undebounced) `isAnyOwnerHome`, so it flipped false the instant the last owner left. But `detectMasterAsleep()` still checked `isAnyoneHome`, which carries a 5-minute departure debounce — so lighting reacted to departure immediately while sleep detection still believed someone was home for another five minutes.

## Timeline

| Time | Event |
|------|-------|
| T+0 | Last owner left. `isAnyOwnerHome` became false immediately. |
| T+0 | `isAnyoneHomeAndAwake` became false, so lighting automation turned off `light.primary_suite`. |
| T+0 | State tracking started the 1-minute sleep detection timer because primary suite lights were off. |
| T+1m | `detectMasterAsleep()` ran while `isAnyoneHome` was still true inside its 5-minute departure debounce window. |
| T+1m | Sleep detection treated the departure-driven lights-off event as bedtime and set `isMasterAsleep=true`. |
| Later | Bedroom door wake detection cleared `isMasterAsleep`. |

## Root Cause

`isAnyoneHomeAndAwake` was derived from `isAnyOwnerHome` (undebounced). This meant that lighting consumers reacted immediately to departure — turning off `light.primary_suite` the instant the last owner left.

`detectMasterAsleep()`, however, read `isAnyoneHome` (debounced, 5-minute window). So for up to 5 minutes after departure, lighting saw "everyone gone" while sleep detection still saw "someone home". When lights went off in that window, sleep detection interpreted it as bedtime rather than departure.

The fix is to change `isAnyoneHomeAndAwake` to derive from the debounced `isAnyoneHome` instead of `isAnyOwnerHome`, so lighting and sleep detection both see the same departure signal and can no longer race.

## Impact

- `isMasterAsleep` was set to true while no owner was home.
- Sleep-mode automations could run incorrectly, including lockdown behavior and sleep music selection.
- The system stayed in sleep state until a later bedroom-door event triggered wake detection.

## Resolution

PR #1119 fixes `isAnyoneHomeAndAwake` in `computed_providers.go` to derive from `isAnyoneHome` (debounced) instead of `isAnyOwnerHome` (undebounced). This propagates the 5-minute departure debounce to all downstream consumers — lighting, sleep detection, and lockdown alike — so the lights-off sweep and `detectMasterAsleep()` no longer race. A regression test was added covering the cascade ordering: when raw `isAnyOwnerHome` flips false, `isAnyoneHomeAndAwake` stays true until debounced `isAnyoneHome` follows.

## Lessons Learned

1. **Consumers sharing the same logical event should read the same upstream signal.** `isAnyoneHomeAndAwake` derived from undebounced `isAnyOwnerHome` while `detectMasterAsleep()` read debounced `isAnyoneHome` — two consumers of the same "owner left" event on different debounce timelines. When the correct debounce level for GPS/WiFi presence bounce is `isAnyoneHome`, all consumers should derive from it.
2. **Cross-plugin timing matters.** Lighting, computed presence, and sleep detection each behaved as designed locally, but their one-minute and five-minute timing windows combined into the incident.
3. **Regression tests should model the timeline.** The important state was not just "nobody home"; it was `isAnyOwnerHome=false` while `isAnyoneHome=true` during the departure debounce window.

## Action Items

- [x] PR #1119: Fix `isAnyoneHomeAndAwake` to derive from debounced `isAnyoneHome` instead of `isAnyOwnerHome`
- [x] PR #1119: Add regression coverage for the departure-debounce false positive
- [x] PR #1122: Add this incident writeup
