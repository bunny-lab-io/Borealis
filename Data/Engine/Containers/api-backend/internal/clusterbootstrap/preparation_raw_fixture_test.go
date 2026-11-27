package clusterbootstrap

import (
	"borealis/api-backend/internal/testsshpipe"
	"context"
	"io"
	"time"
)

func testStreamExchange(ctx context.Context, in io.ReadCloser, out io.WriteCloser, r PreparationSessionRequest, produce func(context.Context, io.Writer) error, check func(context.Context) error, p *PreparationProgress, c *PrejoinPreparationReceipt) error {
	data := in.(*testsshpipe.Conn).Data
	ctx = WithPreparationDeadline(ctx, func() time.Time { return time.Now().Add(900 * time.Millisecond) })
	return ExchangeSSHPreparation(ctx, in, out, r, func(ctx context.Context) (PreparationStreamReceipt, error) {
		stop := context.AfterFunc(ctx, func() { data.Close() })
		defer stop()
		defer data.Close()
		return StreamPreparation(ctx, data, produce)
	}, check, p, c)
}

// ExchangePreparationSession transfers private inputs only after native target
// verification. Transport owns approved-key pinning and executor supervision.
// Producer and refresh must honor cancellation; both finish before return.
func ExchangePreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser,
	request PreparationSessionRequest, produce func(context.Context, io.Writer) error, refresh func(context.Context) error) error {
	if request.Action == "inspect_preparation_progress" {
		return ErrSessionProtocol
	}
	return exchangePreparationSession(parent, input, output, request, produce, refresh, nil)
}

// ExchangePreparationProgressSession returns a bound cursor only after complete
// transfer, terminal frame/EOF and final authority proof. Caller still verifies
// remote exit and runs separate domain reconciliation before advancing a stage.
func ExchangePreparationProgressSession(parent context.Context, input io.ReadCloser, output io.WriteCloser,
	request PreparationSessionRequest, produce func(context.Context, io.Writer) error, refresh func(context.Context) error) (PreparationProgress, error) {
	var progress PreparationProgress
	if request.Action != "inspect_preparation_progress" {
		return progress, ErrSessionProtocol
	}
	if err := exchangePreparationSession(parent, input, output, request, produce, refresh, &progress); err != nil {
		return PreparationProgress{}, err
	}
	return progress, nil
}

func exchangePreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser,
	request PreparationSessionRequest, produce func(context.Context, io.Writer) error, refresh func(context.Context) error, progress *PreparationProgress) error {
	return testStreamExchange(parent, input, output, request, produce, refresh, progress, nil)
}

func ExchangePrejoinPreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, request PreparationSessionRequest,
	produce func(context.Context, io.Writer) error, refresh func(context.Context) error) (PrejoinPreparationReceipt, error) {
	var result PrejoinPreparationReceipt
	if request.Action != "reconcile_prejoin_identity_activation" {
		return result, ErrSessionProtocol
	}
	if err := testStreamExchange(parent, input, output, request, produce, refresh, nil, &result); err != nil {
		return PrejoinPreparationReceipt{}, err
	}
	return result, nil
}

func ServeArtifactPreparationSessionWithResult(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionResultReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, ArtifactPreparationLifetime)
	defer cancel()
	return servePreparationSessionResult(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second}, preparationProfile(true), input.(*testsshpipe.Conn).Open)
}

// ServePreparationSessionWithProgress adds only a bounded journal cursor to the
// inspection action. Other actions must return the zero cursor. The cursor is
// emitted only after complete private transfer, EOF and successful processing.
func ServePreparationSessionWithProgress(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, SessionLifetime)
	defer cancel()
	return servePreparationSessionWithProgress(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second})
}

// ServePreparationSession maintains challenge authority while the receiver
// consumes a bounded, backpressured stream. Only one unacknowledged chunk may
// exist. Handler must validate complete transfer and EOF before any mutation.
// The independent executor watchdog bounds stalled IO/processes as in recovery.
func ServePreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, SessionLifetime)
	defer cancel()
	return servePreparationSession(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second})
}

func servePreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionReceiver, observe func(time.Time) error, timing sessionTiming) error {
	if receive == nil {
		return ErrSessionProtocol
	}
	return servePreparationSessionWithProgress(parent, input, output, nonce, verify,
		func(ctx context.Context, request PreparationSessionRequest, stream io.Reader) (PreparationProgress, error) {
			return PreparationProgress{}, receive(ctx, request, stream)
		}, observe, timing)
}

func servePreparationSessionWithProgress(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error, timing sessionTiming) error {
	return servePreparationSessionProfile(parent, input, output, nonce, verify, receive, observe, timing, preparationProfile(false))
}

func ServeArtifactPreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, ArtifactPreparationLifetime)
	defer cancel()
	return servePreparationSessionProfile(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second}, preparationProfile(true))
}

func servePreparationSessionProfile(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error, timing sessionTiming, profile preparationTransportProfile) error {
	if receive == nil {
		return ErrSessionProtocol
	}
	return servePreparationSessionResult(parent, input, output, nonce, verify, func(ctx context.Context, r PreparationSessionRequest, in io.Reader) (PreparationSessionResult, error) {
		progress, err := receive(ctx, r, in)
		return PreparationSessionResult{Progress: progress}, err
	}, observe, timing, profile, input.(*testsshpipe.Conn).Open)
}
