// Package testlogger provides a test logger that suppresses output by default.
// This reduces noise during test runs while allowing verbose output when needed.
package testlogger

import (
	"os"

	"go.uber.org/zap"
)

// New returns a logger appropriate for tests.
// By default, it returns a no-op logger (zap.NewNop()) to reduce test output noise.
// Set TEST_VERBOSE=true environment variable to get full development logging.
//
// Example usage:
//
//	logger := testlogger.New()
//
// To run tests with verbose logging:
//
//	TEST_VERBOSE=true go test ./...
func New() *zap.Logger {
	if os.Getenv("TEST_VERBOSE") == "true" {
		logger, _ := zap.NewDevelopment()
		return logger
	}
	return zap.NewNop()
}
