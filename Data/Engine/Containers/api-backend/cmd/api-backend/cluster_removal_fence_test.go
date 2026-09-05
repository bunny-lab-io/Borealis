package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func removalFenceFixture() (clusterControllerOperation, clusterControllerNode, clusterRemovalFence) {
	node := clusterControllerNode{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", ApplicationState: "drained"}
	operation := clusterControllerOperation{
		ID: "11111111-1111-4111-8111-111111111111", Kind: "node_remove", State: "running", Attempt: 1, TargetNodeID: node.ID,
		CurrentStep: "node:" + node.ID + ":prepare_member_removal",
		Payload: map[string]any{"action_image": "borealis-engine/api-backend:sha-1234567890ab", "target_size": 1,
			"paired_node_id": "33333333-3333-4333-8333-333333333333", "removal_node_ids": []any{node.ID, "33333333-3333-4333-8333-333333333333"}},
	}
	fence := clusterRemovalFence{OperationID: operation.ID, NodeID: node.ID, NodeName: node.Name,
		NodeUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EtcdMemberName: "engine-2-id", ActionAttempt: 1,
		ActionImage: cleanText(operation.Payload["action_image"]), AcknowledgedAt: 1}
	return operation, node, fence
}

func removalFenceNode(fence clusterRemovalFence, ready string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": fence.NodeName, "uid": fence.NodeUID, "resourceVersion": "10",
			"annotations": map[string]any{clusterK3sEtcdNodeNameAnnotation: fence.EtcdMemberName}},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": ready}}},
	}
}

func TestPlannedRemovalRejectsPartitionWithoutFence(t *testing.T) {
	for _, ready := range []string{"Unknown", "False", "missing", "True"} {
		for _, action := range []string{"transfer_roles", "enter_drain", "prepare_member_removal", "remove_etcd_membership", "wait_member_fenced", "delete_member_node", "verify_member_removed"} {
			if ready == "True" && textInSet(action, "transfer_roles", "enter_drain", "prepare_member_removal") {
				continue
			}
			t.Run(ready+"/"+action, func(t *testing.T) {
				operation, node, fence := removalFenceFixture()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodGet || request.URL.Path != "/api/v1/nodes/"+node.Name {
						t.Errorf("unfenced target received mutation or Job: %s %s", request.Method, request.URL.Path)
						http.Error(w, "unexpected mutation", 500)
						return
					}
					if ready == "missing" {
						http.NotFound(w, request)
						return
					}
					_ = json.NewEncoder(w).Encode(removalFenceNode(fence, ready))
				}))
				defer server.Close()
				runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}}
				err := runner.Run(context.Background(), operation, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID}, []clusterControllerNode{node})
				if err == nil || !strings.Contains(err.Error(), "fence acknowledgement") {
					t.Fatalf("unsafe action accepted: %v", err)
				}
			})
		}
	}
}

func TestRemovalFenceRetryRequiresMatchingIdentity(t *testing.T) {
	for _, changed := range []string{"operation", "target", "hostname", "uid", "etcd", "stale_confirmation", "intent_only"} {
		t.Run(changed, func(t *testing.T) {
			operation, node, fence := removalFenceFixture()
			observed := removalFenceNode(fence, "Unknown")
			switch changed {
			case "operation":
				fence.OperationID = "44444444-4444-4444-8444-444444444444"
			case "target":
				fence.NodeID = "44444444-4444-4444-8444-444444444444"
			case "hostname":
				fence.NodeName = "engine-other"
			case "uid":
				observed["metadata"].(map[string]any)["uid"] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			case "etcd":
				observed["metadata"].(map[string]any)["annotations"].(map[string]any)[clusterK3sEtcdNodeNameAnnotation] = "engine-2-rejoined"
			case "stale_confirmation":
				observed["metadata"].(map[string]any)["annotations"].(map[string]any)[clusterK3sEtcdRemovedNameAnnotation] = "engine-2-old"
			case "intent_only":
				fence.AcknowledgedAt = 0
			}
			operation.Payload["removal_fences"] = map[string]any{node.ID: fence}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					t.Errorf("mismatched fence mutated Kubernetes: %s", request.Method)
				}
				if strings.Contains(request.URL.Path, "/jobs/") {
					http.NotFound(w, request)
					return
				}
				_ = json.NewEncoder(w).Encode(observed)
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
			for _, action := range []string{"enter_drain", "remove_etcd_membership", "delete_member_node"} {
				if err := runner.Run(context.Background(), operation, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID}, []clusterControllerNode{node}); err == nil {
					t.Fatalf("accepted %s with %s evidence", action, changed)
				}
			}
		})
	}
}

func TestRemovalFenceAcknowledgementSurvivesMissingNodeAndRetry(t *testing.T) {
	operation, node, fence := removalFenceFixture()
	setRemovalFenceRecord(operation, fence)
	operation.Attempt = 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/nodes/"+node.Name {
			t.Errorf("acknowledged missing target received action: %s %s", request.Method, request.URL.Path)
		}
		http.NotFound(w, request)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
	for _, action := range []string{"transfer_roles", "enter_drain", "prepare_member_removal", "remove_etcd_membership", "delete_member_node"} {
		if err := runner.Run(context.Background(), operation, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID}, []clusterControllerNode{node}); err != nil {
			t.Fatalf("matching retry %s failed: %v", action, err)
		}
	}
}

