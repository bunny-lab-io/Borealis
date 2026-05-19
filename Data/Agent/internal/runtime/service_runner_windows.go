//go:build windows

package agentruntime

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

func RunServiceIfNeeded(logger *log.Logger, run func(context.Context) error) (int, bool) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return 0, false
	}
	handler := &agentServiceHandler{logger: logger, run: run}
	if err := svc.Run(WindowsServiceName, handler); err != nil {
		if logger != nil {
			logger.Printf("windows service runner failed: %v", err)
		}
		return 1, true
	}
	return handler.exitCode, true
}

type agentServiceHandler struct {
	logger   *log.Logger
	run      func(context.Context) error
	exitCode int
}

func (h *agentServiceHandler) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		if h.run == nil {
			done <- nil
			return
		}
		done <- h.run(ctx)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case err := <-done:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			if err != nil && ctx.Err() == nil {
				if h.logger != nil {
					h.logger.Printf("agent service stopped with error: %v", err)
				}
				h.exitCode = 1
				return false, 1
			}
			h.exitCode = 0
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
			}
		}
	}
}
