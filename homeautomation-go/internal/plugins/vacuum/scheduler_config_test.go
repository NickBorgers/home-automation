package vacuum

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadConfigText(t *testing.T, text string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vacuum_config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	return cfg
}

func TestSchedulerConfigDefaults(t *testing.T) {
	cfg := loadConfigText(t, `
vacuum:
  scheduler:
    mode: log_only
    valetudo_url: http://robot.example
    rooms:
      kitchen: {segment_id: "3", full: 48h, vacuum: 12h}
      office: {segment_id: "4", full: 84h}
`)
	sc := cfg.Vacuum.Scheduler
	require.NoError(t, sc.Err)
	assert.True(t, sc.Enabled())
	assert.Equal(t, 70.0, sc.Battery.StartMinPercent())
	assert.Equal(t, 30.0, sc.Battery.FloorPercent())
	assert.Equal(t, 0.3, sc.Battery.LearningRate)
	assert.Equal(t, 5.0, sc.Battery.DefaultTripCost)
	assert.Equal(t, 0.6, sc.Battery.DefaultCostPerM2[TreatmentFull])
	assert.Equal(t, 0.4, sc.Battery.DefaultCostPerM2[TreatmentVacuum])
	assert.Greater(t, sc.Battery.DefaultCostPerM2[TreatmentFull], sc.Battery.DefaultCostPerM2[TreatmentVacuum],
		"a full clean costs more")
	assert.Equal(t, 5*time.Minute, sc.ReplanInterval)
	assert.Equal(t, 10*time.Minute, sc.StableFor)
	assert.Equal(t, 30*time.Second, sc.TrackInterval)
	assert.Equal(t, 2*time.Minute, sc.MinDwell)
	assert.Equal(t, "vacuum_and_mop", sc.Treatments[TreatmentFull].OperationMode)
	assert.Equal(t, "vacuum", sc.Treatments[TreatmentVacuum].OperationMode)
	assert.Equal(t, 48*time.Hour, sc.Rooms["kitchen"].Full)
	assert.Equal(t, 12*time.Hour, sc.Rooms["kitchen"].Vacuum)
	assert.Equal(t, time.Duration(0), sc.Rooms["office"].Vacuum)
	assert.Equal(t, []itemKey{{"kitchen", TreatmentFull}, {"kitchen", TreatmentVacuum}, {"office", TreatmentFull}},
		sc.itemKeys())
}

func TestSchedulerConfigMissingIsOff(t *testing.T) {
	cfg := loadConfigText(t, "vacuum: {}\n")
	assert.NoError(t, cfg.Vacuum.Scheduler.Err)
	assert.Equal(t, ModeOff, cfg.Vacuum.Scheduler.Mode)
	assert.False(t, cfg.Vacuum.Scheduler.Enabled())
	assert.False(t, (&SchedulerConfig{}).Enabled(), "a zero config is not enabled")
}

func TestSchedulerConfigProblemsDoNotFailLoad(t *testing.T) {
	base := "vacuum:\n  scheduler:\n    mode: active\n    valetudo_url: http://robot.example\n"
	room := "    rooms:\n      kitchen: {segment_id: \"3\", full: 48h}\n"
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad mode", "vacuum:\n  scheduler:\n    mode: sometimes\n", "mode"},
		{"no rooms", base, "no rooms"},
		{"no url", "vacuum:\n  scheduler:\n    mode: active\n" + room, "valetudo_url"},
		{"bad url", "vacuum:\n  scheduler:\n    mode: active\n    valetudo_url: robot\n" + room, "valetudo_url"},
		{"floor above start", base + room + "    battery: {start_min: 50, floor: 60}\n", "floor < start_min"},
		{"start above 100", base + room + "    battery: {start_min: 120}\n", "start_min"},
		{"bad rate", base + room + "    battery: {learning_rate: 2}\n", "learning_rate"},
		{"negative trip", base + room + "    battery: {default_trip_cost: -1}\n", "default_trip_cost"},
		{"unknown treatment", base + room + "    treatments: {mop: {}}\n", "unknown treatment"},
		{"bad duration", base + "    stable_for: soon\n" + room, "stable_for"},
		{"no segment", base + "    rooms:\n      kitchen: {full: 48h}\n", "segment_id"},
		{"no full", base + "    rooms:\n      kitchen: {segment_id: \"3\"}\n", "full interval is required"},
		{"bad vacuum", base + "    rooms:\n      kitchen: {segment_id: \"3\", full: 1h, vacuum: -1h}\n", "positive"},
		{"bad room name", base + "    rooms:\n      Kitchen: {segment_id: \"3\", full: 1h}\n", "lower case"},
		{"reserved room", base + "    rooms:\n      trip: {segment_id: \"3\", full: 1h}\n", "reserved"},
		{"sleeper without state", base + room + "    sleepers: [{rooms: [bed]}]\n", "state is required"},
		{"sleeper without rooms", base + room + "    sleepers: [{state: isMasterAsleep}]\n", "rooms are required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfigText(t, tt.yaml)
			require.Error(t, cfg.Vacuum.Scheduler.Err)
			assert.Contains(t, cfg.Vacuum.Scheduler.Err.Error(), tt.want)
			assert.False(t, cfg.Vacuum.Scheduler.Enabled())
			assert.NotEmpty(t, cfg.Vacuum.ErrorSensorID, "the error announcer still gets its defaults")
		})
	}
}

func TestSchedulerConfigOffSkipsRuntimeChecks(t *testing.T) {
	cfg := loadConfigText(t, "vacuum:\n  scheduler:\n    mode: \"off\"\n")
	assert.NoError(t, cfg.Vacuum.Scheduler.Err, "off needs no URL and no rooms")
}

func TestPublicExampleConfigLoads(t *testing.T) {
	cfg, err := LoadConfig("../../../../configs/vacuum_config.yaml")
	require.NoError(t, err)
	sc := cfg.Vacuum.Scheduler
	require.NoError(t, sc.Err)
	assert.Equal(t, ModeOff, sc.Mode, "the public example ships switched off")
	assert.Equal(t, 70.0, sc.Battery.StartMinPercent())
	assert.Equal(t, 30.0, sc.Battery.FloorPercent())
	assert.Equal(t, "vacuum_and_mop", sc.Treatments[TreatmentFull].OperationMode)
	assert.NotEmpty(t, sc.Rooms)
}
