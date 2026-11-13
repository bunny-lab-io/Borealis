package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

type bootstrapHostFile struct {
	path    string // Fixed root-relative path, never supplied by a request.
	mode    os.FileMode
	data    []byte
	pending string // Deterministic name bound to the durable request, not a glob.
}

// This is the fixed central-SSH baseline, not Engine.sh startup or a target's
// ambient configuration. Source-owned validated network settings are the only
// variable bytes. Pure renderer parity tests keep the existing install contract
// synchronized. Rendering alone grants no file/service mutation authority.
func bootstrapPrejoinHostFiles(expected clusterbootstrap.PreparationExpected) ([]bootstrapHostFile, error) {
	rendered, err := clusterbootstrap.RenderPreparationHostFiles(expected)
	if err != nil {
		return nil, errBootstrapSource
	}
	files := bootstrapHostFileSpecs(rendered)
	// Match the outer journal's stable target identity across worker takeover.
	// A crash leaves a precisely discoverable request-owned scratch name, never
	// authority to delete arbitrary similarly prefixed files. Fresh writes still
	// refuse any existing scratch; cleanup/recovery needs distinct executor proof.
	target := expected.Target
	target.HolderID, target.Generation, target.OperationAttempt = "", 0, 0
	for i := range files {
		raw, err := json.Marshal(struct {
			Domain, Source, Path string
			Target               clusterbootstrap.SessionBinding
			Mode                 os.FileMode
			Data                 []byte
		}{"prejoin-host-file-v1", expected.Source.SourceSHA, files[i].path, target, files[i].mode, files[i].data})
		if err != nil {
			return nil, errBootstrapSource
		}
		digest := sha256.Sum256(raw)
		files[i].pending = ".borealis-host-" + hex.EncodeToString(digest[:])
	}
	return files, nil
}

func bootstrapHostFileSpecs(rendered []clusterbootstrap.PreparationHostFile) []bootstrapHostFile {
	files := make([]bootstrapHostFile, len(rendered))
	for i, file := range rendered {
		files[i] = bootstrapHostFile{path: file.Path, mode: file.Mode, data: file.Data}
	}
	return files
}

const bootstrapPrejoinFirewall = clusterbootstrap.PreparationHostFirewall
