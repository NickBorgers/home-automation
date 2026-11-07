package roompresence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deploymentConfigDir is the config directory at the workspace root. A public checkout has no room
// presence config there. A private deployment's CI puts its own configs there, so these tests check the
// real config and replay its captured walk-throughs without either living in this repository.
var deploymentConfigDir = filepath.Join("..", "..", "..", "..", "configs")

// deploymentFixtureDir holds a deployment's replay logs: <name>.jsonl plus <name>.expect.yaml.
const deploymentFixtureDir = "room_presence_fixtures"

func replayFile(t *testing.T, cfg *Config, logPath string) {
	t.Helper()
	f, err := os.Open(logPath)
	require.NoError(t, err)
	defer f.Close()
	msgs, err := ReadCapture(f)
	require.NoError(t, err)
	require.NotEmpty(t, msgs, "empty replay log")

	expPath := strings.TrimSuffix(logPath, ".jsonl") + ".expect.yaml"
	data, err := os.ReadFile(expPath)
	require.NoError(t, err, "every replay log needs %s", filepath.Base(expPath))
	exp, err := ParseExpectations(data)
	require.NoError(t, err)

	failures, _ := Replay(cfg, msgs, exp)
	for _, f := range failures {
		t.Error(f)
	}
}

func TestReplay_ExampleWalk(t *testing.T) {
	replayFile(t, exampleConfig(t), filepath.Join("testdata", "example_walk.jsonl"))
}

func TestReplay_ReportsFailures(t *testing.T) {
	cfg := exampleConfig(t)
	msgs := []CapturedMessage{
		{T: 100, Topic: eventsTopic, Payload: `{"type":"update","after":{"id":"a","camera":"cam_lounge",` +
			`"label":"person","entered_zones":["lounge_to_study","garden"],"current_zones":["lounge_to_study"]}}`},
		{T: 101, Topic: eventsTopic, Payload: `not json`},
	}
	none := []string{}
	failures, tr := Replay(cfg, msgs, &ReplayExpectations{
		Checkpoints:  []ReplayCheckpoint{{After: 5, Occupied: []string{"study"}, Empty: []string{"lounge"}}},
		UnknownZones: &none,
	})
	assert.True(t, tr.Occupied("lounge"))
	assert.Len(t, failures, 4, "%v", failures) // bad JSON, study, lounge, unknown zones
}

func TestReplay_PeopleCountApplies(t *testing.T) {
	cfg := exampleConfig(t)
	ev := func(id string) CapturedMessage {
		return CapturedMessage{T: 100, Topic: eventsTopic, Payload: `{"type":"update","after":{"id":"` + id +
			`","camera":"cam_kitchen","label":"person","entered_zones":["kitchen"],"current_zones":["kitchen"]}}`}
	}
	_, tr := Replay(cfg, []CapturedMessage{ev("a")}, &ReplayExpectations{People: &ReplayPeople{Home: 1}})
	assert.True(t, tr.Occupied("kitchen"), "fewer tokens than people confirms the track")
	_, tr = Replay(cfg, []CapturedMessage{ev("a")}, &ReplayExpectations{})
	assert.False(t, tr.Occupied("kitchen"))
}

func TestReadCapture_SortsAndSkipsBlankLines(t *testing.T) {
	msgs, err := ReadCapture(strings.NewReader(
		`{"t":2,"topic":"b","payload":"x"}` + "\n\n" + `{"t":1,"topic":"a","payload":"y"}` + "\n"))
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "a", msgs[0].Topic)

	_, err = ReadCapture(strings.NewReader("{"))
	assert.Error(t, err)
}

// TestDeploymentConfig validates a deployment's real config when one is present.
func TestDeploymentConfig(t *testing.T) {
	cfg := loadDeploymentConfig(t)
	assert.NotEmpty(t, cfg.Areas)
}

// TestDeploymentReplays replays every captured log a deployment ships with its config.
func TestDeploymentReplays(t *testing.T) {
	cfg := loadDeploymentConfig(t)
	logs, err := filepath.Glob(filepath.Join(deploymentConfigDir, deploymentFixtureDir, "*.jsonl"))
	require.NoError(t, err)
	if len(logs) == 0 {
		t.Skip("no deployment replay logs")
	}
	for _, log := range logs {
		t.Run(filepath.Base(log), func(t *testing.T) { replayFile(t, cfg, log) })
	}
}

func loadDeploymentConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join(deploymentConfigDir, ConfigFileName))
	require.NoError(t, err)
	if cfg == nil {
		t.Skip("no deployment room presence config")
	}
	return cfg
}
