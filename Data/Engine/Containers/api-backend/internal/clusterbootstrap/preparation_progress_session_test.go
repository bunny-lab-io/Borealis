package clusterbootstrap

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPreparationProgressSession(t *testing.T) {
	for _, step := range []string{"empty", "stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "", "join_cluster"} {
		t.Run(step, func(t *testing.T) {
			expected, _ := preparationFixture()
			request, err := NewPreparationSessionRequest(expected, strings.Repeat("b", 64), strings.Repeat("a", 64), "inspect_preparation_progress")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			result := make(chan error, 1)
			go func() {
				result <- ServePreparationSessionWithProgress(ctx, server, server, request.Session.Nonce,
					func(context.Context, PreparationSessionRequest) error { return nil },
					func(_ context.Context, got PreparationSessionRequest, stream io.Reader) (PreparationProgress, error) {
						data, err := io.ReadAll(stream)
						if err != nil || string(data) != "private fixture" || got != request {
							return PreparationProgress{}, ErrSessionProtocol
						}
						return PreparationProgress{Step: step}, nil
					}, func(time.Time) error { return nil })
			}()
			got, err := ExchangePreparationProgressSession(ctx, client, client, request,
				func(_ context.Context, output io.Writer) error {
					_, err := io.WriteString(output, "private fixture")
					return err
				}, mutationAuthority)
			serverErr := <-result
			valid := (PreparationProgress{Step: step}).Validate() == nil
			if valid && (err != nil || serverErr != nil || got.Step != step) {
				t.Fatal("valid cursor lost", got, err, serverErr)
			}
			if !valid && (err == nil || serverErr == nil || got != (PreparationProgress{})) {
				t.Fatal("invalid cursor accepted")
			}
		})
	}
}

func TestPreparationProgressSenderRejectsForgedCursor(t *testing.T) {
	for _, mode := range []string{"missing", "unknown", "early", "nonce", "digest", "sequence", "trailing", "authority", "ordinary action"} {
		t.Run(mode, func(t *testing.T) {
			expected, _ := preparationFixture()
			action := "inspect_preparation_progress"
			if mode == "ordinary action" {
				action = "stage_source"
			}
			request, _ := NewPreparationSessionRequest(expected, strings.Repeat("b", 64), strings.Repeat("a", 64), action)
			digest, _ := request.Digest()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()
				r := bufio.NewReader(server)
				if readSessionPreamble(r, request.Session.Nonce) != nil {
					return
				}
				if WriteSessionFrame(server, PreparationSessionFrame{Protocol: 1, State: "ready", Nonce: request.Session.Nonce}) != nil {
					return
				}
				var req PreparationSessionRequest
				if ReadSessionFrame(r, &req) != nil {
					return
				}
				frame := PreparationSessionFrame{Protocol: 1, State: "challenge", Nonce: strings.Repeat("c", 64), RequestSHA256: digest}
				if mode == "early" {
					frame.JournalStep = "prepare_host"
				}
				if WriteSessionFrame(server, frame) != nil {
					return
				}
				var incoming PreparationSessionFrame
				if ReadSessionFrame(r, &incoming) != nil {
					return
				}
				frame.State, frame.Nonce, frame.JournalStep = "source_ready", request.Session.Nonce, ""
				if WriteSessionFrame(server, frame) != nil {
					return
				}
				for {
					if ReadSessionFrame(r, &incoming) != nil {
						return
					}
					if incoming.State == "end" {
						break
					}
					frame.State, frame.Sequence = "ack", incoming.Sequence
					if WriteSessionFrame(server, frame) != nil {
						return
					}
				}
				frame.State, frame.Sequence, frame.JournalStep = "verified", incoming.Sequence, "prepare_host"
				switch mode {
				case "missing":
					frame.JournalStep = ""
				case "unknown":
					frame.JournalStep = "join_cluster"
				case "nonce":
					frame.Nonce = strings.Repeat("d", 64)
				case "digest":
					frame.RequestSHA256 = strings.Repeat("d", 64)
				case "sequence":
					frame.Sequence++
				}
				_ = WriteSessionFrame(server, frame)
				if mode == "trailing" {
					_, _ = io.WriteString(server, "x")
				}
			}()
			n := 0
			check := func(context.Context) error {
				n++
				if mode == "authority" && n >= 3 {
					return ErrSessionAuthority
				}
				return nil
			}
			produce := func(_ context.Context, output io.Writer) error {
				_, err := io.WriteString(output, "private fixture")
				return err
			}
			var got PreparationProgress
			var err error
			if mode == "ordinary action" {
				err = ExchangePreparationSession(ctx, client, client, request, produce, check)
			} else {
				got, err = ExchangePreparationProgressSession(ctx, client, client, request, produce, check)
			}
			<-done
			if err == nil || got != (PreparationProgress{}) {
				t.Fatal("forged cursor escaped", got, err)
			}
		})
	}
}
