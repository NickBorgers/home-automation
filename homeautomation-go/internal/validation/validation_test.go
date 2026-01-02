package validation

import (
	"testing"
)

// TestValidatePR300 validates basic arithmetic for PR #300 testing.
func TestValidatePR300(t *testing.T) {
	result := 4 + 4
	expected := 8
	if result != expected {
		t.Errorf("Expected %d + %d = %d, but got %d", 4, 4, expected, result)
	}
}
