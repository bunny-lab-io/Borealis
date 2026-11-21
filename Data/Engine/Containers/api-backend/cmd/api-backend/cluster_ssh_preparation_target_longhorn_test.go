package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClusterSSHPreparationTargetLonghornCPU(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	cpu := sshPreparationTargetCPUFixture(snapshot)[0].CPU
	for _, mode := range []string{"absent", "zero override", "target override", "source override cannot substitute", "missing override", "denied", "nil object", "wrong name", "over cap", "zero percentage", "rounding"} {
		t.Run(mode, func(t *testing.T) {
			policy := clusterSSHInstanceManagerResources{CPUPercentage: "12", CPULimitMilli: 2000}
			node := sshStorageObject("longhorn.io/v1beta2", "Node", "longhorn-system", "engine-02")
			node["spec"] = map[string]any{"instanceManagerCPURequest": json.Number("0")}
			readErr := error(nil)
			want := uint64(1920)
			switch mode {
			case "absent":
				node = nil
				readErr = errClusterSSHPreparationLonghornNodeAbsent
			case "target override":
				node["spec"] = map[string]any{"instanceManagerCPURequest": json.Number("900")}
				want = 900
			case "source override cannot substitute":
				clusterSSHStorageMap(node, "metadata")["name"] = "engine-01"
			case "missing override":
				node["spec"] = map[string]any{}
			case "denied":
				node = nil
				readErr = clusterbootstrap.ErrPreparationConfig
			case "nil object":
				node = nil
			case "wrong name":
				clusterSSHStorageMap(node, "metadata")["name"] = "other"
			case "over cap":
				policy.CPULimitMilli = 1919
			case "zero percentage":
				policy.CPUPercentage = "0"
			case "rounding":
				policy.CPUPercentage = "0.003125"
				want = 1
			}
			got, err := observeClusterSSHPreparationTargetInstanceCPU(func(path string) (map[string]any, error) {
				if path != clusterSSHInstanceNodePrefix+"engine-02" {
					t.Fatal("target read escaped fixed name")
				}
				return node, readErr
			}, "engine-02", cpu, policy)
			positive := mode == "absent" || mode == "zero override" || mode == "target override" || mode == "rounding"
			if (err == nil) != positive || positive && (got.RequestMilli != want || !validClusterSSHPreparationDigest(got.ObservationSHA256) || got.Present != (mode != "absent")) {
				t.Fatal("unexpected target CPU", got, err)
			}
			if !positive && got != (clusterSSHPreparationTargetInstanceCPU{}) {
				t.Fatal("failure exported partial evidence")
			}
		})
	}
}

func TestClusterSSHPreparationLonghornAbsenceTransport(t *testing.T) {
	const good = `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","details":{"name":"engine-02","group":"longhorn.io","kind":"nodes"},"code":404}`
	for _, mode := range []string{"absent", "denied", "proxy", "wrong group", "wrong name", "wrong kind", "wrong code", "duplicate key", "oversized", "unknown path"} {
		t.Run(mode, func(t *testing.T) {
			raw, status := good, 404
			switch mode {
			case "denied":
				status = 403
			case "proxy":
				raw = "<html>not found</html>"
			case "wrong group":
				raw = strings.Replace(raw, "longhorn.io", "foreign.io", 1)
			case "wrong name":
				raw = strings.Replace(raw, "engine-02", "engine-01", 1)
			case "wrong kind":
				raw = strings.Replace(raw, "nodes", "pods", 1)
			case "wrong code":
				raw = strings.Replace(raw, "404", "403", 1)
			case "duplicate key":
				raw = strings.Replace(raw, `"code":404`, `"code":404,"code":404`, 1)
			case "oversized":
				raw = strings.Repeat(" ", 4097) + raw
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic" {
					t.Fatal("changed transport authority")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(raw))
			}))
			defer server.Close()
			c := &kubernetesAPIClient{baseURL: server.URL, token: "synthetic", httpClient: server.Client()}
			path := clusterSSHInstanceNodePrefix + "engine-02"
			if mode == "unknown path" {
				path = "/api/v1/nodes/engine-02"
			}
			var out map[string]any
			err := c.getClusterSSHStorageJSON(context.Background(), path, &out)
			if errors.Is(err, errClusterSSHPreparationLonghornNodeAbsent) != (mode == "absent") || err == nil || out != nil {
				t.Fatal("unproved absence accepted", err)
			}
		})
	}
}
