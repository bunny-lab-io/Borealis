package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClusterJoinedLeaseGuardChecksBeforeErrorGrace(t *testing.T) {
	for _, mode := range []string{"healthy", "error", "ownership lost"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			checked := make(chan struct{}, 1)
			guard := startJoinedClusterLeaseGuard(parent, 10*time.Millisecond, time.Nanosecond, func(context.Context) (bool, error) {
				select {
				case checked <- struct{}{}:
				default:
				}
				if mode == "error" {
					return false, errors.New("fixture renewal failure")
				}
				return mode == "healthy", nil
			})
			defer guard.Close()
			select {
			case <-checked:
			case <-parent.Done():
				t.Fatal("grace expired before first scheduled authority check")
			}
			if mode == "healthy" {
				if guard.Context().Err() != nil || guard.Err() != nil {
					t.Fatal("healthy authority expired at error-grace boundary")
				}
				return
			}
			select {
			case <-guard.Context().Done():
				if !errors.Is(guard.Err(), errClusterControllerLeaseLost) {
					t.Fatal("failed renewal did not reject authority", guard.Err())
				}
			case <-parent.Done():
				t.Fatal("failed renewal did not cancel work")
			}
		})
	}
}

func TestClusterJoinedLeaseGuardCloseJoinsAdmittedCheck(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	guard := startJoinedClusterLeaseGuard(parent, time.Millisecond, time.Nanosecond, func(ctx context.Context) (bool, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return false, ctx.Err()
	})
	select {
	case <-entered:
	case <-parent.Done():
		close(release)
		guard.Close()
		t.Fatal("authority check did not enter")
	}
	closed := make(chan struct{})
	go func() { guard.Close(); close(closed) }()
	select {
	case <-canceled:
	case <-parent.Done():
		close(release)
		<-closed
		t.Fatal("close did not stop authority check")
	}
	select {
	case <-closed:
		t.Error("close returned before admitted authority check joined")
	default:
	}
	if guard.Context().Err() != nil {
		t.Error("scope canceled before admitted check joined")
	}
	close(release)
	select {
	case <-closed:
		if guard.Context().Err() == nil || guard.Err() != nil {
			t.Fatal("ordinary joined close left live scope or reported ownership loss")
		}
	case <-parent.Done():
		t.Fatal("close did not join released authority check")
	}
}
