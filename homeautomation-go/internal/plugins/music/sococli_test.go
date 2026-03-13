package music

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestSoCoClient_ClearQueue(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("successful clear_queue", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{
				Result:   "",
				ExitCode: 0,
				Speaker:  "Front Room",
				Action:   "clear_queue",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.ClearQueue("Front Room")
		require.NoError(t, err)
		assert.Equal(t, "/Front Room/clear_queue", requestPath)
	})

	t.Run("read-only mode does not call server", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, true)
		err := client.ClearQueue("Front Room")
		assert.NoError(t, err)
		assert.Equal(t, 0, callCount, "server should not have been called")
	})
}

func TestSoCoClient_PlayShareLink(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("calls clear_queue then sharelink then play_from_queue", func(t *testing.T) {
		var actions []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action := "unknown"
			switch {
			case r.URL.Path == "/Kitchen/clear_queue":
				action = "clear_queue"
			case r.URL.Path == "/Kitchen/play_from_queue":
				action = "play_from_queue"
			default:
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
		assert.Equal(t, []string{"clear_queue", "sharelink", "play_from_queue"}, actions)
	})

	t.Run("returns error if clear_queue fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := SoCoResponse{
				ExitCode: 1,
				ErrorMsg: "speaker not found",
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayShareLink("Kitchen", "https://tidal.com/browse/playlist/abc123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "clear_queue failed")
	})

	t.Run("returns error if sharelink fails", func(t *testing.T) {
		callNum := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callNum++
			resp := SoCoResponse{ExitCode: 0, Result: "ok"}
			if callNum == 2 {
				// Second call (sharelink) fails
				resp = SoCoResponse{ExitCode: 1, ErrorMsg: "bad url"}
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
			if callNum == 3 {
				// Third call (play_from_queue) fails
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

// =============================================================================
// Direct Speaker Command Tests
// =============================================================================

func TestSoCoClient_SetVolume(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("sets volume with correct endpoint", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetVolume("Kitchen", 42)
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/volume/42", requestPath)
	})

	t.Run("read-only mode skips call", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, true)
		err := client.SetVolume("Kitchen", 50)
		assert.NoError(t, err)
		assert.Equal(t, 0, callCount)
	})
}

func TestSoCoClient_MuteUnmute(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("mute uses correct endpoint", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.Mute("Kitchen")
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/mute", requestPath)
	})

	t.Run("unmute uses correct endpoint", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.Unmute("Kitchen")
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/mute/off", requestPath)
	})
}

func TestSoCoClient_GroupSpeaker(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("follower joins lead group", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.GroupSpeaker("Bedroom", "Kitchen")
		require.NoError(t, err)
		assert.Equal(t, "/Bedroom/group/Kitchen", requestPath)
	})

	t.Run("handles speaker names with spaces", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// r.URL.Path is decoded by Go's HTTP server, so spaces appear as-is
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.GroupSpeaker("Front Room", "Primary Bathroom")
		require.NoError(t, err)
		assert.Equal(t, "/Front Room/group/Primary Bathroom", requestPath)
	})
}

func TestSoCoClient_UngroupSpeaker(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("uses correct endpoint", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.UngroupSpeaker("Kitchen")
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/ungroup", requestPath)
	})
}

func TestSoCoClient_Play(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("uses correct endpoint", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.Play("Kitchen")
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/play", requestPath)
	})
}

func TestSoCoClient_AddURIToQueue(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("uses correct endpoint with URI", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.AddURIToQueue("Kitchen", "http://example.com/rain.m3u")
		require.NoError(t, err)
		assert.Contains(t, requestPath, "/Kitchen/add_uri_to_queue/")
	})

	t.Run("non-zero exit code returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := SoCoResponse{ExitCode: 1, ErrorMsg: "invalid URI"}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.AddURIToQueue("Kitchen", "http://example.com/rain.m3u")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid URI")
	})

	t.Run("read-only mode does not call server", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, true)
		err := client.AddURIToQueue("Kitchen", "http://example.com/rain.m3u")
		assert.NoError(t, err)
		assert.Equal(t, 0, callCount, "server should not have been called")
	})
}

