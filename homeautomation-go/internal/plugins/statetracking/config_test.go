package statetracking

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadConfig_MissingFileIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), ConfigFileName))
	require.NoError(t, err)
	assert.Empty(t, cfg.MasterBedroomDoorSensor)
}

func TestLoadConfig_Sensor(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, "master_bedroom_door_sensor: binary_sensor.example_door\n"))
	require.NoError(t, err)
	assert.Equal(t, "binary_sensor.example_door", cfg.MasterBedroomDoorSensor)
}

func TestLoadConfig_RejectsNonBinarySensor(t *testing.T) {
	for _, id := range []string{"input_boolean.door", "binary_sensor.", "door"} {
		_, err := LoadConfig(writeConfig(t, "master_bedroom_door_sensor: "+id+"\n"))
		assert.Error(t, err, id)
	}
}

func TestLoadConfig_CommentsOnlyIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, "---\n# nothing set\n"))
	require.NoError(t, err)
	assert.Empty(t, cfg.MasterBedroomDoorSensor)
}
