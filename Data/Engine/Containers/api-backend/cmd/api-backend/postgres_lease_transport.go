package main

import (
	"context"
	"database/sql"
	"net"
	"sync"
	"time"

	"github.com/lib/pq"
)

type leaseExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Lease requests own their sockets, including lib/pq's separate CancelRequest
// socket. Cancellation closes them directly; it never waits for PostgreSQL to
// acknowledge a cancellation across a failed primary or a network partition.
// Ordinary application pools and long-running queries keep their own policy.
type postgresLeaseTransport struct {
	dsn string
}

func (p *postgresLeaseTransport) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	connector, err := pq.NewConnector(p.dsn)
	if err != nil {
		return nil, err
	}
	dialer := &leaseSocketDialer{ctx: ctx, sockets: make(map[net.Conn]struct{})}
	stop := context.AfterFunc(ctx, dialer.close)
	defer stop()
	connector.Dialer(dialer)
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer db.Close()
	defer dialer.close()
	result, err := db.ExecContext(ctx, query, args...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, err
}

type leaseSocketDialer struct {
	ctx     context.Context
	mu      sync.Mutex
	closed  bool
	sockets map[net.Conn]struct{}
}

func (d *leaseSocketDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(d.ctx, network, address)
}

func (d *leaseSocketDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}

func (d *leaseSocketDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	defer cancel()
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.ctx.Err() != nil {
		_ = conn.Close()
		return nil, context.Canceled
	}
	d.sockets[conn] = struct{}{}
	return conn, nil
}

func (d *leaseSocketDialer) close() {
	d.mu.Lock()
	d.closed = true
	sockets := d.sockets
	d.sockets = nil
	d.mu.Unlock()
	for conn := range sockets {
		_ = conn.Close()
	}
}
