package clusterbootstrap

import (
	"maps"
	"strings"
	"testing"
)

func preparationPlanFixture() (PreparationPlan, map[string]string) {
	e, runtime := preparationFixture()
	delete(runtime, "BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME")
	return PreparationPlan{Source: e.Source, PlanSHA256: e.CohortSHA256, KubeSystemUID: e.KubeSystemUID, K3sVersion: e.K3sVersion,
		ManagementCIDR: e.ManagementCIDR, PodCIDR: e.PodCIDR, ServiceCIDR: e.ServiceCIDR, ControlPlaneVIP: e.ControlPlaneVIP, EdgeVIP: e.EdgeVIP,
		PeerAddresses: e.PeerAddresses, TargetAddresses: []string{e.Target.Address}}, runtime
}

func TestPreparationPlanConfigurationContract(t *testing.T) {
	for _, mode := range []string{"valid", "two targets", "missing target", "foreign target", "duplicate target", "VIP collision", "overlap", "public subnet", "version", "missing cap", "tuning drift", "private syntax", "unknown setting", "bad URL", "bad CA", "rank", "reference", "changed plan", "changed release"} {
		t.Run(mode, func(t *testing.T) {
			plan, runtime := preparationPlanFixture()
			original, err := PreparationPlanConfigurationDigest(plan, runtime)
			if err != nil {
				t.Fatal("valid plan", err)
			}
			valid := false
			switch mode {
			case "valid", "private syntax":
				valid = true
			case "two targets":
				valid = true
				for _, peer := range plan.PeerAddresses {
					if peer != plan.TargetAddresses[0] {
						plan.TargetAddresses = append(plan.TargetAddresses, peer)
						break
					}
				}
			case "missing target":
				plan.TargetAddresses = nil
			case "foreign target":
				plan.TargetAddresses[0] = "192.168.3.249"
			case "duplicate target":
				plan.TargetAddresses = append(plan.TargetAddresses, plan.TargetAddresses[0])
			case "VIP collision":
				plan.ControlPlaneVIP = plan.PeerAddresses[0]
			case "overlap":
				plan.ServiceCIDR = plan.PodCIDR
			case "public subnet":
				plan.PodCIDR = "8.8.8.0/24"
			case "version":
				plan.K3sVersion = "v1.0.0+k3s1"
			case "missing cap":
				delete(runtime, "BOREALIS_POSTGRES_DB_MEMORY_LIMIT")
			case "tuning drift":
				runtime["BOREALIS_DB_POOL_SIZE"] = "999999"
			case "unknown setting":
				runtime["BOREALIS_REPO_ROOT"] = "/tmp/foreign"
			case "bad URL":
				runtime["BOREALIS_DATABASE_URL"] = "postgresql://foreign:secret@elsewhere/db"
			case "bad CA":
				runtime["BOREALIS_ENGINE_NETWORK_MODE"] = "local"
			case "rank":
				runtime["BOREALIS_CLUSTER_SIZING_RANK"] = "4"
			case "reference":
				runtime["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "1"
			case "changed plan":
				plan.PlanSHA256 = strings.Repeat("f", 64)
				valid = true
			case "changed release":
				plan.Source.SourceSHA = strings.Repeat("f", 40)
				valid = true
			}
			before := maps.Clone(runtime)
			actual, err := PreparationPlanConfigurationDigest(plan, runtime)
			if !maps.Equal(before, runtime) {
				t.Fatal("private settings changed")
			}
			if (err == nil) != valid || (!valid && actual != "") {
				t.Fatal(mode, err)
			}
			if valid {
				again, err := PreparationPlanConfigurationDigest(plan, runtime)
				if err != nil || again != actual {
					t.Fatal("unstable planned projection", err)
				}
				if (mode == "changed plan" || mode == "changed release" || mode == "two targets") && actual == original {
					t.Fatal("plan identity omitted")
				}
			}
		})
	}
}

func TestPreparationPlanProfileTuningParity(t *testing.T) {
	for rank := 0; rank < 4; rank++ {
		plan, runtime := preparationPlanFixture()
		runtime["BOREALIS_CLUSTER_SIZING_RANK"] = string(rune('0' + rank))
		runtime["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = []string{"8192", "16384", "32768", "65536"}[rank]
		sizing, err := preparationSizingFromRuntime(runtime)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range sizing.profileTuning() {
			runtime[key] = value
		}
		if _, err := PreparationPlanConfigurationDigest(plan, runtime); err != nil {
			t.Fatal("native profile rejected", rank, err)
		}
		for key, value := range sizing.profileTuning() {
			runtime[key] = "999999"
			if _, err := PreparationPlanConfigurationDigest(plan, runtime); err == nil {
				t.Fatal("profile drift accepted", rank, key)
			}
			runtime[key] = value
		}
	}
}
