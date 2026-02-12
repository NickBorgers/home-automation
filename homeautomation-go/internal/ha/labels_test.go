package ha

import (
	"testing"
)

func TestNewDeviceLabelChecker(t *testing.T) {
	devices := []*Device{
		{ID: "device1", Labels: []string{"indoor", "monitoring_ignore"}},
		{ID: "device2", Labels: []string{"outdoor"}},
		{ID: "device3", Labels: []string{}},
		{ID: "device4", Labels: nil},
	}

	checker := NewDeviceLabelChecker(devices)

	if checker == nil {
		t.Fatal("expected non-nil checker")
	}
	if len(checker.deviceLabels) != 4 {
		t.Errorf("expected 4 devices, got %d", len(checker.deviceLabels))
	}
}

func TestNewDeviceLabelChecker_NilDevices(t *testing.T) {
	checker := NewDeviceLabelChecker(nil)

	if checker == nil {
		t.Fatal("expected non-nil checker")
	}
	if len(checker.deviceLabels) != 0 {
		t.Errorf("expected empty map, got %d entries", len(checker.deviceLabels))
	}
}

func TestDeviceLabelChecker_ShouldIgnoreForMonitoring(t *testing.T) {
	devices := []*Device{
		{ID: "ignored_device", Labels: []string{"monitoring_ignore"}},
		{ID: "monitored_device", Labels: []string{"indoor"}},
		{ID: "both_labels", Labels: []string{"indoor", "monitoring_ignore"}},
		{ID: "no_labels", Labels: []string{}},
	}

	checker := NewDeviceLabelChecker(devices)

	tests := []struct {
		name     string
		deviceID string
		expected bool
	}{
		{"device with only monitoring_ignore", "ignored_device", true},
		{"device without monitoring_ignore", "monitored_device", false},
		{"device with multiple labels including monitoring_ignore", "both_labels", true},
		{"device with no labels", "no_labels", false},
		{"unknown device", "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.ShouldIgnoreForMonitoring(tt.deviceID)
			if result != tt.expected {
				t.Errorf("ShouldIgnoreForMonitoring(%q) = %v, want %v", tt.deviceID, result, tt.expected)
			}
		})
	}
}

func TestDeviceLabelChecker_GetLabels(t *testing.T) {
	devices := []*Device{
		{ID: "device1", Labels: []string{"indoor", "monitoring_ignore"}},
		{ID: "device2", Labels: []string{}},
	}

	checker := NewDeviceLabelChecker(devices)

	// Device with labels
	labels := checker.GetLabels("device1")
	if len(labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(labels))
	}

	// Device with empty labels
	labels = checker.GetLabels("device2")
	if len(labels) != 0 {
		t.Errorf("expected 0 labels, got %d", len(labels))
	}

	// Unknown device
	labels = checker.GetLabels("unknown")
	if labels != nil {
		t.Errorf("expected nil for unknown device, got %v", labels)
	}
}
