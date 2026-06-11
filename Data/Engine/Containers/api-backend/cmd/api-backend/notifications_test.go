package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeNotificationBroadcaster struct {
	payload map[string]any
	err     error
}

func (b *fakeNotificationBroadcaster) broadcastNotification(_ context.Context, payload map[string]any) error {
	b.payload = copyMap(payload)
	return b.err
}

func TestNotificationNotifyHandlerBuildsScopedPayload(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	broadcaster := &fakeNotificationBroadcaster{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/notifications/notify", strings.NewReader(`{"title":"Done","message":"Job finished","icon":"Check","variant":"success"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	notificationNotifyHandler(auth, broadcaster).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if broadcaster.payload["username"] != "operator" || broadcaster.payload["role"] != "Admin" {
		t.Fatalf("expected scoped operator payload, got %+v", broadcaster.payload)
	}
	if broadcaster.payload["variant"] != "info" || broadcaster.payload["icon"] != "Check" {
		t.Fatalf("expected normalized variant/icon, got %+v", broadcaster.payload)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "sent" {
		t.Fatalf("unexpected response %+v", response)
	}
}

func TestNotificationNotifyHandlerRequiresMessage(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/notifications/notify", strings.NewReader(`{"title":"Nope"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	notificationNotifyHandler(auth, &fakeNotificationBroadcaster{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_payload") {
		t.Fatalf("expected invalid payload, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNotificationNotifyHandlerBroadcastFailure(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/notifications/notify", strings.NewReader(`{"message":"Job finished"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	notificationNotifyHandler(auth, &fakeNotificationBroadcaster{err: errors.New("offline")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "notification_broadcast_failed") {
		t.Fatalf("expected broadcast failure, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
