package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Current placement is independent of worker_admission's historical execution
// ledger. No field here settles old work or authorizes replacement capacity.
type clusterSSHCurrentWorkerRoute struct {
	Worker, PodName, PodUID, Node, Host      string
	SiteID, Generation, OpsPort, DesktopPort int64
	ProfileRank                              int64
	SHA256                                   string
	observation                              clusterSSHCurrentWorkerRouteRow
}

type clusterSSHCurrentWorkerRouteRow struct {
	Worker, WorkerPod, WorkerStatus, RouteWorker, RoutePod, RouteStatus, Scheme, Host, Metadata sql.NullString
	WorkerSite, RouteSite, Generation, Port                                                     sql.NullInt64
}

// Retain both sides before validation: worker and route writes are separate
// transactions. Inner joins or filtering both sides to active rows can hide an
// orphan, terminal counterpart or mixed old/new placement. Read metadata TEXT,
// never jsonb extraction, which would erase duplicate-key evidence. Historical
// terminal-only pairs are not current custody; stale stopped_at/retired_at fields
// are not status because the producer does not clear them on reactivation.
func readClusterSSHCurrentWorkerRoutes(ctx context.Context, db *sql.DB) ([]clusterSSHCurrentWorkerRoute, error) {
	fail := func() ([]clusterSSHCurrentWorkerRoute, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if db == nil || ctx.Err() != nil {
		return fail()
	}
	rows, err := db.QueryContext(ctx, `
SELECT left(w.worker_guid,254), left(w.container_name,254), w.site_id, left(w.status,254),
 left(r.worker_guid,254), left(r.container_name,254), r.site_id, left(r.status,254),
 r.generation, left(r.upstream_scheme,254), left(r.upstream_host,254), r.upstream_port,
 CASE WHEN octet_length(r.metadata_json) <= $5 THEN r.metadata_json ELSE NULL END
FROM engine.job_scheduler_workers w FULL OUTER JOIN engine.job_scheduler_worker_routes r ON r.worker_guid=w.worker_guid
WHERE w.status IN ($1,$2,$3) OR r.status=$4
ORDER BY COALESCE(w.worker_guid,r.worker_guid) LIMIT $6`,
		schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, schedulerRouteStatusActive,
		operatorSiteWorkerInventoryMaxBytes, clusterSSHSiteWorkerLimit+1)
	if err != nil {
		return fail()
	}
	retained := make([]clusterSSHCurrentWorkerRouteRow, 0)
	total := 0
	for rows.Next() {
		var row clusterSSHCurrentWorkerRouteRow
		if rows.Scan(&row.Worker, &row.WorkerPod, &row.WorkerSite, &row.WorkerStatus, &row.RouteWorker, &row.RoutePod, &row.RouteSite,
			&row.RouteStatus, &row.Generation, &row.Scheme, &row.Host, &row.Port, &row.Metadata) != nil {
			_ = rows.Close()
			return fail()
		}
		total += len(row.Metadata.String)
		if len(retained) >= clusterSSHSiteWorkerLimit || total > operatorSiteWorkerInventoryMaxBytes {
			_ = rows.Close()
			return fail()
		}
		retained = append(retained, row)
	}
	readErr, closeErr := rows.Err(), rows.Close() // Release before parsing, hashing, joining or network IO.
	if readErr != nil || closeErr != nil || ctx.Err() != nil {
		return fail()
	}
	result := make([]clusterSSHCurrentWorkerRoute, 0, len(retained))
	sites, uids := map[int64]bool{}, map[string]bool{}
	previous := ""
	for _, row := range retained {
		value, ok := clusterSSHCurrentWorkerRouteFromRow(row)
		if !ok || value.Worker <= previous || sites[value.SiteID] || uids[value.PodUID] {
			return fail()
		}
		previous, sites[value.SiteID], uids[value.PodUID] = value.Worker, true, true
		result = append(result, value)
	}
	if ctx.Err() != nil {
		return fail()
	}
	return result, nil
}

func clusterSSHCurrentWorkerRouteFromRow(row clusterSSHCurrentWorkerRouteRow) (clusterSSHCurrentWorkerRoute, bool) {
	fail := func() (clusterSSHCurrentWorkerRoute, bool) { return clusterSSHCurrentWorkerRoute{}, false }
	for _, value := range []sql.NullString{row.Worker, row.WorkerPod, row.WorkerStatus, row.RouteWorker, row.RoutePod, row.RouteStatus, row.Scheme, row.Host, row.Metadata} {
		if !value.Valid || !utf8.ValidString(value.String) {
			return fail()
		}
	}
	for _, value := range []sql.NullInt64{row.WorkerSite, row.RouteSite, row.Generation, row.Port} {
		if !value.Valid {
			return fail()
		}
	}
	if row.WorkerSite.Int64 < 1 || uint64(row.WorkerSite.Int64) > clusterSSHCapacityLimit || row.RouteSite.Int64 != row.WorkerSite.Int64 ||
		row.Worker.String != schedulerK3sSiteWorkerGUID(row.WorkerSite.Int64) || row.RouteWorker.String != row.Worker.String ||
		!borealisOperatorKubernetesObjectNameAllowed(row.WorkerPod.String) || row.RoutePod.String != row.WorkerPod.String ||
		!slices.Contains([]string{schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle}, row.WorkerStatus.String) ||
		row.RouteStatus.String != schedulerRouteStatusActive || row.Scheme.String != "http" || row.Generation.Int64 < 1 || uint64(row.Generation.Int64) > clusterSSHCapacityLimit ||
		row.Port.Int64 < 1 || row.Port.Int64 > 65535 || len(row.Metadata.String) == 0 || len(row.Metadata.String) > operatorSiteWorkerInventoryMaxBytes {
		return fail()
	}
	ip, err := netip.ParseAddr(row.Host.String)
	if err != nil || ip.String() != row.Host.String || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return fail()
	}
	d := json.NewDecoder(strings.NewReader(row.Metadata.String))
	d.UseNumber()
	value, err := clusterSSHPrivateJSONValue(d, 0)
	metadata, ok := value.(map[string]any)
	if err != nil || !ok || metadata == nil {
		return fail()
	}
	if _, err := d.Token(); err != io.EOF {
		return fail()
	}
	placement := clusterSSHStorageMap(metadata, schedulerWorkerPlacementKey)
	version, versionOK := clusterSSHStorageInteger(placement["version"])
	site, siteOK := clusterSSHStorageInteger(placement["site_id"])
	generation, generationOK := clusterSSHStorageInteger(placement["route_generation"])
	rank, rankOK := clusterSSHStorageInteger(placement["profile_rank"])
	uid, node := clusterSSHStorageText(placement, "pod_uid"), clusterSSHStorageText(placement, "node_name")
	if len(placement) != 8 || !versionOK || version != 1 || !siteOK || site != row.WorkerSite.Int64 || !generationOK || generation != row.Generation.Int64 ||
		!rankOK || rank > 3 || !vipScopeUUID(uid) || !schedulerPlacementName(node) || placement["worker_guid"] != row.Worker.String || placement["pod_name"] != row.WorkerPod.String ||
		metadata["lifecycle_owner"] != "borealis-operator" || metadata["route_kind"] != "site_worker" || metadata["worker_guid"] != row.Worker.String || metadata["upstream_host"] != row.Host.String {
		return fail()
	}
	ops, desktop := clusterSSHStorageMap(metadata, "remote_ops_socket"), clusterSSHStorageMap(metadata, "remote_desktop_guacamole")
	opsPort, opsOK := clusterSSHStorageInteger(ops["port"])
	desktopPort, desktopOK := clusterSSHStorageInteger(desktop["port"])
	if !opsOK || opsPort != row.Port.Int64 || !desktopOK || desktopPort < 1 || desktopPort > 65535 || opsPort == desktopPort ||
		ops["host"] != row.Host.String || ops["worker_guid"] != row.Worker.String || ops["path"] != "/socket.io/" ||
		desktop["host"] != row.Host.String || desktop["worker_guid"] != row.Worker.String || desktop["scheme"] != "http" ||
		desktop["path"] != "/remote-desktop/vnc/guacamole" || desktop["path_prefix"] != "/remote-desktop/vnc" {
		return fail()
	}
	// Preserve the complete raw SQL projection, including historical ledger bytes,
	// as drift evidence only. Never inspect ledger occupancy or rewrite its charge.
	raw, err := json.Marshal(row)
	if err != nil {
		return fail()
	}
	digest := sha256.Sum256(raw)
	return clusterSSHCurrentWorkerRoute{Worker: row.Worker.String, PodName: row.WorkerPod.String, PodUID: uid, Node: node, Host: row.Host.String,
		SiteID: site, Generation: generation, OpsPort: opsPort, DesktopPort: desktopPort, ProfileRank: rank, SHA256: hex.EncodeToString(digest[:]), observation: row}, true
}

