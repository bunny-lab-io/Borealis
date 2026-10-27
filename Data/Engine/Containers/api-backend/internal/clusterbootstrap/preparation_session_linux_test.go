package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreparationSessionRejectsForgedStream(t *testing.T) {
	expected, _ := preparationFixture()
	request, err := NewPreparationSessionRequest(expected, strings.Repeat("b", 64), strings.Repeat("a", 64), "stage_source")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := request.Digest()
	for _, mode := range []string{"journal step", "sequence", "nonce", "request digest", "protocol", "base64", "oversized chunk", "empty end", "duplicate chunk", "heartbeat replay", "missing heartbeat"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			result := make(chan error, 1)
			var consumed atomic.Bool
			go func() {
				result <- servePreparationSession(ctx, server, server, request.Session.Nonce,
					func(context.Context, PreparationSessionRequest) error { return nil },
					func(_ context.Context, _ PreparationSessionRequest, input io.Reader) error {
						_, err := io.Copy(io.Discard, input)
						if err == nil {
							consumed.Store(true)
						}
						return err
					},
					func(time.Time) error { return nil }, sessionTiming{time.Second, 500 * time.Millisecond, time.Second})
			}()
			preamble, _ := SessionPreamble(request.Session.Nonce)
			if _, err := io.WriteString(client, preamble); err != nil {
				t.Fatal(err)
			}
			var frame PreparationSessionFrame
			if ReadSessionFrame(client, &frame) != nil || frame.State != "ready" || WriteSessionFrame(client, request) != nil {
				t.Fatal("handshake failed")
			}
			if ReadSessionFrame(client, &frame) != nil || frame.State != "challenge" {
				t.Fatal("challenge missing")
			}
			heartbeat := frame
			heartbeat.State = "heartbeat"
			if mode == "missing heartbeat" {
				if err := <-result; err == nil || consumed.Load() {
					t.Fatal("expired authority accepted")
				}
				return
			}
			if WriteSessionFrame(client, heartbeat) != nil || ReadSessionFrame(client, &frame) != nil || frame.State != "source_ready" {
				t.Fatal("verification failed")
			}
			frame = PreparationSessionFrame{Protocol: 1, State: "chunk", Nonce: request.Session.Nonce, RequestSHA256: digest, Data: base64.StdEncoding.EncodeToString([]byte("private fixture"))}
			switch mode {
			case "journal step":
				frame.JournalStep = "prepare_host"
			case "sequence":
				frame.Sequence = 1
			case "nonce":
				frame.Nonce = strings.Repeat("f", 64)
			case "request digest":
				frame.RequestSHA256 = strings.Repeat("f", 64)
			case "protocol":
				frame.Protocol = 2
			case "base64":
				frame.Data = "invalid!"
			case "oversized chunk":
				frame.Data = base64.StdEncoding.EncodeToString(make([]byte, PreparationChunkBytes+1))
			case "empty end":
				frame.State = "end"
				frame.Data = ""
			case "heartbeat replay":
				frame = heartbeat
			case "duplicate chunk":
				if WriteSessionFrame(client, frame) != nil {
					t.Fatal("first chunk rejected")
				}
				var ack PreparationSessionFrame
				if ReadSessionFrame(client, &ack) != nil || ack.State != "ack" {
					t.Fatal("ack missing")
				}
			}
			_ = WriteSessionFrame(client, frame)
			if err := <-result; err == nil || consumed.Load() {
				t.Fatal("forged stream accepted")
			}
		})
	}
}

