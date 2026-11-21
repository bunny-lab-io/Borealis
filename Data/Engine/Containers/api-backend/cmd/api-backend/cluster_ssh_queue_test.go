package main

import (
	"strings"
	"testing"
)

func TestClusterSSHInspectionQueueSourceContract(t *testing.T) {
	source := clusterSSHQueueSource{ClusterID: newClusterUUID(), Enabled: 1, Status: "Healthy", ActiveSize: 1, DesiredSize: 1, Members: 1, HMRState: "inactive",
		Release: "2026.09.1.2", SHA: strings.Repeat("a", 40), ConfigJSON: `{"k3s_version":"v1.36.3+k3s1"}`}
	for _, mode := range []string{"pair", "planned pair", "replacement", "recorded development", "active operation", "drained", "pending admission", "disabled", "wrong cluster", "HMR", "database degraded", "database durability", "malformed config", "oversize config", "missing K3s", "invalid release", "invalid SHA", "unrecorded member", "unrecorded replacement", "unsupported size"} {
		t.Run(mode, func(t *testing.T) {
			s := source
			switch mode {
			case "planned pair":
				s.DesiredSize = 3
			case "replacement":
				s.ActiveSize, s.Members, s.DesiredSize, s.Status = 2, 2, 3, "Degraded Quorum"
			case "recorded development":
				s.Release = "dev-" + s.SHA[:12]
			case "active operation":
				s.ActiveOperationID = newClusterUUID()
			case "drained":
				s.Drained = 1
			case "pending admission":
				s.PendingAdmissions = 1
			case "disabled":
				s.Enabled = 0
			case "wrong cluster":
				s.ClusterID = "unknown"
			case "HMR":
				s.HMRState = "isolated"
			case "database degraded":
				s.Status = "Degraded Database"
			case "database durability":
				s.ConfigJSON = `{"k3s_version":"v1.36.3+k3s1","database_runtime":{"durability_quorum":false}}`
			case "malformed config":
				s.ConfigJSON += ` trailing`
			case "oversize config":
				s.ConfigJSON = strings.Repeat(" ", 64<<10) + s.ConfigJSON
			case "missing K3s":
				s.ConfigJSON = "{}"
			case "invalid release":
				s.Release = "main"
			case "invalid SHA":
				s.SHA = s.SHA[:12]
			case "unrecorded member":
				s.Members = 2
			case "unrecorded replacement":
				s.ActiveSize, s.Members, s.DesiredSize = 2, 2, 3
			case "unsupported size":
				s.ActiveSize, s.Members, s.DesiredSize = 3, 3, 3
			}
			count, err := s.validate()
			valid := textInSet(mode, "pair", "planned pair", "replacement", "recorded development")
			if valid {
				want := 2
				if mode == "replacement" {
					want = 1
				}
				if err != nil || count != want {
					t.Fatalf("supported source rejected: %v", err)
				}
			} else if err == nil || count != 0 {
				t.Fatal("unsafe source accepted inspection queue")
			}
		})
	}
}
