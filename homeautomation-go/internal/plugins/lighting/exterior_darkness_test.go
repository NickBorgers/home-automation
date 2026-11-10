package lighting

import (
	"context"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// exteriorGroups are the production Hue groups outside the house.
// They must only turn on when the shared darkness state says it is dark.
var exteriorGroups = []string{"Front of House", "Decorative Outdoor Lights"}

// TestProductionExteriorGroups_RequireDarkness checks the production config.
// An owner arrival, guests, or a front-of-house person must not turn the
// exterior groups on in daylight. The same triggers must still work when dark.
func TestProductionExteriorGroups_RequireDarkness(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)

	triggers := []string{"didOwnerJustReturnHome", "isHaveGuests", "isFrontOfHousePersonPresent"}

	for _, group := range exteriorGroups {
		room := findRoom(cfg, group)
		require.NotNil(t, room, "group %q missing from production config", group)

		for _, trigger := range triggers {
			for _, dark := range []bool{false, true} {
				name := group + "/" + trigger
				if dark {
					name += "/dark"
				} else {
					name += "/daylight"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					stateManager := state.NewManager(ha.NewMockClient(), zap.NewNop(), false)
					values := map[string]bool{
						"isDarkOutside":               dark,
						"isFrontOfHousePersonPresent": false,
						"didOwnerJustReturnHome":      false,
						"isHaveGuests":                false,
						"isAnyoneHomeAndAwake":        true,
					}
					values[trigger] = true
					for k, v := range values {
						require.NoError(t, stateManager.SetBool(k, v))
					}

					m := NewManager(context.Background(), ha.NewMockClient(), stateManager, cfg, zap.NewNop(), false, nil)
					action, matched := m.evaluateConditions(room)

					if dark {
						assert.Equal(t, "on", action, "dark: %s should turn %s on", trigger, group)
						assert.Equal(t, trigger, matched)
					} else {
						assert.Equal(t, "off", action, "daylight: %s must not turn %s on", trigger, group)
						assert.Equal(t, "isDarkOutside", matched)
					}
				})
			}
		}
	}
}
