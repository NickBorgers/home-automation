package validation

import (
	"testing"
)

// TestValidatePR300 is an intentionally failing test to validate PR #300's auto-fix functionality.
// The test expects 4+4=9, which is incorrect. Claude should fix this to expect 8.
func TestValidatePR300(t *testing.T) {
	result := 4 + 4
	expected := 8
	if result != expected {
		t.Errorf("Expected %d + %d = %d, but got %d", 4, 4, expected, result)
	}
}
