package earlywake

import (
	"fmt"
	"path/filepath"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "earlywake",
		Description: "Wakes the common areas when someone turns on a living-space light while a bedroom sleeps",
		Priority:    plugin.PriorityDefault,
		Order:       56, // After picos (55). It needs only the state manager.
		Factory:     createPlugin,
	})
}

// createPlugin builds the early wake plugin from a plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("earlywake plugin requires internal ha.HAClient")
	}
	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("earlywake plugin requires internal state.Manager")
	}

	cfg, err := LoadConfig(filepath.Join(ctx.ConfigDir, "early_wake_config.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load early wake config: %w", err)
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, stateManager, cfg, ctx.Logger, ctx.ReadOnly)
	manager.SetSubscriptionRegistry(ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps Manager to satisfy plugin.Plugin.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string { return "earlywake" }

func (p *pluginAdapter) Start() error { return p.manager.Start() }

func (p *pluginAdapter) Stop() { p.manager.Stop() }

// GetShadowState implements plugin.ShadowStateProvider.
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager (used by tests).
func (p *pluginAdapter) GetManager() *Manager { return p.manager }
