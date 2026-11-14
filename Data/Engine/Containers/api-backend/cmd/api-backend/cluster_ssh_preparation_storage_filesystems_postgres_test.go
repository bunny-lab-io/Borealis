package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHPreparationStorageFilesystemsPostgresCustody(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "credential during filesystem", "controller during filesystem", "Aegis during filesystem", "SQL during SSH close", "closed SSH", "closed HTTP"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, mode == "replacement")
			f = recoveryTransportSQLExercise(t, f)
			f.c.store.db.SetMaxOpenConns(1)
			plan, _, err := clusterSSHPreparationCleanBrokerBinding(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			before := f.events(t)
			pool := func(ctx context.Context) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
			mutate := func(ctx context.Context, when string) error {
				if mode == "credential during filesystem" && when == "filesystem" {
					_, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, snapshot.credentials[len(snapshot.credentials)-1].binding.TargetID)
					return err
				}
				if mode == "controller during filesystem" && when == "filesystem" || mode == "SQL during SSH close" && when == "close" {
					_, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='foreign' WHERE name=$1`, clusterControllerLeaseName)
					return err
				}
				if mode == "Aegis during filesystem" && when == "filesystem" {
					f.aegis.mu.Lock()
					clear(f.aegis.key)
					f.aegis.key = nil
					f.aegis.mu.Unlock()
				}
				return nil
			}
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshPreparationCleanBrokerTestSecret)
			mux := http.NewServeMux()
			broker := registerClusterSSHPreparationCleanBrokerRoute(mux, &authService{store: f.c.store, aegis: f.aegis})
			var envelopes []clusterSSHCredentialEnvelope
			for _, sealed := range snapshot.credentials {
				envelope, err := f.aegis.openClusterSSHCredentials(f.ctx, sealed, sealed.binding)
				if err != nil {
					t.Fatal(err)
				}
				envelopes = append(envelopes, envelope)
			}
			// The real Aegis adapter supplies unchanged synthetic credentials to
			// the pinned SSH fixture; neither binding nor plaintext is substituted.
			broker.aegis = sshPreparationProposalTestAegis{verify: f.aegis.verifyClusterSSHGeneration,
				open: func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
					if pool(ctx) != nil {
						return clusterSSHCredentialEnvelope{}, errClusterConflict
					}
					return f.aegis.openClusterSSHCredentials(ctx, sealed, binding)
				}}
			var connects, closes, filesystemReads atomic.Int32
			broker.connect = sshPreparationFilesystemConnect(t, snapshot, f.signers, envelopes, nil, func(ctx context.Context, _ int, client *sshPreparationFilesystemTestClient) {
				connects.Add(1)
				if err := pool(ctx); err != nil {
					t.Error("SSH retained SQL", err)
				}
				filesystem := client.filesystem
				client.filesystem = func(ctx context.Context, sudo []byte, target clusterremote.Target, key clusterremote.HostKey, request clusterremote.FilesystemRequest, check func(context.Context) error) (clusterremote.TargetFilesystem, error) {
					filesystemReads.Add(1)
					if err := pool(ctx); err != nil {
						return clusterremote.TargetFilesystem{}, err
					}
					value, err := filesystem(ctx, sudo, target, key, request, check)
					if err == nil {
						err = mutate(ctx, "filesystem")
					}
					return value, err
				}
				client.closed = func() error {
					closes.Add(1)
					if err := pool(ctx); err != nil {
						return err
					}
					if mode == "closed SSH" {
						return errClusterConflict
					}
					return mutate(ctx, "close")
				}
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			client := sshPreparationCleanBrokerTestClient(t, nil)
			client.endpoint = func(context.Context) (string, error) { return server.URL, nil }
			base := client.httpClient.Transport
			defer client.httpClient.CloseIdleConnections()
			client.httpClient.Transport = bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
				if err := pool(request.Context()); err != nil {
					return nil, err
				}
				response, err := base.RoundTrip(request)
				if response != nil && mode == "closed HTTP" {
					body := response.Body
					response.Body = &sshPreparationCleanBrokerBody{Reader: body, close: func() error { _ = body.Close(); return errClusterConflict }}
				}
				return response, err
			})
			read := client.preparationTargetsFilesystemRead(snapshot, f.c.preparationCleanTargetsProposalCheck(snapshot), []string{"/opt/Borealis", "/var/lib/longhorn"})
			value, err := read(f.ctx, plan)
			positive := mode == "pair" || mode == "replacement"
			if (err == nil) != positive || err != nil && (value.Digest != "" || len(value.Filesystems) != 0) {
				t.Fatal("wrong SQL/filesystem custody result", err)
			}
			if positive {
				if len(value.Filesystems) != len(snapshot.credentials) || filesystemReads.Load() != int32(2*len(snapshot.credentials)) {
					t.Fatal("whole-cohort filesystem evidence missing")
				}
				if _, err := read(f.ctx, plan); err != nil || filesystemReads.Load() != int32(4*len(snapshot.credentials)) {
					t.Fatal("fresh reread missing", err)
				}
			} else {
				before := connects.Load()
				if _, err := read(f.ctx, plan); err == nil || connects.Load() != before {
					t.Fatal("failed proposal revived")
				}
			}
			server.Close()
			assertCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var state, step, payload string
			var claims int
			if err := f.c.store.db.QueryRowContext(assertCtx, `SELECT state,current_step,payload_json,(SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND (state<>'queued' OR lease_holder<>'' OR lease_expires_at<>0)) FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step, &payload, &claims); err != nil || state != "waiting" || step != clusterSSHQualificationStep || claims != 0 || payload != snapshot.work.Payload {
				t.Fatal("filesystem observation changed phase/claim/payload", err)
			}
			if connects.Load() != closes.Load() || connects.Load() == 0 || f.events(t) != before || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("filesystem observation escaped cleanup or wrote events")
			}
			if strings.Contains(payload, "filesystem_paths") {
				t.Fatal("read-only request became durable readiness")
			}
		})
	}
}
