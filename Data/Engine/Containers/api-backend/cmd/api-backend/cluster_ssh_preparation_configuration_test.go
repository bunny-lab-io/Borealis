package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sshPreparationConfigurationFixture(t *testing.T, replacement bool) clusterSSHPreparationWork {
	t.Helper()
	cohort, source := sshInspectionCohortFixture(t)
	if replacement {
		target := cohort.Targets[1]
		source.Members = append(source.Members, clusterSSHSourceMember{NodeID: target.Binding.TargetID, NodeUID: newClusterUUID(), Name: target.Report.Hostname, Address: target.Binding.Address, MachineID: target.Report.MachineID, BootID: target.Report.BootID})
		source.ActiveSize, source.DesiredSize, source.Status = 2, 3, "Degraded Quorum"
		cohort.Targets = cohort.Targets[:1]
	}
	a := clusterSSHPreparationAuthorization{Version: 1, OperationID: cohort.OperationID, Attempt: cohort.Attempt, ControllerHolder: cohort.ControllerHolder,
		InspectionSHA256: strings.Repeat("a", 64), QualificationSHA256: strings.Repeat("b", 64), SourceSHA256: strings.Repeat("c", 64)}
	for _, target := range cohort.Targets {
		a.Targets = append(a.Targets, clusterSSHPreparationAuthorizedTarget{target.Binding.TargetID, target.Generation + 1, strings.Repeat("d", 64)})
	}
	return clusterSSHPreparationWork{Authorization: a, Baseline: clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true},
		Proof: clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}, Payload: `{"source_k3s_version":"v1.36.3+k3s1"}`}
}

func sshPreparationConfigurationSecret() map[string]any {
	secret := sshPreparationSecretFixture()
	secret["data"].(map[string]string)["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = base64.StdEncoding.EncodeToString([]byte("4096Mi"))
	return secret
}

func TestClusterSSHPreparationConfigurationFreshPlan(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "wrong plan", "claimed work", "authority", "source drift", "Secret drift", "excluded Secret drift", "profile drift", "missing cap", "network drift", "source networks disagree", "target prefix disagrees", "source error", "network error", "cancel", "timeout", "copied input", "changed next read"} {
		t.Run(mode, func(t *testing.T) {
			work := sshPreparationConfigurationFixture(t, mode == "replacement" || mode == "source networks disagree")
			if mode == "target prefix disagrees" {
				work.Proof.Cohort.Targets[1].Report.ConnectedPrefix = "192.168.91.0/24"
			}
			plan, _ := clusterSSHPreparationPlanDigest(work.Authorization)
			source := work.Proof.Source
			secret := sshPreparationConfigurationSecret()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 5*time.Millisecond)
				defer stop()
			}
			checks, reads, networks := 0, 0, 0
			check := func(ctx context.Context) error {
				checks++
				if mode == "authority" && checks > 4 {
					return errClusterConflict
				}
				return ctx.Err()
			}
			get := func(ctx context.Context, path string, out any) error {
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					value = map[string]any{"metadata": map[string]any{"uid": source.KubeSystemUID}}
				case "/api/v1/nodes":
					if mode == "source error" {
						return errClusterConflict
					}
					items := []any{}
					for _, member := range source.Members {
						node := sshSourceKubernetesFixture(member)
						if mode == "source drift" && reads > 0 {
							node["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
						}
						items = append(items, node)
					}
					value = map[string]any{"items": items}
				case clusterSSHRuntimeSecretPath:
					reads++
					if mode == "Secret drift" && reads > 1 {
						secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
					}
					if mode == "excluded Secret drift" && reads > 1 {
						secret["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("changed"))
					}
					if mode == "profile drift" {
						secret["data"].(map[string]string)["BOREALIS_DB_POOL_SIZE"] = base64.StdEncoding.EncodeToString([]byte("999999"))
					}
					if mode == "missing cap" {
						delete(secret["data"].(map[string]string), "BOREALIS_POSTGRES_DB_MEMORY_LIMIT")
					}
					value = secret
				default:
					return errClusterConflict
				}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, out)
			}
			network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				networks++
				if mode == "network error" {
					return clusterbootstrap.SourceNetwork{}, errClusterConflict
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "timeout" {
					<-ctx.Done()
					return clusterbootstrap.SourceNetwork{}, ctx.Err()
				}
				n := sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version)
				if mode == "source networks disagree" && member.NodeUID == source.Members[1].NodeUID {
					n.ServiceCIDR = "10.44.0.0/16"
				}
				if mode == "network drift" && networks > len(source.Members) {
					n.ServiceCIDR = "10.44.0.0/16"
				}
				return n, ctx.Err()
			}
			if mode == "claimed work" {
				work.Claims = append(work.Claims, clusterSSHNetworkTargetClaim{})
			}
			read := newClusterSSHPreparationConfigurationRead(work, check, get, network)
			if mode == "copied input" {
				work.Proof.Cohort.Targets[0].Report.BootID = newClusterUUID()
			}
			requested := plan
			if mode == "wrong plan" {
				requested = strings.Repeat("f", 64)
			}
			digest, err := read(ctx, requested)
			valid := mode == "pair" || mode == "replacement" || mode == "copied input" || mode == "changed next read"
			if (err == nil) != valid || (err != nil && digest != "") {
				t.Fatal(mode, err)
			}
			if valid {
				if networks != 2*len(source.Members) || reads != 2 {
					t.Fatal("missing fresh source rounds", networks, reads)
				}
				if mode == "changed next read" {
					secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
				}
				again, err := read(ctx, plan)
				if mode == "changed next read" {
					if err == nil {
						t.Fatal("prior digest accepted changed Secret")
					}
				} else if err != nil || again != digest || networks != 4*len(source.Members) || reads != 4 {
					t.Fatal("fresh repeat failed", err)
				}
			} else {
				before := checks
				if _, err := read(context.Background(), plan); err == nil || checks != before {
					t.Fatal("ignored failure did not latch")
				}
			}
		})
	}
}
