package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
)

// SSH carries the data verbatim. The control session confirms the complete
// stream before the native parser can see EOF and enter a journaled mutation.
type PreparationStreamReceipt struct {
	Bytes  int64
	SHA256 string
}

type PreparationStreamUpload func(context.Context) (PreparationStreamReceipt, error)
type PreparationStreamOpen func(context.Context, string) (io.ReadCloser, error)

type preparationStreamReader struct {
	ctx        context.Context
	input      io.Reader
	end        <-chan PreparationStreamReceipt
	hash       hash.Hash
	n, maximum int64
	verified   bool
}

func (r *preparationStreamReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.verified {
		return 0, io.EOF
	}
	n, err := r.input.Read(p)
	r.n += int64(n)
	if r.n > r.maximum {
		return 0, ErrSessionProtocol
	}
	r.hash.Write(p[:n])
	if !errors.Is(err, io.EOF) {
		return n, err
	}
	select {
	case <-r.ctx.Done():
		return 0, ErrSessionAuthority
	case end := <-r.end:
		if r.n < 1 || end.Bytes != r.n || end.SHA256 != hex.EncodeToString(r.hash.Sum(nil)) {
			return 0, ErrSessionProtocol
		}
		r.verified = true
		return n, io.EOF
	}
}

// StreamPreparation copies the existing immutable input format and authenticates
// the streamed bytes without creating a second archive on either host.
func StreamPreparation(ctx context.Context, out io.Writer, produce func(context.Context, io.Writer) error) (PreparationStreamReceipt, error) {
	h := sha256.New()
	w := &preparationCountingWriter{destination: io.MultiWriter(out, h)}
	if err := produce(ctx, w); err != nil || ctx.Err() != nil || w.bytes < 1 {
		return PreparationStreamReceipt{}, ErrSessionProtocol
	}
	return PreparationStreamReceipt{w.bytes, hex.EncodeToString(h.Sum(nil))}, nil
}

type preparationCountingWriter struct {
	destination io.Writer
	bytes       int64
}

func (w *preparationCountingWriter) Write(p []byte) (int, error) {
	n, err := w.destination.Write(p)
	w.bytes += int64(n)
	return n, err
}
