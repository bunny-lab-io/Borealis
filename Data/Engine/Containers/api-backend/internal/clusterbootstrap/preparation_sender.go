package clusterbootstrap

import (
	"context"

	"io"
	"log"
	"time"
)

// PreparationProofTimeout bounds a single live controller/target proof.
const PreparationProofTimeout = 3 * time.Second

// ExchangeSSHPreparation supervises one pinned SSH upload and its terminal receipt.
func ExchangeSSHPreparation(parent context.Context, input io.ReadCloser, output io.WriteCloser, request PreparationSessionRequest, upload PreparationStreamUpload, refresh func(context.Context) error, progress *PreparationProgress, completion *PrejoinPreparationReceipt) (resultErr error) {
	request.SSHStream = true
	if (request.Action == "inspect_preparation_progress") != (progress != nil) || (request.Action == "reconcile_prejoin_identity_activation") != (completion != nil) {
		return ErrSessionProtocol
	}
	// Only locally chosen labels reach the source log. Frames, archive bytes,
	// target stderr and credential material must stay inside the session.
	stage := "validate_request"
	if progress != nil {
		began := time.Now()
		defer func() {
			if resultErr != nil {
				log.Printf("native progress inspection exchange failed: phase=%s elapsed=%s", stage, time.Since(began).Round(time.Millisecond))
			}
		}()
	}
	if request.IdentitySource != nil {
		source := *request.IdentitySource
		request.IdentitySource = &source
	}
	digest, err := request.Digest()
	if err != nil || input == nil || output == nil || refresh == nil || upload == nil {
		return ErrSessionProtocol
	}
	profile := preparationProfile(bulkPreparationAction(request.Action))
	ctx, cancel := context.WithTimeout(parent, profile.lifetime)
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
	check := func(budget time.Duration) error {
		checkCtx, checkCancel := context.WithTimeout(ctx, budget)
		defer checkCancel()
		err := refresh(checkCtx)
		if err != nil || checkCtx.Err() != nil {
			return ErrSessionAuthority
		}
		return nil
	}
	stage = "initial_authority"
	if check(PreparationProofTimeout) != nil {
		return ErrSessionAuthority
	}
	stage = "preamble"
	preamble, _ := SessionPreamble(request.Session.Nonce)
	if _, err := io.WriteString(output, preamble); err != nil {
		return ErrSessionProtocol
	}
	reader := &io.LimitedReader{R: input, N: profile.output + 1}
	stage = "receiver_ready"
	var ready PreparationSessionFrame
	if ReadSessionFrame(reader, &ready) != nil || ready != (PreparationSessionFrame{Protocol: 1, State: "ready", Nonce: request.Session.Nonce}) {
		return ErrSessionProtocol
	}
	stage = "send_request"
	if WriteSessionFrame(output, request) != nil {
		return ErrSessionProtocol
	}
	frames := make(chan PreparationSessionFrame)
	readDone := make(chan struct{})
	finishRequest := make(chan struct{})
	responseEOF := make(chan bool, 1)
	go func() {
		defer close(readDone)
		defer close(frames)
		for {
			var frame PreparationSessionFrame
			if ReadSessionFrame(reader, &frame) != nil {
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
			if frame.State == "verified" {
				select {
				case <-finishRequest:
				case <-ctx.Done():
					return
				}
				// The wrapper can retain stdout until its reader sees stdin EOF.
				// Half-close only the request. The owner still needs the exit status.
				if stream, ok := output.(interface{ CloseWrite() error }); ok && stream.CloseWrite() != nil {
					responseEOF <- false
					return
				}
				var trailing [1]byte
				n, err := reader.Read(trailing[:])
				responseEOF <- n == 0 && err == io.EOF && reader.N > 0
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
	type uploadResult struct {
		receipt PreparationStreamReceipt
		err     error
	}
	var uploaded chan uploadResult
	producerDone := make(chan struct{})
	started, sentEnd := false, false
	defer func() {
		cancel()
		if started {
			<-producerDone
		}
	}()
	stage = "target_verification"
	seenChallenges := make(map[string]bool)
	deadline := time.NewTimer(SessionLeaseWindow)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ErrSessionAuthority
		case <-deadline.C:
			return ErrSessionAuthority
		case upload := <-uploaded:
			if upload.err != nil || upload.receipt.Bytes < 1 || upload.receipt.Bytes > profile.bytes || !digestPattern.MatchString(upload.receipt.SHA256) {
				return ErrSessionProtocol
			}
			uploaded = nil
			sentEnd = true
			if WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: "end", Nonce: request.Session.Nonce, RequestSHA256: digest, Bytes: upload.receipt.Bytes, StreamSHA256: upload.receipt.SHA256}) != nil {
				return ErrSessionProtocol
			}
		case frame, ok := <-frames:
			if !ok || frame.Bytes != 0 || frame.StreamSHA256 != "" || frame.GrantMillis != 0 || frame.Protocol != 1 || frame.RequestSHA256 != digest || frame.Data != "" || frame.State != "verified" && (frame.JournalStep != "" || frame.CompletionBase64 != "") {
				return ErrSessionProtocol
			}
			if frame.State == "challenge" {
				if frame.Sequence != 0 || !digestPattern.MatchString(frame.Nonce) || seenChallenges[frame.Nonce] || len(seenChallenges) >= profile.challenges {
					return ErrSessionProtocol
				}
				seenChallenges[frame.Nonce] = true
				budget := PreparationProofTimeout
				if progress != nil && started {
					// Only this read-only challenge can queue behind the running
					// archive producer. The transport separately caps the wait and
					// fresh proof. The peer still enforces its original lease.
					budget += PreparationProofTimeout
				}
				began := time.Now()
				if check(budget) != nil {
					if progress != nil {
						log.Printf("native progress challenge rejected: challenge=%d transfer_started=%t check_elapsed=%s", len(seenChallenges), started, time.Since(began).Round(time.Millisecond))
					}
					stage = "challenge_authority"
					return ErrSessionAuthority
				}
				frame.State = "heartbeat"
				if request.SSHStream {
					window := SessionLeaseWindow
					if limit, ok := ctx.Deadline(); ok {
						window = min(window, time.Until(limit))
					}
					frame.GrantMillis = window.Milliseconds()
					if frame.GrantMillis < 1 {
						return ErrSessionAuthority
					}
				}
				if remaining, bounded := PreparationTimeRemaining(ctx); bounded {
					frame.GrantMillis = min(remaining, SessionLeaseWindow).Milliseconds()
					if frame.GrantMillis < 1 {
						return ErrSessionAuthority
					}
				}
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
				stage = "input_transfer"
				started = true
				uploaded = make(chan uploadResult, 1)
				go func(done chan<- uploadResult) {
					defer close(producerDone)
					r, err := upload(ctx)
					done <- uploadResult{r, err}
				}(uploaded)
			case "verified":
				var receipt PrejoinPreparationReceipt
				if request.Action == "reconcile_prejoin_identity_activation" {
					expected, e := request.Expected()
					if e != nil || request.IdentitySource == nil {
						return ErrSessionProtocol
					}
					receipt, e = decodePreparationReceipt(frame.CompletionBase64, expected, *request.IdentitySource)
					if e != nil {
						return ErrSessionProtocol
					}
				} else if frame.CompletionBase64 != "" || completion != nil {
					return ErrSessionProtocol
				}
				cursor := PreparationProgress{Step: frame.JournalStep}
				if progress != nil {
					if cursor.Validate() != nil {
						return ErrSessionProtocol
					}
				} else if frame.JournalStep != "" {
					return ErrSessionProtocol
				}
				if !sentEnd || frame.Sequence != 0 {
					return ErrSessionProtocol
				}
				close(finishRequest)
				select {
				case clean := <-responseEOF:
					if !clean {
						return ErrSessionProtocol
					}
				case <-ctx.Done():
					return ErrSessionAuthority
				case <-deadline.C:
					return ErrSessionAuthority
				}
				if check(PreparationProofTimeout) != nil {
					stage = "terminal_authority"
					return ErrSessionAuthority
				}
				// Preserve the successful SSH channel until its owner verifies
				// remote exit status. The response reader already consumed EOF.
				stop()
				completed = true
				if progress != nil {
					*progress = cursor
				}
				if completion != nil {
					*completion = receipt
				}
				return nil
			default:
				return ErrSessionProtocol
			}
		}
	}
}
