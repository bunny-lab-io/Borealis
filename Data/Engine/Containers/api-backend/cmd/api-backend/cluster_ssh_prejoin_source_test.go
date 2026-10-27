package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sshPrejoinSourceFixture(t *testing.T, replacement bool) (clusterSSHPreparationAuthority, clusterSSHPrejoinSource, map[string]any) {
	t.Helper()
	cohort, members, lease, baseline := sshPreparationFixture(t)
	if replacement {
		m := cohort.Targets[1]
		members.Members = append(members.Members, clusterSSHSourceMember{NodeID: newClusterUUID(), NodeUID: newClusterUUID(), Name: m.Report.Hostname,
			Address: m.Binding.Address, MachineID: m.Report.MachineID, BootID: m.Report.BootID})
		members.ActiveSize, members.Status = 2, "Degraded Quorum"
		cohort.Targets = cohort.Targets[:1]
	}
	_, process, pod := existingIdentitySourceFixture(t)
	// Explicitly select the second survivor for replacement, not list head.
	m := members.Members[len(members.Members)-1]
	process.NodeName = m.Name
	meta := clusterSSHStorageMap(pod, "metadata")
	clusterSSHStorageMap(meta, "labels")["borealis.io/engine-node"] = m.Name
	clusterSSHStorageMap(meta, "annotations")["borealis.io/revision"] = baseline.SourceSHA
	clusterSSHStorageMap(pod, "spec")["nodeName"] = m.Name
	a := clusterSSHPreparationAuthority{Cohort: cohort, Source: members, Lease: lease, Baseline: baseline,
		K3sVersion: "v1.36.3+k3s1", PayloadSHA256: strings.Repeat("a", 64)}
	s := clusterSSHPrejoinSource{Binding: engineidentity.Binding{ClusterID: members.ClusterID, SourceID: m.NodeID, SourceUID: m.NodeUID}, Process: process}
	a.IdentitySource = &s.Binding
	if !s.valid(a) {
		t.Fatal("invalid fixture")
	}
	return a, s, pod
}

func TestClusterSSHPrejoinSourceAuthority(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "qualification", "recovery", "source target", "source replaced", "source zero", "source UID zero", "namespace zero", "peer zero", "pod zero", "node name", "controller", "attempt", "generation", "payload"} {
		t.Run(mode, func(t *testing.T) {
			a, s, _ := sshPrejoinSourceFixture(t, mode == "replacement" || mode == "peer zero")
			switch mode {
			case "qualification":
				a.Lease.OperationStep, a.Lease.Step = clusterSSHQualificationStep, "qualify"
			case "recovery":
				a.Recovery = &clusterSSHIdentityRecoveryAuthorityBinding{}
			case "source target":
				s.Binding.SourceID = a.Lease.TargetID
			case "source replaced":
				s.Binding.SourceUID = newClusterUUID()
			case "source zero":
				s.Binding.SourceID = "00000000-0000-0000-0000-000000000000"
				a.Source.Members[0].NodeID = s.Binding.SourceID
			case "pod zero":
				s.Process.PodUID = "00000000-0000-0000-0000-000000000000"
			case "source UID zero":
				s.Binding.SourceUID = "00000000-0000-0000-0000-000000000000"
				a.Source.Members[0].NodeUID = s.Binding.SourceUID
			case "namespace zero":
				a.Source.KubeSystemUID = "00000000-0000-0000-0000-000000000000"
			case "peer zero":
				a.Source.Members[0].BootID = "00000000-0000-0000-0000-000000000000"
			case "node name":
				s.Process.NodeName = "other-source"
			case "controller":
				a.Lease.ControllerHolder += "-other"
			case "attempt":
				a.Lease.OperationAttempt++
			case "generation":
				a.Lease.Generation = a.Cohort.Targets[0].Generation
			case "payload":
				a.PayloadSHA256 = ""
			}
			if got := s.valid(a); got != (mode == "expansion" || mode == "replacement") {
				t.Fatal("incorrect preparation authority decision", got)
			}
		})
	}
}

