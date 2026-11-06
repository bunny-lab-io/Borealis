package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func existingIdentityConfirmationFixture(t *testing.T) (existingIdentityFixture, clusterExistingIdentityPlan, *goAegisService, clusterSSHPlannedTarget, clusterSSHCredentialEnvelope) {
	t.Helper()
	f := newExistingIdentityFixture(t)
	p, err := f.c.reserveClusterExistingIdentity(f.ctx, newClusterUUID(), f.maintenance, f.source, f.target, "admin", f.read(t))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, aegisKeyLength)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	verification, err := aegisEncryptText(aegisVerificationPlaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO engine.aegis_cipher_state(id,kdf_name,kdf_params_json,verification_token,created_at,updated_at) VALUES(1,'scrypt','{}',$1,1,1)`, verification)
	t.Cleanup(func() {
		_, _ = f.c.store.db.ExecContext(context.Background(), `DELETE FROM engine.aegis_cipher_state WHERE id=1`)
	})
	aegis := newGoAegisService(f.c.store.db, nil)
	if aegis.installClusterKey(f.ctx, key) != nil {
		t.Fatal("Aegis fixture")
	}
	_, remote, _, _ := clusterSSHTestService(t)
	address := ""
	for _, m := range p.Members {
		if m.NodeID == p.TargetID {
			address = m.Address
		}
	}
	binding := clusterSSHCredentialBinding{ClusterID: p.ClusterID, OperationID: p.OperationID, TargetID: newClusterUUID(), Address: address, Port: 22, Fingerprint: remote.key.Fingerprint}
	envelope := clusterSSHCredentialEnvelope{Version: 1, Binding: binding, Username: "test-operator", Method: "password", Password: "  private synthetic<&>$ password  ", SudoPassword: "  private synthetic sudo  "}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("connection retained before crypto")
	}
	sealed, err := aegis.sealClusterSSHCredentials(f.ctx, envelope)
	if err != nil {
		t.Fatal(err)
	}
	target := clusterSSHPlannedTarget{Binding: binding, Key: remote.key, KeyBase64: base64.StdEncoding.EncodeToString(remote.key.PublicKey), Sealed: sealed}
	return f, p, aegis, target, envelope
}

func assertExistingIdentityUnconfirmed(t *testing.T, f existingIdentityFixture, p clusterExistingIdentityPlan) {
	t.Helper()
	var count int
	var step, payload string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT current_step,payload_json,(SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1) FROM engine.cluster_operations WHERE id=$1`, p.OperationID).Scan(&step, &payload, &count) != nil || count != 0 {
		t.Fatal("partial confirmation target")
	}
	var events int
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='existing_identity_confirmed'`, p.OperationID).Scan(&events) != nil || events != 0 {
		t.Fatal("partial confirmation event")
	}
}

func TestClusterExistingIdentityPostgresConfirmation(t *testing.T) {
	for _, mode := range []string{"success", "expired cleanup", "claimed cancellation", "changed phase cancellation", "plan drift", "config drift", "maintenance drift", "member drift", "controller drift", "Aegis drift", "cancelled", "wrong pin", "wrong binding", "wrong digest", "stale plan", "future plan", "bad actor"} {
		t.Run(mode, func(t *testing.T) {
			f, p, aegis, target, envelope := existingIdentityConfirmationFixture(t)
			digest, _ := clusterSSHPreparationDigest(p)
			actor := "admin"
			switch mode {
			case "plan drift":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, p.OperationID)
			case "config drift":
				f.exec(t, `UPDATE engine.cluster_state SET config_json=config_json||' ' WHERE id=1`)
			case "maintenance drift":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, p.MaintenanceID)
			case "member drift":
				f.exec(t, `UPDATE engine.cluster_nodes SET management_ip='192.168.90.99' WHERE id=$1`, p.SourceID)
			case "controller drift":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
			case "Aegis drift":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='replacement' WHERE id=1`)
			case "cancelled":
				if _, err := f.c.store.cancelClusterOperation(f.ctx, "admin", p.OperationID); err != nil {
					t.Fatal(err)
				}
			case "wrong pin":
				target.Key.Fingerprint = "SHA256:" + strings.Repeat("a", 43)
			case "wrong binding":
				target.Binding.Address = "192.168.90.99"
				target.Sealed.binding = target.Binding
			case "wrong digest":
				digest = strings.Repeat("d", 64)
			case "stale plan", "future plan":
				p.ObservedAt = time.Now().Unix() - clusterExistingIdentityPlanLifetime
				if mode == "future plan" {
					p.ObservedAt = time.Now().Unix() + 60
				}
				digest, _ = clusterSSHPreparationDigest(p)
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, p.OperationID, marshalClusterJSON(p))
			case "bad actor":
				actor = "admin\n"
			}
			receipt, err := f.c.store.confirmClusterExistingIdentity(f.ctx, actor, p, digest, target)
			if mode != "success" && mode != "expired cleanup" && mode != "claimed cancellation" && mode != "changed phase cancellation" {
				if err == nil {
					t.Fatal("unsafe confirmation", mode)
				}
				assertExistingIdentityUnconfirmed(t, f, p)
				return
			}
			if err != nil || !receipt.valid(p) {
				t.Fatal("confirmation", err)
			}
			var payload, step, storedCredential string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT o.payload_json,t.current_step,c.ciphertext FROM engine.cluster_operations o JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id JOIN engine.cluster_onboarding_credentials c ON c.target_id=t.id WHERE o.id=$1`, p.OperationID).Scan(&payload, &step, &storedCredential) != nil || step != clusterExistingIdentityInspect || storedCredential != target.Sealed.ciphertext {
				t.Fatal("confirmation binding")
			}
			if strings.Contains(payload, envelope.Password) || strings.Contains(payload, storedCredential) || strings.Contains(payload, target.Sealed.generation) {
				t.Fatal("private data in ordinary payload")
			}
			storedPlan, storedReceipt, err := parseClusterExistingIdentityPayload([]byte(payload))
			if err != nil || !reflect.DeepEqual(storedPlan, p) || storedReceipt == nil || *storedReceipt != receipt {
				t.Fatal("exact confirmation receipt", err)
			}
			opened, err := aegis.openClusterSSHCredentials(f.ctx, target.Sealed, target.Binding)
			if err != nil || opened.Password != envelope.Password || opened.SudoPassword != envelope.SudoPassword {
				t.Fatal("credential syntax changed")
			}
			readback, state, err := f.c.store.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID)
			if err != nil || !reflect.DeepEqual(readback, p) || state != "awaiting_identity_observation" {
				t.Fatal("confirmation lost-ack readback", err)
			}
			if _, err := f.c.store.confirmClusterExistingIdentity(f.ctx, actor, p, digest, target); err == nil {
				t.Fatal("confirmation replay renewed credentials")
			}
			if _, claimed, err := f.c.claimOperation(f.ctx); err != nil || claimed {
				t.Fatal("generic execution activated")
			}
			if pending, err := f.c.store.pendingClusterSSHIdentityRecoveries(f.ctx); err != nil || len(pending) != 0 {
				t.Fatal("onboarding recovery activated")
			}
			if mode == "expired cleanup" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, target.Binding.TargetID)
				if f.c.store.cleanupClusterSSHCredentials(f.ctx) != nil {
					t.Fatal("expiry cleanup")
				}
			}
			if mode == "claimed cancellation" || mode == "changed phase cancellation" {
				if mode == "claimed cancellation" {
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running',lease_holder=$2,lease_generation=1,lease_expires_at=$3 WHERE id=$1`, target.Binding.TargetID, newClusterUUID(), time.Now().Unix()+45)
				} else {
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='recover_existing_identity' WHERE id=$1`, target.Binding.TargetID)
				}
				if _, err := f.c.store.cancelClusterOperation(f.ctx, "admin", p.OperationID); err == nil {
					t.Fatal("unsafe claimed/unknown cancellation")
				}
				return
			}
			if _, err := f.c.store.cancelClusterOperation(f.ctx, "admin", p.OperationID); err != nil {
				t.Fatal("safe confirmed cancellation", err)
			}
			var count int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, target.Binding.TargetID).Scan(&count) != nil || count != 0 {
				t.Fatal("cancel retained credentials")
			}
			if _, state, err := f.c.store.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID); err != nil || state != "cancelled" {
				t.Fatal("cancelled confirmation readback")
			}
			var application, reason string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT application_state,drain_reason FROM engine.cluster_nodes WHERE id=$1`, p.TargetID).Scan(&application, &reason) != nil || application != "drained" || reason != "operator maintenance" {
				t.Fatal("confirmation changed maintenance")
			}
		})
	}
}

