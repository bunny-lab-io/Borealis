package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type notificationBroadcaster interface {
	broadcastNotification(ctx context.Context, payload map[string]any) error
}

type legacyNotificationBroadcaster struct {
	baseURL *url.URL
	auth    *authService
	client  *http.Client
}

func registerNotificationRoutes(mux *http.ServeMux, auth *authService, legacyURL *url.URL) {
	mux.HandleFunc("POST /api/notifications/notify", notificationNotifyHandler(auth, &legacyNotificationBroadcaster{
		baseURL: legacyURL,
		auth:    auth,
		client:  &http.Client{Timeout: 3 * time.Second},
	}))
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

func (b *legacyNotificationBroadcaster) broadcastNotification(ctx context.Context, payload map[string]any) error {
	if b == nil || b.baseURL == nil || b.auth == nil || b.auth.verifier == nil || len(b.auth.verifier.secret) == 0 {
		return errors.New("notification broadcaster unavailable")
	}
	client := b.client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	body, err := json.Marshal(map[string]any{"notification": payload})
	if err != nil {
		return err
	}
	target := b.baseURL.ResolveReference(&url.URL{Path: "/api/internal/notifications/broadcast"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(b.auth.verifier.secret))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("legacy notification broadcaster returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
