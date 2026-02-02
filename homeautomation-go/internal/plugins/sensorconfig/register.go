package sensorconfig

import (
	"fmt"
	"path/filepath"

	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "sensorconfig",
		Description: "Sensor config - applies Zigbee sensor threshold configurations at startup",
		Priority:    plugin.PriorityDefault,
		Order:       5, // Very early - configuration should happen before other plugins
		Factory:     createPlugin,
	})
}

// createPlugin creates a new SensorConfig plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the HA client interface to get the internal type
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("sensorconfig plugin requires internal ha.HAClient")
	}

	// Load sensor configuration
	configPath := filepath.Join(ctx.ConfigDir, "sensor_config.yaml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load sensor config: %w", err)
	}

	manager := NewManager(ctx.ShutdownCtx, haClient, config, ctx.Logger, ctx.ReadOnly)
	return &pluginAdapter{manager: manager}, nil
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "sensorconfig"
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
