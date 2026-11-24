package bravia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPSK = `p$ss#w"rd 1` // special characters must reach the TV unchanged

func newTestServer(t *testing.T, handle func(req map[string]interface{}) string) (*Client, *[]map[string]interface{}) {
	t.Helper()
	var seen []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sony/video", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, testPSK, r.Header.Get("X-Auth-PSK"))
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &req))
		seen = append(seen, req)
		_, _ = io.WriteString(w, handle(req))
	}))
	t.Cleanup(srv.Close)
	return NewClient(strings.TrimPrefix(srv.URL, "http://"), testPSK), &seen
}

func TestGetBrightness(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		want    Setting
		wantErr string
		off     bool
	}{
		{
			name:  "reads value and range",
			reply: `{"result":[[{"target":"brightness","currentValue":"25","isAvailable":true,"candidate":[{"max":50,"min":0,"step":1}]}]],"id":1}`,
			want:  Setting{Current: 25, Min: 0, Max: 50},
		},
		{
			name:  "screen off",
			reply: `{"error":[40005,"not power-on"],"id":1}`,
			off:   true,
		},
		{
			name:  "screen off, write-style message",
			reply: `{"error":[40005,"Display Is Turned off"],"id":1}`,
			off:   true,
		},
		{
			name:    "not available in picture mode",
			reply:   `{"result":[[{"target":"brightness","currentValue":"25","isAvailable":false,"candidate":[{"max":50,"min":0,"step":1}]}]],"id":1}`,
			wantErr: "not available",
		},
		{
			name:    "bad PSK",
			reply:   `{"error":[403,"Forbidden"],"id":1}`,
			wantErr: "BRAVIA_PSK",
		},
		{
			name:    "no brightness reported",
			reply:   `{"result":[[]],"id":1}`,
			wantErr: "did not report",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, seen := newTestServer(t, func(map[string]interface{}) string { return tt.reply })
			got, err := c.GetBrightness(context.Background())
			require.Len(t, *seen, 1)
			assert.Equal(t, "getPictureQualitySettings", (*seen)[0]["method"])
			assert.Equal(t, "1.0", (*seen)[0]["version"])
			switch {
			case tt.off:
				assert.True(t, errors.Is(err, ErrTurnedOff), "got %v", err)
			case tt.wantErr != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestSetBrightnessSendsStringValue(t *testing.T) {
	c, seen := newTestServer(t, func(map[string]interface{}) string { return `{"result":[],"id":1}` })
	require.NoError(t, c.SetBrightness(context.Background(), 17))
	require.Len(t, *seen, 1)
	req := (*seen)[0]
	assert.Equal(t, "setPictureQualitySettings", req["method"])
	params := req["params"].([]interface{})[0].(map[string]interface{})
	settings := params["settings"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "brightness", settings["target"])
	assert.Equal(t, "17", settings["value"])
}

func TestGetPowerStatus(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		want    PowerStatus
		wantErr string
	}{
		{name: "active", reply: `{"result":[{"status":"active"}],"id":1}`, want: PowerStatusActive},
		{name: "standby", reply: `{"result":[{"status":"standby"}],"id":1}`, want: PowerStatusStandby},
		{name: "authentication failure", reply: `{"error":[403,"Forbidden"],"id":1}`, wantErr: "BRAVIA_PSK"},
		{name: "unknown response", reply: `{"result":[{"status":"unknown"}],"id":1}`, wantErr: "unexpected power status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var method string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/sony/system", r.URL.Path)
				var req map[string]interface{}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				method, _ = req["method"].(string)
				assert.Equal(t, []interface{}{}, req["params"])
				_, _ = io.WriteString(w, tt.reply)
			}))
			defer srv.Close()

			got, err := NewClient(srv.URL, testPSK).GetPowerStatus(context.Background())
			assert.Equal(t, "getPowerStatus", method)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPowerStatusTimeoutIsInconclusiveError(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewClient(srv.URL, testPSK).GetPowerStatus(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestHTTPErrorWithoutJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	err := NewClient(srv.URL, testPSK).SetBrightness(context.Background(), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}
