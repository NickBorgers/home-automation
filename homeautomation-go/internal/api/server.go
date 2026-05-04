package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"homeautomation/internal/alert"
	"homeautomation/internal/ha"
	"homeautomation/internal/logbuffer"
	"homeautomation/internal/notify"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

//go:embed templates/dashboard.html
var dashboardHTML string

//go:embed templates/timeline.html
var timelineHTML string

// Server provides HTTP API endpoints for the home automation system
type Server struct {
	haClient      ha.HAClient
	stateManager  *state.Manager
	shadowTracker *shadowstate.Tracker
	logBuffer     *logbuffer.Buffer
	logger        *zap.Logger
	server        *http.Server
	timezone      *time.Location
	alertNotifier alert.Alerter
}

// NewServer creates a new API server
func NewServer(
	haClient ha.HAClient,
	stateManager *state.Manager,
	shadowTracker *shadowstate.Tracker,
	logBuffer *logbuffer.Buffer,
	logger *zap.Logger,
	port int,
	timezone *time.Location,
	alertNotifier alert.Alerter,
) *Server {
	s := &Server{
		haClient:      haClient,
		stateManager:  stateManager,
		shadowTracker: shadowTracker,
		logBuffer:     logBuffer,
		logger:        logger,
		timezone:      timezone,
		alertNotifier: alertNotifier,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleSitemap)
	mux.HandleFunc("/api/state", s.handleGetState)
	mux.HandleFunc("/api/states", s.handleGetStatesByPlugin)
	mux.HandleFunc("/api/shadow", s.handleGetAllShadowStates)
	mux.HandleFunc("/api/shadow/lighting", s.handleGetLightingShadowState)
	mux.HandleFunc("/api/shadow/music", s.handleGetMusicShadowState)
	mux.HandleFunc("/api/shadow/security", s.handleGetSecurityShadowState)
	mux.HandleFunc("/api/shadow/loadshedding", s.handleGetLoadSheddingShadowState)
	mux.HandleFunc("/api/shadow/sleephygiene", s.handleGetSleepHygieneShadowState)
	mux.HandleFunc("/api/shadow/energy", s.handleGetEnergyShadowState)
	mux.HandleFunc("/api/shadow/statetracking", s.handleGetStateTrackingShadowState)
	mux.HandleFunc("/api/shadow/dayphase", s.handleGetDayPhaseShadowState)
	mux.HandleFunc("/api/shadow/tv", s.handleGetTVShadowState)
	mux.HandleFunc("/api/notify", s.handleNotify)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/timeline", s.handleTimeline)
	mux.HandleFunc("/api/timeline/events", s.handleTimelineEvents)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// StateResponse represents the JSON response for the state endpoint
type StateResponse struct {
	Booleans map[string]bool    `json:"booleans"`
	Numbers  map[string]float64 `json:"numbers"`
	Strings  map[string]string  `json:"strings"`
	JSONs    map[string]any     `json:"jsons"`
}

type notifyRequest struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Urgency  string   `json:"urgency"`
	Tags     []string `json:"tags"`
	Speakers []string `json:"speakers"`
	Priority int      `json:"priority"`
}

type notifyResponse struct {
	Dispatched       bool `json:"dispatched"`
	SuppressedAsleep bool `json:"suppressed_asleep"`
}

