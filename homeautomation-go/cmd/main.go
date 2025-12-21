// Home Automation Go Application
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"homeautomation/internal/api"
	"homeautomation/internal/config"
	dayphaselib "homeautomation/internal/dayphase"
	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/dayphase"
	"homeautomation/internal/plugins/energy"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/loadshedding"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/reset"
	"homeautomation/internal/plugins/security"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/plugins/tv"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Load environment variables from .env file if present
	if err := godotenv.Load(); err != nil {
		logger.Info("No .env file found, using environment variables")
	}

	haURL := os.Getenv("HA_URL")
	haToken := os.Getenv("HA_TOKEN")
	readOnly := os.Getenv("READ_ONLY") == "true"

	if haURL == "" || haToken == "" {
		logger.Fatal("HA_URL and HA_TOKEN environment variables must be set")
	}

	// HTTP API port configuration
	httpPort := 8080 // default port
	if portStr := os.Getenv("HTTP_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			httpPort = port
		} else {
			logger.Warn("Invalid HTTP_PORT value, using default", zap.String("value", portStr), zap.Int("default", 8080))
		}
	}

	// Load timezone (default to UTC if not set)
	timezoneName := os.Getenv("TIMEZONE")
	if timezoneName == "" {
		timezoneName = "UTC"
	}
	timezone, err := time.LoadLocation(timezoneName)
	if err != nil {
		logger.Fatal("Failed to load timezone",
			zap.String("timezone", timezoneName),
			zap.Error(err))
	}
	logger.Info("Using timezone", zap.String("timezone", timezoneName))

	// Determine config directory path
	// Priority: CONFIG_DIR env var > ./configs (container) > ../configs (local dev)
	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		// Auto-detect: prefer ./configs if it exists (container), otherwise ../configs (local dev)
		if _, err := os.Stat("./configs"); err == nil {
			configDir = "./configs"
		} else {
			configDir = "../configs"
		}
	}
	logger.Info("Using config directory", zap.String("path", configDir))

	// Get location coordinates for sun event calculations
	// Default: Austin, TX area (32.85486, -97.50515)
	latitude := 32.85486
	longitude := -97.50515

	if latStr := os.Getenv("LATITUDE"); latStr != "" {
		if lat, err := strconv.ParseFloat(latStr, 64); err == nil {
			latitude = lat
		} else {
			logger.Warn("Invalid LATITUDE value, using default", zap.String("value", latStr))
		}
	}

	if lonStr := os.Getenv("LONGITUDE"); lonStr != "" {
		if lon, err := strconv.ParseFloat(lonStr, 64); err == nil {
			longitude = lon
		} else {
			logger.Warn("Invalid LONGITUDE value, using default", zap.String("value", lonStr))
		}
	}

	logger.Info("Using location coordinates",
		zap.Float64("latitude", latitude),
		zap.Float64("longitude", longitude))

	logger.Info("Starting Home Automation Client",
		zap.String("url", haURL),
		zap.Bool("read_only", readOnly))

	// Create HA client
	client := ha.NewClient(haURL, haToken, logger)

	// Connect to Home Assistant
	if err := client.Connect(); err != nil {
		logger.Fatal("Failed to connect to Home Assistant", zap.Error(err))
	}
	defer client.Disconnect()

	logger.Info("Connected to Home Assistant")

	// Create State Manager
	stateManager := state.NewManager(client, logger, readOnly)

	// Sync all state from HA
	if err := stateManager.SyncFromHA(); err != nil {
		logger.Fatal("Failed to sync state from HA", zap.Error(err))
	}

	// Setup computed state variables
	if err := stateManager.SetupComputedState(); err != nil {
		logger.Fatal("Failed to setup computed state", zap.Error(err))
	}

	// Create Shadow State Tracker
	shadowTracker := shadowstate.NewTracker()
	logger.Info("Shadow State Tracker created")

	// Create Subscription Registry for automatic shadow state input tracking
	subscriptionRegistry := shadowstate.NewSubscriptionRegistry()
	logger.Info("Subscription Registry created for automatic input tracking")

	// Start HTTP API server
	apiServer := api.NewServer(stateManager, shadowTracker, logger, httpPort, timezone)
	if err := apiServer.Start(); err != nil {
		logger.Fatal("Failed to start HTTP API server", zap.Error(err))
	}
	defer apiServer.Stop()
	logger.Info("HTTP API server started",
		zap.Int("port", httpPort),
		zap.String("endpoint", fmt.Sprintf("http://localhost:%d/api/state", httpPort)))

	// Display current state
	displayState(stateManager, logger)

	// Subscribe to interesting state changes
	subscribeToChanges(stateManager, logger)

	// Start State Tracking Manager (MUST start before other plugins that depend on derived states)
	stateTrackingManager := statetracking.NewManager(client, stateManager, logger, readOnly, subscriptionRegistry)
	if err := stateTrackingManager.Start(); err != nil {
		logger.Fatal("Failed to start State Tracking Manager", zap.Error(err))
	}
	defer stateTrackingManager.Stop()
	logger.Info("State Tracking Manager started - computing derived states and sleep detection")

	// Create day phase calculator
	dayPhaseCalc := dayphaselib.NewCalculator(latitude, longitude, logger)

	// Start Day Phase Manager (sun events and day phase)
	dayPhaseManager, err := startDayPhaseManager(client, stateManager, logger, readOnly, configDir, dayPhaseCalc)
	if err != nil {
		logger.Fatal("Failed to start Day Phase Manager", zap.Error(err))
	}
	defer dayPhaseManager.Stop()

	// Start Energy State Manager
	energyManager, err := startEnergyManager(client, stateManager, logger, readOnly, configDir, timezone, subscriptionRegistry)
	if err != nil {
		logger.Fatal("Failed to start Energy State Manager", zap.Error(err))
	}
	defer energyManager.Stop()

	// Start Music Manager
	musicManager, err := startMusicManager(client, stateManager, logger, readOnly, configDir)
	if err != nil {
		logger.Fatal("Failed to start Music Manager", zap.Error(err))
	}
	defer musicManager.Stop()

	// Register music shadow state provider with tracker
	shadowTracker.RegisterPluginProvider("music", func() shadowstate.PluginShadowState {
		return musicManager.GetShadowState()
	})
	logger.Info("Registered music shadow state with tracker")

	// Start Lighting Manager
	lightingManager, err := startLightingManager(client, stateManager, logger, readOnly, configDir, subscriptionRegistry)
	if err != nil {
		logger.Fatal("Failed to start Lighting Manager", zap.Error(err))
	}
	defer lightingManager.Stop()

	// Register lighting shadow state provider with tracker
	shadowTracker.RegisterPluginProvider("lighting", func() shadowstate.PluginShadowState {
		return lightingManager.GetShadowState()
	})
	logger.Info("Registered lighting shadow state with tracker")

	// Start Security Manager
	securityManager := security.NewManager(client, stateManager, logger, readOnly, subscriptionRegistry)
	if err := securityManager.Start(); err != nil {
		logger.Fatal("Failed to start Security Manager", zap.Error(err))
	}
	defer securityManager.Stop()
	logger.Info("Security Manager started successfully")

	// Register security shadow state provider with tracker
	shadowTracker.RegisterPluginProvider("security", func() shadowstate.PluginShadowState {
		return securityManager.GetShadowState()
	})
	logger.Info("Registered security shadow state with tracker")

	// Start Sleep Hygiene Manager
	sleepHygieneManager, err := startSleepHygieneManager(client, stateManager, logger, readOnly, configDir)
	if err != nil {
		logger.Fatal("Failed to start Sleep Hygiene Manager", zap.Error(err))
	}
	defer sleepHygieneManager.Stop()

	// Register sleep hygiene shadow state provider with tracker
	shadowTracker.RegisterPluginProvider("sleephygiene", func() shadowstate.PluginShadowState {
		return sleepHygieneManager.GetShadowState()
	})
	logger.Info("Registered sleep hygiene shadow state with tracker")

	// Start Load Shedding Manager
	loadSheddingManager := loadshedding.NewManager(client, stateManager, logger, readOnly, subscriptionRegistry)
	if err := loadSheddingManager.Start(); err != nil {
		logger.Fatal("Failed to start Load Shedding Manager", zap.Error(err))
	}
	defer loadSheddingManager.Stop()
	logger.Info("Load Shedding Manager started successfully")

	// Register load shedding shadow state provider with tracker
	shadowTracker.RegisterPluginProvider("loadshedding", func() shadowstate.PluginShadowState {
		return loadSheddingManager.GetShadowState()
	})

	// Start TV Manager
	tvManager := tv.NewManager(client, stateManager, logger, readOnly, subscriptionRegistry)
	if err := tvManager.Start(); err != nil {
		logger.Fatal("Failed to start TV Manager", zap.Error(err))
	}
	defer tvManager.Stop()
	logger.Info("TV Manager started successfully")

	// Register Phase 6 read-heavy plugin shadow state providers
	shadowTracker.RegisterPluginProvider("energy", func() shadowstate.PluginShadowState {
		return energyManager.GetShadowState()
	})
	logger.Info("Registered energy shadow state with tracker")

	shadowTracker.RegisterPluginProvider("statetracking", func() shadowstate.PluginShadowState {
		return stateTrackingManager.GetShadowState()
	})
	logger.Info("Registered statetracking shadow state with tracker")

	shadowTracker.RegisterPluginProvider("dayphase", func() shadowstate.PluginShadowState {
		return dayPhaseManager.GetShadowState()
	})
	logger.Info("Registered dayphase shadow state with tracker")

	// Start Reset Coordinator (must be last - after all plugins are started)
	resetCoordinator := reset.NewCoordinator(stateManager, logger, readOnly, []reset.PluginWithName{
		{Name: "State Tracking", Plugin: stateTrackingManager},
		{Name: "Day Phase", Plugin: dayPhaseManager},
		{Name: "Energy", Plugin: energyManager},
		{Name: "Load Shedding", Plugin: loadSheddingManager},
		{Name: "Lighting", Plugin: lightingManager},
		{Name: "Music", Plugin: musicManager},
		{Name: "Security", Plugin: securityManager},
		{Name: "Sleep Hygiene", Plugin: sleepHygieneManager},
	})
	if err := resetCoordinator.Start(); err != nil {
		logger.Fatal("Failed to start Reset Coordinator", zap.Error(err))
	}
	defer resetCoordinator.Stop()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("Application running. Press Ctrl+C to exit.")
	if readOnly {
		logger.Info("Monitoring state changes in READ-ONLY mode...")
	} else {
		logger.Info("Monitoring state changes...")
	}

	// Wait for shutdown signal
	<-sigChan

	logger.Info("Shutting down gracefully...")
}

