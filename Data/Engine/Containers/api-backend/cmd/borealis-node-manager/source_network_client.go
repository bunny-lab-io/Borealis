package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// This client has no configurable verb, socket, credential path or output path.
// Kubernetes supplies the public Job/Pod IDs through the downward API. The
// controller independently verifies both objects and their ownership chain.
func sourceNetworkClient(args []string) error {
	jobUID, podUID := os.Getenv("BOREALIS_SOURCE_JOB_UID"), os.Getenv("BOREALIS_SOURCE_POD_UID")
	if len(args) != 1 || !clusterbootstrap.ValidSourceReceiptIdentity(args[0], jobUID, podUID) {
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
	receipt, err := readSourceNetworkReceipt(ctx, &http.Client{Transport: transport}, strings.TrimSpace(string(raw)), args[0], jobUID, podUID)
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

func readSourceNetworkReceipt(ctx context.Context, client *http.Client, token, nonce, jobUID, podUID string) ([]byte, error) {
	if client == nil || len(token) < 32 || len(token) > 4096 || !clusterbootstrap.ValidSourceReceiptIdentity(nonce, jobUID, podUID) {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://node-manager/v1/action", strings.NewReader(`{"verb":"InspectSourceNetwork","params":{}}`))
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(managerTokenHeader, token)
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, clusterbootstrap.SourceNetworkReceiptLimit+1))
	if err != nil || ctx.Err() != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return clusterbootstrap.NewSourceNetworkReceipt(raw, nonce, jobUID, podUID)
}
