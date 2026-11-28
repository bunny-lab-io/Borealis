package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

// updateSSHPreparationOwnership compares the same approved operation and every
// claimed target in a single transaction. Only the heartbeat extends expiry.
// Taking locks before reading the database clock prevents a delayed worker
// from renewing an expired claim. A sibling mismatch rolls back both updates.
func (s *postgresOperatorStore) updateSSHPreparationOwnership(parent context.Context, work clusterSSHPreparationWork, renew bool) (time.Time, error) {
	started := time.Now()
	if len(work.Claims) == 0 || len(work.Claims) != len(work.Proof.Cohort.Targets) || len(work.Claims) > 2 {
		return time.Time{}, errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	first := work.Claims[0].Lease
	var ids, ciphertext, fingerprints, addresses []string
	var generations, ports []int64
	for _, claim := range work.Claims {
		l := claim.Lease
		if !validClusterSSHObservationLease(l) || l.Step != first.Step || l.OperationStep != first.OperationStep || l.Holder != first.Holder || l.OperationID != first.OperationID ||
			l.OperationAttempt != first.OperationAttempt || l.ControllerHolder != first.ControllerHolder ||
			claim.Sealed.generation != work.Claims[0].Sealed.generation || claim.Sealed.binding.TargetID != l.TargetID {
			return time.Time{}, errClusterConflict
		}
		ids = append(ids, l.TargetID)
		generations = append(generations, l.Generation)
		ciphertext = append(ciphertext, claim.Sealed.ciphertext)
		fingerprints = append(fingerprints, claim.Sealed.binding.Fingerprint)
		addresses = append(addresses, claim.Sealed.binding.Address)
		ports = append(ports, int64(claim.Sealed.binding.Port))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, errClusterUnavailable
	}
	defer tx.Rollback()
	// Parent and credential writers use the same parent-before-target order.
	// A separate statement below observes post-lock state and clock.
	var locked int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM (
 SELECT t.id FROM engine.cluster_application_leases l, engine.cluster_state c, engine.cluster_operations o,
 engine.aegis_cipher_state a, engine.cluster_onboarding_credentials p, engine.cluster_onboarding_targets t
 WHERE l.name=$1 AND c.id=1 AND o.id=$2 AND a.id=1 AND t.operation_id=o.id AND p.target_id=t.id
 ORDER BY t.id FOR SHARE OF l,c,o,a,p FOR UPDATE OF t) locked`,
		clusterControllerLeaseName, first.OperationID).Scan(&locked); err != nil || locked != len(ids) {
		return time.Time{}, errClusterConflict
	}
	const eligible = `WITH moment AS MATERIALIZED (SELECT extract(epoch FROM clock_timestamp()) AS now),
 approved AS (SELECT * FROM unnest($1::text[], $2::bigint[], $3::text[], $4::text[], $5::text[], $6::bigint[])
 AS v(id,generation,ciphertext,fingerprint,address,port)),
 eligible AS (SELECT t.id, floor(least(moment.now+$15,p.expires_at))::bigint AS expiry,
 floor(1000*(least(l.expires_at,p.expires_at,case when $16 then floor(moment.now+$15) else t.lease_expires_at end)-moment.now))::bigint AS valid_ms
 FROM moment,approved v,engine.cluster_onboarding_targets t,engine.cluster_onboarding_credentials p,
 engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l,engine.aegis_cipher_state a
 WHERE o.id=$7 AND o.attempt=$8 AND o.kind='ssh_onboarding' AND o.state=$17 AND o.current_step=$18
 AND encode(sha256(convert_to(o.payload_json,'UTF8')),'hex')=$9 AND (coalesce(o.error_text,'')='' OR ($17='waiting' AND o.error_text=$20)) AND c.id=1 AND c.active_operation_id=o.id AND c.hmr_state='inactive'
 AND c.cluster_id=t.cluster_id AND l.name=$10 AND l.holder=$11 AND l.expires_at>moment.now
 AND a.id=1 AND a.verification_token=$12 AND t.id=v.id AND t.operation_id=o.id AND t.operation_attempt=o.attempt
 AND t.state='running' AND t.current_step=$19 AND t.lease_holder=$13 AND t.lease_generation=v.generation
 AND t.lease_expires_at>moment.now AND t.host_key_fingerprint=v.fingerprint AND t.management_ip=v.address AND t.ssh_port=v.port
 AND t.credential_state='available' AND p.target_id=t.id AND p.expires_at>moment.now AND p.aegis_generation=$12
 AND p.ciphertext=v.ciphertext AND c.baseline_sha=$14 AND c.enabled=1 AND c.cluster_id=$21 AND c.active_size=$22 AND c.desired_size=$23 AND c.status=$24
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_nodes n WHERE n.membership_state='Active' AND n.application_state<>'active'))`
	state := "running"
	if first.isNetworkQualification() {
		state = "waiting"
	}
	args := []any{pq.Array(ids), pq.Array(generations), pq.Array(ciphertext), pq.Array(fingerprints), pq.Array(addresses), pq.Array(ports),
		first.OperationID, first.OperationAttempt, sshPreparationFenceDigest(work.Payload), clusterControllerLeaseName, first.ControllerHolder,
		work.Claims[0].Sealed.generation, first.Holder, work.Baseline.SourceSHA, clusterSSHTargetLeaseSeconds, renew, state, first.OperationStep, first.Step, clusterSSHStageReady, work.Proof.Source.ClusterID, work.Proof.Source.ActiveSize, work.Proof.Source.DesiredSize, work.Proof.Source.Status}
	query := eligible
	if renew {
		query += `, renewed AS (UPDATE engine.cluster_onboarding_targets t SET lease_expires_at=e.expiry
 FROM eligible e WHERE t.id=e.id RETURNING e.valid_ms) SELECT count(*),coalesce(min(valid_ms),0) FROM renewed`
	} else {
		query += ` SELECT count(*),coalesce(min(valid_ms),0) FROM eligible`
	}
	var count int
	var validMillis int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count, &validMillis); err != nil {
		return time.Time{}, errClusterUnavailable
	}
	until := started.Add(time.Duration(validMillis) * time.Millisecond)
	if count != len(ids) || !time.Now().Before(until) {
		return time.Time{}, errClusterConflict
	}
	if renew {
		err = tx.Commit()
	} else {
		err = tx.Rollback()
	}
	if err != nil || !time.Now().Before(until) {
		return time.Time{}, errClusterConflict
	}
	return until, nil
}

type clusterSSHPairOwnerKey struct{}

// The single ticker renews both rows and publishes the earliest confirmed
// deadline. Action dispatch can check custody without extending the grant.
func (s *postgresOperatorStore) withSSHOperationOwnership(parent context.Context, aegis *goAegisService, work clusterSSHPreparationWork, interval, maximum time.Duration, execute func(context.Context, func(context.Context) error) error) error {
	var confirmed atomic.Pointer[time.Time]
	check := func(ctx context.Context, renew bool) error {
		if aegis.verifyClusterSSHMemoryKey(ctx, work.Claims[0].Sealed.generation) != nil {
			reportClusterSSHPrejoinCheck(ctx, "Aegis locked or generation changed")
			return clusterbootstrap.ErrSessionAuthority
		}
		until, err := s.updateSSHPreparationOwnership(ctx, work, renew)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "paired controller, target or credential ownership")
			return clusterbootstrap.ErrSessionAuthority
		}
		if renew {
			confirmed.Store(&until)
		}
		return ctx.Err()
	}
	parent = clusterbootstrap.WithPreparationDeadline(parent, func() time.Time {
		if p := confirmed.Load(); p != nil {
			return *p
		}
		return time.Time{}
	})
	boundary := func(ctx context.Context) error { return check(ctx, false) }
	parent = context.WithValue(parent, clusterSSHPairOwnerKey{}, boundary)
	return runClusterSSHPreparationScopeFor(parent, interval, maximum, func(ctx context.Context) error { return check(ctx, true) }, func(ctx context.Context) error { return execute(ctx, boundary) })
}
