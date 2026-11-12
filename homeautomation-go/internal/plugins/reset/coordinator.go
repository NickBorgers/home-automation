package reset

import (
	"fmt"

	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Resettable is an interface for plugins that can be reset
type Resettable interface {
	Reset() error
}

// Coordinator watches the reset boolean and orchestrates system-wide resets
type Coordinator struct {
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	plugins      []PluginWithName
	subscription state.Subscription
}

// PluginWithName pairs a resettable plugin with its name for logging
type PluginWithName struct {
	Name   string
	Plugin Resettable
}

// NewCoordinator creates a new reset coordinator
func NewCoordinator(stateManager *state.Manager, logger *zap.Logger, readOnly bool, plugins []PluginWithName) *Coordinator {
	return &Coordinator{
		stateManager: stateManager,
		logger:       logger.Named("reset"),
		readOnly:     readOnly,
		plugins:      plugins,
	}
}

// Start begins monitoring the reset boolean
func (c *Coordinator) Start() error {
	c.logger.Info("Starting Reset Coordinator",
		zap.Int("plugin_count", len(c.plugins)),
		zap.Bool("read_only", c.readOnly))

	// Subscribe to reset changes
	sub, err := c.stateManager.Subscribe("reset", c.handleResetChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to reset: %w", err)
	}
	c.subscription = sub

	// A reset turned on while the app was down produces no change event, so it
	// would stay on and never run. Treat it as a fresh trigger.
	if pending, err := c.stateManager.GetBool("reset"); err == nil && pending {
		c.logger.Info("reset was already on at startup")
		c.handleResetChange("reset", false, true)
	}

	c.logger.Info("Reset Coordinator started successfully")
	return nil
}

// Stop cleans up the coordinator
func (c *Coordinator) Stop() {
	if c.subscription != nil {
		c.subscription.Unsubscribe()
		c.subscription = nil
	}
	c.logger.Info("Reset Coordinator stopped")
}

// handleResetChange processes reset boolean changes
func (c *Coordinator) handleResetChange(key string, oldValue, newValue interface{}) {
	newReset, ok := newValue.(bool)
	if !ok {
		c.logger.Warn("Reset value is not a boolean", zap.Any("value", newValue))
		return
	}

	// Only act when reset goes from false -> true
	if !newReset {
		return
	}

	c.logger.Info("Reset triggered - coordinating system-wide reset")

	// Turn reset back to false immediately to prevent loops
	if !c.readOnly {
		if err := c.stateManager.SetBool("reset", false); err != nil {
			c.logger.Error("Failed to turn reset off", zap.Error(err))
			// Continue with reset anyway
		} else {
			c.logger.Info("Reset boolean turned off")
		}
	} else {
		c.logger.Info("READ-ONLY: Would turn reset boolean off")
	}

	// Execute reset on all plugins
	c.executeReset()
}

// executeReset calls Reset() on all plugins in order
func (c *Coordinator) executeReset() {
	c.logger.Info("Executing reset on all plugins",
		zap.Int("plugin_count", len(c.plugins)))

	successCount := 0
	errorCount := 0

	for _, p := range c.plugins {
		c.logger.Info("Resetting plugin", zap.String("plugin", p.Name))

		if err := p.Plugin.Reset(); err != nil {
			c.logger.Error("Failed to reset plugin",
				zap.String("plugin", p.Name),
				zap.Error(err))
			errorCount++
			// Continue to reset other plugins
		} else {
			c.logger.Info("Successfully reset plugin", zap.String("plugin", p.Name))
			successCount++
		}
	}

	c.logger.Info("Reset complete",
		zap.Int("success", successCount),
		zap.Int("errors", errorCount),
		zap.Int("total", len(c.plugins)))
}
