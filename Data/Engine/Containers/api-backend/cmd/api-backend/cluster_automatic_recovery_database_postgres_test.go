package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterAutomaticRecoveryDatabasePostgresAuthority(t *testing.T) {
	for _, mode := range []string{"partial health", "real SQL no replicas", "SQL cancellation", "native forbidden", "replica drift", "Pod drift", "holder before", "holder after", "expiry after", "member after", "member origin mismatch", "missing CNPG provenance", "pool mismatch", "namespace mismatch", "cancelled during SQL", "concurrent", "authority history reset", "store failure session reset", "missing observer session"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticRecoveryPostgresFixture(t)
			r := f.reserve(t)
			db := f.c.controller.store.db
			b, objects, rows := automaticRecoveryDatabaseFixture()
			snapshot, _, err := readClusterAutomaticRecoverySnapshot(f.ctx, db, r.Controller)
			if err != nil {
				t.Fatal(err)
			}
			originalMembers := []clusterControllerNode{clusterNodeByID(snapshot.Nodes, r.NodeID)}
			for _, n := range snapshot.Nodes {
				if n.ID != r.NodeID {
					originalMembers = append(originalMembers, n)
				}
			}
			b.Node.ClusterID, b.Node.NodeID, b.Node.NodeName, b.Node.NodeUID, b.Node.Controller = r.ClusterID, r.NodeID, r.NodeName, r.NodeUID, r.Controller
			pods := objects[clusterSSHStoragePodsPath+"&limit=4"]["items"].([]any)
			for i, n := range originalMembers {
				m := &b.Members[i]
				node := objects["/api/v1/nodes/"+m.NodeName]
				delete(objects, "/api/v1/nodes/"+m.NodeName)
				m.NodeID, m.NodeName = n.ID, n.Name
				if i == 0 {
					m.NodeUID = r.NodeUID
				}
				meta := clusterSSHStorageMap(node, "metadata")
				meta["name"], meta["uid"] = m.NodeName, m.NodeUID
				objects["/api/v1/nodes/"+m.NodeName] = node
				clusterSSHStorageMap(pods[i].(map[string]any), "spec")["nodeName"] = m.NodeName
			}
			if !b.valid() {
				t.Fatal("invalid bound fixture")
			}
			if mode == "SQL cancellation" {
				// A single occupied pool slot forces actual database/sql acquisition to
				// wait. Cancellation must return without taking or leaking that slot.
				held, err := db.Conn(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(f.ctx, 50*time.Millisecond)
				defer cancel()
				before := db.Stats().WaitCount
				if values, err := readClusterAutomaticRecoveryDatabaseSQL(ctx, db); err == nil || values != nil || db.Stats().WaitCount <= before {
					t.Error("cancelled SQL acquired occupied pool")
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
				if db.Stats().InUse != 0 {
					t.Fatal("SQL pool leaked")
				}
				return
			}
			var gets, probes atomic.Int32
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gets.Add(1)
				if db.Stats().InUse != 0 {
					t.Error("SQL retained during native IO")
				}
				if req.Method != http.MethodGet {
					t.Error("native mutation")
				}
				if mode == "native forbidden" {
					w.WriteHeader(403)
					return
				}
				object, ok := objects[req.URL.RequestURI()]
				if !ok {
					t.Error("unbound request", req.URL.RequestURI())
					http.NotFound(w, req)
					return
				}
				_ = json.NewEncoder(w).Encode(object)
			}))
			defer native.Close()
			runner := &kubernetesClusterStepRunner{db: db, namespace: "borealis", controllerHolder: r.Controller, kube: &kubernetesAPIClient{baseURL: native.URL, httpClient: native.Client()}}
			f.c.controller.runner = runner
			if mode == "pool mismatch" {
				runner.db = nil
			}
			if mode == "namespace mismatch" {
				runner.namespace = "other"
			}
			if mode == "missing CNPG provenance" {
				b.ClusterUID = ""
			}
			if mode == "member origin mismatch" {
				b.Members[2].NodeID = newClusterUUID()
			}
			if mode == "holder before" {
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName)
			}
			sessionContext := context.WithValue(f.ctx, clusterAutomaticRecoveryObserverSessionKey{}, r.Session)
			if mode == "missing observer session" {
				sessionContext = f.ctx
			}
			ctx, cancel := context.WithCancel(sessionContext)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			read := func(ctx context.Context) ([]clusterAutomaticRecoveryDatabaseRow, error) {
				call := probes.Add(1)
				if mode == "concurrent" && call == 1 {
					close(entered)
					<-release
				}
				for i := range rows {
					rows[i].Reply.Time = rows[i].Reply.Time.Add(time.Second)
				}
				if db.Stats().InUse != 0 {
					t.Error("SQL retained before database observation")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > clusterControllerLeaseAcquireTimeout {
					t.Error("original five-second scope missing")
				}
				switch mode {
				case "replica drift":
					rows[1].Client.String = "10.42.99.99"
				case "Pod drift":
					clusterSSHStorageMap(pods[2].(map[string]any), "metadata")["resourceVersion"] = "2"
				case "holder after":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName)
				case "expiry after":
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
				case "member after":
					f.exec(t, `UPDATE engine.cluster_nodes SET node_name='changed-member' WHERE id=$1`, b.Members[2].NodeID)
				case "authority history reset":
					if call == 3 {
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName)
					}
				case "cancelled during SQL":
					cancel()
				}
				return rows, nil
			}
			observer := f.c.databaseHealthObserverWithRead(b, read)
			if mode == "real SQL no replicas" {
				actual, err := readClusterAutomaticRecoveryDatabaseSQL(f.ctx, db)
				if err != nil || len(actual) != 1 || actual[0].Application.Valid || db.Stats().InUse != 0 {
					t.Fatal("actual PostgreSQL query/close", actual, err, db.Stats())
				}
				if recoveryDatabaseReplication(b, actual) {
					t.Fatal("single fixture database proved replication")
				}
				observer = f.c.databaseHealthObserver(b)
			}
			target := clusterNodeByID(snapshot.Nodes, r.NodeID)
			if mode == "missing observer session" {
				if _, err := observer(ctx, target); err == nil || gets.Load() != 0 || probes.Load() != 0 {
					t.Fatal("missing session reached native IO", err)
				}
				return
			}
			if mode == "store failure session reset" {
				if _, err := observer(ctx, target); err == nil {
					t.Fatal("first sample healthy")
				}
				callback := func(ctx context.Context, node clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
					health, err := observer(ctx, node)
					if err != nil || !health.Database {
						t.Error("established progress missing", health, err)
					}
					// Native scope completed, but coordinator's following
					// transaction must fail its original lease check.
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName)
					return health, err
				}
				if _, err := f.c.Observe(f.ctx, r.OperationID, callback); err == nil {
					t.Fatal("post-callback store failure accepted")
				}
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, r.Controller)
				var nextSession string
				reset := func(ctx context.Context, node clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
					nextSession = clusterAutomaticRecoveryObserverSession(ctx)
					health, err := observer(ctx, node)
					if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
						t.Error("failed store retained old session history", health, err)
					}
					return health, err
				}
				next, err := f.c.Observe(f.ctx, r.OperationID, reset)
				if err != nil || nextSession == r.Session || nextSession == "" || next.HealthySince != 0 {
					t.Fatal("session reset missing", next, err)
				}
				return
			}

			if mode == "concurrent" {
				result := make(chan error, 1)
				go func() { _, err := observer(ctx, target); result <- err }()
				select {
				case <-entered:
				case err := <-result:
					t.Fatal("overlap fixture failed before SQL observation", err)
				}
				if _, err := observer(ctx, target); err == nil {
					t.Error("concurrent observer accepted")
				}
				close(release)
				if err := <-result; err == nil {
					t.Fatal("in-flight history survived overlap")
				}
				if _, err := observer(ctx, target); err == nil {
					t.Fatal("overlap retained baseline")
				}
				if health, err := observer(ctx, target); err != nil || !health.Database {
					t.Fatal("fresh continuity failed", health, err)
				}
				return
			}
			if mode == "authority history reset" {
				if _, err := observer(ctx, target); err == nil {
					t.Fatal("first sample healthy")
				}
				if health, err := observer(ctx, target); err != nil || !health.Database {
					t.Fatal("fresh progress missing", health, err)
				}
				if _, err := observer(ctx, target); err == nil {
					t.Fatal("lost authority healthy")
				}
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, r.Controller)
				if _, err := observer(ctx, target); err == nil {
					t.Fatal("authority loss retained history")
				}
				return
			}
			if mode == "partial health" {
				if _, err := observer(ctx, target); err == nil {
					t.Fatal("first sample became Database health")
				}
			}

			if strings.HasSuffix(mode, " after") || textInSet(mode, "holder before", "member origin mismatch", "missing CNPG provenance", "pool mismatch", "namespace mismatch", "cancelled during SQL") {
				health, err := observer(ctx, target)
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatal("lost authority became health", health, err)
				}
				if !strings.HasSuffix(mode, " after") && mode != "cancelled during SQL" && (gets.Load() != 0 || probes.Load() != 0) {
					t.Fatal("invalid authority reached observation")
				}
				return
			}
			seed, err := f.c.Observe(f.ctx, r.OperationID, f.observer(t, true))
			if err != nil || seed.HealthySince == 0 {
				t.Fatal("healthy interval fixture", seed, err)
			}
			next, err := f.c.Observe(f.ctx, r.OperationID, observer)
			if err != nil || next.Phase != "recovering" || next.HealthySince != 0 || next.RecoveredAt != 0 || next.RecoveryDeadline != r.RecoveryDeadline || next.NodeUID != r.NodeUID {
				t.Fatal("partial database health advanced H04", next, err)
			}
			if mode == "partial health" {
				if next.Reason != "required_health_unavailable" || gets.Load() != 40 || probes.Load() != 2 {
					t.Fatal("database proof scope", next, gets.Load(), probes.Load())
				}
			} else if next.Reason != "observation_unknown" {
				t.Fatal("uncertain database proof retained continuity", next)
			}
			if db.Stats().InUse != 0 {
				t.Fatal("observation leaked connection")
			}
			f.assertInert(t, r.OperationID)
		})
	}
}