const clusterSSHWorkerSecretName = "borealis-job-scheduler-runtime-env"
const clusterSSHWorkerSecretPath = "/api/v1/namespaces/borealis/secrets/" + clusterSSHWorkerSecretName

type clusterSSHWorkerPolicy struct {
	Profile      string                           `json:"profile"`
	OnlineWindow int64                            `json:"online_window_seconds"`
	Limits       borealisOperatorSiteWorkerLimits `json:"limits"`
}

func (p clusterSSHWorkerPolicy) resources() (clusterSSHApplicationResource, bool) {
	profile, ok := operatorSiteWorkerResources(p.Profile, p.Limits)
	if !ok || !slices.Contains([]string{"small", "standard", "large"}, p.Profile) || p.OnlineWindow < 60 || p.OnlineWindow > 3600 {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources("site-worker", map[string]any{
		"requests": map[string]any{"cpu": profile.RequestCPU, "memory": profile.RequestMemory},
		"limits":   map[string]any{"cpu": profile.LimitCPU, "memory": profile.LimitMemory},
	})
}

// A generic Deployment receipt declares the new producer's settings but does
// not prove every running operator/node clone consumes them. Until independent
// producer-version proof exists, prospective recreation retains the componentwise
// larger known legacy/new envelope. Actual Pods and their rotation clones stay
// charged from their own observed resources, without rewriting either producer.
func (p clusterSSHWorkerPolicy) recreationResources() (clusterSSHApplicationResource, bool) {
	declared, valid := p.resources()
	legacy, ok := borealisOperatorSiteWorkerResourceProfile(p.Profile)
	if !valid || !ok {
		return clusterSSHApplicationResource{}, false
	}
	previous, ok := clusterSSHWorkloadResources("site-worker", map[string]any{
		"requests": map[string]any{"cpu": legacy.RequestCPU, "memory": legacy.RequestMemory},
		"limits":   map[string]any{"cpu": legacy.LimitCPU, "memory": legacy.LimitMemory},
	})
	if !ok {
		return clusterSSHApplicationResource{}, false
	}
	declared.CPURequestMilli = max(declared.CPURequestMilli, previous.CPURequestMilli)
	declared.CPULimitMilli = max(declared.CPULimitMilli, previous.CPULimitMilli)
	declared.MemoryRequestBytes = max(declared.MemoryRequestBytes, previous.MemoryRequestBytes)
	declared.MemoryLimitBytes = max(declared.MemoryLimitBytes, previous.MemoryLimitBytes)
	return declared, true
}

// Resolve only the scheduler's two demand inputs from its declared environment.
// Never use this controller's environment or infer them from a surviving worker.
// Defaults mirror scheduler code; this is not running-producer/version proof.
func observeClusterSSHWorkerPolicy(read func(string) (map[string]any, error)) (clusterSSHWorkerPolicy, error) {
	fail := func() (clusterSSHWorkerPolicy, error) {
		return clusterSSHWorkerPolicy{}, clusterbootstrap.ErrPreparationConfig
	}
	if read == nil {
		return fail()
	}
	deployment, err := read(clusterSSHApplicationDeploymentPrefix + "job-scheduler")
	id, ok := clusterSSHStorageMetadata(deployment, "apps/v1", "Deployment", "borealis")
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(deployment, "spec"), "template"), "spec")
	containers, valid := spec["containers"].([]any)
	if err != nil || !ok || id.Name != "job-scheduler" || !valid || len(containers) != 1 {
		return fail()
	}
	c, ok := containers[0].(map[string]any)
	refs, refsOK := c["envFrom"].([]any)
	if !ok || c["name"] != "job-scheduler" || !refsOK || len(refs) != 1 || !clusterSSHStorageEmptyList(c["command"]) || !clusterSSHStorageEmptyList(c["args"]) {
		return fail()
	}
	ref, ok := refs[0].(map[string]any)
	secretRef := clusterSSHStorageMap(ref, "secretRef")
	if !ok || len(ref) != 1 || len(secretRef) < 1 || len(secretRef) > 2 || secretRef["name"] != clusterSSHWorkerSecretName {
		return fail()
	}
	if optional, exists := secretRef["optional"]; exists && optional != false {
		return fail()
	}
	for key := range secretRef {
		if key != "name" && key != "optional" {
			return fail()
		}
	}
	secret, err := read(clusterSSHWorkerSecretPath)
	id, ok = clusterSSHStorageMetadata(secret, "v1", "Secret", "borealis")
	labels := clusterSSHStorageMap(clusterSSHStorageMap(secret, "metadata"), "labels")
	data, dataOK := secret["data"].(map[string]any)
	if err != nil || !ok || id.Name != clusterSSHWorkerSecretName || !dataOK || secret["type"] != "Opaque" ||
		labels["app.kubernetes.io/name"] != "job-scheduler" || labels["app.kubernetes.io/part-of"] != "borealis" || labels["app.kubernetes.io/managed-by"] != "Engine.sh" {
		return fail()
	}
	values := map[string]string{"BOREALIS_SITE_WORKER_RESOURCE_PROFILE": "", "BOREALIS_AGENT_ONLINE_WINDOW_SECONDS": ""}
	for key := range values {
		if raw, present := data[key]; present {
			encoded, ok := raw.(string)
			decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
			if !ok || len(encoded) > 128 || err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded {
				return fail()
			}
			values[key] = string(decoded)
		}
	}
	if raw, present := c["env"]; present {
		env, ok := raw.([]any)
		if !ok || len(env) > 256 {
			return fail()
		}
		seen := map[string]bool{}
		for _, raw := range env {
			e, ok := raw.(map[string]any)
			name := clusterSSHStorageText(e, "name")
			if !ok || name == "" || seen[name] {
				return fail()
			}
			seen[name] = true
			if _, watched := values[name]; watched {
				v, ok := e["value"].(string)
				if !ok || len(e) != 2 || len(v) > 96 {
					return fail()
				}
				values[name] = v
			}
		}
	}
	p := clusterSSHWorkerPolicy{Profile: firstText(strings.ToLower(strings.TrimSpace(values["BOREALIS_SITE_WORKER_RESOURCE_PROFILE"])), "standard"), OnlineWindow: 300}
	if raw := strings.TrimSpace(values["BOREALIS_AGENT_ONLINE_WINDOW_SECONDS"]); raw != "" {
		window, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(window, 10) != raw {
			return fail()
		}
		// Match scheduler envInt's clamping without using the local environment.
		p.OnlineWindow = max(int64(60), min(int64(3600), window))
	}
	p.Limits, err = observeClusterSSHWorkerLimits(read)
	if err != nil {
		return fail()
	}
	if _, ok := p.resources(); !ok {
		return fail()
	}
	return p, nil
}

