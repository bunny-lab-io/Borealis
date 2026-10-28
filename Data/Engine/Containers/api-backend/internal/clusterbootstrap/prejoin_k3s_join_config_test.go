package clusterbootstrap

import (
	"strconv"
	"strings"
	"testing"
)

func TestPrejoinK3sJoinConfigurationKeepsNodeFenced(t *testing.T) {
	expected, _ := preparationFixture()
	raw, err := PrejoinK3sJoinConfiguration(expected)
	if err != nil {
		t.Fatal(err)
	}
	// Fixed syntax contains only independently validated IPv4/hostname/path
	// scalars; no YAML dependency or runtime process is needed for this contract.
	for key, value := range map[string]string{"server": "https://" + expected.ControlPlaneVIP + ":6443", "token-file": PrejoinK3sJoinTokenPath, "node-name": expected.Target.Hostname, "node-ip": expected.Target.Address} {
		if strings.Count(raw, key+": "+strconv.Quote(value)+"\n") != 1 {
			t.Fatal("fixed source/target binding missing", key)
		}
	}
	for _, required := range []string{"tls-san:\n  - " + strconv.Quote(expected.ControlPlaneVIP) + "\n", "disable:\n  - traefik\n  - servicelb\n", "secrets-encryption: true\n", "embedded-registry: true\n"} {
		if strings.Count(raw, required) != 1 {
			t.Fatal("required cluster setting missing")
		}
	}
	for key, value := range map[string]string{"engine-node": "true", "application-state": "drained", "control-plane-eligible": "false", "edge-eligible": "false", "scheduler-eligible": "false", "postgres-primary-eligible": "false"} {
		if strings.Count(raw, "  - borealis.io/"+key+"="+value+"\n") != 1 {
			t.Fatal("node became eligible before activation", key)
		}
	}
	for _, forbidden := range []string{"cluster-init:", "token:", "borealis.io/application-state=active", "eligible=true"} {
		if strings.Contains(raw, forbidden) {
			t.Fatal("configuration bypassed guarded join", forbidden)
		}
	}
	for _, mutate := range []func(*PreparationExpected){
		func(e *PreparationExpected) { e.Target.Hostname = "engine\ntoken: injected" },
		func(e *PreparationExpected) { e.ControlPlaneVIP = "https://foreign" },
		func(e *PreparationExpected) { e.Target.Address = "192.168.3.250\n" },
		func(e *PreparationExpected) { e.K3sVersion = "v1.36.4+k3s1" },
	} {
		bad := expected
		mutate(&bad)
		if raw, err := PrejoinK3sJoinConfiguration(bad); err == nil || raw != "" {
			t.Fatal("invalid configuration rendered")
		}
	}
}
