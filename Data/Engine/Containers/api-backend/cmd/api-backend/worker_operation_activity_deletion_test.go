package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func deletionCustodyMetadata(id int64, terminal bool) string {
	operation := map[string]any{"version": 1, "token": strings.Repeat("a", 32), "worker_guid": schedulerK3sSiteWorkerGUID(17),
		"job_id": id, "node_name": "engine-one", "pod_uid": "b85f09f4-7c78-4eae-a1e5-f4ce67a360fb", "route_generation": 1,
		"site_id": 17, "kind": "execute_script", "agent_guid": "B17AB452-286B-47BA-A033-2AAEC0D7C855"}
	if terminal {
		operation["receipt_sha256"] = strings.Repeat("b", 64)
		operation["grant_nonce"] = strings.Repeat("c", 32)
	}
	return mustJSON(map[string]any{"worker_operation": operation})
}

func TestWorkerOperationActivityPostgresDeletionCustody(t *testing.T) {
	for _, mode := range []string{"ordinary", "committed", "unresolved", "status is not receipt", "wrong activity", "malformed", "duplicate key", "admission wins lock", "completion wins lock", "deletion wins lock", "device clear ordinary", "device clear unresolved", "device purge unresolved"} {
		t.Run(mode, func(t *testing.T) {
			m, ctx, _ := schedulerOwnershipFixture(t)
			host := "deletion-custody-" + newClusterUUID()
			var ids []int64
			for i := 0; i < 2; i++ {
				var id int64
				if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.activity_history(hostname,status,metadata_json) VALUES($1,'Running','{}') RETURNING id`, host).Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := m.store.db.ExecContext(cleanup, `DELETE FROM engine.activity_history WHERE id=ANY($1)`, pq.Array(ids)); err != nil {
					t.Error(err)
				}
			})
			metadata := "{}"
			allowed := mode == "ordinary" || mode == "committed" || mode == "completion wins lock" || mode == "deletion wins lock"
			switch mode {
			case "committed", "wrong activity":
				id := ids[1]
				if mode == "wrong activity" {
					id = ids[0]
				}
				metadata = deletionCustodyMetadata(id, true)
			case "unresolved", "status is not receipt", "completion wins lock", "device clear unresolved", "device purge unresolved":
				metadata = deletionCustodyMetadata(ids[1], false)
			case "malformed":
				metadata = `{"worker_operation":null}`
			case "duplicate key":
				metadata = `{"worker_operation":null,"worker_operation":{}}`
			}
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.activity_history SET metadata_json=$1,status='Success' WHERE id=$2`, metadata, ids[1]); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "device ") {
				m.store.db.SetMaxOpenConns(1)
				if mode == "device purge unresolved" {
					tx, err := m.store.db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = purgeDeviceRows(ctx, tx, devicePurgeRecord{Hostname: host})
					_ = tx.Rollback()
					if !errors.Is(err, errWorkerOperationActivityRetained) {
						t.Fatalf("purge did not retain uncertain execution: %v", err)
					}
				} else {
					payload, status, err := m.store.deleteDeviceActivity(ctx, operatorProfile{Role: "Admin"}, host)
					if mode == "device clear ordinary" {
						if err != nil || status != http.StatusOK || payload["status"] != "ok" {
							t.Fatalf("ordinary clear: payload=%v status=%d error=%v", payload, status, err)
						}
					} else if !errors.Is(err, errWorkerOperationActivityRetained) {
						t.Fatalf("clear did not retain uncertain execution: %v", err)
					}
				}
				var count int
				want := 2
				if mode == "device clear ordinary" {
					want = 0
				}
				if err := m.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.activity_history WHERE id=ANY($1)`, pq.Array(ids)).Scan(&count); err != nil || count != want {
					t.Fatalf("delete count=%d want=%d error=%v", count, want, err)
				}
				return
			}
			var blocker *sql.Tx
			if mode == "admission wins lock" || mode == "completion wins lock" {
				var err error
				blocker, err = m.store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer rollbackQuietly(blocker)
				if _, err := blocker.ExecContext(ctx, `UPDATE engine.activity_history SET metadata_json=$1 WHERE id=$2`, deletionCustodyMetadata(ids[1], mode == "completion wins lock"), ids[1]); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := m.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackQuietly(tx)
			// Earlier work in the caller transaction must also roll back on a
			// retained receipt; scheduled-job/device purge is one transaction.
			if _, err := tx.ExecContext(ctx, `UPDATE engine.activity_history SET script_name='caller changed' WHERE id=$1`, ids[0]); err != nil {
				t.Fatal(err)
			}
			type result struct {
				count int64
				err   error
			}
			done := make(chan result, 1)
			go func() {
				count, err := deleteScheduledActivityHistoryRows(ctx, tx, ids)
				done <- result{count, err}
			}()
			if blocker != nil {
				// Prove actual database lock wait before releasing the competing
				// writer, rather than relying on goroutine scheduling or sleeps.
				wait, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				for {
					var waiting bool
					if err := m.store.db.QueryRowContext(wait, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%activity_history WHERE id=ANY%')`).Scan(&waiting); err != nil {
						_ = blocker.Rollback()
						<-done
						t.Fatal(err)
					}
					if waiting {
						break
					}
				}
				if err := blocker.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			got := <-done
			if allowed {
				if got.err != nil || got.count != 2 {
					t.Fatalf("delete count=%d error=%v", got.count, got.err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if mode == "deletion wins lock" {
					var value string
					if err := m.store.db.QueryRowContext(ctx, `SELECT metadata_json FROM engine.activity_history WHERE id=$1 FOR UPDATE`, ids[1]).Scan(&value); !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("later admission found deleted activity: %q %v", value, err)
					}
				}
			} else {
				if !errors.Is(got.err, errWorkerOperationActivityRetained) || got.count != 0 {
					t.Fatalf("uncertain custody deleted: %+v", got)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := m.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.activity_history WHERE id=ANY($1) AND script_name IS NULL`, pq.Array(ids)).Scan(&count); err != nil || count != 2 {
					t.Fatalf("caller failed to roll back completely: count=%d error=%v", count, err)
				}
			}
		})
	}
}