func TestClusterSSHPrejoinSourceObservation(t *testing.T) {
	for _, mode := range []string{"valid", "replacement", "clock", "authority lost", "authority drift", "aliased source", "aliased target", "namespace drift", "final namespace drift", "pod drift", "source drift", "pagination", "malformed pagination", "extra node", "drained", "unlabelled", "image whitespace", "private error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			a, s, pod := sshPrejoinSourceFixture(t, mode == "replacement")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authCalls, calls, pods, lists, namespaces := 0, 0, 0, 0, 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				authCalls++
				if mode == "authority lost" {
					return a, errors.New("private database diagnostic")
				}
				if authCalls == 2 && mode == "authority drift" {
					a.PayloadSHA256 = strings.Repeat("b", 64)
				}
				if mode == "clock" {
					a.Cohort.ObservedAt++
				}
				return a, nil // Deliberately share backing slices with caller.
			}
			read := func(ctx context.Context, path string, out any) error {
				calls++
				if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
					t.Fatal("unbounded observation")
				}
				if calls == 1 && mode == "aliased source" {
					a.Source.Members[0].BootID = newClusterUUID()
				}
				if calls == 1 && mode == "aliased target" {
					a.Cohort.Targets[0].Report.BootID = newClusterUUID()
				}
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					namespaces++
					uid := a.Source.KubeSystemUID
					if mode == "namespace drift" && namespaces == 2 || mode == "final namespace drift" && namespaces == 4 {
						uid = newClusterUUID()
					}
					value = map[string]any{"metadata": map[string]any{"uid": uid}}
				case "/api/v1/nodes":
					lists++
					var nodes []any
					for _, m := range a.Source.Members {
						n := sshSourceKubernetesFixture(m)
						meta := clusterSSHStorageMap(n, "metadata")
						labels := map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
						meta["labels"] = labels
						if mode == "drained" {
							labels["borealis.io/application-state"] = "drained"
						}
						if mode == "unlabelled" {
							delete(labels, "borealis.io/engine-node")
						}
						if mode == "source drift" && lists == 2 {
							meta["uid"] = newClusterUUID()
						}
						nodes = append(nodes, n)
					}
					if mode == "extra node" {
						nodes = append(nodes, nodes[0])
					}
					list := map[string]any{"items": nodes}
					if mode == "pagination" {
						list["metadata"] = map[string]any{"continue": "next-page"}
					}
					if mode == "malformed pagination" {
						list["metadata"] = map[string]any{"continue": []any{}}
					}
					value = list
				case "/api/v1/namespaces/borealis/pods/" + s.Process.PodName:
					pods++
					runtime := anySlice(clusterSSHStorageMap(pod, "status")["containerStatuses"])[0].(map[string]any)
					if mode == "pod drift" && pods == 2 {
						runtime["containerID"] = "containerd://" + strings.Repeat("c", 64)
					}
					if mode == "image whitespace" {
						image := "bad image@sha256:" + strings.Repeat("a", 64)
						anySlice(clusterSSHStorageMap(pod, "spec")["containers"])[0].(map[string]any)["image"] = image
						runtime["imageID"] = image
					}
					value = pod
				default:
					t.Fatal("unexpected Kubernetes path", path)
				}
				if mode == "private error" {
					return errors.New("private Kubernetes diagnostic")
				}
				if mode == "cancelled" {
					cancel()
				}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, out)
			}
			proof, err := readClusterSSHPrejoinSourceCustody(ctx, s, authority, read)
			if mode == "valid" || mode == "replacement" || mode == "clock" {
				if err != nil || !proof.valid(s) || authCalls != 2 || calls != 8 {
					t.Fatal("valid custody rejected", err, authCalls, calls)
				}
			} else if err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("unsafe custody or private diagnostic accepted", err)
			}
		})
	}
}

func TestClusterSSHPrejoinSourceMaterial(t *testing.T) {
	for _, mode := range []string{"valid", "other root", "closed", "empty proof", "wrong source", "authority drift", "pod drift", "malformed image", "callback error", "cancelled", "final drift"} {
		t.Run(mode, func(t *testing.T) {
			a, source, pod := sshPrejoinSourceFixture(t, false)
			p, err := clusterIdentityPodCustody(pod, a.Baseline.SourceSHA, source.Process)
			if err != nil {
				t.Fatal(err)
			}
			proof := clusterSSHPrejoinSourceCustody{Source: source, Pod: p, AuthoritySHA256: strings.Repeat("a", 64)}
			root := t.TempDir()
			want := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "other root" {
				root = t.TempDir()
			}
			if mode == "closed" {
				consumer.Close()
			}
			if mode == "cancelled" {
				cancel()
			}
			calls, used := 0, false
			read := func(context.Context) (clusterSSHPrejoinSourceCustody, error) {
				calls++
				if mode == "authority drift" && calls == 2 || mode == "final drift" && calls == 3 {
					proof.AuthoritySHA256 = strings.Repeat("b", 64)
				}
				if mode == "pod drift" && calls == 2 {
					proof.Pod.ContainerID = "containerd://" + strings.Repeat("c", 64)
				}
				if mode == "empty proof" {
					return clusterSSHPrejoinSourceCustody{}, nil
				}
				if mode == "wrong source" {
					proof.Source.Binding.SourceUID = newClusterUUID()
				}
				if mode == "malformed image" {
					proof.Pod.Image = "api:latest"
				}
				return proof, nil
			}
			err = withClusterSSHPrejoinSourceMaterial(ctx, consumer, root, source, read, func(_ context.Context, got *engineidentity.Material) error {
				used = true
				if !got.Equal(want) {
					t.Fatal("wrong source material")
				}
				if mode == "callback error" {
					return errors.New("private transport failure")
				}
				return nil
			})
			if mode == "valid" {
				if err != nil || !used || calls != 3 {
					t.Fatal("valid material rejected", err, calls)
				}
			} else {
				if err != clusterbootstrap.ErrSessionAuthority {
					t.Fatal("unsafe material or private diagnostic accepted", err)
				}
				if used && mode != "callback error" && mode != "final drift" {
					t.Fatal("invalid proof reached consumer")
				}
			}
		})
	}
}

func TestClusterSSHPrejoinSourceLocalProcess(t *testing.T) {
	_, source, _ := sshPrejoinSourceFixture(t, false)
	fields := map[string]string{"BOREALIS_ENGINE_POD_NAME": source.Process.PodName, "BOREALIS_ENGINE_POD_UID": source.Process.PodUID,
		"BOREALIS_ENGINE_NODE_NAME": source.Process.NodeName, "BOREALIS_API_BACKGROUND_LOOPS": "1"}
	for name, value := range fields {
		t.Setenv(name, value)
	}
	if !clusterSSHPrejoinLocalProcessMatches(source) {
		t.Fatal("own process rejected")
	}
	for name := range fields {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")
			if clusterSSHPrejoinLocalProcessMatches(source) {
				t.Fatal("unbound local identity accepted")
			}
		})
	}
}
