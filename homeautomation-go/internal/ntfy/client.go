// Package ntfy provides a client for sending notifications via ntfy.sh
package ntfy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Notifier is the interface for sending notifications.
// This allows for easy mocking in tests.
type Notifier interface {
	Send(msg *Message) error
}

// Message represents a notification to be sent via ntfy.sh
type Message struct {
	Title    string   // Notification title
	Body     string   // Notification body (required)
	Priority int      // Priority 1-5: min, low, default, high, urgent
	Tags     []string // Emoji tags: "warning", "droplet", "thermometer", etc.
	Click    string   // URL to open when notification is tapped
}

// Priority constants for ntfy notifications
const (
	PriorityMin     = 1
	PriorityLow     = 2
	PriorityDefault = 3
	PriorityHigh    = 4
	PriorityUrgent  = 5
)

// Client is an ntfy.sh notification client
type Client struct {
	baseURL    string // e.g., "https://ntfy.sh"
	topic      string // e.g., "my-secret-topic"
	httpClient *http.Client
	logger     *zap.Logger
	readOnly   bool
}

// ntfyPayload is the JSON payload sent to ntfy.sh
type ntfyPayload struct {
	Topic    string   `json:"topic,omitempty"`
	Title    string   `json:"title,omitempty"`
	Message  string   `json:"message"`
	Priority int      `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
}

// NewClient creates a new ntfy client.
// topicURL should be the full URL including topic, e.g., "https://ntfy.sh/my-topic"
// Returns nil if topicURL is empty, logging an error.
func NewClient(topicURL string, logger *zap.Logger, readOnly bool) *Client {
	if topicURL == "" {
		logger.Error("NTFY_TOPIC_URL not configured - notifications will be disabled",
			zap.String("action", "set NTFY_TOPIC_URL environment variable to enable notifications"))
		return nil
	}

	// Parse the topic URL to extract base URL and topic
	parsed, err := url.Parse(topicURL)
	if err != nil {
		logger.Error("Invalid NTFY_TOPIC_URL - notifications will be disabled",
			zap.String("url", topicURL),
			zap.Error(err))
		return nil
	}

	// Extract topic from path (e.g., "/my-topic" -> "my-topic")
	topic := strings.TrimPrefix(parsed.Path, "/")
	if topic == "" {
		logger.Error("NTFY_TOPIC_URL missing topic - notifications will be disabled",
			zap.String("url", topicURL),
			zap.String("action", "URL should be like https://ntfy.sh/your-topic"))
		return nil
	}

	// Construct base URL (scheme + host)
	baseURL := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)

	return &Client{
		baseURL: baseURL,
		topic:   topic,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:   logger,
		readOnly: readOnly,
	}
}

// Send sends a notification via ntfy.sh
func (c *Client) Send(msg *Message) error {
	if msg == nil {
		return fmt.Errorf("message cannot be nil")
	}
	if msg.Body == "" {
		return fmt.Errorf("message body cannot be empty")
	}

	// Validate priority
	priority := msg.Priority
	if priority < PriorityMin || priority > PriorityUrgent {
		priority = PriorityDefault
	}

	payload := ntfyPayload{
		Topic:    c.topic, // Required when POSTing JSON to base URL
		Title:    msg.Title,
		Message:  msg.Body,
		Priority: priority,
		Tags:     msg.Tags,
		Click:    msg.Click,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal notification payload: %w", err)
	}

	if c.readOnly {
		c.logger.Info("READ_ONLY: Would send ntfy notification",
			zap.String("title", msg.Title),
			zap.String("body", msg.Body),
			zap.Int("priority", priority),
			zap.Strings("tags", msg.Tags),
		)
		return nil
	}

	// POST JSON to base URL (not topic URL) per ntfy.sh API requirements
	req, err := http.NewRequest(http.MethodPost, c.baseURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned non-success status: %d", resp.StatusCode)
	}

	c.logger.Debug("Sent ntfy notification",
		zap.String("title", msg.Title),
		zap.String("body", msg.Body),
		zap.Int("priority", priority),
	)

	return nil
}

// MapImportanceToNtfyPriority converts Home Assistant importance levels to ntfy priority
func MapImportanceToNtfyPriority(importance string) int {
	switch importance {
	case "max":
		return PriorityUrgent
	case "high":
		return PriorityHigh
	case "default", "":
		return PriorityDefault
	case "low":
		return PriorityLow
	case "min":
		return PriorityMin
	default:
		return PriorityDefault
	}
}
