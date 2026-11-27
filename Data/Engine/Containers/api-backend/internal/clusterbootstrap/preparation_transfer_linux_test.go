package clusterbootstrap

import (
	"borealis/api-backend/internal/testsshpipe"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"

	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumePreparationRequiresCompleteBoundInput(t *testing.T) {
	p, journal, _, destination := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	_, manager, _, _ := p.bundle.ManagerProof()
	var wire bytes.Buffer
	if err := p.WritePreparation(context.Background(), expected, &wire, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"trailing", "truncated", "helper mismatch", "consumer failure", "valid"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(wire.Bytes())
			digest := manager
			if mode == "trailing" {
				raw = append(raw, 'x')
			}
			if mode == "truncated" {
				raw = raw[:len(raw)-1]
			}
			if mode == "helper mismatch" {
				digest = strings.Repeat("0", 64)
			}
			parent := mutationTempDir(t)
			called := false
			err := ConsumePreparation(context.Background(), parent, expected, digest, bytes.NewReader(raw), mutationAuthority, func(ctx context.Context, inputs *PreparationInputs) error {
				called = true
				if mode == "consumer failure" {
					return errors.New("private consumer failure")
				}
				_, err := inputs.publishSource(ctx, journal, destination)
				return err
			})
			if (err == nil) != (mode == "valid") {
				t.Fatal("unexpected consumption result", err)
			}
			if called != (mode == "valid" || mode == "consumer failure") {
				t.Fatal("unverified stream reached consumer")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("scratch retained")
			}
		})
	}
}

func TestPreparationTransferAndAtomicPublication(t *testing.T) {
	p, j, _, destination := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	var stream bytes.Buffer
	if err := p.WritePreparation(context.Background(), expected, &stream, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	// The development descriptor authenticates the original inventory bytes,
	// including JSON field order and whitespace. Both ends must see that identity.
	header := bytes.NewReader(stream.Bytes()[len(preparationTransferMagic):])
	if _, err := readPreparationTransferField(header, MaxPreparationConfigBytes); err != nil {
		t.Fatal(err)
	}
	inventory, err := readPreparationTransferField(header, MaxManifestBytes)
	if err != nil || string(inventory) != p.bundle.rawManifest {
		t.Fatal("sender changed the verified bootstrap inventory", err)
	}
	stream.WriteString("next-frame")
	parent := mutationTempDir(t)
	received, err := ReceivePreparation(context.Background(), parent, expected, &stream, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer received.Close()
	if stream.String() != "next-frame" {
		t.Fatal("transfer consumed following frame")
	}
	want, _ := p.configuration.Export()
	got, _ := received.configuration.Export()
	if !bytes.Equal(want, got) {
		t.Fatal("configuration changed")
	}
	if _, err := received.publishSource(context.Background(), j, destination); err != nil {
		t.Fatal(err)
	}
	environment, _ := p.configuration.Environment()
	for _, name := range []string{"compose.env", "runtime.env"} {
		raw, err := os.ReadFile(filepath.Join(destination, "Borealis", "Engine", "Deploy", name))
		if err != nil || !bytes.Equal(raw, environment) {
			t.Fatal("published environment changed", err)
		}
	}
	if err := received.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("receiver scratch retained")
	}
}

func TestPreparationTransferRejectsUnboundOrInterruptedStream(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	var stream bytes.Buffer
	if err := p.WritePreparation(context.Background(), expected, &stream, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	base := stream.Bytes()
	configStart := len(preparationTransferMagic) + 4
	configSize := int(binary.BigEndian.Uint32(base[len(preparationTransferMagic):]))
	manifestStart := configStart + configSize + 4
	manifestSize := int(binary.BigEndian.Uint32(base[manifestStart-4:]))
	archiveStart := manifestStart + manifestSize
	for _, mode := range []string{"magic", "zero configuration", "oversized configuration", "truncated configuration", "configuration bytes", "oversized manifest", "manifest bytes", "archive bytes", "truncated archive", "trailer", "target claim", "source", "authority", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(base)
			want := expected
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "magic":
				raw[0] ^= 1
			case "zero configuration":
				binary.BigEndian.PutUint32(raw[len(preparationTransferMagic):], 0)
			case "oversized configuration":
				binary.BigEndian.PutUint32(raw[len(preparationTransferMagic):], MaxPreparationConfigBytes+1)
			case "truncated configuration":
				raw = raw[:configStart+3]
			case "configuration bytes":
				raw[configStart] = '['
			case "oversized manifest":
				binary.BigEndian.PutUint32(raw[manifestStart-4:], MaxManifestBytes+1)
			case "manifest bytes":
				raw[manifestStart] = '['
			case "archive bytes":
				raw[archiveStart+50] ^= 1
			case "truncated archive":
				raw = raw[:archiveStart+100]
			case "trailer":
				raw[len(raw)-1] ^= 1
			case "target claim":
				want.Target.Generation++
			case "source":
				want.Source.Release = "2026.09.999-rc.2"
			case "cancellation":
				cancel()
			}
			parent := mutationTempDir(t)
			calls := 0
			check := func(context.Context) error {
				calls++
				if mode == "authority" && calls >= 3 {
					return errors.New("private-error")
				}
				return nil
			}
			got, err := ReceivePreparation(ctx, parent, want, bytes.NewReader(raw), check)
			if err == nil || got != nil || err.Error() != "node preparation configuration invalid or changed; private data withheld" {
				t.Fatal("invalid input escaped", err)
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("failed transfer retained scratch")
			}
		})
	}
}

