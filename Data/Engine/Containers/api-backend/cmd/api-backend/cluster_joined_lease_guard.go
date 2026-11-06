package main

import (
	"context"
	"fmt"
	"time"
)

// SSH scopes own synchronous authority checks with their existing deadlines.
// Error grace applies after a failed renewal, not before the first scheduled
// check. Unlike the controller/scheduler ownership watchdog, Close must join an
// admitted callback before the enclosing scope can release its resources.
type clusterJoinedLeaseGuard struct {
	*clusterControllerLeaseGuard
}

func startJoinedClusterLeaseGuard(parent context.Context, interval, renewalGrace time.Duration, renew func(context.Context) (bool, error)) *clusterJoinedLeaseGuard {
	if interval <= 0 {
		interval = clusterControllerLeaseRenewInterval
	}
	if renewalGrace <= 0 {
		renewalGrace = clusterControllerLeaseRenewalGrace
	}
	guardCtx, cancel := context.WithCancelCause(parent)
	heartbeatCtx, heartbeatCancel := context.WithCancel(parent)
	guard := &clusterJoinedLeaseGuard{&clusterControllerLeaseGuard{
		ctx: guardCtx, cancel: cancel, heartbeatCancel: heartbeatCancel, done: make(chan struct{}),
	}}
	go func() {
		defer close(guard.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastRenewed := time.Now()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if heartbeatCtx.Err() != nil {
					return
				}
				owned, err := renew(heartbeatCtx)
				if heartbeatCtx.Err() != nil {
					return
				}
				if err != nil {
					if time.Since(lastRenewed) >= renewalGrace {
						guard.fail(fmt.Errorf("%w: renewal failed throughout grace period: %v", errClusterControllerLeaseLost, err))
						return
					}
					continue
				}
				if !owned {
					guard.fail(errClusterControllerLeaseLost)
					return
				}
				lastRenewed = time.Now()
			}
		}
	}()
	return guard
}

func (g *clusterJoinedLeaseGuard) Close() {
	g.heartbeatCancel()
	<-g.done
	g.cancel(context.Canceled)
}
