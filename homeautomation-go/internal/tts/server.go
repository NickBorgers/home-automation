package tts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	defaultTTL = 5 * time.Minute
	defaultCap = 32
)

// Server stores synthesized MP3s in memory and serves them at
// /audio/{id}.mp3 over plain HTTP so LAN-only Sonos speakers can fetch them.
type Server struct {
	baseURL    string
	addr       string
	logger     *zap.Logger
	httpServer *http.Server
	ttl        time.Duration
	cap        int

	mu    sync.Mutex
	items map[string]item
	now   func() time.Time // injectable clock for tests
}

type item struct {
	body      []byte
	expiresAt time.Time
}

// NewServer constructs an audio file server. addr is the listen address
// (e.g. ":8085"); baseURL is what we hand to Sonos (e.g.
// "http://10.212.100.100:8085") — typically dockergeneric's LAN address.
func NewServer(addr, baseURL string, logger *zap.Logger) *Server {
	s := &Server{
		baseURL: strings.TrimRight(baseURL, "/"),
		addr:    addr,
		logger:  logger.Named("tts.server"),
		ttl:     defaultTTL,
		cap:     defaultCap,
		items:   make(map[string]item),
		now:     time.Now,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/", s.handleAudio)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Start binds the listen port and launches the audio file server in a
// goroutine. Returns an error immediately if the port cannot be bound so the
// caller can Fatal at startup rather than discovering the failure later when
// Sonos tries to fetch an audio URL that never existed.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("TTS audio server listen %s: %w", s.addr, err)
	}
	s.logger.Info("Starting TTS audio file server",
		zap.String("addr", s.addr),
		zap.String("base_url", s.baseURL))
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("TTS audio server error", zap.Error(err))
		}
	}()
	return nil
}

// Stop gracefully shuts down the audio file server with a 5s deadline.
func (s *Server) Stop() error {
	s.logger.Info("Stopping TTS audio file server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown TTS audio server: %w", err)
	}
	return nil
}

// Store puts mp3 in the cache and returns the absolute URL Sonos should fetch.
// Evicts expired entries and (if still over cap) the oldest entries.
func (s *Server) Store(mp3 []byte) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for k, v := range s.items {
		if !v.expiresAt.After(now) {
			delete(s.items, k)
		}
	}
	for len(s.items) >= s.cap {
		var oldestKey string
		var oldestAt time.Time
		first := true
		for k, v := range s.items {
			if first || v.expiresAt.Before(oldestAt) {
				oldestKey = k
				oldestAt = v.expiresAt
				first = false
			}
		}
		delete(s.items, oldestKey)
	}

	s.items[id] = item{body: mp3, expiresAt: now.Add(s.ttl)}
	return fmt.Sprintf("%s/audio/%s.mp3", s.baseURL, id), nil
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/audio/"), ".mp3")
	if id == "" || strings.ContainsAny(id, "/.") {
		http.NotFound(w, r)
		return
	}

	s.mu.Lock()
	it, ok := s.items[id]
	expired := ok && !it.expiresAt.After(s.now())
	if expired {
		delete(s.items, id)
	}
	s.mu.Unlock()

	if !ok || expired {
		s.logger.Debug("audio not found", zap.String("id", id))
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(it.body)))
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(it.body); err != nil {
		s.logger.Debug("write audio response failed", zap.String("id", id), zap.Error(err))
	}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate audio id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
