package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap/zaptest"
)

func TestClient_Synthesize_Success(t *testing.T) {
	canned := []byte("\xFF\xFB\x90\x00fake-mp3-bytes")
	var gotMethod, gotCT string
	var gotBody kokoroRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(canned)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "af_heart", zaptest.NewLogger(t))
	mp3, err := c.Synthesize(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %s", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type: want application/json, got %s", gotCT)
	}
	if gotBody.Input != "hello world" {
		t.Errorf("input: want 'hello world', got %q", gotBody.Input)
	}
	if gotBody.Voice != "af_heart" {
		t.Errorf("voice: want af_heart, got %q", gotBody.Voice)
	}
	if gotBody.Model != "kokoro" {
		t.Errorf("model: want kokoro, got %q", gotBody.Model)
	}
	if gotBody.ResponseFormat != "mp3" {
		t.Errorf("response_format: want mp3, got %q", gotBody.ResponseFormat)
	}
	if string(mp3) != string(canned) {
		t.Errorf("body: want %d bytes round-tripped, got %d", len(canned), len(mp3))
	}
}

func TestClient_Synthesize_VoiceConfigurable(t *testing.T) {
	var gotVoice string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req kokoroRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		gotVoice = req.Voice
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "bf_emma", zaptest.NewLogger(t))
	if _, err := c.Synthesize(context.Background(), "hi"); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if gotVoice != "bf_emma" {
		t.Errorf("voice: want bf_emma, got %q", gotVoice)
	}
}

func TestClient_Synthesize_Non2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kokoro down"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "af_heart", zaptest.NewLogger(t))
	_, err := c.Synthesize(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should include status: %v", err)
	}
}

func TestClient_Synthesize_EmptyBodyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "af_heart", zaptest.NewLogger(t))
	_, err := c.Synthesize(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error on empty body")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty body: %v", err)
	}
}

func TestClient_Synthesize_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "af_heart", zaptest.NewLogger(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Synthesize(ctx, "hi")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