func displayState(manager *state.Manager, logger *zap.Logger) {
	logger.Info("=== Current State ===")

	// Display booleans
	logger.Info("--- Boolean Variables ---")
	boolVars := []string{
		"isNickHome", "isCarolineHome", "isToriHere",
		"isAnyOwnerHome", "isAnyoneHome", "isAnyoneHomeAndAwake",
		"isMasterAsleep", "isGuestAsleep", "isAnyoneAsleep", "isEveryoneAsleep",
		"isGuestBedroomDoorOpen", "isHaveGuests",
		"isAppleTVPlaying", "isTVPlaying", "isTVon",
		"isFadeOutInProgress", "isFreeEnergyAvailable", "isGridAvailable",
		"isExpectingSomeone",
	}

	for _, key := range boolVars {
		value, err := manager.GetBool(key)
		if err != nil {
			logger.Error("Failed to get bool", zap.String("key", key), zap.Error(err))
			continue
		}
		logger.Info(fmt.Sprintf("  %s: %v", key, value))
	}

	// Display numbers
	logger.Info("--- Number Variables ---")
	numVars := []string{"alarmTime", "remainingSolarGeneration", "thisHourSolarGeneration"}

	for _, key := range numVars {
		value, err := manager.GetNumber(key)
		if err != nil {
			logger.Error("Failed to get number", zap.String("key", key), zap.Error(err))
			continue
		}
		logger.Info(fmt.Sprintf("  %s: %.2f", key, value))
	}

	// Display strings
	logger.Info("--- String Variables ---")
	strVars := []string{
		"dayPhase", "sunevent", "musicPlaybackType",
		"batteryEnergyLevel", "currentEnergyLevel", "solarProductionEnergyLevel",
	}

	for _, key := range strVars {
		value, err := manager.GetString(key)
		if err != nil {
			logger.Error("Failed to get string", zap.String("key", key), zap.Error(err))
			continue
		}
		logger.Info(fmt.Sprintf("  %s: %s", key, value))
	}

	logger.Info("======================")
}

