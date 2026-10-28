package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapArtifactCustody(t *testing.T) {
	for _, mode := range []string{"fresh", "directory prefix", "archive prefix", "all archives", "binary scratch", "binary chmod", "binary final", "early binary", "foreign archive", "duplicate role", "extra token", "unsafe parent", "unsafe archive", "linked archive", "foreign scratch", "two scratch", "binary plus scratch", "host changed", "earlier action", "existing fresh", "installed service", "runtime service", "runtime fresh service"} {
		t.Run(mode, func(t *testing.T) {
			root := bootstrapHostCustodyFixture(t)
			expected := bootstrapHostFilesFixture()
			if installBootstrapPrejoinHostFilesWith(context.Background(), root, expected, func(context.Context) error { return nil }) != nil {
				t.Fatal("host setup")
			}
			for _, dir := range []string{"var/lib", "usr/local/bin"} {
				if root.MkdirAll(dir, 0700) != nil {
					t.Fatal("system setup")
				}
			}
			write := func(path string, data []byte, mode os.FileMode) {
				t.Helper()
				if root.WriteFile(path, data, mode) != nil {
					t.Fatal("file setup", path)
				}
			}
			images := "var/lib/rancher/k3s/agent/images"
			name := "borealis-api-backend-" + strings.Repeat("a", 16) + ".tar"
			scratch := ".borealis-artifact-" + strings.Repeat("b", 64)
			if mode != "fresh" {
				if root.MkdirAll(images, 0700) != nil {
					t.Fatal("image setup")
				}
			}
			if mode == "archive prefix" {
				write(filepath.Join(images, name), []byte("inert"), 0600)
			}
			if mode != "fresh" && mode != "directory prefix" && mode != "archive prefix" && mode != "early binary" {
				for _, role := range clusterbootstrap.ImageRoles() {
					write(filepath.Join(images, "borealis-"+role+"-"+strings.Repeat("a", 16)+".tar"), []byte("inert"), 0600)
				}
				for _, pin := range clusterbootstrap.ExternalImagePins() {
					write(filepath.Join(images, clusterbootstrap.ExternalImageAssetName(pin.Reference)), []byte("inert"), 0600)
				}
				write(filepath.Join(images, clusterbootstrap.K3sArchiveName), []byte("inert"), 0600)
				write(filepath.Join(images, "borealis-node-postgresql-linux-amd64.oci.tar"), []byte("inert"), 0600)
			}
			binary := func(path string, mode os.FileMode) {
				f, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
				if err != nil {
					t.Fatal(err)
				}
				if f.Truncate(clusterbootstrap.K3sPins().Binary.Size) != nil || f.Close() != nil {
					t.Fatal("binary fixture")
				}
			}
			action, initial := "reconcile_prejoin_artifacts", false
			switch mode {
			case "fresh":
				action, initial = "publish_prejoin_artifacts", true
			case "binary scratch":
				binary("usr/local/bin/"+scratch, 0600)
			case "binary chmod":
				binary("usr/local/bin/"+scratch, 0755)
			case "binary final":
				binary("usr/local/bin/k3s", 0755)
			case "early binary":
				binary("usr/local/bin/k3s", 0755)
			case "foreign archive":
				write(images+"/foreign.tar", nil, 0600)
			case "duplicate role":
				write(images+"/borealis-api-backend-"+strings.Repeat("b", 16)+".tar", nil, 0600)
			case "extra token":
				write("var/lib/rancher/k3s/token", nil, 0600)
			case "unsafe parent":
				root.Chmod("var/lib/rancher", 0777)
			case "unsafe archive":
				root.Chmod(images+"/"+name, 0644)
			case "linked archive":
				root.Remove(images + "/" + name)
				root.Symlink(t.TempDir(), images+"/"+name)
			case "foreign scratch":
				write(images+"/.borealis-artifact-foreign", nil, 0600)
			case "two scratch":
				write(images+"/"+scratch, nil, 0600)
				write(images+"/.borealis-artifact-"+strings.Repeat("c", 64), nil, 0600)
			case "binary plus scratch":
				binary("usr/local/bin/k3s", 0755)
				binary("usr/local/bin/"+scratch, 0600)
			case "host changed":
				write("etc/rancher/k3s/registries.yaml", []byte("changed"), 0600)
			case "earlier action":
				action = "reconcile_prejoin_host"
			case "installed service", "runtime service", "runtime fresh service":
				files, err := bootstrapPrejoinK3sFiles(expected)
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					write(file.path, file.data, file.mode)
				}
				if mode == "runtime service" {
					action = "reconcile_prejoin_k3s"
				}
				if mode == "runtime fresh service" {
					action, initial = "install_prejoin_k3s", true
				}
			case "existing fresh":
				action, initial = "publish_prejoin_artifacts", true
			}
			err := verifyBootstrapArtifactPaths(root, expected, action, initial)
			valid := mode == "fresh" || mode == "directory prefix" || mode == "archive prefix" || mode == "all archives" || mode == "binary scratch" || mode == "binary chmod" || mode == "binary final" || mode == "runtime service"
			if (err == nil) != valid {
				t.Fatal("incorrect bounded custody", err)
			}
			if mode != "fresh" && verifyBootstrapHostPaths(root, expected, "reconcile_prejoin_host", false) == nil {
				t.Fatal("earlier custody broadened")
			}
		})
	}
}
