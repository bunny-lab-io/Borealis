package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

const clusterSSHRuntimeSecretPath = "/api/v1/namespaces/borealis/secrets/borealis-api-backend-runtime-env"

var clusterSSHSourceJobPathRE = regexp.MustCompile(`^/apis/batch/v1/namespaces/borealis/jobs/borealis-source-[0-9a-f]{32}$`)

// Each authority read must freshly validate sole-controller ownership, exact
// target claim, complete cohort, immutable recorded baseline and Aegis before
// returning its DB connection. No callback may retain a transaction or select
// source identity from this API replica's process environment.
type clusterSSHPreparationAuthority struct {
	Cohort         clusterSSHInspectionCohort
	Source         clusterSSHSourceCohort
	Queue          clusterSSHQueueSource
	Lease          clusterSSHTargetLease
	Baseline       clusterbootstrap.Expected
	K3sVersion     string
	PayloadSHA256  string
	Recovery       *clusterSSHIdentityRecoveryAuthorityBinding
	IdentitySource *engineidentity.Binding
	payload        string
}

type clusterSSHPreparationAuthorityRead func(context.Context) (clusterSSHPreparationAuthority, error)
type clusterSSHSourceNetworkRead func(context.Context, clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error)

// One reader belongs to one preparation attempt. It freezes the first complete
// Secret receipt, then rejects UID/revision/content drift across every bundle
// verification/export. Production assembly uses the fenced DB/Aegis adapter;
// its controller transport supplies fresh InspectSourceNetwork receipts.
// Current claims and bootstrap receiver still cannot enter a mutating phase.
type clusterSSHPreparationSnapshot struct {
	DesiredWorkerSites []int64                                  `json:"desired_worker_sites"`
	Expected           clusterbootstrap.PreparationExpected     `json:"expected"`
	Settings           map[string]string                        `json:"settings"`
	Observation        string                                   `json:"observation_sha256"`
	Sources            []clusterbootstrap.SourceNetwork         `json:"sources"`
	Storage            clusterSSHStorageSnapshot                `json:"storage"`
	CapacitySHA256     string                                   `json:"capacity_sha256,omitempty"`
	CapacityTargets    []clusterSSHPreparationTargetInstanceCPU `json:"capacity_targets,omitempty"`
	started            time.Time                                // Local acquisition start; never imported from wire or storage.
}

func (clusterSSHPreparationSnapshot) String() string   { return "preparation snapshot [redacted]" }
func (clusterSSHPreparationSnapshot) GoString() string { return "preparation snapshot [redacted]" }

type clusterSSHPreparationSnapshotRead func(context.Context) (clusterSSHPreparationSnapshot, error)

func newClusterSSHPreparationSourceRead(authority clusterSSHPreparationAuthorityRead,
	getJSON func(context.Context, string, any) error, network clusterSSHSourceNetworkRead) clusterSSHPreparationRead {
	read := newClusterSSHPreparationSnapshotRead(authority, getJSON, network)
	return func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		value, err := read(ctx)
		return value.Expected, value.Settings, err
	}
}

