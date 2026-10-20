// Package admissionpolicy exercises emitted policy with Kubernetes 1.36's real
// compiler, request matchers and JSON patcher. No API server or network is used.
package admissionpolicy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/admission/plugin/policy/matching"
	"k8s.io/apiserver/pkg/admission/plugin/policy/mutating/patch"
	"k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	"k8s.io/apiserver/pkg/admission/plugin/webhook/matchconditions"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/cel/environment"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

const producer = "system:serviceaccount:longhorn-system:longhorn-service-account"

// Adapt the generated API MatchResources to Kubernetes' matcher interface.
type criteria struct{ admissionv1.MatchResources }

func (c criteria) GetMatchResources() admissionv1.MatchResources { return c.MatchResources }
func (c criteria) GetParsedNamespaceSelector() (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(c.NamespaceSelector)
}
func (c criteria) GetParsedObjectSelector() (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(c.ObjectSelector)
}

type evaluator struct {
	configPath string
	compiler   *plugincel.CompositedCompiler
	policy     admissionv1.MutatingAdmissionPolicy
	binding    admissionv1.MutatingAdmissionPolicyBinding
	patcher    patch.Patcher
	conditions matchconditions.Matcher
	matcher    *matching.Matcher
	objects    admission.ObjectInterfaces
}

