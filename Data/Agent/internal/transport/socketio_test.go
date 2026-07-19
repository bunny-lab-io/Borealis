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

func TestSocketURLPreservesRoutePrefix(t *testing.T) {
	got, err := socketURL("https://borealis.example.com/_borealis/site-workers/worker-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://borealis.example.com/_borealis/site-workers/worker-1/socket.io/?EIO=4&transport=websocket"
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

func TestConnectRunsAfterAckResponseCallback(t *testing.T) {
	afterAckCh := make(chan struct{}, 1)
	ack := runSocketAckScenario(t, `4211["agent_maintenance_request",{"operation_id":"op-1"}]`, func(client *Client) {
		client.On("agent_maintenance_request", func(ctx context.Context, payload any) (any, error) {
			return NewAfterAckResponse(
				map[string]any{"status": "ok", "operation_id": "op-1"},
				func() { afterAckCh <- struct{}{} },
			), nil
		})
	})

	if !strings.HasPrefix(ack, "4311") {
		t.Fatalf("ack = %q, want ack id 11", ack)
	}
	values := decodeAckPayload(t, strings.TrimPrefix(ack, "4311"))
	if values[0]["status"] != "ok" || values[0]["operation_id"] != "op-1" {
		t.Fatalf("unexpected ack payload: %#v", values)
	}
	select {
	case <-afterAckCh:
	case <-time.After(time.Second):
		t.Fatal("after-ack callback did not run")
	}
}

func TestConnectAcksUnsupportedEvent(t *testing.T) {
	ack := runSocketAckScenario(t, `429["missing_event",{}]`, nil)
	if ack != `439[{"error":"unsupported_event"}]` {
		t.Fatalf("ack = %q", ack)
	}
}

func TestConnectReturnsAfterReadIdleTimeout(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket.io/" {
			t.Errorf("path = %q", r.URL.Path)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte("0{}")); err != nil {
			t.Errorf("write open failed: %v", err)
			return
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read namespace connect failed: %v", err)
			return
		}
		if string(msg) != "40" {
			t.Errorf("namespace connect = %q", string(msg))
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"agent-sid"}`)); err != nil {
			t.Errorf("write namespace ack failed: %v", err)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	client.SetConnectTimeout(time.Second)
	client.SetReadIdleTimeout(40 * time.Millisecond)
	client.OnConnected(func(context.Context) error {
		connected <- struct{}{}
		return nil
	})
	err := client.Connect(context.Background())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("Connect error = %v, want timeout", err)
	}
	select {
	case <-connected:
	default:
		t.Fatal("socket did not reach connected state before idle timeout")
	}
}

func TestConnectRespondsToPingWhileEventHandlerRuns(t *testing.T) {
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	pongCh := make(chan string, 1)
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"agent-sid"}`)); err != nil {
			errCh <- err
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`42["vpn_tunnel_start",{"agent_id":"A"}]`)); err != nil {
			errCh <- err
			return
		}
		select {
		case <-handlerStarted:
		case <-time.After(time.Second):
			errCh <- fmt.Errorf("handler did not start")
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("2")); err != nil {
			errCh <- err
			return
		}
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			errCh <- err
			return
		}
		_, pong, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		pongCh <- string(pong)
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	client.On("vpn_tunnel_start", func(ctx context.Context, payload any) (any, error) {
		close(handlerStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseHandler:
			return map[string]any{"ok": true}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Connect(ctx)
	}()

	select {
	case err := <-errCh:
		close(releaseHandler)
		t.Fatal(err)
	case pong := <-pongCh:
		if pong != "3" {
			close(releaseHandler)
			t.Fatalf("pong = %q, want Engine.IO pong", pong)
		}
	case <-time.After(3 * time.Second):
		close(releaseHandler)
		t.Fatal("timed out waiting for pong")
	}
	close(releaseHandler)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect exit")
	}
}

func TestConnectRespondsToPingWhileAckHandlerRuns(t *testing.T) {
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	pongCh := make(chan string, 1)
	ackCh := make(chan string, 1)
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"agent-sid"}`)); err != nil {
			errCh <- err
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`4215["service_control_action",{"name":"svc"}]`)); err != nil {
			errCh <- err
			return
		}
		select {
		case <-handlerStarted:
		case <-time.After(time.Second):
			errCh <- fmt.Errorf("handler did not start")
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("2")); err != nil {
			errCh <- err
			return
		}
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			errCh <- err
			return
		}
		_, pong, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		pongCh <- string(pong)
		<-releaseHandler
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
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
	client.On("service_control_action", func(ctx context.Context, payload any) (any, error) {
		close(handlerStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseHandler:
			return map[string]any{"ok": true}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Connect(ctx)
	}()

	select {
	case err := <-errCh:
		close(releaseHandler)
		t.Fatal(err)
	case pong := <-pongCh:
		if pong != "3" {
			close(releaseHandler)
			t.Fatalf("pong = %q, want Engine.IO pong", pong)
		}
	case <-time.After(3 * time.Second):
		close(releaseHandler)
		t.Fatal("timed out waiting for pong")
	}
	close(releaseHandler)
	select {
	case err := <-errCh:
		t.Fatal(err)
	case ack := <-ackCh:
		if !strings.HasPrefix(ack, "4315") {
			t.Fatalf("ack = %q, want ack id 15", ack)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ack")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect exit")
	}
}

func TestConnectInvokesOnConnectedAfterNamespaceConnect(t *testing.T) {
	connectedCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"agent-sid"}`)); err != nil {
			errCh <- err
			return
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	client.OnConnected(func(ctx context.Context) error {
		connectedCh <- struct{}{}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Connect(ctx)
	}()
	select {
	case err := <-errCh:
		t.Fatal(err)
	case <-connectedCh:
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for connect exit")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for namespace connect")
	}
}

func TestConnectInvokesActivityCallbackOnIncomingTraffic(t *testing.T) {
	activityCh := make(chan struct{}, 3)
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`40{"sid":"agent-sid"}`)); err != nil {
			errCh <- err
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte("2")); err != nil {
			errCh <- err
			return
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	client.OnActivity(func() {
		activityCh <- struct{}{}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Connect(ctx)
	}()

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			t.Fatal(err)
		case <-activityCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for socket activity")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect exit")
	}
}

func TestConnectTimesOutWaitingForNamespaceConnect(t *testing.T) {
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	client := NewClient(server.URL, nil)
	client.SetConnectTimeout(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.Connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "socket namespace connect timeout") {
		t.Fatalf("Connect error = %v, want namespace connect timeout", err)
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
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
