package housemap

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/roompresence"
)

// exampleRoomPresence is the room presence config for the same example house.
func exampleRoomPresence(t *testing.T) *roompresence.Config {
	t.Helper()
	cfg, err := roompresence.LoadConfig(filepath.Join("..", "plugins", "roompresence", "testdata", "example_config.yaml"))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	return cfg
}

func TestCrossCheck_ExampleAgrees(t *testing.T) {
	assert.Empty(t, CrossCheck(exampleMap(t), exampleRoomPresence(t)))
}

func TestCrossCheck_Rules(t *testing.T) {
	tests := []struct {
		name   string
		change func(m *Map, rp *roompresence.Config)
		want   []string
	}{
		{
			name:   "room with an unknown area",
			change: func(m *Map, _ *roompresence.Config) { m.Spaces["study"].Area = "library" },
			// The study door now joins lounge and library, so its zone and the neighbors no longer match.
			want: []string{
				"areas library and lounge are joined by [lounge_study]",
				`zone "lounge_to_study" is a boundary between lounge and study, but the door joins lounge and library`,
				"room presence neighbors lounge and study are not joined",
				`area "library" is not in the room presence config`,
			},
		},
		{
			name: "zone on the wrong door",
			change: func(m *Map, _ *roompresence.Config) {
				m.Doors["hall_bedroom"].Zones["cam_hall"] = "hall_to_porch"
			},
			want: []string{`door "hall_bedroom": cam_hall zone "hall_to_porch" is a boundary between hall and porch, ` +
				"but the door joins hall and bedroom"},
		},
		{
			name: "zone written the other way round",
			change: func(m *Map, _ *roompresence.Config) {
				m.Doors["hall_bedroom"].Zones["cam_hall"] = "bedroom_to_hall"
			},
		},
		{
			name: "zone on an exterior door",
			change: func(m *Map, _ *roompresence.Config) {
				m.Doors["front"].Zones = map[string]string{"cam_hall": "hall_to_porch"}
			},
			want: []string{"but the door joins porch and a space with no area"},
		},
		{
			name: "zone that is not a boundary",
			change: func(m *Map, _ *roompresence.Config) {
				m.Doors["hall_bedroom"].Zones["cam_hall"] = "hall"
			},
			want: []string{`door "hall_bedroom": cam_hall zone "hall" is not a boundary zone in room presence`},
		},
		{
			name: "zone override",
			change: func(m *Map, rp *roompresence.Config) {
				rp.ZoneOverrides["bedroom_door"] = "hall_to_bedroom"
				m.Doors["hall_bedroom"].Zones["cam_hall"] = "bedroom_door"
			},
		},
		{
			name: "door with neither neighbors nor a zone",
			change: func(_ *Map, rp *roompresence.Config) {
				rp.Areas["bedroom"].Neighbors = nil
				rp.Areas["porch"].Neighbors = nil
				rp.Areas["study"].Neighbors = nil
				rp.Areas["lounge"].Neighbors = nil
			},
			want: []string{"areas kitchen and lounge are joined by [lounge_kitchen], but room presence has neither"},
		},
		{
			name: "neighbors with no door",
			change: func(_ *Map, rp *roompresence.Config) {
				rp.Areas["study"].Neighbors = append(rp.Areas["study"].Neighbors, "hall")
			},
			want: []string{"room presence neighbors hall and study are not joined by any door or opening in the map"},
		},
		{
			name: "leads outside without an exterior door",
			change: func(m *Map, _ *roompresence.Config) {
				m.Doors["front"].Exterior = false
			},
			want: []string{`area "porch" leads outside, but no room of it has an exterior door in the map`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, rp := exampleMap(t), exampleRoomPresence(t)
			tt.change(m, rp)
			got := CrossCheck(m, rp)
			require.Len(t, got, len(tt.want), "%q", got)
			for i, w := range tt.want {
				assert.Contains(t, got[i], w)
			}
		})
	}
}
