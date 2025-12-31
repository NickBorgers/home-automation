// Package testutil provides testing utilities for home automation plugins.
package testutil

import (
	"homeautomation/internal/testlogger"

	"go.uber.org/zap"
)

// TestLogger returns a logger appropriate for tests.
// By default, it returns a no-op logger to reduce test output noise.
// Set TEST_VERBOSE=true environment variable to get full development logging.
//
// Deprecated: Use testlogger.New() directly for internal packages to avoid
// circular imports. This function is kept for backward compatibility with
// external packages that use pkg/testutil.
func TestLogger() *zap.Logger {
	return testlogger.New()
}
