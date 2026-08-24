package main

import "testing"

func TestNodeHealthParsersRequireReadyNodeAndWorkloads(t *testing.T) {
	if !nodeReady([]byte(`{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`)) {
		t.Fatal("expected Ready=True node")
	}
	if nodeReady([]byte(`{"status":{"conditions":[{"type":"Ready","status":"False"}]}}`)) {
		t.Fatal("expected Ready=False node rejection")
	}
	if !nodeLabelTrue([]byte(`{"metadata":{"labels":{"borealis.io/edge-eligible":"true"}}}`), "borealis.io/edge-eligible") {
		t.Fatal("expected edge eligibility label")
	}
	workloads, err := readyNodeWorkloads([]byte(`{"items":[{"metadata":{"labels":{"app.kubernetes.io/name":"api-backend"}},"spec":{"replicas":1},"status":{"availableReplicas":1,"readyReplicas":1,"updatedReplicas":1}},{"metadata":{"labels":{"app.kubernetes.io/name":"job-scheduler"}},"spec":{"replicas":1},"status":{"availableReplicas":0,"readyReplicas":0,"updatedReplicas":1}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !workloads["api-backend"] || workloads["job-scheduler"] {
		t.Fatalf("unexpected workload readiness: %#v", workloads)
	}
}

func TestNodeHealthParsersRequireNodeScopedReadyEndpoint(t *testing.T) {
	host, port, err := readyAPIEndpoint([]byte(`{"items":[{"ports":[{"port":5001}],"endpoints":[{"addresses":["10.42.1.8"],"nodeName":"engine-1","conditions":{"ready":true}},{"addresses":["10.42.2.8"],"nodeName":"engine-2","conditions":{"ready":false}}]}]}`), "engine-1")
	if err != nil || host != "10.42.1.8" || port != 5001 {
		t.Fatalf("unexpected endpoint result host=%q port=%d err=%v", host, port, err)
	}
	if _, _, err := readyAPIEndpoint([]byte(`{"items":[]}`), "engine-1"); err == nil {
		t.Fatal("expected missing endpoint rejection")
	}
}

func TestNodeHealthParsersRequireReadyPostgresAndValidService(t *testing.T) {
	if !podListHasReadyPod([]byte(`{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`)) {
		t.Fatal("expected ready PostgreSQL pod")
	}
	if podListHasReadyPod([]byte(`{"items":[]}`)) {
		t.Fatal("expected empty PostgreSQL list rejection")
	}
	host, port, err := apiServiceAddress([]byte(`{"spec":{"clusterIP":"10.43.0.8","ports":[{"port":5001}]}}`))
	if err != nil || host != "10.43.0.8" || port != 5001 {
		t.Fatalf("unexpected service result host=%q port=%d err=%v", host, port, err)
	}
}

func TestSupportedUbuntuReleaseRequiresUbuntu2404OrNewer(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "ubuntu 24.04", content: "ID=ubuntu\nVERSION_ID=\"24.04\"\n", want: true},
		{name: "ubuntu 26.04", content: "ID='ubuntu'\nVERSION_ID='26.04'\n", want: true},
		{name: "ubuntu 22.04", content: "ID=ubuntu\nVERSION_ID=22.04\n", want: false},
		{name: "debian", content: "ID=debian\nVERSION_ID=24\n", want: false},
		{name: "missing version", content: "ID=ubuntu\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := supportedUbuntuRelease([]byte(test.content)); got != test.want {
				t.Fatalf("supportedUbuntuRelease()=%v want %v", got, test.want)
			}
		})
	}
}
