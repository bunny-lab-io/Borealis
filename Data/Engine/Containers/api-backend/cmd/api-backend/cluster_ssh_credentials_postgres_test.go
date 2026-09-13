package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"borealis/api-backend/internal/clusterremote"
	"golang.org/x/crypto/ssh"
)

func clusterSSHCredentialsFixture(t *testing.T) (*postgresOperatorStore, *goAegisService, context.Context, string, []clusterSSHPlannedTarget, []clusterSSHCredentialEnvelope) {
	t.Helper()
	store, ctx, clusterID, _ := clusterAdmissionFixture(t)
	// A single available connection catches accidental crypto/integration lookups
	// while a row, transaction or pooled connection remains checked out.
	store.db.SetMaxOpenConns(1)
	key := make([]byte, aegisKeyLength)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	verification, err := aegisEncryptText(aegisVerificationPlaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	// Own a new fixture row only. Refuse to overwrite any existing Aegis state.
	if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.aegis_cipher_state
		(id,kdf_name,kdf_params_json,verification_token,created_at,updated_at) VALUES(1,'scrypt','{}',$1,1,1)`, verification); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM engine.aegis_cipher_state WHERE id=1`); err != nil {
			t.Error(err)
		}
	})
	aegis := newGoAegisService(store.db, nil)
	if err := aegis.installClusterKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	operationID := newClusterUUID()
	now := time.Now().Unix()
	// Match runClusterController's hostname-plus-UUID default. Target worker
	// holders are UUIDs, but the existing controller holder is an opaque identity.
	if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)`, clusterControllerLeaseName, "borealis-controller-01-"+newClusterUUID(), now+300, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.cluster_operations
		(id,kind,state,current_step,requested_by,created_at,updated_at) VALUES($1,'ssh_onboarding','running','inspect_ssh_targets','ssh-test',$2,$2)`, operationID, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operationID); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1 WHERE id=1`, operationID); err != nil {
		t.Fatal(err)
	}
	var targets []clusterSSHPlannedTarget
	var envelopes []clusterSSHCredentialEnvelope
	for index := 0; index < 2; index++ {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		binding := clusterSSHCredentialBinding{ClusterID: clusterID, OperationID: operationID, TargetID: newClusterUUID(),
			Address: fmt.Sprintf("192.168.90.%d", 21+index), Port: 22, Fingerprint: ssh.FingerprintSHA256(key)}
		envelope := clusterSSHCredentialEnvelope{Version: 1, Binding: binding, Username: "operator", Method: "password",
			Password: "  " + base64.RawURLEncoding.EncodeToString(random) + " <&>$  ", SudoPassword: "  " + base64.RawURLEncoding.EncodeToString(random) + " sudo  "}
		sealed, err := aegis.sealClusterSSHCredentials(ctx, envelope)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, clusterSSHPlannedTarget{Binding: binding, Key: clusterremote.HostKey{Algorithm: key.Type(), Fingerprint: binding.Fingerprint, PublicKey: key.Marshal()}, KeyBase64: base64.StdEncoding.EncodeToString(key.Marshal()), Sealed: sealed})
		envelopes = append(envelopes, envelope)
	}
	return store, aegis, ctx, operationID, targets, envelopes
}

func insertSSHFixtureTargets(t *testing.T, store *postgresOperatorStore, ctx context.Context, operationID string, targets []clusterSSHPlannedTarget) {
	t.Helper()
	if err := validateClusterSSHPlannedTargets(targets, len(targets)); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := insertClusterSSHPlannedTargets(ctx, tx, operationID, targets[0].Binding.ClusterID, targets); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestClusterSSHCredentialsPostgresBoundEncryptionAndOwnership(t *testing.T) {
	store, aegis, ctx, operationID, targets, envelopes := clusterSSHCredentialsFixture(t)
	insertSSHFixtureTargets(t, store, ctx, operationID, targets)
	cold := newGoAegisService(store.db, nil)
	if _, err := cold.sealClusterSSHCredentials(ctx, envelopes[0]); err == nil {
		t.Fatal("locked Aegis stored credentials")
	}
	if _, err := cold.openClusterSSHCredentials(ctx, targets[0].Sealed, targets[0].Binding); err == nil {
		t.Fatal("locked Aegis released credentials")
	}
	plaintext := targets[0].Sealed
	plaintext.ciphertext = envelopes[0].Password
	if _, err := aegis.openClusterSSHCredentials(ctx, plaintext, targets[0].Binding); err == nil {
		t.Fatal("plaintext fallback accepted")
	}
	var lifetime int64
	if err := store.db.QueryRowContext(ctx, `SELECT expires_at-created_at FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, targets[0].Binding.TargetID).Scan(&lifetime); err != nil || lifetime != clusterSSHCredentialLifetimeSeconds {
		t.Fatal("unbounded credential lifetime")
	}
	store.db.SetMaxOpenConns(2)
	type claimResult struct {
		lease clusterSSHTargetLease
		err   error
	}
	claims := make(chan claimResult, 2)
	for range 2 {
		go func() {
			lease, err := store.claimClusterSSHTarget(ctx, operationID, targets[0].Binding.TargetID, newClusterUUID())
			claims <- claimResult{lease, err}
		}()
	}
	var winner clusterSSHTargetLease
	winners := 0
	for range 2 {
		result := <-claims
		if result.err == nil {
			winners++
			winner = result.lease
		}
	}
	if winners != 1 {
		t.Fatal("concurrent workers did not produce exactly one owner")
	}
	store.db.SetMaxOpenConns(1)
	for index, target := range targets {
		lease := winner
		var err error
		if index != 0 {
			lease, err = store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID()); err == nil {
			t.Fatal("second worker claimed live target")
		}
		sealed, err := store.loadClusterSSHTargetCredentials(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sealed.ciphertext, envelopes[index].Password) {
			t.Fatal("plaintext persisted")
		}
		opened, err := aegis.openClusterSSHCredentials(ctx, sealed, target.Binding)
		if err != nil || opened.Password != envelopes[index].Password || opened.SudoPassword != envelopes[index].SudoPassword {
			t.Fatal("credential syntax or binding changed")
		}
		for _, value := range []string{fmt.Sprintf("%+v", opened), fmt.Sprintf("%#v", sealed)} {
			if !strings.Contains(value, "redacted") || strings.Contains(value, opened.Password) || strings.Contains(value, sealed.ciphertext) {
				t.Fatal("formatted credentials exposed")
			}
		}
		if _, err := aegis.openClusterSSHCredentials(ctx, sealed, targets[1-index].Binding); err == nil {
			t.Fatal("credential rebound to other host")
		}
		if err := store.renewClusterSSHTarget(ctx, lease); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, target.Binding.TargetID); err != nil {
			t.Fatal(err)
		}
		if err := store.renewClusterSSHTarget(ctx, lease); err == nil {
			t.Fatal("expired holder renewed")
		}
		next, err := store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
		if err != nil || next.Generation <= lease.Generation {
			t.Fatal("new owner did not fence prior generation")
		}
		if _, err := store.loadClusterSSHTargetCredentials(ctx, lease); err == nil {
			t.Fatal("stale holder loaded credential")
		}
		if err := store.renewClusterSSHTarget(ctx, lease); err == nil {
			t.Fatal("stale generation renewed")
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET current_step='prepare' WHERE id=$1`, target.Binding.TargetID); err != nil {
			t.Fatal(err)
		}
		if err := store.renewClusterSSHTarget(ctx, next); err == nil {
			t.Fatal("previous step retained execution authority")
		}
		if _, err := store.loadClusterSSHTargetCredentials(ctx, next); err == nil {
			t.Fatal("previous step loaded credentials")
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, target.Binding.TargetID); err != nil {
			t.Fatal(err)
		}
		next, err = store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
		if err == nil {
			t.Fatal("unimplemented preparation step accepted a target claim")
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET current_step='inspect' WHERE id=$1`, target.Binding.TargetID); err != nil {
			t.Fatal(err)
		}
		next, err = store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.renewClusterSSHTarget(ctx, next); err != nil {
			t.Fatal("new inspection claim not authorized under current controller")
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName); err != nil {
			t.Fatal(err)
		}
		if err := store.renewClusterSSHTarget(ctx, next); err == nil {
			t.Fatal("worker survived controller lease expiry")
		}
		if _, err := store.loadClusterSSHTargetCredentials(ctx, next); err == nil {
			t.Fatal("worker loaded credentials without live controller")
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder=$1,expires_at=$2 WHERE name=$3`, newClusterUUID(), time.Now().Unix()+300, clusterControllerLeaseName); err != nil {
			t.Fatal(err)
		}
		if err := store.renewClusterSSHTarget(ctx, next); err == nil {
			t.Fatal("worker retained old controller authority")
		}
	}
	// Even valid ciphertext copied into another target's row fails inner binding.
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$1 WHERE target_id=$2`, targets[0].Sealed.ciphertext, targets[1].Binding.TargetID); err != nil {
		t.Fatal(err)
	}
	forged := targets[1].Sealed
	forged.ciphertext = targets[0].Sealed.ciphertext
	if _, err := aegis.openClusterSSHCredentials(ctx, forged, targets[1].Binding); err == nil {
		t.Fatal("swapped ciphertext accepted")
	}
}