func TestSoCoClient_PlayURIFromQueue(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("calls clear_queue then add_uri_to_queue then play_from_queue", func(t *testing.T) {
		var actions []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action := "unknown"
			switch {
			case r.URL.Path == "/Kitchen/clear_queue":
				action = "clear_queue"
			case r.URL.Path == "/Kitchen/play_from_queue":
				action = "play_from_queue"
			default:
				action = "add_uri_to_queue"
			}
			actions = append(actions, action)
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayURIFromQueue("Kitchen", "http://example.com/rain.m3u")
		require.NoError(t, err)
		assert.Equal(t, []string{"clear_queue", "add_uri_to_queue", "play_from_queue"}, actions)
	})

	t.Run("returns error if clear_queue fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := SoCoResponse{ExitCode: 1, ErrorMsg: "speaker not found"}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayURIFromQueue("Kitchen", "http://example.com/rain.m3u")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "clear_queue failed")
	})

	t.Run("returns error if add_uri_to_queue fails", func(t *testing.T) {
		callNum := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callNum++
			resp := SoCoResponse{ExitCode: 0, Result: "ok"}
			if callNum == 2 {
				resp = SoCoResponse{ExitCode: 1, ErrorMsg: "invalid URI"}
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayURIFromQueue("Kitchen", "http://example.com/rain.m3u")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "add_uri_to_queue failed")
	})

	t.Run("returns error if play_from_queue fails", func(t *testing.T) {
		callNum := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callNum++
			resp := SoCoResponse{ExitCode: 0, Result: "ok"}
			if callNum == 3 {
				resp = SoCoResponse{ExitCode: 1, ErrorMsg: "queue empty"}
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.PlayURIFromQueue("Kitchen", "http://example.com/rain.m3u")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "play_from_queue failed")
	})
}

func TestSoCoClient_SetShuffle(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("shuffle on", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetShuffle("Kitchen", true)
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/shuffle/on", requestPath)
	})

	t.Run("shuffle off", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetShuffle("Kitchen", false)
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/shuffle/off", requestPath)
	})
}

func TestSoCoClient_SetRepeat(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("repeat all", func(t *testing.T) {
		var requestPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath = r.URL.Path
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetRepeat("Kitchen", "all")
		require.NoError(t, err)
		assert.Equal(t, "/Kitchen/repeat/all", requestPath)
	})
}

// =============================================================================
// Retry Logic Tests
// =============================================================================

func TestSoCoClient_RetryOnServerError(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("retries on HTTP 500 and succeeds on second attempt", func(t *testing.T) {
		var attemptCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempt := atomic.AddInt32(&attemptCount, 1)
			if attempt == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("temporary error"))
				return
			}
			resp := SoCoResponse{Result: "success", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetVolume("Kitchen", 42)
		require.NoError(t, err)
		assert.Equal(t, int32(2), atomic.LoadInt32(&attemptCount))
	})

	t.Run("exhausts all retries on persistent server error", func(t *testing.T) {
		var attemptCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attemptCount, 1)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("persistent error"))
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetVolume("Kitchen", 42)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("all %d attempts failed", socoMaxRetries+1))
		assert.Equal(t, int32(socoMaxRetries+1), atomic.LoadInt32(&attemptCount))
	})
}

func TestSoCoClient_NoRetryOnApplicationError(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("does not retry SoCo application error (non-zero exit_code)", func(t *testing.T) {
		var attemptCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attemptCount, 1)
			resp := SoCoResponse{ExitCode: 1, ErrorMsg: "speaker not found"}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetVolume("Kitchen", 42)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "speaker not found")
		assert.Equal(t, int32(1), atomic.LoadInt32(&attemptCount), "should not retry SoCo application errors")
	})

	t.Run("does not retry HTTP 404", func(t *testing.T) {
		var attemptCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attemptCount, 1)
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("not found"))
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, false)
		err := client.SetVolume("Kitchen", 42)
		assert.Error(t, err)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attemptCount), "should not retry HTTP 4xx errors")
	})
}

func TestSoCoClient_RetryOnConnectionError(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	t.Run("retries on connection refused", func(t *testing.T) {
		// Point to a port that is not listening
		client := NewSoCoClient("http://127.0.0.1:1", logger, false)
		err := client.SetVolume("Kitchen", 42)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "all 3 attempts failed")
	})
}

func TestIsRetryableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil error", nil, false},
		{"network error", fmt.Errorf("sococli volume request failed: connection refused"), true},
		{"HTTP 500", fmt.Errorf("sococli volume returned HTTP 500: error"), true},
		{"HTTP 502", fmt.Errorf("sococli volume returned HTTP 502: bad gateway"), true},
		{"HTTP 503", fmt.Errorf("sococli volume returned HTTP 503: unavailable"), true},
		{"HTTP 429", fmt.Errorf("sococli volume returned HTTP 429: rate limited"), true},
		{"read body failure", fmt.Errorf("sococli volume: failed to read response body: reset"), true},
		{"HTTP 404", fmt.Errorf("sococli volume returned HTTP 404: not found"), false},
		{"HTTP 400", fmt.Errorf("sococli volume returned HTTP 400: bad request"), false},
		{"SoCo exit_code", fmt.Errorf("sococli volume failed (exit_code=1): speaker not found"), false},
		{"parse error", fmt.Errorf("sococli volume: failed to parse response: invalid json"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.retryable, isRetryableError(tt.err))
		})
	}
}
