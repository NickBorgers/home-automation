package vacuum

import (
	"fmt"
	"path/filepath"

	"homeautomation/internal/housemap"
	"homeautomation/internal/valetudo"
	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/shadowstate"
	pkgstate "homeautomation/pkg/state"

	"go.uber.org/zap"
)

func init() {
	plugin.Register(plugin.PluginInfo{
		Name:        "vacuum",
		Description: "Casper (robot vacuum): error TTS announcements and the dirty-room scheduler",
		Priority:    plugin.PriorityDefault,
		Order:       60,
		Factory:     createPlugin,
	})
}

func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
	haClient := pkgha.UnwrapClient(ctx.HAClient)
	if haClient == nil {
		return nil, fmt.Errorf("vacuum plugin requires internal ha.HAClient")
	}
	stateManager := pkgstate.UnwrapManager(ctx.StateManager)
	if stateManager == nil {
		return nil, fmt.Errorf("vacuum plugin requires internal state.Manager")
	}

	configPath := filepath.Join(ctx.ConfigDir, "vacuum_config.yaml")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load vacuum config: %w", err)
	}

	manager := NewManager(
		ctx.ShutdownCtx,
		haClient,
		stateManager,
		ctx.AlertNotifier,
		ctx.Notifier,
		cfg,
		ctx.Logger,
		ctx.ReadOnly,
		ctx.TimeProvider,
		ctx.Registry,
	)

	// The scheduler is optional. Whatever is wrong with it, the error announcer keeps running.
	sc := &cfg.Vacuum.Scheduler
	var houseMap *housemap.Map
	var robot Robot
	if sc.Enabled() {
		houseMap, err = housemap.Load(filepath.Join(ctx.ConfigDir, housemap.FileName))
		if err != nil {
			ctx.Logger.Error("Vacuum scheduler cannot load the house map", zap.Error(err))
			houseMap = nil
		}
		robot = valetudo.NewClient(sc.ValetudoURL, nil)
	}
	scheduler := NewScheduler(ctx.ShutdownCtx, haClient, stateManager, sc, houseMap, robot, ctx.Occupancy,
		manager.shadowTracker, ctx.Registry, ctx.Logger, ctx.ReadOnly, ctx.TimeProvider)

	return &pluginAdapter{manager: manager, scheduler: scheduler}, nil
}

type pluginAdapter struct {
	manager   *Manager
	scheduler *Scheduler
}

func (p *pluginAdapter) Name() string { return "vacuum" }

func (p *pluginAdapter) Start() error {
	if err := p.manager.Start(); err != nil {
		return err
	}
	return p.scheduler.Start()
}

func (p *pluginAdapter) Stop() {
	p.scheduler.Stop()
	p.manager.Stop()
}

func (p *pluginAdapter) Reset() error { return p.manager.Reset() }

func (p *pluginAdapter) GetShadowState() shadowstate.PluginShadowState {
	return p.manager.GetShadowState()
}

func (p *pluginAdapter) GetManager() *Manager { return p.manager }
