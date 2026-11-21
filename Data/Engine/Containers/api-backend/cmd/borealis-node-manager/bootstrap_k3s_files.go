package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

func bootstrapPrejoinK3sFiles(expected clusterbootstrap.PreparationExpected) ([]bootstrapHostFile, error) {
	pins := clusterbootstrap.K3sPins()
	if expected.Validate() != nil || expected.K3sVersion != pins.Version {
		return nil, errBootstrapSource
	}
	// Publish environment first. An interrupted prefix never exposes a service
	// with missing or inherited environment. This still grants no start authority.
	files := []bootstrapHostFile{
		{path: "etc/systemd/system/k3s.service.env", mode: 0600, data: []byte{}},
		{path: "etc/systemd/system/k3s.service", mode: 0644, data: []byte(clusterbootstrap.PrejoinK3sServiceUnit)},
	}
	target := expected.Target
	target.HolderID, target.Generation, target.OperationAttempt = "", 0, 0
	for i := range files {
		raw, err := json.Marshal(struct {
			Domain, Source, Installer, Path string
			Target                          clusterbootstrap.SessionBinding
			Mode                            os.FileMode
			Data                            []byte
		}{"prejoin-k3s-service-file-v1", expected.Source.SourceSHA, pins.Installer.SHA256, files[i].path, target, files[i].mode, files[i].data})
		if err != nil {
			return nil, errBootstrapSource
		}
		digest := sha256.Sum256(raw)
		files[i].pending = ".borealis-k3s-" + hex.EncodeToString(digest[:])
	}
	return files, nil
}
