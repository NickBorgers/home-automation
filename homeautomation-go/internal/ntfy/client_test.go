package ntfy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewClient(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("returns client when URL provided", func(t *testing.T) {
		client := NewClient("https://ntfy.sh/test-topic", logger, false)
		assert.NotNil(t, client)
		assert.Equal(t, "https://ntfy.sh", client.baseURL)
		assert.Equal(t, "test-topic", client.topic)
		assert.False(t, client.readOnly)
	})

	t.Run("returns nil when URL empty", func(t *testing.T) {
		client := NewClient("", logger, false)
		assert.Nil(t, client)
	})

	t.Run("returns nil when URL has no topic", func(t *testing.T) {
		client := NewClient("https://ntfy.sh", logger, false)
		assert.Nil(t, client)
	})

	t.Run("returns nil when URL has only slash", func(t *testing.T) {
		client := NewClient("https://ntfy.sh/", logger, false)
		assert.Nil(t, client)
	})

	t.Run("respects readOnly flag", func(t *testing.T) {
		client := NewClient("https://ntfy.sh/test-topic", logger, true)
		assert.NotNil(t, client)
		assert.True(t, client.readOnly)
	})
}

func TestClient_Send(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("sends valid notification", func(t *testing.T) {
		var receivedPayload ntfyPayload

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			err = json.Unmarshal(body, &receivedPayload)
			require.NoError(t, err)

			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewClient(server.URL+"/test-topic", logger, false)

		msg := &Message{
			Title:    "Test Title",
			Body:     "Test body message",
			Priority: PriorityHigh,
			Tags:     []string{"warning", "house"},
			Click:    "https://example.com",
		}

		err := client.Send(msg)
		require.NoError(t, err)

		assert.Equal(t, "test-topic", receivedPayload.Topic)
		assert.Equal(t, "Test Title", receivedPayload.Title)
		assert.Equal(t, "Test body message", receivedPayload.Message)
		assert.Equal(t, PriorityHigh, receivedPayload.Priority)
		assert.Equal(t, []string{"warning", "house"}, receivedPayload.Tags)
		assert.Equal(t, "https://example.com", receivedPayload.Click)
	})

	t.Run("sends minimal notification", func(t *testing.T) {
		var receivedPayload ntfyPayload

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &receivedPayload)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewClient(server.URL+"/my-topic", logger, false)

		msg := &Message{
			Body: "Just the message",
		}

		err := client.Send(msg)
		require.NoError(t, err)

		assert.Equal(t, "my-topic", receivedPayload.Topic)
		assert.Equal(t, "", receivedPayload.Title)
		assert.Equal(t, "Just the message", receivedPayload.Message)
		assert.Equal(t, PriorityDefault, receivedPayload.Priority)
	})

	t.Run("defaults invalid priority to default", func(t *testing.T) {
		var receivedPayload ntfyPayload

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &receivedPayload)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewClient(server.URL+"/test-topic", logger, false)

		// Test priority too high
		msg := &Message{Body: "test", Priority: 10}
		err := client.Send(msg)
		require.NoError(t, err)
		assert.Equal(t, PriorityDefault, receivedPayload.Priority)

		// Test priority too low
		msg = &Message{Body: "test", Priority: -1}
		err = client.Send(msg)
		require.NoError(t, err)
		assert.Equal(t, PriorityDefault, receivedPayload.Priority)
	})

	t.Run("returns error for nil message", func(t *testing.T) {
		client := NewClient("https://ntfy.sh/test", logger, false)
		err := client.Send(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "message cannot be nil")
	})

	t.Run("returns error for empty body", func(t *testing.T) {
		client := NewClient("https://ntfy.sh/test", logger, false)
		err := client.Send(&Message{Title: "Title only"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "message body cannot be empty")
	})

	t.Run("returns error on server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := NewClient(server.URL+"/test-topic", logger, false)

		err := client.Send(&Message{Body: "test"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "non-success status: 500")
	})

	t.Run("returns error on network failure", func(t *testing.T) {
		client := NewClient("http://localhost:1/test-topic", logger, false)

		err := client.Send(&Message{Body: "test"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send notification")
	})

	t.Run("does not send in read-only mode", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewClient(server.URL+"/test-topic", logger, true) // readOnly = true

		err := client.Send(&Message{
			Title: "Test",
			Body:  "Should not be sent",
		})

		assert.NoError(t, err)
		assert.Equal(t, 0, callCount, "server should not have been called")
	})
}

func TestMapImportanceToNtfyPriority(t *testing.T) {
	tests := []struct {
		importance string
		expected   int
	}{
		{"max", PriorityUrgent},
		{"high", PriorityHigh},
		{"default", PriorityDefault},
		{"", PriorityDefault},
		{"low", PriorityLow},
		{"min", PriorityMin},
		{"unknown", PriorityDefault},
	}

	for _, tt := range tests {
		t.Run(tt.importance, func(t *testing.T) {
			result := MapImportanceToNtfyPriority(tt.importance)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMockClient(t *testing.T) {
	t.Run("records calls", func(t *testing.T) {
		mock := NewMockClient()

		msg1 := &Message{Title: "First", Body: "Body 1"}
		msg2 := &Message{Title: "Second", Body: "Body 2"}

		err := mock.Send(msg1)
		assert.NoError(t, err)

		err = mock.Send(msg2)
		assert.NoError(t, err)

		calls := mock.GetCalls()
		assert.Len(t, calls, 2)
		assert.Equal(t, "First", calls[0].Title)
		assert.Equal(t, "Second", calls[1].Title)
	})

	t.Run("returns configured error", func(t *testing.T) {
		mock := NewMockClient()
		mock.SetError(assert.AnError)

		err := mock.Send(&Message{Body: "test"})
		assert.Error(t, err)
	})

	t.Run("reset clears calls and error", func(t *testing.T) {
		mock := NewMockClient()
		mock.Send(&Message{Body: "test"})
		mock.SetError(assert.AnError)

		mock.Reset()

		assert.Len(t, mock.GetCalls(), 0)
		assert.NoError(t, mock.Send(&Message{Body: "after reset"}))
	})
}

// Ensure Client and MockClient implement Notifier
var _ Notifier = (*Client)(nil)
var _ Notifier = (*MockClient)(nil)