func subscribeToChanges(manager *state.Manager, logger *zap.Logger) {
	// Subscribe to all state variables
	for _, variable := range state.AllVariables {
		key := variable.Key
		manager.Subscribe(key, func(varKey string, oldValue, newValue interface{}) {
			logger.Info("State changed",
				zap.String("key", varKey),
				zap.Any("old", oldValue),
				zap.Any("new", newValue))
		})
	}

	logger.Info("Subscribed to all state change notifications",
		zap.Int("variable_count", len(state.AllVariables)))
}

func startEnergyManager(client ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, configDir string, timezone *time.Location, registry *shadowstate.SubscriptionRegistry) (*energy.Manager, error) {
	// Load energy configuration
	configPath := filepath.Join(configDir, "energy_config.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load energy config: %w", err)
	}

	logger.Info("Loaded energy configuration",
		zap.Int("energy_states", len(energyConfig.Energy.EnergyStates)),
		zap.String("free_energy_start", energyConfig.Energy.FreeEnergyTime.Start),
		zap.String("free_energy_end", energyConfig.Energy.FreeEnergyTime.End))

	// Create and start energy manager
	energyManager := energy.NewManager(client, stateManager, energyConfig, logger, readOnly, timezone, registry)
	if err := energyManager.Start(); err != nil {
		return nil, fmt.Errorf("failed to start energy manager: %w", err)
	}

	return energyManager, nil
}

