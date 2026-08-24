package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type aegisClusterKeyInstallerStub struct {
	key []byte
	err error
}

func (s *aegisClusterKeyInstallerStub) installClusterKey(_ context.Context, key []byte) error {
	s.key = append([]byte(nil), key...)
	return s.err
}

func TestAegisClusterKeyHandlerRequiresVerifiedClientCertificate(t *testing.T) {
	installer := &aegisClusterKeyInstallerStub{}
	request := httptest.NewRequest(http.MethodPost, aegisClusterKeyPath, strings.NewReader(`{"key":"value"}`))
	recorder := httptest.NewRecorder()
	aegisClusterKeyHandler(installer).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || len(installer.key) != 0 {
		t.Fatalf("expected unverified client rejection, status=%d key=%x", recorder.Code, installer.key)
	}
}

func TestAegisClusterKeyHandlerInstallsBoundedVerifiedKey(t *testing.T) {
	installer := &aegisClusterKeyInstallerStub{}
	key := []byte("0123456789abcdef0123456789abcdef")
	request := httptest.NewRequest(http.MethodPost, aegisClusterKeyPath, strings.NewReader(`{"key":"`+base64.StdEncoding.EncodeToString(key)+`"}`))
	request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	recorder := httptest.NewRecorder()
	aegisClusterKeyHandler(installer).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || string(installer.key) != string(key) {
		t.Fatalf("expected verified key install, status=%d key=%x", recorder.Code, installer.key)
	}
}

func TestAegisClusterKeyHandlerRejectsWrongLength(t *testing.T) {
	installer := &aegisClusterKeyInstallerStub{}
	request := httptest.NewRequest(http.MethodPost, aegisClusterKeyPath, strings.NewReader(`{"key":"`+base64.StdEncoding.EncodeToString([]byte("short"))+`"}`))
	request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	recorder := httptest.NewRecorder()
	aegisClusterKeyHandler(installer).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || len(installer.key) != 0 {
		t.Fatalf("expected invalid key rejection, status=%d key=%x", recorder.Code, installer.key)
	}
}

func TestAegisClusterKeyHandlerRejectsUnknownOrTrailingJSON(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	for _, body := range []string{
		`{"key":"` + key + `","unexpected":true}`,
		`{"key":"` + key + `"}{"key":"` + key + `"}`,
	} {
		installer := &aegisClusterKeyInstallerStub{}
		request := httptest.NewRequest(http.MethodPost, aegisClusterKeyPath, strings.NewReader(body))
		request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		recorder := httptest.NewRecorder()
		aegisClusterKeyHandler(installer).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || len(installer.key) != 0 {
			t.Fatalf("expected strict JSON rejection, status=%d key=%x", recorder.Code, installer.key)
		}
	}
}

func TestAegisClusterKeyFanoutLoopReconcilesNewReplicasUntilStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int64
	runAegisClusterKeyFanoutLoop(ctx, time.Millisecond, func(context.Context) error {
		calls.Add(1)
		return nil
	})
	deadline := time.Now().Add(250 * time.Millisecond)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected periodic key reconciliation, calls=%d", calls.Load())
	}
	cancel()
	time.Sleep(5 * time.Millisecond)
	stoppedAt := calls.Load()
	time.Sleep(5 * time.Millisecond)
	if calls.Load() != stoppedAt {
		t.Fatalf("key reconciliation continued after shutdown: before=%d after=%d", stoppedAt, calls.Load())
	}
}
