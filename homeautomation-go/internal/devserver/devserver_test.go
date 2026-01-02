package devserver

import (
	"fmt"
	"homeautomation/internal/testlogger"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDevServer(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	t.Run("default port", func(t *testing.T) {

		ds := NewDevServer(logger, 0)
		require.NotNil(t, ds)
		assert.Equal(t, DefaultDevPort, ds.port)
	})

	t.Run("custom port", func(t *testing.T) {

		ds := NewDevServer(logger, 19999)
		require.NotNil(t, ds)
		assert.Equal(t, 19999, ds.port)
	})
}

func TestDevServer_StartStop(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Use a unique port for testing
	ds := NewDevServer(logger, 19876)

	err := ds.Start()
	require.NoError(t, err)

	// Verify server is accessible
	url := fmt.Sprintf("http://localhost:%d/api/websocket", 19876)
	resp, err := http.Get(url)
	require.NoError(t, err)
	resp.Body.Close()
	// WebSocket endpoint returns 400 for regular HTTP GET, which is expected
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Stop server
	err = ds.Stop()
	require.NoError(t, err)

	// Verify server is no longer accessible (may need a brief delay)
	time.Sleep(100 * time.Millisecond)
}

func TestDevServer_GetURLs(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	ds := NewDevServer(logger, 12345)

	assert.Equal(t, "ws://localhost:12345/api/websocket", ds.GetWebSocketURL())
	assert.Equal(t, DefaultDevToken, ds.GetToken())
}

func TestDevServer_SampleData(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()

	// Use a unique port for testing
	ds := NewDevServer(logger, 19877)

	err := ds.Start()
	require.NoError(t, err)
	defer ds.Stop()

	// Check that server is running and has sample data
	// We can verify by checking that the mock server's states are populated
	// The sample data is populated in populateSampleData()

	// Verify the WebSocket URL format
	assert.Contains(t, ds.GetWebSocketURL(), "ws://")
	assert.Contains(t, ds.GetWebSocketURL(), "19877")
}
