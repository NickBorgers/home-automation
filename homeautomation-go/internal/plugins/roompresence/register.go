package roompresence

import (
	"fmt"
	"path/filepath"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"

	"go.uber.org/zap"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "roompresence",
		Description: "Room presence from Frigate person events",
		Priority:    plugin.PriorityDefault,
		Order:       45, // Before lighting (50), which reads its outputs.
		Factory:     createPlugin,
	})
}

// createPlugin builds the plugin. A missing or invalid config does not fail startup: the plugin runs
// unconfigured and says why in shadow state.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("roompresence plugin requires internal ha.HAClient")
	}
	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("roompresence plugin requires internal state.Manager")
	}

	reason := ""
	cfg, err := LoadConfig(filepath.Join(ctx.ConfigDir, ConfigFileName))
	if err != nil {
		ctx.Logger.Error("Room presence config is invalid; running unconfigured", zap.Error(err))
		reason = err.Error()
	}

	m := NewManager(ctx.ShutdownCtx, haClient, stateManager, cfg, reason, ctx.Logger, ctx.ReadOnly, ctx.Registry)
	m.SetOccupancy(ctx.Occupancy)
	return &pluginAdapter{manager: m}, nil
}

// pluginAdapter wraps Manager to satisfy plugin.Plugin.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string { return "roompresence" }

func (p *pluginAdapter) Start() error { return p.manager.Start() }

func (p *pluginAdapter) Stop() { p.manager.Stop() }

// GetShadowState implements plugin.ShadowStateProvider.
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager.
func (p *pluginAdapter) GetManager() *Manager { return p.manager }
