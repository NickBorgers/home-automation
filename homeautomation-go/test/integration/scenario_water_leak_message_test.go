package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Story: When a leak sensor gets wet, I hear which place is leaking in one short sentence.
//
// Invariants:
//   - The spoken text and the push body name the place once.
//   - Each says "leak" once, and neither repeats the device or entity name.
//   - The place comes from the device name when there is one, without a "Sensor" suffix.
//   - Without a device name, the place comes from the friendly name.
func TestScenario_WaterLeakMessage_NamesPlaceOnce(t *testing.T) {
	tests := []struct {
		name         string
		deviceName   string // empty: no device registry entry
		friendlyName string
		wantSpeech   string
		wantBody     string
	}{
		{
			name:         "device name preferred",
			deviceName:   "Example Sink Water Leak Sensor",
			friendlyName: "Example Sink Water Leak Sensor Water leak detected",
			wantSpeech:   "Water leak at the Example Sink.",
			wantBody:     "Leak at the Example Sink",
		},
		{
			name:         "friendly name fallback",
			friendlyName: "Example Tub Leak Sensor Water leak detected",
			wantSpeech:   "Water leak at the Example Tub.",
			wantBody:     "Leak at the Example Tub",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env, cleanup := setupSensorMonitoringTest(t)
			defer cleanup()

			// GIVEN: a dry leak sensor
			t.Log("GIVEN: a dry leak sensor")
			entityID := "binary_sensor.example_leak"
			attrs := map[string]interface{}{"device_class": "moisture", "friendly_name": tc.friendlyName}
			env.server.SetState(entityID, "off", attrs)
			if tc.deviceName != "" {
				env.server.SetRegistries(
					[]map[string]interface{}{{"entity_id": entityID, "device_id": "dev1", "labels": []string{}}},
					[]map[string]interface{}{{"id": "dev1", "name": tc.deviceName, "labels": []string{}}},
				)
			}
			waitForProcessing(t, env.manager)
			require.NoError(t, env.environmental.Start())
			defer env.environmental.Stop()
			env.mockAlerter.Reset()

			// WHEN: the sensor gets wet
			t.Log("WHEN: the sensor reports a leak")
			env.server.SetState(entityID, "on", attrs)
			waitForCondition(t, func() bool { return len(env.mockAlerter.Calls()) >= 1 }, "leak alert")

			// THEN: one short message names the place once
			t.Log("THEN: speech and push body name the place once and say leak once")
			calls := env.mockAlerter.Calls()
			require.Len(t, calls, 1)
			a := calls[0]
			assert.Equal(t, "Water leak", a.Title)
			assert.Equal(t, tc.wantSpeech, a.Speech)
			assert.Equal(t, tc.wantBody, a.Body)
			for _, text := range []string{a.Speech, a.Body} {
				assert.Equal(t, 1, strings.Count(strings.ToLower(text), "leak"), text)
				assert.NotContains(t, strings.ToLower(text), "sensor", text)
			}
		})
	}
}
