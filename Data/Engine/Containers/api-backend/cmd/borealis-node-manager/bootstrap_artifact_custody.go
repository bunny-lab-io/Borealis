package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

func bootstrapArtifactAction(action string) bool {
	return action == "publish_prejoin_artifacts" || action == "reconcile_prejoin_artifacts" || bootstrapK3sAction(action) || bootstrapK3sConfigurationAction(action) || bootstrapK3sStartupAction(action)
}

var bootstrapArtifactScratch = regexp.MustCompile(`^\.borealis-artifact-[a-f0-9]{64}$`)
var bootstrapArtifactSuffix = regexp.MustCompile(`^[a-f0-9]{16}\.tar$`)

// This bounded inert-path observation grants neither publication nor completion.
// Exact archive identities/hashes and ordered source extras are proved by the
// journaled complete-artifact transaction after all input has authenticated.
// Existing source/host actions retain their own stricter absence boundaries.
func verifyBootstrapArtifactPaths(root *os.Root, expected clusterbootstrap.PreparationExpected, action string, initial bool) error {
	if root == nil || expected.Validate() != nil || !bootstrapArtifactAction(action) {
		return errBootstrapSource
	}
	// Earlier artifact actions require actual service-file absence even when
	// systemd still caches not-found after a foreign or interrupted file write.
	starting := bootstrapK3sStartupAction(action)
	runtime := starting && !(initial && action == "start_prepared_k3s")
	configuration := bootstrapK3sConfigurationAction(action) || starting
	freshService := !configuration && !bootstrapK3sAction(action) || initial && action == "install_prejoin_k3s"
	if verifyBootstrapK3sFilePrefix(root, expected, configuration, freshService) != nil {
		return errBootstrapSource
	}
	for _, path := range []string{"etc/borealis", "var/lib/kubelet", "etc/kubernetes"} {
		if !bootstrapDockerPathAbsent(root, path) {
			if runtime && path != "etc/borealis" {
				if _, err := bootstrapHostFileAncestors(root, []bootstrapHostFile{{path: filepath.Join(path, "observation")}}); err == nil {
					continue
				}
			}
			return errBootstrapSource
		}
	}
	hostErr := proveBootstrapHostFilePrefix(root, expected, true)
	if configuration {
		hostErr = verifyBootstrapK3sJoinObservedPhase(root, expected, initial && action == "configure_prejoin_k3s", runtime)
	}
	if absent, err := bootstrapSourcePath(root, "opt/Borealis"); err != nil || absent || hostErr != nil {
		return errBootstrapSource
	}
	if initial && action == "publish_prejoin_artifacts" {
		if !bootstrapDockerPathAbsent(root, "var/lib/rancher/k3s") || !bootstrapDockerPathAbsent(root, "usr/local/bin/k3s") {
			return errBootstrapSource
		}
	}
	archives, pending := 0, 0
	seen := map[string]bool{}
	for _, directory := range []string{"var/lib/rancher/k3s", "var/lib/rancher/k3s/agent", "var/lib/rancher/k3s/agent/images"} {
		if bootstrapDockerPathAbsent(root, directory) {
			continue
		}
		if _, err := bootstrapHostFileAncestors(root, []bootstrapHostFile{{path: filepath.Join(directory, "observation")}}); err != nil {
			return errBootstrapSource
		}
		info, err := root.Lstat(directory)
		if err != nil || info.Mode().Perm() != 0700 {
			return errBootstrapSource
		}
		// K3s owns its payload/datastore/agent directories after startup. Keep
		// archive directory exact; complete transaction still hashes all37 files.
		if runtime && directory != "var/lib/rancher/k3s/agent/images" {
			continue
		}
		entries, err := bootstrapArtifactDirectory(root, directory, 36)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if directory != "var/lib/rancher/k3s/agent/images" {
				want := "agent"
				if directory == "var/lib/rancher/k3s/agent" {
					want = "images"
				}
				if entry.Name() != want || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					return errBootstrapSource
				}
				continue
			}
			name := entry.Name()
			if bootstrapArtifactScratch.MatchString(name) {
				pending++
			} else {
				role := bootstrapArtifactArchiveRole(name)
				if role == "" || seen[role] {
					return errBootstrapSource
				}
				seen[role] = true
				archives++
			}
			if bootstrapArtifactFileMetadata(root, filepath.Join(directory, name), 0600, clusterbootstrap.MaxImageArchiveBytes, false) != nil {
				return errBootstrapSource
			}
		}
	}
	if archives > 34 || pending > 1 {
		return errBootstrapSource
	}
	entries, err := bootstrapArtifactDirectory(root, "usr/local/bin", 4097)
	if err != nil {
		return err
	}
	binarySeen := false
	for _, entry := range entries {
		name := entry.Name()
		if name != "k3s" && !strings.HasPrefix(name, ".borealis-artifact-") {
			continue
		}
		if archives != 34 || pending != 0 || binarySeen {
			return errBootstrapSource
		}
		binarySeen = true
		mode := os.FileMode(0755)
		if name != "k3s" {
			if !bootstrapArtifactScratch.MatchString(name) {
				return errBootstrapSource
			}
			pending++
			info, err := root.Lstat(filepath.Join("usr/local/bin", name))
			if err != nil {
				return errBootstrapSource
			}
			mode = info.Mode()
			if mode != 0600 && mode != 0755 {
				return errBootstrapSource
			}
		}
		if bootstrapArtifactFileMetadata(root, filepath.Join("usr/local/bin", name), mode, clusterbootstrap.K3sPins().Binary.Size, name == "k3s") != nil {
			return errBootstrapSource
		}
	}
	if pending > 1 {
		return errBootstrapSource
	}
	return nil
}

func bootstrapArtifactArchiveRole(name string) string {
	if name == clusterbootstrap.K3sArchiveName || name == "borealis-node-postgresql-linux-amd64.oci.tar" {
		return name
	}
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if name == clusterbootstrap.ExternalImageAssetName(pin.Reference) {
			return name
		}
	}
	for _, role := range clusterbootstrap.ImageRoles() {
		if suffix, ok := strings.CutPrefix(name, "borealis-"+role+"-"); ok && bootstrapArtifactSuffix.MatchString(suffix) {
			return role
		}
	}
	return ""
}

func bootstrapArtifactDirectory(root *os.Root, path string, bound int) ([]os.DirEntry, error) {
	if _, err := bootstrapHostFileAncestors(root, []bootstrapHostFile{{path: filepath.Join(path, "observation")}}); err != nil {
		return nil, errBootstrapSource
	}
	dir, err := root.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errBootstrapSource
	}
	entries, readErr := dir.ReadDir(bound)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) >= bound {
		return nil, errBootstrapSource
	}
	return entries, nil
}

func bootstrapArtifactFileMetadata(root *os.Root, path string, mode os.FileMode, maximum int64, exact bool) error {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode() != mode || info.Size() < 0 || info.Size() > maximum || exact && info.Size() != maximum {
		return errBootstrapSource
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() || owner.Nlink != 1 {
		return errBootstrapSource
	}
	return nil
}