func startMusicManager(client ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, configDir string) (*music.Manager, error) {
	// Load music configuration
	configPath := filepath.Join(configDir, "music_config.yaml")
	musicConfig, err := music.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load music config: %w", err)
	}

	// Count total music modes
	logger.Info("Loaded music configuration",
		zap.Int("music_modes", len(musicConfig.Music)))

	// Create and start music manager
	musicManager := music.NewManager(client, stateManager, musicConfig, logger, readOnly, nil)
	if err := musicManager.Start(); err != nil {
		return nil, fmt.Errorf("failed to start music manager: %w", err)
	}

	logger.Info("Music Manager started successfully")
	return musicManager, nil
}

func startLightingManager(client ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, configDir string, registry *shadowstate.SubscriptionRegistry) (*lighting.Manager, error) {
	// Load lighting configuration
	configPath := filepath.Join(configDir, "hue_config.yaml")
	lightingConfig, err := lighting.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load lighting config: %w", err)
	}

	logger.Info("Loaded lighting configuration",
		zap.Int("rooms", len(lightingConfig.Rooms)))

	// Create and start lighting manager
	lightingManager := lighting.NewManager(client, stateManager, lightingConfig, logger, readOnly, registry)
	if err := lightingManager.Start(); err != nil {
		return nil, fmt.Errorf("failed to start lighting manager: %w", err)
	}

	logger.Info("Lighting Manager started successfully")
	return lightingManager, nil
}

func startSleepHygieneManager(client ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, configDir string) (*sleephygiene.Manager, error) {
	// Load schedule configuration
	configLoader := config.NewLoader(configDir, logger)
	if err := configLoader.LoadScheduleConfig(); err != nil {
		return nil, fmt.Errorf("failed to load schedule config: %w", err)
	}

	logger.Info("Loaded schedule configuration for Sleep Hygiene")

	// Create and start sleep hygiene manager
	sleepHygieneManager := sleephygiene.NewManager(client, stateManager, configLoader, logger, readOnly, nil)
	if err := sleepHygieneManager.Start(); err != nil {
		return nil, fmt.Errorf("failed to start sleep hygiene manager: %w", err)
	}

	logger.Info("Sleep Hygiene Manager started successfully")
	return sleepHygieneManager, nil
}

func startDayPhaseManager(client ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, configDir string, calculator *dayphaselib.Calculator) (*dayphase.Manager, error) {
	// Load schedule configuration (needed for day phase calculation)
	configLoader := config.NewLoader(configDir, logger)
	if err := configLoader.LoadScheduleConfig(); err != nil {
		return nil, fmt.Errorf("failed to load schedule config: %w", err)
	}

	logger.Info("Loaded schedule configuration for Day Phase")

	// Create and start day phase manager
	dayPhaseManager := dayphase.NewManager(client, stateManager, configLoader, calculator, logger, readOnly)
	if err := dayPhaseManager.Start(); err != nil {
		return nil, fmt.Errorf("failed to start day phase manager: %w", err)
	}

	logger.Info("Day Phase Manager started successfully")
	return dayPhaseManager, nil
}
