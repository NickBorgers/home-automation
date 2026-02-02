package christmas

import (
	"fmt"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "christmas",
		Description: "Activates holiday lights when input_boolean.christmas is turned on",
		Priority:    plugin.PriorityDefault,
		Order:       70, // After sexmode (65)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new christmas plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("christmas plugin requires internal ha.HAClient")
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, ctx.Logger, ctx.ReadOnly, ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "christmas"
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
