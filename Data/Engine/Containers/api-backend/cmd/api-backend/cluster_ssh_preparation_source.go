package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
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
	Cohort     clusterSSHInspectionCohort
	Source     clusterSSHSourceCohort
	Lease      clusterSSHTargetLease
	Baseline   clusterbootstrap.Expected
	K3sVersion string
}

type clusterSSHPreparationAuthorityRead func(context.Context) (clusterSSHPreparationAuthority, error)
type clusterSSHSourceNetworkRead func(context.Context, clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error)

// One reader belongs to one preparation attempt. It freezes the first complete
// Secret receipt, then rejects UID/revision/content drift across every bundle
// verification/export. Production assembly uses the fenced DB/Aegis adapter;
// its controller transport supplies fresh InspectSourceNetwork receipts.
// Current claims and bootstrap receiver still cannot enter a mutating phase.
type clusterSSHPreparationSnapshot struct {
	Expected    clusterbootstrap.PreparationExpected `json:"expected"`
	Settings    map[string]string                    `json:"settings"`
	Observation string                               `json:"observation_sha256"`
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
	return func(parent context.Context) (clusterSSHPreparationSnapshot, error) {
		fail := func() (clusterSSHPreparationSnapshot, error) {
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		if authority == nil || getJSON == nil || network == nil || parent.Err() != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail()
		}
		before, err := authority(ctx)
		if err != nil || before.Baseline.Validate() != nil || validateClusterSSHInspectionCohort(before.Cohort, before.Source) != nil {
			return fail()
		}
		observed, err := observeClusterSSHSourceCohort(ctx, getJSON, before.Source)
		if err != nil || !reflect.DeepEqual(observed, before.Source) {
			return fail()
		}
		pods, services := "", ""
		for _, member := range observed.Members {
			value, err := network(ctx, member)
			if err != nil || value.Validate() != nil || value.NodeUID != member.NodeUID || value.Hostname != member.Name ||
				value.MachineID != member.MachineID || value.BootID != member.BootID || value.K3sVersion != before.K3sVersion {
				return fail()
			}
			if pods != "" && (pods != value.PodCIDR || services != value.ServiceCIDR) {
				return fail()
			}
			pods, services = value.PodCIDR, value.ServiceCIDR
		}
		expected, err := buildClusterSSHPreparationExpected(before.Cohort, observed, before.Lease, before.Baseline, before.K3sVersion, pods, services)
		if err != nil {
			return fail()
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
			return fail()
		}
		settings := secret.Settings()
		if _, err := clusterbootstrap.NewPreparationConfiguration(expected, settings); err != nil {
			return fail()
		}
		// Finish with fresh public source, private Secret and short DB reads. No
		// DB connection remains held during Kubernetes, TLS or secret decoding.
		reobserved, err := observeClusterSSHSourceCohort(ctx, getJSON, before.Source)
		if err != nil || !reflect.DeepEqual(observed, reobserved) {
			return fail()
		}
		rechecked, err := readSecret()
		if err != nil || !secret.SameObservation(rechecked) {
			return fail()
		}
		after, err := authority(ctx)
		if err != nil || ctx.Err() != nil {
			return fail()
		}
		before.Cohort.ObservedAt, after.Cohort.ObservedAt = 0, 0
		if !reflect.DeepEqual(before, after) || (retained != nil && !secret.SameObservation(*retained)) {
			return fail()
		}
		if retained == nil {
			retained = &secret
		}
		return clusterSSHPreparationSnapshot{Expected: expected, Settings: settings, Observation: secret.ObservationSHA256()}, nil
	}
}

func prepareClusterSSHTargetFromSource(ctx context.Context, scratchParent string, store *postgresOperatorStore, aegis *goAegisService,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) (*clusterbootstrap.PreparationInputs, error) {
	client, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	authority := newClusterSSHPreparationAuthorityRead(store, aegis, lease, baseline, sealed)
	read := client.preparationRead(authority, lease, baseline, sealed)
	expected, settings, err := read(ctx)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return prepareClusterSSHTargetInputs(ctx, expected, settings, scratchParent, read)
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
	if (method == http.MethodGet && response.StatusCode != http.StatusOK) || (method == http.MethodPost && response.StatusCode != http.StatusCreated) {
		return clusterbootstrap.ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || ctx.Err() != nil || len(raw) == 0 || len(raw) > 2<<20 || json.Unmarshal(raw, out) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}
