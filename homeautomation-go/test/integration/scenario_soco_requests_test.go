package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/plugins/music"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// TestScenario_SoCoRequests_DoNotOverlap checks that simultaneous speaker work cannot
// corrupt SoCo-CLI responses.
//
// Story: "When automations operate on several speakers at once, each operation should
// receive its own SoCo-CLI response."
//
// INVARIANT: Only one request from the home automation SoCo client may be in flight at
// a time because SoCo-CLI captures command output through process-wide stdout.
func TestScenario_SoCoRequests_DoNotOverlap(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	inFlight := 0
	overlapped := false
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > 1 {
			overlapped = true
		}
		isFirst := inFlight == 1 && r.URL.Path == "/Primary Bed/volume"
		mu.Unlock()

		if isFirst {
			close(firstStarted)
			<-releaseFirst
		}

		mu.Lock()
		inFlight--
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(music.SoCoResponse{Result: "24"})
	}))
	defer server.Close()

	client := music.NewSoCoClient(server.URL, testlogger.New(), false)

	// GIVEN: A volume read is still running in SoCo-CLI.
	t.Log("GIVEN: Primary Bed's SoCo volume read is still running")
	firstDone := make(chan error, 1)
	go func() {
		_, err := client.GetVolume("Primary Bed")
		firstDone <- err
	}()
	<-firstStarted

	// WHEN: Another automation reads a different speaker at the same time.
	t.Log("WHEN: Primary Bath's volume is read concurrently")
	secondDone := make(chan error, 1)
	go func() {
		_, err := client.GetVolume("Primary Bath")
		secondDone <- err
	}()
	time.Sleep(50 * time.Millisecond)

	// THEN: The second request waits until the first operation has completed.
	t.Log("THEN: The second request waits, so SoCo-CLI never handles overlapping requests")
	mu.Lock()
	requestOverlapped := overlapped
	mu.Unlock()
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.False(t, requestOverlapped, "SoCo-CLI requests must be serialized")
}
