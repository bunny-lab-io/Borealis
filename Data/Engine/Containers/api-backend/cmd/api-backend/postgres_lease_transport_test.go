package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Drop both directions without closing either socket. This reproduces a
// blackhole, including the driver's separate cancellation connection, while
// retaining real PostgreSQL protocol, authentication and query execution.
type postgresBlackholeProxy struct {
	listener net.Listener
	accepted chan struct{}
	blocked  atomic.Bool
	mu       sync.Mutex
	sockets  []net.Conn
	workers  sync.WaitGroup
}

func newPostgresBlackholeProxy(t *testing.T, target string) *postgresBlackholeProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &postgresBlackholeProxy{listener: listener, accepted: make(chan struct{})}
	proxy.workers.Add(1)
	go func() {
		defer proxy.workers.Done()
		defer close(proxy.accepted)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.DialTimeout("tcp", target, time.Second)
			if err != nil {
				_ = client.Close()
				continue
			}
			proxy.mu.Lock()
			proxy.sockets = append(proxy.sockets, client, server)
			proxy.mu.Unlock()
			proxy.workers.Add(2)
			copyTraffic := func(dst, src net.Conn) {
				defer proxy.workers.Done()
				defer func() {
					// A real partition drops FIN/RST too. Local socket close must
					// not rescue remote transaction locks through the proxy.
					if !proxy.blocked.Load() {
						_ = dst.Close()
						_ = src.Close()
					}
				}()
				buffer := make([]byte, 32*1024)
				for {
					n, err := src.Read(buffer)
					if n > 0 && !proxy.blocked.Load() {
						if _, writeErr := io.Copy(dst, bytes.NewReader(buffer[:n])); writeErr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}
			go copyTraffic(server, client)
			go copyTraffic(client, server)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-proxy.accepted
		proxy.mu.Lock()
		for _, conn := range proxy.sockets {
			_ = conn.Close()
		}
		proxy.mu.Unlock()
		proxy.workers.Wait()
	})
	return proxy
}

func TestPostgresLeaseTransportBlackholeAndRecovery(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Host == "" {
		t.Fatal("test requires PostgreSQL URL with TCP host")
	}
	direct, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	proxy := newPostgresBlackholeProxy(t, parsed.Host)
	parsed.Host = proxy.listener.Addr().String()
	application := "lease-blackhole-" + newClusterUUID()
	query := parsed.Query()
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	transport := &postgresLeaseTransport{dsn: parsed.String()}

	leaseCtx, cancelLease := context.WithTimeout(ctx, 2*time.Second)
	defer cancelLease()
	finished := make(chan error, 1)
	go func() {
		_, err := transport.ExecContext(leaseCtx, "SELECT pg_sleep(5)")
		finished <- err
	}()
	for {
		var active bool
		if err := direct.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='active' AND query='SELECT pg_sleep(5)')`, application).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("lease query ended before blackhole: %v", err)
		case <-ctx.Done():
			t.Fatal("real PostgreSQL query never became active")
		case <-time.After(5 * time.Millisecond):
		}
	}
	proxy.blocked.Store(true)
	started := time.Now()
	cancelLease()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blackholed active query returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active lib/pq query waited on blackholed reply/cancellation")
	}
	t.Logf("active blackholed query canceled in %s", time.Since(started))

	// A fresh connection must also stop during blackholed authentication.
	deadlineCtx, deadlineCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer deadlineCancel()
	started = time.Now()
	_, err = transport.ExecContext(deadlineCtx, "SELECT 1")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("blackholed startup err=%v elapsed=%s", err, time.Since(started))
	}
	if err := direct.PingContext(ctx); err != nil {
		t.Fatalf("lease cancellation affected ordinary pool: %v", err)
	}
	proxy.blocked.Store(false)
	if _, err := transport.ExecContext(ctx, "SELECT 1"); err != nil {
		t.Fatalf("lease did not reconnect after healing: %v", err)
	}
}

func TestPostgresControlCancellationReleasesTransactionLocks(t *testing.T) {
	dsn := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Host == "" {
		t.Fatal("test requires PostgreSQL URL with TCP host")
	}
	proxy := newPostgresBlackholeProxy(t, parsed.Host)
	parsed.Host = proxy.listener.Addr().String()
	query := parsed.Query()
	query.Set("transaction_timeout", "0") // Control safety must override caller defaults.
	parsed.RawQuery = query.Encode()
	control := sql.OpenDB(&postgresControlConnector{dsn: parsed.String()})
	defer control.Close()
	control.SetMaxOpenConns(1)
	direct, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var timeout string
	if err := control.QueryRowContext(ctx, `SHOW transaction_timeout`).Scan(&timeout); err != nil || timeout != "5s" {
		t.Fatalf("control transaction bound=%q err=%v", timeout, err)
	}
	txCtx, cancelTx := context.WithCancel(ctx)
	defer cancelTx()
	tx, err := control.BeginTx(txCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	lockID := time.Now().UnixNano()
	if _, err := tx.ExecContext(txCtx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	var acquired bool
	if err := direct.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, lockID).Scan(&acquired); err != nil || acquired {
		t.Fatalf("control transaction never held real lock: acquired=%v err=%v", acquired, err)
	}
	proxy.blocked.Store(true)
	started := time.Now()
	cancelTx()
	for !acquired {
		if err := direct.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, lockID).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if time.Since(started) > 7*time.Second {
			t.Fatal("blackholed canceled transaction retained ownership lock")
		}
		if !acquired {
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Logf("canceled control transaction released real lock in %s", time.Since(started))
	proxy.blocked.Store(false)
	if err := control.PingContext(ctx); err != nil {
		t.Fatalf("control pool failed to replace canceled connection: %v", err)
	}
	// Canceling a completed query/transaction must not close a reused idle socket.
	completedCtx, completedCancel := context.WithCancel(ctx)
	completed, err := control.BeginTx(completedCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := completed.QueryRowContext(completedCtx, `SELECT pg_backend_pid()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := completed.Commit(); err != nil {
		t.Fatal(err)
	}
	completedCancel()
	if err := control.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&after); err != nil || after != before {
		t.Fatalf("finished transaction watcher closed reused connection: before=%d after=%d err=%v", before, after, err)
	}
}
