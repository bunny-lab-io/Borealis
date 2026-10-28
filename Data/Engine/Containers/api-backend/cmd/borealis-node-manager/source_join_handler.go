package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type sourceJoinRead func(context.Context, clusterbootstrap.PreparationExpected, engineidentity.Binding,
	func(context.Context) error, func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error) error

// This endpoint exists only on the authenticated local Unix socket. It does not
// return a generic action result. Request is public; response is a private frame
// which the fixed non-root Job must encrypt before sending its callback.
func (m *manager) handleSourceJoinMaterial(w http.ResponseWriter, r *http.Request) {
	observe := func(ctx context.Context) (clusterbootstrap.SourceNetwork, error) {
		result, err := m.inspectSourceNetwork(ctx)
		network, ok := result["source_network"].(clusterbootstrap.SourceNetwork)
		if err != nil || !ok {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrK3sJoinMaterial
		}
		return network, nil
	}
	identity := func(request clusterbootstrap.K3sJoinSourceRequest) error {
		if m.nodeName != request.Network.Hostname {
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		for path, expected := range map[string]string{"/etc/machine-id": request.Network.MachineID, "/proc/sys/kernel/random/boot_id": request.Network.BootID} {
			raw, err := os.ReadFile(path)
			if err != nil || len(raw) > 64 || strings.TrimSpace(string(raw)) != expected {
				return clusterbootstrap.ErrK3sJoinMaterial
			}
		}
		return nil
	}
	serveSourceJoinMaterial(w, r, observe, identity, withSourceK3sJoinMaterial)
}

func serveSourceJoinMaterial(w http.ResponseWriter, r *http.Request, observe func(context.Context) (clusterbootstrap.SourceNetwork, error),
	identity func(clusterbootstrap.K3sJoinSourceRequest) error, read sourceJoinRead) {
	fail := func() { http.Error(w, "source join material unavailable", http.StatusConflict) }
	if r.Method != http.MethodPost || r.URL.Path != clusterbootstrap.K3sJoinSourceSocketPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Content-Encoding") != "" || observe == nil || identity == nil || read == nil {
		fail()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, err := io.ReadAll(io.LimitReader(r.Body, clusterbootstrap.MaxK3sJoinSourceRequestBytes+1))
	if err != nil {
		fail()
		return
	}
	request, err := clusterbootstrap.ParseK3sJoinSourceRequest(raw, time.Now())
	if err != nil {
		fail()
		return
	}
	check := func(ctx context.Context) error {
		if ctx.Err() != nil || request.Validate(time.Now()) != nil || identity(request) != nil {
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		return nil
	}
	prove := func() bool {
		if check(ctx) != nil {
			return false
		}
		network, err := observe(ctx)
		return err == nil && network == request.Network && check(ctx) == nil
	}
	if !prove() {
		fail()
		return
	}
	// Allocate final bound once so buffer growth cannot leave abandoned copies.
	storage := make([]byte, clusterbootstrap.MaxK3sJoinTokenBytes+clusterbootstrap.MaxK3sJoinCABytes+1024)
	defer clear(storage)
	output := bytes.NewBuffer(storage[:0])
	err = read(ctx, request.Expected, request.Source, check, func(ctx context.Context, material *clusterbootstrap.K3sJoinMaterial, refresh func(context.Context) error) error {
		return material.WriteJoinMaterial(ctx, request.Expected, request.Source, output, refresh)
	})
	if err != nil || output.Len() == 0 || output.Len() > len(storage) || !prove() {
		fail()
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(output.Bytes())
}
