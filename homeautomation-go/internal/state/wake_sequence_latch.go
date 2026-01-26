package state

import (
	"sync"

	"go.uber.org/zap"
)

// WakeSequenceLatch manages the wake sequence latch for isAnyoneHomeAndAwake.
//
// The latch behavior:
// - Activates on rising edge of isWakeSequenceActive (false->true transition)
// - Keeps isAnyoneHomeAndAwake=true even when everyone is asleep
// - Clears when isAnyoneAsleep transitions from true to false (everyone wakes up)
//
// This prevents lights from flickering off during the morning wake-up routine
// when Caroline might turn off bedroom lights while Nick is still getting ready.
type WakeSequenceLatch struct {
	mu            sync.RWMutex
	active        bool
	manager       *Manager
	logger        *zap.Logger
	subs          []Subscription
	onLatchChange func() // Callback when latch state changes
}

// NewWakeSequenceLatch creates a new wake sequence latch.
// The onLatchChange callback is invoked whenever the latch state changes,
// allowing the caller to trigger recomputation of isAnyoneHomeAndAwake.
func NewWakeSequenceLatch(manager *Manager, logger *zap.Logger, onLatchChange func()) *WakeSequenceLatch {
	return &WakeSequenceLatch{
		manager:       manager,
		logger:        logger.Named("wake-latch"),
		onLatchChange: onLatchChange,
	}
}

// Start begins monitoring for wake sequence and sleep state changes.
// This sets up subscriptions to:
// - isWakeSequenceActive: to activate latch on rising edge
// - isAnyoneAsleep: to clear latch when everyone wakes up
func (l *WakeSequenceLatch) Start() error {
	// Subscribe to wake sequence changes - latch on rising edge
	sub, err := l.manager.Subscribe("isWakeSequenceActive", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)

		// Latch only activates on rising edge (false -> true)
		if !oldBool && newBool {
			l.mu.Lock()
			if !l.active {
				l.active = true
				l.logger.Info("Wake sequence latch activated (rising edge of isWakeSequenceActive)")
				l.mu.Unlock()
				if l.onLatchChange != nil {
					l.onLatchChange()
				}
			} else {
				l.mu.Unlock()
			}
		}
	})
	if err != nil {
		return err
	}
	l.subs = append(l.subs, sub)

	// Subscribe to isAnyoneAsleep - clear latch when everyone wakes up
	sub, err = l.manager.Subscribe("isAnyoneAsleep", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)

		// Clear latch on falling edge (true -> false), meaning everyone woke up
		if oldBool && !newBool {
			l.mu.Lock()
			if l.active {
				l.active = false
				l.logger.Info("Wake sequence latch cleared (isAnyoneAsleep became false)")
				l.mu.Unlock()
				if l.onLatchChange != nil {
					l.onLatchChange()
				}
			} else {
				l.mu.Unlock()
			}
		}
	})
	if err != nil {
		return err
	}
	l.subs = append(l.subs, sub)

	l.logger.Info("Wake sequence latch monitoring started")
	return nil
}

// Stop stops monitoring and cleans up subscriptions.
func (l *WakeSequenceLatch) Stop() {
	for _, sub := range l.subs {
		sub.Unsubscribe()
	}
	l.subs = nil
	l.logger.Info("Wake sequence latch monitoring stopped")
}

// IsActive returns whether the latch is currently active.
func (l *WakeSequenceLatch) IsActive() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.active
}

// Reset clears the latch state.
// This is useful for testing or manual reset scenarios.
func (l *WakeSequenceLatch) Reset() {
	l.mu.Lock()
	wasActive := l.active
	l.active = false
	l.mu.Unlock()

	if wasActive {
		l.logger.Info("Wake sequence latch manually reset")
		if l.onLatchChange != nil {
			l.onLatchChange()
		}
	}
}
