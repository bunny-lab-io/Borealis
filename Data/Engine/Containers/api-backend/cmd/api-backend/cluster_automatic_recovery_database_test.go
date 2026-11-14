package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func automaticRecoveryDatabaseFixture() (clusterAutomaticRecoveryDatabaseBinding, map[string]map[string]any, []clusterAutomaticRecoveryDatabaseRow) {
	n, ns, node := automaticRecoveryNativeFixture()
	b := clusterAutomaticRecoveryDatabaseBinding{Node: n, NamespaceUID: newClusterUUID(), ClusterUID: newClusterUUID(), PrimaryPod: "borealis-postgres-1"}
	object := func(version, kind, name, uid, namespace string) map[string]any {
		return map[string]any{"apiVersion": version, "kind": kind, "metadata": map[string]any{"name": name, "uid": uid, "namespace": namespace, "resourceVersion": "1"}}
	}
	objects := map[string]map[string]any{"/api/v1/namespaces/kube-system": ns, "/api/v1/namespaces/borealis": object("v1", "Namespace", "borealis", b.NamespaceUID, "")}
	cluster := object("postgresql.cnpg.io/v1", "Cluster", "borealis-postgres", b.ClusterUID, "borealis")
	cluster["spec"] = map[string]any{"instances": 3, "postgresql": map[string]any{"parameters": map[string]any{"synchronous_commit": "on"}, "synchronous": map[string]any{"method": "any", "number": 1, "dataDurability": "required"}}}
	cluster["status"] = map[string]any{"readyInstances": 3, "currentPrimary": b.PrimaryPod, "targetPrimary": b.PrimaryPod, "phase": "Cluster in healthy state"}
	objects[clusterSSHStoragePostgresPath] = cluster
	pods := []any{}
	for i := 0; i < 3; i++ {
		m := clusterAutomaticRecoveryDatabaseMember{NodeID: newClusterUUID(), NodeName: fmt.Sprintf("database-node-%d", i+1), NodeUID: newClusterUUID(), Pod: fmt.Sprintf("borealis-postgres-%d", i+1), PodUID: newClusterUUID(), PodIP: fmt.Sprintf("10.42.%d.10", i), Image: "registry.example/postgres@sha256:" + strings.Repeat("a", 64), ImageID: "registry.example/postgres@sha256:" + strings.Repeat("b", 64), ContainerID: fmt.Sprintf("containerd://%064x", i+1)}
		if i == 0 {
			m.NodeID, m.NodeName, m.NodeUID = n.NodeID, n.NodeName, n.NodeUID
		}
		b.Members[i] = m
		nativeNode := object("v1", "Node", m.NodeName, m.NodeUID, "")
		nativeNode["status"] = node["status"]
		objects["/api/v1/nodes/"+m.NodeName] = nativeNode
		p := object("v1", "Pod", m.Pod, m.PodUID, "borealis")
		meta := clusterSSHStorageMap(p, "metadata")
		meta["labels"] = map[string]any{"cnpg.io/cluster": "borealis-postgres"}
		meta["ownerReferences"] = []any{map[string]any{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "name": "borealis-postgres", "uid": b.ClusterUID, "controller": true}}
		p["spec"] = map[string]any{"nodeName": m.NodeName, "containers": []any{map[string]any{"name": "postgres", "image": m.Image}}, "initContainers": []any{map[string]any{"name": "bootstrap-controller"}}}
		p["status"] = map[string]any{"phase": "Running", "podIP": m.PodIP, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "postgres", "image": m.Image, "imageID": m.ImageID, "containerID": m.ContainerID, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-01T00:00:00Z"}}}}, "initContainerStatuses": []any{map[string]any{"name": "bootstrap-controller", "state": map[string]any{"terminated": map[string]any{"exitCode": 0, "reason": "Completed"}}}}}
		pods = append(pods, p)
	}
	objects[clusterSSHStoragePodsPath+"&limit=4"] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": pods}
	observed := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	nsString := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	rows := []clusterAutomaticRecoveryDatabaseRow{}
	for _, m := range b.Members[1:] {
		rows = append(rows, clusterAutomaticRecoveryDatabaseRow{Reply: sql.NullTime{Time: observed.Add(-time.Second), Valid: true}, Server: nsString(b.Members[0].PodIP), Commit: nsString("on"), Standbys: nsString(`ANY 1 ("borealis-postgres-2", "borealis-postgres-3")`), Point: nsString("1/20"), Application: nsString(m.Pod), Client: nsString(m.PodIP), State: nsString("streaming"), Sync: nsString("quorum"), Flush: nsString("1/21")})
	}
	return b, objects, rows
}

func TestClusterAutomaticRecoveryDatabaseReplication(t *testing.T) {
	for _, mode := range []string{"healthy", "reordered", "equal WAL", "asynchronous peer", "no rows", "missing", "extra", "duplicate", "foreign empty address", "address drift", "server drift", "standby server", "commit off", "policy wildcard", "policy priority", "policy number", "policy duplicate", "policy missing quote", "policy oversized", "policy null", "policy foreign", "all async", "priority sync", "priority potential", "reply future", "reply null", "unknown sync", "not streaming", "missing address", "missing app", "missing state", "missing sync", "missing point", "missing flush", "behind WAL", "zero WAL", "bad WAL", "row point drift", "row policy drift", "binding missing UID", "binding repeated node", "binding repeated IP", "binding changed target", "binding changed primary"} {
		t.Run(mode, func(t *testing.T) {
			b, _, rows := automaticRecoveryDatabaseFixture()
			r := &rows[0]
			switch mode {
			case "reordered":
				rows[0], rows[1] = rows[1], rows[0]
			case "equal WAL":
				r.Flush = r.Point
			case "asynchronous peer":
				r.Sync.String = "async"
			case "no rows":
				rows = nil
			case "missing":
				rows = rows[:1]
			case "extra":
				rows = append(rows, rows[0])
			case "duplicate":
				rows[1] = rows[0]
			case "foreign empty address":
				r.Application.String = "foreign"
				r.Client.String = ""
			case "address drift":
				r.Client.String = "10.42.99.99"
			case "server drift":
				r.Server.String = b.Members[1].PodIP
			case "standby server":
				r.Recovery = true
			case "commit off":
				r.Commit.String = "off"
			case "policy wildcard":
				r.Standbys.String = "ANY 1 (*)"
			case "policy priority":
				r.Standbys.String = "FIRST 1 (borealis-postgres-2,borealis-postgres-3)"
			case "policy number":
				r.Standbys.String = "ANY 0 (borealis-postgres-2,borealis-postgres-3)"
			case "policy duplicate":
				r.Standbys.String = "ANY 1 (borealis-postgres-2,borealis-postgres-2)"
			case "policy missing quote":
				r.Standbys.String = `ANY 1 ("borealis-postgres-2,borealis-postgres-3)`
			case "policy oversized":
				r.Standbys.String = strings.Repeat(" ", 513)
			case "policy null":
				r.Standbys.Valid = false
			case "policy foreign":
				r.Standbys.String = "ANY 1 (borealis-postgres-2,borealis-postgres-4)"
			case "all async":
				rows[0].Sync.String = "async"
				rows[1].Sync.String = "async"
			case "priority sync":
				r.Sync.String = "sync"
			case "priority potential":
				r.Sync.String = "potential"
			case "reply future":
				r.Reply.Time = r.Reply.Time.Add(24 * time.Hour)
			case "reply null":
				r.Reply.Valid = false
			case "unknown sync":
				r.Sync.String = "unknown"
			case "not streaming":
				r.State.String = "catchup"
			case "missing address":
				r.Client.Valid = false
			case "missing app":
				r.Application.Valid = false
			case "missing state":
				r.State.Valid = false
			case "missing sync":
				r.Sync.Valid = false
			case "missing point":
				r.Point.Valid = false
			case "missing flush":
				r.Flush.Valid = false
			case "behind WAL":
				r.Flush.String = "1/1F"
			case "zero WAL":
				r.Point.String = "0/0"
			case "bad WAL":
				r.Flush.String = "100000000/1"
			case "row point drift":
				rows[1].Point.String = "1/21"
			case "row policy drift":
				rows[1].Commit.String = "off"
			case "binding missing UID":
				b.ClusterUID = ""
			case "binding repeated node":
				b.Members[2].NodeName = b.Members[1].NodeName
			case "binding repeated IP":
				b.Members[2].PodIP = b.Members[1].PodIP
			case "binding changed target":
				b.Node.NodeUID = newClusterUUID()
			case "binding changed primary":
				b.PrimaryPod = "borealis-postgres-4"
			}
			want := textInSet(mode, "healthy", "reordered", "equal WAL", "asynchronous peer", "reply future")
			if got := recoveryDatabaseReplication(b, rows); got != want {
				t.Fatal("replication decision", got, want)
			}
		})
	}
}

func TestClusterAutomaticRecoveryDatabaseNative(t *testing.T) {
	for _, mode := range []string{"healthy database", "missing namespace", "namespace UID", "CNPG UID", "Node UID", "Node unknown", "primary drift", "target primary drift", "CNPG phase", "CNPG ready count", "CNPG instances", "durability preferred", "sync count", "commit off", "missing Pod", "extra Pod", "duplicate Pod", "continued list", "remaining list", "Pod UID", "Pod owner", "Pod node", "Pod IP", "Pod unready", "container restarted", "image drift", "init active", "init sidecar", "aliased init policy", "deleting Pod", "aliased deletion", "ephemeral", "host network", "SQL error", "SQL unknown", "SQL cancellation", "native cancellation", "native timeout", "native forbidden", "redirect", "duplicate JSON", "alias JSON", "trailing JSON", "oversize", "Pod drift after SQL", "CNPG drift after SQL", "Node drift after SQL"} {
		t.Run(mode, func(t *testing.T) {
			b, objects, rows := automaticRecoveryDatabaseFixture()
			cluster := objects[clusterSSHStoragePostgresPath]
			status := clusterSSHStorageMap(cluster, "status")
			spec := clusterSSHStorageMap(cluster, "spec")
			policy := clusterSSHStorageMap(clusterSSHStorageMap(spec, "postgresql"), "synchronous")
			list := objects[clusterSSHStoragePodsPath+"&limit=4"]
			pods := list["items"].([]any)
			pod := pods[1].(map[string]any)
			ps := clusterSSHStorageMap(pod, "spec")
			pstatus := clusterSSHStorageMap(pod, "status")
			runtime := pstatus["containerStatuses"].([]any)[0].(map[string]any)
			switch mode {
			case "missing namespace":
				delete(objects, "/api/v1/namespaces/borealis")
			case "namespace UID":
				clusterSSHStorageMap(objects["/api/v1/namespaces/borealis"], "metadata")["uid"] = newClusterUUID()
			case "CNPG UID":
				clusterSSHStorageMap(cluster, "metadata")["uid"] = newClusterUUID()
			case "Node UID":
				clusterSSHStorageMap(objects["/api/v1/nodes/"+b.Members[1].NodeName], "metadata")["uid"] = newClusterUUID()
			case "Node unknown":
				clusterSSHStorageMap(objects["/api/v1/nodes/"+b.Members[1].NodeName], "status")["conditions"] = []any{map[string]any{"type": "Ready", "status": "Unknown"}}
			case "primary drift":
				status["currentPrimary"] = b.Members[1].Pod
			case "target primary drift":
				status["targetPrimary"] = b.Members[1].Pod
			case "CNPG phase":
				status["phase"] = "Failing over"
			case "CNPG ready count":
				status["readyInstances"] = 2
			case "CNPG instances":
				spec["instances"] = 1
			case "durability preferred":
				policy["dataDurability"] = "preferred"
			case "sync count":
				policy["number"] = 0
			case "commit off":
				clusterSSHStorageMap(clusterSSHStorageMap(spec, "postgresql"), "parameters")["synchronous_commit"] = "off"
			case "missing Pod":
				list["items"] = pods[:2]
			case "extra Pod":
				list["items"] = append(pods, pod)
			case "duplicate Pod":
				list["items"] = []any{pods[0], pods[1], pods[1]}
			case "continued list":
				clusterSSHStorageMap(list, "metadata")["continue"] = "next"
			case "remaining list":
				clusterSSHStorageMap(list, "metadata")["remainingItemCount"] = 1
			case "Pod UID":
				clusterSSHStorageMap(pod, "metadata")["uid"] = newClusterUUID()
			case "Pod owner":
				clusterSSHStorageMap(pod, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "Pod node":
				ps["nodeName"] = b.Members[2].NodeName
			case "Pod IP":
				pstatus["podIP"] = "10.42.99.99"
			case "Pod unready":
				runtime["ready"] = false
			case "container restarted":
				runtime["containerID"] = "containerd://" + strings.Repeat("f", 64)
			case "image drift":
				runtime["imageID"] = "registry.example/postgres@sha256:" + strings.Repeat("c", 64)
			case "init active":
				pstatus["initContainerStatuses"].([]any)[0].(map[string]any)["state"] = map[string]any{"running": map[string]any{}}
			case "init sidecar":
				ps["initContainers"].([]any)[0].(map[string]any)["restartPolicy"] = "Always"
			case "aliased init policy":
				ps["initContainers"].([]any)[0].(map[string]any)["RestartPolicy"] = "Always"
			case "deleting Pod":
				clusterSSHStorageMap(pod, "metadata")["deletionTimestamp"] = "now"
			case "aliased deletion":
				clusterSSHStorageMap(pod, "metadata")["DeletionTimestamp"] = "now"
			case "ephemeral":
				ps["ephemeralContainers"] = []any{map[string]any{"name": "debug"}}
			case "host network":
				ps["hostNetwork"] = true
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "native timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 50*time.Millisecond)
				defer stop()
			}
			var gets atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gets.Add(1)
				if r.Method != http.MethodGet {
					t.Error("mutation")
				}
				if mode == "native cancellation" {
					cancel()
					<-r.Context().Done()
					return
				}
				if mode == "native timeout" {
					<-r.Context().Done()
					return
				}
				if mode == "native forbidden" {
					w.WriteHeader(403)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "/redirected", 307)
					return
				}
				object, ok := objects[r.URL.RequestURI()]
				if !ok {
					http.NotFound(w, r)
					return
				}
				raw, _ := json.Marshal(object)
				wire := string(raw)
				if r.URL.RequestURI() == clusterSSHStoragePodsPath+"&limit=4" {
					switch mode {
					case "duplicate JSON":
						wire = strings.Replace(wire, `"uid":`, `"uid":"duplicate","uid":`, 1)
					case "alias JSON":
						wire = strings.Replace(wire, `"podIP":`, `"PodIP":`, 1)
					case "trailing JSON":
						wire += `{}`
					case "oversize":
						wire += strings.Repeat(" ", 2<<20)
					}
				}
				_, _ = w.Write([]byte(wire))
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
			probes := 0
			read := func(context.Context) ([]clusterAutomaticRecoveryDatabaseRow, error) {
				probes++
				switch mode {
				case "SQL error":
					return nil, errors.New("private failure")
				case "SQL unknown":
					return nil, nil
				case "SQL cancellation":
					cancel()
				case "Pod drift after SQL":
					clusterSSHStorageMap(pod, "metadata")["resourceVersion"] = "2"
				case "CNPG drift after SQL":
					clusterSSHStorageMap(cluster, "metadata")["resourceVersion"] = "2"
				case "Node drift after SQL":
					clusterSSHStorageMap(objects["/api/v1/nodes/"+b.Members[2].NodeName], "metadata")["resourceVersion"] = "2"
				}
				return rows, nil
			}
			health, err := readClusterAutomaticRecoveryDatabase(ctx, func(ctx context.Context, path string, out any) error {
				return kube.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
			}, read, b)
			if mode != "healthy database" {
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatal("unknown became health", health, err)
				}
				return
			}
			want := clusterAutomaticRecoveryHealth{NodeName: b.Node.NodeName, NodeUID: b.Node.NodeUID, Node: true}
			if err != nil || health != want || health.healthy() || probes != 1 || gets.Load() != 20 {
				t.Fatal("database scope", health, err, probes, gets.Load())
			}
		})
	}
}

