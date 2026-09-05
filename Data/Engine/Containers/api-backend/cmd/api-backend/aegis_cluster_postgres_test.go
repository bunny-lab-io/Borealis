package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestAegisClusterPostgresRenewedHolderUnlocksJoiningReplica(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	key := bytes.Repeat([]byte{42}, aegisKeyLength)
	token, err := aegisEncryptText(aegisVerificationPlaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	// Disposable PostgreSQL lane only. Refuse to overwrite an existing cipher.
	if _, err := db.ExecContext(ctx, `INSERT INTO engine.aegis_cipher_state
		(id,kdf_name,kdf_params_json,verification_token,created_at,updated_at)
		VALUES(1,'scrypt','{}',$1,1,1)`, token); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, `DELETE FROM engine.aegis_cipher_state WHERE id=1 AND verification_token=$1`, token); err != nil {
			t.Error(err)
		}
	}()
	holder := newGoAegisService(db, nil)
	if err := holder.installClusterKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	joining := newGoAegisService(db, nil)
	if _, err := joining.activeKey(); !errors.Is(err, errAegisLocked) {
		t.Fatalf("joining process must start locked: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	ca := newAegisTestCA(t, now, 1)
	old, renewed := ca.identity(t, now, 10, time.Hour), ca.identity(t, now, 11, 4*time.Hour)
	holderTLS := newAegisTestProjection(t, now, old, ca.pem)
	joiningTLS := newAegisTestProjection(t, now, old, ca.pem)
	source := &authService{aegis: holder, aegisTLS: holderTLS.loader}
	t.Setenv("BOREALIS_AEGIS_CLUSTER_FANOUT_ENABLED", "1")
	t.Setenv("BOREALIS_AEGIS_CLUSTER_LISTEN_HOST", "127.0.0.1")
	t.Setenv("BOREALIS_AEGIS_CLUSTER_PEER_HOST", "127.0.0.1")
	t.Setenv("BOREALIS_AEGIS_CLUSTER_PORT", "0")
	server, exited, err := startAegisClusterKeyServer(&authService{aegis: joining, aegisTLS: joiningTLS.loader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		server.Close()
		if err := <-exited; err != nil {
			t.Error(err)
		}
	}()
	_, port, err := net.SplitHostPort(server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_AEGIS_CLUSTER_PORT", port)
	if _, err := holderTLS.loader.config(false); err != nil {
		t.Fatal(err)
	}
	// Existing receiver listener and the only unlocked holder outlive old leaf.
	for _, projection := range []*aegisTestProjection{holderTLS, joiningTLS} {
		projection.update(t, renewed.cert, renewed.key, ca.pem)
		projection.clock.Store(now.Add(2 * time.Hour).UnixNano())
	}
	count, err := fanoutAegisClusterKey(ctx, source)
	if err != nil || count != 1 {
		t.Fatalf("renewed holder failed to unlock joining replica: count=%d err=%v", count, err)
	}
	for _, service := range []*goAegisService{holder, joining} {
		actual, err := service.activeKey()
		if err != nil || !bytes.Equal(actual, key) {
			t.Fatalf("memory key not preserved/transferred: %v", err)
		}
		zeroBytes(actual)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("key verification leaked a database connection")
	}
	// TLS authentication alone cannot install a key failing database verification.
	joining.setActiveKey(nil)
	holder.setActiveKey(bytes.Repeat([]byte{7}, aegisKeyLength))
	if count, err := fanoutAegisClusterKey(ctx, source); err == nil || count != 0 {
		t.Fatalf("invalid Aegis key accepted: count=%d err=%v", count, err)
	}
	if _, err := joining.activeKey(); !errors.Is(err, errAegisLocked) {
		t.Fatal("invalid key unlocked joining replica")
	}
	for range 2 {
		cold := newGoAegisService(db, nil)
		if count, err := fanoutAegisClusterKey(ctx, &authService{aegis: cold}); !errors.Is(err, errAegisLocked) || count != 0 {
			t.Fatalf("cold restart acquired a persisted key: count=%d err=%v", count, err)
		}
	}
}

func TestAegisClusterFanoutDoesNotFollowKeyRedirect(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	ca := newAegisTestCA(t, now, 1)
	identity := ca.identity(t, now, 10, time.Hour)
	p := newAegisTestProjection(t, now, identity, ca.pem)
	var leaked atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	server := aegisTestTLSServer(t, p.loader, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_AEGIS_CLUSTER_FANOUT_ENABLED", "1")
	t.Setenv("BOREALIS_AEGIS_CLUSTER_PEER_HOST", "127.0.0.1")
	t.Setenv("BOREALIS_AEGIS_CLUSTER_PORT", port)
	holder := newGoAegisService(nil, nil)
	holder.setActiveKey(bytes.Repeat([]byte{42}, aegisKeyLength))
	count, err := fanoutAegisClusterKey(context.Background(), &authService{aegis: holder, aegisTLS: p.loader})
	if err == nil || count != 0 || leaked.Load() != 0 {
		t.Fatalf("key redirect was not rejected: count=%d err=%v destinationCalls=%d", count, err, leaked.Load())
	}
}