func newEvaluator(t *testing.T) *evaluator {
	t.Helper()
	config := filepath.Join(t.TempDir(), "bounds.json")
	if err := os.WriteFile(config, []byte(`{"requests":{"memory":"64Mi"},"limits":{"cpu":"1.5","memory":"128Mi"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("python3", "../../../Data/Engine/K3s/cluster/render-longhorn-instance-manager.py", "policy", config).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v: %s", err, output)
	}
	var bundle struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(output, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Items) != 2 {
		t.Fatalf("expected policy and binding, got %d", len(bundle.Items))
	}
	e := &evaluator{configPath: config}
	if err := json.Unmarshal(bundle.Items[0], &e.policy); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bundle.Items[1], &e.binding); err != nil {
		t.Fatal(err)
	}
	if e.binding.Spec.PolicyName != e.policy.Name || e.policy.Spec.FailurePolicy == nil || *e.policy.Spec.FailurePolicy != admissionv1.Fail || e.policy.Spec.ReinvocationPolicy != admissionv1.IfNeededReinvocationPolicy {
		t.Fatal("binding/failure/reinvocation contract changed")
	}
	// Same compiler wiring as apiserver policy/mutating/compilation.go.
	compiler, err := plugincel.NewCompositedCompiler(environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()))
	if err != nil {
		t.Fatal(err)
	}
	e.compiler = compiler
	opts := plugincel.OptionalVariableDeclarations{HasAuthorizer: true}
	vars := []plugincel.NamedExpressionAccessor{}
	for _, v := range e.policy.Spec.Variables {
		vars = append(vars, &validating.Variable{Name: v.Name, Expression: v.Expression})
	}
	compiler.CompileAndStoreVariables(vars, opts, environment.StoredExpressions)
	conditions := []plugincel.ExpressionAccessor{}
	for _, c := range e.policy.Spec.MatchConditions {
		conditions = append(conditions, &matchconditions.MatchCondition{Name: c.Name, Expression: c.Expression})
	}
	e.conditions = matchconditions.NewMatcher(compiler.CompileCondition(conditions, opts, environment.StoredExpressions), e.policy.Spec.FailurePolicy, "policy", "validate", e.policy.Name)
	if len(e.policy.Spec.Mutations) != 1 || e.policy.Spec.Mutations[0].JSONPatch == nil {
		t.Fatal("expected one JSON patch mutation")
	}
	opts.HasPatchTypes = true
	compiled := compiler.CompileMutatingEvaluator(&patch.JSONPatchCondition{Expression: e.policy.Spec.Mutations[0].JSONPatch.Expression}, opts, environment.StoredExpressions)
	if errs := compiled.CompilationErrors(); len(errs) != 0 {
		t.Fatalf("CEL compilation: %v", errs)
	}
	e.patcher = patch.NewJSONPatcher(compiled)
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	e.objects = admission.NewObjectInterfacesFromScheme(scheme)
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, name := range []string{"longhorn-system", "other"} {
		if err := indexer.Add(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"kubernetes.io/metadata.name": name}}}); err != nil {
			t.Fatal(err)
		}
	}
	// All test namespaces are cached: no client or live API fallback is provided.
	e.matcher = matching.NewMatcher(corelisters.NewNamespaceLister(indexer), nil)
	return e
}

func (e *evaluator) apply(pod *corev1.Pod, actor string, operation admission.Operation) (*corev1.Pod, bool, error) {
	ctx := e.compiler.CreateContext(context.Background())
	gvk, gvr := corev1.SchemeGroupVersion.WithKind("Pod"), corev1.SchemeGroupVersion.WithResource("pods")
	attrs := admission.NewAttributesRecord(pod, nil, gvk, pod.Namespace, pod.Name, gvr, "", operation, &metav1.CreateOptions{}, false, &user.DefaultInfo{Name: actor})
	for _, match := range []*admissionv1.MatchResources{e.policy.Spec.MatchConstraints, e.binding.Spec.MatchResources} {
		matches, _, _, err := e.matcher.Matches(attrs, e.objects, criteria{*match})
		if err != nil || !matches {
			return pod, false, err
		}
	}
	versioned := &admission.VersionedAttributes{Attributes: attrs, VersionedKind: gvk, VersionedObject: pod}
	result := e.conditions.Match(ctx, versioned, nil, nil)
	if result.Error != nil || !result.Matches {
		return pod, false, result.Error
	}
	ns, err := e.matcher.GetNamespace(ctx, pod.Namespace)
	if err != nil {
		return nil, true, err
	}
	output, err := e.patcher.Patch(ctx, patch.Request{MatchedResource: gvr, VersionedAttributes: versioned, ObjectInterfaces: e.objects, Namespace: ns}, celconfig.RuntimeCELCostBudget)
	if err != nil {
		return nil, true, err
	}
	return output.(*corev1.Pod), true, nil
}

func nativePod(cpu string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "instance-manager-example", Namespace: "longhorn-system", Labels: map[string]string{"longhorn.io/component": "instance-manager", "longhorn.io/data-engine": "v1", "longhorn.io/instance-manager-type": "aio"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "longhorn.io/v1beta2", Kind: "InstanceManager", Name: "instance-manager-example", UID: "synthetic-owner"}}}, Spec: corev1.PodSpec{ServiceAccountName: "longhorn-service-account", NodeName: "synthetic-node", Containers: []corev1.Container{{Name: "instance-manager", Image: "synthetic-image", Args: []string{"daemon"}, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}}}}}}
}

func TestInstanceManagerBounds(t *testing.T) {
	e := newEvaluator(t)
	for _, cpu := range []string{"1m", "1200m", "1", "1500m"} {
		t.Run(cpu, func(t *testing.T) {
			original := nativePod(cpu)
			before := original.DeepCopy()
			want := original.DeepCopy()
			want.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("64Mi")
			want.Spec.Containers[0].Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m"), corev1.ResourceMemory: resource.MustParse("128Mi")}
			got, matched, err := e.apply(original, producer, admission.Create)
			if err != nil || !matched {
				t.Fatalf("mutation matched=%v: %v", matched, err)
			}
			if !equality.Semantic.DeepEqual(want, got) || !equality.Semantic.DeepEqual(before, original) {
				t.Fatal("wrong bounds, unrelated field changed, or input mutated")
			}
			// Typed API roundtrip normalizes 1.5 to 1500m; second admission must stay valid.
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var normalized corev1.Pod
			if err := json.Unmarshal(raw, &normalized); err != nil {
				t.Fatal(err)
			}
			again, matched, err := e.apply(&normalized, producer, admission.Create)
			if err != nil || !matched || !equality.Semantic.DeepEqual(want, again) {
				t.Fatalf("reinvocation failed: matched=%v err=%v", matched, err)
			}
		})
	}
	t.Run("equivalent quantities", func(t *testing.T) {
		pod := nativePod("1.2")
		pod.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("67108864")
		pod.Spec.Containers[0].Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1.5"), corev1.ResourceMemory: resource.MustParse("134217728")}
		got, matched, err := e.apply(pod, producer, admission.Create)
		if err != nil || !matched || !equality.Semantic.DeepEqual(pod, got) {
			t.Fatalf("equivalent quantities rejected: %v", err)
		}
	})
}

func TestInstanceManagerRejectsUnsupported(t *testing.T) {
	e := newEvaluator(t)
	cases := map[string]func(*corev1.Pod){
		"over CPU cap": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("1501m")
		},
		"zero CPU": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("0")
		},
		"missing CPU":          func(p *corev1.Pod) { delete(p.Spec.Containers[0].Resources.Requests, corev1.ResourceCPU) },
		"missing resources":    func(p *corev1.Pod) { p.Spec.Containers[0].Resources = corev1.ResourceRequirements{} },
		"V2":                   func(p *corev1.Pod) { p.Labels["longhorn.io/data-engine"] = "v2" },
		"missing engine label": func(p *corev1.Pod) { delete(p.Labels, "longhorn.io/data-engine") },
		"wrong manager type":   func(p *corev1.Pod) { p.Labels["longhorn.io/instance-manager-type"] = "engine" },
		"extra container":      func(p *corev1.Pod) { p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "sidecar"}) },
		"no containers":        func(p *corev1.Pod) { p.Spec.Containers = nil },
		"wrong container":      func(p *corev1.Pod) { p.Spec.Containers[0].Name = "other" },
		"init container":       func(p *corev1.Pod) { p.Spec.InitContainers = []corev1.Container{{Name: "init"}} },
		"ephemeral container": func(p *corev1.Pod) {
			p.Spec.EphemeralContainers = []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}}
		},
		"wrong service account": func(p *corev1.Pod) { p.Spec.ServiceAccountName = "other" },
		"pod resources": func(p *corev1.Pod) {
			p.Spec.Resources = &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}
		},
		"overhead": func(p *corev1.Pod) {
			p.Spec.Overhead = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}
		},
		"resource claims": func(p *corev1.Pod) { p.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "claim"}} },
		"runtime class":   func(p *corev1.Pod) { s := "other"; p.Spec.RuntimeClassName = &s },
		"resize policy": func(p *corev1.Pod) {
			p.Spec.Containers[0].ResizePolicy = []corev1.ContainerResizePolicy{{ResourceName: corev1.ResourceCPU}}
		},
		"restart policy": func(p *corev1.Pod) { s := corev1.ContainerRestartPolicyAlways; p.Spec.Containers[0].RestartPolicy = &s },
		"extra request": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Requests[corev1.ResourceEphemeralStorage] = resource.MustParse("1Mi")
		},
		"wrong memory request": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("65Mi")
		},
		"partial limits": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m")}
		},
		"wrong CPU limit": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("128Mi")}
		},
		"wrong memory limit": func(p *corev1.Pod) {
			p.Spec.Containers[0].Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m"), corev1.ResourceMemory: resource.MustParse("129Mi")}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			pod := nativePod("1200m")
			change(pod)
			before := pod.DeepCopy()
			_, matched, err := e.apply(pod, producer, admission.Create)
			if !matched || err == nil {
				t.Fatalf("unsupported Pod escaped fail-closed mutation: matched=%v err=%v", matched, err)
			}
			if !equality.Semantic.DeepEqual(before, pod) {
				t.Fatal("failed mutation altered input")
			}
		})
	}
}

func TestInstanceManagerExcludesOtherRequests(t *testing.T) {
	e := newEvaluator(t)
	for _, name := range []string{"caller", "namespace", "role", "update"} {
		t.Run(name, func(t *testing.T) {
			pod := nativePod("1200m")
			actor := producer
			operation := admission.Create
			switch name {
			case "caller":
				actor = "system:serviceaccount:other:longhorn-service-account"
			case "namespace":
				pod.Namespace = "other"
			case "role":
				pod.Labels["longhorn.io/component"] = "manager"
			case "update":
				operation = admission.Update
			}
			before := pod.DeepCopy()
			got, matched, err := e.apply(pod, actor, operation)
			if err != nil || matched || !equality.Semantic.DeepEqual(before, got) {
				t.Fatalf("out-of-scope request changed: matched=%v err=%v", matched, err)
			}
		})
	}
}

func TestFailedJSONPatchTestDoesNotReject(t *testing.T) {
	// Regression proof: Kubernetes intentionally turns failed test operations into
	// successful no-ops. Never use one as the unsupported-shape rejection branch.
	e := newEvaluator(t)
	compiler, err := plugincel.NewCompositedCompiler(environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()))
	if err != nil {
		t.Fatal(err)
	}
	expression := `[JSONPatch{op:"test",path:"/spec/containers",value:[]}]`
	e.patcher = patch.NewJSONPatcher(compiler.CompileMutatingEvaluator(&patch.JSONPatchCondition{Expression: expression}, plugincel.OptionalVariableDeclarations{HasPatchTypes: true}, environment.StoredExpressions))
	pod := nativePod("1200m")
	got, matched, err := e.apply(pod, producer, admission.Create)
	if err != nil || !matched || !equality.Semantic.DeepEqual(pod, got) {
		t.Fatalf("upstream test semantics changed: %v", err)
	}
	// The actual policy must instead produce an evaluation error for false guard.
	e = newEvaluator(t)
	pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("2")
	_, _, err = e.apply(pod, producer, admission.Create)
	if err == nil || !strings.Contains(err.Error(), "no such key: unsupported instance-manager resource shape or bounds") {
		t.Fatalf("expected explicit guard error, got %v", err)
	}
}

func TestInstanceManagerProbe(t *testing.T) {
	e := newEvaluator(t)
	renderer := "../../../Data/Engine/K3s/cluster/render-longhorn-instance-manager.py"
	raw, err := exec.Command("python3", renderer, "probe", e.configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("render probe: %v: %s", err, raw)
	}
	verify := func(input []byte) error {
		command := exec.Command("python3", renderer, "verify-probe", e.configPath)
		command.Stdin = strings.NewReader(string(input))
		return command.Run()
	}
	if verify(raw) == nil {
		t.Fatal("unmutated probe accepted")
	}
	var pod corev1.Pod
	if err := json.Unmarshal(raw, &pod); err != nil {
		t.Fatal(err)
	}
	// API server resolves generateName before admission; no object is persisted.
	pod.Name = pod.GenerateName + "synthetic"
	got, matched, err := e.apply(&pod, producer, admission.Create)
	if err != nil || !matched {
		t.Fatalf("probe admission matched=%v: %v", matched, err)
	}
	admitted, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(admitted); err != nil {
		t.Fatalf("real evaluator output rejected by setup gate: %v", err)
	}
}
