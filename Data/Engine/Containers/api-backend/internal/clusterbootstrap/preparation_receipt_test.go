package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func preparationReceiptFixture(expected PreparationExpected, source engineidentity.Binding) PrejoinPreparationReceipt {
	r := PrejoinPreparationReceipt{Version: 1, ExpectedSHA256: preparationReceiptDigest(expected), Source: source,
		Node: K3sJoinedNode{NodeUID: "99999999-9999-4999-8999-999999999999", Hostname: expected.Target.Hostname, MachineID: expected.Target.MachineID, BootID: expected.TargetBootID, Address: expected.Target.Address, K3sVersion: expected.K3sVersion, Ready: true}, MemberID: 42, MemberName: expected.Target.Hostname + "-12345678", ImageTargetsSHA256: strings.Repeat("e", 64)}
	for i, name := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s", "start_k3s", "verify_images", "activate_identity"} {
		r.Steps[i] = PreparationStepReceipt{name, strings.Repeat("a", 64), strings.Repeat("a", 64)}
	}
	r.Steps[8].InputSHA256 = preparationReceiptHash("prepared-k3s-start-v1\x00" + r.Steps[7].ResultSHA256)
	r.Steps[8].ResultSHA256 = startedPreparationReceipt(r.Steps[8].InputSHA256, r.Node, r.MemberID, r.MemberName)
	r.Steps[9].InputSHA256 = preparationReceiptHash("prepared-image-verification-v1\x00" + r.Steps[8].InputSHA256 + "\x00" + r.Steps[8].ResultSHA256 + "\x00" + r.ImageTargetsSHA256)
	r.Steps[9].ResultSHA256 = preparationReceiptDigest(struct{ Domain, Input, Startup, Targets string }{"prepared-image-verification-receipt-v1", r.Steps[9].InputSHA256, r.Steps[8].ResultSHA256, r.ImageTargetsSHA256})
	r.Steps[10].InputSHA256 = preparationReceiptHash("prejoin-identity-activation-v1\x00" + r.Steps[9].InputSHA256 + "\x00" + r.Steps[9].ResultSHA256)
	r.Steps[10].ResultSHA256 = r.Steps[10].InputSHA256
	return r
}

func TestPrejoinPreparationReceipt(t *testing.T) {
	request := artifactSessionRequestFixture(t)
	expected, _ := request.Expected()
	good := preparationReceiptFixture(expected, *request.IdentitySource)
	if good.Validate(expected, *request.IdentitySource) != nil {
		t.Fatal("fixture invalid")
	}
	for _, mode := range []string{"version", "source", "expected", "Node", "member", "name", "images", "stage order", "missing stage", "activation", "startup", "not ready"} {
		t.Run(mode, func(t *testing.T) {
			r := good
			switch mode {
			case "version":
				r.Version++
			case "source":
				r.Source.SourceUID = expected.Target.TargetID
			case "expected":
				expected.Target.Generation++
				defer func() { expected.Target.Generation-- }()
			case "Node":
				r.Node.NodeUID = expected.Target.TargetID
			case "member":
				r.MemberID++
			case "name":
				r.MemberName = "foreign-12345678"
			case "images":
				r.ImageTargetsSHA256 = strings.Repeat("f", 64)
			case "stage order":
				r.Steps[0], r.Steps[1] = r.Steps[1], r.Steps[0]
			case "missing stage":
				r.Steps[2] = PreparationStepReceipt{}
			case "activation":
				r.Steps[10].ResultSHA256 = strings.Repeat("f", 64)
			case "startup":
				r.Steps[8].ResultSHA256 = strings.Repeat("f", 64)
			case "not ready":
				r.Node.Ready = false
			}
			if r.Validate(expected, *request.IdentitySource) == nil {
				t.Fatal("changed receipt accepted")
			}
		})
	}
}

