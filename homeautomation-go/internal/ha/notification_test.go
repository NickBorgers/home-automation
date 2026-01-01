package ha

import (
	"fmt"
	"testing"
)

func TestSendNotification_Basic(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.SendNotification("nicks_iphone", &Notification{
		Message: "Test notification",
	})
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	calls := mock.GetNotificationCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 notification call, got %d", len(calls))
	}

	call := calls[0]
	if call.Domain != "notify" {
		t.Errorf("Expected domain 'notify', got %q", call.Domain)
	}
	if call.Service != "mobile_app_nicks_iphone" {
		t.Errorf("Expected service 'mobile_app_nicks_iphone', got %q", call.Service)
	}
	if call.Data["message"] != "Test notification" {
		t.Errorf("Expected message 'Test notification', got %q", call.Data["message"])
	}
}

func TestSendNotification_WithTitle(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.SendNotification("person_phone", &Notification{
		Message: "Water leak detected!",
		Title:   "Alert",
	})
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	calls := mock.GetNotificationCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 notification call, got %d", len(calls))
	}

	call := calls[0]
	if call.Data["message"] != "Water leak detected!" {
		t.Errorf("Expected message 'Water leak detected!', got %q", call.Data["message"])
	}
	if call.Data["title"] != "Alert" {
		t.Errorf("Expected title 'Alert', got %q", call.Data["title"])
	}
}

func TestSendNotification_WithData(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.SendNotification("nicks_iphone", &Notification{
		Message: "Motion detected in backyard",
		Title:   "Security Alert",
		Data: &NotificationData{
			URL:        "/lovelace/cameras",
			Group:      "security-alerts",
			Tag:        "backyard-motion",
			Importance: "high",
		},
	})
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	calls := mock.GetNotificationCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 notification call, got %d", len(calls))
	}

	call := calls[0]
	data, ok := call.Data["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data field to be a map")
	}

	if data["url"] != "/lovelace/cameras" {
		t.Errorf("Expected url '/lovelace/cameras', got %q", data["url"])
	}
	if data["group"] != "security-alerts" {
		t.Errorf("Expected group 'security-alerts', got %q", data["group"])
	}
	if data["tag"] != "backyard-motion" {
		t.Errorf("Expected tag 'backyard-motion', got %q", data["tag"])
	}
	if data["importance"] != "high" {
		t.Errorf("Expected importance 'high', got %q", data["importance"])
	}
}

func TestSendNotification_WithStickyAndPersistent(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.SendNotification("nicks_iphone", &Notification{
		Message: "Critical alert",
		Data: &NotificationData{
			Sticky:     true,
			Persistent: true,
		},
	})
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	calls := mock.GetNotificationCalls()
	call := calls[0]
	data := call.Data["data"].(map[string]interface{})

	if data["sticky"] != true {
		t.Errorf("Expected sticky true, got %v", data["sticky"])
	}
	if data["persistent"] != true {
		t.Errorf("Expected persistent true, got %v", data["persistent"])
	}
}

func TestSendNotification_Validation(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	tests := []struct {
		name         string
		deviceName   string
		notification *Notification
		expectError  string
	}{
		{
			name:         "nil notification",
			deviceName:   "test_device",
			notification: nil,
			expectError:  "notification cannot be nil",
		},
		{
			name:         "empty message",
			deviceName:   "test_device",
			notification: &Notification{Message: ""},
			expectError:  "notification message is required",
		},
		{
			name:         "empty device name",
			deviceName:   "",
			notification: &Notification{Message: "test"},
			expectError:  "device name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mock.SendNotification(tt.deviceName, tt.notification)
			if err == nil {
				t.Errorf("Expected error, got nil")
			} else if err.Error() != tt.expectError {
				t.Errorf("Expected error %q, got %q", tt.expectError, err.Error())
			}
		})
	}
}

func TestSendNotificationToMultiple(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.SendNotificationToMultiple(
		[]string{"nicks_iphone", "carolines_iphone"},
		&Notification{
			Message: "Family announcement",
			Title:   "Home",
		},
	)
	if err != nil {
		t.Fatalf("SendNotificationToMultiple failed: %v", err)
	}

	calls := mock.GetNotificationCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 notification calls, got %d", len(calls))
	}

	// Check both devices received the notification
	services := make(map[string]bool)
	for _, call := range calls {
		services[call.Service] = true
	}

	if !services["mobile_app_nicks_iphone"] {
		t.Error("Expected notification to nicks_iphone")
	}
	if !services["mobile_app_carolines_iphone"] {
		t.Error("Expected notification to carolines_iphone")
	}
}

func TestSendNotificationToMultiple_EmptyList(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.SendNotificationToMultiple(
		[]string{},
		&Notification{Message: "test"},
	)
	if err == nil {
		t.Error("Expected error for empty device list")
	}
}

func TestClearNotification(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	err := mock.ClearNotification("nicks_iphone", "backyard-motion")
	if err != nil {
		t.Fatalf("ClearNotification failed: %v", err)
	}

	calls := mock.GetNotificationCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 notification call, got %d", len(calls))
	}

	call := calls[0]
	if call.Service != "mobile_app_nicks_iphone" {
		t.Errorf("Expected service 'mobile_app_nicks_iphone', got %q", call.Service)
	}
	if call.Data["message"] != "clear_notification" {
		t.Errorf("Expected message 'clear_notification', got %q", call.Data["message"])
	}

	data := call.Data["data"].(map[string]interface{})
	if data["tag"] != "backyard-motion" {
		t.Errorf("Expected tag 'backyard-motion', got %q", data["tag"])
	}
}

func TestClearNotification_Validation(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	tests := []struct {
		name        string
		deviceName  string
		tag         string
		expectError string
	}{
		{
			name:        "empty device name",
			deviceName:  "",
			tag:         "some-tag",
			expectError: "device name is required",
		},
		{
			name:        "empty tag",
			deviceName:  "test_device",
			tag:         "",
			expectError: "tag is required to clear a notification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mock.ClearNotification(tt.deviceName, tt.tag)
			if err == nil {
				t.Errorf("Expected error, got nil")
			} else if err.Error() != tt.expectError {
				t.Errorf("Expected error %q, got %q", tt.expectError, err.Error())
			}
		})
	}
}

func TestNotificationData_ToMap(t *testing.T) {
	tests := []struct {
		name     string
		data     *NotificationData
		expected map[string]interface{}
	}{
		{
			name:     "nil data",
			data:     nil,
			expected: nil,
		},
		{
			name:     "empty data",
			data:     &NotificationData{},
			expected: map[string]interface{}{},
		},
		{
			name: "with URL and group",
			data: &NotificationData{
				URL:   "/lovelace/home",
				Group: "alerts",
			},
			expected: map[string]interface{}{
				"url":   "/lovelace/home",
				"group": "alerts",
			},
		},
		{
			name: "with custom fields",
			data: &NotificationData{
				Custom: map[string]interface{}{
					"image": "/local/camera.jpg",
				},
			},
			expected: map[string]interface{}{
				"image": "/local/camera.jpg",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.toMap()

			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d fields, got %d", len(tt.expected), len(result))
			}

			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("Expected %s=%v, got %v", k, v, result[k])
				}
			}
		})
	}
}

func TestNotificationServiceError(t *testing.T) {
	mock := NewMockClient()
	mock.Connect()

	// Set up error for the notification service
	mock.SetServiceError("notify", "mobile_app_nicks_iphone",
		fmt.Errorf("service not found"))

	err := mock.SendNotification("nicks_iphone", &Notification{
		Message: "Test",
	})
	if err == nil {
		t.Error("Expected error when service fails")
	}
}
