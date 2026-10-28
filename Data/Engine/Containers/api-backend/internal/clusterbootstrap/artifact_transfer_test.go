package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparationArtifactsFixture(t *testing.T) (*PreparationArtifacts, K3sInputPins, []ExternalImagePin) {
	t.Helper()
	expected, _ := imageInventoryFixture(t)
	return preparationArtifactsForSourceFixture(t, expected)
}

func preparationArtifactsForSourceFixture(t *testing.T, expected Expected) (*PreparationArtifacts, K3sInputPins, []ExternalImagePin) {
	t.Helper()
	ctx, check := context.Background(), func(context.Context) error { return nil }
	application, _ := stagedImagesForSourceFixture(t, expected)
	ki, kp, assets := k3sStageFixture(t)
	ki.wire.Repository, ki.wire.Release, ki.wire.SourceSHA = expected.Repository, expected.Release, expected.SourceSHA
	kp.Version = K3sPins().Version
	ki.wire.K3sVersion = kp.Version
	ki.raw, _ = json.Marshal(ki.wire)
	k3s, err := stageK3sInputs(ctx, t.TempDir(), ki, func(_ context.Context, name string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(assets[name])), nil
	}, check, kp, reserveImageFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k3s.Close() })
	ew := externalInventoryWire{Version: 1, Repository: expected.Repository, Release: expected.Release, SourceSHA: expected.SourceSHA, Platform: "linux-amd64"}
	archives, pins := map[string][]byte{}, []ExternalImagePin{}
	for _, compiled := range ExternalImagePins() {
		raw, pin := externalFixtureReference(t, "gzip", compiled.Reference)
		proof, err := inspectExternalImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pin)
		if err != nil {
			t.Fatal(err)
		}
		archives[pin.Reference] = raw
		pins = append(pins, pin)
		ew.Images = append(ew.Images, proof)
	}
	raw, _ := json.Marshal(ew)
	ei, err := parseExternalImageInventory(raw, expected, pins)
	if err != nil {
		t.Fatal(err)
	}
	external, err := stageExternalImages(ctx, t.TempDir(), ei, func(_ context.Context, p ExternalImageProof) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archives[p.Reference])), nil
	}, check,
		func(ctx context.Context, in io.ReaderAt, size int64, reference string) (ExternalImageProof, error) {
			for _, pin := range pins {
				if pin.Reference == reference {
					return inspectExternalImageArchive(ctx, in, size, pin)
				}
			}
			return ExternalImageProof{}, ErrImageArchive
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = external.Close() })
	reference, blobs := postgresImageFixture(t, "gzip", false)
	postgres, err := acquirePostgresImage(ctx, t.TempDir(), reference, check, func(_ context.Context, digest string, _ int64, _ bool, out io.Writer) error {
		_, err := out.Write(blobs[digest])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Close() })
	a, err := NewPreparationArtifacts(ctx, expected, kp.Version, reference, application, k3s, external, postgres, check)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, kp, pins
}

func TestPreparationArtifactSetOwnership(t *testing.T) {
	for _, mode := range []string{"complete", "missing application", "missing K3s", "missing external", "missing postgres", "closed K3s", "mixed application", "mixed external", "mixed K3s", "wrong postgres", "aliased postgres", "lost authority", "writer failure"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := preparationArtifactsFixture(t)
			app, k3s, external, postgres := a.application, a.k3s, a.external, a.postgres
			check := func(context.Context) error { return nil }
			switch mode {
			case "missing application":
				app = nil
			case "missing K3s":
				k3s = nil
			case "missing external":
				external = nil
			case "missing postgres":
				postgres = nil
			case "closed K3s":
				k3s.Close()
			case "mixed application":
				app.inventory.wire.SourceSHA = strings.Repeat("a", 40)
			case "mixed external":
				external.inventory.wire.Release = "2026.09.999-rc.2"
			case "mixed K3s":
				k3s.inventory.wire.Repository = "foreign/Borealis"
			case "wrong postgres":
				a.postgresReference = PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64)
			case "aliased postgres":
				postgres = &PostgresImage{set: external, proof: a.postgres.proof}
			case "lost authority":
				check = func(context.Context) error { return ErrSessionAuthority }
			}
			result, err := NewPreparationArtifacts(context.Background(), a.expected, a.k3sVersion, a.postgresReference, app, k3s, external, postgres, check)
			valid := mode == "complete" || mode == "writer failure"
			if (err == nil) != valid {
				t.Fatal("incorrect complete-set binding", err)
			}
			if !valid {
				if result != nil || a.application.root == nil || a.external.root == nil || a.postgres.set.root == nil {
					t.Fatal("failed constructor consumed caller ownership")
				}
				return
			}
			if _, err := json.Marshal(result); err == nil {
				t.Fatal("generic serialization accepted")
			}
			if strings.Contains(fmt.Sprintf("%v %#v", result, result), a.application.path) {
				t.Fatal("scratch path escaped")
			}
			if mode == "writer failure" && result.WriteArtifacts(context.Background(), externalStageFailWriter{}, check) == nil {
				t.Fatal("writer failure hidden")
			}
			paths := []string{app.path, k3s.path, external.path, postgres.set.path}
			if result.Close() != nil || result.Close() != nil {
				t.Fatal("cleanup failed")
			}
			for _, path := range paths {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("owned scratch retained", err)
				}
			}
		})
	}
}

