package logbuffer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLogLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantLevel string
		wantMsg   string
		wantErr   bool
	}{
		{
			name:      "basic log line",
			line:      `{"level":"info","ts":"2025-01-15T10:30:00.000Z","msg":"Test message"}`,
			wantLevel: "info",
			wantMsg:   "Test message",
			wantErr:   false,
		},
		{
			name:      "log line with fields",
			line:      `{"level":"error","ts":"2025-01-15T10:30:00.000Z","msg":"Error occurred","plugin":"lighting","room":"bedroom"}`,
			wantLevel: "error",
			wantMsg:   "Error occurred",
			wantErr:   false,
		},
		{
			name:      "debug level",
			line:      `{"level":"debug","ts":"2025-01-15T10:30:00.000Z","msg":"Debug info"}`,
			wantLevel: "debug",
			wantMsg:   "Debug info",
			wantErr:   false,
		},
		{
			name:    "invalid json",
			line:    `not json`,
			wantErr: true,
		},
		{
			name:    "empty json",
			line:    `{}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := parseLogLine([]byte(tt.line))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLogLine() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if event.Level != tt.wantLevel {
					t.Errorf("Level = %v, want %v", event.Level, tt.wantLevel)
				}
				if event.Message != tt.wantMsg {
					t.Errorf("Message = %v, want %v", event.Message, tt.wantMsg)
				}
			}
		})
	}
}

func TestReadEventsFromReader(t *testing.T) {
	logContent := `{"level":"info","ts":"2025-01-15T10:00:00.000Z","msg":"First message"}
{"level":"warn","ts":"2025-01-15T10:01:00.000Z","msg":"Second message"}
{"level":"error","ts":"2025-01-15T10:02:00.000Z","msg":"Third message"}
{"level":"info","ts":"2025-01-15T10:03:00.000Z","msg":"Fourth message"}
{"level":"info","ts":"2025-01-15T10:04:00.000Z","msg":"Fifth message"}
`

	t.Run("read all events", func(t *testing.T) {
		reader := strings.NewReader(logContent)
		events, err := readEventsFromReader(reader, time.Time{}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 5 {
			t.Errorf("got %d events, want 5", len(events))
		}
	})

	t.Run("read with limit", func(t *testing.T) {
		reader := strings.NewReader(logContent)
		events, err := readEventsFromReader(reader, time.Time{}, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 3 {
			t.Errorf("got %d events, want 3", len(events))
		}
	})

	t.Run("read with since filter", func(t *testing.T) {
		reader := strings.NewReader(logContent)
		since, _ := time.Parse(time.RFC3339, "2025-01-15T10:02:00.000Z")
		events, err := readEventsFromReader(reader, since, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Events after 10:02 should be 10:03 and 10:04
		if len(events) != 2 {
			t.Errorf("got %d events, want 2", len(events))
		}
	})

	t.Run("handles malformed lines gracefully", func(t *testing.T) {
		contentWithBadLines := `{"level":"info","ts":"2025-01-15T10:00:00.000Z","msg":"Good line"}
not json
{"level":"info","ts":"2025-01-15T10:01:00.000Z","msg":"Another good line"}
`
		reader := strings.NewReader(contentWithBadLines)
		events, err := readEventsFromReader(reader, time.Time{}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 2 {
			t.Errorf("got %d events, want 2 (skipping malformed line)", len(events))
		}
	})
}

func TestReadEventsFromFile(t *testing.T) {
	// Create a temporary log file
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	logContent := `{"level":"info","ts":"2025-01-15T10:00:00.000Z","msg":"Message 1"}
{"level":"info","ts":"2025-01-15T10:01:00.000Z","msg":"Message 2"}
{"level":"info","ts":"2025-01-15T10:02:00.000Z","msg":"Message 3"}
`

	if err := os.WriteFile(logFile, []byte(logContent), 0644); err != nil {
		t.Fatalf("failed to write test log file: %v", err)
	}

	t.Run("read from file", func(t *testing.T) {
		events, err := ReadEventsFromFile(logFile, time.Time{}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 3 {
			t.Errorf("got %d events, want 3", len(events))
		}
	})

	t.Run("non-existent file returns empty", func(t *testing.T) {
		events, err := ReadEventsFromFile("/nonexistent/path/file.log", time.Time{}, 0)
		if err != nil {
			t.Errorf("expected nil error for non-existent file, got: %v", err)
		}
		if len(events) != 0 {
			t.Errorf("got %d events, want 0", len(events))
		}
	})
}

func TestBufferWithFile(t *testing.T) {
	// Create a temporary log file
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	logContent := `{"level":"info","ts":"2025-01-15T10:00:00.000Z","msg":"Historical event 1"}
{"level":"info","ts":"2025-01-15T10:01:00.000Z","msg":"Historical event 2"}
{"level":"info","ts":"2025-01-15T10:02:00.000Z","msg":"Historical event 3"}
`

	if err := os.WriteFile(logFile, []byte(logContent), 0644); err != nil {
		t.Fatalf("failed to write test log file: %v", err)
	}

	t.Run("file-backed buffer reads from file", func(t *testing.T) {
		buffer := NewBufferWithFile(100, logFile)

		if !buffer.IsFileBacked() {
			t.Error("expected IsFileBacked() to return true")
		}

		events := buffer.GetEvents(time.Time{}, 0)
		if len(events) != 3 {
			t.Errorf("got %d events, want 3", len(events))
		}

		if events[0].Message != "Historical event 1" {
			t.Errorf("first event message = %q, want %q", events[0].Message, "Historical event 1")
		}
	})

	t.Run("file-backed buffer count returns in-memory count", func(t *testing.T) {
		buffer := NewBufferWithFile(100, logFile)
		// Count() returns in-memory count (0 since we haven't called Add()),
		// not the file event count (which would require reading the entire file)
		count := buffer.Count()
		if count != 0 {
			t.Errorf("Count() = %d, want 0 (in-memory count, not file count)", count)
		}
	})

	t.Run("file-backed buffer overflow is always false", func(t *testing.T) {
		buffer := NewBufferWithFile(100, logFile)
		if buffer.HasOverflowed() {
			t.Error("expected HasOverflowed() to return false for file-backed buffer")
		}
	})

	t.Run("non-file-backed buffer still works", func(t *testing.T) {
		buffer := NewBuffer(100)

		if buffer.IsFileBacked() {
			t.Error("expected IsFileBacked() to return false")
		}

		buffer.Add(Event{
			Timestamp: time.Now(),
			Level:     "info",
			Message:   "Test event",
		})

		events := buffer.GetEvents(time.Time{}, 0)
		if len(events) != 1 {
			t.Errorf("got %d events, want 1", len(events))
		}
	})
}

func TestLogFilePathMethod(t *testing.T) {
	t.Run("returns path for file-backed buffer", func(t *testing.T) {
		buffer := NewBufferWithFile(100, "/path/to/log.file")
		if buffer.LogFilePath() != "/path/to/log.file" {
			t.Errorf("LogFilePath() = %q, want %q", buffer.LogFilePath(), "/path/to/log.file")
		}
	})

	t.Run("returns empty for non-file-backed buffer", func(t *testing.T) {
		buffer := NewBuffer(100)
		if buffer.LogFilePath() != "" {
			t.Errorf("LogFilePath() = %q, want empty string", buffer.LogFilePath())
		}
	})
}

func TestParseLogLineWithFields(t *testing.T) {
	line := `{"level":"info","ts":"2025-01-15T10:00:00.000Z","msg":"Test","plugin":"lighting","count":42,"enabled":true}`

	event, err := parseLogLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Level != "info" {
		t.Errorf("Level = %q, want %q", event.Level, "info")
	}

	if event.Message != "Test" {
		t.Errorf("Message = %q, want %q", event.Message, "Test")
	}

	// Check additional fields
	if plugin, ok := event.Fields["plugin"].(string); !ok || plugin != "lighting" {
		t.Errorf("Fields[plugin] = %v, want %q", event.Fields["plugin"], "lighting")
	}

	if count, ok := event.Fields["count"].(float64); !ok || count != 42 {
		t.Errorf("Fields[count] = %v, want 42", event.Fields["count"])
	}

	if enabled, ok := event.Fields["enabled"].(bool); !ok || !enabled {
		t.Errorf("Fields[enabled] = %v, want true", event.Fields["enabled"])
	}
}
