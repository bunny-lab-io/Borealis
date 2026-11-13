package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"
)

type clusterSSHPreparationArtifactGate func(context.Context, string) (string, error)
type clusterSSHPreparationArtifactDemandRead func(context.Context, string) ([]clusterSSHFilesystemDemand, error)
type clusterSSHPreparationArtifactConsumer func(context.Context, clusterSSHPreparationArtifactGate, clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error)

type clusterSSHPreparationArtifactBytes interface {
	prove(context.Context, func(context.Context) error) (string, error)
	storageDemand(context.Context, func(context.Context) error) ([]clusterSSHFilesystemDemand, error)
	Close() error
}

type clusterSSHPreparationArtifactSource struct {
	Observation string
	Postgres    string
}

// Private seams exercise scope ownership independently of bulk fixture bytes.
// Production constructs every reader/acquirer below from native implementations.
type clusterSSHPreparationArtifactDependencies struct {
	proposal    func(context.Context) error
	source      func(context.Context, func(context.Context) error) (clusterSSHPreparationArtifactSource, error)
	publication func(context.Context, func(context.Context) error) (string, error)
	acquire     func(context.Context, string, func(context.Context) error) (clusterSSHPreparationArtifactBytes, error)
	terminal    func(clusterSSHPreparationReadinessReceipt, string) (func(context.Context) error, error)
}

