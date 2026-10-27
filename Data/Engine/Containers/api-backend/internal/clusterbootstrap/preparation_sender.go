package clusterbootstrap

import (
	"context"
	"encoding/base64"
	"io"
	"time"
)

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
	if request.IdentitySource != nil {
		source := *request.IdentitySource
		request.IdentitySource = &source
	}
	digest, err := request.Digest()
	if err != nil || input == nil || output == nil || produce == nil || refresh == nil {
		return ErrSessionProtocol
	}
	ctx, cancel := context.WithTimeout(parent, SessionLifetime)
	completed := false
	stop := context.AfterFunc(ctx, func() { input.Close(); output.Close() })
	defer func() {
		stop()
		cancel()
		if !completed {
			input.Close()
			output.Close()
		}
	}()
	check := func() error {
		checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
		defer checkCancel()
		err := refresh(checkCtx)
		if err != nil || checkCtx.Err() != nil {
			return ErrSessionAuthority
		}
		return nil
	}
	if check() != nil {
		return ErrSessionAuthority
	}
	preamble, _ := SessionPreamble(request.Session.Nonce)
	if _, err := io.WriteString(output, preamble); err != nil {
		return ErrSessionProtocol
	}
	reader := &io.LimitedReader{R: input, N: MaxPreparationSessionOutputBytes + 1}
	var ready PreparationSessionFrame
	if ReadSessionFrame(reader, &ready) != nil || ready != (PreparationSessionFrame{Protocol: 1, State: "ready", Nonce: request.Session.Nonce}) {
		return ErrSessionProtocol
	}
	if WriteSessionFrame(output, request) != nil {
		return ErrSessionProtocol
	}
	frames := make(chan PreparationSessionFrame)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(frames)
		for {
			var frame PreparationSessionFrame
			if ReadSessionFrame(reader, &frame) != nil {
				return
			}
			if frame.State == "verified" {
				var trailing [1]byte
				if n, err := reader.Read(trailing[:]); n != 0 || err != io.EOF || reader.N < 1 {
					return
				}
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
			if frame.State == "verified" {
				return
			}
		}
	}()
	defer func() {
		cancel()
		if !completed {
			input.Close()
		}
		<-readDone
	}()
	pipeReader, pipeWriter := io.Pipe()
	defer pipeReader.Close()
	defer pipeWriter.Close()
	type chunkResult struct {
		data []byte
		err  error
	}
	chunks := make(chan chunkResult)
	producerDone := make(chan struct{})
	chunkDone := make(chan struct{})
	started := false
	defer func() {
		cancel()
		pipeReader.CloseWithError(ErrSessionAuthority)
		pipeWriter.CloseWithError(ErrSessionAuthority)
		if started {
			<-producerDone
			<-chunkDone
		}
	}()
	start := func() {
		started = true
		go func() {
			defer close(producerDone)
			err := produce(ctx, pipeWriter)
			if err != nil {
				err = ErrSessionProtocol
			}
			pipeWriter.CloseWithError(err)
		}()
		go func() {
			defer close(chunkDone)
			for {
				data := make([]byte, PreparationChunkBytes)
				n, err := io.ReadFull(pipeReader, data)
				if err == io.ErrUnexpectedEOF {
					err = io.EOF
				}
				select {
				case chunks <- chunkResult{data[:n], err}:
				case <-ctx.Done():
					clear(data)
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}
	var sequence, total int64
	awaitingAck, sentEnd, eof := false, false, false
	seenChallenges := make(map[string]bool)
	deadline := time.NewTimer(SessionLeaseWindow)
	defer deadline.Stop()
	sendEnd := func() error {
		if total == 0 {
			return ErrSessionProtocol
		}
		sentEnd = true
		return WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: "end", Nonce: request.Session.Nonce, RequestSHA256: digest, Sequence: sequence})
	}
	for {
		var next <-chan chunkResult
		if started && !awaitingAck && !sentEnd && !eof {
			next = chunks
		}
		select {
		case <-ctx.Done():
			return ErrSessionAuthority
		case <-deadline.C:
			return ErrSessionAuthority
		case chunk := <-next:
			if chunk.err != nil && chunk.err != io.EOF {
				clear(chunk.data)
				return ErrSessionProtocol
			}
			eof = chunk.err == io.EOF
			total += int64(len(chunk.data))
			if total > MaxPreparationTransferBytes {
				clear(chunk.data)
				return ErrSessionProtocol
			}
			if len(chunk.data) > 0 {
				frame := PreparationSessionFrame{Protocol: 1, State: "chunk", Nonce: request.Session.Nonce, RequestSHA256: digest, Sequence: sequence, Data: base64.StdEncoding.EncodeToString(chunk.data)}
				clear(chunk.data)
				if WriteSessionFrame(output, frame) != nil {
					return ErrSessionProtocol
				}
				awaitingAck = true
			} else if eof {
				if sendEnd() != nil {
					return ErrSessionProtocol
				}
			}
		case frame, ok := <-frames:
			if !ok || frame.Protocol != 1 || frame.RequestSHA256 != digest || frame.Data != "" || frame.State != "verified" && frame.JournalStep != "" {
				return ErrSessionProtocol
			}
			if frame.State == "challenge" {
				if frame.Sequence != 0 || !digestPattern.MatchString(frame.Nonce) || seenChallenges[frame.Nonce] || len(seenChallenges) >= 64 {
					return ErrSessionProtocol
				}
				seenChallenges[frame.Nonce] = true
				if check() != nil {
					return ErrSessionAuthority
				}
				frame.State = "heartbeat"
				if WriteSessionFrame(output, frame) != nil {
					return ErrSessionProtocol
				}
				deadline.Reset(SessionLeaseWindow)
				continue
			}
			if frame.Nonce != request.Session.Nonce {
				return ErrSessionProtocol
			}
			switch frame.State {
			case "source_ready":
				if started || frame.Sequence != 0 || len(seenChallenges) == 0 {
					return ErrSessionProtocol
				}
				start()
			case "ack":
				if !awaitingAck || sentEnd || frame.Sequence != sequence {
					return ErrSessionProtocol
				}
				awaitingAck = false
				sequence++
				if eof && sendEnd() != nil {
					return ErrSessionProtocol
				}
			case "verified":
				cursor := PreparationProgress{Step: frame.JournalStep}
				if progress != nil {
					if cursor.Validate() != nil {
						return ErrSessionProtocol
					}
				} else if frame.JournalStep != "" {
					return ErrSessionProtocol
				}
				if !sentEnd || awaitingAck || frame.Sequence != sequence {
					return ErrSessionProtocol
				}
				if check() != nil {
					return ErrSessionAuthority
				}
				// Preserve the successful SSH channel until its owner verifies
				// remote exit status. The response reader already consumed EOF.
				stop()
				completed = true
				if progress != nil {
					*progress = cursor
				}
				return nil
			default:
				return ErrSessionProtocol
			}
		}
	}
}
