package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewSoCoClient(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("returns client when URL provided", func(t *testing.T) {
		client := NewSoCoClient("http://127.0.0.1:8000", logger, false)
		assert.NotNil(t, client)
		assert.Equal(t, "http://127.0.0.1:8000", client.baseURL)
		assert.False(t, client.readOnly)
	})

	t.Run("returns nil when URL empty", func(t *testing.T) {
		client := NewSoCoClient("", logger, false)
		assert.Nil(t, client)
	})

	t.Run("respects readOnly flag", func(t *testing.T) {
		client := NewSoCoClient("http://127.0.0.1:8000", logger, true)
		assert.NotNil(t, client)
		assert.True(t, client.readOnly)
	})
}

func TestSoCoClient_ShareLink(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("successful sharelink", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{
				Result:   "success",
				ExitCode: 0,
				Speaker:  "Kitchen",
				Action:   "sharelink",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.ShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		require.NoError(t, err)
		assert.Contains(t, requestPath, "/Kitchen/sharelink/")
	})

	t.Run("non-zero exit code returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := SoCoResponse{
				Result:   "",
				ErrorMsg: "speaker not found",
				ExitCode: 1,
				Speaker:  "Kitchen",
				Action:   "sharelink",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.ShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exit_code=1")
		assert.Contains(t, err.Error(), "speaker not found")
	})

	t.Run("HTTP error returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.ShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 500")
	})

	t.Run("connection failure returns error", func(t *testing.T) {
		client := NewSoCoClient("http://localhost:1", logger, false)
		err := client.ShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "request failed")
	})

	t.Run("read-only mode does not call server", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, true)
		err := client.ShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.NoError(t, err)
		assert.Equal(t, 0, callCount, "server should not have been called")
	})
}

func TestSoCoClient_PlayFromQueue(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("successful play_from_queue", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{
				Result:   "success",
				ExitCode: 0,
				Speaker:  "Kitchen",
				Action:   "play_from_queue",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayFromQueue("Kitchen")
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/play_from_queue", requestPath)
	})

	t.Run("read-only mode does not call server", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, true)
		err := client.PlayFromQueue("Kitchen")
		assert.NoError(t, err)
		assert.Equal(t, 0, callCount, "server should not have been called")
	})
}

func TestSoCoClient_PlayShareLink(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("calls sharelink then play_from_queue", func(t *testing.T) {
		var actions []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action := "unknown"
			if r.URL.Path == "/Kitchen/play_from_queue" {
				action = "play_from_queue"
			} else {
				action = "sharelink"
			}
			actions = append(actions, action)
			resp := SoCoResponse{
				Result:   "success",
				ExitCode: 0,
				Speaker:  "Kitchen",
				Action:   action,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		require.NoError(t, err)
		assert.Equal(t, []string{"sharelink", "play_from_queue"}, actions)
	})

	t.Run("returns error if sharelink fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := SoCoResponse{
				ExitCode: 1,
				ErrorMsg: "bad url",
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sharelink failed")
	})

	t.Run("returns error if play_from_queue fails", func(t *testing.T) {
		callNum := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callNum++
			resp := SoCoResponse{ExitCode: 0, Result: "ok"}
			if callNum > 1 {
				// Second call (play_from_queue) fails
				resp = SoCoResponse{ExitCode: 1, ErrorMsg: "queue empty"}
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "play_from_queue failed")
	})
}
