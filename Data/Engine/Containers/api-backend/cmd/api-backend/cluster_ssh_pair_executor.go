package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"borealis/api-backend/internal/clusterbootstrap"
)

// Runs once inside the original serving API Pod. It never opens an HTTP
// listener or starts normal workers. The operator supplies an existing WebUI
// session, the cipher, and the ordinary pair submission over private stdin.
func runPairRecoveryWorker(ctx context.Context, cfg gatewayConfig, r io.Reader) error {
	pin, err := clusterbootstrap.RecoveryPair()
	if err != nil {
		return err
	}
	var input struct {
		ClaimCommitted bool            `json:"claim_committed,omitempty"`
		Session        string          `json:"session"`
		Cipher         string          `json:"cipher"`
		Submission     json.RawMessage `json:"submission"`
	}
	const limit = clusterSSHQueueBodyMaxBytes + 32768
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || len(data) > limit {
		clear(data)
		return errClusterConflict
	}
	defer clear(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(&struct{}{}) != io.EOF || len(input.Cipher) > 4096 || input.Session == "" || len(input.Session) > 16384 || input.ClaimCommitted == (len(input.Submission) != 0) {
		return errClusterConflict
	}
	defer clear(input.Submission)
	secret := strings.TrimSpace(os.Getenv("SECRET_KEY"))
	if secret == "" {
		raw, err := os.ReadFile(cfg.EngineSecretPath)
		if err != nil {
			return errors.New("original Engine identity unavailable")
		}
		secret = strings.TrimSpace(string(raw))
		clear(raw)
	}
	if secret == "" {
		return errors.New("original Engine identity unavailable")
	}
	db, err := openPostgresPool(cfg, false)
	if err != nil {
		return errClusterUnavailable
	}
	defer db.Close()
	store := &postgresOperatorStore{db: db}
	aegis := newGoAegisService(db, []byte(secret))
	auth := &authService{store: store, aegis: aegis, verifier: &tokenVerifier{secret: []byte(secret), maxAge: cfg.AuthTokenTTL, now: time.Now}, bootstrapGate: &goBootstrapGate{store: store, aegis: aegis}, timeout: cfg.AuthTimeout}
	key, err := aegis.deriveAndVerify(ctx, input.Cipher)
	input.Cipher = ""
	if err != nil {
		return errors.New("original Aegis verification failed")
	}
	aegis.setActiveKey(key)
	clear(key)
	defer aegis.clearActiveKey()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/server/cluster/onboarding/operations/"+pin.Operation+"/resume/"+pin.Plan, bytes.NewReader(input.Submission))
	if err != nil {
		return errClusterConflict
	}
	req.Header.Set("Authorization", "Bearer "+input.Session)
	input.Session = ""
	req.Header.Set("Content-Type", "application/json")
	worker := newClusterSSHPreparationRuntime(store, aegis)
	if worker == nil || !worker.ready() {
		return errors.New("original source worker unavailable")
	}
	if input.ClaimCommitted {
		if _, failure := requireAdmin(ctx, auth, req); failure != nil {
			return errors.New("existing WebUI administrator session rejected")
		}
	} else if err := grantPairRecovery(req, auth, store, aegis, pin); err != nil {
		return err
	}
	req.Header.Del("Authorization")
	pending, err := worker.pending(ctx)
	if err != nil || pending.Operation.ID != pin.Operation {
		return errors.New("fresh grant committed; pair claim unavailable")
	}
	work, err := worker.claim(ctx, pending.Operation, pending.Controller, newClusterUUID())
	if err != nil {
		return errors.New("fresh grant committed; pair claim rejected")
	}
	return worker.execute(ctx, work)
}

// A pre-rollout proof is read on the existing API Pod with its normal database
// role. The deployment script must compare these original identities to the
// live Kubernetes objects before it can patch the controller Pod.
func checkPairRecoveryDeployment(ctx context.Context, cfg gatewayConfig, out io.Writer) error {
	pin, err := clusterbootstrap.RecoveryPair()
	if err != nil {
		return err
	}
	db, err := openPostgresPool(cfg, false)
	if err != nil {
		return errClusterUnavailable
	}
	defer db.Close()
	store := postgresOperatorStore{db: db}
	p, err := store.stoppedPair(ctx, pin.Operation, pin.Plan)
	if err != nil || p.source.Release != pin.Release || len(p.proof.Source.Members) != 1 || p.proof.Source.Members[0].NodeUID != pin.Boot.SourceUID || p.proof.Source.Members[0].BootID != pin.Boot.Before {
		return errClusterConflict
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) != pin.Boot.After {
		return errClusterConflict
	}
	var system string
	if db.QueryRowContext(ctx, "SELECT system_identifier::text FROM pg_control_system() WHERE NOT pg_is_in_recovery()").Scan(&system) != nil || system != pin.Boot.Database {
		return errClusterConflict
	}
	for i, t := range p.approval.Targets {
		var clean bool
		if t.TargetID != pin.Targets[i] || t.QualificationGeneration+3 != pin.Generation ||
			db.QueryRowContext(ctx, `SELECT t.state='running' AND t.current_step='stage_source' AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=$3 AND t.credential_state='required' AND NOT EXISTS
(SELECT 1 FROM engine.cluster_onboarding_credentials c WHERE c.target_id=t.id)
FROM engine.cluster_onboarding_targets t WHERE t.id=$1 AND t.operation_id=$2 AND t.ordinal=$4`, t.TargetID, pin.Operation, pin.Generation-1, i+1).Scan(&clean) != nil || !clean {
			return errClusterConflict
		}
	}
	return json.NewEncoder(out).Encode(map[string]string{"operation": pin.Operation, "plan": pin.Plan, "node": p.proof.Source.Members[0].Name, "node_uid": pin.Boot.SourceUID, "boot": pin.Boot.After, "namespace_uid": p.proof.Source.KubeSystemUID, "controller": p.approval.ControllerHolder, "baseline": p.source.SHA})
}

func openPairControllerStore(cfg gatewayConfig) (operatorStore, func(), error) {
	if clusterbootstrap.IsPairExecutor() {
		// The reserved pair runs against the original, already-installed schema.
		db, err := openPostgresPool(cfg, true)
		if err != nil {
			return nil, nil, err
		}
		return &postgresOperatorStore{db: db}, func() { _ = db.Close() }, nil
	}
	return openControlOperatorStore(cfg)
}
