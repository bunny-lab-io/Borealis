package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func recoveryReissueSQLFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationRecoveryClaim) {
	t.Helper()
	f, _ := newSSHPreparationRecoveryFixture(t, replacement)
	record, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
	claim, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, newClusterUUID())
	if err != nil || claim.custody == nil {
		t.Fatal("actual Q+2 issuance", err)
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
	return f, claim
}

func recoveryReissueSQLRequest(t *testing.T, f sshPreparationAuthorityFixture, previous string) clusterSSHPreparationRecoveryReissueRequest {
	t.Helper()
	var payload string
	if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	return clusterSSHPreparationRecoveryReissueRequest{ID: newClusterUUID(), PreviousID: previous, PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest([]byte(payload)), Holder: newClusterUUID()}
}

func recoveryReissueSQLClaim(t *testing.T, f sshPreparationAuthorityFixture, previous string) clusterSSHPreparationRecoveryReissuedProofClaim {
	t.Helper()
	claim, err := f.c.reissueClusterSSHPreparationRecoveryProof(f.ctx, f.op, recoveryReissueSQLRequest(t, f, previous))
	if err != nil || claim.custody == nil {
		t.Fatal("actual reissued proof", err)
	}
	return claim
}

func recoveryReissueSQLReject(t *testing.T, f sshPreparationAuthorityFixture, request clusterSSHPreparationRecoveryReissueRequest) {
	t.Helper()
	before := sshPreparationRecoveryState(t, f)
	claim, err := f.c.reissueClusterSSHPreparationRecoveryProof(f.ctx, f.op, request)
	if err == nil || claim.custody != nil || len(claim.Claims) != 0 {
		t.Fatal("invalid reissue produced executable custody")
	}
	if before != sshPreparationRecoveryState(t, f) || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("rejected reissue changed state or retained connection")
	}
}