func TestPreparationArtifactTransfer(t *testing.T) {
	a, kp, ep := preparationArtifactsFixture(t)
	check := func(context.Context) error { return nil }
	var stream bytes.Buffer
	if a.WriteArtifacts(context.Background(), &stream, check) != nil {
		t.Fatal("send failed")
	}
	for _, mode := range []string{"complete", "truncated", "wrong source", "wrong postgres", "wrong version", "unknown identity", "duplicate identity", "mixed application", "mixed K3s", "mixed external", "mixed postgres", "changed trailer", "authority after two families", "public pins"} {
		t.Run(mode, func(t *testing.T) {
			data, expected, pg, version := bytes.Clone(stream.Bytes()), a.expected, a.postgresReference, a.k3sVersion
			switch mode {
			case "truncated":
				data = data[:len(data)-5]
			case "wrong source":
				expected.SourceSHA = strings.Repeat("a", 40)
			case "wrong postgres":
				pg = PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64)
			case "wrong version":
				version = "unknown"
			case "changed trailer":
				data[len(data)-1] ^= 1
			case "mixed application", "mixed K3s", "mixed external", "mixed postgres":
				magic := map[string]string{"mixed application": imageTransferMagic, "mixed K3s": k3sTransferMagic, "mixed external": externalTransferMagic, "mixed postgres": postgresTransferMagic}[mode]
				offset := bytes.Index(data, []byte(magic))
				if offset < 0 {
					t.Fatal("missing frame")
				}
				data[offset] ^= 1
			case "unknown identity", "duplicate identity":
				offset := len(artifactTransferMagic) + 4
				size := int(binary.BigEndian.Uint32(data[len(artifactTransferMagic):offset]))
				raw := string(data[offset : offset+size])
				field := `"unknown":1,`
				if mode == "duplicate identity" {
					field = `"version":1,`
				}
				raw = strings.Replace(raw, "{", "{"+field, 1)
				next := append(bytes.Clone(data[:offset]), []byte(raw)...)
				next = append(next, data[offset+size:]...)
				data = next
				binary.BigEndian.PutUint32(data[len(artifactTransferMagic):], uint32(len(raw)))
				digest := sha256.Sum256([]byte(raw))
				copy(data[len(data)-sha256.Size:], digest[:])
			}
			parent := t.TempDir()
			marker := filepath.Join(parent, "caller-owned")
			if os.WriteFile(marker, []byte("retain"), 0600) != nil {
				t.Fatal("marker failed")
			}
			reader := bytes.NewReader(append(data, []byte("next-frame")...))
			authority := func(context.Context) error {
				entries, _ := os.ReadDir(parent)
				if mode == "authority after two families" && len(entries) >= 4 {
					return ErrSessionAuthority
				}
				return nil
			}
			var received *PreparationArtifacts
			var err error
			if mode == "public pins" {
				received, err = ReceivePreparationArtifacts(context.Background(), parent, expected, version, pg, reader, authority)
			} else {
				received, err = receivePreparationArtifacts(context.Background(), parent, expected, version, pg, reader, authority, kp, ep)
			}
			if mode != "complete" {
				entries, readErr := os.ReadDir(parent)
				if err == nil || received != nil || readErr != nil || len(entries) != 1 || entries[0].Name() != "caller-owned" {
					t.Fatal("partial aggregate escaped or cleanup failed", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			left, _ := io.ReadAll(reader)
			if string(left) != "next-frame" {
				t.Fatal("consumed next frame")
			}
			var replay bytes.Buffer
			if received.WriteArtifacts(context.Background(), &replay, check) != nil || !bytes.Equal(replay.Bytes(), stream.Bytes()) {
				t.Fatal("complete bundle changed in transport")
			}
			if received.Close() != nil {
				t.Fatal("cleanup failed")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 {
				t.Fatal("completed scratch retained")
			}
		})
	}
}