type preparationRejectWriter struct{ bytes int }

func (w *preparationRejectWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	return 0, errors.New("private sink error")
}

func TestPreparationTransferWriterFencesAndPrivacy(t *testing.T) {
	for _, mode := range []string{"changed archive", "changed claim", "authority", "output failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			p, _, _, _ := sourcePublicationFixture(t)
			expected := p.configuration.wire.Expected
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var output bytes.Buffer
			var writer io.Writer = &output
			check := mutationAuthority
			switch mode {
			case "changed archive":
				if err := os.WriteFile(p.bundle.ArchivePath(), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed claim":
				expected.Target.Generation++
			case "authority":
				check = func(context.Context) error { return errors.New("private authority error") }
			case "output failure":
				writer = &preparationRejectWriter{}
			case "cancelled":
				cancel()
			}
			if err := p.WritePreparation(ctx, expected, writer, check); err != ErrPreparationConfig {
				t.Fatal("private diagnostic escaped", err)
			}
			if output.Len() != 0 {
				t.Fatal("unauthorized input was transferred")
			}
		})
	}
}

// Exercise the native cursor and first installation effect across separate
// framed sessions. All writes and the real journal live under the test root.
func TestPreparationProgressToSourceInstallation(t *testing.T) {
	inputs, journal, journalPath, destination := sourcePublicationFixture(t)
	verifyPreparationProgressToSourceInstallation(t, inputs, journal, journalPath, destination)
}

func verifyPreparationProgressToSourceInstallation(t *testing.T, inputs *PreparationInputs, journal *MutationJournal, journalPath, destination string) {
	t.Helper()
	expected := inputs.configuration.wire.Expected
	_, manager, _, err := inputs.bundle.ManagerProof()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*SessionLifetime)
	defer cancel()
	for _, action := range []string{"inspect_preparation_progress", "stage_source"} {
		inspection := action == "inspect_preparation_progress"
		digit := "a"
		if !inspection {
			digit = "b"
		}
		request, err := NewPreparationSessionRequest(expected, manager, strings.Repeat(digit, 64), action)
		if err != nil {
			t.Fatal(err)
		}
		scratch := mutationTempDir(t)
		client, target := testsshpipe.Pipe()
		done := make(chan error, 1)
		duringTransfer := make(chan struct{})
		var producing atomic.Bool
		var resumed sync.Once
		observe := func(time.Time) error {
			if producing.Load() {
				resumed.Do(func() { close(duringTransfer) })
			}
			return nil
		}
		go func() {
			done <- servePreparationSessionWithProgress(ctx, target, target, request.Session.Nonce,
				func(_ context.Context, r PreparationSessionRequest) error {
					if r != request {
						return ErrSessionProtocol
					}
					return nil
				},
				func(ctx context.Context, r PreparationSessionRequest, stream io.Reader) (PreparationProgress, error) {
					var cursor PreparationProgress
					err := ConsumePreparation(ctx, scratch, expected, manager, stream, mutationAuthority, func(ctx context.Context, received *PreparationInputs) error {
						if inspection {
							var err error
							cursor, err = received.InspectPreparationProgress(ctx, journal)
							return err
						}
						_, err := received.publishSource(ctx, journal, destination)
						return err
					})
					return cursor, err
				}, observe, sessionTiming{250 * time.Millisecond, SessionLeaseWindow, 2 * time.Second})
		}()
		produce := func(ctx context.Context, w io.Writer) error {
			producing.Store(true)
			select {
			case <-duringTransfer:
			case <-ctx.Done():
				return ctx.Err()
			}
			return inputs.WritePreparation(ctx, expected, w, mutationAuthority)
		}
		var cursor PreparationProgress
		if inspection {
			cursor, err = ExchangePreparationProgressSession(ctx, client, client, request, produce, mutationAuthority)
		} else {
			err = ExchangePreparationSession(ctx, client, client, request, produce, mutationAuthority)
		}
		peerErr := <-done
		client.Close()
		if err != nil || peerErr != nil {
			t.Fatalf("%s: sender=%v receiver=%v", action, err, peerErr)
		}
		entries, err := os.ReadDir(scratch)
		if err != nil || len(entries) != 0 {
			t.Fatal("receiver scratch retained", err)
		}
		if inspection {
			if cursor.Step != "empty" || len(readMutationFixture(t, journalPath).Steps) != 0 {
				t.Fatal("inspection changed journal or returned invalid cursor", cursor)
			}
			if _, err := os.Stat(filepath.Join(destination, "Borealis")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unexpected inspection effect", err)
			}
		}
	}
	if readMutationFixture(t, journalPath).Steps["stage_source"].State != "applied" {
		t.Fatal("first installation effect not recorded")
	}
	if _, err := os.Stat(filepath.Join(destination, "Borealis", "Engine", "Deploy", "runtime.env")); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(inputs.bundle.ArchivePath())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("inspected and published a %d-byte verified archive in separate sessions", stat.Size())
}
