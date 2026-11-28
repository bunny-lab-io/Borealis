package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPostgresImageTransferReceiver(t *testing.T) {
	ctx := context.Background()
	for _, platform := range []bool{false, true} {
		reference, blobs := postgresImageFixture(t, "gzip", platform)
		check := func(context.Context) error { return nil }
		sender, err := acquirePostgresImage(ctx, t.TempDir(), reference, check, func(_ context.Context, digest string, _ int64, _ bool, out io.Writer) error {
			_, err := out.Write(blobs[digest])
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sender.Close()
		var stream bytes.Buffer
		if sender.WriteImage(ctx, &stream, check) != nil {
			t.Fatal("sender failed")
		}
		for _, mode := range []string{"complete", "truncated", "wrong source", "wrong magic", "zero inventory", "oversized inventory", "changed archive", "changed trailer", "lost authority", "cancel", "unknown field", "duplicate field", "alias field", "zero archive", "oversized archive", "foreign reference", "bad digest"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				data, want := bytes.Clone(stream.Bytes()), reference
				offset := len(postgresTransferMagic) + 4
				size := int(binary.BigEndian.Uint32(data[len(postgresTransferMagic):offset]))
				var wire postgresTransferMetadata
				if json.Unmarshal(data[offset:offset+size], &wire) != nil {
					t.Fatal("invalid fixture")
				}
				metadata := string(data[offset : offset+size])
				switch mode {
				case "truncated":
					data = data[:len(data)-5]
				case "wrong source":
					want = PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64)
				case "wrong magic":
					data[0] ^= 1
				case "zero inventory":
					binary.BigEndian.PutUint32(data[len(postgresTransferMagic):], 0)
				case "oversized inventory":
					binary.BigEndian.PutUint32(data[len(postgresTransferMagic):], maxPostgresTransferMetadata+1)
				case "changed archive":
					data[offset+size+100] ^= 1
				case "changed trailer":
					data[len(data)-1] ^= 1
				case "cancel":
					cancel()
				case "unknown field":
					metadata = strings.Replace(metadata, "{", `{"unknown":1,`, 1)
				case "duplicate field":
					metadata = strings.Replace(metadata, "{", `{"version":1,`, 1)
				case "alias field":
					metadata = strings.Replace(metadata, `"version"`, `"Version"`, 1)
				case "zero archive":
					wire.ArchiveBytes = 0
				case "oversized archive":
					wire.ArchiveBytes = MaxImageArchiveBytes + 1
				case "foreign reference":
					wire.Reference = "ghcr.io/foreign/database@sha256:" + strings.Repeat("a", 64)
				case "bad digest":
					wire.ArchiveSHA256 = "unknown"
				}
				switch mode {
				case "zero archive", "oversized archive", "foreign reference", "bad digest":
					raw, _ := json.Marshal(wire)
					metadata = string(raw)
				}
				if metadata != string(stream.Bytes()[offset:offset+size]) {
					data = append(bytes.Clone(data[:offset]), []byte(metadata)...)
					binary.BigEndian.PutUint32(data[len(postgresTransferMagic):], uint32(len(metadata)))
					data = append(data, stream.Bytes()[offset+size:]...)
					// Keep framing authentic so rejection must come from the
					// metadata contract, not an accidentally stale trailer.
					digest := sha256.Sum256([]byte(metadata))
					copy(data[len(data)-sha256.Size:], digest[:])
				}
				parent, calls := t.TempDir(), 0
				reader := bytes.NewReader(append(data, []byte("next-frame")...))
				receiver, err := ReceivePostgresImage(ctx, parent, want, reader, func(context.Context) error {
					calls++
					if mode == "lost authority" && calls == 5 {
						return ErrSessionAuthority
					}
					return nil
				})
				if mode != "complete" {
					switch mode {
					case "unknown field", "duplicate field", "alias field", "zero archive", "oversized archive", "foreign reference", "bad digest":
						if reader.Len() != len(data)+len("next-frame")-offset-len(metadata) {
							t.Fatal("invalid metadata reached archive IO")
						}
					}
					entries, readErr := os.ReadDir(parent)
					if err == nil || receiver != nil || readErr != nil || len(entries) != 0 {
						t.Fatal("invalid receiver escaped", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer receiver.Close()
				left, _ := io.ReadAll(reader)
				if string(left) != "next-frame" || !reflect.DeepEqual(sender.Proof(), receiver.Proof()) {
					t.Fatal("changed source proof or frame boundary")
				}
				var got, original bytes.Buffer
				if sender.WriteArchive(ctx, &original, check) != nil || receiver.WriteArchive(ctx, &got, check) != nil || !bytes.Equal(got.Bytes(), original.Bytes()) {
					t.Fatal("changed PostgreSQL archive")
				}
			})
		}
	}
}