func TestClusterSSHPreparationRecoveryReissuePostgresAtomicLineage(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "pair", true: "replacement"}[replacement], func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, replacement)
			before, err := f.c.readClusterSSHPreparationRecoveryWork(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			events := f.events(t)
			previousID := original.Reservation.ID
			previousClaims := original.Claims
			var previousHistory []clusterSSHPreparationRecoveryProofRecord
			for generation := 3; generation <= 4; generation++ {
				request := recoveryReissueSQLRequest(t, f, previousID)
				claim, err := f.c.reissueClusterSSHPreparationRecoveryProof(f.ctx, f.op, request)
				if err != nil || claim.custody == nil || !reflect.DeepEqual(claim.Lineage.Original, original.Reservation) || len(claim.Lineage.Requests) != generation-2 {
					t.Fatal("lineage issuance", err)
				}
				if !reflect.DeepEqual(claim.Lineage.Requests[:len(previousHistory)], previousHistory) && len(previousHistory) != 0 {
					t.Fatal("reissue rewrote earlier history")
				}
				tip := claim.Lineage.Requests[len(claim.Lineage.Requests)-1]
				if tip.ID != request.ID || tip.PreviousID != previousID || tip.Holder != request.Holder {
					t.Fatal("wrong request binding")
				}
				for i, target := range claim.Claims {
					prior := previousClaims[i]
					if target.Lease.Generation != prior.Lease.Generation+1 || target.Lease.Holder != request.Holder || target.Sealed != prior.Sealed || target.Previous != tip.Targets[i] ||
						target.Previous.PreviousHolder != prior.Lease.Holder || target.Previous.PreviousGeneration != prior.Lease.Generation || target.Previous.PreviousExpiresAt != 1 ||
						validClusterSSHPreparationLease(target.Lease) || validClusterSSHObservationLease(target.Lease) {
						t.Fatal("reissued proof lost exact prior custody or gained initial authority")
					}
					var holder string
					var current, expiry int64
					if f.c.store.db.QueryRowContext(f.ctx, `SELECT lease_holder,lease_generation,lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, target.Lease.TargetID).Scan(&holder, &current, &expiry) != nil || holder != request.Holder || current != target.Lease.Generation || expiry != claim.custody.assigned[target.Lease.TargetID] {
						t.Fatal("issued expiry differs from SQL")
					}
					var remaining float64
					if f.c.store.db.QueryRowContext(f.ctx, `SELECT $1::bigint-extract(epoch FROM clock_timestamp())`, expiry).Scan(&remaining) != nil || remaining <= 0 || remaining > float64(clusterSSHTargetLeaseSeconds) {
						t.Fatal("reissued lease exceeded fixed bound")
					}
				}
				after, err := f.c.readClusterSSHPreparationRecoveryWork(f.ctx, f.op)
				if err != nil || after.work.Payload != before.work.Payload || !reflect.DeepEqual(after.work, before.work) || !reflect.DeepEqual(after.credentials, before.credentials) {
					t.Fatal("reissue changed approved original envelope/source/credentials", err)
				}
				if _, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op); err == nil {
					t.Fatal("initial Q+2 parser accepted v2")
				}
				if _, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, original.Reservation.ID, newClusterUUID()); err == nil {
					t.Fatal("initial claim recreated handle")
				}
				assertSSHPreparationRecoveryInert(t, f)
				// An acknowledgement cannot reconstruct local executable custody,
				// even after every SQL lease expires again.
				recoveryReissueSQLReject(t, f, request)
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
				recoveryReissueSQLReject(t, f, request)
				reused := recoveryReissueSQLRequest(t, f, tip.ID)
				reused.ID = request.ID
				recoveryReissueSQLReject(t, f, reused)
				reused = recoveryReissueSQLRequest(t, f, tip.ID)
				reused.Holder = request.Holder
				recoveryReissueSQLReject(t, f, reused)
				previousID, previousClaims, previousHistory = tip.ID, claim.Claims, claim.Lineage.Requests
			}
			if f.events(t) != events || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("proof reissue published event or retained connection")
			}
			oldEntered := false
			if f.c.withClusterSSHPreparationRecoveryProof(f.ctx, f.aegis, original, func(context.Context, clusterSSHPreparationRecoveryAuthority, func(context.Context) error) error {
				oldEntered = true
				return errors.New("obsolete proof entered")
			}) == nil || oldEntered {
				t.Fatal("Q+2 authority survived newer lineage")
			}
		})
	}
	for _, sameRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct requests one winner", true: "same request one winner"}[sameRequest], func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, false)
			f.c.store.db.SetMaxOpenConns(4)
			requests := []clusterSSHPreparationRecoveryReissueRequest{recoveryReissueSQLRequest(t, f, original.Reservation.ID), recoveryReissueSQLRequest(t, f, original.Reservation.ID)}
			if sameRequest {
				requests[1] = requests[0]
			}
			start := make(chan struct{})
			results := make(chan clusterSSHPreparationRecoveryReissuedProofClaim, 2)
			var wg sync.WaitGroup
			for _, request := range requests {
				wg.Add(1)
				go func(request clusterSSHPreparationRecoveryReissueRequest) {
					defer wg.Done()
					<-start
					claim, _ := f.c.reissueClusterSSHPreparationRecoveryProof(f.ctx, f.op, request)
					results <- claim
				}(request)
			}
			close(start)
			wg.Wait()
			close(results)
			winners := 0
			for claim := range results {
				if claim.custody != nil {
					winners++
					if len(claim.Lineage.Requests) != 1 {
						t.Fatal("unexpected chain")
					}
				}
			}
			if winners != 1 {
				t.Fatal("expected exactly one complete cohort winner", winners)
			}
			for _, request := range requests {
				recoveryReissueSQLReject(t, f, request)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuePostgresRejectsDrift(t *testing.T) {
	for _, mode := range []string{"live sibling", "missing sibling", "prepared sibling", "joined sibling", "generation", "different holders", "controller", "controller expiry", "attempt", "phase", "active pointer", "source", "source member", "credential", "credential expiry", "ciphertext", "aegis", "report", "key", "raw payload", "stale tip", "reused original ID", "reused original holder", "zero expiry", "later holder", "history payload digest", "history prefix", "history original"} {
		t.Run(mode, func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, false)
			previous := original.Reservation.ID
			if strings.HasPrefix(mode, "history") || mode == "later holder" {
				claim := recoveryReissueSQLClaim(t, f, previous)
				previous = claim.Lineage.Requests[0].ID
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
				if strings.HasPrefix(mode, "history") {
					var payload map[string]json.RawMessage
					if json.Unmarshal([]byte(claim.payload), &payload) != nil {
						t.Fatal("payload")
					}
					lineage := claim.Lineage
					switch mode {
					case "history payload digest":
						lineage.Requests[0].PayloadSHA256 = strings.Repeat("b", 64)
					case "history prefix":
						lineage.Requests[0].PreviousRecordSHA256 = strings.Repeat("c", 64)
					case "history original":
						lineage.Original.Targets[0].PreviousHolder = newClusterUUID()
					}
					payload[clusterSSHPreparationRecoveryField], _ = json.Marshal(lineage)
					wire, _ := json.Marshal(payload)
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, string(wire))
				}
			}
			request := recoveryReissueSQLRequest(t, f, previous)
			id := f.targets[len(f.targets)-1].Binding.TargetID
			switch mode {
			case "live sibling":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=extract(epoch FROM clock_timestamp())::bigint+30 WHERE id=$1`, id)
			case "missing sibling":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, id)
			case "prepared sibling":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='prepared',current_step='preparation_complete' WHERE id=$1`, id)
			case "joined sibling":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='joined',current_step='joined' WHERE id=$1`, id)
			case "generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, id)
			case "different holders", "later holder":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, id, newClusterUUID())
			case "zero expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=0 WHERE id=$1`, id)
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName)
			case "controller expiry":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
			case "phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='prepare_ssh_targets' WHERE id=$1`, f.op.ID)
			case "active pointer":
				f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
			case "source":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "source member":
				f.exec(t, `UPDATE engine.cluster_nodes SET node_name=node_name||'-changed' WHERE membership_state='Active'`)
			case "credential":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, id)
			case "credential expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, id)
			case "ciphertext":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, id)
			case "aegis":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
			case "report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, id)
			case "key":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_base64='changed' WHERE id=$1`, id)
			case "raw payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
			case "stale tip":
				request.PreviousID = newClusterUUID()
			case "reused original ID":
				request.ID = original.Reservation.ID
			case "reused original holder":
				request.Holder = original.Reservation.Targets[0].PreviousHolder
			}
			recoveryReissueSQLReject(t, f, request)
		})
	}
	t.Run("raw request hash keeps noncanonical original bytes", func(t *testing.T) {
		f, original := recoveryReissueSQLFixture(t, false)
		f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
		claim := recoveryReissueSQLClaim(t, f, original.Reservation.ID)
		if !reflect.DeepEqual(claim.Lineage.Original, original.Reservation) {
			t.Fatal("canonical chain changed original record")
		}
	})
}

func recoveryReissueSQLFullState(t *testing.T, f sshPreparationAuthorityFixture) string {
	t.Helper()
	var state string
	err := f.c.store.db.QueryRowContext(f.ctx, `SELECT json_build_array(to_jsonb(o),to_jsonb(c),
 (SELECT json_agg(to_jsonb(t) ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(p) ORDER BY p.target_id) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(n) ORDER BY n.id) FROM engine.cluster_nodes n),
 (SELECT json_agg(to_jsonb(l) ORDER BY l.name) FROM engine.cluster_application_leases l WHERE l.name=$2),
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=o.id))::text
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID, clusterControllerLeaseName).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestClusterSSHPreparationRecoveryReissuePostgresPostWriteFence(t *testing.T) {
	for _, clock := range []string{"controller", "credential"} {
		t.Run("post-parent wait/"+clock, func(t *testing.T) { recoveryReissueSQLClockWait(t, clock) })
	}
	for _, mode := range []string{"deleted cohort", "substituted target", "added target", "generation", "holder", "extended expiry", "expired lease", "report", "credential", "payload", "attempt", "parent state", "active pointer", "source", "source member", "controller expiry", "credential expiry", "before expiry extension"} {
		t.Run(mode, func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, mode == "added target")
			request := recoveryReissueSQLRequest(t, f, original.Reservation.ID)
			id := f.targets[len(f.targets)-1].Binding.TargetID
			mutation := ""
			switch mode {
			case "deleted cohort":
				mutation = `DELETE FROM engine.cluster_onboarding_targets WHERE operation_id=NEW.id;`
			case "substituted target":
				mutation = `SELECT * INTO target FROM engine.cluster_onboarding_targets WHERE id='` + id + `'; DELETE FROM engine.cluster_onboarding_targets WHERE id=target.id; target.id='` + newClusterUUID() + `'; INSERT INTO engine.cluster_onboarding_targets SELECT (target).*;`
			case "added target":
				mutation = `SELECT * INTO target FROM engine.cluster_onboarding_targets WHERE id='` + id + `'; target.id='` + newClusterUUID() + `'; target.ordinal=2; target.management_ip='192.0.2.199'; target.host_key_fingerprint=target.host_key_fingerprint||'-added'; INSERT INTO engine.cluster_onboarding_targets SELECT (target).*;`
			case "generation":
				mutation = `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id='` + id + `';`
			case "holder":
				mutation = `UPDATE engine.cluster_onboarding_targets SET lease_holder='` + newClusterUUID() + `' WHERE id='` + id + `';`
			case "extended expiry":
				mutation = `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=lease_expires_at+86400 WHERE id='` + id + `';`
			case "expired lease":
				mutation = `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id='` + id + `';`
			case "report":
				mutation = `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id='` + id + `';`
			case "credential":
				mutation = `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id='` + id + `';`
			case "payload":
				mutation = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=NEW.id;`
			case "attempt":
				mutation = `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=NEW.id;`
			case "parent state":
				mutation = `UPDATE engine.cluster_operations SET state='waiting' WHERE id=NEW.id;`
			case "active pointer":
				mutation = `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1;`
			case "source":
				mutation = `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1;`
			case "source member":
				mutation = `UPDATE engine.cluster_nodes SET node_name=node_name||'-changed' WHERE membership_state='Active';`
			case "controller expiry":
				mutation = `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name='` + clusterControllerLeaseName + `';`
			case "credential expiry":
				mutation = `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id='` + id + `';`
			case "before expiry extension":
				mutation = `NEW.lease_expires_at=NEW.lease_expires_at+86400;`
			}
			table, moment := "engine.cluster_operations", "AFTER"
			if mode == "before expiry extension" {
				table, moment = "engine.cluster_onboarding_targets", "BEFORE"
			}
			// Interpolated identifiers are generated fixture UUIDs, never inputs.
			f.exec(t, `CREATE OR REPLACE FUNCTION engine.test_recovery_reissue_drift() RETURNS trigger LANGUAGE plpgsql AS $body$
 DECLARE target engine.cluster_onboarding_targets%ROWTYPE;
 BEGIN IF pg_trigger_depth()=1 THEN `+mutation+` END IF; RETURN NEW; END; $body$`)
			f.exec(t, `CREATE TRIGGER test_recovery_reissue_drift `+moment+` UPDATE ON `+table+` FOR EACH ROW EXECUTE FUNCTION engine.test_recovery_reissue_drift()`)
			t.Cleanup(func() {
				f.exec(t, `DROP TRIGGER test_recovery_reissue_drift ON `+table)
				f.exec(t, `DROP FUNCTION engine.test_recovery_reissue_drift()`)
			})
			before := recoveryReissueSQLFullState(t, f)
			recoveryReissueSQLReject(t, f, request)
			if after := recoveryReissueSQLFullState(t, f); after != before {
				t.Fatal("trigger rejection did not roll back original source/credentials/rows")
			}
		})
	}
}

func recoveryReissueSQLClockWait(t *testing.T, clock string) {
	t.Helper()
	f, original := recoveryReissueSQLFixture(t, false)
	request := recoveryReissueSQLRequest(t, f, original.Reservation.ID)
	f.c.store.db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	var deadline int64
	if f.c.store.db.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint+2`).Scan(&deadline) != nil {
		t.Fatal("clock")
	}
	if clock == "controller" {
		f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=$2 WHERE name=$1`, clusterControllerLeaseName, deadline)
	} else {
		f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=$2 WHERE target_id=$1`, f.targets[1].Binding.TargetID, deadline)
	}
	// Test-only advisory barrier waits after target and parent writes. Final
	// clock must reject expiration that happens while this transaction waits.
	f.exec(t, `CREATE FUNCTION engine.test_recovery_reissue_wait() RETURNS trigger LANGUAGE plpgsql AS $body$
 BEGIN PERFORM pg_advisory_xact_lock(6812093); RETURN NEW; END; $body$`)
	f.exec(t, `CREATE TRIGGER test_recovery_reissue_wait AFTER UPDATE ON engine.cluster_operations FOR EACH ROW EXECUTE FUNCTION engine.test_recovery_reissue_wait()`)
	t.Cleanup(func() {
		f.exec(t, `DROP TRIGGER test_recovery_reissue_wait ON engine.cluster_operations`)
		f.exec(t, `DROP FUNCTION engine.test_recovery_reissue_wait()`)
	})
	before := recoveryReissueSQLFullState(t, f)
	blocker, err := f.c.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.ExecContext(ctx, `SELECT pg_advisory_xact_lock(6812093)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	joined := false
	go func() { _, err := f.c.reissueClusterSSHPreparationRecoveryProof(ctx, f.op, request); done <- err }()
	defer func() {
		blocker.Rollback()
		cancel()
		if !joined {
			<-done
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	wait := func(query string, args ...any) {
		for {
			var ready bool
			if f.c.store.db.QueryRowContext(ctx, query, args...).Scan(&ready) != nil {
				t.Fatal("barrier/clock observation")
			}
			if ready {
				return
			}
			select {
			case <-tick.C:
			case err := <-done:
				joined = true
				t.Fatal("reissue returned before barrier", err)
			case <-ctx.Done():
				t.Fatal("barrier deadline")
			}
		}
	}
	wait(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event='advisory' AND query LIKE '%UPDATE engine.cluster_operations%')`)
	wait(`SELECT extract(epoch FROM clock_timestamp()) >= $1`, deadline)
	if blocker.Commit() != nil {
		t.Fatal("unlock")
	}
	result := <-done
	joined = true
	if result == nil || recoveryReissueSQLFullState(t, f) != before || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("post-write lock wait bypassed final clock or retained partial state")
	}
}

