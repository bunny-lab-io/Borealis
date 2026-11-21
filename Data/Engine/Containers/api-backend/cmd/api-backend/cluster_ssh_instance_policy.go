package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	_ "embed"
	"encoding/json"
	"maps"
	"reflect"
	"regexp"
	"strings"
)

const clusterSSHInstancePolicyName = "borealis-longhorn-instance-manager-bounds"
const clusterSSHInstancePolicyPath = "/apis/admissionregistration.k8s.io/v1/mutatingadmissionpolicies/" + clusterSSHInstancePolicyName
const clusterSSHInstanceBindingPath = "/apis/admissionregistration.k8s.io/v1/mutatingadmissionpolicybindings/" + clusterSSHInstancePolicyName

// Generated from the canonical Python producer; parity test prevents independent
// CEL contracts. Placeholders are substituted only after strict quantity parsing.
//
//go:embed cluster_ssh_instance_policy_template.json
var clusterSSHInstancePolicyTemplate string

var clusterSSHInstancePatchBounds = regexp.MustCompile(`value: ("[^"\n]+")},\n  JSONPatch\{op: "add", path: "/spec/containers/0/resources/limits", value: (\{"cpu": "[^"\n]+", "memory": "[^"\n]+"\})\}`)

func clusterSSHInstanceExpectedPolicy(memory, cpu, cap string) ([]map[string]any, bool) {
	raw := strings.NewReplacer("@MEMORY_REQUEST@", memory, "@CPU_LIMIT@", cpu, "@MEMORY_LIMIT@", cap).Replace(clusterSSHInstancePolicyTemplate)
	var bundle struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &bundle) != nil || len(bundle.Items) != 2 {
		return nil, false
	}
	return bundle.Items, true
}

func observeClusterSSHInstancePolicy(read func(string) (map[string]any, error)) (clusterSSHApplicationResource, error) {
	fail := func() (clusterSSHApplicationResource, error) {
		return clusterSSHApplicationResource{}, clusterbootstrap.ErrPreparationConfig
	}
	policy, err := read(clusterSSHInstancePolicyPath)
	id, ok := clusterSSHStorageMetadata(policy, "admissionregistration.k8s.io/v1", "MutatingAdmissionPolicy", "")
	if err != nil || !ok || id.Name != clusterSSHInstancePolicyName {
		return fail()
	}
	spec := clusterSSHStorageMap(policy, "spec")
	mutations, ok := spec["mutations"].([]any)
	if !ok || len(mutations) != 1 {
		return fail()
	}
	mutation, ok := mutations[0].(map[string]any)
	if !ok {
		return fail()
	}
	expression := clusterSSHStorageText(clusterSSHStorageMap(mutation, "jsonPatch"), "expression")
	matches := clusterSSHInstancePatchBounds.FindStringSubmatch(expression)
	if len(matches) != 3 {
		return fail()
	}
	var memory string
	var limits map[string]any
	if json.Unmarshal([]byte(matches[1]), &memory) != nil || json.Unmarshal([]byte(matches[2]), &limits) != nil {
		return fail()
	}
	bounds, ok := clusterSSHWorkloadResources("instance-manager", map[string]any{"requests": map[string]any{"cpu": "1m", "memory": memory}, "limits": limits})
	if !ok {
		return fail()
	}
	expected, ok := clusterSSHInstanceExpectedPolicy(memory, clusterSSHStorageText(limits, "cpu"), clusterSSHStorageText(limits, "memory"))
	if !ok || !reflect.DeepEqual(spec, expected[0]["spec"]) {
		return fail()
	}
	binding, err := read(clusterSSHInstanceBindingPath)
	id, ok = clusterSSHStorageMetadata(binding, "admissionregistration.k8s.io/v1", "MutatingAdmissionPolicyBinding", "")
	if err != nil || !ok || id.Name != clusterSSHInstancePolicyName {
		return fail()
	}
	// API defaulting sets Equivalent on the binding. It has no resource rules;
	// policy's explicit Exact CREATE rules remain authoritative. Never mutate
	// observed objects: their complete receipts must bind even defaulted fields.
	actual := maps.Clone(clusterSSHStorageMap(binding, "spec"))
	match := maps.Clone(clusterSSHStorageMap(actual, "matchResources"))
	if match["matchPolicy"] == "Equivalent" {
		delete(match, "matchPolicy")
	}
	actual["matchResources"] = match
	if !reflect.DeepEqual(actual, expected[1]["spec"]) {
		return fail()
	}
	bounds.CPURequestMilli = 0 // synthetic probe request is never source sizing data.
	return bounds, nil
}
