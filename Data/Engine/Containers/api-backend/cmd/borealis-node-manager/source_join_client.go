package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
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

func sourceJoinClient(args []string) error {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if len(args) != 1 {
		return fail
	}
	request, err := clusterbootstrap.ParseK3sJoinSourceRequest([]byte(args[0]), time.Now())
	job, pod := os.Getenv("BOREALIS_SOURCE_JOB_UID"), os.Getenv("BOREALIS_SOURCE_POD_UID")
	if err != nil || !clusterbootstrap.ValidSourceReceiptIdentity(request.ID, job, pod) {
		return fail
	}
	secret := []byte(strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET")))
	defer clear(secret)
	if len(secret) < 32 || len(secret) > 4096 {
		return fail
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	file, err := os.OpenFile(defaultSecretPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fail
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 32 || info.Size() > 4096 {
		file.Close()
		return fail
	}
	token, err := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	defer clear(token)
	if err != nil || closeErr != nil || len(token) > 4096 {
		return fail
	}
	localTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", defaultSocketPath)
	}, DisableKeepAlives: true, DisableCompression: true}
	defer localTransport.CloseIdleConnections()
	wire, receipt, err := readSourceJoinDelivery(ctx, &http.Client{Transport: localTransport}, strings.TrimSpace(string(token)), secret, request, job, pod)
	defer clear(wire)
	if err != nil {
		return fail
	}
	// Resolve only fixed internal service; accept bounded private IPv4 peers.
	lookup, stop := context.WithTimeout(ctx, 3*time.Second)
	addresses, err := net.DefaultResolver.LookupIPAddr(lookup, "borealis-cluster-source.borealis.svc")
	stop()
	if err != nil || len(addresses) < 1 || len(addresses) > 3 {
		return fail
	}
	var peers []string
	for _, address := range addresses {
		if address.IP.To4() == nil || !address.IP.IsPrivate() || address.Zone != "" {
			return fail
		}
		peers = append(peers, "http://"+net.JoinHostPort(address.IP.String(), "8090"))
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	if deliverSourceJoin(ctx, &http.Client{Transport: transport, Timeout: 4 * time.Second}, peers, request, wire) != nil {
		return fail
	}
	public, err := json.Marshal(receipt)
	if err != nil || len(public) > 4096 || ctx.Err() != nil || request.Validate(time.Now()) != nil {
		return fail
	}
	output, err := os.OpenFile("/dev/termination-log", os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fail
	}
	defer output.Close()
	info, err = output.Stat()
	if err != nil || !info.Mode().IsRegular() || output.Truncate(0) != nil {
		return fail
	}
	if n, err := output.Write(public); err != nil || n != len(public) || output.Sync() != nil {
		return fail
	}
	return nil
}

func readSourceJoinDelivery(ctx context.Context, client *http.Client, token string, secret []byte, request clusterbootstrap.K3sJoinSourceRequest, job, pod string) ([]byte, clusterbootstrap.K3sJoinSourceReceipt, error) {
	fail := func() ([]byte, clusterbootstrap.K3sJoinSourceReceipt, error) {
		return nil, clusterbootstrap.K3sJoinSourceReceipt{}, clusterbootstrap.ErrK3sJoinMaterial
	}
	if client == nil || len(token) < 32 || len(token) > 4096 || len(secret) < 32 || len(secret) > 4096 || request.Validate(time.Now()) != nil || !clusterbootstrap.ValidSourceReceiptIdentity(request.ID, job, pod) || ctx.Err() != nil {
		return fail()
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return fail()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://node-manager"+clusterbootstrap.K3sJoinSourceSocketPath, bytes.NewReader(raw))
	if err != nil {
		return fail()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(managerTokenHeader, token)
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copyClient.Do(req)
	if err != nil {
		return fail()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/octet-stream" {
		return fail()
	}
	maximum := clusterbootstrap.MaxK3sJoinTokenBytes + clusterbootstrap.MaxK3sJoinCABytes + 1024
	// Fixed allocation avoids abandoned plaintext copies from ReadAll growth.
	frame := make([]byte, maximum+1)
	defer clear(frame)
	n, err := io.ReadFull(response.Body, frame)
	if (err != io.ErrUnexpectedEOF && err != io.EOF) || n == 0 || n > maximum {
		return fail()
	}
	check := func(ctx context.Context) error {
		if ctx.Err() != nil {
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		return request.Validate(time.Now())
	}
	input := bytes.NewReader(frame[:n])
	material, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, request.Expected, request.Source, input, check)
	if err != nil {
		return fail()
	}
	defer material.Close()
	if input.Len() != 0 || check(ctx) != nil {
		return fail()
	}
	return clusterbootstrap.SealK3sJoinSourceDelivery(secret, request, job, pod, frame[:n], time.Now())
}

func deliverSourceJoin(ctx context.Context, client *http.Client, peers []string, request clusterbootstrap.K3sJoinSourceRequest, wire []byte) error {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if client == nil || len(peers) < 1 || len(peers) > 3 || len(wire) < 1 || len(wire) > clusterbootstrap.MaxK3sJoinSourceDeliveryBytes {
		return fail
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		// Revalidate fixed endpoint even when called outside DNS wrapper.
		ip := strings.TrimSuffix(strings.TrimPrefix(peer, "http://"), ":8090")
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil || !parsed.IsPrivate() || peer != "http://"+net.JoinHostPort(parsed.String(), "8090") || request.Validate(time.Now()) != nil || ctx.Err() != nil {
			return fail
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterbootstrap.K3sJoinSourcePath, bytes.NewReader(wire))
		if err != nil {
			return fail
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set(clusterbootstrap.K3sJoinSourceRequestHeader, request.ID)
		response, err := copyClient.Do(req)
		if err != nil {
			continue
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNoContent && request.Validate(time.Now()) == nil && ctx.Err() == nil {
			return nil
		}
		if response.StatusCode != http.StatusNotFound {
			return fail
		}
	}
	return fail
}
