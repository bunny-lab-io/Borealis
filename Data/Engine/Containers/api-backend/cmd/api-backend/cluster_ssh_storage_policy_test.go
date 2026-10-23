package main

import (
	"context"
	"encoding/json"
	"testing"
)

func addSSHStoragePolicyFixture(f *sshStorageFixture) {
	settings := map[string]string{
		"current-longhorn-version": "v1.12.0", "default-data-path": "/var/lib/longhorn/", "create-default-disk-labeled-nodes": "false",
		"storage-reserved-percentage-for-default-disk": "30", "storage-minimal-available-percentage": "25", "storage-over-provisioning-percentage": "100",
		"default-replica-count": `{"v1":"3","v2":"3"}`, "replica-soft-anti-affinity": "false", "replica-zone-soft-anti-affinity": "true", "replica-disk-soft-anti-affinity": "true",
		"allow-empty-node-selector-volume": "true", "allow-empty-disk-selector-volume": "true", "disable-scheduling-on-cordoned-node": "true", "replica-auto-balance": "disabled",
	}
	for name, value := range settings {
		object := sshStorageObject("longhorn.io/v1beta2", "Setting", "longhorn-system", name)
		object["value"] = value
		f.objects[clusterSSHStorageSettingPrefix+name] = object
	}
	for _, local := range []bool{false, true} {
		name, locality, binding, reclaim := "borealis-longhorn", "disabled", "Immediate", "Delete"
		if local {
			name, locality, binding, reclaim = "borealis-longhorn-local", "strict-local", "WaitForFirstConsumer", "Retain"
		}
		object := sshStorageObject("storage.k8s.io/v1", "StorageClass", "", name)
		object["provisioner"], object["volumeBindingMode"], object["reclaimPolicy"] = clusterLonghornCSIDriver, binding, reclaim
		// Local class mirrors installed manifest: absent fsType/dataEngine.
		parameters := map[string]any{"numberOfReplicas": "1", "dataLocality": locality, "staleReplicaTimeout": "30"}
		if !local {
			parameters["fsType"], parameters["dataEngine"], parameters["unmapMarkSnapChainRemoved"] = "ext4", "v1", "ignored"
		}
		object["parameters"] = parameters
		f.objects[clusterSSHStorageClassPrefix+name] = object
	}
}

func TestClusterSSHStorageProvisioningPolicy(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		f := newSSHStorageFixture(t, replacement)
		observation, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
		if err != nil {
			t.Fatal(err)
		}
		p := observation.Requirements.Policy
		if p.DefaultPath != "/var/lib/longhorn" || p.ReservedPercent != 30 || p.MinimalAvailablePercent != 25 || p.OverProvisioningPercent != 100 || p.DefaultReplicas != 3 || len(p.Classes) != 2 {
			t.Fatal("installed policy lost")
		}
		if p.Classes[0].Replicas != 1 || p.Classes[1].Locality != "strict-local" {
			t.Fatal("class policy lost")
		}
		if replacement && observation.Requirements.Volumes[0].Replicas != 3 {
			t.Fatal("class default replaced actual replica count")
		}
	}
}

func TestClusterSSHStorageProvisioningRejectsUnsupported(t *testing.T) {
	cases := map[string]func(*sshStorageFixture){
		"missing setting": func(f *sshStorageFixture) { delete(f.objects, clusterSSHStorageSettingPrefix+"default-data-path") },
		"version": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"current-longhorn-version"]["value"] = "v1.13.0"
		},
		"plain default replicas": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"default-replica-count"]["value"] = "3"
		},
		"duplicate default replicas": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"default-replica-count"]["value"] = `{"v1":"1","v1":"3","v2":"3"}`
		},
		"wrong setting identity": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageSettingPrefix+"default-data-path"], "metadata")["namespace"] = "foreign"
		},
		"root path": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"default-data-path"]["value"] = "/"
		},
		"path traversal": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"default-data-path"]["value"] = "/var/lib/../longhorn"
		},
		"double slash": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"default-data-path"]["value"] = "/var//lib/longhorn/"
		},
		"tag-only": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"create-default-disk-labeled-nodes"]["value"] = "true"
		},
		"hard zones": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"replica-zone-soft-anti-affinity"]["value"] = "false"
		},
		"reserved all": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"storage-reserved-percentage-for-default-disk"]["value"] = "100"
		},
		"negative percentage": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"storage-minimal-available-percentage"]["value"] = "-1"
		},
		"overprovision overflow": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageSettingPrefix+"storage-over-provisioning-percentage"]["value"] = "18446744073709551616"
		},
		"foreign provisioner": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn-local"]["provisioner"] = "foreign"
		},
		"PG nonlocal": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn-local"], "parameters")["dataLocality"] = "disabled"
		},
		"PG immediate": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn-local"]["volumeBindingMode"] = "Immediate"
		},
		"PG delete": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn-local"]["reclaimPolicy"] = "Delete"
		},
		"v2": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn-local"], "parameters")["dataEngine"] = "v2"
		},
		"tag selector": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn"], "parameters")["diskSelector"] = "ssd"
		},
		"unknown parameter": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn"], "parameters")["futureCapacityPolicy"] = "enabled"
		},
		"nonstrings": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn"], "parameters")["numberOfReplicas"] = 3
		},
		"mount options": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageClassPrefix+"borealis-longhorn"]["mountOptions"] = []any{"ro"}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			change(f)
			if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err == nil {
				t.Fatal("unsupported provisioning accepted")
			}
		})
	}
}

func TestClusterSSHStorageProvisioningReceiptAndClone(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	var escaped clusterSSHStorageRequirements
	err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, r clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
		escaped = r
		r.Policy.Classes[0].Replicas = 3
		return checks.Inputs(ctx)
	})
	if err != nil || escaped.Policy.Classes[0].Replicas != 3 {
		t.Fatal("consumer copy unexpectedly aliases retained policy", err)
	}
	f = newSSHStorageFixture(t, false)
	err = withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(ctx context.Context, r clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
		f.mu.Lock()
		f.objects[clusterSSHStorageSettingPrefix+"storage-minimal-available-percentage"]["value"] = "26"
		f.mu.Unlock()
		return nil
	})
	if err == nil {
		t.Fatal("changed policy survived final observation")
	}
	// Class omission cannot create an imported valid policy, even with a digest.
	f = newSSHStorageFixture(t, false)
	v := sshStorageSnapshotFixture(t, f.a)
	raw, _ := json.Marshal(v)
	var copied clusterSSHStorageSnapshot
	_ = json.Unmarshal(raw, &copied)
	copied.Requirements.Policy.Classes = nil
	if validClusterSSHStorageSnapshot(copied, f.a.Source) {
		t.Fatal("unbound policy imported")
	}
}