func TestClusterSSHPreparationRecoveryReissuePostgresRechecksAfterWait(t *testing.T) {
	for _, mode := range []string{"payload", "source", "controller expiry", "credential expiry", "ciphertext", "holder", "expiry", "generation", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, false)
			request := recoveryReissueSQLRequest(t, f, original.Reservation.ID)
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			blocker, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err = blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			joined := false
			go func() { _, err := f.c.reissueClusterSSHPreparationRecoveryProof(ctx, f.op, request); done <- err }()
			defer func() {
				blocker.Rollback()
				cancel()
				if !joined {
					<-done
				}
			}()
			waitSSHPreparationRecoveryControllerLock(t, ctx, f)
			id := f.targets[1].Binding.TargetID
			switch mode {
			case "payload":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
			case "source":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "controller expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "credential expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, id)
			case "ciphertext":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, id)
			case "holder":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, id, newClusterUUID())
			case "expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=2 WHERE id=$1`, id)
			case "generation":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, id)
			case "cancel":
				cancel()
			}
			if err != nil || mode != "cancel" && blocker.Commit() != nil {
				t.Fatal("blocker mutation", err)
			}
			result := <-done
			joined = true
			if mode == "cancel" {
				blocker.Rollback()
			}
			if result == nil {
				t.Fatal("reissue survived post-read authority drift")
			}
			var count int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND lease_holder=$2`, f.op.ID, request.Holder).Scan(&count) != nil || count != 0 {
				t.Fatal("failed CAS retained partial new holder")
			}
			var payload string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload) != nil || strings.Contains(payload, request.ID) || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("failed reissue retained appended history or connection")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuePostgresProofScope(t *testing.T) {
	for _, mode := range []string{"same-second copy", "caller mutation", "ignored check failure", "source loss", "Aegis lock", "expired claim", "canceled entry", "invalid entry"} {
		t.Run(mode, func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, false)
			claim := recoveryReissueSQLClaim(t, f, original.Reservation.ID)
			ctx, cancel := context.WithTimeout(f.ctx, 7*time.Second)
			defer cancel()
			release, started, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var closeOnce sync.Once
			var escaped func(context.Context) error
			consume := func(work context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
				escaped = check
				borrowCtx, borrowCancel := context.WithTimeout(work, time.Second)
				conn, err := f.c.store.db.Conn(borrowCtx)
				borrowCancel()
				if err != nil {
					return errors.New("consumer could not borrow sole SQL connection")
				}
				conn.Close()
				value.Lineage.Requests[0].Targets[0].PreviousHolder = "consumer changed"
				value.Cohort.Targets[0].Key.PublicKey[0] ^= 1
				close(started)
				select {
				case <-release:
					if mode == "ignored check failure" {
						_ = check(work)
						return nil
					}
					return check(work)
				case <-work.Done():
					return work.Err()
				}
			}
			if mode == "canceled entry" {
				cancel()
			}
			if mode == "invalid entry" {
				consume = nil
			}
			if mode == "expired claim" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			}
			go func() {
				var err error
				defer func() { done <- err }()
				err = f.c.withClusterSSHPreparationRecoveryReissuedProofEvery(ctx, f.aegis, claim, 15*time.Second, consume)
			}()
			joined := false
			defer func() {
				cancel()
				closeOnce.Do(func() { close(release) })
				if !joined {
					<-done
				}
			}()
			if mode == "canceled entry" || mode == "invalid entry" || mode == "expired claim" {
				if err := <-done; err == nil || !claim.custody.failed.Load() {
					joined = true
					t.Fatal("invalid scope entry retained usable custody")
				}
				joined = true
			} else {
				select {
				case <-started:
				case err := <-done:
					joined = true
					t.Fatal("entry", err)
				case <-ctx.Done():
					t.Fatal("entry deadline")
				}
				copy := claim
				entered := false
				if f.c.withClusterSSHPreparationRecoveryReissuedProof(ctx, f.aegis, copy, func(context.Context, clusterSSHPreparationRecoveryReissuedProofAuthority, func(context.Context) error) error {
					entered = true
					return nil
				}) == nil || entered || claim.custody.failed.Load() {
					t.Fatal("duplicate copy admitted or poisoned legitimate owner")
				}
				switch mode {
				case "caller mutation":
					claim.Lineage.Requests[0].Targets[0].PreviousHolder = "caller changed"
					claim.Claims[0].Sealed.ciphertext += "changed"
					claim.custody.assigned[claim.Claims[0].Lease.TargetID] = 1
				case "ignored check failure":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[1].Lease.TargetID)
				case "source loss":
					f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
				case "Aegis lock":
					f.aegis.mu.Lock()
					clear(f.aegis.key)
					f.aegis.key = nil
					f.aegis.mu.Unlock()
				}
				closeOnce.Do(func() { close(release) })
				err := <-done
				joined = true
				wantSuccess := mode == "same-second copy" || mode == "caller mutation"
				if (err == nil) != wantSuccess {
					t.Fatal("scope outcome", err)
				}
				if escaped(context.Background()) == nil {
					t.Fatal("escaped callback survived scope")
				}
			}
			entered := false
			if f.c.withClusterSSHPreparationRecoveryReissuedProof(f.ctx, f.aegis, claim, func(context.Context, clusterSSHPreparationRecoveryReissuedProofAuthority, func(context.Context) error) error {
				entered = true
				return nil
			}) == nil || entered {
				t.Fatal("closed/failed scope replayed")
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("proof retained connection")
			}
		})
	}
	t.Run("exact expiry renewal preserves all history", func(t *testing.T) {
		f, original := recoveryReissueSQLFixture(t, false)
		issued := recoveryReissueSQLClaim(t, f, original.Reservation.ID)
		issued.custody.started.Store(true)
		claim, expected, err := freezeClusterSSHPreparationRecoveryReissuedProof(issued)
		if err != nil {
			t.Fatal(err)
		}
		const stable = `SELECT json_build_array(to_jsonb(o),
 (SELECT json_agg(to_jsonb(t)-'lease_expires_at' ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(p) ORDER BY p.target_id) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=o.id))::text FROM engine.cluster_operations o WHERE id=$1`
		var before, after string
		if f.c.store.db.QueryRowContext(f.ctx, stable, f.op.ID).Scan(&before) != nil {
			t.Fatal("history")
		}
		value, next, err := f.c.observeClusterSSHPreparationRecoveryReissuedProof(f.ctx, claim, expected, true)
		if err != nil || !maps.Equal(value.ExpiresAt, next) {
			t.Fatal("renewal", err)
		}
		if f.c.store.db.QueryRowContext(f.ctx, stable, f.op.ID).Scan(&after) != nil || before != after || !maps.Equal(issued.custody.assigned, expected) {
			t.Fatal("renewal changed immutable history/credential expiry")
		}
		for id, expiry := range next {
			var remaining float64
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT $1::bigint-extract(epoch FROM clock_timestamp())`, expiry).Scan(&remaining) != nil || remaining <= 0 || remaining > float64(clusterSSHTargetLeaseSeconds) {
				t.Fatal("renewal lease bound", id)
			}
		}
		wrong := maps.Clone(next)
		wrong[claim.Claims[1].Lease.TargetID]++
		before = recoveryReissueSQLFullState(t, f)
		if _, _, err := f.c.observeClusterSSHPreparationRecoveryReissuedProof(f.ctx, claim, wrong, true); err == nil || before != recoveryReissueSQLFullState(t, f) {
			t.Fatal("foreign expiry partially renewed")
		}
	})
}

