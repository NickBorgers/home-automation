package infrastructure

import (
	"fmt"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "infrastructure",
		Description: "Monitors aerobic septic system with failure detection and alerts",
		Priority:    plugin.PriorityDefault,
		Order:       70, // After monitoring (65)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new infrastructure plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("infrastructure plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("infrastructure plugin requires internal state.Manager")
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, stateManager, ctx.Logger, ctx.ReadOnly, ctx.Registry, ctx.NtfyClient, ctx.Notifier)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "infrastructure"
}

func (p *pluginAdapter) Start() error {
	return p.manager.Start()
}

func (p *pluginAdapter) Stop() {
	p.manager.Stop()
}

// Implement plugin.Resettable
func (p *pluginAdapter) Reset() error {
	return p.manager.Reset()
}

// Implement plugin.ShadowStateProvider
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager instance.
func (p *pluginAdapter) GetManager() *Manager {
	return p.manager
}
