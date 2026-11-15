package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFile copies src to dir/name.
func copyFile(t *testing.T, src, dir, name string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func getMap(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/map", nil))
	return w
}

func TestHandleMap_RendersPage(t *testing.T) {
	dir := t.TempDir()
	copyFile(t, "../housemap/testdata/example_house_map.yaml", dir, "house_map.yaml")
	copyFile(t, "../plugins/roompresence/testdata/example_config.yaml", dir, "room_presence_config.yaml")

	s := createTestServer(t)
	s.SetConfigDir(dir)
	w := getMap(t, s)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "<!DOCTYPE html>") {
		t.Errorf("page does not start with a doctype: %.80q", body)
	}
	if !strings.Contains(body, "<title>House Floor Plan</title>") {
		t.Error("page has no title")
	}
	if !strings.Contains(body, "<code>study</code> <em>unseen</em>") {
		t.Error("room presence config was not used to mark unseen areas")
	}
}

func TestHandleMap_WithoutRoomPresence(t *testing.T) {
	dir := t.TempDir()
	copyFile(t, "../housemap/testdata/example_house_map.yaml", dir, "house_map.yaml")

	s := createTestServer(t)
	s.SetConfigDir(dir)
	if w := getMap(t, s); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHandleMap_NotFound(t *testing.T) {
	s := createTestServer(t)
	if w := getMap(t, s); w.Code != http.StatusNotFound {
		t.Errorf("no config dir: status = %d, want 404", w.Code)
	}

	s.SetConfigDir(t.TempDir())
	if w := getMap(t, s); w.Code != http.StatusNotFound {
		t.Errorf("no house_map.yaml: status = %d, want 404", w.Code)
	}
}

func TestHandleMap_BadMap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "house_map.yaml"), []byte("spaces: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	s := createTestServer(t)
	s.SetConfigDir(dir)
	if w := getMap(t, s); w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
