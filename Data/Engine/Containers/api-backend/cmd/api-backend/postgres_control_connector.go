package main

import (
	"context"
	"database/sql/driver"
	"net"
	"strings"

	"github.com/lib/pq"
)

// Control-plane transactions hold ownership locks. Cancellation must close the
// active socket even if lib/pq's separate CancelRequest cannot reach the server;
// otherwise an idle transaction could obstruct takeover after its lease expires.
// The server transaction limit also covers partitions dropping TCP disconnects.
type postgresControlConnector struct{ dsn string }

func newBoundedPostgresConnector(dsn string) (*pq.Connector, error) {
	dsn = strings.TrimSpace(dsn)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		var err error
		dsn, err = pq.ParseURL(dsn)
		if err != nil {
			return nil, err
		}
	}
	// PostgreSQL 17 terminates the entire transaction, including idle periods
	// and lock waits. Enforce this per control connection, never cluster-wide.
	return pq.NewConnector(dsn + " transaction_timeout=5000")
}

func (c *postgresControlConnector) Driver() driver.Driver { return &pq.Driver{} }

func (c *postgresControlConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connector, err := newBoundedPostgresConnector(c.dsn)
	if err != nil {
		return nil, err
	}
	lifetimeCtx, cancel := context.WithCancel(context.Background())
	sockets := &leaseSocketDialer{ctx: lifetimeCtx, cancel: cancel, sockets: make(map[net.Conn]struct{})}
	stop := context.AfterFunc(ctx, sockets.close)
	defer stop()
	connector.Dialer(sockets)
	conn, err := connector.Connect(ctx)
	if err != nil || ctx.Err() != nil {
		sockets.close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return &postgresControlConn{Conn: conn, sockets: sockets}, nil
}

type postgresControlConn struct {
	driver.Conn
	sockets *leaseSocketDialer
}

func (c *postgresControlConn) IsValid() bool {
	c.sockets.mu.Lock()
	closed := c.sockets.closed
	c.sockets.mu.Unlock()
	if closed {
		return false
	}
	if valid, ok := c.Conn.(driver.Validator); ok {
		return valid.IsValid()
	}
	return true
}

func (c *postgresControlConn) Close() error {
	c.sockets.close()
	return c.Conn.Close()
}

func (c *postgresControlConn) ResetSession(ctx context.Context) error {
	if !c.IsValid() {
		return driver.ErrBadConn
	}
	if reset, ok := c.Conn.(driver.SessionResetter); ok {
		return reset.ResetSession(ctx)
	}
	return nil
}

func (c *postgresControlConn) Ping(ctx context.Context) error {
	stop := context.AfterFunc(ctx, c.sockets.close)
	defer stop()
	return c.Conn.(driver.Pinger).Ping(ctx)
}

func (c *postgresControlConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	stop := context.AfterFunc(ctx, c.sockets.close)
	defer stop()
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *postgresControlConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	stop := context.AfterFunc(ctx, c.sockets.close)
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		stop()
		return nil, err
	}
	return &postgresControlRows{Rows: rows, stop: stop}, nil
}

func (c *postgresControlConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	stop := context.AfterFunc(ctx, c.sockets.close)
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	if err != nil {
		stop()
		return nil, err
	}
	return &postgresControlTx{Tx: tx, stop: stop}, nil
}

func (c *postgresControlConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stop := context.AfterFunc(ctx, c.sockets.close)
	defer stop()
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &postgresControlStmt{Stmt: stmt, sockets: c.sockets}, nil
}

type postgresControlTx struct {
	driver.Tx
	stop func() bool
}

func (tx *postgresControlTx) Commit() error   { defer tx.stop(); return tx.Tx.Commit() }
func (tx *postgresControlTx) Rollback() error { defer tx.stop(); return tx.Tx.Rollback() }

type postgresControlRows struct {
	driver.Rows
	stop func() bool
}

func (r *postgresControlRows) Close() error { defer r.stop(); return r.Rows.Close() }

type postgresControlStmt struct {
	driver.Stmt
	sockets *leaseSocketDialer
}

func (s *postgresControlStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	stop := context.AfterFunc(ctx, s.sockets.close)
	defer stop()
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *postgresControlStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	stop := context.AfterFunc(ctx, s.sockets.close)
	rows, err := s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		stop()
		return nil, err
	}
	return &postgresControlRows{Rows: rows, stop: stop}, nil
}