func TestPreparationSessionTransfer(t *testing.T) {
	p, journal, _, destination := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	_, manager, _, err := p.bundle.ManagerProof()
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewPreparationSessionRequest(expected, manager, strings.Repeat("a", 64), "stage_source")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	serverResult := make(chan error, 1)
	var verified, received atomic.Bool
	var refreshed atomic.Int64
	go func() {
		serverResult <- servePreparationSession(ctx, server, server, request.Session.Nonce,
			func(context.Context, PreparationSessionRequest) error { verified.Store(true); return nil },
			func(ctx context.Context, got PreparationSessionRequest, input io.Reader) error {
				if !verified.Load() || got != request {
					return ErrSessionProtocol
				}
				inputs, err := ReceivePreparation(ctx, mutationTempDir(t), expected, input, mutationAuthority)
				if err != nil {
					return err
				}
				defer inputs.Close()
				var trailing [1]byte
				if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF {
					return ErrSessionProtocol
				}
				if _, err := inputs.publishSource(ctx, journal, destination); err != nil {
					return err
				}
				received.Store(true)
				return nil
			}, func(time.Time) error { return nil }, sessionTiming{100 * time.Millisecond, 5 * time.Second, time.Second})
	}()
	err = ExchangePreparationSession(ctx, client, client, request, func(ctx context.Context, output io.Writer) error {
		if !verified.Load() {
			return errors.New("private inputs sent before verification")
		}
		return p.WritePreparation(ctx, expected, output, mutationAuthority)
	}, func(context.Context) error { refreshed.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
	if !received.Load() || refreshed.Load() < 3 {
		t.Fatal("missing publication or authority renewal")
	}
}

func TestPreparationSessionRequestBinding(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	_, digest, _, _ := p.bundle.ManagerProof()
	request, err := NewPreparationSessionRequest(p.configuration.wire.Expected, digest, strings.Repeat("a", 64), "stage_source")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"action", "joined action", "generation", "source", "private digest", "expected", "nonce"} {
		t.Run(mode, func(t *testing.T) {
			r := request
			switch mode {
			case "action":
				r.Action = "join_cluster"
			case "joined action":
				r.Session.Action = "deliver_identity_recovery"
			case "generation":
				r.Session.Binding.Generation++
			case "source":
				r.Session.SourceSHA = strings.Repeat("b", 40)
			case "private digest":
				r.Session.ManagerSHA256 = "private"
			case "expected":
				r.ExpectedBase64 += "\n"
			case "nonce":
				r.Session.Nonce = "private"
			}
			if _, err := r.Digest(); err == nil {
				t.Fatal("unbound request accepted")
			}
		})
	}
	frame := PreparationSessionFrame{Data: "private payload"}
	for _, rendered := range []string{fmt.Sprint(frame), fmt.Sprintf("%+v", frame), fmt.Sprintf("%#v", frame)} {
		if strings.Contains(rendered, frame.Data) {
			t.Fatal("frame leaked private data")
		}
	}
}

func TestPreparationSessionFailureCleanup(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	_, digest, _, _ := p.bundle.ManagerProof()
	request, _ := NewPreparationSessionRequest(p.configuration.wire.Expected, digest, strings.Repeat("a", 64), "stage_source")
	for _, mode := range []string{"native rejection", "authority loss", "producer failure", "receiver failure", "receiver omitted EOF", "cancel", "empty"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			serverResult := make(chan error, 1)
			var producerEntered, producerExited, receiverExited atomic.Bool
			var checks atomic.Int64
			go func() {
				serverResult <- servePreparationSession(ctx, server, server, request.Session.Nonce,
					func(context.Context, PreparationSessionRequest) error {
						if mode == "native rejection" {
							return errors.New("private native error")
						}
						return nil
					}, func(ctx context.Context, _ PreparationSessionRequest, input io.Reader) error {
						defer receiverExited.Store(true)
						if mode == "receiver failure" {
							return errors.New("private receiver error")
						}
						if mode == "receiver omitted EOF" {
							return nil
						}
						_, err := io.Copy(io.Discard, input)
						return err
					}, func(time.Time) error { return nil }, sessionTiming{10 * time.Millisecond, time.Second, time.Second})
			}()
			err := ExchangePreparationSession(ctx, client, client, request, func(ctx context.Context, output io.Writer) error {
				producerEntered.Store(true)
				defer producerExited.Store(true)
				if mode == "producer failure" {
					return errors.New("private producer error")
				}
				if mode == "empty" {
					return nil
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "authority loss" {
					<-ctx.Done()
					return ctx.Err()
				}
				_, err := io.Copy(output, bytes.NewReader(bytes.Repeat([]byte("secret"), PreparationChunkBytes)))
				return err
			}, func(context.Context) error {
				if checks.Add(1) > 2 && mode == "authority loss" {
					return errors.New("private authority error")
				}
				return nil
			})
			if err == nil || strings.Contains(err.Error(), "private ") {
				t.Fatal("failed session accepted or leaked", err)
			}
			if serverErr := <-serverResult; serverErr == nil {
				t.Fatal("failed receiver returned success")
			}
			if producerEntered.Load() && !producerExited.Load() {
				t.Fatal("producer escaped session")
			}
			if mode == "native rejection" && producerEntered.Load() {
				t.Fatal("private source sent before native verification")
			}
			if mode != "native rejection" && !receiverExited.Load() {
				t.Fatal("receiver escaped session")
			}
		})
	}
}
