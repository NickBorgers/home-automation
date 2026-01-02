package validation

import (
	"testing"
)

// TestValidatePR300 validates PR #300's auto-fix functionality.
// This test was auto-fixed by Claude's PR #300 retry loop.
func TestValidatePR300(t *testing.T) {
	result := 4 + 4
	expected := 8 // Fixed: 4+4=8
	if result != expected {
		t.Errorf("Expected %d + %d = %d, but got %d", 4, 4, expected, result)
	}
}
