package transport

import "testing"

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
