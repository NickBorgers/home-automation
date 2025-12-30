package logbuffer

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestBufferCore_Integration(t *testing.T) {
	buffer := NewBuffer(100)
	core := NewBufferCore(buffer, zapcore.InfoLevel)
	logger := zap.New(core)

	// Log some events
	logger.Info("test message 1",
		zap.String("key", "dayPhase"),
		zap.String("old", "day"),
		zap.String("new", "evening"))

	logger.Info("test message 2",
		zap.Bool("active", true),
		zap.Int("count", 42))

	// Debug should be filtered out
	logger.Debug("debug message")

	events := buffer.GetEvents(time.Time{}, 0)
	if len(events) != 2 {
		t.Errorf("len(events) = %d, want 2", len(events))
	}

	// Check first event
	e1 := events[0]
	if e1.Level != "info" {
		t.Errorf("events[0].Level = %q, want %q", e1.Level, "info")
	}
	if e1.Message != "test message 1" {
		t.Errorf("events[0].Message = %q, want %q", e1.Message, "test message 1")
	}
	if e1.Fields["key"] != "dayPhase" {
		t.Errorf("events[0].Fields[\"key\"] = %v, want %q", e1.Fields["key"], "dayPhase")
	}

	// Check second event
	e2 := events[1]
	if e2.Fields["active"] != true {
		t.Errorf("events[1].Fields[\"active\"] = %v, want true", e2.Fields["active"])
	}
	if e2.Fields["count"] != int64(42) {
		t.Errorf("events[1].Fields[\"count\"] = %v, want 42", e2.Fields["count"])
	}
}

func TestBufferCore_LevelFiltering(t *testing.T) {
	tests := []struct {
		name      string
		coreLevel zapcore.Level
		logLevel  zapcore.Level
		logged    bool
	}{
		{"info core, info log", zapcore.InfoLevel, zapcore.InfoLevel, true},
		{"info core, warn log", zapcore.InfoLevel, zapcore.WarnLevel, true},
		{"info core, debug log", zapcore.InfoLevel, zapcore.DebugLevel, false},
		{"warn core, info log", zapcore.WarnLevel, zapcore.InfoLevel, false},
		{"warn core, warn log", zapcore.WarnLevel, zapcore.WarnLevel, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := NewBuffer(10)
			core := NewBufferCore(buffer, tt.coreLevel)
			logger := zap.New(core)

			switch tt.logLevel {
			case zapcore.DebugLevel:
				logger.Debug("test")
			case zapcore.InfoLevel:
				logger.Info("test")
			case zapcore.WarnLevel:
				logger.Warn("test")
			case zapcore.ErrorLevel:
				logger.Error("test")
			}

			events := buffer.GetEvents(time.Time{}, 0)
			if tt.logged && len(events) != 1 {
				t.Errorf("expected event to be logged, got %d events", len(events))
			}
			if !tt.logged && len(events) != 0 {
				t.Errorf("expected event not to be logged, got %d events", len(events))
			}
		})
	}
}

func TestBufferCore_With(t *testing.T) {
	buffer := NewBuffer(10)
	core := NewBufferCore(buffer, zapcore.InfoLevel)
	logger := zap.New(core)

	// Create a child logger with context fields
	childLogger := logger.With(zap.String("plugin", "lighting"))
	childLogger.Info("test message")

	events := buffer.GetEvents(time.Time{}, 0)
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	if events[0].Fields["plugin"] != "lighting" {
		t.Errorf("events[0].Fields[\"plugin\"] = %v, want %q", events[0].Fields["plugin"], "lighting")
	}
}

func TestBufferCore_GetBuffer(t *testing.T) {
	buffer := NewBuffer(10)
	core := NewBufferCore(buffer, zapcore.InfoLevel)

	if core.GetBuffer() != buffer {
		t.Error("GetBuffer() did not return the expected buffer")
	}
}

func TestBufferCore_Sync(t *testing.T) {
	buffer := NewBuffer(10)
	core := NewBufferCore(buffer, zapcore.InfoLevel)

	// Sync should be a no-op that returns nil
	if err := core.Sync(); err != nil {
		t.Errorf("Sync() returned error: %v", err)
	}
}

func TestFieldToInterface(t *testing.T) {
	tests := []struct {
		name     string
		field    zapcore.Field
		expected interface{}
	}{
		{"string", zap.String("key", "value"), "value"},
		{"int64", zap.Int64("key", 42), int64(42)},
		{"bool true", zap.Bool("key", true), true},
		{"bool false", zap.Bool("key", false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fieldToInterface(tt.field)
			if result != tt.expected {
				t.Errorf("fieldToInterface(%v) = %v (%T), want %v (%T)",
					tt.field, result, result, tt.expected, tt.expected)
			}
		})
	}
}
