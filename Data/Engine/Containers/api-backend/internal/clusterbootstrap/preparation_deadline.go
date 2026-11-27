package clusterbootstrap

import (
	"context"
	"time"
)

type preparationDeadlineKey struct{}

// WithPreparationDeadline makes the independently confirmed lease limit
// available to the pinned transport. It never renews a lease.
func WithPreparationDeadline(ctx context.Context, deadline func() time.Time) context.Context {
	return context.WithValue(ctx, preparationDeadlineKey{}, deadline)
}

// PreparationTimeRemaining includes both the database-confirmed ownership
// limit and any shorter enclosing operation or credential deadline.
func PreparationTimeRemaining(ctx context.Context) (time.Duration, bool) {
	get, ok := ctx.Value(preparationDeadlineKey{}).(func() time.Time)
	if !ok {
		return 0, false
	}
	if get == nil || ctx.Err() != nil {
		return 0, true
	}
	until := get()
	if limit, ok := ctx.Deadline(); ok && limit.Before(until) {
		until = limit
	}
	return time.Until(until), true
}

func CheckPreparationDeadline(ctx context.Context) error {
	left, ok := PreparationTimeRemaining(ctx)
	if !ok || left <= 0 {
		return ErrSessionAuthority
	}
	return nil
}
