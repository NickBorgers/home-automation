package ha

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// Notification represents a notification to be sent via Home Assistant's notify service.
// The service name format is "notify.mobile_app_<device_id>".
type Notification struct {
	// Message is the notification body (required)
	Message string

	// Title is the notification title (optional)
	Title string

	// Data contains platform-specific notification options (optional)
	Data *NotificationData
}

// NotificationData contains optional platform-specific notification options.
// See: https://companion.home-assistant.io/docs/notifications/notifications-basic/
type NotificationData struct {
	// URL to open when notification is tapped (iOS uses "url", Android uses "clickAction")
	URL string `json:"url,omitempty"`

	// ClickAction for Android - URL or app action to open
	ClickAction string `json:"clickAction,omitempty"`

	// Group notifications together visually
	Group string `json:"group,omitempty"`

	// Tag creates replaceable notifications - same tag replaces previous
	Tag string `json:"tag,omitempty"`

	// Channel for Android notification channels
	Channel string `json:"channel,omitempty"`

	// Importance for Android (high, low, max, min, default)
	Importance string `json:"importance,omitempty"`

	// Sticky prevents notification from being dismissed on tap (Android)
	Sticky bool `json:"sticky,omitempty"`

	// Persistent makes notification undismissable (Android)
	Persistent bool `json:"persistent,omitempty"`

	// TTSText for text-to-speech on Android
	TTSText string `json:"tts_text,omitempty"`

	// Sound for notification sound (iOS/macOS: set to "none" to disable)
	Sound string `json:"push.sound,omitempty"`

	// Badge number for app icon (iOS)
	Badge int `json:"push.badge,omitempty"`

	// Color for notification accent color (Android, hex or color name)
	Color string `json:"color,omitempty"`

	// IconURL for custom notification icon (Android)
	IconURL string `json:"icon_url,omitempty"`

	// Additional custom data fields
	Custom map[string]interface{} `json:"-"`
}

// toMap converts NotificationData to a map for the service call
func (d *NotificationData) toMap() map[string]interface{} {
	if d == nil {
		return nil
	}

	data := make(map[string]interface{})

	if d.URL != "" {
		data["url"] = d.URL
	}
	if d.ClickAction != "" {
		data["clickAction"] = d.ClickAction
	}
	if d.Group != "" {
		data["group"] = d.Group
	}
	if d.Tag != "" {
		data["tag"] = d.Tag
	}
	if d.Channel != "" {
		data["channel"] = d.Channel
	}
	if d.Importance != "" {
		data["importance"] = d.Importance
	}
	if d.Sticky {
		data["sticky"] = true
	}
	if d.Persistent {
		data["persistent"] = true
	}
	if d.TTSText != "" {
		data["tts_text"] = d.TTSText
	}
	// Handle push data (iOS-specific fields) - pre-create map to avoid fragile merge logic
	if d.Sound != "" || d.Badge != 0 {
		push := make(map[string]interface{})
		if d.Sound != "" {
			push["sound"] = d.Sound
		}
		if d.Badge != 0 {
			push["badge"] = d.Badge
		}
		data["push"] = push
	}
	if d.Color != "" {
		data["color"] = d.Color
	}
	if d.IconURL != "" {
		data["icon_url"] = d.IconURL
	}

	// Add custom fields
	for k, v := range d.Custom {
		data[k] = v
	}

	return data
}

// validateNotification validates the notification parameters.
// This is shared between Client and MockClient implementations.
func validateNotification(deviceName string, notification *Notification) error {
	if notification == nil {
		return fmt.Errorf("notification cannot be nil")
	}
	if notification.Message == "" {
		return fmt.Errorf("notification message is required")
	}
	if deviceName == "" {
		return fmt.Errorf("device name is required")
	}
	return nil
}

// buildNotificationServiceData builds the service data map for a notification.
// This is shared between Client and MockClient implementations.
func buildNotificationServiceData(notification *Notification) map[string]interface{} {
	serviceData := map[string]interface{}{
		"message": notification.Message,
	}

	if notification.Title != "" {
		serviceData["title"] = notification.Title
	}

	if notification.Data != nil {
		dataMap := notification.Data.toMap()
		if len(dataMap) > 0 {
			serviceData["data"] = dataMap
		}
	}

	return serviceData
}

// buildClearNotificationServiceData builds the service data map for clearing a notification.
// This is shared between Client and MockClient implementations.
func buildClearNotificationServiceData(tag string) map[string]interface{} {
	return map[string]interface{}{
		"message": "clear_notification",
		"data": map[string]interface{}{
			"tag": tag,
		},
	}
}

// SendNotification sends a notification to a mobile device via Home Assistant.
// The deviceName should be the device identifier (e.g., "nicks_iphone", "person_phone").
// It will be automatically prefixed with "mobile_app_" for the service name.
//
// Example:
//
//	client.SendNotification("nicks_iphone", &Notification{
//	    Message: "Water leak detected!",
//	    Title:   "Alert",
//	})
func (c *Client) SendNotification(deviceName string, notification *Notification) error {
	if err := validateNotification(deviceName, notification); err != nil {
		return err
	}

	serviceData := buildNotificationServiceData(notification)
	serviceName := fmt.Sprintf("mobile_app_%s", deviceName)

	return c.CallService(context.Background(), "notify", serviceName, serviceData)
}

// SendNotificationToMultiple sends a notification to multiple devices.
// This is a convenience method that calls SendNotification for each device.
// If multiple notifications fail, all errors are aggregated and returned.
func (c *Client) SendNotificationToMultiple(deviceNames []string, notification *Notification) error {
	if len(deviceNames) == 0 {
		return fmt.Errorf("at least one device name is required")
	}

	var errs []error
	for _, deviceName := range deviceNames {
		if err := c.SendNotification(deviceName, notification); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", deviceName, err))
			c.logger.Error("Failed to send notification",
				zap.String("device", deviceName),
				zap.Error(err))
		}
	}

	return errors.Join(errs...)
}

// ClearNotification clears a notification with the specified tag on a device.
// This sends a special "clear_notification" message to remove a previously sent notification.
func (c *Client) ClearNotification(deviceName, tag string) error {
	if deviceName == "" {
		return fmt.Errorf("device name is required")
	}
	if tag == "" {
		return fmt.Errorf("tag is required to clear a notification")
	}

	serviceData := buildClearNotificationServiceData(tag)
	serviceName := fmt.Sprintf("mobile_app_%s", deviceName)

	return c.CallService(context.Background(), "notify", serviceName, serviceData)
}
