package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func artifactSessionRequestFixture(t *testing.T) PreparationSessionRequest {
	t.Helper()
	expected, _ := preparationFixture()
	source := engineidentity.Binding{ClusterID: expected.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
	r, err := NewArtifactPreparationSessionRequest(expected, strings.Repeat("a", 64), strings.Repeat("b", 64), "publish_prejoin_artifacts", source, PostgresImageRepository+"@sha256:"+strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestArtifactPreparationRequestAndLimits(t *testing.T) {
	r := artifactSessionRequestFixture(t)
	original, _ := r.Digest()
	for _, action := range []string{"install_prejoin_k3s", "reconcile_prejoin_k3s", "configure_prejoin_k3s", "reconcile_prejoin_k3s_configuration", "start_prepared_k3s", "reconcile_prepared_k3s_start"} {
		changed := r
		changed.Action = action
		digest, err := changed.Digest()
		if err != nil || digest == original || !artifactPreparationAction(action) {
			t.Fatal("fixed K3s action lost artifact binding", action, err)
		}
		expected, err := changed.Expected()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewPrejoinPreparationSessionRequest(expected, r.Session.ManagerSHA256, r.Session.Nonce, action, *r.IdentitySource); err == nil {
			t.Fatal("K3s action accepted without complete artifact identity")
		}
	}
	for _, mode := range []string{"different postgres", "missing postgres", "tag postgres", "different repository", "missing identity", "old action"} {
		changed := r
		switch mode {
		case "different postgres":
			changed.PostgresReference = PostgresImageRepository + "@sha256:" + strings.Repeat("d", 64)
		case "missing postgres":
			changed.PostgresReference = ""
		case "tag postgres":
			changed.PostgresReference = PostgresImageRepository + ":latest"
		case "different repository":
			changed.PostgresReference = "foreign/database@sha256:" + strings.Repeat("c", 64)
		case "missing identity":
			changed.IdentitySource = nil
		case "old action":
			changed.Action = "prepare_prejoin_host"
		}
		digest, err := changed.Digest()
		if mode == "different postgres" {
			if err != nil || digest == original {
				t.Fatal("source database identity not bound")
			}
		} else if err == nil {
			t.Fatal("invalid artifact request", mode)
		}
	}
	legacy, large := preparationProfile(false), preparationProfile(true)
	if legacy.chunk != PreparationChunkBytes || legacy.frame != MaxSessionFrameBytes || legacy.bytes != MaxPreparationTransferBytes || legacy.lifetime != SessionLifetime {
		t.Fatal("ordinary action limits broadened")
	}
	if large.bytes < 36*MaxImageArchiveBytes || large.chunk <= legacy.chunk || large.frame < large.chunk*4/3+4096 || large.lifetime != ArtifactPreparationLifetime || large.challenges < int(large.lifetime/SessionChallengeEvery)+1 {
		t.Fatal("complete artifact set exceeds bounded transport")
	}
	var frame bytes.Buffer
	value := PreparationSessionFrame{Protocol: 1, State: "chunk", Data: strings.Repeat("x", large.chunk)}
	if WriteSessionFrame(&frame, value) == nil {
		t.Fatal("ordinary frame limit weakened")
	}
	if writeSessionFrameBounded(&frame, value, large.frame) != nil {
		t.Fatal("artifact frame rejected")
	}
	var decoded PreparationSessionFrame
	if ReadSessionFrame(bytes.NewReader(frame.Bytes()), &decoded) == nil || readSessionFrameBounded(bytes.NewReader(frame.Bytes()), &decoded, large.frame) != nil {
		t.Fatal("frame class isolation lost")
	}
}

func TestArtifactPreparationSessionTransfer(t *testing.T) {
	for _, mode := range []string{"complete", "old executor", "old action", "authority loss", "consumer failure"} {
		t.Run(mode, func(t *testing.T) {
			r := artifactSessionRequestFixture(t)
			profile := preparationProfile(true)
			if mode == "old executor" {
				profile = preparationProfile(false)
			}
			if mode == "old action" {
				r.Action = "prepare_prejoin_host"
				r.PostgresReference = ""
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			payload := bytes.Repeat([]byte("bounded authenticated artifact transport"), 80000)
			want := sha256.Sum256(payload)
			var verified, received atomic.Bool
			result := make(chan error, 1)
			go func() {
				result <- servePreparationSessionProfile(ctx, server, server, r.Session.Nonce,
					func(context.Context, PreparationSessionRequest) error { verified.Store(true); return nil },
					func(_ context.Context, got PreparationSessionRequest, in io.Reader) (PreparationProgress, error) {
						if !verified.Load() || got.PostgresReference != r.PostgresReference {
							return PreparationProgress{}, ErrSessionProtocol
						}
						h := sha256.New()
						n, err := io.Copy(h, in)
						if err != nil || n != int64(len(payload)) || !bytes.Equal(h.Sum(nil), want[:]) {
							return PreparationProgress{}, ErrSessionProtocol
						}
						received.Store(true)
						if mode == "consumer failure" {
							return PreparationProgress{}, ErrMutationUnknown
						}
						return PreparationProgress{}, nil
					}, func(time.Time) error { return nil }, sessionTiming{50 * time.Millisecond, 2 * time.Second, time.Second}, profile)
			}()
			var produced atomic.Bool
			err := ExchangePreparationSession(ctx, client, client, r, func(_ context.Context, out io.Writer) error {
				if !verified.Load() {
					return ErrSessionProtocol
				}
				produced.Store(true)
				_, err := io.Copy(out, bytes.NewReader(payload))
				return err
			}, func(context.Context) error {
				if mode == "authority loss" && produced.Load() {
					return ErrSessionAuthority
				}
				return nil
			})
			serverErr := <-result
			if (err == nil) != (mode == "complete") || (serverErr == nil) != (mode == "complete") {
				t.Fatal("wrong session outcome", err, serverErr)
			}
			if mode == "complete" && !received.Load() {
				t.Fatal("incomplete transfer accepted")
			}
			if (mode == "old action" || mode == "old executor") && (verified.Load() || produced.Load()) {
				t.Fatal("cross-class request reached native verification")
			}
		})
	}
}