func TestRemovalPreflightChecksAllTargetsBeforeStorageShortcut(t *testing.T) {
	operation, node, fence := removalFenceFixture()
	setRemovalFenceRecord(operation, fence)
	operation.Attempt = 2
	other := clusterControllerNode{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/nodes/"+node.Name {
			_ = json.NewEncoder(w).Encode(removalFenceNode(fence, "Unknown"))
			return
		}
		http.NotFound(w, request)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
	if allowed, err := runner.removalRetryHasFencedTarget(context.Background(), operation, []clusterControllerNode{node, other}); err == nil || allowed {
		t.Fatalf("storage shortcut accepted unfenced second target: allowed=%v err=%v", allowed, err)
	}
	if err := runner.Run(context.Background(), operation, clusterControllerStep{Name: "preflight"}, []clusterControllerNode{node, other}); err == nil {
		t.Fatal("retry preflight accepted unfenced missing second target")
	}
}

func TestRemovalFenceRecoversInterruptedAcknowledgementFromExactJob(t *testing.T) {
	operation, node, expected := removalFenceFixture()
	var persisted clusterRemovalFence
	var job map[string]any
	creates, commits := 0, 0
	ready := "True"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/nodes/"+node.Name {
			_ = json.NewEncoder(w).Encode(removalFenceNode(expected, ready))
			return
		}
		if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/jobs") {
			if persisted.NodeUID != expected.NodeUID || persisted.AcknowledgedAt != 0 {
				t.Error("fence action preceded durable identity intent")
			}
			creates++
			_ = json.NewDecoder(request.Body).Decode(&job)
			job["status"] = map[string]any{"succeeded": 1}
		}
		if job == nil {
			http.NotFound(w, request)
			return
		}
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer server.Close()
	commit := func(_ context.Context, _ clusterControllerOperation, record clusterRemovalFence) error {
		commits++
		if record.AcknowledgedAt > 0 && commits == 2 {
			return errors.New("controller interrupted before acknowledgement commit")
		}
		persisted = record
		return nil
	}
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}, namespace: "borealis", persistRemovalFence: commit}
	if err := runner.prepareRemovalFence(context.Background(), operation, node); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("expected interrupted commit, got %v", err)
	}
	if persisted.AcknowledgedAt != 0 || creates != 1 {
		t.Fatalf("unexpected pre-restart proof=%#v Jobs=%d", persisted, creates)
	}
	// Reload persisted JSON as a new controller/attempt, with target now partitioned.
	operation.Payload = parseClusterJSON(marshalClusterJSON(operation.Payload))
	setRemovalFenceRecord(operation, persisted)
	operation.Attempt = 2
	operation.CurrentStep = "preflight"
	ready = "Unknown"
	restarted := &kubernetesClusterStepRunner{kube: runner.kube, namespace: "borealis", persistRemovalFence: commit}
	fence, _, err := restarted.removalFenceStatus(context.Background(), operation, node)
	if err != nil || fence.AcknowledgedAt == 0 || persisted.AcknowledgedAt == 0 || creates != 1 {
		t.Fatalf("restart failed to recover exact acknowledgement: fence=%#v creates=%d err=%v", fence, creates, err)
	}
	// Subsequent restart uses PostgreSQL evidence even after Job TTL cleanup.
	job = nil
	if err := restarted.prepareRemovalFence(context.Background(), operation, node); err != nil || creates != 1 {
		t.Fatalf("durable acknowledgement replay failed: %v", err)
	}
}

func TestRemovalFenceRejectsNodeReplacementBeforeDelete(t *testing.T) {
	_, node, fence := removalFenceFixture()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("replacement node received delete: %s", request.Method)
		}
		observed := removalFenceNode(fence, "Unknown")
		observed["metadata"].(map[string]any)["uid"] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		_ = json.NewEncoder(w).Encode(observed)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
	if err := runner.deleteNodeResourceWithFence(context.Background(), node.Name, &fence); err == nil {
		t.Fatal("deleted replacement node using new UID")
	}
}

func TestRemovalDeleteCarriesFenceUIDAndResourceVersion(t *testing.T) {
	_, node, fence := removalFenceFixture()
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(removalFenceNode(fence, "Unknown"))
			return
		}
		if request.Method != http.MethodDelete {
			t.Errorf("unexpected request %s", request.Method)
		}
		var options map[string]any
		_ = json.NewDecoder(request.Body).Decode(&options)
		preconditions := nestedMap(options, "preconditions")
		if cleanText(preconditions["uid"]) != fence.NodeUID || cleanText(preconditions["resourceVersion"]) != "10" {
			t.Errorf("delete lost fence preconditions: %#v", options)
		}
		deleted = true
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
	if err := runner.deleteNodeResourceWithFence(context.Background(), node.Name, &fence); err != nil || !deleted {
		t.Fatalf("guarded delete failed: %v", err)
	}
}

