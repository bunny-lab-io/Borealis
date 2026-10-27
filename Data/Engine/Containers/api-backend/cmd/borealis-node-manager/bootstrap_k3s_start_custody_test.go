package main

import (
	"context"
	"strings"
	"testing"
)

func TestBootstrapStartingK3sUnitStates(t *testing.T) {
	inert := bootstrapInertK3sFixture + "InvocationID=\n"
	running := bootstrapStartedK3sFixture()
	pre := strings.NewReplacer("ActiveState=active", "ActiveState=activating", "SubState=running", "SubState=start-pre", "MainPID=42", "MainPID=0", "ControlPID=0", "ControlPID=43", "Job=\n", "Job=12\n").Replace(running)
	starting := strings.NewReplacer("ActiveState=active", "ActiveState=activating", "SubState=running", "SubState=start", "Job=\n", "Job=12\n").Replace(running)
	for _, raw := range []string{inert, pre, starting, running, strings.Replace(running, "Job=\n", "Job=12\n", 1)} {
		if validateBootstrapStartingK3s(raw, false) != nil {
			t.Fatal("known startup transition rejected", raw)
		}
		if raw != running {
			if _, err := parseBootstrapStartedK3s(raw); err == nil {
				t.Fatal("transition became completed receipt")
			}
		}
	}
	absent := strings.NewReplacer("LoadState=loaded", "LoadState=not-found", "UnitFileState=disabled", "UnitFileState=", "FragmentPath=/etc/systemd/system/k3s.service", "FragmentPath=", "Type=notify", "Type=simple").Replace(inert)
	if validateBootstrapStartingK3s(absent, true) != nil || validateBootstrapStartingK3s(absent, false) == nil {
		t.Fatal("absent unit escaped cursor-only scope")
	}
	for _, raw := range []string{pre, starting, running} {
		for _, change := range [][2]string{{"Job=12", "Job=stop"}, {"ControlPID=0", "ControlPID=01"}, {"LoadState=loaded", "LoadState=not-found"}, {"SubState=start", "SubState=auto-restart"}, {"NeedDaemonReload=no", "NeedDaemonReload=yes"}, {"UnitFileState=disabled", "UnitFileState=enabled"}, {"Result=success", "Result=exit-code"}, {"DropInPaths=\n", "DropInPaths=/foreign\n"}, {"FragmentPath=/etc/systemd/system/k3s.service", "FragmentPath=/foreign"}, {strings.Repeat("a", 32), strings.Repeat("0", 32)}} {
			changed := strings.Replace(raw, change[0], change[1], 1)
			if changed == raw {
				continue
			}
			if validateBootstrapStartingK3s(changed, false) == nil {
				t.Fatal("unsafe transition admitted", change)
			}
		}
	}
	for _, raw := range []string{running + "Job=\n", strings.TrimSuffix(running, "\n"), strings.Replace(starting, "Job=12", "Job=", 1), strings.Replace(pre, "ControlPID=43", "ControlPID=0", 1), strings.Replace(running, "ActiveState=active", "ActiveState=failed", 1)} {
		if validateBootstrapStartingK3s(raw, false) == nil {
			t.Fatal("ambiguous transition admitted")
		}
	}
}

func TestBootstrapStartingK3sPathCustody(t *testing.T) {
	for _, mode := range []string{"configured", "running", "incomplete", "foreign archive", "kubelet symlink", "unsafe kubelet", "public kubeconfig", "consumer config"} {
		t.Run(mode, func(t *testing.T) {
			root, expected, source, material := bootstrapJoinFilesFixture(t)
			for _, dir := range []string{"opt/Borealis", "var/lib/rancher/k3s/agent/images", "usr/local/bin", "etc/systemd/system"} {
				if root.MkdirAll(dir, 0700) != nil {
					t.Fatal("fixture directory")
				}
			}
			service, err := bootstrapPrejoinK3sFiles(expected)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range service {
				if root.WriteFile(f.path, f.data, f.mode) != nil {
					t.Fatal("service fixture")
				}
			}
			check := func(context.Context) error { return nil }
			err = withBootstrapK3sJoinFiles(context.Background(), expected, source, material, check, func(plan bootstrapK3sJoinFiles) error {
				if mode != "incomplete" {
					if installBootstrapK3sJoinFiles(context.Background(), root, expected, plan, check) != nil {
						t.Fatal("config fixture")
					}
				}
				if mode != "configured" && mode != "incomplete" {
					if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.MkdirAll("var/lib/kubelet", 0755) != nil || root.WriteFile("etc/rancher/k3s/k3s.yaml", []byte("generated fixture"), 0600) != nil {
						t.Fatal("runtime fixture")
					}
				}
				switch mode {
				case "foreign archive":
					if root.WriteFile("var/lib/rancher/k3s/agent/images/foreign.tar", []byte("foreign"), 0600) != nil {
						t.Fatal("archive fixture")
					}
				case "kubelet symlink":
					if root.Rename("var/lib/kubelet", "var/lib/kubelet-original") != nil || root.Symlink("kubelet-original", "var/lib/kubelet") != nil {
						t.Fatal("link fixture")
					}
				case "unsafe kubelet":
					if root.Chmod("var/lib/kubelet", 0777) != nil {
						t.Fatal("mode fixture")
					}
				case "public kubeconfig":
					if root.Chmod("etc/rancher/k3s/k3s.yaml", 0644) != nil {
						t.Fatal("config mode fixture")
					}
				case "consumer config":
					if root.MkdirAll("etc/borealis", 0700) != nil {
						t.Fatal("consumer fixture")
					}
				}
				valid := mode == "configured" || mode == "running"
				if (verifyBootstrapArtifactPaths(root, expected, "reconcile_prepared_k3s_start", true) == nil) != valid {
					t.Fatal("wrong startup custody")
				}
				if mode != "incomplete" && (verifyBootstrapProgressPaths(root, expected) == nil) != valid {
					t.Fatal("wrong startup cursor")
				}
				if mode == "running" {
					for _, action := range []string{"reconcile_prejoin_artifacts", "reconcile_prejoin_k3s", "reconcile_prejoin_k3s_configuration", "start_prepared_k3s"} {
						if verifyBootstrapArtifactPaths(root, expected, action, true) == nil {
							t.Fatal("old/fresh phase accepted running host", action)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
