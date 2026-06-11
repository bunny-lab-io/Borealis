package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOperatorRealtimeHubBroadcastsGoOwnedEvents(t *testing.T) {
	hub := newOperatorRealtimeHub()
	events := hub.subscribe()
	defer hub.unsubscribe(events)

	if err := hub.broadcastAgentStatus(context.Background(), map[string]any{"hostname": "LAB-OPERATOR-01"}); err != nil {
		t.Fatal(err)
	}
	expectRealtimeEvent(t, events, "agent_status_changed")

	if err := hub.broadcastDeviceEvent(context.Background(), "device_services_changed", map[string]any{"hostname": "LAB-OPERATOR-01"}); err != nil {
		t.Fatal(err)
	}
	expectRealtimeEvent(t, events, "device_services_changed")

	if err := hub.broadcastNotification(context.Background(), map[string]any{"message": "Done"}); err != nil {
		t.Fatal(err)
	}
	expectRealtimeEvent(t, events, "borealis_notification")
}

func TestOperatorRealtimeRejectsUnknownDeviceEvent(t *testing.T) {
	hub := newOperatorRealtimeHub()
	if err := hub.broadcastDeviceEvent(context.Background(), "device_processes_changed", map[string]any{}); err == nil {
		t.Fatal("expected invalid device event error")
	}
}

func TestOperatorRealtimeEventsHandlerRequiresAuth(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, operatorRealtimeEventsPath, nil)

	operatorRealtimeEventsHandler(auth, newOperatorRealtimeHub()).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized response, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteSSEEventEncodesNamedPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := writeSSEEvent(recorder, operatorRealtimeEvent{
		Name:      "agent_status_changed",
		Payload:   map[string]any{"hostname": "LAB-OPERATOR-01"},
		CreatedAt: time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: agent_status_changed") || !strings.Contains(body, `"hostname":"LAB-OPERATOR-01"`) {
		t.Fatalf("unexpected SSE body: %s", body)
	}
}

func expectRealtimeEvent(t *testing.T, events <-chan operatorRealtimeEvent, name string) {
	t.Helper()
	select {
	case event := <-events:
		if event.Name != name {
			t.Fatalf("expected event %s, got %+v", name, event)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for event %s", name)
	}
}
