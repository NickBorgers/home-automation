package ha

import "strings"

// Label constants used for device filtering across plugins
const (
	// MonitoringIgnoreLabel is the Home Assistant label that excludes devices from monitoring.
	// In Home Assistant, this corresponds to a label with display name "Monitoring Ignore".
	// Devices with this label will be excluded from:
	// - Water leak monitoring
	// - Battery monitoring
	// - Stale sensor detection
	// - Temperature lockup detection
	MonitoringIgnoreLabel = "monitoring_ignore"

	// IndoorLabel is used to identify indoor devices for humidity alerting
	IndoorLabel = "indoor"

	// UnconditionedLabel identifies unconditioned spaces (barns, attics, sheds) for humidity alerting.
	// These spaces naturally track outdoor humidity and use relaxed thresholds.
	// Devices with this label are automatically treated as indoor (alertable) with:
	// - Higher absolute thresholds (75% warning, 80% critical vs 55%/65% for conditioned)
	// - Alert suppression when humidity tracks close to outdoor levels
	UnconditionedLabel = "unconditioned"
)

// DeviceLabelChecker provides centralized device label checking functionality.
// It caches device labels to avoid repeated API calls during sensor discovery.
type DeviceLabelChecker struct {
	deviceLabels map[string][]string // deviceID -> labels
}

// NewDeviceLabelChecker creates a DeviceLabelChecker from a list of devices.
// This is typically called once during plugin startup after fetching devices from HA.
func NewDeviceLabelChecker(devices []*Device) *DeviceLabelChecker {
	checker := &DeviceLabelChecker{
		deviceLabels: make(map[string][]string),
	}
	for _, device := range devices {
		checker.deviceLabels[device.ID] = device.Labels
	}
	return checker
}

// HasLabel checks if a device has a specific label (case-sensitive).
func (c *DeviceLabelChecker) HasLabel(deviceID, label string) bool {
	labels, ok := c.deviceLabels[deviceID]
	if !ok {
		return false
	}
	for _, l := range labels {
		if l == label {
			return true
		}
	}
	return false
}

// HasLabelIgnoreCase checks if a device has a specific label (case-insensitive).
// This is useful for labels like "indoor" which may be entered as "Indoor", "INDOOR", etc.
func (c *DeviceLabelChecker) HasLabelIgnoreCase(deviceID, label string) bool {
	labels, ok := c.deviceLabels[deviceID]
	if !ok {
		return false
	}
	for _, l := range labels {
		if strings.EqualFold(l, label) {
			return true
		}
	}
	return false
}

// ShouldIgnoreForMonitoring checks if a device has the monitoring_ignore label.
// Returns true if the device should be excluded from all monitoring features.
func (c *DeviceLabelChecker) ShouldIgnoreForMonitoring(deviceID string) bool {
	return c.HasLabel(deviceID, MonitoringIgnoreLabel)
}

// GetLabels returns all labels for a device, or nil if device not found.
func (c *DeviceLabelChecker) GetLabels(deviceID string) []string {
	return c.deviceLabels[deviceID]
}
