// Package alert provides a unified notification dispatcher that sends alerts
// to both ntfy (push notifications) and TTS (verbal announcements) channels.
package alert

import (
	"context"
	"errors"
	"fmt"

	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"

	"go.uber.org/zap"
)

// Alert describes a notification to dispatch to push and TTS channels.
type Alert struct {
	Title    string              // Push notification title (ntfy)
	Body     string              // Message body: used for both ntfy and TTS
	Urgency  notify.UrgencyLevel // Controls TTS sleep suppression; maps to ntfy priority if Priority is 0
	Tags     []string            // ntfy emoji tags (e.g. "warning", "droplet")
	Speakers []string            // Optional TTS speaker override; uses default speakers if empty
	Priority int                 // Explicit ntfy priority (1-5); derived from Urgency if 0
}

// Alerter dispatches an alert to all configured notification channels.
type Alerter interface {
	Send(ctx context.Context, a Alert) error
}

// Manager implements Alerter, wrapping an ntfy client and a TTS notifier.
// A nil ntfyClient disables push notifications.
type Manager struct {
	ntfyClient ntfy.Notifier
	notifier   notify.Notifier
	logger     *zap.Logger
}

// NewManager creates a Manager. ntfyClient may be nil (push notifications disabled).
func NewManager(ntfyClient ntfy.Notifier, notifier notify.Notifier, logger *zap.Logger) *Manager {
	return &Manager{
		ntfyClient: ntfyClient,
		notifier:   notifier,
		logger:     logger.Named("alert"),
	}
}

// Send dispatches the alert to the push (ntfy) and TTS channels.
//
// Push: always attempted when ntfyClient is configured; errors are logged but
// never propagated. TTS: errors other than ErrSuppressedAsleep are logged and
// discarded. ErrSuppressedAsleep IS propagated so callers that track per-alert
// suppression (e.g. the vacuum plugin) can react accordingly.
func (m *Manager) Send(ctx context.Context, a Alert) error {
	if a.Body == "" {
		return fmt.Errorf("alert body cannot be empty")
	}

	// Push notification via ntfy (no sleep suppression).
	if m.ntfyClient != nil {
		priority := a.Priority
		if priority < ntfy.PriorityMin || priority > ntfy.PriorityUrgent {
			if a.Urgency == notify.UrgencyUrgent {
				priority = ntfy.PriorityUrgent
			} else {
				priority = ntfy.PriorityDefault
			}
		}
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    a.Title,
			Body:     a.Body,
			Priority: priority,
			Tags:     a.Tags,
		}); err != nil {
			m.logger.Error("ntfy send failed", zap.String("title", a.Title), zap.Error(err))
		}
	}

	// TTS announcement (sleep suppression controlled by Urgency).
	opts := []notify.AnnounceOption{notify.WithUrgency(a.Urgency)}
	if len(a.Speakers) > 0 {
		opts = append(opts, notify.WithSpeakers(a.Speakers))
	}
	if err := m.notifier.Announce(ctx, a.Body, opts...); err != nil {
		if errors.Is(err, notify.ErrSuppressedAsleep) {
			m.logger.Debug("TTS announcement suppressed (master asleep)", zap.String("title", a.Title))
			return notify.ErrSuppressedAsleep
		}
		m.logger.Error("TTS announce failed", zap.String("title", a.Title), zap.Error(err))
	}

	return nil
}
