// Package notify is a public re-export of internal/notify so external
// modules (downstream private plugins composed via go.work) can build
// against the shared TTS announcement helper.
//
// The public Notifier interface is a type alias for the internal one — the
// same value flows through plugin.Context.Notifier with no conversion.
// Options (WithSpeakers, WithUrgency) and urgency constants are re-bound
// here so downstream callers don't need access to internal/notify.
package notify

import (
	internal "homeautomation/internal/notify"
)

// Notifier sends verbal (TTS) announcements to speakers.
type Notifier = internal.Notifier

// AnnounceOption customizes a single Announce call.
type AnnounceOption = internal.AnnounceOption

// UrgencyLevel controls how the notifier handles an announcement when the
// master is asleep.
type UrgencyLevel = internal.UrgencyLevel

// Urgency levels. See internal/notify for the full semantic definition.
const (
	UrgencyDeferable = internal.UrgencyDeferable
	UrgencyUrgent    = internal.UrgencyUrgent
)

// ErrSuppressedAsleep is returned by Announce when a UrgencyDeferable
// announcement is dropped because the master is asleep.
var ErrSuppressedAsleep = internal.ErrSuppressedAsleep

// WithSpeakers overrides the default speaker list for this announcement.
var WithSpeakers = internal.WithSpeakers

// WithUrgency sets the announcement urgency.
var WithUrgency = internal.WithUrgency

// MockNotifier records Announce calls for tests. Re-exported so downstream
// plugin tests can assert TTS behavior without writing their own mock (and
// without access to internal/notify's unexported announceOpts).
type MockNotifier = internal.MockNotifier

// MockCall captures a single Announce invocation on MockNotifier.
type MockCall = internal.MockCall
