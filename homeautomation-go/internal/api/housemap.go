package api

import (
	"bytes"
	"net/http"
	"path/filepath"

	"go.uber.org/zap"

	"homeautomation/internal/housemap"
	"homeautomation/internal/plugins/roompresence"
)

// mapPageHead turns the review fragment into a full page. The fragment starts with <title> because it is
// also published as an artifact, and the artifact host adds this part itself.
const mapPageHead = "<!DOCTYPE html>\n<meta charset=\"utf-8\">\n" +
	"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n"

// SetConfigDir names the directory that holds house_map.yaml and room_presence_config.yaml. Call it before
// Start. Without it, /map answers 404.
func (s *Server) SetConfigDir(dir string) {
	s.configDir = dir
}

// handleMap draws the house map review page. It reads the files on every request, so the page always
// matches the configs in the running container.
func (s *Server) handleMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.configDir == "" {
		http.Error(w, "No house map configured", http.StatusNotFound)
		return
	}

	m, err := housemap.Load(filepath.Join(s.configDir, "house_map.yaml"))
	if err != nil {
		s.logger.Error("Failed to load house map", zap.Error(err))
		http.Error(w, "Failed to load house map", http.StatusInternalServerError)
		return
	}
	if m == nil {
		http.Error(w, "No house map configured", http.StatusNotFound)
		return
	}

	opts := housemap.ReviewOptions{Source: "house_map.yaml"}
	rp, err := roompresence.LoadConfig(filepath.Join(s.configDir, "room_presence_config.yaml"))
	if err != nil {
		// The page still works without it. Only the unseen-area marks are lost.
		s.logger.Warn("Failed to load room presence config for house map", zap.Error(err))
	} else if rp != nil {
		opts.Seen = map[string]bool{}
		for name, a := range rp.Areas {
			opts.Seen[name] = a.Seen
		}
	}

	var buf bytes.Buffer
	buf.WriteString(mapPageHead)
	if err := m.WriteReviewHTML(&buf, opts); err != nil {
		s.logger.Error("Failed to render house map", zap.Error(err))
		http.Error(w, "Failed to render house map", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())

	s.logger.Debug("House map request served",
		zap.String("remote_addr", r.RemoteAddr))
}
