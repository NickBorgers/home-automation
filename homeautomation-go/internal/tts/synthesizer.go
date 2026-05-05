package tts

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Synthesizer is the seam notify.Manager depends on. Tests inject a fake.
type Synthesizer interface {
	SynthesizeAndServe(ctx context.Context, text string) (string, error)
}

// Service combines a Kokoro client and a local audio file server.
type Service struct {
	client *Client
	server *Server
	logger *zap.Logger
}

// NewService wires a client and server into a Synthesizer.
func NewService(client *Client, server *Server, logger *zap.Logger) *Service {
	return &Service{
		client: client,
		server: server,
		logger: logger.Named("tts.service"),
	}
}

// SynthesizeAndServe synthesizes text to MP3 via Kokoro, stores the bytes
// in the local audio server, and returns the URL Sonos should fetch.
func (s *Service) SynthesizeAndServe(ctx context.Context, text string) (string, error) {
	mp3, err := s.client.Synthesize(ctx, text)
	if err != nil {
		return "", fmt.Errorf("synthesize: %w", err)
	}
	url, err := s.server.Store(mp3)
	if err != nil {
		return "", fmt.Errorf("store mp3: %w", err)
	}
	s.logger.Debug("synthesized announcement",
		zap.Int("bytes", len(mp3)),
		zap.String("url", url))
	return url, nil
}
