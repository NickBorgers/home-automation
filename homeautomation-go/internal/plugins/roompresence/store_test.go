package roompresence

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testStore = "input_text.room_presence_tokens"

func storeConfig(t *testing.T) *Config {
	cfg := exampleConfig(t)
	cfg.TokenStore = testStore
	return cfg
}

func TestEncodeDecodeTokens_RoundTrip(t *testing.T) {
	kept := []KeptToken{{Area: "bedroom", Since: t0.Add(-time.Hour)}, {Area: "study", Since: t0.Add(-time.Minute)}}
	v, dropped, err := encodeTokens(t0, kept)
	require.NoError(t, err)
	assert.Zero(t, dropped)
	assert.Equal(t, fmt.Sprintf(`{"t":%d,"k":[["bedroom",%d],["study",%d]]}`, t0.Unix(),
		t0.Add(-time.Hour).Unix(), t0.Add(-time.Minute).Unix()), v)

	savedAt, got, err := decodeTokens(v)
	require.NoError(t, err)
	assert.True(t, savedAt.Equal(t0))
	require.Len(t, got, 2)
	assert.Equal(t, "bedroom", got[0].Area)
	assert.True(t, got[0].Since.Equal(t0.Add(-time.Hour)))
}

func TestEncodeTokens_DropsNewestWhenFull(t *testing.T) {
	var kept []KeptToken
	for i := 0; i < 20; i++ {
		kept = append(kept, KeptToken{Area: "a_long_unseen_area_name", Since: t0.Add(time.Duration(i) * time.Minute)})
	}
	v, dropped, err := encodeTokens(t0, kept)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(v), maxStoreLen)
	assert.Positive(t, dropped)
	_, got, err := decodeTokens(v)
	require.NoError(t, err)
	assert.Len(t, got, 20-dropped)
	assert.True(t, got[0].Since.Equal(t0), "the oldest token is kept")
}

func TestDecodeTokens_RejectsBadValue(t *testing.T) {
	for _, v := range []string{"not json", `{"t":1,"k":[["bedroom"]]}`, `{"t":1,"k":[[1,2]]}`} {
		_, _, err := decodeTokens(v)
		assert.Error(t, err, v)
	}
}

func TestTracker_UnseenTokensAndRestore(t *testing.T) {
	tr := NewTracker(exampleConfig(t))
	tr.newToken(t0, "kitchen", "cam_kitchen", "test", false)
	tr.newToken(t0, "study", "", "test", false)
	assert.Equal(t, []KeptToken{{Area: "study", Since: t0}}, tr.UnseenTokens(), "seen areas are not kept")

	tr2 := NewTracker(exampleConfig(t))
	n := tr2.Restore(t0.Add(time.Minute), []KeptToken{
		{Area: "study", Since: t0},
		{Area: "kitchen", Since: t0}, // seen: the camera finds the person again
		{Area: "gone", Since: t0},    // no longer in the config
	})
	assert.Equal(t, 1, n)
	assert.True(t, tr2.Occupied("study"))
	assert.False(t, tr2.Occupied("kitchen"))
	assert.Empty(t, tr2.DrainEntries(), "a restored token is not a person entering")
}

func TestTracker_RestoredTokenKeepsIdleClock(t *testing.T) {
	tr := NewTracker(exampleConfig(t))
	// The study's idle limit is 3 h. A token that arrived 2 h 59 min before the restart has 1 min left.
	tr.Restore(t0, []KeptToken{{Area: "study", Since: t0.Add(-179 * time.Minute)}})
	tr.Tick(t0.Add(30 * time.Second))
	assert.True(t, tr.Occupied("study"))
	tr.Tick(t0.Add(61 * time.Second))
	assert.False(t, tr.Occupied("study"), "idle limit counts from the first arrival, not the restore")
}

func TestManager_TokenStoreWrittenInLogOnly(t *testing.T) {
	m, mockHA, _ := newTestManager(t, storeConfig(t), false)
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "p", "cam_lounge",
		[]string{"lounge", "lounge_to_study"}, "lounge_to_study"))
	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "end", "p", "cam_lounge",
		[]string{"lounge", "lounge_to_study"}, "lounge_to_study"))
	require.True(t, m.Occupied("study"))
	for _, c := range mockHA.GetServiceCalls() {
		assert.NotContains(t, c.Data["value"], "study", "a token that just arrived is not saved yet")
	}

	clock := t0.Add(time.Minute) // past the settle time
	m.now = func() time.Time { return clock }
	m.apply(func(tr *Tracker, now time.Time) { tr.Tick(now) })

	var last string
	for _, c := range mockHA.GetServiceCalls() {
		assert.Equal(t, "input_text", c.Domain, "log_only writes only the token store")
		last, _ = c.Data["value"].(string)
	}
	_, kept, err := decodeTokens(last)
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, "study", kept[0].Area)

	// A change that does not touch unseen areas writes nothing.
	calls := len(mockHA.GetServiceCalls())
	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "k", "cam_kitchen",
		[]string{"kitchen_to_hall", "kitchen"}, "kitchen"))
	assert.Len(t, mockHA.GetServiceCalls(), calls)
}

