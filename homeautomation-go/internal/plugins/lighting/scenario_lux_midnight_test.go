package lighting

import (
	"context"
	"testing"
	"time"

	dayphaselib "homeautomation/internal/dayphase"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// User story: "When midnight passes, my lights do not suddenly come up."
//
// Found in production on 2026-10-31 at 00:00 CDT: the Living Room and Kitchen cans went to
// 100% and both chandeliers turned on, because the sun times were a day stale.
//
// INVARIANT: the lux target never rises between 22:00 and sunrise. Nothing but a person
// raises the lights at night.
//
// This scenario lives beside the lighting package (not test/integration) because it drives the
// unexported lux tick and the real sun-anchor resolver on a fake clock.
func TestScenario_UserStory_MidnightDoesNotBrightenTheLights(t *testing.T) {
	t.Parallel()
	tz := mustChicago(t)

	// GIVEN: it is 23:59 on Oct 30, Living Room and Kitchen lux control is active (production
	// config), someone is home and awake, it is dark outside, and the chandeliers are off.
	t.Log("GIVEN: 23:59 CDT, production lux rooms active, chandeliers off, dark outside")
	site := newMidnightSite(t, time.Date(2026, 10, 30, 23, 59, 0, 0, tz))
	env := testutil.NewEnv(t)
	m := NewManager(context.Background(), env.MockHA, env.StateMgr, site.cfg, env.Logger, false, nil)
	m.luxOutdoor = &smoother{window: luxDefaultSmoothing}
	m.SetClock(site.clk)
	m.SetAnchorResolver(site.anchors.resolve)
	env.MockHA.SetState(site.cfg.LuxCurve.OutdoorSensor, "0", nil)
	require.NoError(t, env.StateMgr.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, env.StateMgr.SetBool("isKitchenOccupied", true))
	require.NoError(t, env.StateMgr.SetBool("isTVPlaying", false))
	require.NoError(t, env.StateMgr.SetBool("isHaveGuests", false))

	// The day phase plugin's own calculator last refreshed before midnight, so its cache holds Oct 30.
	phaseCalc := dayphaselib.NewCalculator(32.85486, -97.50515, tz, testlogger.New())
	phaseCalc.SetClock(site.clk)
	require.NoError(t, phaseCalc.UpdateSunTimes())

	cans := map[string]bool{}
	for _, name := range []string{"Living Room", "Kitchen"} {
		cans[findRoom(site.cfg, name).LuxControl.Cans] = true
	}
	var chandeliersOn []string
	maxPct := map[string]float64{} // highest brightness sent per entity
	record := func() {
		for _, c := range env.MockHA.GetServiceCalls() {
			if c.Domain != "light" || c.Service != "turn_on" {
				continue
			}
			entity, _ := c.Data["entity_id"].(string)
			if pct, ok := c.Data["brightness_pct"].(int); ok && float64(pct) > maxPct[entity] {
				maxPct[entity] = float64(pct)
			}
			if entity == "light.front_foyer_chandelier" || entity == "light.front_room_chandelier" {
				chandeliersOn = append(chandeliersOn, entity)
			}
		}
		env.MockHA.ClearServiceCalls()
	}

	site.clk.Set(time.Date(2026, 10, 30, 23, 59, 0, 0, tz))
	m.luxTick()
	record()
	evening := map[string]float64{}
	for e := range cans {
		evening[e] = maxPct[e]
	}
	t.Logf("evening level before midnight: %v", evening)
	assert.Equal(t, "night", string(phaseCalc.GetSunEvent()), "sun_event is night before midnight")

	// WHEN: the clock passes local midnight and lux ticks keep running (and past 01:30)
	t.Log("WHEN: clock passes 00:00 local and lux ticks run every minute until 02:30")
	for site.clk.Now().Before(time.Date(2026, 10, 31, 2, 30, 0, 0, tz)) {
		site.clk.Advance(time.Minute)
		m.luxTick()
		record()

		// THEN: sun_event stays night at every instant
		assert.Equal(t, "night", string(phaseCalc.GetSunEvent()),
			"sun_event flipped at %s", site.clk.Now().In(tz).Format("15:04:05"))
	}

	// THEN: no lux command raised the cans above their evening level, and the chandeliers stayed off
	t.Log("THEN: cans never above evening level, chandeliers off")
	for e := range cans {
		assert.LessOrEqual(t, maxPct[e], evening[e], "%s was raised after midnight", e)
	}
	assert.Empty(t, chandeliersOn, "chandeliers must stay off after midnight")
}
