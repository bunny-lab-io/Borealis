package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClusterSSHSystemUpgradeProducerResourceParity(t *testing.T) {
	// Synthetic source configuration, not production defaults. Run actual producer
	// validation, then consume its unchanged resource values through source reads.
	for _, cpu := range []string{"125m", "0.125"} {
		t.Run(cpu, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "system-upgrade-resources.json")
			raw := `{"requests":{"cpu":"` + cpu + `","memory":"80Mi"},"limits":{"cpu":"1","memory":"256Mi"}}`
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("python3", "../../../../../../Data/Engine/K3s/cluster/render-system-upgrade.py", "config", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			var bounds map[string]any
			if err := json.Unmarshal(out, &bounds); err != nil {
				t.Fatal(err)
			}
			f := newSSHStorageFixture(t, false)
			sshSystemUpgradeSpec(f)["containers"].([]any)[0].(map[string]any)["resources"] = bounds
			sshSystemUpgradeRuntimeFixture(f)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			want := clusterSSHApplicationResource{Name: "system-upgrade-controller", CPURequestMilli: 125, CPULimitMilli: 1000, MemoryRequestBytes: 80 << 20, MemoryLimitBytes: 256 << 20}
			if err != nil || got.Requirements.SystemUpgradeResources.Envelope != want {
				t.Fatal("producer/consumer bounds differ", err, got.Requirements.SystemUpgradeResources)
			}
		})
	}
}
