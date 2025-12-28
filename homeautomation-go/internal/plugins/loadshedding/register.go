package loadshedding

import (
	"fmt"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "loadshedding",
		Description: "Load shedding - controls thermostats based on energy state",
		Priority:    plugin.PriorityDefault,
		Order:       80, // After TV (70)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new LoadShedding plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("loadshedding plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("loadshedding plugin requires internal state.Manager")
	}

	manager := NewManager(haClient, stateManager, ctx.Logger, ctx.ReadOnly, ctx.Registry)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "loadshedding"
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
func (p *pluginAdapter) GetShadowState() interface{} {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager instance.
func (p *pluginAdapter) GetManager() *Manager {
	return p.manager
}
