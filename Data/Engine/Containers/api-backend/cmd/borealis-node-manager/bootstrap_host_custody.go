package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func bootstrapHostAction(action string) bool {
	return action == "prepare_prejoin_host" || action == "complete_prejoin_host" || action == "complete_prejoin_firewall" || action == "reconcile_prejoin_host"
}

// Separate host-phase boundary. Earlier source/identity action allowlists stay
// unchanged. A new write request starts clean; only its in-flight checks accept
// the writer's exact ordered prefix. Reconciliation requires complete files and
// still must prove the entire host effect under the distinct-executor journal.
func verifyBootstrapHostPaths(root *os.Root, expected clusterbootstrap.PreparationExpected, action string, initial bool) error {
	if root == nil || expected.Validate() != nil || !bootstrapHostAction(action) {
		return errBootstrapSource
	}
	if action == "prepare_prejoin_host" && initial {
		if verifyBootstrapSourcePaths(root, "install_prejoin_identity", true) != nil ||
			preflightBootstrapHostFiles(context.Background(), root, expected, func(context.Context) error { return nil }) != nil {
			return errBootstrapSource
		}
		return nil
	}
	for _, path := range []string{"etc/borealis", "var/lib/rancher/k3s", "usr/local/bin/k3s", "var/lib/kubelet", "etc/kubernetes"} {
		if !bootstrapDockerPathAbsent(root, path) {
			return errBootstrapSource
		}
	}
	absent, err := bootstrapSourcePath(root, "opt/Borealis")
	if err != nil || absent {
		return errBootstrapSource
	}
	return proveBootstrapHostFilePrefix(root, expected, action == "reconcile_prejoin_host" || action == "complete_prejoin_firewall")
}

// The file writer calls custody only before creating scratch and after writing,
// chmod/fsync, or publication. Permit at most one complete scratch for the next
// unpublished file, never arbitrary/empty/partially written scratch. No recovery
// or write permission follows from a matching prefix.
func proveBootstrapHostFilePrefix(root *os.Root, expected clusterbootstrap.PreparationExpected, complete bool) error {
	files, err := bootstrapPrejoinHostFiles(expected)
	if err != nil || root == nil {
		return errBootstrapSource
	}
	allowed := map[string]map[string]bool{
		"etc/rancher/k3s":               {"config.yaml.d": true},
		"etc/rancher/k3s/config.yaml.d": {},
	}
	unpublished, scratch := false, false
	for _, file := range files {
		parent := filepath.Dir(file.path)
		if bootstrapHostParents(context.Background(), root, parent, func() error { return nil }, false) != nil {
			return errBootstrapSource
		}
		pending := file
		pending.path = filepath.Join(parent, file.pending)
		finalAbsent := bootstrapDockerPathAbsent(root, file.path)
		pendingAbsent := bootstrapDockerPathAbsent(root, pending.path)
		if !finalAbsent {
			if unpublished || !pendingAbsent || proveBootstrapHostFile(root, file) != nil {
				return errBootstrapSource
			}
			if names := allowed[parent]; names != nil {
				names[filepath.Base(file.path)] = false
			}
			continue
		}
		if complete || !pendingAbsent && (unpublished || scratch || proveBootstrapHostFile(root, pending) != nil) {
			return errBootstrapSource
		}
		unpublished = true
		if !pendingAbsent {
			scratch = true
			if names := allowed[parent]; names != nil {
				names[file.pending] = false
			}
		}
	}
	// Exclusive bounded K3s inventory: no tokens, extra drop-ins or unknown
	// directories can hide alongside the two expected generated files.
	for _, path := range []string{"etc/rancher/k3s", "etc/rancher/k3s/config.yaml.d"} {
		if bootstrapDockerPathAbsent(root, path) {
			if complete {
				return errBootstrapSource
			}
			continue
		}
		if _, err := bootstrapHostFileAncestors(root, []bootstrapHostFile{{path: filepath.Join(path, "observation")}}); err != nil {
			return errBootstrapSource
		}
		dir, err := root.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errBootstrapSource
		}
		entries, readErr := dir.ReadDir(4)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > len(allowed[path]) {
			return errBootstrapSource
		}
		for _, entry := range entries {
			directory, exists := allowed[path][entry.Name()]
			if !exists || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() != directory {
				return errBootstrapSource
			}
		}
	}
	return nil
}

// Inspection accepts bounded possible preparation state only to read the
// journal cursor. It grants no writes or completed-effect proof. Subsequent
// phase reconciliation must independently prove source/account/keys/host state.
func verifyBootstrapProgressPaths(root *os.Root, expected clusterbootstrap.PreparationExpected) error {
	if root == nil || expected.Validate() != nil {
		return errBootstrapSource
	}
	for _, path := range []string{"etc/borealis", "var/lib/rancher/k3s", "usr/local/bin/k3s", "var/lib/kubelet", "etc/kubernetes"} {
		if !bootstrapDockerPathAbsent(root, path) {
			return errBootstrapSource
		}
	}
	absent, err := bootstrapSourcePath(root, "opt/Borealis")
	if err != nil {
		return errBootstrapSource
	}
	if absent {
		return verifyBootstrapSourcePaths(root, "stage_source", true)
	}
	return proveBootstrapHostFilePrefix(root, expected, false)
}
