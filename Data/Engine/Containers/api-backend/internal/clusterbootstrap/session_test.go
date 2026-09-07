package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sessionFixture() SessionRequest {
	return SessionRequest{Protocol: 1, Action: "verify", Nonce: strings.Repeat("c", 64), Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), ManagerSHA256: strings.Repeat("b", 64), AllowQualification: true,
		Binding: SessionBinding{ClusterID: "11111111-1111-4111-8111-111111111111", OperationID: "22222222-2222-4222-8222-222222222222", TargetID: "33333333-3333-4333-8333-333333333333", HolderID: "44444444-4444-4444-8444-444444444444", Generation: 7, OperationAttempt: 2,
			Address: "192.168.3.251", Port: 22, Hostname: "engine-02", MachineID: strings.Repeat("a", 32), HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}}
}

type sessionFixtureClient struct {
	in   *io.PipeWriter
	out  *io.PipeReader
	done chan struct{}
	err  error
}

func newSessionFixture(t *testing.T, verify func(context.Context, SessionRequest) error) *sessionFixtureClient {
	t.Helper()
	in, writer := io.Pipe()
	reader, out := io.Pipe()
	c := &sessionFixtureClient{in: writer, out: reader, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	go func() {
		c.err = serveSession(ctx, in, out, sessionFixture().Nonce, verify, sessionTiming{10 * time.Millisecond, 150 * time.Millisecond, 100 * time.Millisecond})
		close(c.done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = writer.Close()
		_ = reader.Close()
		select {
		case <-c.done:
		case <-time.After(time.Second):
			t.Error("bootstrap session did not stop")
		}
	})
	return c
}

func (c *sessionFixtureClient) start(t *testing.T, password string) SessionMessage {
	t.Helper()
	r := sessionFixture()
	preamble, _ := SessionPreamble(r.Nonce)
	if _, err := io.WriteString(c.in, password+preamble); err != nil {
		t.Fatal(err)
	}
	var ready SessionMessage
	if ReadSessionFrame(c.out, &ready) != nil || ready.State != "ready" || ready.Nonce != r.Nonce {
		t.Fatal("missing bound ready message")
	}
	if WriteSessionFrame(c.in, r) != nil {
		t.Fatal("request rejected")
	}
	return c.challenge(t)
}

func (c *sessionFixtureClient) challenge(t *testing.T) SessionMessage {
	t.Helper()
	var challenge SessionMessage
	if ReadSessionFrame(c.out, &challenge) != nil || challenge.Protocol != 1 || challenge.State != "challenge" || !digestPattern.MatchString(challenge.Nonce) {
		t.Fatal("missing bounded authority challenge")
	}
	return challenge
}

func (c *sessionFixtureClient) heartbeat(t *testing.T, challenge SessionMessage) {
	t.Helper()
	if WriteSessionFrame(c.in, SessionMessage{Protocol: 1, State: "heartbeat", Nonce: challenge.Nonce}) != nil {
		t.Fatal("heartbeat rejected")
	}
}

func (c *sessionFixtureClient) result(t *testing.T) error {
	t.Helper()
	select {
	case <-c.done:
		return c.err
	case <-time.After(time.Second):
		t.Fatal("session exceeded test bound")
		return nil
	}
}

func TestBootstrapSessionRequiresFreshChallengesAndExactCompletion(t *testing.T) {
	for name, password := range map[string]string{"consumed": "", "empty unused": "\n", "unused syntax": " $(unexecuted-fixture); private password \n"} {
		t.Run(name, func(t *testing.T) {
			finish := make(chan struct{})
			c := newSessionFixture(t, func(ctx context.Context, request SessionRequest) error {
				if request != sessionFixture() {
					return errors.New("binding changed")
				}
				select {
				case <-finish:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			challenge := c.start(t, password)
			for n := 0; n < 3; n++ {
				c.heartbeat(t, challenge)
				if n < 2 {
					next := c.challenge(t)
					if next.Nonce == challenge.Nonce {
						t.Fatal("challenge reused")
					}
					challenge = next
				}
			}
			close(finish)
			var done SessionMessage
			if ReadSessionFrame(c.out, &done) != nil || done.State != "verified" || done.Nonce != sessionFixture().Nonce || done.Binding == nil || *done.Binding != sessionFixture().Binding || done.SourceSHA != sessionFixture().SourceSHA || done.ManagerSHA256 != sessionFixture().ManagerSHA256 {
				t.Fatal("completion not bound to exact request")
			}
			if err := c.result(t); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBootstrapSessionStopsOnExpiredReplayedAndDisconnectedAuthority(t *testing.T) {
	for _, mode := range []string{"missing initial confirmation", "expired", "replayed", "wrong nonce", "disconnect", "blocked output"} {
		t.Run(mode, func(t *testing.T) {
			var called atomic.Bool
			stopped := make(chan struct{})
			c := newSessionFixture(t, func(ctx context.Context, _ SessionRequest) error {
				called.Store(true)
				<-ctx.Done()
				close(stopped)
				return ctx.Err()
			})
			first := c.start(t, "")
			if mode != "missing initial confirmation" {
				c.heartbeat(t, first)
				if mode != "blocked output" {
					next := c.challenge(t)
					switch mode {
					case "replayed":
						c.heartbeat(t, first)
					case "wrong nonce":
						next.Nonce = strings.Repeat("0", 64)
						c.heartbeat(t, next)
					case "disconnect":
						_ = c.in.Close()
					}
				}
			}
			if err := c.result(t); err == nil {
				t.Fatal("lost authority accepted")
			}
			if mode == "missing initial confirmation" {
				if called.Load() {
					t.Fatal("work started before fresh authority confirmation")
				}
			} else {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("work context survived authority loss")
				}
			}
		})
	}
}

func TestBootstrapSessionRejectsInvalidFramingAndWriteActions(t *testing.T) {
	r := sessionFixture()
	if r.Validate() != nil {
		t.Fatal("fixture identity invalid")
	}
	for _, action := range []string{"prepare", "join", "repair_identity", "shell", ""} {
		changed := r
		changed.Action = action
		if changed.Validate() == nil {
			t.Fatal("unimplemented host mutation authorized")
		}
	}
	var valid bytes.Buffer
	if WriteSessionFrame(&valid, r) != nil {
		t.Fatal("frame failed")
	}
	for _, body := range []string{
		`{"protocol":1,"protocol":1}`, `{"Protocol":1}`, `{"protocol":null}`, `{"unknown":1}`, `{"protocol":1} {}`, `{"protocol":[]}`,
	} {
		var frame bytes.Buffer
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(body)))
		frame.Write(header[:])
		frame.WriteString(body)
		var decoded SessionRequest
		if ReadSessionFrame(&frame, &decoded) == nil {
			t.Fatal("ambiguous frame accepted")
		}
	}
	for _, raw := range [][]byte{{}, {0, 0, 0, 0}, {0, 1, 0, 0}, valid.Bytes()[:valid.Len()-1]} {
		var decoded SessionRequest
		if ReadSessionFrame(bytes.NewReader(raw), &decoded) == nil {
			t.Fatal("unbounded or truncated frame accepted")
		}
	}
	for _, prefix := range []string{strings.Repeat("x", 4097) + "\n", "bad\rpassword\n", "one\ntwo\n", "\xff\n"} {
		c := newSessionFixture(t, func(context.Context, SessionRequest) error { t.Error("invalid preamble started verifier"); return nil })
		preamble, _ := SessionPreamble(r.Nonce)
		_, _ = io.WriteString(c.in, prefix+preamble)
		if c.result(t) == nil {
			t.Fatal("invalid optional sudo line accepted")
		}
	}
}