func newClusterSSHPreparationSnapshotRead(authority clusterSSHPreparationAuthorityRead,
	getJSON func(context.Context, string, any) error, network clusterSSHSourceNetworkRead) clusterSSHPreparationSnapshotRead {
	gate := make(chan struct{}, 1)
	var retained *clusterbootstrap.PreparationRuntimeSecret
	var retainedSources []clusterbootstrap.SourceNetwork
	return func(parent context.Context) (clusterSSHPreparationSnapshot, error) {
		started := time.Now()
		fail := func(stage string) (clusterSSHPreparationSnapshot, error) {
			// Fixed stage names only. Never log Secret, Job or authority data.
			log.Printf("SSH preparation source snapshot rejected at %s after %s", stage, time.Since(started).Round(time.Millisecond))
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		if authority == nil || getJSON == nil || network == nil || parent.Err() != nil {
			return fail("inputs")
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail("reader gate")
		}
		before, err := authority(ctx)
		if err != nil || before.Baseline.Validate() != nil || validateClusterSSHInspectionCohort(before.Cohort, before.Source) != nil {
			return fail("initial authority")
		}
		observed, err := observeClusterSSHSourceCohort(ctx, getJSON, before.Source)
		if err != nil || !reflect.DeepEqual(observed, before.Source) {
			return fail("initial source membership")
		}
		sources, err := observeClusterSSHSourceNetworks(ctx, before, network)
		if err != nil || (retainedSources != nil && !slices.Equal(retainedSources, sources)) {
			return fail("initial source network")
		}
		expected, err := buildClusterSSHPreparationExpected(before.Cohort, observed, before.Lease, before.Baseline, before.K3sVersion, sources[0].PodCIDR, sources[0].ServiceCIDR, before.payload)
		if err != nil {
			return fail("expected configuration")
		}
		readSecret := func() (clusterbootstrap.PreparationRuntimeSecret, error) {
			// RawMessage remains private local memory and is never error context.
			var raw json.RawMessage
			if getJSON(ctx, clusterSSHRuntimeSecretPath, &raw) != nil {
				return clusterbootstrap.PreparationRuntimeSecret{}, clusterbootstrap.ErrPreparationConfig
			}
			return clusterbootstrap.ParsePreparationRuntimeSecret(raw)
		}
		secret, err := readSecret()
		if err != nil {
			return fail("runtime settings read")
		}
		settings := secret.Settings()
		if _, err := clusterbootstrap.NewPreparationConfiguration(expected, settings); err != nil {
			return fail("configuration validation")
		}
		// Reobserve every source link, including changes while another source
		// or the private Secret was read. Job/Pod receipts cannot be reused.
		currentSources, err := observeClusterSSHSourceNetworks(ctx, before, network)
		if err != nil || !slices.Equal(sources, currentSources) {
			return fail("source network recheck")
		}
		// Finish with fresh public source, private Secret and short DB reads. No
		// DB connection remains held during Kubernetes, TLS or secret decoding.
		reobserved, err := observeClusterSSHSourceCohort(ctx, getJSON, before.Source)
		if err != nil || !reflect.DeepEqual(observed, reobserved) {
			return fail("source membership recheck")
		}
		rechecked, err := readSecret()
		if err != nil || !secret.SameObservation(rechecked) {
			return fail("runtime settings recheck")
		}
		after, err := authority(ctx)
		if err != nil || ctx.Err() != nil {
			return fail("final authority read")
		}
		before.Cohort.ObservedAt, after.Cohort.ObservedAt = 0, 0
		if !reflect.DeepEqual(before, after) || (retained != nil && !secret.SameObservation(*retained)) {
			return fail("final authority comparison")
		}
		if retained == nil {
			retained = &secret
			retainedSources = slices.Clone(sources)
		}
		return clusterSSHPreparationSnapshot{Expected: expected, Settings: settings, Observation: secret.ObservationSHA256(), Sources: sources, started: started}, nil
	}
}

// Keep inputs inside a joined lease scope. The fixed consumer must honor ctx,
// use Inputs for exports and Authority for session heartbeats, and join children.
// Operation completion belongs after this scope succeeds, under its own SQL
// fence; changing the target phase inside consume invalidates this scope.
func withClusterSSHPreparationSource(parent context.Context, scratchParent string, store *postgresOperatorStore, aegis *goAegisService,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials,
	consume func(context.Context, *clusterbootstrap.PreparationInputs, *clusterbootstrap.PreparationArtifacts, string, clusterSSHPreparedImageCapacity, clusterbootstrap.PreparationExpected, clusterSSHPreparationChecks) error) error {
	client, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return err
	}
	return withClusterSSHPreparationSourceFor(parent, scratchParent, store, aegis, lease, baseline, sealed, false, consume, nil, client)
}

// Derive the outer allowance from the existing native session budget and source
// acquisition allowance. No lease, credential or individual phase is extended.
func clusterSSHPreparationSourceBudget(joining bool) time.Duration {
	return clusterSSHStartupDispatchBudget(true, true, joining) + 20*time.Minute
}

