package main

import (
	"context"
	"log"
)

func (m *goSchedulerManager) processPatchPolicyTick(ctx context.Context, profile operatorProfile, now int64, nowMinute int64) error {
	if m == nil || m.store == nil {
		return nil
	}
	result, err := m.store.evaluatePatchPoliciesAt(ctx, profile, 0, nowMinute, now, false)
	if err != nil {
		return err
	}
	if count := coerceInt64(result["count"]); count > 0 {
		log.Printf("patch policy evaluation completed runs=%d scheduled_ts=%d", count, nowMinute)
	}
	return nil
}
