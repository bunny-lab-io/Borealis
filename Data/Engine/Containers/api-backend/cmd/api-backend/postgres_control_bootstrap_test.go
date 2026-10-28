package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestPostgresControlBootstrapOutlivesOwnershipTransactionBudget(t *testing.T) {
	dsn := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	cfg := gatewayConfig{DatabaseURL: dsn, DBSSLMode: "disable", DBConnectTimeout: 5 * time.Second,
		DBMaxOpenConns: 2, DBMaxIdleConns: 1}
	_, closeSetup, err := openOperatorStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	closeSetup()
	direct, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	blocker, err := direct.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, `LOCK TABLE assemblies.official_catalog_state IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	application := "controller-bootstrap-" + newClusterUUID()
	query := parsed.Query()
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	cfg.DatabaseURL = parsed.String()
	type result struct {
		store operatorStore
		err   error
	}
	done := make(chan result)
	releaseStore := make(chan struct{})
	finished := make(chan struct{})
	defer func() {
		close(releaseStore)
		_ = blocker.Rollback()
		<-finished
	}()
	go func() {
		defer close(finished)
		store, closeStore, err := openControlOperatorStore(cfg)
		defer closeStore()
		select {
		case done <- result{store: store, err: err}:
			<-releaseStore
		case <-releaseStore:
		}
	}()
	var bootstrapPID int
	for bootstrapPID == 0 {
		err := direct.QueryRowContext(ctx, `SELECT COALESCE(MAX(pid),0) FROM pg_stat_activity
			WHERE application_name=$1 AND wait_event_type='Lock'`, application).Scan(&bootstrapPID)
		if err != nil {
			t.Fatal(err)
		}
		if bootstrapPID != 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("controller bootstrap ended before DDL lock wait: %v", r.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case r := <-done:
		t.Fatalf("schema bootstrap inherited ownership transaction timeout: %v", r.err)
	case <-time.After(5250 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	var opened result
	select {
	case opened = <-done:
		if opened.err != nil {
			t.Fatal(opened.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	store := opened.store.(*postgresOperatorStore)
	var timeout string
	if err := store.db.QueryRowContext(ctx, `SHOW transaction_timeout`).Scan(&timeout); err != nil || timeout != "5s" {
		t.Fatalf("post-bootstrap control transaction limit=%q err=%v", timeout, err)
	}
	var bootstrapStillOpen bool
	if err := direct.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)`, bootstrapPID).Scan(&bootstrapStillOpen); err != nil {
		t.Fatal(err)
	}
	if bootstrapStillOpen {
		t.Fatal("ordinary bootstrap connection remained open after bounded pool handoff")
	}
}
