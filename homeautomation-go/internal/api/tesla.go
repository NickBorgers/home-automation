package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	teslaapi "homeautomation/internal/tesla"

	"go.uber.org/zap"
)

// SetTeslaAuthenticator wires the Tesla OAuth handler into the API server.
// It is called at startup only when the Tesla plugin found credentials; without
// it the Tesla endpoints answer 503.
func (s *Server) SetTeslaAuthenticator(auth TeslaAuthenticator) {
	s.teslaMu.Lock()
	defer s.teslaMu.Unlock()
	s.teslaAuth = auth
}

func (s *Server) teslaAuthenticator() TeslaAuthenticator {
	s.teslaMu.Lock()
	defer s.teslaMu.Unlock()
	return s.teslaAuth
}

// handleGetTeslaShadowState returns the shadow state for the Tesla plugin
func (s *Server) handleGetTeslaShadowState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := s.shadowTracker.GetPluginState("tesla")
	if !ok {
		http.Error(w, "Tesla shadow state not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := s.writeJSONWithLocalTimestamps(w, state); err != nil {
		s.logger.Error("Failed to encode shadow state response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Tesla shadow state request served",
		zap.String("remote_addr", r.RemoteAddr))
}

// handleTeslaLogin starts the Tesla OAuth flow by redirecting the browser to
// Tesla. This endpoint is reachable on the tailnet only; Tesla itself never
// calls it.
func (s *Server) handleTeslaLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := s.teslaAuthenticator()
	if auth == nil {
		http.Error(w, "Tesla Fleet API is not configured", http.StatusServiceUnavailable)
		return
	}

	state, err := newOAuthState()
	if err != nil {
		s.logger.Error("Failed to generate Tesla OAuth state", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	s.teslaMu.Lock()
	s.oauthState = state
	s.oauthStateExp = time.Now().Add(oauthStateTTL)
	s.teslaMu.Unlock()

	s.logger.Info("Starting Tesla authorization", zap.String("remote_addr", r.RemoteAddr))
	http.Redirect(w, r, auth.AuthorizeURL(state), http.StatusFound)
}

// handleTeslaCallback finishes the OAuth flow. Tesla redirects the browser here
// with a one-time code, which is exchanged for tokens and written to disk.
func (s *Server) handleTeslaCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := s.teslaAuthenticator()
	if auth == nil {
		http.Error(w, "Tesla Fleet API is not configured", http.StatusServiceUnavailable)
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		s.logger.Warn("Tesla authorization was refused", zap.String("error", errParam))
		http.Error(w, "Tesla refused the authorization: "+errParam, http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	if !s.consumeOAuthState(r.URL.Query().Get("state")) {
		// Either the flow was never started here, it expired, or the state does
		// not match. All three mean this callback is not ours to trust.
		s.logger.Warn("Rejected Tesla callback with an unrecognized state",
			zap.String("remote_addr", r.RemoteAddr))
		http.Error(w, "Unrecognized or expired authorization attempt. Start again at /api/tesla/login.", http.StatusBadRequest)
		return
	}

	if err := auth.Exchange(r.Context(), code); err != nil {
		// The code itself is never logged: it is a credential.
		s.logger.Error("Tesla token exchange failed", zap.Error(err))
		http.Error(w, "Token exchange failed. Check the service logs.", http.StatusBadGateway)
		return
	}

	s.logger.Info("Tesla authorization complete")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "Tesla authorization complete. You can close this tab.")
}

// consumeOAuthState checks the returned state against the pending one. A state
// is cleared only when it matches, or when it has expired.
//
// Clearing on every call would let one stray request to /api/tesla/callback —
// a missing state parameter, a stale bookmark, a link prefetch — throw away a
// login the owner had just started, forcing them to begin again. A mismatch
// cannot be brute-forced past the 24 random bytes and the ten-minute window,
// so leaving the pending state alone costs nothing.
func (s *Server) consumeOAuthState(returned string) bool {
	s.teslaMu.Lock()
	defer s.teslaMu.Unlock()

	pending := s.oauthState
	if pending == "" || returned == "" {
		return false
	}
	if time.Now().After(s.oauthStateExp) {
		s.oauthState = ""
		s.oauthStateExp = time.Time{}
		return false
	}
	if subtle.ConstantTimeCompare([]byte(pending), []byte(returned)) != 1 {
		return false
	}

	s.oauthState = ""
	s.oauthStateExp = time.Time{}
	return true
}

func newOAuthState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate oauth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// TeslaEnergyController is the Powerwall surface this server exposes, and it
// only reads. Changing a Powerwall setting is not offered over HTTP: this
// system decides its own behavior, so a reserve change belongs to an
// automation, not to a request from outside.
//
// There is no telemetry endpoint either. Powerwall data is meant to come from
// the gateways on the LAN, not from Tesla's cloud.
type TeslaEnergyController interface {
	// EnergySites lists the Powerwall systems on the account, so a site id can
	// be looked up when configuring an automation.
	EnergySites(ctx context.Context) ([]teslaapi.EnergySite, error)
}

// SetTeslaEnergyController wires the Powerwall site lookup into the API server.
func (s *Server) SetTeslaEnergyController(controller TeslaEnergyController) {
	s.teslaMu.Lock()
	defer s.teslaMu.Unlock()
	s.teslaEnergy = controller
}

func (s *Server) teslaEnergyController() TeslaEnergyController {
	s.teslaMu.Lock()
	defer s.teslaMu.Unlock()
	return s.teslaEnergy
}

type energySiteResponse struct {
	SiteID int64  `json:"site_id"`
	Name   string `json:"site_name"`
}

// handleTeslaEnergySites lists the Powerwall systems on the account.
//
//	GET /api/tesla/energy/sites
//
// It answers a question shadow state cannot: which systems exist on the
// account. Shadow state reports what the plugin already knows, so a live
// lookup belongs here instead. It costs one billed Fleet API request, so call
// it when you need a site id, not on a timer.
func (s *Server) handleTeslaEnergySites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	controller := s.teslaEnergyController()
	if controller == nil {
		http.Error(w, "Tesla energy control is not configured", http.StatusServiceUnavailable)
		return
	}

	sites, err := controller.EnergySites(r.Context())
	if err != nil {
		s.logger.Error("Listing Powerwall systems failed", zap.Error(err))
		writeAPIError(w, http.StatusBadGateway, "Listing Powerwall systems failed: "+err.Error())
		return
	}

	response := make([]energySiteResponse, 0, len(sites))
	for _, site := range sites {
		response = append(response, energySiteResponse{SiteID: site.ID, Name: site.Name})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode Powerwall system list", zap.Error(err))
	}
}