// Separate explicit joining scope permits approved drained registrations while
// retaining original source/configuration/storage proof. Startup and image
// verification remain native journal phases with independently proven prefixes.
func withClusterSSHPreparationSourceFor(parent context.Context, scratchParent string, store *postgresOperatorStore, aegis *goAegisService,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials, joining bool,
	consume func(context.Context, *clusterbootstrap.PreparationInputs, *clusterbootstrap.PreparationArtifacts, string, clusterSSHPreparedImageCapacity, clusterbootstrap.PreparationExpected, clusterSSHPreparationChecks) error, allocate func(context.Context) (string, error), client *clusterSSHSourceBrokerClient) error {
	if store == nil || store.db == nil || aegis == nil || consume == nil || !validClusterSSHPreparationLease(lease) {
		return clusterbootstrap.ErrPreparationConfig
	}
	if client == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	authority := newClusterSSHPreparationAuthorityRead(store, aegis, lease, baseline, sealed)
	leaseCheck := newClusterSSHPreparationLeaseCheck(authority, func(ctx context.Context) error { return store.renewClusterSSHPreparationTarget(ctx, lease, sealed) })
	// Retain the existing twenty-minute source-acquisition allowance around
	// the exact bounded session budget, including identity activation.
	maximum := clusterSSHPreparationSourceBudget(joining)
	return runClusterSSHPreparationScopeFor(parent, 5*time.Second, maximum, leaseCheck, func(ctx context.Context) (result error) {
		var phase, last string
		var finalSnapshotPassed bool
		var began time.Time
		emit, _ := ctx.Value(clusterSSHPreparationObserverKey{}).(func(string))
		mark := func(next string) {
			if emit == nil {
				return
			}
			if phase != "" {
				last = phase
				emit(fmt.Sprintf("Source preparation phase=%s status=completed elapsed_ms=%d", phase, time.Since(began).Milliseconds()))
			}
			phase, began = next, time.Now()
			emit(fmt.Sprintf("Source preparation phase=%s status=entered last_completed=%s", phase, last))
		}
		defer func() {
			if emit == nil {
				return
			}
			status := "completed"
			if result != nil && !finalSnapshotPassed {
				status = "failed"
			}
			emit(fmt.Sprintf("Source preparation phase=%s status=%s elapsed_ms=%d last_completed=%s", phase, status, time.Since(began).Milliseconds(), last))
		}()
		cleanup := func(name string, close func() error, rejection error) {
			began := time.Now()
			if emit != nil {
				emit(fmt.Sprintf("Source cleanup resource=%s status=entered", name))
			}
			err := close()
			if err != nil {
				result = rejection
			}
			if emit != nil {
				status := "completed"
				if err != nil {
					status = "failed"
				}
				emit(fmt.Sprintf("Source cleanup resource=%s status=%s elapsed_ms=%d", name, status, time.Since(began).Milliseconds()))
			}
		}
		mark("source_snapshot")
		snapshotRead := client.snapshotReadFor(authority, lease, baseline, sealed, joining)
		initial, err := snapshotRead(ctx)
		read := func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
			value, err := snapshotRead(ctx)
			return value.Expected, value.Settings, err
		}
		expected, settings := initial.Expected, initial.Settings
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		// This is the first successful read. Nothing in the joining executor
		// may allocate private scratch or start native work before this commit.
		if joining {
			if allocate == nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			mark("allocation")
			scratchParent, err = allocate(ctx)
			if err != nil {
				return err
			}
		}
		mark("configuration")
		config, err := clusterbootstrap.NewPreparationConfiguration(expected, settings)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "source configuration")
			return clusterbootstrap.ErrPreparationConfig
		}
		inputCheck := newClusterSSHPreparationInputCheck(config, read)
		checks := clusterSSHPreparationChecks{
			Inputs:    clusterSSHPreparationBoundary(ctx, leaseCheck, inputCheck),
			Authority: clusterSSHPreparationBoundary(ctx, leaseCheck, func(context.Context) error { return nil }),
		}
		// The first full snapshot bound the source, settings and storage. Hashing
		// private release files cannot change a host. Keep claim/credential
		// checks live here and re-read the complete snapshot before handoff.
		checkBytes := checks.privateArtifactCheck(joining)
		mark("bootstrap_binding")
		inputs, err := prepareClusterSSHTargetInputs(ctx, expected, config, scratchParent, checkBytes, prepareClusterSSHBootstrap)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "bootstrap binding")
			return clusterbootstrap.ErrPreparationConfig
		}
		defer cleanup("inputs", inputs.Close, clusterbootstrap.ErrPreparationConfig)
		checks.Storage = newClusterSSHPrejoinWorkerStorageRead(ctx, initial.Storage, checks.Authority)
		mark("host_package_inventory")
		packages, err := resolveClusterSSHHostPackageInventory(ctx, expected.Source)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "host package inventory")
			return err
		}
		mark("host_package_staging")
		packageBytes, err := packages.stage(ctx, scratchParent, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "host package staging")
			return err
		}
		mark("host_package_binding")
		if err := inputs.BindHostPackages(ctx, expected, packageBytes, checkBytes); err != nil {
			_ = packageBytes.Close()
			reportClusterSSHPrejoinCheck(ctx, "host package binding")
			return err
		}
		mark("host_package_demand")
		packageDemand, err := packages.demands(ctx, packageBytes, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "host package demand")
			return err
		}
		mark("join_broker")
		joinClient, err := newClusterSSHJoinBrokerClient()
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "join broker configuration")
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		checks.K3sJoin = newClusterSSHPreparationJoinRead(ctx, joinClient, expected, initial.Sources, lease, baseline, sealed, authority, checks.Authority)
		mark("application_inventory")
		inventory, err := resolveClusterSSHImageInventory(ctx, expected.Source)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "application inventory")
			return err
		}
		mark("application_archives")
		images, err := inventory.stage(ctx, scratchParent, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "application image staging")
			return err
		}
		defer cleanup("images", images.Close, clusterbootstrap.ErrImageArchive)
		mark("external_inventory")
		externalInventory, err := resolveClusterSSHExternalInventory(ctx, expected.Source)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "external inventory")
			return err
		}
		mark("external_archives")
		externalImages, err := externalInventory.stage(ctx, scratchParent, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "external image staging")
			return err
		}
		defer cleanup("externalImages", externalImages.Close, clusterbootstrap.ErrImageArchive)
		mark("postgres_acquisition")
		postgresImage, err := clusterbootstrap.AcquirePostgresImage(ctx, scratchParent, initial.Storage.Requirements.PostgresImage.Resolved, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "PostgreSQL image acquisition")
			return err
		}
		defer cleanup("postgresImage", postgresImage.Close, clusterbootstrap.ErrImageArchive)
		// Static publications may have changed during dynamic acquisition.
		mark("publication_recheck")
		if inventory.refresh(ctx) != nil || externalInventory.refresh(ctx) != nil || checkBytes(ctx) != nil {
			reportClusterSSHPrejoinCheck(ctx, "publication recheck")
			return clusterbootstrap.ErrImageArchive
		}
		// Compose target demands separately from physically reserved PostgreSQL
		// acquisition scratch. Complete K3s assets remain owned through consumption.
		mark("k3s_inventory")
		k3s, err := resolveClusterSSHK3sInventory(ctx, expected.Source, expected.K3sVersion)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "K3s inventory")
			return err
		}
		mark("k3s_archives")
		k3sInputs, err := k3s.stage(ctx, scratchParent, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "K3s staging")
			return err
		}
		defer cleanup("k3sInputs", k3sInputs.Close, clusterbootstrap.ErrImageArchive)
		mark("artifact_binding")
		artifacts, err := clusterbootstrap.NewPreparationArtifacts(ctx, expected.Source, expected.K3sVersion, initial.Storage.Requirements.PostgresImage.Resolved, images, k3sInputs, externalImages, postgresImage, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "artifact set binding")
			return err
		}
		defer cleanup("artifacts", artifacts.Close, clusterbootstrap.ErrImageArchive)
		mark("filesystem_demands")
		appDemand, err := inventory.demands()
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "application demand")
			return err
		}
		externalDemand, err := externalInventory.demands()
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "external demand")
			return err
		}
		k3sDemand, err := k3s.demands()
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "K3s demand")
			return err
		}
		joinDemand, err := clusterSSHK3sConfigurationDemands(expected)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "K3s configuration demand")
			return err
		}
		packagePlan, _, err := clusterbootstrap.HostPackagePins()
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "package pins")
			return err
		}
		hostFiles, err := clusterSSHPreparationHostFileDemands(expected, packagePlan.Suite)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "host file demand")
			return err
		}
		baseDemand, err := mergeClusterSSHFilesystemDemands(appDemand, externalDemand, k3sDemand, joinDemand, hostFiles,
			packageDemand)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "combined filesystem demand")
			return err
		}
		mark("bootstrap_demand")
		bundleDemand, err := inputs.StorageDemand(ctx, expected, checkBytes)
		if err != nil {
			reportClusterSSHPrejoinCheck(ctx, "bootstrap demand")
			return err
		}
		mark("postgres_capacity")
		if err := withClusterSSHPostgresCapacity(ctx, initial.Storage.Requirements.PostgresImage, postgresImage, checkBytes, baseDemand,
			func(ctx context.Context, capacity clusterSSHPreparedImageCapacity) error {
				mark("worker_capacity")
				capacity, err = clusterSSHPreparationWorkerCapacity(bundleDemand, inventory, externalInventory, k3s, packages, capacity)
				if err != nil {
					reportClusterSSHPrejoinCheck(ctx, "worker capacity")
					return err
				}
				if err := packages.refresh(ctx); err != nil {
					reportClusterSSHPrejoinCheck(ctx, "host package publication recheck")
					return err
				}
				// The joining consumer rechecks full inputs on both sides of its
				// target storage check before it can enter native dispatch.
				mark("target_handoff")
				if err := consume(ctx, inputs, artifacts, initial.Storage.Requirements.PostgresImage.Resolved, capacity, expected, checks); err != nil {
					return err
				}
				mark("post_dispatch_recheck")
				fresh, err := inputs.StorageDemand(ctx, expected, checkBytes)
				if err != nil || fresh != bundleDemand {
					return clusterbootstrap.ErrImageArchive
				}
				freshPackages, err := packages.demands(ctx, packageBytes, checkBytes)
				if err != nil || !reflect.DeepEqual(freshPackages, packageDemand) {
					return clusterbootstrap.ErrImageArchive
				}
				return nil
			}); err != nil {
			reportClusterSSHPrejoinCheck(ctx, "PostgreSQL capacity or source consumer")
			return err
		}
		mark("publication_cleanup")
		if err := k3s.refresh(ctx); err != nil {
			return err
		}
		if err := externalInventory.refresh(ctx); err != nil {
			return err
		}
		if err := inventory.refresh(ctx); err != nil {
			return err
		}
		if err := packages.refresh(ctx); err != nil {
			return err
		}
		// A short session heartbeat must not spawn source Jobs. Revalidate full
		// source/configuration after consumption before acknowledging success.
		mark("final_source_snapshot")
		err = checks.Inputs(ctx)
		finalSnapshotPassed = err == nil
		return err
	})
}

