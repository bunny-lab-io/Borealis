package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Exercise the actual HTTP cancellation/SQL lifetime boundary: client return
// does not imply that the server has returned its borrowed connection yet.
func testClusterSSHSourceBrokerPostgresHandlerLifecycle(t *testing.T) {
	f := newSSHPreparationAuthorityFixture(t)
	f.c.store.db.SetMaxOpenConns(1)
	before := f.events(t)
	acquired, cancelled, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	allowCleanup := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		connection, err := f.c.store.db.Conn(r.Context())
		if err != nil {
			t.Error("handler pool acquisition", err)
			return
		}
		defer func() {
			if connection.Close() != nil {
				t.Error("handler pool release")
			}
		}()
		close(acquired)
		<-r.Context().Done()
		close(cancelled)
		<-release
	}))
	defer server.Close()
	defer allowCleanup()
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	clientDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		clientDone <- err
	}()
	select {
	case <-acquired:
	case <-f.ctx.Done():
		t.Fatal("handler did not acquire connection", f.ctx.Err())
	}
	cancel()
	select {
	case err := <-clientDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("client cancellation cause lost", err)
		}
	case <-f.ctx.Done():
		t.Fatal("client did not return after cancellation", f.ctx.Err())
	}
	select {
	case <-cancelled:
	case <-f.ctx.Done():
		t.Fatal("handler did not observe cancellation", f.ctx.Err())
	}
	select {
	case <-finished:
		t.Fatal("handler skipped controlled cleanup")
	default:
	}
	if f.c.store.db.Stats().InUse != 1 {
		t.Fatal("fixture did not retain connection after client return")
	}
	allowCleanup()
	server.Close()
	if f.c.store.db.Stats().InUse != 0 || f.events(t) != before {
		t.Fatal("completed handler retained connection or wrote event")
	}
}
