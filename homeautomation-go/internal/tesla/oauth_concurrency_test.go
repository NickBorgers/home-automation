package tesla

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tesla invalidates a refresh token as soon as it issues a replacement. If two
// callers refresh at the same time, the second one is left holding a token
// Tesla has already thrown away, and the integration needs a fresh browser
// authorization to recover. So an expired token must cost exactly one refresh
// no matter how many callers ask for it at once.
func TestAccessTokenRefreshesOnceUnderConcurrentCallers(t *testing.T) {
	server := newTokenServer(t)
	auth, store := newTestAuthenticator(t, server.URL)

	require.NoError(t, store.Save(&Tokens{
		AccessToken:  "expired",
		RefreshToken: "refresh-0",
		Expiry:       time.Now().Add(-time.Minute),
	}))

	// Hold the response open so every caller is inside AccessToken while the
	// first refresh is still in flight. Without this the first caller finishes
	// before the others start, and the test proves nothing.
	server.delay = 100 * time.Millisecond

	const callers = 16
	tokens := make([]string, callers)

	var ready, done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	start := make(chan struct{})

	for i := 0; i < callers; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start // release every caller at once
			token, err := auth.AccessToken(context.Background())
			assert.NoError(t, err)
			tokens[i] = token
		}(i)
	}

	ready.Wait()
	close(start)
	done.Wait()

	assert.Equal(t, 1, server.Requests(), "a single expired token must cost exactly one refresh")
	for i, token := range tokens {
		assert.Equal(t, "access-1", token, "caller %d got a different token", i)
	}
}

// Authorized() and AccessToken() take the same lock. Calling them together must
// not deadlock, which is the failure mode when a locked helper is called from a
// method that already holds the lock.
func TestAuthorizedAndAccessTokenAreSafeTogether(t *testing.T) {
	server := newTokenServer(t)
	auth, store := newTestAuthenticator(t, server.URL)

	require.NoError(t, store.Save(&Tokens{
		AccessToken:  "expired",
		RefreshToken: "refresh-0",
		Expiry:       time.Now().Add(-time.Minute),
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			auth.Authorized()
			_, _ = auth.AccessToken(context.Background())
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Authorized and AccessToken deadlocked")
	}
}

// A 5xx from the token endpoint is worth one more try. Tesla does not bill the
// auth endpoint, so the retry is free.
func TestPostTokenRetriesServerError(t *testing.T) {
	server := newTokenServer(t)
	server.statuses = []int{http.StatusBadGateway}
	auth, _ := newTestAuthenticator(t, server.URL)

	require.NoError(t, auth.Exchange(context.Background(), "the-code"))

	assert.Equal(t, 2, server.Requests(), "a 502 should be retried once")
	assert.True(t, auth.Authorized())
}

// A 400 means Tesla understood the request and rejected it. Asking again would
// get the same answer.
func TestPostTokenDoesNotRetryClientError(t *testing.T) {
	server := newTokenServer(t)
	server.status = http.StatusBadRequest
	auth, _ := newTestAuthenticator(t, server.URL)

	err := auth.Exchange(context.Background(), "the-code")

	require.Error(t, err)
	assert.Equal(t, 1, server.Requests(), "a 400 must not be retried")
}

// Two 5xx in a row exhaust the budget. The retry must stop rather than loop.
func TestPostTokenGivesUpAfterOneRetry(t *testing.T) {
	server := newTokenServer(t)
	server.status = http.StatusServiceUnavailable
	auth, _ := newTestAuthenticator(t, server.URL)

	err := auth.Exchange(context.Background(), "the-code")

	require.Error(t, err)
	assert.Equal(t, maxAttempts, server.Requests())
}

// A cancelled context must not spend a retry.
func TestPostTokenStopsOnCancelledContext(t *testing.T) {
	server := newTokenServer(t)
	server.status = http.StatusBadGateway
	auth, _ := newTestAuthenticator(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := auth.Exchange(ctx, "the-code")

	require.Error(t, err)
	assert.LessOrEqual(t, server.Requests(), 1, "a cancelled context must not be retried")
}

// transport() decides whether a Fleet API failure is worth repeating. A
// cancelled context never is.
func TestTransportIgnoresCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.False(t, transport(ctx, context.Canceled))
	assert.False(t, transport(context.Background(), nil))
}
