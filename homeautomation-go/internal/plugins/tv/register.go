package tv

import (
	"fmt"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "tv",
		Description: "TV monitoring - tracks Apple TV, sync box, and HDMI input states",
		Priority:    plugin.PriorityDefault,
		Order:       70, // After security (60)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new TV plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("tv plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("tv plugin requires internal state.Manager")
	}

	manager := NewManager(haClient, stateManager, ctx.Logger, ctx.ReadOnly, ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "tv"
}

func (p *pluginAdapter) Start() error {
	return p.manager.Start()
}

func (p *pluginAdapter) Stop() {
	p.manager.Stop()
}

// Implement plugin.ShadowStateProvider
func (p *pluginAdapter) GetShadowState() interface{} {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager instance.
func (p *pluginAdapter) GetManager() *Manager {
	return p.manager
}