// Limits belong to the original operator Deployment declaration, not the
// scheduler's local environment or a surviving legacy Pod. This is not running-
// producer/version proof. The caller receipts the whole Deployment and repeats
// it under existing source authority; recreationResources retains legacy bounds.
func observeClusterSSHWorkerLimits(read func(string) (map[string]any, error)) (borealisOperatorSiteWorkerLimits, error) {
	fail := func() (borealisOperatorSiteWorkerLimits, error) {
		return borealisOperatorSiteWorkerLimits{}, clusterbootstrap.ErrPreparationConfig
	}
	deployment, err := read(clusterSSHApplicationDeploymentPrefix + "borealis-operator")
	id, valid := clusterSSHStorageMetadata(deployment, "apps/v1", "Deployment", "borealis")
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(deployment, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if err != nil || !valid || id.Name != "borealis-operator" || !ok || len(containers) != 1 {
		return fail()
	}
	c, ok := containers[0].(map[string]any)
	env, envOK := c["env"].([]any)
	if !ok || c["name"] != "borealis-operator" || !clusterSSHStorageEmptyList(c["command"]) ||
		!clusterSSHStorageStrings(c["args"], "borealis-operator") || !clusterSSHStorageEmptyList(c["envFrom"]) || !envOK || len(env) > 256 {
		return fail()
	}
	values := map[string]string{}
	seen := map[string]bool{}
	for _, raw := range env {
		e, ok := raw.(map[string]any)
		name := clusterSSHStorageText(e, "name")
		if !ok || name == "" || seen[name] {
			return fail()
		}
		seen[name] = true
		if slices.Contains([]string{"BOREALIS_SITE_WORKER_CPU_LIMIT", "BOREALIS_SITE_WORKER_MEMORY_LIMIT", "BOREALIS_SITE_WORKER_PIDS_LIMIT"}, name) {
			value, ok := e["value"].(string)
			if !ok || len(e) != 2 || len(value) > 32 {
				return fail()
			}
			values[name] = value
		}
	}
	return borealisOperatorSiteWorkerLimits{
		CPU: values["BOREALIS_SITE_WORKER_CPU_LIMIT"], Memory: values["BOREALIS_SITE_WORKER_MEMORY_LIMIT"], PIDs: values["BOREALIS_SITE_WORKER_PIDS_LIMIT"],
	}, nil
}

type clusterSSHWorkerCensusRead func(context.Context, int64) ([]int64, error)

// One bounded statement sees queued/running recovery work, active worker rows
// and the scheduler's online-device demand. Time is only a query cutoff; never
// include heartbeat/clock churn in the frozen projection. No payload is read.
func readClusterSSHWorkerCensus(ctx context.Context, db *sql.DB, window int64) ([]int64, error) {
	if db == nil || window < 60 || window > 3600 || ctx.Err() != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	rows, err := db.QueryContext(ctx, `
SELECT site_id FROM (
 SELECT site_id FROM engine.job_scheduler_work_items WHERE status IN ($1,$2)
 UNION
 SELECT site_id FROM engine.job_scheduler_workers WHERE status IN ($3,$4,$5)
 UNION
 SELECT ds.site_id FROM engine.device_sites ds
 JOIN engine.devices d ON lower(d.hostname)=lower(ds.device_hostname)
 WHERE d.last_seen IS NOT NULL AND d.last_seen >= $6
 AND LOWER(COALESCE(NULLIF(d.status,''),'active')) IN ('active','online')
) demand WHERE site_id IS NOT NULL AND site_id > 0 ORDER BY site_id LIMIT $7`,
		workStatusQueued, workStatusRunning, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle,
		time.Now().Unix()-window, clusterSSHSiteWorkerLimit+1)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	sites := make([]int64, 0)
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			_ = rows.Close()
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		sites = append(sites, id)
	}
	readErr, closeErr := rows.Err(), rows.Close() // Return pooled connection before validation/shaping or IO.
	if readErr != nil || closeErr != nil || ctx.Err() != nil || !clusterSSHWorkerCensusValid(sites) {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return sites, nil
}

func clusterSSHWorkerCensusValid(sites []int64) bool {
	if sites == nil || len(sites) > clusterSSHSiteWorkerLimit {
		return false
	}
	previous := int64(0)
	for _, id := range sites {
		if id <= previous || uint64(id) > clusterSSHCapacityLimit {
			return false
		}
		previous = id
	}
	return true
}

// Existing observed demand stays charged, including extra/stale Pods. For each
// required site add only the gap up to one worker using the selected recreation
// profile. Engine.sh's Agent rotation clones every worker's init/main envelope
// and keeps the complete candidate cohort until all candidates are ready. Add
// that cohort once, separately from applications/infrastructure. Missing-site
// reserves also cover their later rotation; this bounds current demand only,
// never future worker growth. Native source observation still rejects an
// in-progress rotation's candidate names/duplicate worker identities.
// Application candidate/active-surge demand is a separate declared subtotal;
// never multiply infrastructure or worker envelopes by application copy counts.
func clusterSSHDesiredWorkerWorkloadBudget(snapshot clusterSSHPreparationSnapshot) (cpu, memory uint64, ok bool) {
	r := snapshot.Storage.Requirements
	cpu, memory, ok = clusterSSHNodeWorkloadBudget(r)
	profile, valid := r.WorkerPolicy.recreationResources()
	if !ok || !valid || !clusterSSHWorkerCensusValid(snapshot.DesiredWorkerSites) {
		return 0, 0, false
	}
	applicationCPU, applicationMemory, valid := clusterSSHApplicationUpdateOverlapBudget(r.Applications)
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, applicationCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, applicationMemory)
	if !ok {
		return 0, 0, false
	}
	rotationCPU, rotationMemory, valid := clusterSSHSiteWorkerBudget(r.SiteWorkers)
	if !valid {
		return 0, 0, false
	}
	for _, site := range snapshot.DesiredWorkerSites {
		var observedCPU, observedMemory uint64
		for _, worker := range r.SiteWorkers {
			if worker.SiteID == site {
				// NodeWorkloadBudget already checked the larger whole-set sums.
				observedCPU += worker.Resources.CPURequestMilli
				observedMemory += worker.Resources.MemoryLimitBytes
			}
		}
		if observedCPU < profile.CPURequestMilli {
			gap := profile.CPURequestMilli - observedCPU
			cpu, ok = clusterSSHCapacitySum(cpu, gap)
			if !ok {
				return 0, 0, false
			}
			rotationCPU, ok = clusterSSHCapacitySum(rotationCPU, gap)
			if !ok {
				return 0, 0, false
			}
		}
		if observedMemory < profile.MemoryLimitBytes {
			gap := profile.MemoryLimitBytes - observedMemory
			memory, ok = clusterSSHCapacitySum(memory, gap)
			if !ok {
				return 0, 0, false
			}
			rotationMemory, ok = clusterSSHCapacitySum(rotationMemory, gap)
			if !ok {
				return 0, 0, false
			}
		}
	}
	cpu, ok = clusterSSHCapacitySum(cpu, rotationCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, rotationMemory)
	if !ok {
		return 0, 0, false
	}
	return cpu, memory, true
}
