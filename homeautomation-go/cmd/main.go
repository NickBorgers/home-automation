// Home Automation Go Application
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"homeautomation/internal/api"
	"homeautomation/internal/devserver"
	"homeautomation/internal/ha"
	"homeautomation/internal/logbuffer"
	"homeautomation/internal/plugins/reset"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	pkgha "homeautomation/pkg/ha"
	"homeautomation/pkg/plugin"
	pkgstate "homeautomation/pkg/state"

	// Import plugin packages to trigger init() registrations
	_ "homeautomation/internal/plugins/dayphase"
	_ "homeautomation/internal/plugins/energy"
	_ "homeautomation/internal/plugins/lighting"
	_ "homeautomation/internal/plugins/loadshedding"
	_ "homeautomation/internal/plugins/music"
	_ "homeautomation/internal/plugins/security"
	_ "homeautomation/internal/plugins/sleephygiene"
	_ "homeautomation/internal/plugins/statetracking"
	_ "homeautomation/internal/plugins/tv"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// Create in-memory log buffer for timeline visualization
	// This stores the last 10,000 log events for the /api/timeline/events endpoint
	logBuffer := logbuffer.NewBuffer(logbuffer.DefaultBufferSize)

	// Initialize logger with stdout output (better for docker logs)
	// and ring buffer capture for timeline visualization
	config := zap.NewProductionConfig()
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stdout"}
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// Build the production logger core for stdout
	stdoutCore, err := buildStdoutCore(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Create buffer core for timeline visualization (capture INFO and above)
	bufferCore := logbuffer.NewBufferCore(logBuffer, zapcore.InfoLevel)

	// Combine both cores using zap.NewTee
	logger := zap.New(zapcore.NewTee(stdoutCore, bufferCore))
	defer logger.Sync()

	// Load environment variables from .env file if present
	if err := godotenv.Load(); err != nil {
		logger.Info("No .env file found, using environment variables")
	}

	// Check for development mode
	devMode := os.Getenv("DEV_MODE") == "true"

	var haURL, haToken string
	var readOnly bool
	var devServer *devserver.DevServer

	if devMode {
		logger.Info("Starting in DEVELOPMENT MODE with mock HA server")

		// Start embedded mock HA server
		devServer = devserver.NewDevServer(logger, devserver.DefaultDevPort)
		if err := devServer.Start(); err != nil {
			logger.Fatal("Failed to start development server", zap.Error(err))
		}

		haURL = devServer.GetWebSocketURL()
		haToken = devServer.GetToken()
		readOnly = true // Dev mode is always read-only for safety

		logger.Info("Mock HA server running",
			zap.String("url", haURL),
			zap.Bool("read_only", readOnly))
	} else {
		haURL = os.Getenv("HA_URL")
		haToken = os.Getenv("HA_TOKEN")
		readOnly = os.Getenv("READ_ONLY") == "true"

		if haURL == "" || haToken == "" {
			logger.Fatal("HA_URL and HA_TOKEN environment variables must be set (or use DEV_MODE=true for local UI testing)")
		}
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
	apiServer := api.NewServer(stateManager, shadowTracker, logBuffer, logger, httpPort, timezone)
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

	// Create plugin context with all dependencies
	// Wrap internal types with pkg adapters for the plugin context
	ctx := plugin.NewContext(pkgha.WrapClient(client), pkgstate.WrapManager(stateManager), logger, readOnly, configDir, timezone)
	ctx.Registry = subscriptionRegistry
	ctx.Latitude = latitude
	ctx.Longitude = longitude

	// Create all registered plugins using the plugin registry
	plugins, err := plugin.CreateAll(ctx)
	if err != nil {
		logger.Fatal("Failed to create plugins", zap.Error(err))
	}
	logger.Info("Created plugins from registry", zap.Int("count", len(plugins)))

	// Start all plugins and collect resettable ones
	var resettablePlugins []reset.PluginWithName
	for _, p := range plugins {
		if err := p.Start(); err != nil {
			logger.Fatal("Failed to start plugin", zap.String("plugin", p.Name()), zap.Error(err))
		}
		logger.Info("Plugin started successfully", zap.String("plugin", p.Name()))

		// Register shadow state provider if plugin supports it
		if provider, ok := p.(plugin.ShadowStateProvider); ok {
			name := p.Name()
			shadowTracker.RegisterPluginProvider(name, func() shadowstate.PluginShadowState {
				return provider.GetShadowState()
			})
			logger.Info("Registered shadow state provider", zap.String("plugin", name))
		}

		// Collect resettable plugins for the reset coordinator
		if resettable, ok := p.(plugin.Resettable); ok {
			resettablePlugins = append(resettablePlugins, reset.PluginWithName{
				Name:   p.Name(),
				Plugin: resettable,
			})
		}
	}

	// Defer stopping all plugins in reverse order
	defer func() {
		for i := len(plugins) - 1; i >= 0; i-- {
			plugins[i].Stop()
		}
	}()

	// Start Reset Coordinator (must be last - after all plugins are started)
	resetCoordinator := reset.NewCoordinator(stateManager, logger, readOnly, resettablePlugins)
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

	// Stop dev server if running
	if devServer != nil {
		if err := devServer.Stop(); err != nil {
			logger.Error("Failed to stop dev server", zap.Error(err))
		}
	}
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

// buildStdoutCore creates a zap core that writes to stdout with the given config.
func buildStdoutCore(config zap.Config) (zapcore.Core, error) {
	encoder := zapcore.NewJSONEncoder(config.EncoderConfig)

	// Create stdout sink
	stdoutSink, _, err := zap.Open("stdout")
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout: %w", err)
	}

	return zapcore.NewCore(encoder, stdoutSink, config.Level), nil
}
