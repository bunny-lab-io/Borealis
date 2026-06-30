package main

import "testing"

func TestScheduledExecutableDevicesFiltersContainedDevices(t *testing.T) {
	devices := []map[string]any{
		{"hostname": "active-1", "security_status": "active"},
		{"hostname": "online-1", "security_status": "online"},
		{"hostname": "quarantined-1", "security_status": "quarantined"},
		{"hostname": "revoked-1", "security_status": "revoked"},
		{"hostname": "legacy-1"},
	}

	filtered := scheduledExecutableDevices(devices)

	got := map[string]bool{}
	for _, device := range filtered {
		got[cleanText(device["hostname"])] = true
	}
	for _, hostname := range []string{"active-1", "online-1", "legacy-1"} {
		if !got[hostname] {
			t.Fatalf("expected %s to remain executable, got %#v", hostname, got)
		}
	}
	for _, hostname := range []string{"quarantined-1", "revoked-1"} {
		if got[hostname] {
			t.Fatalf("expected %s to be filtered, got %#v", hostname, got)
		}
	}
}
