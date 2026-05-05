package tts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// newTestServer wraps httptest around the Server's mux so we don't need a
// real listening port.
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(":0", "http://placeholder", zaptest.NewLogger(t))
	ts := httptest.NewServer(http.HandlerFunc(s.handleAudio))
	t.Cleanup(ts.Close)
	s.baseURL = ts.URL // serve URLs through httptest
	return s, ts
}

func TestServer_StoreAndServe(t *testing.T) {
	s, _ := newTestServer(t)
	body := []byte("\xFF\xFB\x90mp3-bytes")

	url, err := s.Store(body)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if !strings.HasPrefix(url, s.baseURL+"/audio/") || !strings.HasSuffix(url, ".mp3") {
		t.Errorf("unexpected URL: %s", url)
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("content-type: want audio/mpeg, got %s", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control: want no-store, got %s", got)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(body) {
		t.Errorf("body mismatch: want %d bytes, got %d", len(body), len(got))
	}
}

func TestServer_NotFound(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/audio/nonexistent.mp3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status: want 404, got %d", resp.StatusCode)
	}
}

func TestServer_PathTraversalRejected(t *testing.T) {
	_, ts := newTestServer(t)

	for _, p := range []string{"/audio/../etc.mp3", "/audio/.mp3", "/audio/.mp3"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("path %q: want 404, got %d", p, resp.StatusCode)
		}
	}
}

func TestServer_TTLExpiry(t *testing.T) {
	s, _ := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	s.ttl = 100 * time.Millisecond

	url, err := s.Store([]byte("abc"))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Before expiry: served.
	resp, _ := http.Get(url)
	if resp.StatusCode != 200 {
		t.Errorf("pre-expiry: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Advance the injected clock past TTL.
	s.now = func() time.Time { return now.Add(time.Second) }
	resp, _ = http.Get(url)
	if resp.StatusCode != 404 {
		t.Errorf("post-expiry: want 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestServer_CapEvictsOldest(t *testing.T) {
	s, _ := newTestServer(t)
	// Pin clock so eviction is deterministic.
	now := time.Now()
	tick := 0
	s.now = func() time.Time {
		tick++
		return now.Add(time.Duration(tick) * time.Millisecond)
	}
	s.cap = 3

	urls := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		url, err := s.Store([]byte{byte(i)})
		if err != nil {
			t.Fatalf("Store: %v", err)
		}
		urls = append(urls, url)
	}

	// First two entries should have been evicted.
	for i, url := range urls {
		resp, _ := http.Get(url)
		got := resp.StatusCode
		resp.Body.Close()
		if i < 2 {
			if got != 404 {
				t.Errorf("entry %d: want 404 (evicted), got %d", i, got)
			}
		} else {
			if got != 200 {
				t.Errorf("entry %d: want 200 (kept), got %d", i, got)
			}
		}
	}

	if len(s.items) != 3 {
		t.Errorf("cap: want 3 items kept, got %d", len(s.items))
	}
}
