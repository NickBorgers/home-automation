package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/logbuffer"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
)

// Security Test Suite for HTTP Interface
//
// This file contains security-focused tests that verify the HTTP interface
// is truly read-only and cannot be used to modify system state.
//
// Security Properties Verified:
// 1. All endpoints reject non-GET HTTP methods (POST, PUT, DELETE, PATCH)
// 2. No endpoint reads or processes request bodies
// 3. No endpoint modifies internal state
// 4. Query parameters cannot trigger state changes
//
// Related Issue: #324 - Security review of HTTP interfaces

// allEndpoints lists all HTTP endpoints exposed by the API server
var allEndpoints = []string{
	"/",
	"/api/state",
	"/api/states",
	"/api/shadow",
	"/api/shadow/lighting",
	"/api/shadow/music",
	"/api/shadow/security",
	"/api/shadow/loadshedding",
	"/api/shadow/sleephygiene",
	"/api/shadow/energy",
	"/api/shadow/statetracking",
	"/api/shadow/dayphase",
	"/api/shadow/tv",
	"/health",
	"/dashboard",
	"/timeline",
	"/api/timeline/events",
}

// nonGetMethods lists HTTP methods that should be rejected by all endpoints
var nonGetMethods = []string{
	http.MethodPost,
	http.MethodPut,
	http.MethodDelete,
	http.MethodPatch,
	http.MethodConnect,
	http.MethodTrace,
}

// createTestServer creates a fully configured test server with mock dependencies
func createTestServer(t *testing.T) *Server {
	t.Helper()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	mockClient.Connect() // Connect so health endpoint returns 200
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()

	// Register shadow states so endpoints return real data
	shadowTracker.RegisterPlugin("lighting", shadowstate.NewLightingShadowState())
	shadowTracker.RegisterPlugin("music", shadowstate.NewMusicShadowState())
	shadowTracker.RegisterPlugin("security", shadowstate.NewSecurityShadowState())
	shadowTracker.RegisterPlugin("loadshedding", shadowstate.NewLoadSheddingShadowState())
	shadowTracker.RegisterPlugin("sleephygiene", shadowstate.NewSleepHygieneShadowState())
	shadowTracker.RegisterPlugin("energy", shadowstate.NewEnergyShadowState())
	shadowTracker.RegisterPlugin("statetracking", shadowstate.NewStateTrackingShadowState())
	shadowTracker.RegisterPlugin("dayphase", shadowstate.NewDayPhaseShadowState())
	shadowTracker.RegisterPlugin("tv", shadowstate.NewTVShadowState())

	buffer := logbuffer.NewBuffer(100)
	return NewServer(mockClient, stateManager, shadowTracker, buffer, logger, 8080, time.UTC)
}

// TestAllEndpointsRejectNonGetMethods verifies that every endpoint rejects
// all non-GET HTTP methods with 405 Method Not Allowed.
//
// This is the primary security control ensuring the API is read-only.
func TestAllEndpointsRejectNonGetMethods(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	for _, endpoint := range allEndpoints {
		for _, method := range nonGetMethods {
			endpoint := endpoint // capture for parallel
			method := method     // capture for parallel

			t.Run(method+" "+endpoint, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(method, endpoint, nil)
				w := httptest.NewRecorder()
				server.server.Handler.ServeHTTP(w, req)

				if w.Code != http.StatusMethodNotAllowed {
					t.Errorf("%s %s: expected status 405, got %d", method, endpoint, w.Code)
				}
			})
		}
	}
}

// TestAllEndpointsAcceptGet verifies that all endpoints respond successfully to GET requests.
// This ensures our security checks don't accidentally break legitimate read operations.
func TestAllEndpointsAcceptGet(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	for _, endpoint := range allEndpoints {
		endpoint := endpoint // capture for parallel

		t.Run("GET "+endpoint, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, endpoint, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// Allow 200 OK or 404 Not Found (for / endpoint which returns 404 with body)
			// 404 is expected for the root sitemap endpoint
			if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
				t.Errorf("GET %s: expected status 200 or 404, got %d", endpoint, w.Code)
			}
		})
	}
}