func TestManager_TokenStoreReadOnlyNeverWrites(t *testing.T) {
	m, mockHA, _ := newTestManager(t, storeConfig(t), true)
	require.NoError(t, m.Start())
	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "end", "p", "cam_lounge",
		[]string{"lounge", "lounge_to_study"}, "lounge_to_study"))
	m.Stop()
	assert.Empty(t, mockHA.GetServiceCalls())
}

func TestManager_TokenStoreOldCopyNotLoaded(t *testing.T) {
	m, mockHA, _ := newTestManager(t, storeConfig(t), false)
	v, _, err := encodeTokens(t0.Add(-16*time.Minute), []KeptToken{{Area: "study", Since: t0.Add(-time.Hour)}})
	require.NoError(t, err)
	mockHA.SetState(testStore, v, nil)
	require.NoError(t, m.Start())
	defer m.Stop()
	assert.False(t, m.Occupied("study"), "a copy older than token_store_max_age is from a crash, not a restart")
}

func TestManager_TokenStoreRestoreBeforeBedSensor(t *testing.T) {
	m, mockHA, _ := newTestManager(t, storeConfig(t), false)
	v, _, err := encodeTokens(t0, []KeptToken{{Area: "bedroom", Since: t0.Add(-time.Hour)},
		{Area: "bedroom", Since: t0.Add(-50 * time.Minute)}})
	require.NoError(t, err)
	mockHA.SetState(testStore, v, nil)
	mockHA.SetState("binary_sensor.example_bed_presence", "on", nil)
	require.NoError(t, m.Start())
	defer m.Stop()
	assert.Equal(t, 2, m.GetShadowState().Outputs.Areas["bedroom"].Tokens,
		"both people come back, and the bed sensor adds no third token")
}

func TestManager_TokenStoreSavedOnStop(t *testing.T) {
	m, mockHA, _ := newTestManager(t, storeConfig(t), false)
	require.NoError(t, m.Start())
	m.Stop()
	calls := mockHA.GetServiceCalls()
	require.NotEmpty(t, calls, "stop writes the store, so the next start sees a fresh copy")
	v, _ := calls[len(calls)-1].Data["value"].(string)
	_, kept, err := decodeTokens(v)
	require.NoError(t, err)
	assert.Empty(t, kept)
}

func TestManager_StoreTokensSettleAndRefresh(t *testing.T) {
	m, mockHA, _ := newTestManager(t, storeConfig(t), false)
	m.storeTokens(t0, nil, false) // the first save writes the empty list
	base := len(mockHA.GetServiceCalls())
	writes := func() int { return len(mockHA.GetServiceCalls()) - base }
	kept := []KeptToken{{Area: "study", Since: t0}}

	m.storeTokens(t0.Add(2*time.Second), kept, false)
	assert.Equal(t, 0, writes(), "a token that arrived 2 s ago may be a doorway flicker")

	m.storeTokens(t0.Add(storeSettle), kept, false)
	assert.Equal(t, 1, writes(), "settled: saved")

	m.storeTokens(t0.Add(storeSettle+time.Minute), kept, false)
	assert.Equal(t, 1, writes(), "unchanged and not yet due")

	m.storeTokens(t0.Add(storeSettle+5*time.Minute), kept, false)
	assert.Equal(t, 2, writes(), "every max_age/3 the save time is refreshed")

	m.storeTokens(t0.Add(time.Hour), nil, false)
	assert.Equal(t, 3, writes(), "the person left: saved as empty")
	m.storeTokens(t0.Add(2*time.Hour), nil, false)
	assert.Equal(t, 3, writes(), "an empty store needs no refresh")

	m.storeTokens(t0.Add(2*time.Hour), []KeptToken{{Area: "study", Since: t0.Add(2 * time.Hour)}}, true)
	assert.Equal(t, 4, writes(), "the stop save keeps even a token that just arrived")
}