func TestRemovalRejectsSuccessfulJobForDifferentOperation(t *testing.T) {
	operation, node, fence := removalFenceFixture()
	fence.AcknowledgedAt = 0
	setRemovalFenceRecord(operation, fence)
	action, step := removalFenceAction(operation, fence)
	jobName := clusterActionJobName(operation.ID, "attempt:1:"+step.Name)
	job := clusterActionJobManifest(jobName, "borealis", node.Name, fence.ActionImage,
		append(clusterNodeActionArgs(action, "PrepareMemberRemoval"), removalFenceArgs(fence)...), "44444444-4444-4444-8444-444444444444", step.Name)
	job["status"] = map[string]any{"succeeded": 1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/nodes/"+node.Name {
			_ = json.NewEncoder(w).Encode(removalFenceNode(fence, "Unknown"))
		} else {
			_ = json.NewEncoder(w).Encode(job)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}, namespace: "borealis",
		persistRemovalFence: func(context.Context, clusterControllerOperation, clusterRemovalFence) error {
			t.Error("stale successful Job gained durable acknowledgement")
			return nil
		}}
	if _, _, err := runner.removalFenceStatus(context.Background(), operation, node); err == nil {
		t.Fatal("successful stale-operation Job accepted")
	}
}

func TestClusterRemovalFencePostgresPersistsAndFencesStaleWriters(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	operation, node, fence := removalFenceFixture()
	const holder = "removal-fence-test"
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.realtime_outbox WHERE payload_json LIKE '%' || $1 || '%'`, operation.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operation.ID)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1 AND active_operation_id=$1`, operation.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operation.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_nodes WHERE id=$1`, node.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder IN ($2,$3)`, clusterControllerLeaseName, holder, holder+"-new")
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',3,3,'inactive',$1,'{}',$2,$2) ON CONFLICT(id) DO UPDATE SET enabled=1,status='Healthy',active_size=3,desired_size=3,hmr_state='inactive',active_operation_id=$1,config_json='{}'`, []any{operation.ID, now}},
		{`INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,roles_json,probe_health_json,created_at,updated_at)
		VALUES($1,$2,$2,'192.168.50.22','amd64','Ubuntu 24.04','Active','drained','{}','{}',$3,$3)`, []any{node.ID, node.Name, now}},
		{`INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,attempt,created_at,updated_at)
		VALUES($1,'node_remove','running',$2,$3,'fence-test',$4,1,$5,$5)`, []any{operation.ID, operation.CurrentStep, node.ID, marshalClusterJSON(operation.Payload), now}},
		{`INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)
		ON CONFLICT(name) DO UPDATE SET holder=$2,expires_at=$3,updated_at=$4`, []any{clusterControllerLeaseName, holder, now + 60, now}},
	} {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	controller := &clusterController{store: store, holder: holder, now: time.Now}
	if err := controller.persistRemovalFence(ctx, operation, fence); err == nil {
		t.Fatal("acknowledgement accepted without durable intent")
	}
	fence.AcknowledgedAt = 0
	if err := controller.persistRemovalFence(ctx, operation, fence); err != nil {
		t.Fatal(err)
	}
	fence.AcknowledgedAt = now
	if err := controller.persistRemovalFence(ctx, operation, fence); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, operation.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	operation.Payload = parseClusterJSON(payload)
	recorded, exists, err := removalFenceRecord(operation, node)
	if err != nil || !exists || recorded != fence || cleanText(operation.Payload["paired_node_id"]) == "" {
		t.Fatalf("durable proof or unrelated payload lost: %#v, %v", operation.Payload, err)
	}
	changed := fence
	changed.NodeUID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := controller.persistRemovalFence(ctx, operation, changed); err == nil {
		t.Fatal("acknowledged target identity overwritten")
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_operations SET attempt=2,current_step='preflight' WHERE id=$1`, operation.ID); err != nil {
		t.Fatal(err)
	}
	if err := controller.persistRemovalFence(ctx, operation, fence); err == nil {
		t.Fatal("stale operation attempt persisted proof")
	}
	operation.Attempt, operation.CurrentStep = 2, "preflight"
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`, holder+"-new", clusterControllerLeaseName); err != nil {
		t.Fatal(err)
	}
	if err := controller.persistRemovalFence(ctx, operation, fence); !errors.Is(err, errClusterControllerLeaseLost) {
		t.Fatalf("stale controller accepted: %v", err)
	}
	restarted := &clusterController{store: store, holder: holder + "-new", now: time.Now}
	if err := restarted.persistRemovalFence(ctx, operation, fence); err != nil {
		t.Fatalf("new holder failed idempotent proof replay: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=$1 WHERE name=$2`, now-1, clusterControllerLeaseName); err != nil {
		t.Fatal(err)
	}
	if err := restarted.persistRemovalFence(ctx, operation, fence); !errors.Is(err, errClusterControllerLeaseLost) {
		t.Fatalf("expired holder accepted: %v", err)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("fence store retained connection after returning")
	}
}