// Only the immutable_artifacts gate is exposed. No target lease, SSH connection,
// native journal receipt, capacity grant or production polling is created.
// Acquisition uses existing artifact bounds and the live waiting proposal expiry.
// Once bytes are owned, complete reproof/consumption keeps the existing 30-second
// approval bound; a release which cannot fit that bound remains unqualified.
func runClusterSSHPreparationArtifacts(parent context.Context, plan string, d clusterSSHPreparationArtifactDependencies, consume clusterSSHPreparationArtifactConsumer) (result error) {
	if parent.Err() != nil || !validClusterSSHPreparationDigest(plan) || d.proposal == nil || d.source == nil || d.publication == nil || d.acquire == nil || d.terminal == nil || consume == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var owned clusterSSHPreparationArtifactBytes
	cleanup := func() {
		if owned != nil {
			if owned.Close() != nil {
				result = clusterbootstrap.ErrPreparationConfig
			}
			owned = nil
		}
	}
	defer func() {
		cleanup()
		if ctx.Err() != nil {
			result = clusterbootstrap.ErrPreparationConfig
		}
	}()
	var retainedSource clusterSSHPreparationArtifactSource
	var retainedPublication, retainedProof string
	initialized := false
	observe := func(ctx context.Context, check func(context.Context) error) error {
		if ctx.Err() != nil || check(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		source, err := d.source(ctx, check)
		if err != nil || !validClusterSSHPreparationDigest(source.Observation) || !clusterbootstrap.ValidPostgresImageReference(source.Postgres) || check(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		publication, err := d.publication(ctx, check)
		if err != nil || !validClusterSSHPreparationDigest(publication) || check(ctx) != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		if initialized && (source != retainedSource || publication != retainedPublication) {
			return clusterbootstrap.ErrPreparationConfig
		}
		retainedSource, retainedPublication, initialized = source, publication, true
		return nil
	}
	prove := func(ctx context.Context, check func(context.Context) error) (string, error) {
		if observe(ctx, check) != nil || owned == nil {
			return "", clusterbootstrap.ErrPreparationConfig
		}
		proof, err := owned.prove(ctx, check)
		if err != nil || !validClusterSSHPreparationDigest(proof) || observe(ctx, check) != nil {
			return "", clusterbootstrap.ErrPreparationConfig
		}
		digest, err := clusterSSHPreparationDigest([]string{"preparation-immutable-artifacts-v1", plan, retainedSource.Observation, retainedSource.Postgres, retainedPublication, proof})
		if err != nil || (retainedProof != "" && digest != retainedProof) {
			return "", clusterbootstrap.ErrPreparationConfig
		}
		return digest, nil
	}
	// Stop and join the waiting-only acquisition heartbeat before consumption:
	// an exact approval commit must not race a background waiting-phase read.
	if runClusterSSHArtifactPreparationScope(ctx, 5*time.Second, d.proposal, func(ctx context.Context) error {
		if observe(ctx, d.proposal) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		var err error
		owned, err = d.acquire(ctx, retainedSource.Postgres, d.proposal)
		if err != nil || owned == nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return observe(ctx, d.proposal)
	}) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	proofCtx, stopProof := context.WithTimeout(ctx, 30*time.Second)
	defer stopProof()
	defer func() {
		cleanup()
		if proofCtx.Err() != nil {
			result = clusterbootstrap.ErrPreparationConfig
		}
	}()
	var err error
	retainedProof, err = prove(proofCtx, d.proposal)
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	readerCtx, stopReaders := context.WithCancel(proofCtx)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: stopReaders}
	defer func() {
		if lifetime.close() != nil {
			result = clusterbootstrap.ErrPreparationConfig
		}
	}()
	serial := make(chan struct{}, 1)
	gate := func(parent context.Context, requested string) (string, error) {
		var digest string
		err := lifetime.run(parent, func(ctx context.Context) error {
			if requested != plan {
				return clusterbootstrap.ErrPreparationConfig
			}
			select {
			case serial <- struct{}{}:
				defer func() { <-serial }()
			case <-ctx.Done():
				return clusterbootstrap.ErrPreparationConfig
			}
			var err error
			digest, err = prove(ctx, d.proposal)
			return err
		})
		if err != nil {
			return "", clusterbootstrap.ErrPreparationConfig
		}
		return digest, nil
	}
	demand := func(parent context.Context, requested string) ([]clusterSSHFilesystemDemand, error) {
		var value []clusterSSHFilesystemDemand
		err := lifetime.run(parent, func(ctx context.Context) error {
			if requested != plan {
				return clusterbootstrap.ErrPreparationConfig
			}
			select {
			case serial <- struct{}{}:
				defer func() { <-serial }()
			case <-ctx.Done():
				return clusterbootstrap.ErrPreparationConfig
			}
			if observe(ctx, d.proposal) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			var err error
			value, err = owned.storageDemand(ctx, d.proposal)
			if err != nil || len(value) == 0 || observe(ctx, d.proposal) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			value = slices.Clone(value)
			return nil
		})
		if err != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		return value, nil
	}
	readiness, err := consume(readerCtx, gate, demand)
	if err != nil {
		cancel()
	}
	if closeErr := lifetime.close(); err != nil || closeErr != nil || proofCtx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	terminal, err := d.terminal(readiness, retainedProof)
	if err != nil || terminal == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	if _, err := prove(proofCtx, terminal); err != nil || terminal(proofCtx) != nil || proofCtx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

// Controller-only composition. Waiting proposal reads release SQL connections
// before source/publication/archive IO. Terminal checks recognize only the exact
// committed payload returned by the synchronous approval consumer.
func (c *clusterController) withPreparationArtifacts(parent context.Context, snapshot clusterSSHPreparationApprovalSnapshot, scratch string, consume clusterSSHPreparationArtifactConsumer) error {
	if c == nil || c.store == nil || c.store.db == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.kube == nil || runner.namespace != "borealis" || runner.controllerHolder != c.holder {
		return clusterbootstrap.ErrPreparationConfig
	}
	// Own the full proposal before exposing closures; copied caller slices cannot
	// relabel either the original source or any target after scope construction.
	wire, err := json.Marshal(snapshot.work)
	if err != nil || len(snapshot.work.Claims) != 0 {
		return clusterbootstrap.ErrPreparationConfig
	}
	var work clusterSSHPreparationWork
	if json.Unmarshal(wire, &work) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	snapshot = clusterSSHPreparationApprovalSnapshot{work: work, credentials: slices.Clone(snapshot.credentials)}
	plan, err := clusterSSHPreparationPlanDigest(work.Authorization)
	if err != nil || work.Authorization.ControllerHolder != c.holder || work.Baseline.Validate() != nil || validateClusterSSHInspectionCohort(work.Proof.Cohort, work.Proof.Source) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	var version string
	if clusterBootstrapObject([]byte(work.Payload), map[string]any{"source_k3s_version": &version}) != nil || version != clusterbootstrap.K3sPins().Version {
		return clusterbootstrap.ErrPreparationConfig
	}
	operation := clusterControllerOperation{ID: work.Authorization.OperationID, Attempt: work.Authorization.Attempt, Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	proposal := func(ctx context.Context) error {
		bound, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		current, _, err := c.loadClusterSSHPreparationApproval(bound, operation)
		currentWire, marshalErr := json.Marshal(current.work)
		if err != nil || marshalErr != nil || !bytes.Equal(wire, currentWire) || !slices.Equal(current.credentials, snapshot.credentials) || bound.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	d := clusterSSHPreparationArtifactDependencies{proposal: proposal}
	d.source = func(ctx context.Context, check func(context.Context) error) (clusterSSHPreparationArtifactSource, error) {
		return readClusterSSHPreparationArtifactSource(ctx, work.Proof.Source, runner.kube.getClusterSSHStorageJSON, check)
	}
	d.publication = func(ctx context.Context, check func(context.Context) error) (string, error) {
		if check(ctx) != nil {
			return "", clusterbootstrap.ErrPreparationConfig
		}
		proof, err := readClusterSSHPreparationPublication(ctx, work.Baseline, version)
		if err != nil || check(ctx) != nil {
			return "", clusterbootstrap.ErrPreparationConfig
		}
		return proof, nil
	}
	d.acquire = func(ctx context.Context, postgres string, check func(context.Context) error) (clusterSSHPreparationArtifactBytes, error) {
		return acquireClusterSSHPreparationArtifacts(ctx, scratch, work.Baseline, version, postgres, check)
	}
	d.terminal = func(receipt clusterSSHPreparationReadinessReceipt, artifact string) (func(context.Context) error, error) {
		if reflect.DeepEqual(receipt, clusterSSHPreparationReadinessReceipt{}) {
			return proposal, nil
		}
		return c.clusterSSHPreparationArtifactTerminal(snapshot, receipt, artifact)
	}
	return runClusterSSHPreparationArtifacts(parent, plan, d, consume)
}

func readClusterSSHPreparationArtifactSource(ctx context.Context, expected clusterSSHSourceCohort, getJSON func(context.Context, string, any) error, check func(context.Context) error) (clusterSSHPreparationArtifactSource, error) {
	fail := func() (clusterSSHPreparationArtifactSource, error) {
		return clusterSSHPreparationArtifactSource{}, clusterbootstrap.ErrPreparationConfig
	}
	if getJSON == nil || check == nil || check(ctx) != nil {
		return fail()
	}
	get := func(ctx context.Context, path string, out any) error {
		if check(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		err := getJSON(ctx, path, out)
		if check(ctx) != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return err
	}
	before, err := observeClusterSSHSourceCohort(ctx, get, expected)
	if err != nil || !reflect.DeepEqual(before, expected) {
		return fail()
	}
	storage, err := observeClusterSSHStorage(ctx, expected, get)
	if err != nil || !storage.Requirements.PostgresImage.valid() {
		return fail()
	}
	after, err := observeClusterSSHSourceCohort(ctx, get, expected)
	if err != nil || !reflect.DeepEqual(after, expected) || check(ctx) != nil || ctx.Err() != nil {
		return fail()
	}
	digest, err := clusterSSHPreparationDigest(struct {
		Source  clusterSSHSourceCohort
		Storage string
	}{expected, storage.Requirements.observation})
	if err != nil || !validClusterSSHPreparationDigest(storage.Requirements.observation) {
		return fail()
	}
	return clusterSSHPreparationArtifactSource{digest, storage.Requirements.PostgresImage.Resolved}, nil
}

type clusterSSHOwnedPreparationArtifacts struct {
	mu          sync.Mutex
	failed      bool
	bundle      *clusterbootstrap.Bundle
	artifacts   *clusterbootstrap.PreparationArtifacts
	packages    *clusterbootstrap.HostPackageSet
	expected    clusterbootstrap.Expected
	version     string
	manifest    *clusterbootstrap.Manifest
	publication func(context.Context) error
	demand      []clusterSSHFilesystemDemand
}

func (v *clusterSSHOwnedPreparationArtifacts) prove(ctx context.Context, check func(context.Context) error) (proof string, result error) {
	if v == nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	defer func() { v.failed = v.failed || result != nil }()
	return v.proveOwned(ctx, check)
}

func (v *clusterSSHOwnedPreparationArtifacts) proveOwned(ctx context.Context, check func(context.Context) error) (string, error) {
	if v.failed || v.bundle == nil || v.artifacts == nil || v.packages == nil || v.publication == nil || check == nil || ctx.Err() != nil || check(ctx) != nil || v.publication(ctx) != nil || check(ctx) != nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	bundle, err := v.bundle.PreparationArtifactProof(ctx, v.expected, v.version, v.manifest, check)
	if err != nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	h := sha256.New()
	if v.artifacts.WriteArtifacts(ctx, h, check) != nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	packages := sha256.New()
	if v.packages.WriteArchive(ctx, packages, check) != nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	again, err := v.bundle.PreparationArtifactProof(ctx, v.expected, v.version, v.manifest, check)
	if err != nil || again != bundle || v.publication(ctx) != nil || check(ctx) != nil || ctx.Err() != nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return clusterSSHPreparationDigest([]string{bundle, hex.EncodeToString(h.Sum(nil)), hex.EncodeToString(packages.Sum(nil))})
}

// Artifact materialization only: installed package content, generated host/join
// configuration and Longhorn replicas remain separate inputs. Not storage ready.
// Reproof lends already-acquired bytes; it never reacquires another archive.
func (v *clusterSSHOwnedPreparationArtifacts) storageDemand(ctx context.Context, check func(context.Context) error) ([]clusterSSHFilesystemDemand, error) {
	return v.storageDemandWith(ctx, check, v.proveOwned)
}

// Private seam exercises the owned lifetime without downloading pinned runtime
// artifacts. Production always supplies the same owner's complete byte proof.
func (v *clusterSSHOwnedPreparationArtifacts) storageDemandWith(ctx context.Context, check func(context.Context) error,
	prove func(context.Context, func(context.Context) error) (string, error)) (demand []clusterSSHFilesystemDemand, result error) {
	if v == nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	defer func() { v.failed = v.failed || result != nil }()
	if v.failed || len(v.demand) == 0 || check == nil || prove == nil || ctx.Err() != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	proof, err := prove(ctx, check)
	if err != nil || !validClusterSSHPreparationDigest(proof) || check(ctx) != nil || ctx.Err() != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return slices.Clone(v.demand), nil
}

func (v *clusterSSHOwnedPreparationArtifacts) Close() error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.failed, v.demand = true, nil
	var errs []error
	if v.packages != nil {
		errs = append(errs, v.packages.Close())
		v.packages = nil
	}
	if v.artifacts != nil {
		errs = append(errs, v.artifacts.Close())
		v.artifacts = nil
	}
	if v.bundle != nil {
		errs = append(errs, v.bundle.Close())
		v.bundle = nil
	}
	return errors.Join(errs...)
}

// Reuse the inventories already verified during acquisition, including the
// PostgreSQL archive's native measurement. Preserve existing retained-generation
// and expanded-entry accounting; worker scratch is not a target charge.
func clusterSSHPreparationArtifactDemand(expected clusterbootstrap.Expected, version, postgres string,
	application clusterSSHImageInventory, external clusterSSHExternalInventory, k3s clusterSSHK3sInventory,
	managerBytes int64, pg clusterbootstrap.PostgresImageDemand) ([]clusterSSHFilesystemDemand, error) {
	if expected.Validate() != nil || version != clusterbootstrap.K3sPins().Version || application.expected != expected || external.expected != expected ||
		k3s.expected != expected || k3s.version != version || !clusterbootstrap.ValidPostgresImageReference(postgres) || pg.Reference != postgres ||
		managerBytes < 1 || managerBytes > clusterbootstrap.MaxBundleBytes ||
		pg.IncomingBytes == 0 || pg.IncomingEntries != 1 || pg.RuntimeBytes == 0 || pg.RuntimeEntries == 0 {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	appDemand, err := application.demands()
	if err != nil {
		return nil, err
	}
	externalDemand, err := external.demands()
	if err != nil {
		return nil, err
	}
	k3sDemand, err := k3s.demands()
	if err != nil {
		return nil, err
	}
	return mergeClusterSSHFilesystemDemands(appDemand, externalDemand, k3sDemand, []clusterSSHFilesystemDemand{
		{Path: "/opt/Borealis", Bytes: pg.IncomingBytes, Entries: pg.IncomingEntries + 1},
		{Path: "/var/lib/rancher/k3s", Bytes: pg.RuntimeBytes, Entries: pg.RuntimeEntries},
		// Fixed preparation transport owns one upload on /var/tmp and one root
		// copy under /opt. The root wrapper owns all source/artifact scratch,
		// already covered above; charge only its extra directory and helper here.
		{Path: "/opt/Borealis", Bytes: uint64(managerBytes), Entries: 2},
		{Path: "/var/tmp", Bytes: uint64(managerBytes), Entries: 2},
	})
}

func acquireClusterSSHPreparationArtifacts(ctx context.Context, scratch string, expected clusterbootstrap.Expected, version, postgres string, check func(context.Context) error) (_ clusterSSHPreparationArtifactBytes, result error) {
	var closes []func() error
	defer func() {
		if result != nil {
			for i := len(closes) - 1; i >= 0; i-- {
				if closes[i]() != nil {
					result = clusterbootstrap.ErrPreparationConfig
				}
			}
		}
	}()
	if check == nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	bundle, err := prepareClusterSSHBootstrap(ctx, expected, scratch)
	if bundle != nil {
		closes = append(closes, bundle.Close)
	}
	if err != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	manifest, bootstrap, err := readClusterSSHBootstrapPublication(ctx, expected)
	if err != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	packages, err := resolveClusterSSHHostPackageInventory(ctx, expected)
	if err != nil {
		return nil, err
	}
	packageBytes, err := packages.stage(ctx, scratch, check)
	if packageBytes != nil {
		closes = append(closes, packageBytes.Close)
	}
	if err != nil {
		return nil, err
	}
	packageDemand, err := packages.demands(ctx, packageBytes, check)
	if err != nil {
		return nil, err
	}
	application, err := resolveClusterSSHImageInventory(ctx, expected)
	if err != nil {
		return nil, err
	}
	images, err := application.stage(ctx, scratch, check)
	if images != nil {
		closes = append(closes, images.Close)
	}
	if err != nil {
		return nil, err
	}
	external, err := resolveClusterSSHExternalInventory(ctx, expected)
	if err != nil {
		return nil, err
	}
	externalImages, err := external.stage(ctx, scratch, check)
	if externalImages != nil {
		closes = append(closes, externalImages.Close)
	}
	if err != nil {
		return nil, err
	}
	k3s, err := resolveClusterSSHK3sInventory(ctx, expected, version)
	if err != nil {
		return nil, err
	}
	k3sInputs, err := k3s.stage(ctx, scratch, check)
	if k3sInputs != nil {
		closes = append(closes, k3sInputs.Close)
	}
	if err != nil {
		return nil, err
	}
	pg, err := clusterbootstrap.AcquirePostgresImage(ctx, scratch, postgres, check)
	if pg != nil {
		closes = append(closes, pg.Close)
	}
	if err != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	pgDemand, err := pg.StorageDemand(ctx, check)
	if err != nil {
		return nil, err
	}
	managerSource, _, managerBytes, err := bundle.ManagerProof()
	if err != nil || managerSource != expected || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	demand, err := clusterSSHPreparationArtifactDemand(expected, version, postgres, application, external, k3s, managerBytes, pgDemand)
	if err != nil {
		return nil, err
	}
	demand, err = mergeClusterSSHFilesystemDemands(demand, packageDemand)
	if err != nil {
		return nil, err
	}
	artifacts, err := clusterbootstrap.NewPreparationArtifacts(ctx, expected, version, postgres, images, k3sInputs, externalImages, pg, check)
	if err != nil {
		return nil, err
	}
	closes = []func() error{bundle.Close, artifacts.Close, packageBytes.Close}
	publication, err := clusterSSHPreparationArtifactPublication(expected, bootstrap, application.assets, external.assets, k3s.assets, packages.assets)
	if err != nil || publication(ctx) != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return &clusterSSHOwnedPreparationArtifacts{bundle: bundle, artifacts: artifacts, packages: packageBytes, expected: expected, version: version, manifest: manifest, publication: publication, demand: demand}, nil
}

// Retain the exact asset generation used by each native staging resolver.
// Fresh metadata must still publish those same bytes, including after other
// families were acquired. A reverted publication cannot relabel owned bytes.
func clusterSSHPreparationArtifactPublication(expected clusterbootstrap.Expected, groups ...map[string]clusterBootstrapAsset) (func(context.Context) error, error) {
	if expected.Validate() != nil || len(groups) != 5 {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	assets, limits, ids := map[string]clusterBootstrapAsset{}, map[string]int64{}, map[int64]bool{}
	for _, group := range groups {
		if len(group) == 0 {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		for name, asset := range group {
			if _, exists := assets[name]; exists || ids[asset.ID] {
				return nil, clusterbootstrap.ErrPreparationConfig
			}
			assets[name], limits[name], ids[asset.ID] = asset, asset.Size, true
		}
	}
	return func(ctx context.Context) error {
		fresh, err := resolveClusterPackagedAssets(ctx, expected, limits)
		if err != nil || !reflect.DeepEqual(fresh, assets) || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}, nil
}

// This final read can recognize one exact already committed approval. It never
// writes state or substitutes running state for the original typed proposal.
func (c *clusterController) clusterSSHPreparationArtifactTerminal(snapshot clusterSSHPreparationApprovalSnapshot, receipt clusterSSHPreparationReadinessReceipt, artifact string) (func(context.Context) error, error) {
	w := snapshot.work
	if c == nil || c.store == nil || c.store.db == nil || c.holder != w.Authorization.ControllerHolder || !validClusterSSHPreparationDigest(artifact) || len(receipt.Checks) != 6 || receipt.Checks[0].SHA256 != artifact {
		return nil, errClusterConflict
	}
	raw, err := json.Marshal(receipt)
	if err != nil || parseClusterSSHPreparationReadiness(raw, w.Authorization) != nil {
		return nil, errClusterConflict
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(w.Payload), &payload) != nil || payload["ssh_preparation_authorization"] != nil || payload["ssh_preparation_confirmation"] != nil || payload["ssh_preparation_readiness"] != nil {
		return nil, errClusterConflict
	}
	confirmation, err := parseClusterSSHPreparationRequest(payload["ssh_preparation_request"], w.Authorization, w.Proof.Source)
	if err != nil {
		return nil, errClusterConflict
	}
	qualification, err := parseClusterSSHQualificationReport(string(payload["ssh_qualification"]), w.Authorization.Attempt)
	if err != nil || !completedClusterSSHQualification(qualification) {
		return nil, errClusterConflict
	}
	w.Authorization.ConfirmationSHA256, err = clusterSSHPreparationDigest(confirmation)
	if err != nil {
		return nil, errClusterConflict
	}
	payload["ssh_preparation_authorization"], err = json.Marshal(w.Authorization)
	if err != nil {
		return nil, errClusterConflict
	}
	payload["ssh_preparation_confirmation"], err = json.Marshal(confirmation)
	if err != nil {
		return nil, errClusterConflict
	}
	payload["ssh_preparation_readiness"] = raw
	updated, err := json.Marshal(payload)
	if err != nil || len(updated) > 256<<10 {
		return nil, errClusterConflict
	}
	if _, _, err := parseClusterSSHPreparationAuthorization(string(updated), w.Proof, w.Source); err != nil {
		return nil, errClusterConflict
	}
	check, err := clusterSSHPreparationProposalCheck(snapshot)
	if err != nil {
		return nil, errClusterConflict
	}
	return func(ctx context.Context) error {
		if ctx.Err() != nil {
			return errClusterConflict
		}
		tx, err := c.store.db.BeginTx(ctx, nil)
		if err != nil {
			return errClusterUnavailable
		}
		defer tx.Rollback()
		if check(ctx, tx) != nil {
			return errClusterConflict
		}
		var present int
		err = tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT 1 FROM moment,engine.cluster_operations o,engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND s.id=1 AND s.active_operation_id=o.id AND l.name=$5 AND l.holder=$6 AND l.expires_at>moment.now
 AND $7::bigint>moment.now-$9 AND $7::bigint<=moment.now AND $8::bigint>moment.now-$9 AND $8::bigint<=moment.now
 AND $8::bigint>=$7::bigint AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$10
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=o.id AND (t.inspected_at>moment.now-$9 AND t.inspected_at<=moment.now AND p.expires_at>moment.now) IS NOT TRUE)`,
			w.Authorization.OperationID, clusterSSHPreparationOperationStep, w.Authorization.Attempt, string(updated), clusterControllerLeaseName, c.holder,
			qualification.ObservedAt, confirmation.ConfirmedAt, clusterSSHInspectionLifetimeSeconds, len(w.Proof.Cohort.Targets)).Scan(&present)
		if err != nil || present != 1 || ctx.Err() != nil {
			return errClusterConflict
		}
		return nil
	}, nil
}
