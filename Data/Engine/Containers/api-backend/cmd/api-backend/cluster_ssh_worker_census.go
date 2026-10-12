package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"database/sql"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"time"
)

const clusterSSHWorkerSecretName = "borealis-job-scheduler-runtime-env"
const clusterSSHWorkerSecretPath = "/api/v1/namespaces/borealis/secrets/" + clusterSSHWorkerSecretName

type clusterSSHWorkerPolicy struct {
	Profile      string `json:"profile"`
	OnlineWindow int64  `json:"online_window_seconds"`
}

func (p clusterSSHWorkerPolicy) resources() (clusterSSHApplicationResource, bool) {
	profile, ok := borealisOperatorSiteWorkerResourceProfile(p.Profile)
	if !ok || !slices.Contains([]string{"small", "standard", "large"}, p.Profile) || p.OnlineWindow < 60 || p.OnlineWindow > 3600 {
		return clusterSSHApplicationResource{}, false
	}
	return clusterSSHWorkloadResources("site-worker", map[string]any{
		"requests": map[string]any{"cpu": profile.RequestCPU, "memory": profile.RequestMemory},
		"limits":   map[string]any{"cpu": profile.LimitCPU, "memory": profile.LimitMemory},
	})
}

// Resolve only the scheduler's two demand inputs from its declared environment.
// Never use this controller's environment or infer them from a surviving worker.
// Existing producer image/entrypoint supplies no defaults beyond scheduler code.
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
	if _, ok := p.resources(); !ok {
		return fail()
	}
	return p, nil
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
// profile; max by resource avoids both undercounting and double charging.
func clusterSSHDesiredWorkerWorkloadBudget(snapshot clusterSSHPreparationSnapshot) (cpu, memory uint64, ok bool) {
	r := snapshot.Storage.Requirements
	cpu, memory, ok = clusterSSHNodeWorkloadBudget(r)
	profile, valid := r.WorkerPolicy.resources()
	if !ok || !valid || !clusterSSHWorkerCensusValid(snapshot.DesiredWorkerSites) {
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
			cpu, ok = clusterSSHCapacitySum(cpu, profile.CPURequestMilli-observedCPU)
			if !ok {
				return 0, 0, false
			}
		}
		if observedMemory < profile.MemoryLimitBytes {
			memory, ok = clusterSSHCapacitySum(memory, profile.MemoryLimitBytes-observedMemory)
			if !ok {
				return 0, 0, false
			}
		}
	}
	return cpu, memory, true
}
