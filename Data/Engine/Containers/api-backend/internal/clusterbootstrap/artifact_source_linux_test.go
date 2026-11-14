package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func artifactProofFixture(path string, raw []byte) prejoinArtifactFile {
	digest := sha256.Sum256(raw)
	return prejoinArtifactFile{path: path, mode: 0600, size: int64(len(raw)), digest: hex.EncodeToString(digest[:])}
}

func TestPreparationArtifactFileProof(t *testing.T) {
	for _, mode := range []string{"complete", "changed bytes", "short", "mode", "link", "hardlink", "parent link", "parent mode", "renamed root", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if root.Mkdir("dir", 0700) != nil || root.WriteFile("dir/file", []byte("pinned bytes"), 0600) != nil {
				t.Fatal("fixture failed")
			}
			file := artifactProofFixture("dir/file", []byte("pinned bytes"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "changed bytes":
				root.WriteFile(file.path, []byte("changed data"), 0600)
			case "short":
				root.WriteFile(file.path, []byte("short"), 0600)
			case "mode":
				root.Chmod(file.path, 0755)
			case "link":
				root.Rename(file.path, "dir/real")
				root.Symlink("real", file.path)
			case "hardlink":
				root.Link(file.path, "dir/other")
			case "parent link":
				root.Rename("dir", "real")
				root.Symlink("real", "dir")
			case "parent mode":
				root.Chmod("dir", 0777)
			case "renamed root":
				if os.Rename(root.Name(), root.Name()+".retained") != nil || os.Mkdir(root.Name(), 0700) != nil {
					t.Fatal("rename fixture failed")
				}
				t.Cleanup(func() { os.RemoveAll(root.Name() + ".retained") })
			case "cancel":
				cancel()
			}
			if err := provePrejoinArtifactFile(ctx, root, file.path, file); (err == nil) != (mode == "complete") {
				t.Fatal("incorrect exact file proof", err)
			}
		})
	}
}

func TestPreparationArtifactSourceCustody(t *testing.T) {
	p, j, _, parent, install, material := prejoinHostFixture(t)
	ctx := context.Background()
	_, _, identity, err := p.prejoinHostRequest(ctx, j, install, material, parent)
	if err != nil {
		t.Fatal(err)
	}
	identity.files = material.Files()
	defer func() {
		for _, raw := range identity.files {
			clear(raw)
		}
	}()
	archive, err := os.Open(p.bundle.ArchivePath())
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	environment, err := p.configuration.Environment()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(environment)
	path := filepath.Join(parent, "Borealis")
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data := [][]byte{[]byte("pinned installer fixture"), []byte(`{"services":{}}`)}
	files := []prejoinArtifactFile{artifactProofFixture(prejoinK3sInstaller, data[0]), artifactProofFixture(prejoinImageManifest, data[1])}
	proof := &prejoinArtifactSourceProof{input: mutationDigest("artifact input"), plan: prejoinArtifactPlan{files: files, digest: mutationDigest("artifact plan")}}
	paths := []string{}
	for _, file := range files {
		name := strings.TrimPrefix(file.path, "opt/Borealis/")
		paths = append(paths, name, filepath.Join(filepath.Dir(name), prejoinArtifactPending(proof.input, file.path)))
	}
	paths = append(paths, "Engine/Deploy/foreign", "Engine/Deploy/.borealis-artifact-foreign")
	clearFiles := func() {
		for _, name := range paths {
			if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	defer clearFiles()
	for phase := 0; phase <= 4; phase++ {
		clearFiles()
		for i := range files {
			name := ""
			if phase >= (i+1)*2 {
				name = paths[i*2]
			} else if phase == i*2+1 {
				name = paths[i*2+1]
			}
			if name != "" && root.WriteFile(name, data[i], 0600) != nil {
				t.Fatal("fixture write")
			}
		}
		if err := verifyPublishedSourceWithArtifacts(ctx, archive, path, environment, &identity, proof); err != nil {
			t.Fatal("exact artifact prefix rejected", phase, err)
		}
		if err := verifyPublishedSourceWithIdentityRoot(ctx, archive, path, environment, &identity); (err == nil) != (phase == 0) {
			t.Fatal("earlier phase admitted artifact effects", phase, err)
		}
	}
	for _, mode := range []string{"changed installer", "unknown file", "foreign scratch", "out of order", "both names", "short scratch", "unsafe mode", "linked installer"} {
		t.Run(mode, func(t *testing.T) {
			clearFiles()
			if root.WriteFile(paths[0], data[0], 0600) != nil {
				t.Fatal("fixture write")
			}
			switch mode {
			case "changed installer":
				root.WriteFile(paths[0], []byte("changed"), 0600)
			case "unknown file":
				root.WriteFile(paths[4], []byte("preserve"), 0600)
			case "foreign scratch":
				root.WriteFile(paths[5], data[1], 0600)
			case "out of order":
				root.Remove(paths[0])
				root.WriteFile(paths[2], data[1], 0600)
			case "both names":
				root.WriteFile(paths[1], data[0], 0600)
			case "short scratch":
				root.WriteFile(paths[3], []byte("partial"), 0600)
			case "unsafe mode":
				root.Chmod(paths[0], 0755)
			case "linked installer":
				root.Rename(paths[0], paths[4])
				root.Symlink("foreign", paths[0])
			}
			if verifyPublishedSourceWithArtifacts(ctx, archive, path, environment, &identity, proof) == nil {
				t.Fatal("unowned source effects accepted")
			}
		})
	}
}
