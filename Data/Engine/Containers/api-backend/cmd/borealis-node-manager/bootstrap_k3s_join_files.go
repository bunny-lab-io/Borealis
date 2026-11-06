package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
)

type bootstrapK3sJoinFiles struct{ files []bootstrapHostFile }

func (bootstrapK3sJoinFiles) String() string               { return "private K3s join files [redacted]" }
func (bootstrapK3sJoinFiles) GoString() string             { return "private K3s join files [redacted]" }
func (bootstrapK3sJoinFiles) MarshalJSON() ([]byte, error) { return nil, errBootstrapSource }

// Borrow private token bytes only inside synchronous file/proof operation.
// Exact claim/source is rechecked before export and after consumer. A retained
// plan cannot retain token bytes after return; it is never generic JSON output.
func withBootstrapK3sJoinFiles(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error, consume func(bootstrapK3sJoinFiles) error) error {
	config, err := clusterbootstrap.PrejoinK3sJoinConfiguration(expected)
	if err != nil || material == nil || check == nil || consume == nil || material.Verify(ctx, expected, source, check) != nil {
		return errBootstrapSource
	}
	storage := make([]byte, clusterbootstrap.MaxK3sJoinTokenBytes)
	defer clear(storage)
	token := bytes.NewBuffer(storage[:0])
	if material.WriteToken(ctx, expected, source, check, token) != nil || token.Len() < 1 || token.Len() > len(storage) {
		return errBootstrapSource
	}
	plan := bootstrapK3sJoinFiles{[]bootstrapHostFile{
		{path: strings.TrimPrefix(clusterbootstrap.PrejoinK3sJoinTokenPath, "/"), mode: 0600, data: token.Bytes()},
		{path: strings.TrimPrefix(clusterbootstrap.PrejoinK3sJoinConfigPath, "/"), mode: 0600, data: []byte(config)},
	}}
	target := expected.Target
	target.HolderID, target.Generation, target.OperationAttempt = "", 0, 0
	contentDigests := make([][sha256.Size]byte, len(plan.files))
	metadata := make([]bootstrapHostFile, len(plan.files))
	for i := range plan.files {
		file := &plan.files[i]
		// Hash private bytes directly, never marshal a second plaintext copy.
		content := sha256.Sum256(file.data)
		contentDigests[i] = content
		raw, err := json.Marshal(struct {
			Domain, Revision, Path, Content string
			Target                          clusterbootstrap.SessionBinding
			Source                          engineidentity.Binding
			Mode                            os.FileMode
		}{"prejoin-k3s-join-file-v1", expected.Source.SourceSHA, file.path, hex.EncodeToString(content[:]), target, source, file.mode})
		if err != nil {
			return errBootstrapSource
		}
		digest := sha256.Sum256(raw)
		file.pending = ".borealis-k3s-join-" + hex.EncodeToString(digest[:])
		metadata[i] = *file
	}
	if consume(plan) != nil || material.Verify(ctx, expected, source, check) != nil {
		return errBootstrapSource
	}
	for i, file := range plan.files {
		if file.path != metadata[i].path || file.mode != metadata[i].mode || file.pending != metadata[i].pending || sha256.Sum256(file.data) != contentDigests[i] {
			return errBootstrapSource
		}
	}
	return nil
}