// TestRequestBodiesIgnored verifies that request bodies in POST/PUT/DELETE/PATCH
// requests are not processed - endpoints reject based on method before reading body.
func TestRequestBodiesIgnored(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	// Malicious payloads that should never be processed
	maliciousPayloads := []string{
		`{"isNickHome": false}`,
		`{"dayPhase": "hacked"}`,
		`{"command": "rm -rf /"}`,
		`<script>alert('xss')</script>`,
		`; DROP TABLE states; --`,
	}

	for _, endpoint := range allEndpoints {
		for _, payload := range maliciousPayloads {
			endpoint := endpoint // capture for parallel
			payload := payload   // capture for parallel

			t.Run("POST "+endpoint+" with payload", func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				server.server.Handler.ServeHTTP(w, req)

				// Should reject with 405 before processing body
				if w.Code != http.StatusMethodNotAllowed {
					t.Errorf("POST %s with malicious payload: expected 405, got %d", endpoint, w.Code)
				}
			})
		}
	}
}

// TestQueryParametersCannotModifyState verifies that query parameters
// cannot be used to trigger state modifications.
func TestQueryParametersCannotModifyState(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	shadowTracker := shadowstate.NewTracker()
	buffer := logbuffer.NewBuffer(100)
	server := NewServer(mockClient, stateManager, shadowTracker, buffer, logger, 8080, time.UTC)

	// Set initial state values
	stateManager.SetBool("isNickHome", true)
	stateManager.SetString("dayPhase", "morning")

	// Malicious query parameters that attempt to modify state
	maliciousQueries := []string{
		"/api/state?isNickHome=false",
		"/api/state?dayPhase=hacked",
		"/api/state?set=isNickHome&value=false",
		"/api/state?action=modify&key=dayPhase&value=evil",
		"/api/states?update=true&isNickHome=false",
		"/api/shadow?modify=lighting",
		"/api/timeline/events?delete=all",
	}

	// Run requests sequentially to ensure state verification is accurate.
	// Using parallel subtests here would cause a race: the parent test's
	// state verification (below) could run before subtests complete.
	for _, url := range maliciousQueries {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(w, req)

		// Request should succeed or return appropriate error (not 500)
		if w.Code == http.StatusInternalServerError {
			t.Errorf("GET %s: unexpected server error", url)
		}
	}

	// Verify state was not modified
	isNickHome, _ := stateManager.GetBool("isNickHome")
	if !isNickHome {
		t.Error("State isNickHome was unexpectedly modified")
	}

	dayPhase, _ := stateManager.GetString("dayPhase")
	if dayPhase != "morning" {
		t.Errorf("State dayPhase was unexpectedly modified to %s", dayPhase)
	}
}

// TestTimelineEventsQueryParamsValidation verifies that the timeline events
// endpoint properly validates query parameters and cannot be exploited.
func TestTimelineEventsQueryParamsValidation(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"valid limit", "?limit=10", http.StatusOK},
		{"valid since", "?since=2025-01-01T00:00:00Z", http.StatusOK},
		{"negative limit rejected", "?limit=-1", http.StatusBadRequest},
		{"non-numeric limit rejected", "?limit=abc", http.StatusBadRequest},
		{"invalid date rejected", "?since=not-a-date", http.StatusBadRequest},
		{"SQL injection in limit", "?limit=1%3BDROP+TABLE+events", http.StatusBadRequest},
		{"command injection in since", "?since=2025-01-01T00%3A00%3A00Z%3Brm", http.StatusBadRequest},
	}

	for _, tc := range tests {
		tc := tc // capture for parallel

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/timeline/events"+tc.query, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("GET /api/timeline/events%s: expected status %d, got %d",
					tc.query, tc.wantStatus, w.Code)
			}
		})
	}
}