func TestClusterSSHPreparationRecoveryReissuePostgresJoinsCancellation(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "heartbeat loss", "escaped check"} {
		t.Run(mode, func(t *testing.T) {
			f, original := recoveryReissueSQLFixture(t, false)
			claim := recoveryReissueSQLClaim(t, f, original.Reservation.ID)
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			started, stopping, cleanup, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			checks := make(chan func(context.Context) error, 1)
			done := make(chan error, 1)
			var once sync.Once
			interval := 30 * time.Millisecond
			if mode == "escaped check" {
				interval = 15 * time.Second
			}
			go func() {
				var err error
				defer func() { done <- err }()
				err = f.c.withClusterSSHPreparationRecoveryReissuedProofEvery(ctx, f.aegis, claim, interval, func(work context.Context, _ clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
					checks <- check
					close(started)
					if mode == "escaped check" {
						select {
						case <-finish:
							return nil
						case <-work.Done():
							return work.Err()
						}
					}
					<-work.Done()
					close(stopping)
					<-cleanup
					return work.Err()
				})
			}()
			joined := false
			defer func() {
				cancel()
				once.Do(func() { close(cleanup) })
				if !joined {
					<-done
				}
			}()
			select {
			case <-started:
			case err := <-done:
				joined = true
				t.Fatal("entry", err)
			case <-ctx.Done():
				t.Fatal("entry deadline")
			}
			if mode == "parent cancellation" {
				cancel()
			}
			if mode == "heartbeat loss" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[1].Lease.TargetID)
			}
			if mode != "escaped check" {
				select {
				case <-stopping:
				case <-time.After(4 * time.Second):
					t.Fatal("consumer not canceled")
				}
				select {
				case <-done:
					joined = true
					t.Fatal("returned before cleanup joined")
				default:
				}
				once.Do(func() { close(cleanup) })
			} else {
				blocker, err := f.c.store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback()
				if _, err = blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
					t.Fatal(err)
				}
				check := <-checks
				checked := make(chan error, 1)
				go func() { checked <- check(context.Background()) }()
				waitSSHPreparationRecoveryControllerLock(t, ctx, f)
				close(finish)
				select {
				case err := <-checked:
					if err == nil {
						t.Fatal("escaped check survived closure")
					}
				case <-time.After(4 * time.Second):
					t.Fatal("escaped callback not joined")
				}
				blocker.Rollback()
			}
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("lost authority succeeded")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("scope join timeout")
			}
			if !claim.custody.failed.Load() || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("failure not poisoned or connection retained")
			}
		})
	}
}
