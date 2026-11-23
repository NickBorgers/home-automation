package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Story: When a leak sensor is wet, I keep getting the alert every 15 minutes until the sensor
// dries, so a leak can't be missed.
//
// Invariants:
//   - Exactly one alert when the sensor first reports a leak.
//   - No alert before 15 minutes have passed since that sensor's last alert.
//   - While the sensor stays wet, one more alert arrives every 15 minutes.
//   - Once the sensor reads anything but "on", no more alerts arrive, however long we wait.
//   - A later leak alerts immediately and starts a fresh 15 minute cycle.
func TestScenario_WaterLeakRepeat_AlertsEvery15MinutesUntilDry(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	// GIVEN: a dry leak sensor
	t.Log("GIVEN: a dry leak sensor named Example Sink")
	entityID := "binary_sensor.example_sink_leak"
	attrs := map[string]interface{}{"device_class": "moisture", "friendly_name": "Example Sink Leak Sensor Water leak detected"}
	env.server.SetState(entityID, "off", attrs)
	waitForProcessing(t, env.manager)
	require.NoError(t, env.environmental.Start())
	env.mockAlerter.Reset()

	count := func() int { return len(env.mockAlerter.Calls()) }
	advance := func(d time.Duration) { env.mockClock.AdvanceAndProcess(d) }

	// WHEN: the sensor gets wet (T+0)
	t.Log("WHEN T+0: the sensor reports a leak")
	env.server.SetState(entityID, "on", attrs)
	waitForCondition(t, func() bool { return count() == 1 }, "first leak alert")

	// THEN: nothing more until 15 minutes have passed
	t.Log("THEN T+14m: still only the first alert")
	advance(14 * time.Minute)
	assert.Equal(t, 1, count(), "no repeat before 15 minutes")

	t.Log("THEN T+15m: the first repeat arrives")
	advance(1 * time.Minute)
	assert.Equal(t, 2, count(), "repeat at 15 minutes")

	t.Log("THEN T+29m: no extra alert")
	advance(14 * time.Minute)
	assert.Equal(t, 2, count(), "no repeat between 15 and 30 minutes")

	t.Log("THEN T+30m: the second repeat arrives")
	advance(1 * time.Minute)
	require.Equal(t, 3, count(), "repeat at 30 minutes")

	calls := env.mockAlerter.Calls()
	assert.Equal(t, calls[0].Urgency, calls[2].Urgency, "repeat keeps the urgency")
	assert.Contains(t, calls[2].Speech, "Example Sink")
	assert.Contains(t, calls[2].Body, "Example Sink")

	// WHEN: the sensor dries
	t.Log("WHEN T+31m: the sensor dries")
	advance(1 * time.Minute)
	env.server.SetState(entityID, "off", attrs)
	waitForProcessing(t, env.manager)

	// THEN: no more alerts, even after another 30 minutes
	t.Log("THEN T+61m: no alert after the sensor dried")
	advance(30 * time.Minute)
	assert.Equal(t, 3, count(), "no alerts once the sensor is dry")

	// WHEN: it gets wet again
	t.Log("WHEN: the sensor gets wet again")
	env.server.SetState(entityID, "on", attrs)
	waitForCondition(t, func() bool { return count() == 4 }, "immediate alert on new leak")

	// THEN: a fresh 15 minute cycle starts from this alert
	t.Log("THEN: the new leak repeats 15 minutes after its own first alert")
	advance(14 * time.Minute)
	assert.Equal(t, 4, count())
	advance(1 * time.Minute)
	assert.Equal(t, 5, count())
}

// Invariant: a sensor that reads "unavailable" stops the repeats, and the next "on" alerts at once.
func TestScenario_WaterLeakRepeat_UnavailableStopsRepeats(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("GIVEN: a wet leak sensor that has alerted once")
	entityID := "binary_sensor.example_tub_leak"
	attrs := map[string]interface{}{"device_class": "moisture", "friendly_name": "Example Tub Leak Sensor Water leak detected"}
	env.server.SetState(entityID, "off", attrs)
	waitForProcessing(t, env.manager)
	require.NoError(t, env.environmental.Start())
	env.mockAlerter.Reset()
	env.server.SetState(entityID, "on", attrs)
	waitForCondition(t, func() bool { return len(env.mockAlerter.Calls()) == 1 }, "first leak alert")

	t.Log("WHEN: the sensor goes unavailable")
	env.server.SetState(entityID, "unavailable", attrs)
	waitForProcessing(t, env.manager)

	t.Log("THEN: no repeat arrives")
	env.mockClock.AdvanceAndProcess(45 * time.Minute)
	assert.Len(t, env.mockAlerter.Calls(), 1)

	t.Log("WHEN: it reports a leak again")
	env.server.SetState(entityID, "on", attrs)

	t.Log("THEN: it alerts immediately")
	waitForCondition(t, func() bool { return len(env.mockAlerter.Calls()) == 2 }, "alert after unavailable")
}

// Invariant: each sensor repeats on its own schedule, measured from its own last alert.
func TestScenario_WaterLeakRepeat_SensorsRepeatIndependently(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("GIVEN: two dry leak sensors")
	a, b := "binary_sensor.example_sink_leak", "binary_sensor.example_tub_leak"
	attrsA := map[string]interface{}{"device_class": "moisture", "friendly_name": "Example Sink Leak Sensor Water leak detected"}
	attrsB := map[string]interface{}{"device_class": "moisture", "friendly_name": "Example Tub Leak Sensor Water leak detected"}
	env.server.SetState(a, "off", attrsA)
	env.server.SetState(b, "off", attrsB)
	waitForProcessing(t, env.manager)
	require.NoError(t, env.environmental.Start())
	env.mockAlerter.Reset()
	count := func() int { return len(env.mockAlerter.Calls()) }

	t.Log("WHEN T+0: the sink gets wet; T+10m: the tub gets wet")
	env.server.SetState(a, "on", attrsA)
	waitForCondition(t, func() bool { return count() == 1 }, "sink alert")
	env.mockClock.AdvanceAndProcess(10 * time.Minute)
	env.server.SetState(b, "on", attrsB)
	waitForCondition(t, func() bool { return count() == 2 }, "tub alert")

	t.Log("THEN T+15m: only the sink repeats")
	env.mockClock.AdvanceAndProcess(5 * time.Minute)
	require.Equal(t, 3, count())
	assert.Contains(t, env.mockAlerter.Calls()[2].Speech, "Example Sink")

	t.Log("THEN T+25m: the tub repeats")
	env.mockClock.AdvanceAndProcess(10 * time.Minute)
	require.Equal(t, 4, count())
	assert.Contains(t, env.mockAlerter.Calls()[3].Speech, "Example Tub")
}
