package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTeslaAuth records what the handlers asked it to do.
type stubTeslaAuth struct {
	lastState   string
	lastCode    string
	exchanges   int
	exchangeErr error
	authorized  bool
}

func (s *stubTeslaAuth) AuthorizeURL(state string) string {
	s.lastState = state
	return "https://auth.tesla.com/oauth2/v3/authorize?state=" + url.QueryEscape(state)
}

func (s *stubTeslaAuth) Exchange(_ context.Context, code string) error {
	s.exchanges++
	s.lastCode = code
	if s.exchangeErr != nil {
		return s.exchangeErr
	}
	s.authorized = true
	return nil
}

func (s *stubTeslaAuth) Authorized() bool { return s.authorized }

// startLogin runs the login endpoint and returns the state Tesla would echo back.
func startLogin(t *testing.T, server *Server) string {
	t.Helper()

	recorder := httptest.NewRecorder()
	server.handleTeslaLogin(recorder, httptest.NewRequest(http.MethodGet, "/api/tesla/login", nil))
	require.Equal(t, http.StatusFound, recorder.Code)

	location, err := url.Parse(recorder.Header().Get("Location"))
	require.NoError(t, err)
	state := location.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

func TestTeslaEndpointsUnavailableWithoutConfiguration(t *testing.T) {
	server := createTestServer(t)

	for _, path := range []string{"/api/tesla/login", "/api/tesla/callback?code=abc&state=xyz"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)

		server.server.Handler.ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code, path)
	}
}

func TestTeslaEndpointsRejectNonGet(t *testing.T) {
	server := createTestServer(t)
	server.SetTeslaAuthenticator(&stubTeslaAuth{})

	for _, path := range []string{"/api/tesla/login", "/api/tesla/callback"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))

		server.server.Handler.ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code, path)
	}
}

func TestTeslaLoginRedirectsToTesla(t *testing.T) {
	server := createTestServer(t)
	auth := &stubTeslaAuth{}
	server.SetTeslaAuthenticator(auth)

	state := startLogin(t, server)

	assert.Equal(t, auth.lastState, state)
}

func TestTeslaCallbackCompletesAuthorization(t *testing.T) {
	server := createTestServer(t)
	auth := &stubTeslaAuth{}
	server.SetTeslaAuthenticator(auth)
	state := startLogin(t, server)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tesla/callback?code=the-code&state="+url.QueryEscape(state), nil)
	server.server.Handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "the-code", auth.lastCode)
	assert.True(t, auth.Authorized())
}

func TestTeslaCallbackRejectsUnknownState(t *testing.T) {
	server := createTestServer(t)
	auth := &stubTeslaAuth{}
	server.SetTeslaAuthenticator(auth)
	startLogin(t, server)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tesla/callback?code=the-code&state=forged", nil)
	server.server.Handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Zero(t, auth.exchanges, "a forged state must never reach the token exchange")
}

func TestTeslaCallbackStateIsSingleUse(t *testing.T) {
	server := createTestServer(t)
	auth := &stubTeslaAuth{}
	server.SetTeslaAuthenticator(auth)
	state := startLogin(t, server)

	target := "/api/tesla/callback?code=the-code&state=" + url.QueryEscape(state)

	first := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, target, nil))
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, target, nil))

	assert.Equal(t, http.StatusBadRequest, second.Code)
	assert.Equal(t, 1, auth.exchanges, "replaying a callback must not exchange twice")
}

func TestTeslaCallbackRequiresCode(t *testing.T) {
	server := createTestServer(t)
	server.SetTeslaAuthenticator(&stubTeslaAuth{})
	state := startLogin(t, server)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tesla/callback?state="+url.QueryEscape(state), nil)
	server.server.Handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeslaCallbackSurfacesTeslaError(t *testing.T) {
	server := createTestServer(t)
	server.SetTeslaAuthenticator(&stubTeslaAuth{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tesla/callback?error=access_denied", nil)
	server.server.Handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "access_denied")
}

func TestTeslaCallbackReportsExchangeFailure(t *testing.T) {
	server := createTestServer(t)
	auth := &stubTeslaAuth{exchangeErr: errors.New("tesla said no")}
	server.SetTeslaAuthenticator(auth)
	state := startLogin(t, server)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tesla/callback?code=the-code&state="+url.QueryEscape(state), nil)
	server.server.Handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "the-code", "the authorization code is a credential and must not be echoed")
}

func TestTeslaShadowStateEndpoint(t *testing.T) {
	server := createTestServer(t)

	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/shadow/tesla", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "vehicleState")
}
