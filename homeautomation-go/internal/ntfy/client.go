// Package ntfy provides a client for sending notifications via ntfy.sh
package ntfy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
	topicURL   string
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
// Returns nil if topicURL is empty, logging an error.
func NewClient(topicURL string, logger *zap.Logger, readOnly bool) *Client {
	if topicURL == "" {
		logger.Error("NTFY_TOPIC_URL not configured - notifications will be disabled",
			zap.String("action", "set NTFY_TOPIC_URL environment variable to enable notifications"))
		return nil
	}

	return &Client{
		topicURL: topicURL,
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

	req, err := http.NewRequest(http.MethodPost, c.topicURL, bytes.NewBuffer(jsonData))
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
