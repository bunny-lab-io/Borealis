package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const operatorRealtimeEventsPath = "/api/realtime/events"

var allowedOperatorDeviceEvents = map[string]struct{}{
	"device_inventory_changed": {},
	"device_services_changed":  {},
}

type operatorRealtimeEvent struct {
	Name      string
	Payload   map[string]any
	CreatedAt time.Time
}

type operatorRealtimeHub struct {
	mu          sync.RWMutex
	subscribers map[chan operatorRealtimeEvent]struct{}
}

func newOperatorRealtimeHub() *operatorRealtimeHub {
	return &operatorRealtimeHub{subscribers: map[chan operatorRealtimeEvent]struct{}{}}
}

func registerRealtimeRoutes(mux *http.ServeMux, auth *authService, hub *operatorRealtimeHub) {
	mux.HandleFunc("GET "+operatorRealtimeEventsPath, operatorRealtimeEventsHandler(auth, hub))
}

func operatorRealtimeEventsHandler(auth *authService, hub *operatorRealtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if hub == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "operator_realtime_unavailable"})
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming_unavailable"})
			return
		}

		header := w.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache, no-transform")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")

		events := hub.subscribe()
		defer hub.unsubscribe(events)

		_ = writeSSEEvent(w, operatorRealtimeEvent{
			Name:      "realtime_ready",
			Payload:   map[string]any{"status": "ok"},
			CreatedAt: time.Now().UTC(),
		})
		flusher.Flush()

		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case event := <-events:
				if err := writeSSEEvent(w, event); err != nil {
					return
				}
				flusher.Flush()
			case <-ping.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

func (h *operatorRealtimeHub) subscribe() chan operatorRealtimeEvent {
	ch := make(chan operatorRealtimeEvent, 32)
	h.mu.Lock()
	if h.subscribers == nil {
		h.subscribers = map[chan operatorRealtimeEvent]struct{}{}
	}
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *operatorRealtimeHub) unsubscribe(ch chan operatorRealtimeEvent) {
	if h == nil || ch == nil {
		return
	}
	h.mu.Lock()
	delete(h.subscribers, ch)
	h.mu.Unlock()
}

func (h *operatorRealtimeHub) emit(eventName string, payload map[string]any) error {
	if h == nil {
		return errors.New("operator realtime hub unavailable")
	}
	eventName = cleanText(eventName)
	if eventName == "" {
		return errors.New("event_name_required")
	}
	event := operatorRealtimeEvent{
		Name:      eventName,
		Payload:   copyMap(payload),
		CreatedAt: time.Now().UTC(),
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Operator fanout is best-effort; stale clients must not block ingest.
		}
	}
	return nil
}

func (h *operatorRealtimeHub) broadcastAgentStatus(ctx context.Context, payload map[string]any) error {
	_ = ctx
	return h.emit("agent_status_changed", payload)
}

func (h *operatorRealtimeHub) broadcastDeviceEvent(ctx context.Context, eventName string, payload map[string]any) error {
	_ = ctx
	eventName = cleanText(eventName)
	if _, ok := allowedOperatorDeviceEvents[eventName]; !ok {
		return errors.New("invalid_device_event")
	}
	return h.emit(eventName, payload)
}

func (h *operatorRealtimeHub) broadcastNotification(ctx context.Context, payload map[string]any) error {
	_ = ctx
	return h.emit("borealis_notification", payload)
}

func (h *operatorRealtimeHub) broadcastWatchdogIncidents(ctx context.Context, payload map[string]any) error {
	_ = ctx
	return h.emit("watchdog_incidents_changed", payload)
}

func writeSSEEvent(w http.ResponseWriter, event operatorRealtimeEvent) error {
	name := cleanText(event.Name)
	if name == "" {
		return errors.New("event_name_required")
	}
	payload := event.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	id := event.CreatedAt.UnixNano()
	if id == 0 {
		id = time.Now().UTC().UnixNano()
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, sanitizeSSEEventName(name), encoded)
	return err
}

func sanitizeSSEEventName(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return -1
	}, value)
}