func TestPrejoinPreparationReceiptSession(t *testing.T) {
	for _, mode := range []string{"complete", "missing", "wrong action", "wrong target", "consumer failure", "authority failure", "cursor"} {
		t.Run(mode, func(t *testing.T) {
			request := artifactSessionRequestFixture(t)
			request.Action = "reconcile_prejoin_identity_activation"
			expected, _ := request.Expected()
			receipt := preparationReceiptFixture(expected, *request.IdentitySource)
			if mode == "wrong action" {
				request.Action = "verify_prepared_images"
			}
			if mode == "wrong target" {
				receipt.Node.NodeUID = expected.Target.TargetID
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			done := make(chan error, 1)
			go func() {
				done <- ServeArtifactPreparationSessionWithResult(ctx, server, server, request.Session.Nonce,
					func(context.Context, PreparationSessionRequest) error { return nil },
					func(_ context.Context, _ PreparationSessionRequest, input io.Reader) (PreparationSessionResult, error) {
						if _, err := io.Copy(io.Discard, input); err != nil {
							return PreparationSessionResult{}, err
						}
						value := PreparationSessionResult{Completion: &receipt}
						if mode == "missing" {
							value.Completion = nil
						}
						if mode == "cursor" {
							value.Progress.Step = "activate_identity"
						}
						if mode == "consumer failure" {
							return value, ErrMutationUnknown
						}
						return value, nil
					}, func(time.Time) error { return nil })
			}()
			got, err := ExchangePrejoinPreparationSession(ctx, client, client, request, func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, "private fixture"); return err }, func(context.Context) error {
				if mode == "authority failure" {
					return ErrSessionAuthority
				}
				return nil
			})
			client.Close()
			serverErr := <-done
			if mode == "complete" {
				if err != nil || serverErr != nil || got != receipt {
					t.Fatal("complete receipt lost", err, serverErr)
				}
			} else if err == nil || got != (PrejoinPreparationReceipt{}) {
				t.Fatal("unsafe receipt accepted")
			}
		})
	}
}

func TestPrejoinPreparationReceiptCanonical(t *testing.T) {
	request := artifactSessionRequestFixture(t)
	expected, _ := request.Expected()
	receipt := preparationReceiptFixture(expected, *request.IdentitySource)
	raw, _ := json.Marshal(receipt)
	var object map[string]json.RawMessage
	json.Unmarshal(raw, &object)
	var steps []json.RawMessage
	json.Unmarshal(object["steps"], &steps)
	extra := append(steps, steps[0])
	extraRaw, _ := json.Marshal(extra)
	for _, mode := range []string{"valid", "unknown", "duplicate", "alias", "extra step", "whitespace", "truncated", "oversized", "noncanonical base64"} {
		t.Run(mode, func(t *testing.T) {
			value := string(raw)
			switch mode {
			case "unknown":
				value = strings.Replace(value, `"version":1`, `"version":1,"extra":true`, 1)
			case "duplicate":
				value = strings.Replace(value, `"version":1`, `"version":1,"version":1`, 1)
			case "alias":
				value = strings.Replace(value, `"version"`, `"Version"`, 1)
			case "extra step":
				value = strings.Replace(value, string(object["steps"]), string(extraRaw), 1)
			case "whitespace":
				value = " " + value
			case "truncated":
				value = value[:len(value)-1]
			case "oversized":
				value = strings.Repeat("x", 9<<10)
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(value))
			if mode == "noncanonical base64" {
				encoded += "\n"
			}
			got, err := decodePreparationReceipt(encoded, expected, *request.IdentitySource)
			if (err == nil) != (mode == "valid") || err != nil && got != (PrejoinPreparationReceipt{}) {
				t.Fatal("incorrect canonical receipt decision", err)
			}
		})
	}
}
func TestPrejoinPreparationReceiptSenderRejectsForgedFrame(t *testing.T) {
	for _, mode := range []string{"missing", "unknown", "early", "nonce", "digest", "sequence", "trailing", "authority", "ordinary action"} {
		t.Run(mode, func(t *testing.T) {
			request := artifactSessionRequestFixture(t)
			request.Action = "reconcile_prejoin_identity_activation"
			expected, _ := request.Expected()
			receipt := preparationReceiptFixture(expected, *request.IdentitySource)
			encoded, _ := encodePreparationReceipt(receipt)
			if mode == "ordinary action" {
				request.Action = "verify_prepared_images"
			}
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
					frame.CompletionBase64 = encoded
				}
				if WriteSessionFrame(server, frame) != nil {
					return
				}
				var incoming PreparationSessionFrame
				if ReadSessionFrame(r, &incoming) != nil {
					return
				}
				frame.State, frame.Nonce, frame.CompletionBase64 = "source_ready", request.Session.Nonce, ""
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
				frame.State, frame.Sequence, frame.CompletionBase64 = "verified", incoming.Sequence, encoded
				switch mode {
				case "missing":
					frame.CompletionBase64 = ""
				case "unknown":
					frame.CompletionBase64 = "invalid"
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
			var got PrejoinPreparationReceipt
			var err error
			if mode == "ordinary action" {
				err = ExchangePreparationSession(ctx, client, client, request, produce, check)
			} else {
				got, err = ExchangePrejoinPreparationSession(ctx, client, client, request, produce, check)
			}
			<-done
			if err == nil || got != (PrejoinPreparationReceipt{}) {
				t.Fatal("forged receipt escaped", got, err)
			}
		})
	}
}
