package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// Fixed read-only client. Socket/token/output paths remain existing local contract.
func sourceMembershipClient(args []string) error {
	jobUID, podUID := os.Getenv("BOREALIS_SOURCE_JOB_UID"), os.Getenv("BOREALIS_SOURCE_POD_UID")
	if len(args) != 2 || !clusterbootstrap.ValidSourceReceiptIdentity(args[0], jobUID, podUID) {
		return clusterbootstrap.ErrPreparationConfig
	}
	if _, err := clusterbootstrap.DecodeMembershipObservationRequest(args[1]); err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	file, err := os.OpenFile(defaultSecretPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return clusterbootstrap.ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	_ = file.Close()
	if err != nil || len(raw) > 4096 {
		return clusterbootstrap.ErrPreparationConfig
	}
	defer clear(raw)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", defaultSocketPath)
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	receipt, err := readSourceMembershipReceipt(ctx, &http.Client{Transport: transport}, strings.TrimSpace(string(raw)), args[0], jobUID, podUID, args[1])
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	// Kubelet provides this writable file even with a read-only container root.
	// Never create a replacement or follow a symlink supplied by another path.
	output, err := os.OpenFile("/dev/termination-log", os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	defer output.Close()
	info, err = output.Stat()
	if err != nil || !info.Mode().IsRegular() || output.Truncate(0) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	if n, err := output.Write(receipt); err != nil || n != len(receipt) || output.Sync() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

func readSourceMembershipReceipt(ctx context.Context, client *http.Client, token, nonce, jobUID, podUID, payload string) ([]byte, error) {
	request, err := clusterbootstrap.DecodeMembershipObservationRequest(payload)
	if client == nil || ctx.Err() != nil || len(token) < 32 || len(token) > 4096 || err != nil || !clusterbootstrap.ValidSourceReceiptIdentity(nonce, jobUID, podUID) {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err := json.Marshal(actionRequest{Verb: "InspectSourceMembership", Params: map[string]any{"payload": payload}})
	if err != nil || len(raw) > maxRequestBytes {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://node-manager/v1/action", strings.NewReader(string(raw)))
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(managerTokenHeader, token)
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(req)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, clusterbootstrap.MembershipObservationResponseLimit+1))
	if err != nil || ctx.Err() != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return clusterbootstrap.NewMembershipObservationReceipt(raw, request, nonce, jobUID, podUID)
}

func (m *manager) inspectSourceMembership(ctx context.Context, params map[string]any) (map[string]any, error) {
	payload, ok := params["payload"].(string)
	if !ok || len(params) != 1 {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	request, err := clusterbootstrap.DecodeMembershipObservationRequest(payload)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	result, err := m.observeBootstrapMembership(ctx, bootstrapMembershipObservationRequest(request), func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	encoded, err := clusterbootstrap.EncodeMembershipObservationResult(result, request)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return map[string]any{"membership_observation": encoded}, nil
}