func TestClusterSSHCredentialsPostgresExpiryRotationResetAndTerminalCleanup(t *testing.T) {
	for _, cause := range []string{"expiry", "rotation", "reset", "succeeded", "failed", "cancelled"} {
		t.Run(cause, func(t *testing.T) {
			store, aegis, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
			insertSSHFixtureTargets(t, store, ctx, operationID, targets)
			lease, err := store.claimClusterSSHTarget(ctx, operationID, targets[0].Binding.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			switch cause {
			case "expiry":
				_, err = store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1`)
			case "rotation":
				key := make([]byte, aegisKeyLength)
				if _, err = rand.Read(key); err != nil {
					t.Fatal(err)
				}
				var verification string
				verification, err = aegisEncryptText(aegisVerificationPlaintext, key)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.db.ExecContext(ctx, `UPDATE engine.aegis_cipher_state SET verification_token=$1 WHERE id=1`, verification)
				aegis.setActiveKey(key)
				if _, openErr := aegis.openClusterSSHCredentials(ctx, targets[0].Sealed, targets[0].Binding); openErr == nil {
					t.Fatal("old Aegis generation decrypted")
				}
			case "reset":
				_, err = store.db.ExecContext(ctx, `DELETE FROM engine.aegis_cipher_state WHERE id=1`)
			default:
				_, err = store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET state=$1 WHERE id=$2`, cause, operationID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.loadClusterSSHTargetCredentials(ctx, lease); err == nil {
				t.Fatal("invalidated credential loaded before cleanup")
			}
			if err := store.renewClusterSSHTarget(ctx, lease); err == nil {
				t.Fatal("invalidated credential retained worker authority")
			}
			if err := store.cleanupClusterSSHCredentials(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_credentials`).Scan(&count); err != nil || count != 0 {
				t.Fatal("active credential retained")
			}
			var state, credentialState string
			if err := store.db.QueryRowContext(ctx, `SELECT state,credential_state FROM engine.cluster_onboarding_targets WHERE id=$1`, lease.TargetID).Scan(&state, &credentialState); err != nil || state != "running" || credentialState != "required" {
				t.Fatal("cleanup erased uncertain target evidence")
			}
			if err := store.cleanupClusterSSHCredentials(ctx); err != nil {
				t.Fatal("restart cleanup not idempotent")
			}
		})
	}
}

func TestClusterSSHCredentialsPostgresRejectsDuplicateHostsAndAegisChangeBeforeCommit(t *testing.T) {
	store, _, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
	duplicate := append([]clusterSSHPlannedTarget(nil), targets...)
	duplicate[1].Key = duplicate[0].Key
	duplicate[1].KeyBase64 = duplicate[0].KeyBase64
	duplicate[1].Binding.Fingerprint = duplicate[0].Binding.Fingerprint
	duplicate[1].Sealed.binding = duplicate[1].Binding
	if validateClusterSSHPlannedTargets(duplicate, 2) == nil {
		t.Fatal("cloned SSH identity accepted as distinct host")
	}
	duplicate = append([]clusterSSHPlannedTarget(nil), targets...)
	duplicate[1].Binding.Address = duplicate[0].Binding.Address
	duplicate[1].Sealed.binding = duplicate[1].Binding
	if validateClusterSSHPlannedTargets(duplicate, 2) == nil {
		t.Fatal("duplicate target address accepted")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.aegis_cipher_state SET verification_token='changed-before-commit' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertClusterSSHPlannedTargets(ctx, tx, operationID, targets[0].Binding.ClusterID, targets); err == nil {
		t.Fatal("stale encryption generation persisted")
	}
	tx.Rollback()
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1`, operationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected transaction retained targets")
	}
}

func TestClusterSSHCredentialsPostgresOperationFences(t *testing.T) {
	for _, cause := range []string{"attempt", "operation_step", "active_operation"} {
		t.Run(cause, func(t *testing.T) {
			store, _, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
			insertSSHFixtureTargets(t, store, ctx, operationID, targets)
			lease, err := store.claimClusterSSHTarget(ctx, operationID, targets[0].Binding.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			switch cause {
			case "attempt":
				_, err = store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, operationID)
			case "operation_step":
				_, err = store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step='other_step' WHERE id=$1`, operationID)
			case "active_operation":
				_, err = store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1 WHERE id=1`, newClusterUUID())
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.renewClusterSSHTarget(ctx, lease); err == nil {
				t.Fatal("old operation authority renewed")
			}
			if _, err := store.loadClusterSSHTargetCredentials(ctx, lease); err == nil {
				t.Fatal("old operation authority loaded credentials")
			}
		})
	}
}
