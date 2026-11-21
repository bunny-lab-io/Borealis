package main

import (
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the registered API route and controller HTTP client through the real
// waiting-proposal SQL reader, Aegis and clean-target adapter. Only remote SSH is
// replaced by a pinned protocol fixture; no fixture grants readiness or claims.
func TestClusterSSHPreparationCleanBrokerPostgresRoute(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "wrong holder", "wrong plan", "wrong proposal", "attempt", "controller expiry", "credential expiry", "changed intent", "Aegis locked", "during native credential", "during native holder", "during native cancel", "during SSH close holder", "HTTP close error", "old native proof"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, mode == "replacement")
			f.c.store.db.SetMaxOpenConns(1)
			exercise, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			plan, _, err := clusterSSHPreparationCleanBrokerBinding(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			before := f.events(t)
			pool := func(ctx context.Context) error {
				if f.c.store.db.Stats().InUse != 0 {
					return errClusterConflict
				}
				var one int
				if err := f.c.store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
					return errClusterConflict
				}
				return nil
			}
			mutate := func(name string) error {
				var sql string
				var args []any
				switch name {
				case "attempt":
					sql = `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`
					args = []any{f.op.ID}
				case "controller expiry":
					sql = `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`
					args = []any{clusterControllerLeaseName}
				case "credential expiry", "during native credential":
					sql = `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`
					args = []any{snapshot.credentials[len(snapshot.credentials)-1].binding.TargetID}
				case "during native holder", "during SSH close holder":
					sql = `UPDATE engine.cluster_application_leases SET holder='foreign-holder' WHERE name=$1`
					args = []any{clusterControllerLeaseName}
				case "changed intent":
					sql = `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{ssh_preparation_request,request_id}',to_jsonb($2::text))::text WHERE id=$1`
					args = []any{f.op.ID, newClusterUUID()}
				case "Aegis locked":
					f.aegis.mu.Lock()
					clear(f.aegis.key)
					f.aegis.key = nil
					f.aegis.mu.Unlock()
				}
				if sql == "" {
					return nil
				}
				_, err := f.c.store.db.ExecContext(exercise, sql, args...)
				return err
			}
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshPreparationCleanBrokerTestSecret)
			mux := http.NewServeMux()
			broker := registerClusterSSHPreparationCleanBrokerRoute(mux, &authService{store: f.c.store, aegis: f.aegis})
			broker.aegis = sshPreparationProposalTestAegis{verify: f.aegis.verifyClusterSSHGeneration,
				open: func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
					if err := pool(ctx); err != nil {
						return clusterSSHCredentialEnvelope{}, err
					}
					return f.aegis.openClusterSSHCredentials(ctx, sealed, binding)
				}}
			var connects, reads, closes atomic.Int64
			broker.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationProposalClient, error) {
				if err := pool(ctx); err != nil {
					return nil, err
				}
				index := int(connects.Add(1)-1) % len(snapshot.credentials)
				original := snapshot.work.Proof.Cohort.Targets[index]
				if target.Address != original.Binding.Address || target.Port != original.Binding.Port || key.Fingerprint != original.Key.Fingerprint || credential == nil {
					return nil, errClusterConflict
				}
				return &sshPreparationProposalTestClient{inspect: func(ctx context.Context, _ []byte, request clusterremote.PreparationProposal, current func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
					reads.Add(1)
					if pool(ctx) != nil || current(ctx) != nil || request.Validate() != nil || request.PlanSHA256 != plan || request.TargetID != original.Binding.TargetID || request.QualificationGeneration != snapshot.work.Authorization.Targets[index].QualificationGeneration {
						return clusterremote.PreparationProposalProof{}, errClusterConflict
					}
					if strings.HasPrefix(mode, "during native ") && mode != "during native cancel" {
						if err := mutate(mode); err != nil {
							return clusterremote.PreparationProposalProof{}, err
						}
					}
					if mode == "during native cancel" {
						cancel()
						<-ctx.Done()
					}
					proof := sshPreparationProposalTestProof(request, strings.Repeat("a", 64))
					if mode == "old native proof" {
						proof.Version = 1
					}
					return proof, nil
				}, close: func() error {
					closes.Add(1)
					if mode == "during SSH close holder" {
						return mutate(mode)
					}
					return nil
				}}, nil
			}
			server := httptest.NewServer(mux)
			defer server.Close() // Join all handlers before fixture SQL cleanup.
			var requests atomic.Int64
			client := sshPreparationCleanBrokerTestClient(t, nil)
			client.endpoint = func(context.Context) (string, error) { return server.URL, nil }
			standard := client.httpClient.Transport
			defer client.httpClient.CloseIdleConnections()
			client.httpClient.Transport = bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				if err := pool(r.Context()); err != nil {
					return nil, err
				}
				requests.Add(1)
				if !strings.HasPrefix(mode, "during ") {
					if err := mutate(mode); err != nil {
						return nil, err
					}
				}
				if mode == "wrong holder" || mode == "wrong plan" || mode == "wrong proposal" {
					wire, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					var request clusterSSHPreparationCleanBrokerRequest
					if openClusterSSHVIP(client.requestCipher, clusterSSHPreparationCleanBrokerPath, wire, &request, clusterSSHPreparationCleanBrokerLimit) != nil {
						return nil, errClusterConflict
					}
					switch mode {
					case "wrong holder":
						request.ControllerHolder = "foreign-holder"
					case "wrong plan":
						request.PlanSHA256 = strings.Repeat("f", 64)
					case "wrong proposal":
						request.ProposalSHA256 = strings.Repeat("f", 64)
					}
					wire, err = sealClusterSSHVIP(client.requestCipher, clusterSSHPreparationCleanBrokerPath, request, clusterSSHPreparationCleanBrokerLimit)
					if err != nil {
						return nil, err
					}
					r.Body = io.NopCloser(bytes.NewReader(wire))
					r.ContentLength = int64(len(wire))
				}
				response, err := standard.RoundTrip(r)
				if response != nil && mode == "HTTP close error" {
					body := response.Body
					response.Body = &sshPreparationCleanBrokerBody{Reader: body, close: func() error { _ = body.Close(); return errClusterConflict }}
				}
				return response, err
			})
			read := client.preparationRead(snapshot, f.c.preparationCleanTargetsProposalCheck(snapshot))
			value, err := read(exercise, plan)
			positive := mode == "pair" || mode == "replacement"
			if (err == nil) != positive || err != nil && value != "" {
				t.Fatal("unexpected API observation", err)
			}
			if positive {
				again, err := read(exercise, plan)
				if err != nil || again != value || requests.Load() != 2 || reads.Load() != int64(4*len(snapshot.credentials)) {
					t.Fatal("missing independent native reread", err, requests.Load(), reads.Load())
				}
			} else {
				before := requests.Load()
				if value, err := read(context.Background(), plan); err == nil || value != "" || requests.Load() != before {
					t.Fatal("failed reader revived")
				}
			}
			server.Close()
			if requests.Load() == 0 || connects.Load() != closes.Load() {
				t.Fatal("API route skipped or SSH not closed", requests.Load(), connects.Load(), closes.Load())
			}
			if !positive && !strings.HasPrefix(mode, "during ") && mode != "HTTP close error" && mode != "old native proof" && reads.Load() != 0 {
				t.Fatal("invalid request reached native target")
			}
			assertCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			var state, step string
			if err := f.c.store.db.QueryRowContext(assertCtx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step); err != nil || state != "waiting" || step != clusterSSHQualificationStep {
				t.Fatal("observation changed waiting phase", err)
			}
			var claims int
			if err := f.c.store.db.QueryRowContext(assertCtx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND (state<>'queued' OR current_step<>'qualification_complete' OR lease_holder<>'' OR lease_expires_at<>0)`, f.op.ID).Scan(&claims); err != nil || claims != 0 {
				t.Fatal("observation manufactured worker claim", err)
			}
			if f.events(t) != before {
				t.Fatal("observation emitted events")
			}
		})
	}
}