// Separate bounded private GET path: ordinary Kubernetes errors can retain
// response bodies and the generic client can follow redirects. Neither is
// appropriate for a Secret. Keep the existing TLS/token authority, permit only
// these fixed reads and return static errors without response bodies/URLs.
func (c *kubernetesAPIClient) getClusterSSHPreparationJSON(ctx context.Context, path string, out any) error {
	if path != clusterSSHRuntimeSecretPath && path != "/api/v1/namespaces/kube-system" && path != "/api/v1/nodes" {
		return clusterbootstrap.ErrPreparationConfig
	}
	return c.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
}

func (c *kubernetesAPIClient) doClusterSSHSourceJSON(ctx context.Context, method, path string, body, out any) error {
	const jobs = "/apis/batch/v1/namespaces/borealis/jobs"
	valid := method == http.MethodPost && path == jobs
	if method == http.MethodGet {
		if strings.HasPrefix(path, jobs+"/borealis-source-") {
			valid = clusterSSHSourceJobPathRE.MatchString(path)
		} else if u, err := url.Parse(path); err == nil && u.Path == "/api/v1/namespaces/borealis/pods" && len(u.Query()) == 1 {
			values := u.Query()["labelSelector"]
			if len(values) == 1 {
				uid := strings.TrimPrefix(values[0], "batch.kubernetes.io/controller-uid=")
				valid = values[0] == "batch.kubernetes.io/controller-uid="+uid && clusterUUIDRE.MatchString(uid)
			}
		}
	}
	if !valid {
		return clusterbootstrap.ErrPreparationConfig
	}
	return c.clusterSSHPrivateJSON(ctx, method, path, body, out)
}

func (c *kubernetesAPIClient) clusterSSHPrivateJSON(ctx context.Context, method, path string, body, out any) error {
	if c == nil || c.httpClient == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return clusterbootstrap.ErrPreparationConfig
	}
	base.Path, base.RawQuery = u.Path, u.RawQuery
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), reader)
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	defer response.Body.Close()
	if method == http.MethodGet && response.StatusCode == http.StatusNotFound && clusterSSHPreparationLonghornNodePath(path) {
		if ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return clusterSSHPreparationLonghornNotFound(path, response.Body)
	}
	if method == http.MethodGet && response.StatusCode == http.StatusNotFound && clusterSSHCNPGOptionalConfigPath(path) {
		if ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return clusterSSHCNPGNotFound(path, response.Body)
	}
	if (method == http.MethodGet && response.StatusCode != http.StatusOK) || (method == http.MethodPost && response.StatusCode != http.StatusCreated) {
		return clusterbootstrap.ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || ctx.Err() != nil || len(raw) == 0 || len(raw) > 2<<20 || json.Unmarshal(raw, out) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}
