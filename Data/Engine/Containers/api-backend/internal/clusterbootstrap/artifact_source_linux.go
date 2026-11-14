package clusterbootstrap

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Only the artifact transaction may permit these two generated source extras.
// Earlier source/account/identity/host verification retains its empty allowlist.
type prejoinArtifactSourceProof struct {
	input string
	plan  prejoinArtifactPlan
}

func (p *prejoinArtifactSourceProof) consume(ctx context.Context, root *os.Root, actual map[string]fs.FileInfo) error {
	if p == nil || !digestPattern.MatchString(p.input) || !digestPattern.MatchString(p.plan.digest) {
		return ErrPreparationConfig
	}
	var files []prejoinArtifactFile
	for _, file := range p.plan.files {
		if strings.HasPrefix(file.path, "opt/Borealis/") {
			files = append(files, file)
		}
	}
	if len(files) != 2 || files[0].path != prejoinK3sInstaller || files[1].path != prejoinImageManifest {
		return ErrPreparationConfig
	}
	unpublished := false
	for _, file := range files {
		if file.mode != 0600 {
			return ErrPreparationConfig
		}
		name := strings.TrimPrefix(file.path, "opt/Borealis/")
		pending := filepath.Join(filepath.Dir(name), prejoinArtifactPending(p.input, file.path))
		_, finalPresent := actual[name]
		_, pendingPresent := actual[pending]
		if finalPresent {
			if unpublished || pendingPresent || provePrejoinArtifactFile(ctx, root, name, file) != nil {
				return ErrPreparationConfig
			}
			delete(actual, name)
			continue
		}
		if pendingPresent {
			if unpublished || provePrejoinArtifactFile(ctx, root, pending, file) != nil {
				return ErrPreparationConfig
			}
			delete(actual, pending)
		}
		unpublished = true
	}
	return nil
}