// A driver fixture exercises database/sql rows, connection return and context
// cancellation, without claiming any native PostgreSQL replication evidence.
type recoveryDatabaseConnector struct {
	mode       string
	rowsClosed atomic.Bool
	entered    chan struct{}
	values     [][]driver.Value
}

func (c *recoveryDatabaseConnector) Connect(context.Context) (driver.Conn, error) {
	return &recoveryDatabaseConn{c}, nil
}
func (c *recoveryDatabaseConnector) Driver() driver.Driver { return c }
func (c *recoveryDatabaseConnector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}

type recoveryDatabaseConn struct{ c *recoveryDatabaseConnector }

func (*recoveryDatabaseConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (*recoveryDatabaseConn) Begin() (driver.Tx, error) { return nil, errors.New("unsupported") }
func (*recoveryDatabaseConn) Close() error              { return nil }
func (c *recoveryDatabaseConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if q != clusterAutomaticRecoveryDatabaseQuery {
		return nil, errors.New("query changed")
	}
	if c.c.mode == "query error" {
		return nil, errors.New("private query failure")
	}
	if c.c.mode == "cancelled query" {
		close(c.c.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &recoveryDatabaseRows{c: c.c}, nil
}

type recoveryDatabaseRows struct {
	c     *recoveryDatabaseConnector
	index int
}

func (*recoveryDatabaseRows) Columns() []string {
	return []string{"server", "recovery", "commit", "standbys", "point", "application", "client", "state", "sync", "flush", "reply_time"}
}
func (r *recoveryDatabaseRows) Close() error {
	r.c.rowsClosed.Store(true)
	if r.c.mode == "close error" {
		return errors.New("private close failure")
	}
	return nil
}
func (r *recoveryDatabaseRows) Next(out []driver.Value) error {
	if r.index >= len(r.c.values) {
		if r.c.mode == "row error" {
			return errors.New("private row failure")
		}
		return io.EOF
	}
	copy(out, r.c.values[r.index])
	r.index++
	return nil
}

func TestClusterAutomaticRecoveryDatabaseSQLLifecycle(t *testing.T) {
	for _, mode := range []string{"healthy", "query error", "scan error", "row error", "close error", "too many", "cancelled before", "cancelled query"} {
		t.Run(mode, func(t *testing.T) {
			b, _, want := automaticRecoveryDatabaseFixture()
			c := &recoveryDatabaseConnector{mode: mode, entered: make(chan struct{})}
			for _, r := range want {
				c.values = append(c.values, []driver.Value{r.Server.String, false, r.Commit.String, r.Standbys.String, r.Point.String, r.Application.String, r.Client.String, r.State.String, r.Sync.String, r.Flush.String, r.Reply.Time})
			}
			if mode == "scan error" {
				c.values[1][1] = "not bool"
			}
			if mode == "too many" {
				c.values = append(c.values, c.values...)
			}
			db := sql.OpenDB(c)
			defer db.Close()
			db.SetMaxOpenConns(1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled before" {
				cancel()
			}
			joined := make(chan struct{})
			if mode == "cancelled query" {
				go func() { defer close(joined); <-c.entered; cancel() }()
			} else {
				close(joined)
			}
			rows, err := readClusterAutomaticRecoveryDatabaseSQL(ctx, db)
			<-joined
			if db.Stats().InUse != 0 {
				t.Fatal("SQL connection retained")
			}
			if mode == "healthy" {
				if err != nil || !reflect.DeepEqual(rows, want) || !recoveryDatabaseReplication(b, rows) {
					t.Fatal("positive SQL read", rows, err)
				}
			} else if err == nil || rows != nil {
				t.Fatal("partial SQL error escaped", rows, err)
			}
			if !textInSet(mode, "query error", "cancelled before", "cancelled query") && !c.rowsClosed.Load() {
				t.Fatal("rows not closed")
			}
		})
	}
}

func TestClusterAutomaticRecoveryDatabaseRenderedPolicy(t *testing.T) {
	// Invoke actual repository renderer, then extract its fixed policy without
	// introducing a second YAML dependency into this Go module.
	source := `{"apiVersion":"apps/v1","kind":"StatefulSet","metadata":{"namespace":"borealis","name":"postgres-db","labels":{"app.kubernetes.io/name":"postgres-db","app.kubernetes.io/part-of":"borealis","app.kubernetes.io/managed-by":"Engine.sh"}},"spec":{"template":{"spec":{"containers":[{"name":"postgres-db","resources":{"requests":{"cpu":"100m","memory":"128Mi"},"limits":{"cpu":"1","memory":"512Mi"}}}]}}}}`
	for _, count := range []string{"1", "3"} {
		t.Run(count, func(t *testing.T) {
			cmd := exec.Command("python3", "../../../../K3s/cluster/render-postgres.py", "../../../../K3s/cluster/postgres.yaml", count)
			cmd.Stdin = strings.NewReader(source)
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(raw))
			}
			b, objects, _ := automaticRecoveryDatabaseFixture()
			cluster := objects[clusterSSHStoragePostgresPath]
			spec := clusterSSHStorageMap(cluster, "spec")
			postgres := clusterSSHStorageMap(spec, "postgresql")
			policy := map[string]any{}
			for _, line := range strings.Split(string(raw), "\n") {
				if !strings.HasPrefix(line, "      ") || strings.HasPrefix(line, "       ") {
					continue
				}
				key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
				if !ok {
					continue
				}
				switch key {
				case "method", "dataDurability":
					policy[key] = value
				case "number":
					if value == "1" {
						policy[key] = 1
					}
				case "synchronous_commit":
					postgres["parameters"] = map[string]any{"synchronous_commit": strings.Trim(value, `"`)}
				}
			}
			postgres["synchronous"] = policy
			rawCluster, _ := json.Marshal(cluster)
			decoded, _, decodeErr := clusterSSHStorageObject(rawCluster)
			if decodeErr != nil || recoveryDatabaseCluster(decoded, b) != (count == "3") {
				t.Fatal("health policy diverged from canonical renderer", string(raw))
			}
		})
	}
}

func TestClusterAutomaticRecoveryDatabaseHistory(t *testing.T) {
	for _, mode := range []string{"first sample", "both advance", "one advances", "initial unchanged", "unchanged within bound", "unchanged boundary", "unchanged expired", "gap", "local regression", "reply regression", "future stale", "future advancing", "past advancing", "second peer later", "current time cannot refresh", "failed authority", "cancelled", "concurrent", "recreated", "session changed", "session missing", "slow observation"} {
		t.Run(mode, func(t *testing.T) {
			b, _, rows := automaticRecoveryDatabaseFixture()
			start := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)
			if strings.HasPrefix(mode, "future") {
				for i := range rows {
					rows[i].Reply.Time = start.Add(24 * time.Hour)
					if mode == "future stale" {
						rows[i].Reply.Time = start.Add(30 * time.Second)
					}
				}
			}
			if mode == "past advancing" {
				for i := range rows {
					rows[i].Reply.Time = start.Add(-24 * time.Hour)
				}
			}
			session := newClusterUUID()
			var state clusterAutomaticRecoveryDatabaseContinuity
			observe := func(at time.Time, valid bool) bool {
				if !state.begin(session) {
					t.Fatal("unexpected busy state")
				}
				return state.finish(b, rows, at, at, valid)
			}
			if observe(start, true) {
				t.Fatal("first sample became healthy")
			}
			if mode == "first sample" {
				return
			}
			if mode == "initial unchanged" || mode == "future stale" {
				for i := 1; i <= 6; i++ {
					if observe(start.Add(time.Duration(i)*10*time.Second), true) {
						t.Fatal("wall-clock aging created freshness")
					}
				}
				return
			}
			rows[0].Reply.Time = rows[0].Reply.Time.Add(time.Second)
			if mode != "one advances" && mode != "second peer later" {
				rows[1].Reply.Time = rows[1].Reply.Time.Add(time.Second)
			}
			healthy := observe(start.Add(10*time.Second), true)
			if mode == "one advances" {
				if healthy {
					t.Fatal("one peer supplied full freshness")
				}
				return
			}
			if mode == "second peer later" {
				if healthy {
					t.Fatal("missing peer progress accepted")
				}
				rows[1].Reply.Time = rows[1].Reply.Time.Add(time.Second)
				if !observe(start.Add(12*time.Second), true) {
					t.Fatal("bounded distinct progress lost")
				}
				return
			}
			if !healthy {
				t.Fatal("both original replicas advanced without health")
			}
			if textInSet(mode, "both advance", "future advancing", "past advancing") {
				return
			}
			at, valid, want := start.Add(14*time.Second), true, false
			switch mode {
			case "unchanged within bound":
				want = true
			case "unchanged boundary":
				at, want = start.Add(clusterControllerLeaseRenewalGrace), true
			case "unchanged expired", "current time cannot refresh":
				at = start.Add(clusterControllerLeaseRenewalGrace + time.Nanosecond)
			case "gap":
				at = start.Add(30 * time.Second)
				for i := range rows {
					rows[i].Reply.Time = rows[i].Reply.Time.Add(time.Second)
				}
			case "local regression":
				at = start.Add(9 * time.Second)
			case "reply regression":
				rows[1].Reply.Time = rows[1].Reply.Time.Add(-2 * time.Second)
			case "failed authority", "cancelled":
				valid = false
			case "concurrent":
				if !state.begin(session) || state.begin(session) {
					t.Fatal("concurrent acquisition accepted")
				}
				if state.finish(b, rows, at, at, true) {
					t.Fatal("concurrent invalidation retained health")
				}
				if observe(at, true) {
					t.Fatal("concurrent invalidation retained baseline")
				}
				return
			case "session changed":
				session = newClusterUUID()
			case "session missing":
				if state.begin("") {
					t.Fatal("missing session accepted")
				}
				if observe(at, true) {
					t.Fatal("missing session retained baseline")
				}
				return
			case "recreated":
				state = clusterAutomaticRecoveryDatabaseContinuity{}
			case "slow observation":
				if !state.begin(session) {
					t.Fatal("busy")
				}
				if state.finish(b, rows, at, at.Add(clusterControllerLeaseAcquireTimeout+time.Nanosecond), true) {
					t.Fatal("unbounded observation accepted")
				}
				return
			}
			if got := observe(at, valid); got != want {
				t.Fatal("history decision", got, want)
			}
			if !valid && observe(at.Add(time.Second), true) {
				t.Fatal("failed observation retained baseline")
			}
		})
	}
}
