package state

import (
	"sync"

	"go.uber.org/zap"
)

// WakeSequenceLatch manages the wake sequence latch for isAnyoneHomeAndAwake.
//
// The latch behavior:
// - Activates on rising edge of isWakeSequenceActive (false->true transition)
// - Activates on rising edge of isEarlyWakeActive, or at Start if it is already true
// - Keeps isAnyoneHomeAndAwake=true even when everyone is asleep
// - Clears when isAnyoneAsleep transitions from true to false (everyone wakes up)
// - Clears when isEarlyWakeActive transitions from true to false, unless isWakeSequenceActive is true
//
// While the alarm wake sequence runs (isWakeSequenceActive true), it owns the latch,
// so the end of early wake does not clear it.
//
// This prevents lights from flickering off during the morning wake-up routine
// when Caroline might turn off bedroom lights while Nick is still getting ready.
//
// Early wake (isEarlyWakeActive) means someone got up while a bedroom still sleeps.
// The latch wakes the common areas for them. It never sets isWakeSequenceActive,
// so the sleep music zone and the bedroom lights stay as they are.
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
// - isEarlyWakeActive: to activate latch on rising edge (and at Start if already true), and clear it on falling edge
// - isAnyoneAsleep: to clear latch when everyone wakes up
func (l *WakeSequenceLatch) Start() error {
	// Subscribe to wake sequence changes - latch on rising edge
	sub, err := l.manager.Subscribe("isWakeSequenceActive", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)

		// Latch only activates on rising edge (false -> true)
		if !oldBool && newBool {
			l.activate("Wake sequence latch activated (rising edge of isWakeSequenceActive)")
		}
	})
	if err != nil {
		return err
	}
	l.subs = append(l.subs, sub)

	// Subscribe to early wake changes - latch on rising edge, clear on falling edge.
	// Early wake clears at a normal wake or at bedtime. At bedtime the latch must
	// let go too, or the house would stay awake all night.
	sub, err = l.manager.Subscribe("isEarlyWakeActive", func(key string, oldValue, newValue interface{}) {
		oldBool, _ := oldValue.(bool)
		newBool, _ := newValue.(bool)

		if !oldBool && newBool {
			l.activate("Wake sequence latch activated (rising edge of isEarlyWakeActive)")
			return
		}
		if oldBool && !newBool {
			// The alarm wake sequence owns the latch while it runs.
			if wakeSeq, err := l.manager.GetBool("isWakeSequenceActive"); err == nil && wakeSeq {
				l.logger.Info("Early wake ended; wake sequence latch kept (isWakeSequenceActive is true)")
				return
			}
			l.clear("Wake sequence latch cleared (isEarlyWakeActive became false)")
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
			l.clear("Wake sequence latch cleared (isAnyoneAsleep became false)")
		}
	})
	if err != nil {
		return err
	}
	l.subs = append(l.subs, sub)

	// Early wake is a held state, not a moment like the alarm. If it is already on
	// (a restart during an early morning), the house must stay awake.
	if earlyWake, err := l.manager.GetBool("isEarlyWakeActive"); err == nil && earlyWake {
		l.activate("Wake sequence latch activated (isEarlyWakeActive already true at start)")
	}

	l.logger.Info("Wake sequence latch monitoring started")
	return nil
}

// activate turns the latch on and triggers recomputation if it was off.
func (l *WakeSequenceLatch) activate(reason string) {
	l.mu.Lock()
	if l.active {
		l.mu.Unlock()
		return
	}
	l.active = true
	l.logger.Info(reason)
	l.mu.Unlock()
	if l.onLatchChange != nil {
		l.onLatchChange()
	}
}

// clear turns the latch off and triggers recomputation if it was on.
func (l *WakeSequenceLatch) clear(reason string) {
	l.mu.Lock()
	if !l.active {
		l.mu.Unlock()
		return
	}
	l.active = false
	l.logger.Info(reason)
	l.mu.Unlock()
	if l.onLatchChange != nil {
		l.onLatchChange()
	}
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
