package sleephygiene

import (
	"fmt"
	"time"

	"homeautomation/internal/config"
	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	pkgstate "homeautomation/pkg/state"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "sleephygiene",
		Description: "Sleep hygiene - manages wake-up sequences and alarm integration",
		Priority:    plugin.PriorityDefault,
		Order:       45, // After music (40), before lighting (50)
		Factory:     createPlugin,
	})
}

// createPlugin creates a new SleepHygiene plugin instance from the plugin context.
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	// Unwrap the interfaces to get the internal types
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("sleephygiene plugin requires internal ha.HAClient")
	}

	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("sleephygiene plugin requires internal state.Manager")
	}

	// Load schedule configuration
	configLoader := config.NewLoader(ctx.ConfigDir, ctx.Logger)
	if err := configLoader.LoadScheduleConfig(); err != nil {
		return nil, fmt.Errorf("failed to load schedule config: %w", err)
	}

	// Convert plugin.TimeProvider to sleephygiene.TimeProvider if provided
	// Both interfaces are identical (Now() time.Time), so we can use a type adapter
	var timeProvider TimeProvider
	if ctx.TimeProvider != nil {
		timeProvider = timeProviderAdapter{ctx.TimeProvider}
	}

	manager := NewManager(haClient, stateManager, configLoader, ctx.Logger, ctx.ReadOnly, timeProvider)
	return &pluginAdapter{manager: manager}, nil
}

// timeProviderAdapter adapts plugin.TimeProvider to sleephygiene.TimeProvider
type timeProviderAdapter struct {
	provider plugin.TimeProvider
}

func (a timeProviderAdapter) Now() time.Time {
	return a.provider.Now()
}

// pluginAdapter wraps the Manager to implement the plugin.Plugin interface.
type pluginAdapter struct {
	manager *Manager
}

func (p *pluginAdapter) Name() string {
	return "sleephygiene"
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
