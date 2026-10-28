package clusterbootstrap

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparationArtifactFilePublication(t *testing.T) {
	for _, sourceFile := range []bool{false, true} {
		for _, mode := range []string{"complete", "final collision", "scratch collision", "short writer", "oversized writer", "changed bytes", "lost authority", "parent replacement", "late final collision"} {
			t.Run(mode+map[bool]string{false: "/archive", true: "/source"}[sourceFile], func(t *testing.T) {
				root, err := os.OpenRoot(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				name := "var/lib/rancher/k3s/agent/images/fixture.tar"
				if sourceFile {
					name = prejoinK3sInstaller
				}
				parent := filepath.Dir(name)
				if root.MkdirAll(parent, 0700) != nil {
					t.Fatal("parent setup")
				}
				raw := []byte("immutable file bytes")
				file := artifactProofFixture(name, raw)
				input := mutationDigest("bound complete image intent")
				pending := filepath.Join(parent, prejoinArtifactPending(input, name))
				lost, injected := false, false
				check := func(context.Context) error {
					if lost {
						return ErrSessionAuthority
					}
					if sourceFile {
						if _, err := root.Lstat(pending); err == nil && provePrejoinArtifactFile(context.Background(), root, pending, file) != nil {
							return ErrMutationUnknown
						}
					}
					if mode == "late final collision" && !injected {
						if provePrejoinArtifactFile(context.Background(), root, pending, file) == nil {
							injected = true
							if root.WriteFile(name, []byte("foreign final"), 0600) != nil {
								t.Fatal("collision fixture")
							}
						}
					}
					return nil
				}
				file.write = func(ctx context.Context, out io.Writer, current func(context.Context) error) error {
					if current(ctx) != nil {
						return ErrSessionAuthority
					}
					data := raw
					switch mode {
					case "short writer":
						data = raw[:len(raw)-1]
					case "oversized writer":
						data = append(bytes.Clone(raw), 'x')
					case "changed bytes":
						data = bytes.Repeat([]byte("x"), len(raw))
					case "parent replacement":
						if root.Rename(parent, parent+".retained") != nil || root.Mkdir(parent, 0700) != nil {
							t.Fatal("parent replacement")
						}
					}
					_, err := out.Write(data)
					if mode == "lost authority" {
						lost = true
					}
					if err != nil || current(ctx) != nil {
						return ErrImageArchive
					}
					return nil
				}
				switch mode {
				case "final collision":
					root.WriteFile(name, []byte("foreign final"), 0600)
				case "scratch collision":
					root.WriteFile(pending, []byte("retained evidence"), 0600)
				}
				err = publishPrejoinArtifactFile(context.Background(), root, file, input, check)
				if (err == nil) != (mode == "complete") {
					t.Fatal("incorrect publication", err)
				}
				if mode == "complete" {
					if provePrejoinArtifactFile(context.Background(), root, name, file) != nil {
						t.Fatal("published bytes differ")
					}
					if _, err := root.Lstat(pending); !os.IsNotExist(err) {
						t.Fatal("successful scratch remains")
					}
				} else if mode == "final collision" || mode == "late final collision" {
					got, err := root.ReadFile(name)
					if err != nil || string(got) != "foreign final" {
						t.Fatal("foreign final overwritten")
					}
				} else if mode == "scratch collision" {
					got, err := root.ReadFile(pending)
					if err != nil || string(got) != "retained evidence" {
						t.Fatal("retained scratch changed")
					}
				} else if _, err := root.Lstat(name); !os.IsNotExist(err) {
					t.Fatal("failed copy published final")
				}
			})
		}
	}
}

func TestPreparationArtifactSourceProofBeforePromotion(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := "var/lib/rancher/k3s/agent/images/fixture.tar"
	if root.MkdirAll(filepath.Dir(name), 0700) != nil {
		t.Fatal("parents")
	}
	file := artifactProofFixture(name, []byte("authenticated inert bytes"))
	file.write = func(ctx context.Context, out io.Writer, check func(context.Context) error) error {
		if check(ctx) != nil {
			return ErrSessionAuthority
		}
		_, err := out.Write([]byte("authenticated inert bytes"))
		return err
	}
	input := mutationDigest("source drift during large transfer")
	pending := filepath.Join(filepath.Dir(name), prejoinArtifactPending(input, name))
	check := func(context.Context) error { return nil }
	sourceProof := func(context.Context) error {
		if !artifactPathAbsent(root, pending) {
			return ErrPreparationConfig
		}
		return nil
	}
	if publishPrejoinArtifactFileGuarded(context.Background(), root, file, name, input, check, sourceProof) == nil {
		t.Fatal("source drift accepted")
	}
	if !artifactPathAbsent(root, name) || provePrejoinArtifactFile(context.Background(), root, pending, file) != nil {
		t.Fatal("changed source promoted final or erased complete scratch")
	}
}
