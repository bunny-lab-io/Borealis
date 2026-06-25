package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type notificationBroadcaster interface {
	broadcastNotification(ctx context.Context, payload map[string]any) error
}

func registerNotificationRoutes(mux *http.ServeMux, auth *authService, broadcaster notificationBroadcaster) {
	mux.HandleFunc("POST /api/notifications/notify", notificationNotifyHandler(auth, broadcaster))
}

func notificationNotifyHandler(auth *authService, broadcaster notificationBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		user, failure := requireUser(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}

		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body)
		}
		if body == nil {
			body = map[string]any{}
		}

		message := cleanText(body["message"])
		if message == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_payload",
				"message": "Notification message is required.",
			})
			return
		}

		now := time.Now()
		variant := strings.ToLower(firstText(
			cleanText(body["variant"]),
			cleanText(body["type"]),
			cleanText(body["severity"]),
			"info",
		))
		switch variant {
		case "info", "warning", "error":
		default:
			variant = "info"
		}

		payload := map[string]any{
			"id":         fmt.Sprintf("notif-%d-%d", now.Unix(), now.UnixMilli()%1000),
			"title":      firstText(cleanText(body["title"]), "Notification"),
			"message":    message,
			"icon":       firstText(cleanText(body["icon"]), "NotificationsActive"),
			"variant":    variant,
			"username":   user.Username,
			"role":       firstText(user.Role, "User"),
			"created_at": now.Unix(),
		}

		if broadcaster != nil {
			if err := broadcaster.broadcastNotification(r.Context(), payload); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"error":   "notification_broadcast_failed",
					"message": err.Error(),
				})
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "sent",
			"notification": payload,
		})
	}
}
