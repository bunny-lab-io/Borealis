package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSocketURL(t *testing.T) {
	got, err := socketURL("https://borealis.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://borealis.example.com/socket.io/?EIO=4&transport=websocket"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestParseEventPacket(t *testing.T) {
	event, payload, ackID, err := parseEventPacket(`12["quick_job_run",{"job_id":7}]`)
	if err != nil {
		t.Fatal(err)
	}
	if event != "quick_job_run" || ackID != "12" {
		t.Fatalf("event=%q ack=%q", event, ackID)
	}
	obj := payload.(map[string]any)
	if obj["job_id"].(float64) != 7 {
		t.Fatalf("payload mismatch: %#v", payload)
	}
}

func TestEncodeEventPacket(t *testing.T) {
	packet, err := encodeEventPacket("connect_agent", map[string]any{"agent_id": "A"})
	if err != nil {
		t.Fatal(err)
	}
	if packet != `42["connect_agent",{"agent_id":"A"}]` {
		t.Fatalf("packet = %q", packet)
	}
}

func TestConnectAcksHandledEvent(t *testing.T) {
	ack := runSocketAckScenario(t, `427["quick_job_run",{"job_id":7}]`, func(client *Client) {
		client.On("quick_job_run", func(ctx context.Context, payload any) (any, error) {
			obj, ok := payload.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("payload type %T", payload)
			}
			return map[string]any{
				"status": "ok",
				"job_id": int(obj["job_id"].(float64)),
			}, nil
		})
	})

	if !strings.HasPrefix(ack, "437") {
		t.Fatalf("ack = %q, want ack id 7", ack)
	}
	values := decodeAckPayload(t, strings.TrimPrefix(ack, "437"))
	if values[0]["status"] != "ok" || values[0]["job_id"].(float64) != 7 {
		t.Fatalf("unexpected ack payload: %#v", values)
	}
}

func TestConnectAcksUnsupportedEvent(t *testing.T) {
	ack := runSocketAckScenario(t, `429["missing_event",{}]`, nil)
	if ack != `439[{"error":"unsupported_event"}]` {
		t.Fatalf("ack = %q", ack)
	}
}

func runSocketAckScenario(t *testing.T, eventPacket string, register func(*Client)) string {
	t.Helper()

	ackCh := make(chan string, 1)
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket.io/" {
			errCh <- fmt.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte("0{}")); err != nil {
			errCh <- err
			return
		}
		_, message, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if string(message) != "40" {
			errCh <- fmt.Errorf("open ack = %q", string(message))
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(eventPacket)); err != nil {
			errCh <- err
			return
		}
		_, ack, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		ackCh <- string(ack)
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	if register != nil {
		register(client)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Connect(ctx)
	}()

	select {
	case err := <-errCh:
		t.Fatal(err)
	case ack := <-ackCh:
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		return ack
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for socket ack")
	}
	return ""
}

func decodeAckPayload(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var values []map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatalf("decode ack payload %q: %v", raw, err)
	}
	return values
}
