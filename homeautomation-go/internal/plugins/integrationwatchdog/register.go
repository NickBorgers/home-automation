package integrationwatchdog

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
		Name:        "integrationwatchdog",
		Description: "Watches HA integrations for stale state and reloads config entries to recover",
		Priority:    plugin.PriorityDefault,
		Order:       95, // Late — no other plugins depend on it
		Factory:     createPlugin,
	})
}

// createPlugin builds the watchdog plugin from a plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("integrationwatchdog plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("integrationwatchdog plugin requires internal state.Manager")
	}

	configPath := filepath.Join(ctx.ConfigDir, "integration_watchdog_config.yaml")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load integration watchdog config: %w", err)
	}

	mgr := NewManager(ctx.ShutdownCtx, haClient, stateManager, cfg, ctx.Logger, ctx.ReadOnly, ctx.Registry)
	return &pluginAdapter{manager: mgr}, nil
}

// pluginAdapter wraps Manager to satisfy plugin.Plugin.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string { return "integrationwatchdog" }

func (p *pluginAdapter) Start() error { return p.manager.Start() }

func (p *pluginAdapter) Stop() { p.manager.Stop() }

// GetShadowState implements plugin.ShadowStateProvider.
func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

// GetManager returns the underlying Manager (used by tests).
func (p *pluginAdapter) GetManager() *Manager { return p.manager }
