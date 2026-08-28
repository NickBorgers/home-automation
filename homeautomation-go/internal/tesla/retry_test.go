package tesla

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/teslamotors/vehicle-command/pkg/account"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forgeFleetToken builds a token that account.New will accept. Only the middle
// segment is read, and only for the audience list, so nothing is signed.
func forgeFleetToken(t *testing.T) string {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"aud":     []string{DefaultAudience},
		"ou_code": "NA",
		"sub":     "test-subject",
	})
	require.NoError(t, err)

	return fmt.Sprintf("header.%s.signature", base64.RawStdEncoding.EncodeToString(payload))
}

// The retry on the Fleet API path is only worth having if transport() actually
// recognises what the vehicle-command library returns. That library wraps the
// error from http.Client.Do, so the *url.Error should still be reachable — but
// "should" is not good enough for a path that would otherwise be silently dead.
func TestTransportRecognisesFleetAPIConnectionFailure(t *testing.T) {
	acct, err := account.New(forgeFleetToken(t), UserAgent)
	require.NoError(t, err)

	// Nothing is listening here, so Get fails at the transport layer.
	acct.Host = "127.0.0.1:1"

	_, err = acct.Get(context.Background(), "api/1/vehicles")
	require.Error(t, err)

	var urlErr *url.Error
	assert.True(t, errors.As(err, &urlErr), "the library must keep the *url.Error reachable")
	assert.True(t, transport(context.Background(), err),
		"a Fleet API connection failure must be retryable, or the retry path is dead code")
}

// The other half: an answer from Tesla is not a transport failure, so it must
// not be retried — Tesla has already billed that request.
//
// This cannot be driven through a test server, because account.Get always
// dials https:// and its http.Client is unexported, so a plain test server
// fails at the TLS handshake rather than at the status code. Instead this
// asserts on the exact error the library builds for a non-200, which is a bare
// fmt.Errorf with no wrapped *url.Error:
//
//	err := fmt.Errorf("http error when sending command to %s: %s", url, response.Status)
func TestTransportIgnoresFleetAPIErrorResponse(t *testing.T) {
	answered := fmt.Errorf("http error when sending command to %s: %s",
		"https://fleet-api.prd.na.vn.cloud.tesla.com/api/1/vehicles", "500 Internal Server Error")

	assert.False(t, transport(context.Background(), answered),
		"a response Tesla already billed must not be retried")
}
