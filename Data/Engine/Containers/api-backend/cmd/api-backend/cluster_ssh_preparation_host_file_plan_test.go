package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"reflect"
	"strings"
	"testing"
)

func TestClusterSSHPreparationPlanHostFileDemands(t *testing.T) {
	_, _, snapshot := sshBrokerFixture(t)
	expected := snapshot.Expected
	plan := clusterbootstrap.PreparationPlan{Source: expected.Source, PlanSHA256: strings.Repeat("a", 64), KubeSystemUID: expected.KubeSystemUID, K3sVersion: expected.K3sVersion,
		ManagementCIDR: expected.ManagementCIDR, PodCIDR: expected.PodCIDR, ServiceCIDR: expected.ServiceCIDR, ControlPlaneVIP: expected.ControlPlaneVIP, EdgeVIP: expected.EdgeVIP,
		PeerAddresses: append([]string(nil), expected.PeerAddresses...), TargetAddresses: []string{expected.Target.Address}}
	got, err := clusterSSHPreparationPlanHostFileDemands(plan, "noble")
	want, claimedErr := clusterSSHPreparationHostFileDemands(expected, "noble")
	if err != nil || claimedErr != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("proposed/native bytes diverged", err, claimedErr)
	}
	if got, err := clusterSSHPreparationPlanHostFileDemands(clusterbootstrap.PreparationPlan{}, "noble"); err == nil || got != nil {
		t.Fatal("absent source plan supplied demand")
	}
}