func TestClusterExistingIdentityPostgresConcurrentConfirmation(t *testing.T) {
	f, p, _, target, _ := existingIdentityConfirmationFixture(t)
	f.c.store.db.SetMaxOpenConns(4)
	digest, _ := clusterSSHPreparationDigest(p)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.c.store.confirmClusterExistingIdentity(f.ctx, "admin", p, digest, target)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("confirmation not exclusive", success)
	}
	var count int
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='existing_identity_confirmed'`, p.OperationID).Scan(&count) != nil || count != 1 {
		t.Fatal("duplicate confirmation event")
	}
}

func TestClusterExistingIdentityPostgresConfirmationWaitFence(t *testing.T) {
	for _, mode := range []string{"expired", "owner", "maintenance", "configuration", "Aegis", "operation cancelled", "context cancelled", "postinsert expiry"} {
		t.Run(mode, func(t *testing.T) {
			f, p, _, target, _ := existingIdentityConfirmationFixture(t)
			f.c.store.db.SetMaxOpenConns(4)
			digest, _ := clusterSSHPreparationDigest(p)
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			expires := time.Now().Unix() + 2
			if mode == "postinsert expiry" {
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=$2 WHERE name=$1`, clusterControllerLeaseName, expires)
			}
			blocker, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if mode == "postinsert expiry" {
				if _, err := blocker.ExecContext(ctx, `LOCK TABLE engine.cluster_operation_events IN ACCESS EXCLUSIVE MODE`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			joined := false
			defer func() {
				blocker.Rollback()
				cancel()
				if !joined {
					<-done
				}
			}()
			go func() {
				_, err := f.c.store.confirmClusterExistingIdentity(ctx, "admin", p, digest, target)
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				pattern := "%engine.cluster_application_leases%"
				if mode == "postinsert expiry" {
					pattern = "%INSERT INTO engine.cluster_operation_events%"
				}
				if f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting) != nil {
					t.Fatal("observe waiter")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("missing confirmation waiter")
				}
			}
			switch mode {
			case "postinsert expiry":
				for {
					var expired bool
					if f.c.store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp()) >= $1`, expires).Scan(&expired) != nil {
						t.Fatal("expiry clock")
					}
					if expired {
						break
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("expiry wait")
					}
				}
			case "expired":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "owner":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
			case "maintenance":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, p.MaintenanceID)
			case "configuration":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_state SET config_json=config_json||' ' WHERE id=1`)
			case "Aegis":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.aegis_cipher_state SET verification_token='replacement' WHERE id=1`)
			case "operation cancelled":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='cancelled',current_step='cancelled' WHERE id=$1`, p.OperationID)
			case "context cancelled":
				cancel()
			}
			if mode == "context cancelled" {
				blocker.Rollback()
			} else if err != nil || blocker.Commit() != nil {
				t.Fatal("blocking change", err)
			}
			if err := <-done; err == nil {
				t.Fatal("drift survived wait")
			}
			joined = true
			assertExistingIdentityUnconfirmed(t, f, p)
		})
	}
}
