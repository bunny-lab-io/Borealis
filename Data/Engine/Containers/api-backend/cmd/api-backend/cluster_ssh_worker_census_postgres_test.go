package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestClusterSSHWorkerCensusPostgresDemandAndPoolRelease(t *testing.T) {
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