// handleGetState returns all state variables as JSON
func (s *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := StateResponse{
		Booleans: make(map[string]bool),
		Numbers:  make(map[string]float64),
		Strings:  make(map[string]string),
		JSONs:    make(map[string]any),
	}

	// Collect all state variables by type
	for _, variable := range state.AllVariables {
		switch variable.Type {
		case state.TypeBool:
			value, err := s.stateManager.GetBool(variable.Key)
			if err != nil {
				s.logger.Error("Failed to get boolean variable",
					zap.String("key", variable.Key),
					zap.Error(err))
				continue
			}
			response.Booleans[variable.Key] = value

		case state.TypeNumber:
			value, err := s.stateManager.GetNumber(variable.Key)
			if err != nil {
				s.logger.Error("Failed to get number variable",
					zap.String("key", variable.Key),
					zap.Error(err))
				continue
			}
			response.Numbers[variable.Key] = value

		case state.TypeString:
			value, err := s.stateManager.GetString(variable.Key)
			if err != nil {
				s.logger.Error("Failed to get string variable",
					zap.String("key", variable.Key),
					zap.Error(err))
				continue
			}
			response.Strings[variable.Key] = value

		case state.TypeJSON:
			var value map[string]interface{}
			if err := s.stateManager.GetJSON(variable.Key, &value); err != nil {
				s.logger.Error("Failed to get JSON variable",
					zap.String("key", variable.Key),
					zap.Error(err))
				continue
			}
			response.JSONs[variable.Key] = value
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("State request served",
		zap.String("remote_addr", r.RemoteAddr))
}

func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req notifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	if strings.TrimSpace(req.Body) == "" {
		writeAPIError(w, http.StatusBadRequest, "body field is required")
		return
	}
	if utf8.RuneCountInString(req.Body) > 500 {
		writeAPIError(w, http.StatusBadRequest, "body must be 500 characters or fewer")
		return
	}
	if len(req.Tags) > 16 {
		writeAPIError(w, http.StatusBadRequest, "tags must contain 16 items or fewer")
		return
	}
	if len(req.Speakers) > 16 {
		writeAPIError(w, http.StatusBadRequest, "speakers must contain 16 items or fewer")
		return
	}
	if req.Priority != 0 && (req.Priority < 1 || req.Priority > 5) {
		writeAPIError(w, http.StatusBadRequest, "priority must be between 1 and 5")
		return
	}

	urgency := notify.UrgencyDeferable
	switch req.Urgency {
	case "", "deferable":
	case "urgent":
		urgency = notify.UrgencyUrgent
	default:
		writeAPIError(w, http.StatusBadRequest, "urgency must be deferable or urgent")
		return
	}

	err := s.alertNotifier.Send(r.Context(), alert.Alert{
		Title:    req.Title,
		Body:     req.Body,
		Urgency:  urgency,
		Tags:     req.Tags,
		Speakers: req.Speakers,
		Priority: req.Priority,
	})
	suppressedAsleep := false
	if errors.Is(err, notify.ErrSuppressedAsleep) {
		suppressedAsleep = true
	} else if err != nil {
		s.logger.Error("notify request dispatch failed", zap.Error(err))
		writeAPIError(w, http.StatusInternalServerError, "failed to dispatch notification")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(notifyResponse{Dispatched: true, SuppressedAsleep: suppressedAsleep}); err != nil {
		s.logger.Error("Failed to encode notify response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Info("notify request dispatched",
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("title", req.Title),
		zap.Bool("suppressed_asleep", suppressedAsleep))
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// PluginMetadata describes which state variables a plugin uses
type PluginMetadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Reads       []string `json:"reads"`
	Writes      []string `json:"writes"`
}

// PluginStateValue represents a state variable value with type information
type PluginStateValue struct {
	Value interface{} `json:"value"`
	Type  string      `json:"type"`
}

// PluginStatesResponse represents the response for /api/states endpoint
type PluginStatesResponse struct {
	Plugins map[string]map[string]PluginStateValue `json:"plugins"`
}

// pluginRegistry defines which state variables each plugin reads/writes
var pluginRegistry = []PluginMetadata{
	{
		Name:        "statetracking",
		Description: "Tracks presence and sleep states, computes derived states",
		Reads:       []string{"isNickHome", "isCarolineHome", "isAssistantHere"},
		Writes:      []string{"isAnyOwnerHome", "isAnyoneHome", "isAnyoneAsleep", "isEveryoneAsleep", "isMasterAsleep", "isGuestAsleep", "didOwnerJustReturnHome"},
	},
	{
		Name:        "dayphase",
		Description: "Tracks time of day and sun position",
		Reads:       []string{},
		Writes:      []string{"dayPhase", "sunevent"},
	},
	{
		Name:        "music",
		Description: "Manages music playback mode and Sonos control",
		Reads:       []string{"dayPhase", "isAnyoneAsleep", "isAnyoneHome", "musicPlaybackType"},
		Writes:      []string{"musicPlaybackType", "currentlyPlayingMusicUri"},
	},
	{
		Name:        "lighting",
		Description: "Controls lighting scenes based on time, presence, and activity",
		Reads:       []string{"dayPhase", "sunevent", "isAnyoneHome", "isTVPlaying", "isEveryoneAsleep", "isMasterAsleep", "isHaveGuests"},
		Writes:      []string{},
	},
	{
		Name:        "tv",
		Description: "Monitors TV and Apple TV playback state",
		Reads:       []string{"isAppleTVPlaying"},
		Writes:      []string{"isAppleTVPlaying", "isTVon", "isTVPlaying"},
	},
	{
		Name:        "energy",
		Description: "Monitors battery, solar production, and grid availability",
		Reads:       []string{"isGridAvailable", "batteryEnergyLevel", "solarProductionEnergyLevel", "isFreeEnergyAvailable"},
		Writes:      []string{"batteryEnergyLevel", "thisHourSolarGeneration", "remainingSolarGeneration", "solarProductionEnergyLevel", "currentEnergyLevel", "isFreeEnergyAvailable"},
	},
	{
		Name:        "loadshedding",
		Description: "Controls thermostat based on available energy",
		Reads:       []string{"currentEnergyLevel"},
		Writes:      []string{},
	},
	{
		Name:        "sleephygiene",
		Description: "Manages wake-up sequences and bedtime routines",
		Reads:       []string{"alarmTime"},
		Writes:      []string{"isFadeOutInProgress", "currentlyPlayingMusic", "musicPlaybackType"},
	},
	{
		Name:        "security",
		Description: "Manages security automation based on presence and sleep",
		Reads:       []string{"isEveryoneAsleep", "isAnyoneHome", "didOwnerJustReturnHome", "isExpectingSomeone"},
		Writes:      []string{},
	},
	{
		Name:        "reset",
		Description: "Coordinates system-wide state resets",
		Reads:       []string{"reset"},
		Writes:      []string{},
	},
}

// handleGetStatesByPlugin returns state variables grouped by which plugins use them
func (s *Server) handleGetStatesByPlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := PluginStatesResponse{
		Plugins: make(map[string]map[string]PluginStateValue),
	}

	// For each plugin, collect the state variables it uses
	for _, plugin := range pluginRegistry {
		pluginStates := make(map[string]PluginStateValue)

		// Collect all unique variables (both reads and writes)
		variableSet := make(map[string]bool)
		for _, v := range plugin.Reads {
			variableSet[v] = true
		}
		for _, v := range plugin.Writes {
			variableSet[v] = true
		}

		// Get the current value of each variable
		for varName := range variableSet {
			value, varType := s.getStateVariableValue(varName)
			if varType != "" {
				pluginStates[varName] = PluginStateValue{
					Value: value,
					Type:  varType,
				}
			}
		}

		response.Plugins[plugin.Name] = pluginStates
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("States by plugin request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// getStateVariableValue retrieves a state variable's value and type
func (s *Server) getStateVariableValue(key string) (interface{}, string) {
	// Find the variable definition to determine its type
	for _, variable := range state.AllVariables {
		if variable.Key != key {
			continue
		}

		switch variable.Type {
		case state.TypeBool:
			value, err := s.stateManager.GetBool(key)
			if err != nil {
				s.logger.Error("Failed to get boolean variable",
					zap.String("key", key),
					zap.Error(err))
				return nil, ""
			}
			return value, "boolean"

		case state.TypeNumber:
			value, err := s.stateManager.GetNumber(key)
			if err != nil {
				s.logger.Error("Failed to get number variable",
					zap.String("key", key),
					zap.Error(err))
				return nil, ""
			}
			return value, "number"

		case state.TypeString:
			value, err := s.stateManager.GetString(key)
			if err != nil {
				s.logger.Error("Failed to get string variable",
					zap.String("key", key),
					zap.Error(err))
				return nil, ""
			}
			return value, "string"

		case state.TypeJSON:
			var value map[string]interface{}
			if err := s.stateManager.GetJSON(key, &value); err != nil {
				s.logger.Error("Failed to get JSON variable",
					zap.String("key", key),
					zap.Error(err))
				return nil, ""
			}
			return value, "json"
		}
	}

	s.logger.Warn("Unknown state variable requested", zap.String("key", key))
	return nil, ""
}

// handleHealth returns a simple health check response
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	healthy := s.haClient.IsHealthy()
	status := "ok"
	httpCode := http.StatusOK

	if !healthy {
		status = "unhealthy"
		httpCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       status,
		"ha_connected": s.haClient.IsConnected(),
	})
}

// Endpoint represents an API endpoint with its documentation
type Endpoint struct {
	Path        string `json:"path"`
	Method      string `json:"method"`
	Description string `json:"description"`
}

// handleSitemap returns a list of all available API endpoints
func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	// Only handle requests to the root path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	endpoints := []Endpoint{
		{
			Path:        "/",
			Method:      "GET",
			Description: "This sitemap - lists all available API endpoints",
		},
		{
			Path:        "/api/state",
			Method:      "GET",
			Description: "Get all state variables grouped by type (booleans, numbers, strings, jsons)",
		},
		{
			Path:        "/api/states",
			Method:      "GET",
			Description: "Get state variables grouped by plugin - shows which plugins use which variables",
		},
		{
			Path:        "/api/shadow",
			Method:      "GET",
			Description: "Get shadow state for all plugins - shows current inputs, inputs at last action, and outputs",
		},
		{
			Path:        "/api/shadow/lighting",
			Method:      "GET",
			Description: "Get shadow state for lighting plugin - shows room states and lighting decisions",
		},
		{
			Path:        "/api/shadow/music",
			Method:      "GET",
			Description: "Get shadow state for music plugin - shows current mode, playlist, speaker group, and playback state",
		},
		{
			Path:        "/api/shadow/security",
			Method:      "GET",
			Description: "Get shadow state for security plugin - shows lockdown status, doorbell events, and garage actions",
		},
		{
			Path:        "/api/shadow/loadshedding",
			Method:      "GET",
			Description: "Get shadow state for load shedding plugin - shows load shedding status, thermostat settings, and energy-based decisions",
		},
		{
			Path:        "/api/shadow/sleephygiene",
			Method:      "GET",
			Description: "Get shadow state for sleep hygiene plugin - shows wake sequence status, fade-out progress, TTS announcements, and reminders",
		},
		{
			Path:        "/api/shadow/energy",
			Method:      "GET",
			Description: "Get shadow state for energy plugin - shows sensor readings, battery/solar/overall energy levels, and computation timestamps",
		},
		{
			Path:        "/api/shadow/statetracking",
			Method:      "GET",
			Description: "Get shadow state for state tracking plugin - shows derived presence/sleep states, timer states, and arrival announcements",
		},
		{
			Path:        "/api/shadow/dayphase",
			Method:      "GET",
			Description: "Get shadow state for day phase plugin - shows sun event, day phase, and calculation timestamps",
		},
		{
			Path:        "/api/shadow/tv",
			Method:      "GET",
			Description: "Get shadow state for TV plugin - shows Apple TV state, TV power, HDMI input, and playback status",
		},
		{
			Path:        "/health",
			Method:      "GET",
			Description: "Health check endpoint - returns {\"status\": \"ok\"}",
		},
		{
			Path:        "/dashboard",
			Method:      "GET",
			Description: "Shadow State Dashboard - web UI to visualize plugin states",
		},
		{
			Path:        "/timeline",
			Method:      "GET",
			Description: "State Timeline - visualize state changes over time for debugging",
		},
		{
			Path:        "/api/timeline/events",
			Method:      "GET",
			Description: "Get recent log events from in-memory buffer - query params: since (ISO8601), limit (int, default 1000)",
		},
		{
			Path:        "/api/notify",
			Method:      "POST",
			Description: "Dispatch an arbitrary alert to push notification and TTS channels",
		},
	}

	// Determine if the request is from a browser (check Accept header)
	acceptHeader := r.Header.Get("Accept")
	preferHTML := false
	if acceptHeader != "" {
		// Simple check - if Accept contains text/html, prefer HTML
		for _, part := range []string{"text/html", "*/*"} {
			if len(acceptHeader) > 0 && (acceptHeader == part || len(acceptHeader) > len(part) && acceptHeader[:len(part)] == part) {
				preferHTML = true
				break
			}
		}
	}

	// Return 404 status code (for automation compatibility) but with helpful body
	w.WriteHeader(http.StatusNotFound)

	if preferHTML {
		// HTML format for browsers
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>Home Automation API</title>
    <style>
        body { font-family: monospace; margin: 40px; background: #1e1e1e; color: #d4d4d4; }
        h1 { color: #4ec9b0; }
        h2 { color: #569cd6; margin-top: 30px; }
        .endpoint { background: #2d2d2d; padding: 15px; margin: 10px 0; border-left: 3px solid #007acc; }
        .method { color: #4ec9b0; font-weight: bold; }
        .path { color: #ce9178; }
        a.path { color: #ce9178; text-decoration: none; }
        a.path:hover { text-decoration: underline; }
        .description { color: #9cdcfe; margin-top: 5px; }
        a { color: #569cd6; text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1>Home Automation API</h1>
    <p>Welcome! This API provides access to the home automation system state.</p>
    <h2>Available Endpoints</h2>
`)
		for _, ep := range endpoints {
			fmt.Fprintf(w, `    <div class="endpoint">
        <div><span class="method">%s</span> <a href="%s" class="path">%s</a></div>
        <div class="description">%s</div>
    </div>
`, ep.Method, ep.Path, ep.Path, ep.Description)
		}
		fmt.Fprintf(w, `    <h2>Examples</h2>
    <div class="endpoint">
        <div>Get all state variables (by type):</div>
        <div class="description">curl <a href="/api/state">http://localhost:8081/api/state</a></div>
    </div>
    <div class="endpoint">
        <div>Get state variables by plugin:</div>
        <div class="description">curl <a href="/api/states">http://localhost:8081/api/states</a></div>
    </div>
    <div class="endpoint">
        <div>Health check:</div>
        <div class="description">curl <a href="/health">http://localhost:8081/health</a></div>
    </div>
</body>
</html>
`)
	} else {
		// Plain text format for terminal
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "Home Automation API\n")
		fmt.Fprintf(w, "===================\n\n")
		fmt.Fprintf(w, "Available endpoints:\n\n")
		for _, ep := range endpoints {
			fmt.Fprintf(w, "  %-10s %-20s %s\n", ep.Method, ep.Path, ep.Description)
		}
		fmt.Fprintf(w, "\nExamples:\n\n")
		fmt.Fprintf(w, "  Get all state variables (by type):\n")
		fmt.Fprintf(w, "    curl http://localhost:8081/api/state\n\n")
		fmt.Fprintf(w, "  Get state variables by plugin:\n")
		fmt.Fprintf(w, "    curl http://localhost:8081/api/states\n\n")
		fmt.Fprintf(w, "  Health check:\n")
		fmt.Fprintf(w, "    curl http://localhost:8081/health\n\n")
		fmt.Fprintf(w, "  Pretty print JSON:\n")
		fmt.Fprintf(w, "    curl http://localhost:8081/api/states | jq\n\n")
	}

	s.logger.Debug("Sitemap request served",
		zap.String("remote_addr", r.RemoteAddr),
		zap.Bool("html_format", preferHTML))
}

// handleGetLightingShadowState returns the shadow state for the lighting plugin
func (s *Server) handleGetLightingShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("lighting")
	if !ok {
		http.Error(w, "Lighting shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Lighting shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetMusicShadowState returns the music plugin shadow state
func (s *Server) handleGetMusicShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("music")
	if !ok {
		http.Error(w, "Music shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Music shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetSecurityShadowState returns the shadow state for the security plugin
func (s *Server) handleGetSecurityShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("security")
	if !ok {
		http.Error(w, "Security shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Security shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetLoadSheddingShadowState returns the shadow state for the load shedding plugin
func (s *Server) handleGetLoadSheddingShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("loadshedding")
	if !ok {
		http.Error(w, "Load shedding shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Load shedding shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetSleepHygieneShadowState returns the sleep hygiene plugin shadow state
func (s *Server) handleGetSleepHygieneShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("sleephygiene")
	if !ok {
		http.Error(w, "Sleep hygiene shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Sleep hygiene shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetEnergyShadowState returns the energy plugin shadow state
func (s *Server) handleGetEnergyShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("energy")
	if !ok {
		http.Error(w, "Energy shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Energy shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetStateTrackingShadowState returns the state tracking plugin shadow state
func (s *Server) handleGetStateTrackingShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("statetracking")
	if !ok {
		http.Error(w, "State tracking shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("State tracking shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetDayPhaseShadowState returns the day phase plugin shadow state
func (s *Server) handleGetDayPhaseShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("dayphase")
	if !ok {
		http.Error(w, "Day phase shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Day phase shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleGetTVShadowState returns the TV plugin shadow state
func (s *Server) handleGetTVShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("tv")
	if !ok {
		http.Error(w, "TV shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("TV shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// AllShadowStatesResponse represents the response for /api/shadow endpoint
type AllShadowStatesResponse struct {
	Plugins  map[string]interface{} `json:"plugins"`
	Metadata ShadowMetadata         `json:"metadata"`
}

// ShadowMetadata contains metadata about the shadow state response
type ShadowMetadata struct {
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"version"`
}

// handleGetAllShadowStates returns shadow states for all plugins
func (s *Server) handleGetAllShadowStates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allStates := s.shadowTracker.GetAllPluginStates()

	// Convert to map[string]interface{} for JSON encoding
	pluginsData := make(map[string]interface{})
	for name, state := range allStates {
		pluginsData[name] = state
	}

	response := AllShadowStatesResponse{
		Plugins: pluginsData,
		Metadata: ShadowMetadata{
			Timestamp: time.Now(),
			Version:   "1.0.0",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, response); err != nil {
		s.logger.Error("Failed to encode shadow states response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("All shadow states request served",
		zap.String("remote_addr", r.RemoteAddr),
		zap.Int("plugin_count", len(allStates)))
}

// Start begins serving HTTP requests
func (s *Server) Start() error {
	s.logger.Info("Starting HTTP API server", zap.String("addr", s.server.Addr))

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTP server
func (s *Server) Stop() error {
	s.logger.Info("Stopping HTTP API server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	return nil
}

// localTimeFormat is the human-readable format for local timestamps
const localTimeFormat = "Jan 2, 2006 3:04:05 PM MST"

// addLocalTimestamps recursively walks a JSON-compatible structure and adds
// "*Local" fields for any timestamp fields, formatted in the configured timezone.
// It returns a new structure with the local time fields added.
func (s *Server) addLocalTimestamps(data interface{}) interface{} {
	if s.timezone == nil {
		return data
	}

	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		// First pass: copy all existing keys
		for key, val := range v {
			result[key] = s.addLocalTimestamps(val)
		}
		// Second pass: add Local versions for timestamp strings
		for key, val := range v {
			if strVal, ok := val.(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, strVal); err == nil {
					localKey := key + "Local"
					result[localKey] = t.In(s.timezone).Format(localTimeFormat)
				} else if t, err := time.Parse(time.RFC3339, strVal); err == nil {
					localKey := key + "Local"
					result[localKey] = t.In(s.timezone).Format(localTimeFormat)
				}
			}
		}
		return result

	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = s.addLocalTimestamps(val)
		}
		return result

	default:
		return data
	}
}

// writeJSONWithLocalTimestamps encodes the given data as JSON, adding local
// timestamp fields for any RFC3339 timestamps found in the structure.
func (s *Server) writeJSONWithLocalTimestamps(w http.ResponseWriter, data interface{}) error {
	// First marshal to JSON, then unmarshal to map so we can transform
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	var genericData interface{}
	if err := json.Unmarshal(jsonBytes, &genericData); err != nil {
		return err
	}

	// Add local timestamps
	transformed := s.addLocalTimestamps(genericData)

	// Encode the transformed data
	return json.NewEncoder(w).Encode(transformed)
}

// handleDashboard serves a web UI for visualizing shadow state
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, dashboardHTML)

	s.logger.Debug("Dashboard request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleTimeline serves a web UI for visualizing state changes over time
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, timelineHTML)

	s.logger.Debug("Timeline request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// TimelineEventsResponse represents the JSON response for the timeline events endpoint
type TimelineEventsResponse struct {
	Events   []logbuffer.Event `json:"events"`
	Count    int               `json:"count"`
	Capacity int               `json:"capacity"`
	Overflow bool              `json:"overflow"`
}

// handleTimelineEvents returns recent log events from the in-memory ring buffer
// Query parameters:
//   - since: ISO8601 timestamp to filter events after (optional)
//   - limit: maximum number of events to return (optional, default 1000)
func (s *Server) handleTimelineEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		parsed, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid 'since' parameter: %v", err), http.StatusBadRequest)
			return
		}
		since = parsed
	}

	limit := 1000 // default limit
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil || parsed < 0 {
			http.Error(w, "Invalid 'limit' parameter: must be a positive integer", http.StatusBadRequest)
			return
		}
		if parsed > 0 {
			limit = parsed
		}
	}

	// Get events from buffer
	events := s.logBuffer.GetEvents(since, limit)

	response := TimelineEventsResponse{
		Events:   events,
		Count:    s.logBuffer.Count(),
		Capacity: s.logBuffer.Capacity(),
		Overflow: s.logBuffer.HasOverflowed(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode timeline events response", zap.Error(err))
	}

	s.logger.Debug("Timeline events request served",
		zap.String("remote_addr", r.RemoteAddr),
		zap.Int("events_returned", len(events)),
		zap.Int("limit", limit))
}
