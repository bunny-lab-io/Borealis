package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"os"
	"path/filepath"
	"regexp"
)

var bootstrapK3sJoinScratch = regexp.MustCompile(`^\.borealis-k3s-join-[a-f0-9]{64}$`)

// Pre-stream/cursor observation only. It cannot authenticate token bytes before
// original-source material arrives. Exact private prefix proof and journal are
// mandatory before any effect/receipt; this grants no adoption or write replay.
func verifyBootstrapK3sJoinObservedPrefix(root *os.Root, expected clusterbootstrap.PreparationExpected, fresh bool) error {
	if root == nil {
		return errBootstrapSource
	}
	files, err := bootstrapPrejoinHostFiles(expected)
	config, configErr := clusterbootstrap.PrejoinK3sJoinConfiguration(expected)
	if err != nil || configErr != nil {
		return errBootstrapSource
	}
	if _, err := bootstrapHostFileAncestors(root, files); err != nil {
		return errBootstrapSource
	}
	for _, file := range files {
		if proveBootstrapHostFile(root, file) != nil {
			return errBootstrapSource
		}
	}
	token, configSeen, scratch := false, false, 0
	for _, directory := range []string{"etc/rancher/k3s", "etc/rancher/k3s/config.yaml.d"} {
		entries, err := bootstrapArtifactDirectory(root, directory, 5)
		if err != nil {
			return errBootstrapSource
		}
		for _, entry := range entries {
			name := entry.Name()
			if directory == "etc/rancher/k3s" && name == "config.yaml.d" {
				if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					return errBootstrapSource
				}
				continue
			}
			if directory == "etc/rancher/k3s" && name == "registries.yaml" || directory == "etc/rancher/k3s/config.yaml.d" && name == "10-borealis.yaml" {
				continue
			}
			if fresh {
				return errBootstrapSource
			}
			path := filepath.Join(directory, name)
			pending := bootstrapK3sJoinScratch.MatchString(name)
			if directory == "etc/rancher/k3s" {
				if name != "borealis-server.token" && !pending || token || scratch != 0 || bootstrapArtifactFileMetadata(root, path, 0600, clusterbootstrap.MaxK3sJoinTokenBytes, false) != nil {
					return errBootstrapSource
				}
				if pending {
					scratch++
				} else {
					token = true
				}
			} else {
				if name != "20-borealis-cluster-join.yaml" && !pending || !token || configSeen || scratch != 0 || proveBootstrapHostFile(root, bootstrapHostFile{path: path, mode: 0600, data: []byte(config)}) != nil {
					return errBootstrapSource
				}
				if pending {
					scratch++
				} else {
					configSeen = true
				}
			}
		}
	}
	// Explicitly require baseline entries even when directory enumeration was
	// raced; byte proof above plus final fixed-path lstat exclude disappearance.
	for _, file := range files {
		if _, err := root.Lstat(file.path); err != nil {
			return errBootstrapSource
		}
	}
	return nil
}
