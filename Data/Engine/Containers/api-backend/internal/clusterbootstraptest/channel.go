// Package clusterbootstraptest connects production watchdog and raw data sessions in worker tests.
package clusterbootstraptest

import (
	b "borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/testsshpipe"
	"context"
	"io"
	"time"
)

func ServePreparationSession(ctx context.Context, in, out *testsshpipe.Conn, nonce string, verify func(context.Context, b.PreparationSessionRequest) error, receive b.PreparationSessionReceiver, observe func(time.Time) error) error {
	return ServePreparationSessionWithProgress(ctx, in, out, nonce, verify, func(c context.Context, r b.PreparationSessionRequest, data io.Reader) (b.PreparationProgress, error) {
		return b.PreparationProgress{}, receive(c, r, data)
	}, observe)
}
func ServePreparationSessionWithProgress(ctx context.Context, in, out *testsshpipe.Conn, nonce string, verify func(context.Context, b.PreparationSessionRequest) error, receive b.PreparationSessionProgressReceiver, observe func(time.Time) error) error {
	return b.ServeSSHPreparation(ctx, in, out, nonce, false, in.Open, verify, func(c context.Context, r b.PreparationSessionRequest, data io.Reader) (b.PreparationSessionResult, error) {
		p, err := receive(c, r, data)
		return b.PreparationSessionResult{Progress: p}, err
	}, observe)
}
func ExchangePreparationSession(ctx context.Context, in, out *testsshpipe.Conn, r b.PreparationSessionRequest, produce func(context.Context, io.Writer) error, check func(context.Context) error) error {
	return exchange(ctx, in, out, r, produce, check, nil)
}
func ExchangePreparationProgressSession(ctx context.Context, in, out *testsshpipe.Conn, r b.PreparationSessionRequest, produce func(context.Context, io.Writer) error, check func(context.Context) error) (b.PreparationProgress, error) {
	var p b.PreparationProgress
	err := exchange(ctx, in, out, r, produce, check, &p)
	return p, err
}
func exchange(ctx context.Context, in, out *testsshpipe.Conn, r b.PreparationSessionRequest, produce func(context.Context, io.Writer) error, check func(context.Context) error, p *b.PreparationProgress) error {
	return b.ExchangeSSHPreparation(ctx, in, out, r, func(ctx context.Context) (b.PreparationStreamReceipt, error) {
		stop := context.AfterFunc(ctx, func() { out.Data.Close() })
		defer stop()
		defer out.Data.Close()
		return b.StreamPreparation(ctx, out.Data, produce)
	}, check, p, nil)
}
