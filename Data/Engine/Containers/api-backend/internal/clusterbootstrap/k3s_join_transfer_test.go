package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
)

func TestK3sJoinTransferExactFrame(t *testing.T) {
	expected, source, token, ca := joinMaterialFixture(t)
	m, err := NewK3sJoinMaterial(expected, source, token, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	check := func(context.Context) error { return nil }
	var stream bytes.Buffer
	if m.WriteJoinMaterial(context.Background(), expected, source, &stream, check) != nil {
		t.Fatal("frame export")
	}
	stream.WriteString("next frame")
	got, err := ReceiveK3sJoinMaterial(context.Background(), expected, source, &stream, check)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if stream.String() != "next frame" {
		t.Fatal("receiver consumed following frame")
	}
	var received bytes.Buffer
	if got.WriteToken(context.Background(), expected, source, check, &received) != nil || !bytes.Equal(received.Bytes(), token) || got.owned.digest != m.owned.digest {
		t.Fatal("private material changed")
	}
}

func TestK3sJoinTransferRejectsCorruptionAndForeignClaim(t *testing.T) {
	for _, mode := range []string{"header", "token", "CA", "trailer", "truncated", "oversized metadata", "foreign claim", "cancel", "authority"} {
		t.Run(mode, func(t *testing.T) {
			expected, source, token, ca := joinMaterialFixture(t)
			m, err := NewK3sJoinMaterial(expected, source, token, ca)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			check := func(context.Context) error { return nil }
			var stream bytes.Buffer
			if m.WriteJoinMaterial(context.Background(), expected, source, &stream, check) != nil {
				t.Fatal("frame fixture")
			}
			raw := bytes.Clone(stream.Bytes())
			metadata := int(binary.BigEndian.Uint32(raw[len(k3sJoinTransferMagic):]))
			payload := len(k3sJoinTransferMagic) + 4 + metadata
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "header":
				raw[0] ^= 1
			case "token":
				raw[payload+len(token)-1] ^= 1
			case "CA":
				raw[payload+len(token)+10] ^= 1
			case "trailer":
				raw[len(raw)-1] ^= 1
			case "truncated":
				raw = raw[:len(raw)-1]
			case "oversized metadata":
				binary.BigEndian.PutUint32(raw[len(k3sJoinTransferMagic):], maxK3sJoinMetadata+1)
			case "foreign claim":
				expected.Target.Generation++
			case "cancel":
				cancel()
			case "authority":
				check = func(context.Context) error { return ErrSessionAuthority }
			}
			input := bytes.NewReader(raw)
			if got, err := ReceiveK3sJoinMaterial(ctx, expected, source, input, check); err == nil || got != nil {
				t.Fatal("invalid frame admitted")
			}
			if mode == "foreign claim" && input.Len() != len(raw)-payload {
				t.Fatal("foreign claim read private payload")
			}
		})
	}
}

type joinTransferWriter func([]byte) (int, error)

func (f joinTransferWriter) Write(b []byte) (int, error) { return f(b) }

func TestK3sJoinTransferExportStopsAndClearsBorrowedBuffer(t *testing.T) {
	for _, mode := range []string{"success", "short write", "write error", "authority loss", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			expected, source, token, ca := joinMaterialFixture(t)
			m, err := NewK3sJoinMaterial(expected, source, token, ca)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lost := false
			check := func(context.Context) error {
				if lost {
					return ErrSessionAuthority
				}
				return nil
			}
			calls := 0
			var borrowed []byte
			writer := joinTransferWriter(func(raw []byte) (int, error) {
				calls++
				borrowed = raw
				switch mode {
				case "short write":
					return len(raw) - 1, nil
				case "write error":
					return 0, io.ErrClosedPipe
				case "authority loss":
					lost = true
				case "cancel":
					cancel()
				}
				return len(raw), nil
			})
			err = m.WriteJoinMaterial(ctx, expected, source, writer, check)
			if (err == nil) != (mode == "success") || calls != 1 || !bytes.Equal(borrowed, make([]byte, len(borrowed))) || !bytes.Equal(m.owned.token, token) {
				t.Fatal("unsafe export lifetime", err, calls)
			}
		})
	}
}
