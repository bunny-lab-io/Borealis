package currentuser

import (
	"context"
	"errors"
	"strings"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

// This result distinguishes a rejected launch from a launched process whose
// original handle never confirmed exit. Only joined permits a terminal receipt.
type currentUserProcessCompletion struct {
	result   scripts.Result
	launched bool
	joined   bool
	detail   string
}

type currentUserProcess struct {
	wait      func(context.Context, int) error
	terminate func() error
	exitCode  func() (uint32, error)
	capture   func() (string, string, error)
	cleanup   func() error
}

func completeCurrentUserProcess(ctx context.Context, timeoutSeconds int, process currentUserProcess) (out currentUserProcessCompletion) {
	out.launched = true
	defer func() { out.resultError(process.cleanup()) }()
	waitErr := process.wait(ctx, timeoutSeconds)
	if waitErr != nil {
		terminateErr := process.terminate()
		// A stop request is asynchronous. Keep the original handle and wait
		// independently of the canceled execution context; never reopen by PID.
		joinErr := process.wait(context.Background(), 0)
		if joinErr != nil {
			out.resultError(errors.Join(waitErr, terminateErr, joinErr))
			return out
		}
	}
	out.joined = true
	code, codeErr := process.exitCode()
	stdout, stderr, captureErr := process.capture()
	out.result = scripts.CleanPowerShellResult(scripts.Result{ReturnCode: int(code), Stdout: stdout, Stderr: stderr})
	// A signaled handle is independent of the exit-code value (including259)
	// and of result-read failures. These are known failed results, not unknown
	// lifetime. TerminateProcess may also have raced a natural exit.
	out.resultError(errors.Join(waitErr, codeErr, captureErr))
	return out
}

func (out *currentUserProcessCompletion) resultError(err error) {
	if err == nil {
		return
	}
	out.result.ReturnCode = -1
	out.result.Stderr = joinStderr(out.result.Stderr, err.Error())
	out.detail = joinStderr(out.detail, err.Error())
}

func deliverCurrentUserCompletion(out currentUserProcessCompletion, complete func(scripts.Result)) (bool, string) {
	if !out.launched {
		return false, out.detail
	}
	if out.joined && complete != nil {
		complete(out.result)
	}
	return true, ""
}

func joinStderr(existing string, extra string) string {
	existing = strings.TrimRight(existing, "\r\n")
	extra = strings.TrimSpace(extra)
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	return existing + "\n" + extra
}
