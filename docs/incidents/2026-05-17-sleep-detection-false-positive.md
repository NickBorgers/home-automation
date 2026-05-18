# Incident Report: Sleep Detection False Positive During Departure

**Date:** 2026-05-17
**Duration:** Same day, until the bedroom door opened and wake detection cleared sleep state
**Severity:** Medium (incorrect sleep-mode automation while nobody was home)
**Resolution:** PR #1119 replaces the sleep-detection presence guard with `isAnyOwnerHome`

## Summary

The house incorrectly entered sleep mode while the owners were leaving, not going to bed. The lighting plugin turned off `light.primary_suite` because `isAnyoneHomeAndAwake` became false. One minute later the state-tracking sleep timer fired, saw `isAnyoneHome=true`, and marked `isMasterAsleep=true`.

That `isAnyoneHome=true` value was stale by design: `isAnyoneHome` has a 5-minute departure debounce to avoid presence sensor false negatives. Sleep detection needed the immediate owner-presence signal, `isAnyOwnerHome`, instead.

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

`detectMasterAsleep()` guarded sleep detection with `isAnyoneHome`. That signal intentionally remains true for 5 minutes after departure, so it is not suitable for deciding whether a primary-suite lights-off event means an owner is still home.

The correct guard is `isAnyOwnerHome`, which tracks owner presence directly and has no departure debounce. It also matches the owner-presence input already used by `isAnyoneHomeAndAwake`, the computed state that triggered the lighting change.

## Impact

- `isMasterAsleep` was set to true while no owner was home.
- Sleep-mode automations could run incorrectly, including lockdown behavior and sleep music selection.
- The system stayed in sleep state until a later bedroom-door event triggered wake detection.

## Resolution

PR #1119 changes `detectMasterAsleep()` to check `isAnyOwnerHome` instead of `isAnyoneHome`. It also adds a regression test for the departure-debounce timeline: owner gone, `isAnyoneHome` still true, primary suite lights off, and sleep detection must not set `isMasterAsleep=true`.

## Lessons Learned

1. **Debounced derived state is not always a safe guard.** `isAnyoneHome` is useful for avoiding short presence drops, but that same debounce can be wrong for timer callbacks that need immediate departure truth.
2. **Cross-plugin timing matters.** Lighting, computed presence, and sleep detection each behaved as designed locally, but their one-minute and five-minute timing windows combined into the incident.
3. **Regression tests should model the timeline.** The important state was not just "nobody home"; it was `isAnyOwnerHome=false` while `isAnyoneHome=true` during the departure debounce window.

## Action Items

- [x] PR #1119: Guard sleep detection with `isAnyOwnerHome`
- [x] PR #1119: Add regression coverage for the departure-debounce false positive
- [x] PR #1121: Add this incident writeup
