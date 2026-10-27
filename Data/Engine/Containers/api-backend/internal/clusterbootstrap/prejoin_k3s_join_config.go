package clusterbootstrap

import "fmt"

const PrejoinK3sJoinTokenPath = "/etc/rancher/k3s/borealis-server.token"
const PrejoinK3sJoinConfigPath = "/etc/rancher/k3s/config.yaml.d/20-borealis-cluster-join.yaml"

// Fixed public configuration. No caller-selected endpoint/path, ambient
// environment or private token bytes enter this renderer. Existing baseline
// supplies inherited pod/service CIDRs. Node remains drained and ineligible
// until separately proved membership, import and identity activation finish.
func PrejoinK3sJoinConfiguration(expected PreparationExpected) (string, error) {
	if expected.Validate() != nil || expected.K3sVersion != K3sPins().Version {
		return "", ErrPreparationConfig
	}
	return fmt.Sprintf("server: %q\ntoken-file: %q\nnode-name: %q\nnode-ip: %q\ntls-san:\n  - %q\ndisable:\n  - traefik\n  - servicelb\nsecrets-encryption: true\nembedded-registry: true\nnode-label:\n  - borealis.io/engine-node=true\n  - borealis.io/application-state=drained\n  - borealis.io/control-plane-eligible=false\n  - borealis.io/edge-eligible=false\n  - borealis.io/scheduler-eligible=false\n  - borealis.io/postgres-primary-eligible=false\n",
		"https://"+expected.ControlPlaneVIP+":6443", PrejoinK3sJoinTokenPath, expected.Target.Hostname, expected.Target.Address, expected.ControlPlaneVIP), nil
}
