package lighting

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inFlightReportClient answers a light service call the way some lights do: the light reports
// its new level to the plugin while the service call is still in flight.
type inFlightReportClient struct {
	ha.HAClient
	report func(data map[string]interface{})
}

func (c *inFlightReportClient) CallService(ctx context.Context, domain, service string, data map[string]interface{}) error {
	if domain == "light" && service == "turn_on" {
		c.report(data)
	}
	return c.HAClient.CallService(ctx, domain, service, data)
}

func numberData(v interface{}) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

// A report of the new level that arrives before the service call returns is the plugin's own
// change. It must not hold the room, and must not start a hold check.
func TestLux_ReportDuringServiceCallIsNotManual(t *testing.T) {
	t.Parallel()
	for _, want := range followerRooms {
		t.Run(want.room, func(t *testing.T) {
			m, env, clk := newProductionRoomFixture(t)
			room := findRoom(m.config, want.room)
			m.haClient = &inFlightReportClient{HAClient: m.haClient, report: func(data map[string]interface{}) {
				entity, _ := data["entity_id"].(string)
				attrs := map[string]interface{}{"brightness": numberData(data["brightness_pct"]) / 100 * 255}
				switch entity {
				case want.strip:
					m.handleFollowerChange(room, &ha.State{EntityID: entity, State: "on", Attributes: attrs})
				case want.cans:
					if k, ok := data["color_temp_kelvin"]; ok {
						attrs["color_temp_kelvin"] = numberData(k)
					}
					m.handleCansChange(room, &ha.State{EntityID: entity, State: "on", Attributes: attrs})
				}
			}}

			m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
			require.NotEmpty(t, callsTo(env.MockHA, "turn_on", want.strip))

			// A new level after the first fade: the strip and the cans both get a new command,
			// and each reports its new level before the call returns.
			env.MockHA.ClearServiceCalls()
			clk.Advance(2 * time.Minute)
			clk.Set(at(20, 0))
			env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
			m.luxTick()
			require.NotEmpty(t, callsTo(env.MockHA, "turn_on", want.strip), "the strip got a new command")
			require.NotEmpty(t, callsTo(env.MockHA, "turn_on", want.cans), "the cans got a new command")

			assert.False(t, m.luxHeld(room), "the plugin's own level is not a manual change")
			m.luxMu.Lock()
			checking := m.luxRoom(room.HueGroup).checking
			m.luxMu.Unlock()
			assert.Nil(t, checking, "no hold check for the plugin's own level")
		})
	}
}

// A follower command that fails to send leaves the previous command in place, so later reports
// are compared with what the light was actually told.
func TestLuxFollowers_FailedSendKeepsThePreviousCommand(t *testing.T) {
	t.Parallel()
	m, env, clk := newProductionRoomFixture(t)
	room := findRoom(m.config, "Pantry")
	strip := "light.pantry_under_cabinet"
	m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
	m.luxMu.Lock()
	before := m.luxRoom(room.HueGroup).followers[strip]
	m.luxMu.Unlock()
	require.NotNil(t, before)

	env.MockHA.SetServiceError("light", "turn_on", assert.AnError)
	clk.Advance(2 * time.Minute)
	clk.Set(at(20, 0))
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
	m.luxTick()

	m.luxMu.Lock()
	after := m.luxRoom(room.HueGroup).followers[strip]
	m.luxMu.Unlock()
	assert.Same(t, before, after, "a failed send does not replace the recorded command")
}
