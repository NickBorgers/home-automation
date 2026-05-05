package alert

import (
	"context"
	"errors"
	"testing"

	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestManager_Send_DispatchesBothChannels(t *testing.T) {
	ntfyMock := ntfy.NewMockClient()
	notifyMock := &notify.MockNotifier{}
	m := NewManager(ntfyMock, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{
		Title:   "Test Alert",
		Body:    "test body",
		Urgency: notify.UrgencyDeferable,
		Tags:    []string{"warning"},
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ntfyMock.GetCalls()) != 1 {
		t.Fatalf("expected 1 ntfy call, got %d", len(ntfyMock.GetCalls()))
	}
	if ntfyMock.GetCalls()[0].Title != "Test Alert" {
		t.Errorf("expected title 'Test Alert', got %q", ntfyMock.GetCalls()[0].Title)
	}
	if ntfyMock.GetCalls()[0].Priority != ntfy.PriorityDefault {
		t.Errorf("expected priority %d, got %d", ntfy.PriorityDefault, ntfyMock.GetCalls()[0].Priority)
	}
	if len(notifyMock.Calls()) != 1 {
		t.Fatalf("expected 1 TTS call, got %d", len(notifyMock.Calls()))
	}
	if notifyMock.Calls()[0].Message != "test body" {
		t.Errorf("expected TTS message 'test body', got %q", notifyMock.Calls()[0].Message)
	}
}

func TestManager_Send_EmptyBodyReturnsError(t *testing.T) {
	m := NewManager(nil, &notify.MockNotifier{}, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{Title: "Test", Body: ""})
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

func TestManager_Send_NilNtfyClientSkipsPush(t *testing.T) {
	notifyMock := &notify.MockNotifier{}
	m := NewManager(nil, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{Title: "Test", Body: "body"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(notifyMock.Calls()) != 1 {
		t.Fatalf("expected 1 TTS call, got %d", len(notifyMock.Calls()))
	}
}

func TestManager_Send_UrgentUsesUrgentPriority(t *testing.T) {
	ntfyMock := ntfy.NewMockClient()
	m := NewManager(ntfyMock, &notify.MockNotifier{}, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{
		Title:   "Urgent",
		Body:    "urgent body",
		Urgency: notify.UrgencyUrgent,
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ntfyMock.GetCalls()[0].Priority != ntfy.PriorityUrgent {
		t.Errorf("expected priority %d for urgent, got %d", ntfy.PriorityUrgent, ntfyMock.GetCalls()[0].Priority)
	}
}

func TestManager_Send_ExplicitPriorityIsRespected(t *testing.T) {
	ntfyMock := ntfy.NewMockClient()
	m := NewManager(ntfyMock, &notify.MockNotifier{}, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{
		Body:     "body",
		Urgency:  notify.UrgencyDeferable,
		Priority: 2,
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ntfyMock.GetCalls()[0].Priority != 2 {
		t.Errorf("expected priority 2, got %d", ntfyMock.GetCalls()[0].Priority)
	}
}

func TestManager_Send_NtfyErrorIsSwallowed(t *testing.T) {
	ntfyMock := ntfy.NewMockClient()
	ntfyMock.Error = errors.New("ntfy unavailable")
	notifyMock := &notify.MockNotifier{}
	m := NewManager(ntfyMock, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{Body: "body"})
	if err != nil {
		t.Fatalf("ntfy error should be swallowed, got %v", err)
	}
	// TTS should still be called even if ntfy failed
	if len(notifyMock.Calls()) != 1 {
		t.Fatalf("expected 1 TTS call, got %d", len(notifyMock.Calls()))
	}
}

func TestManager_Send_SuppressedAsleepPropagates(t *testing.T) {
	notifyMock := &notify.MockNotifier{Err: notify.ErrSuppressedAsleep}
	m := NewManager(nil, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{Body: "body"})
	if !errors.Is(err, notify.ErrSuppressedAsleep) {
		t.Fatalf("expected ErrSuppressedAsleep, got %v", err)
	}
}

func TestManager_Send_TtsErrorIsSwallowed(t *testing.T) {
	notifyMock := &notify.MockNotifier{Err: errors.New("speaker offline")}
	m := NewManager(nil, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{Body: "body"})
	if err != nil {
		t.Fatalf("non-suppression TTS error should be swallowed, got %v", err)
	}
}

func TestManager_Send_CustomSpeakers(t *testing.T) {
	notifyMock := &notify.MockNotifier{}
	m := NewManager(nil, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{
		Body:     "body",
		Speakers: []string{"Kitchen", "Bedroom"},
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(notifyMock.Calls()[0].Speakers) != 2 {
		t.Errorf("expected 2 speakers, got %d", len(notifyMock.Calls()[0].Speakers))
	}
}

func TestMockAlerter_RecordsCalls(t *testing.T) {
	m := &MockAlerter{}

	_ = m.Send(context.Background(), Alert{Title: "A", Body: "b1"})
	_ = m.Send(context.Background(), Alert{Title: "B", Body: "b2"})

	calls := m.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Title != "A" || calls[1].Title != "B" {
		t.Errorf("unexpected call order: %v", calls)
	}
}

func TestMockAlerter_ReturnsErr(t *testing.T) {
	sentinel := errors.New("test error")
	m := &MockAlerter{Err: sentinel}

	err := m.Send(context.Background(), Alert{Body: "body"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	// Call must still be recorded even when returning error
	if len(m.Calls()) != 1 {
		t.Fatalf("expected call to be recorded, got %d calls", len(m.Calls()))
	}
}

func TestMockAlerter_Reset(t *testing.T) {
	m := &MockAlerter{}
	_ = m.Send(context.Background(), Alert{Body: "body"})
	m.Reset()
	if len(m.Calls()) != 0 {
		t.Fatalf("expected 0 calls after reset, got %d", len(m.Calls()))
	}
}

func TestManager_Send_NtfyTagsForwarded(t *testing.T) {
	ntfyMock := ntfy.NewMockClient()
	m := NewManager(ntfyMock, &notify.MockNotifier{}, zaptest.NewLogger(t))

	_ = m.Send(context.Background(), Alert{
		Body: "body",
		Tags: []string{"droplet", "warning"},
	})

	tags := ntfyMock.GetCalls()[0].Tags
	if len(tags) != 2 || tags[0] != "droplet" || tags[1] != "warning" {
		t.Errorf("unexpected tags: %v", tags)
	}
}

func TestManager_Send_SpeechOverridesBodyForTTS(t *testing.T) {
	ntfyMock := ntfy.NewMockClient()
	notifyMock := &notify.MockNotifier{}
	m := NewManager(ntfyMock, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{
		Title:  "Test",
		Body:   "long detailed body for push",
		Speech: "short spoken version",
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ntfyMock.GetCalls()[0].Body != "long detailed body for push" {
		t.Errorf("ntfy should receive Body, got %q", ntfyMock.GetCalls()[0].Body)
	}
	if notifyMock.Calls()[0].Message != "short spoken version" {
		t.Errorf("TTS should receive Speech, got %q", notifyMock.Calls()[0].Message)
	}
}

func TestManager_Send_EmptySpeechFallsBackToBody(t *testing.T) {
	notifyMock := &notify.MockNotifier{}
	m := NewManager(nil, notifyMock, zaptest.NewLogger(t))

	err := m.Send(context.Background(), Alert{Body: "fallback body"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if notifyMock.Calls()[0].Message != "fallback body" {
		t.Errorf("TTS should fall back to Body when Speech empty, got %q", notifyMock.Calls()[0].Message)
	}
}

// zaptest.NewLogger returns a *zap.Logger, confirm we can accept it.
func TestNewManager_ReturnsNonNil(t *testing.T) {
	m := NewManager(nil, &notify.MockNotifier{}, zap.NewNop())
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
}
