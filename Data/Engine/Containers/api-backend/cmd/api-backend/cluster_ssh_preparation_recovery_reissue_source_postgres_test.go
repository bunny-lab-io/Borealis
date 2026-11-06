package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func recoveryReissuedSourceSQLFixture(t *testing.T, replacement, fourth bool) (sshPreparationAuthorityFixture, clusterSSHPreparationRecoveryClaim, clusterSSHPreparationRecoveryReissuedProofClaim, map[string]any) {
	t.Helper()
	baseline := clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	// Existing fixture builds actual approved configuration receipt and obtains
	// original Q+1 and Q+2 SQL claims. No archive/helper/host process is needed.
	f, original, secret := recoveryProgressClaimFixture(t, replacement, baseline)
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
	claim := recoveryReissueSQLClaim(t, f, original.Reservation.ID)
	if fourth {
		f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
		claim = recoveryReissueSQLClaim(t, f, claim.Lineage.Requests[0].ID)
	}
	f.c.store.db.SetMaxOpenConns(1)
	return f, original, claim, secret
}

func recoveryReissuedSourceSQLGet(ctx context.Context, f sshPreparationAuthorityFixture, source clusterSSHSourceCohort, secret map[string]any, path string, out any) error {
	if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
		return err
	}
	var value any
	switch path {
	case clusterSSHRuntimeSecretPath:
		value = secret
	case "/api/v1/namespaces/kube-system":
		value = map[string]any{"metadata": map[string]any{"uid": source.KubeSystemUID}}
	case "/api/v1/nodes":
		var items []any
		for _, member := range source.Members {
			items = append(items, sshSourceKubernetesFixture(member))
		}
		value = map[string]any{"items": items}
	default:
		return errors.New("unexpected source read surface")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestClusterSSHPreparationRecoveryReissueSourcePostgresCustody(t *testing.T) {
	for _, mode := range []string{"pair Q3", "replacement Q3", "pair Q4", "replacement Q4", "runtime bytes", "excluded bytes", "Secret UID", "Secret revision", "same CIDRs changed network", "source boot", "credential during Secret", "controller during network", "payload during Secret", "Aegis during Secret", "second Secret failure", "second network failure", "late Secret change", "late source boot", "late credential change", "late controller change", "late expiry", "late cancellation", "expired claim", "unissued claim"} {
		t.Run(mode, func(t *testing.T) {
			f, original, claim, secret := recoveryReissuedSourceSQLFixture(t, strings.HasPrefix(mode, "replacement"), strings.HasSuffix(mode, "Q4"))
			beforeEvents := f.events(t)
			approvedSecret, _ := json.Marshal(secret)
			expectedSecret, err := clusterbootstrap.ParsePreparationRuntimeSecret(approvedSecret)
			if err != nil {
				t.Fatal(err)
			}
			originalPayload, _, err := clusterSSHPreparationRecoveryPayload(claim.payload)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "runtime bytes":
				secret["data"].(map[string]string)["BOREALIS_OPERATOR_SECRET"] = base64.StdEncoding.EncodeToString([]byte("changed valid synthetic operator secret"))
				changed, err := json.Marshal(secret)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := clusterbootstrap.ParsePreparationRuntimeSecret(changed); err != nil {
					t.Fatal("runtime-byte drift must remain valid configuration", err)
				}
			case "excluded bytes":
				secret["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("changed"))
			case "Secret UID":
				secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
			case "Secret revision":
				secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
			case "expired claim":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			case "unissued claim":
				claim.custody = nil
			}
			gets, networks, secretReads, nodeReads := 0, 0, 0, 0
			scopeCtx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			var result clusterSSHPreparationRecoverySource
			var escaped clusterSSHPreparationRecoverySourceRead
			valid := strings.HasPrefix(mode, "pair") || strings.HasPrefix(mode, "replacement")
			invariantFailure := ""
			rejectInvariant := func(message string) error {
				invariantFailure = message
				return errors.New(message)
			}
			err = f.c.withClusterSSHPreparationRecoveryReissuedProofEvery(scopeCtx, f.aegis, claim, 15*time.Second, func(ctx context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
				get := func(ctx context.Context, path string, out any) error {
					gets++
					if path == clusterSSHRuntimeSecretPath {
						secretReads++
						if secretReads == 2 {
							if mode == "second Secret failure" {
								return errors.New("synthetic final Secret failure")
							}
							if mode == "late Secret change" {
								secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
							}
							if mode == "late credential change" {
								if _, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[len(claim.Claims)-1].Lease.TargetID); err != nil {
									return err
								}
							}
							if mode == "late expiry" {
								if _, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID); err != nil {
									return err
								}
							}
						}
						if secretReads == 1 {
							switch mode {
							case "credential during Secret":
								if _, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[len(claim.Claims)-1].Lease.TargetID); err != nil {
									return err
								}
							case "payload during Secret":
								if _, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID); err != nil {
									return err
								}
							case "Aegis during Secret":
								f.aegis.mu.Lock()
								clear(f.aegis.key)
								f.aegis.key = nil
								f.aegis.mu.Unlock()
							}
						}
					}
					if path == "/api/v1/nodes" {
						nodeReads++
					}
					if path == "/api/v1/nodes" && (mode == "source boot" || mode == "late source boot" && nodeReads == 2) {
						var nodes map[string]any
						if err := recoveryReissuedSourceSQLGet(ctx, f, value.Source, secret, path, &nodes); err != nil {
							return err
						}
						nodes["items"].([]any)[0].(map[string]any)["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
						raw, _ := json.Marshal(nodes)
						return json.Unmarshal(raw, out)
					}
					return recoveryReissuedSourceSQLGet(ctx, f, value.Source, secret, path, out)
				}
				network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
					networks++
					if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
						return clusterbootstrap.SourceNetwork{}, err
					}
					if mode == "second network failure" && networks > len(value.Source.Members) {
						return clusterbootstrap.SourceNetwork{}, errors.New("synthetic second network round failure")
					}
					if mode == "controller during network" || mode == "late controller change" && networks > len(value.Source.Members) {
						if _, err := f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName); err != nil {
							return clusterbootstrap.SourceNetwork{}, err
						}
					}
					if mode == "late cancellation" && networks > len(value.Source.Members) {
						cancel()
					}
					observed := sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version)
					if mode == "same CIDRs changed network" {
						observed.ManagementLink.NetworkNamespace++
						observed.Kubelet.NetworkNamespace++
					}
					return observed, ctx.Err()
				}
				// Attempted relabelling stays a negative control: old Q+2 reader must
				// reject actual v2 payload before its private source IO can begin.
				if valid {
					oldCalls := 0
					old := clusterSSHPreparationRecoveryAuthority{Reservation: original.Reservation, Baseline: value.Baseline, Cohort: value.Cohort, Source: value.Source, Authorization: value.Authorization, IdentitySource: value.IdentitySource, Leases: value.Leases, ExpiresAt: value.ExpiresAt}
					oldRead := newClusterSSHPreparationRecoverySourceRead(old, claim.payload, check, func(context.Context, string, any) error { oldCalls++; return nil }, network)
					if got, err := oldRead(ctx); err == nil || len(got.Expected) != 0 || got.Settings != nil || oldCalls != 0 {
						return rejectInvariant("old Q+2 source reader accepted v2")
					}
				}
				read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, claim.payload, check, get, network)
				escaped = read
				got, err := read(ctx)
				if err != nil {
					if len(got.Expected) != 0 || got.Settings != nil {
						return rejectInvariant("failed source exposed partial bytes")
					}
					before := gets + networks
					if again, repeatErr := read(ctx); repeatErr == nil || len(again.Expected) != 0 || again.Settings != nil || gets+networks != before {
						return rejectInvariant("failed reader was reusable")
					}
					return err
				}
				if !valid {
					return rejectInvariant("invalid source released complete evidence")
				}
				if len(got.Expected) != len(claim.Claims) || !reflect.DeepEqual(got.Settings, expectedSecret.Settings()) {
					return rejectInvariant("source did not reproduce original configuration")
				}
				for i, expected := range got.Expected {
					lease := claim.Claims[i].Lease
					if expected.Validate() != nil || expected.Target.Generation != lease.Generation || expected.Target.HolderID != lease.Holder || expected.Target.TargetID != lease.TargetID || expected.Source != claim.Baseline ||
						expected.TargetBootID != value.Cohort.Targets[i].Report.BootID || expected.KubeSystemUID != value.Source.KubeSystemUID ||
						expected.PodCIDR != "10.42.0.0/16" || expected.ServiceCIDR != "10.43.0.0/16" || expected.K3sVersion != clusterbootstrap.K3sPins().Version {
						return rejectInvariant("source lost actual issued target custody")
					}
					if _, err := buildClusterSSHPreparationExpected(value.Cohort, value.Source, lease, value.Baseline, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16"); err == nil {
						return rejectInvariant("initial builder admitted reissued proof")
					}
				}
				got.Settings["POSTGRES_PASSWORD"] = "caller changed"
				again, err := read(ctx)
				if err != nil {
					return err
				}
				if again.Settings["POSTGRES_PASSWORD"] == "caller changed" {
					return rejectInvariant("reader result aliases retained settings")
				}
				if secretReads != 4 || nodeReads != 4 || networks != 4*len(value.Source.Members) {
					return rejectInvariant("source reuse did not repeat complete original observations")
				}
				result = again
				return nil
			})
			if invariantFailure != "" {
				t.Fatal(invariantFailure)
			}
			if (err == nil) != valid {
				t.Fatal("actual reissued source outcome", err)
			}
			if !valid {
				if len(result.Expected) != 0 || result.Settings != nil {
					t.Fatal("invalid authority released evidence")
				}
			} else if len(result.Expected) != len(claim.Claims) {
				t.Fatal("missing complete cohort")
			}
			if escaped != nil {
				before := gets + networks
				if got, err := escaped(context.Background()); err == nil || len(got.Expected) != 0 || got.Settings != nil || gets+networks != before {
					t.Fatal("escaped source reader outlived issued proof")
				}
			}
			var payload string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload) != nil {
				t.Fatal("retained parent")
			}
			base, _, err := clusterSSHPreparationRecoveryPayload(payload)
			if err != nil || string(base) != string(originalPayload) || f.events(t) != beforeEvents || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("source read rewrote original authorization, published event or retained SQL")
			}
			if mode == "expired claim" || mode == "unissued claim" {
				if gets+networks != 0 {
					t.Fatal("unissued/expired proof performed IO")
				}
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissueSourcePostgresRejectsTypedDrift(t *testing.T) {
	for _, mode := range []string{"original lineage", "older tip", "tip ID", "tip holder", "lease holder", "lease generation", "inspection digest", "inspected report", "source identity", "credential digest", "controller", "missing expiry", "original payload"} {
		t.Run(mode, func(t *testing.T) {
			f, _, claim, _ := recoveryReissuedSourceSQLFixture(t, false, true)
			beforeEvents := f.events(t)
			entered := false
			err := f.c.withClusterSSHPreparationRecoveryReissuedProofEvery(f.ctx, f.aegis, claim, 15*time.Second, func(ctx context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
				entered = true
				before := sshPreparationRecoveryState(t, f)
				// Keep the actual issued scope intact. Only the detached reader input
				// changes, so rejection cannot be credited to failed scope entry.
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				var changed clusterSSHPreparationRecoveryReissuedProofAuthority
				if err := json.Unmarshal(raw, &changed); err != nil {
					return err
				}
				payload := claim.payload
				tip := &changed.Lineage.Requests[len(changed.Lineage.Requests)-1]
				switch mode {
				case "original lineage":
					changed.Lineage.Original.ID = newClusterUUID()
				case "older tip":
					changed.Lineage.Requests = changed.Lineage.Requests[:1]
				case "tip ID":
					tip.ID = newClusterUUID()
				case "tip holder":
					tip.Holder = newClusterUUID()
				case "lease holder":
					changed.Leases[0].Holder = newClusterUUID()
				case "lease generation":
					changed.Leases[0].Generation--
				case "inspection digest":
					changed.Authorization.InspectionSHA256 = strings.Repeat("c", 64)
				case "inspected report":
					changed.Cohort.Targets[0].Report.BootID = newClusterUUID()
				case "source identity":
					changed.Source.Members[0].BootID = newClusterUUID()
				case "credential digest":
					changed.Authorization.Targets[0].CredentialSHA256 = strings.Repeat("c", 64)
				case "controller":
					changed.Leases[0].ControllerHolder = newClusterUUID()
				case "missing expiry":
					delete(changed.ExpiresAt, changed.Leases[0].TargetID)
				case "original payload":
					base, _, err := clusterSSHPreparationRecoveryPayload(payload)
					if err != nil {
						return err
					}
					payload = string(base)
				}
				calls := 0
				read := newClusterSSHPreparationRecoveryReissuedSourceRead(changed, payload, check,
					func(context.Context, string, any) error { calls++; return errors.New("unexpected private source read") },
					func(context.Context, clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
						calls++
						return clusterbootstrap.SourceNetwork{}, errors.New("unexpected network read")
					})
				for i := 0; i < 2; i++ {
					if got, err := read(ctx); err == nil || got.Settings != nil || len(got.Expected) != 0 || calls != 0 {
						return errors.New("substituted reader inputs reached private IO or released partial evidence")
					}
				}
				if got, err := buildClusterSSHPreparationRecoveryReissuedExpected(changed, payload, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16"); err == nil || !reflect.DeepEqual(got, clusterbootstrap.PreparationExpected{}) {
					return errors.New("substituted reader inputs produced expected target")
				}
				if before != sshPreparationRecoveryState(t, f) {
					return errors.New("detached reader changed durable custody")
				}
				return check(ctx)
			})
			if err != nil || !entered {
				t.Fatal("actual reissued scope did not reject detached source substitution", err)
			}
			if beforeEvents != f.events(t) || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("detached source rejection published event or retained SQL")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissueSourcePostgresJoinsCancellation(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "authority loss", "callback cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			f, _, claim, secret := recoveryReissuedSourceSQLFixture(t, false, true)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			entered, stopping, cleanup, failRead := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			var release sync.Once
			var result clusterSSHPreparationRecoverySource
			var escaped clusterSSHPreparationRecoverySourceRead
			calls := 0
			partialEvidence := false
			go func() {
				var outcome error
				defer func() { done <- outcome }()
				outcome = f.c.withClusterSSHPreparationRecoveryReissuedProofEvery(ctx, f.aegis, claim, 30*time.Millisecond, func(work context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
					get := func(readCtx context.Context, path string, out any) error {
						calls++
						if err := recoveryProgressSQLPoolAvailable(readCtx, f); err != nil {
							return err
						}
						if path == clusterSSHRuntimeSecretPath {
							close(entered)
							if mode == "callback cleanup failure" {
								select {
								case <-failRead:
								case <-readCtx.Done():
								}
							} else {
								<-readCtx.Done()
							}
							close(stopping)
							<-cleanup
							// Return only after owned read cleanup completes, even when parent
							// canceled first. Reader must reject late bytes or cleanup error.
							if mode == "callback cleanup failure" {
								return errors.New("synthetic source callback cleanup failure")
							}
							raw, _ := json.Marshal(secret)
							return json.Unmarshal(raw, out)
						}
						return recoveryReissuedSourceSQLGet(readCtx, f, value.Source, secret, path, out)
					}
					network := func(readCtx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
						if err := recoveryProgressSQLPoolAvailable(readCtx, f); err != nil {
							return clusterbootstrap.SourceNetwork{}, err
						}
						return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), readCtx.Err()
					}
					escaped = newClusterSSHPreparationRecoveryReissuedSourceRead(value, claim.payload, check, get, network)
					got, err := escaped(work)
					if err != nil {
						if len(got.Expected) != 0 || got.Settings != nil {
							partialEvidence = true
							return errors.New("canceled source exposed partial evidence")
						}
						return err
					}
					result = got
					return nil
				})
			}()
			joined := false
			defer func() {
				cancel()
				release.Do(func() { close(cleanup) })
				if !joined {
					<-done
				}
			}()
			select {
			case <-entered:
			case err := <-done:
				joined = true
				t.Fatal("source entry", err)
			case <-ctx.Done():
				t.Fatal("source entry timeout")
			}
			switch mode {
			case "parent cancellation":
				cancel()
			case "authority loss":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[1].Lease.TargetID)
			case "callback cleanup failure":
				close(failRead)
			}
			select {
			case <-stopping:
			case err := <-done:
				joined = true
				t.Fatal("source returned before cleanup", err)
			case <-time.After(4 * time.Second):
				t.Fatal("source callback not canceled")
			}
			select {
			case <-done:
				joined = true
				t.Fatal("scope returned with owned source read active")
			default:
			}
			release.Do(func() { close(cleanup) })
			select {
			case err := <-done:
				joined = true
				if err == nil || partialEvidence || len(result.Expected) != 0 || result.Settings != nil {
					t.Fatal("failed source released evidence")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("scope did not join source cleanup")
			}
			before := calls
			if got, err := escaped(context.Background()); err == nil || len(got.Expected) != 0 || got.Settings != nil || calls != before {
				t.Fatal("escaped reader admitted source after cancellation")
			}
			if !claim.custody.failed.Load() || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("failed source claim unpoisoned or SQL connection retained")
			}
			enteredAgain := false
			if f.c.withClusterSSHPreparationRecoveryReissuedProof(f.ctx, f.aegis, claim, func(context.Context, clusterSSHPreparationRecoveryReissuedProofAuthority, func(context.Context) error) error {
				enteredAgain = true
				return nil
			}) == nil || enteredAgain {
				t.Fatal("source cleanup failure made claim reusable")
			}
		})
	}
}
