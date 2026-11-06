package main

import (
	"context"
	"os"
	"testing"
)

func TestBootstrapK3sConfigurationCustody(t *testing.T) {
	for _, mode := range []string{"fresh", "configured", "partial token", "missing service", "foreign config", "linked token", "unsafe parent", "foreign scratch"} {
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
			if mode != "missing service" {
				for _, f := range service {
					if root.WriteFile(f.path, f.data, f.mode) != nil {
						t.Fatal("service fixture")
					}
				}
			}
			check := func(context.Context) error { return nil }
			err = withBootstrapK3sJoinFiles(context.Background(), expected, source, material, check, func(plan bootstrapK3sJoinFiles) error {
				if mode == "configured" || mode == "partial token" || mode == "linked token" {
					f := plan.files[0]
					if root.WriteFile(f.path, f.data, f.mode) != nil {
						t.Fatal("token fixture")
					}
				}
				if mode == "configured" {
					f := plan.files[1]
					if root.WriteFile(f.path, f.data, f.mode) != nil {
						t.Fatal("config fixture")
					}
				}
				switch mode {
				case "foreign config":
					if root.WriteFile("etc/rancher/k3s/config.yaml.d/foreign.yaml", []byte("x"), 0600) != nil {
						t.Fatal("foreign fixture")
					}
				case "linked token":
					if root.Link(plan.files[0].path, "alias") != nil {
						t.Fatal("link fixture")
					}
				case "unsafe parent":
					if root.Chmod("etc/rancher/k3s", 0777) != nil {
						t.Fatal("mode fixture")
					}
				case "foreign scratch":
					if root.WriteFile("etc/rancher/k3s/.borealis-k3s-join-foreign", []byte("x"), 0600) != nil {
						t.Fatal("scratch fixture")
					}
				}
				valid := mode == "fresh" || mode == "configured" || mode == "partial token"
				if (verifyBootstrapArtifactPaths(root, expected, "reconcile_prejoin_k3s_configuration", false) == nil) != valid {
					t.Fatal("wrong configuration custody")
				}
				if (verifyBootstrapArtifactPaths(root, expected, "configure_prejoin_k3s", true) == nil) != (mode == "fresh") {
					t.Fatal("fresh path adopted existing/foreign effects")
				}
				// Cursor recognizes only an admissible prefix, never an effect receipt.
				cursorValid := valid || mode == "missing service"
				if (verifyBootstrapProgressPaths(root, expected) == nil) != cursorValid {
					t.Fatal("wrong progress custody")
				}
				if mode == "configured" || mode == "partial token" {
					for _, action := range []string{"publish_prejoin_artifacts", "reconcile_prejoin_artifacts", "install_prejoin_k3s", "reconcile_prejoin_k3s"} {
						if verifyBootstrapArtifactPaths(root, expected, action, false) == nil {
							t.Fatal("earlier phase accepted later config", action)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := root.Lstat("etc/rancher/k3s/config.yaml.d/20-borealis-cluster-join.yaml"); mode != "configured" && !os.IsNotExist(err) {
				t.Fatal("observation mutated config")
			}
		})
	}
}
