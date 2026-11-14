package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHWorkerCensusPostgresDemandAndPoolRelease(t *testing.T) {
	t.Run("CurrentRouteCustody", testClusterSSHCurrentWorkerRoutesPostgres)
	store, ctx, _, _ := clusterAdmissionFixture(t)
	store.db.SetMaxOpenConns(1)
	prefix := "census-" + newClusterUUID()
	var sites []int64
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, q := range []string{
			`DELETE FROM engine.job_scheduler_work_items WHERE dedupe_key LIKE $1`,
			`DELETE FROM engine.job_scheduler_workers WHERE worker_guid LIKE $1`,
			`DELETE FROM engine.device_sites WHERE device_hostname LIKE $1`,
			`DELETE FROM engine.devices WHERE guid LIKE $1`,
			`DELETE FROM engine.sites WHERE name LIKE $1`,
		} {
			if _, err := store.db.ExecContext(cleanup, q, prefix+"%"); err != nil {
				t.Error(err)
			}
		}
	})
	for i := 0; i < 12; i++ {
		var id int64
		if err := store.db.QueryRowContext(ctx, `INSERT INTO engine.sites(name) VALUES($1) RETURNING id`, fmt.Sprintf("%s-%d", prefix, i)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		sites = append(sites, id)
	}
	for i, status := range []string{workStatusQueued, workStatusRunning, "complete"} {
		exec(`INSERT INTO engine.job_scheduler_work_items(dedupe_key,kind,site_id,lane,payload_json,status,available_at,created_at,updated_at) VALUES($1,'test',$2,'test','{}',$3,0,0,0)`, fmt.Sprintf("%s-%d", prefix, i), sites[i], status)
	}
	for i, status := range []string{schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, "lost"} {
		exec(`INSERT INTO engine.job_scheduler_workers(worker_guid,container_name,site_id,status,started_at,last_seen_at,created_at,updated_at) VALUES($1,'test',$2,$3,0,0,0,0)`, fmt.Sprintf("%s-%d", prefix, i), sites[3+i], status)
	}
	now := time.Now().Unix()
	for i, status := range []string{"active", "ONLINE", "", "offline", "active"} {
		name := fmt.Sprintf("%s-device-%d", prefix, i)
		seen := now
		if i == 4 {
			seen -= 1000
		}
		exec(`INSERT INTO engine.devices(guid,hostname,status,last_seen) VALUES($1,upper($1),$2,$3)`, name, status, seen)
		exec(`INSERT INTO engine.device_sites(device_hostname,site_id) VALUES($1,$2)`, name, sites[7+i])
	}
	// Multiple demand sources for one site must still produce one profile floor.
	exec(`INSERT INTO engine.job_scheduler_workers(worker_guid,container_name,site_id,status,started_at,last_seen_at,created_at,updated_at) VALUES($1,'test',$2,'running',0,0,0,0)`, prefix+"-duplicate", sites[0])
	got, err := readClusterSSHWorkerCensus(ctx, store.db, 300)
	if err != nil || !clusterSSHWorkerCensusValid(got) {
		t.Fatal("census read failed", err)
	}
	for i, id := range sites {
		want := slices.Contains([]int{0, 1, 3, 4, 5, 7, 8, 9}, i)
		if slices.Contains(got, id) != want {
			t.Fatalf("site %d demand=%t", i, want)
		}
	}
	if store.db.Stats().InUse != 0 {
		t.Fatal("census retained database connection")
	}
	probe, cancel := context.WithTimeout(ctx, time.Second)
	conn, err := store.db.Conn(probe)
	cancel()
	if err != nil {
		t.Fatal("pool unavailable", err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err = readClusterSSHWorkerCensus(cancelled, store.db, 300); err != clusterbootstrap.ErrPreparationConfig {
		t.Fatal("cancelled census accepted")
	}
	// More than bounded demand must fail, never silently truncate qualification.
	exec(`INSERT INTO engine.job_scheduler_work_items(dedupe_key,kind,site_id,lane,payload_json,status,available_at,created_at,updated_at) SELECT $1 || '-bound-' || n,'test',1000000000+n,'test','{}','queued',0,0,0 FROM generate_series(1,257) n`, prefix)
	if _, err = readClusterSSHWorkerCensus(ctx, store.db, 300); err != clusterbootstrap.ErrPreparationConfig {
		t.Fatal("oversized census accepted")
	}
	if store.db.Stats().InUse != 0 {
		t.Fatal("rejected census retained database connection")
	}
}

func testClusterSSHCurrentWorkerRoutesPostgres(t *testing.T) {
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
	t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "1")
	store, ctx, _, _ := clusterAdmissionFixture(t)
	store.db.SetMaxOpenConns(1)
	prefix := "custody-" + newClusterUUID()
	var guids []string
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, guid := range guids {
			for _, table := range []string{"job_scheduler_worker_routes", "job_scheduler_workers"} {
				if _, err := store.db.ExecContext(cleanup, `DELETE FROM engine.`+table+` WHERE worker_guid=$1`, guid); err != nil {
					t.Error(err)
				}
			}
		}
		if _, err := store.db.ExecContext(cleanup, `DELETE FROM engine.sites WHERE name LIKE $1`, prefix+"%"); err != nil {
			t.Error(err)
		}
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want bool, count int) []clusterSSHCurrentWorkerRoute {
		t.Helper()
		values, err := readClusterSSHCurrentWorkerRoutes(ctx, store.db)
		if (err == nil) != want || (want && (values == nil || len(values) != count)) || (!want && values != nil) {
			t.Fatal("current SQL custody outcome mismatch", err, len(values), count)
		}
		if store.db.Stats().InUse != 0 {
			t.Fatal("current-route reader retained pool connection")
		}
		probe, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		conn, err := store.db.Conn(probe)
		if err != nil {
			t.Fatal("one-connection pool unavailable", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		return values
	}
	check(true, 0) // Exclusive fixture must start without unrelated current workers/routes.
	newRow := func(index int) clusterSSHCurrentWorkerRouteRow {
		t.Helper()
		var site int64
		if err := store.db.QueryRowContext(ctx, `INSERT INTO engine.sites(name) VALUES($1) RETURNING id`, fmt.Sprintf("%s-%d", prefix, index)).Scan(&site); err != nil {
			t.Fatal(err)
		}
		o := &borealisOperator{namespace: "borealis"}
		profile, _ := borealisOperatorSiteWorkerResourceProfile("standard")
		name, guid := siteWorkerPodNameForSite(site), schedulerK3sSiteWorkerGUID(site)
		pod := operatorInventoryClone(t, o.siteWorkerPodManifest(name, name, "10.43.0.7", site, "SQL Custody", "sql-custody", guid, "borealis-engine/site-worker:sha-cccccccccccc", "standard", profile, 56001, 61001))
		service := operatorInventoryClone(t, o.siteWorkerServiceManifest(name, site, "SQL Custody", "sql-custody", guid, 56001, 61001))
		clusterSSHStorageMap(pod, "metadata")["uid"] = newClusterUUID()
		clusterSSHStorageMap(pod, "spec")["nodeName"] = "engine-original"
		clusterSSHStorageMap(service, "spec")["clusterIP"] = "10.43.0.7"
		guids = append(guids, guid)
		return sshCustodyRouteRow(t, pod, service, `{"worker_admission":{"version":1,"operations":{"old":{"state":"terminal","pod_uid":"old-incarnation"}}}}`)
	}
	put := func(row clusterSSHCurrentWorkerRouteRow) {
		t.Helper()
		exec(`INSERT INTO engine.job_scheduler_workers(worker_guid,container_name,site_id,status,started_at,last_seen_at,created_at,updated_at,stopped_at)
VALUES($1,$2,$3,$4,0,0,0,0,91) ON CONFLICT(worker_guid) DO UPDATE SET container_name=EXCLUDED.container_name,site_id=EXCLUDED.site_id,status=EXCLUDED.status`, row.Worker, row.WorkerPod, row.WorkerSite, row.WorkerStatus)
		exec(`INSERT INTO engine.job_scheduler_worker_routes(worker_guid,container_name,site_id,status,generation,upstream_scheme,upstream_host,upstream_port,metadata_json,route_name,route_path_prefix,route_file_path,created_at,updated_at,retired_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'fixture','/fixture','/fixture',0,0,92) ON CONFLICT(worker_guid) DO UPDATE SET container_name=EXCLUDED.container_name,site_id=EXCLUDED.site_id,status=EXCLUDED.status,generation=EXCLUDED.generation,upstream_scheme=EXCLUDED.upstream_scheme,upstream_host=EXCLUDED.upstream_host,upstream_port=EXCLUDED.upstream_port,metadata_json=EXCLUDED.metadata_json`,
			row.RouteWorker, row.RoutePod, row.RouteSite, row.RouteStatus, row.Generation, row.Scheme, row.Host, row.Port, row.Metadata)
	}
	row := newRow(0)
	put(row)
	baseline := check(true, 1)
	for _, mode := range []string{"worker orphan", "route orphan", "terminal worker", "retired route", "both terminal", "stale timestamps", "old generation", "old container", "duplicate raw metadata", "tiny fractional rank", "missing placement", "oversize metadata", "historical ledger change"} {
		t.Run(mode, func(t *testing.T) {
			put(row)
			switch mode {
			case "worker orphan":
				exec(`DELETE FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, row.Worker)
			case "route orphan":
				exec(`DELETE FROM engine.job_scheduler_workers WHERE worker_guid=$1`, row.Worker)
			case "terminal worker":
				exec(`UPDATE engine.job_scheduler_workers SET status='lost' WHERE worker_guid=$1`, row.Worker)
			case "retired route":
				exec(`UPDATE engine.job_scheduler_worker_routes SET status='retired' WHERE worker_guid=$1`, row.Worker)
			case "both terminal":
				exec(`UPDATE engine.job_scheduler_workers SET status='lost' WHERE worker_guid=$1`, row.Worker)
				exec(`UPDATE engine.job_scheduler_worker_routes SET status='retired' WHERE worker_guid=$1`, row.Worker)
			case "old generation":
				exec(`UPDATE engine.job_scheduler_worker_routes SET generation=generation+1 WHERE worker_guid=$1`, row.Worker)
			case "old container":
				exec(`UPDATE engine.job_scheduler_workers SET container_name=container_name||'-old' WHERE worker_guid=$1`, row.Worker)
			case "duplicate raw metadata":
				exec(`UPDATE engine.job_scheduler_worker_routes SET metadata_json=$2 WHERE worker_guid=$1`, row.Worker, strings.Replace(row.Metadata.String, `"worker_placement":`, `"worker_placement":null,"worker_placement":`, 1))
			case "tiny fractional rank":
				exec(`UPDATE engine.job_scheduler_worker_routes SET metadata_json=$2 WHERE worker_guid=$1`, row.Worker, strings.Replace(row.Metadata.String, `"profile_rank":1`, `"profile_rank":1.00000000000000000001`, 1))
			case "missing placement":
				exec(`UPDATE engine.job_scheduler_worker_routes SET metadata_json='{}' WHERE worker_guid=$1`, row.Worker)
			case "oversize metadata":
				exec(`UPDATE engine.job_scheduler_worker_routes SET metadata_json=$2 WHERE worker_guid=$1`, row.Worker, strings.Repeat(" ", operatorSiteWorkerInventoryMaxBytes+1))
			case "historical ledger change":
				exec(`UPDATE engine.job_scheduler_worker_routes SET metadata_json=$2 WHERE worker_guid=$1`, row.Worker, strings.Replace(row.Metadata.String, "old-incarnation", "retained-historical-incarnation", 1))
			}
			valid := mode == "both terminal" || mode == "stale timestamps" || mode == "historical ledger change"
			count := 1
			if mode == "both terminal" {
				count = 0
			}
			values := check(valid, count)
			if mode == "historical ledger change" && values[0].SHA256 == baseline[0].SHA256 {
				t.Fatal("raw historical ledger drift vanished")
			}
		})
	}
	put(row)
	var unchanged string
	if err := store.db.QueryRowContext(ctx, `SELECT metadata_json FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, row.Worker).Scan(&unchanged); err != nil || unchanged != row.Metadata.String {
		t.Fatal("reader rewrote historical ledger")
	}
	for i := 1; i < clusterSSHSiteWorkerLimit; i++ {
		put(newRow(i))
	}
	check(true, clusterSSHSiteWorkerLimit)
	put(newRow(clusterSSHSiteWorkerLimit))
	check(false, 0)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if values, err := readClusterSSHCurrentWorkerRoutes(cancelled, store.db); err != clusterbootstrap.ErrPreparationConfig || values != nil || store.db.Stats().InUse != 0 {
		t.Fatal("cancelled current reader accepted or retained connection")
	}
}