// TestContentTypeNotRequiredForGet verifies that GET requests work without
// Content-Type headers (since no body is processed).
func TestContentTypeNotRequiredForGet(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	for _, endpoint := range allEndpoints {
		endpoint := endpoint // capture for parallel

		t.Run("GET "+endpoint+" without Content-Type", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, endpoint, nil)
			// Explicitly not setting Content-Type
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// Should work fine - GET requests don't need Content-Type
			if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
				t.Errorf("GET %s without Content-Type: expected 200 or 404, got %d",
					endpoint, w.Code)
			}
		})
	}
}

// TestHEADMethodHandling verifies HEAD requests are handled appropriately.
// HEAD should either work like GET (without body) or return 405.
func TestHEADMethodHandling(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	for _, endpoint := range allEndpoints {
		endpoint := endpoint // capture for parallel

		t.Run("HEAD "+endpoint, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodHead, endpoint, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// HEAD should return 405 (our handlers only allow GET)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("HEAD %s: expected status 405, got %d", endpoint, w.Code)
			}
		})
	}
}

// TestOPTIONSMethodHandling verifies OPTIONS requests are handled appropriately.
func TestOPTIONSMethodHandling(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	for _, endpoint := range allEndpoints {
		endpoint := endpoint // capture for parallel

		t.Run("OPTIONS "+endpoint, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodOptions, endpoint, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// OPTIONS should return 405 (our handlers only allow GET)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("OPTIONS %s: expected status 405, got %d", endpoint, w.Code)
			}
		})
	}
}

// TestUnknownEndpointsReturn404 verifies that unknown paths return 404.
func TestUnknownEndpointsReturn404(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	unknownPaths := []string{
		"/api/modify",
		"/api/set",
		"/api/update",
		"/api/delete",
		"/admin",
		"/api/admin/settings",
		"/api/state/set",
		"/api/shadow/set",
		"/config",
	}

	for _, path := range unknownPaths {
		path := path // capture for parallel

		t.Run("GET "+path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("GET %s: expected status 404, got %d", path, w.Code)
			}
		})
	}
}

// TestPathTraversalAttacks verifies that path traversal attacks are neutralized.
// Go's http.ServeMux automatically cleans URLs with path traversal attempts.
func TestPathTraversalAttacks(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	traversalPaths := []string{
		"/../../../etc/passwd",
		"/api/../../../etc/passwd",
		"/api/state/../../../etc/passwd",
		"/%2e%2e/%2e%2e/%2e%2e/etc/passwd",
	}

	for _, path := range traversalPaths {
		path := path // capture for parallel

		t.Run("GET "+path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// Go's ServeMux cleans URLs and may redirect (301) or return 404
			// Both are acceptable - neither allows access to sensitive files
			if w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
				t.Errorf("GET %s: expected status 404 or 301, got %d", path, w.Code)
			}
		})
	}
}

// TestLargeRequestBodyRejection verifies that large request bodies don't cause issues.
// Even though bodies are not processed, we want to ensure no resource exhaustion.
func TestLargeRequestBodyRejection(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	// Create a large body (1MB of data)
	largeBody := strings.Repeat("x", 1024*1024)

	for _, method := range nonGetMethods {
		method := method // capture for parallel

		t.Run(method+" with large body", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(method, "/api/state", strings.NewReader(largeBody))
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// Should reject with 405 before processing body
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s /api/state with large body: expected 405, got %d", method, w.Code)
			}
		})
	}
}

// TestConcurrentRequests verifies the server handles concurrent read requests safely.
func TestConcurrentRequests(t *testing.T) {
	t.Parallel()
	server := createTestServer(t)

	// Run 100 concurrent requests across different endpoints
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			endpoint := allEndpoints[i%len(allEndpoints)]
			req := httptest.NewRequest(http.MethodGet, endpoint, nil)
			w := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(w, req)

			// All requests should complete without errors
			if w.Code == http.StatusInternalServerError {
				t.Errorf("Concurrent GET %s: unexpected server error", endpoint)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
}
