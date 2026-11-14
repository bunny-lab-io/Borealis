package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

var errPreparedAdmission = errors.New("prepared admission identity guard failed; retain operation for reconciliation")

type preparedAdmissionCheckKey struct{}

// Only the prepared admission context opts into checks at existing child-command
// boundaries. The caller retains all custody until synchronous execution exits.
func checkPreparedAdmissionCommand(ctx context.Context) error {
	if check, ok := ctx.Value(preparedAdmissionCheckKey{}).(func(context.Context) error); ok {
		return check(ctx)
	}
	return nil
}

func parsePreparedAdmissionParams(params map[string]any) (clusterbootstrap.PreparedAdmissionRequest, error) {
	payload, ok := params["payload"].(string)
	if !ok || len(params) != 1 {
		return clusterbootstrap.PreparedAdmissionRequest{}, errPreparedAdmission
	}
	return clusterbootstrap.DecodePreparedAdmissionRequest(payload)
}

func preparedAdmissionStartValid(now time.Time, request clusterbootstrap.PreparedAdmissionRequest) bool {
	return request.StartBefore > now.Unix() && request.StartBefore <= now.Add(5*time.Minute).Unix()
}

func (m *manager) preparedAdmission(ctx context.Context, params map[string]any) (result map[string]any, resultErr error) {
	request, err := parsePreparedAdmissionParams(params)
	now := time.Now()
	if err != nil || m == nil || m.nodeName != request.Expected.Target.Hostname || !preparedAdmissionStartValid(now, request) || os.Geteuid() != 0 || os.Getegid() != 0 {
		return nil, errPreparedAdmission
	}
	ctx, cancel := context.WithTimeout(ctx, nodeManagerActionTimeout(request.Action))
	defer cancel()
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, errPreparedAdmission
	}
	defer func() {
		if root.Close() != nil || ctx.Err() != nil {
			result, resultErr = nil, errPreparedAdmission
		}
	}()
	run := func() error {
		return clusterbootstrap.WithPreparedAdmissionJournal(ctx, bootstrapMutationRoot, request, func(journalCheck func(context.Context) error) error {
			return withPreparedAdmissionNative(ctx, root, identityRuntimeRoot, request, bootstrapJoinedHostCheck(request.Expected, journalCheck), func(ctx context.Context) (*bootstrapMembershipListenerScope, error) {
				kubelet, err := sourceKubelet(ctx)
				if err != nil {
					return nil, errPreparedAdmission
				}
				return captureBootstrapMembershipListener(ctx, kubelet, clusterbootstrap.MembershipListenerProofVersion)
			}, newBootstrapJoinedNodeClient, func(ctx context.Context) error {
				if request.Action == "ReconcilePreparedAdmissionStopped" {
					return nil
				}
				params := map[string]any{}
				switch request.Action {
				case "RedeployRevision", "PromoteCandidate":
					params["target_sha"] = request.Expected.Source.SourceSHA
				case "RunK3sProbeConformance":
					params["k3s_version"] = request.Expected.K3sVersion
				case "EnterApplicationDrain":
					params["reason"] = "prepared admission activation containment"
				}
				_, err := m.execute(ctx, actionRequest{Verb: request.Action, Params: params})
				return err
			})
		})
	}
	if request.Action == "ReconcilePreparedAdmissionStopped" {
		err = reconcilePreparedAdmissionStopped(ctx, request.StartBefore, run)
	} else {
		err = run()
	}
	if err != nil {
		return nil, errPreparedAdmission
	}
	return map[string]any{"version": 1, "request_sha256": request.SHA256()}, nil
}

func reconcilePreparedAdmissionStopped(ctx context.Context, startBefore int64, observe func() error) error {
	ctx, cancel := context.WithDeadline(ctx, time.Unix(startBefore, 0))
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := observe(); !errors.Is(err, clusterbootstrap.ErrMutationBusy) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Fixed paths in production; disposable roots and transport seams in tests.
// Identity consumer blocks recovery while actions create/start consumers. The
// retained K3s PID/executable/listener and exact Node reads prevent a name-only
// replacement from receiving a command. This supplies no controller authority.
func withPreparedAdmissionNative(ctx context.Context, root *os.Root, identityPath string, request clusterbootstrap.PreparedAdmissionRequest,
	hostCheck func(context.Context) error, listenerFor func(context.Context) (*bootstrapMembershipListenerScope, error), clientFor func(*tls.Config) *http.Client,
	action func(context.Context) error) (resultErr error) {
	if ctx.Err() != nil || root == nil || request.Validate() != nil || hostCheck == nil || listenerFor == nil || clientFor == nil || action == nil || hostCheck(ctx) != nil {
		return errPreparedAdmission
	}
	encoded, err := clusterbootstrap.EncodePreparedAdmissionRequest(request)
	if err != nil {
		return errPreparedAdmission
	}
	request, err = clusterbootstrap.DecodePreparedAdmissionRequest(encoded)
	if err != nil {
		return errPreparedAdmission
	}
	consumer, err := engineidentity.AcquireConsumer(identityPath)
	if err != nil {
		return errPreparedAdmission
	}
	defer func() {
		if consumer.Close() != nil || ctx.Err() != nil {
			resultErr = errPreparedAdmission
		}
	}()
	identityCheck := func(current context.Context) error {
		if ctx.Err() != nil || current.Err() != nil || hostCheck(current) != nil {
			return errPreparedAdmission
		}
		material, err := consumer.Load()
		if err != nil || material.Digest() != request.Receipt.Steps[3].ResultSHA256 {
			return errPreparedAdmission
		}
		return nil
	}
	if identityCheck(ctx) != nil {
		return errPreparedAdmission
	}
	listener, err := listenerFor(ctx)
	if listener != nil && listener.close != nil {
		defer func() {
			if listener.close() != nil || ctx.Err() != nil {
				resultErr = errPreparedAdmission
			}
		}()
	}
	if err != nil || listener == nil || listener.check == nil || listener.close == nil || listener.proofVersion != clusterbootstrap.MembershipListenerProofVersion {
		return errPreparedAdmission
	}
	check := func(current context.Context) error {
		if identityCheck(current) != nil || listener.check(current) != nil {
			return errPreparedAdmission
		}
		return nil
	}
	return withBootstrapK3sObservationFiles(ctx, root, request.Expected.Target.Hostname, true, check, func(files []bootstrapHostFile, refresh func(context.Context) error, memberName string) error {
		if memberName != request.Receipt.MemberName {
			return errPreparedAdmission
		}
		pair, err := tls.X509KeyPair(files[1].data, files[2].data)
		if err != nil || len(pair.Certificate) == 0 {
			return errPreparedAdmission
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || leaf.Subject.CommonName != "system:node:"+request.Expected.Target.Hostname || len(leaf.Subject.Organization) != 1 || leaf.Subject.Organization[0] != "system:nodes" {
			return errPreparedAdmission
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(files[0].data) {
			return errPreparedAdmission
		}
		config := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		defer func() { pair.PrivateKey = nil; config.Certificates = nil; config.RootCAs = nil }()
		client := clientFor(config)
		if client == nil {
			return errPreparedAdmission
		}
		defer client.CloseIdleConnections()
		closed, failed := false, false
		var mu sync.Mutex
		defer func() { mu.Lock(); closed = true; mu.Unlock() }()
		prove := func(current context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			if closed || failed || current.Err() != nil || refresh(current) != nil {
				failed = true
				return errPreparedAdmission
			}
			raw, err := sourceNetworkHTTPGet(current, client, "https://127.0.0.1:6443", "/api/v1/nodes/"+request.Expected.Target.Hostname)
			defer clear(raw)
			if err != nil {
				failed = true
				return errPreparedAdmission
			}
			if _, err := clusterbootstrap.ParsePreparedAdmissionNode(raw, request); err != nil || refresh(current) != nil {
				failed = true
				return errPreparedAdmission
			}
			return nil
		}
		if prove(ctx) != nil {
			return errPreparedAdmission
		}
		guarded := context.WithValue(ctx, preparedAdmissionCheckKey{}, (func(context.Context) error)(prove))
		err = action(guarded)
		if final := prove(ctx); final != nil {
			return errors.Join(err, final)
		}
		return err
	})
}

func readPreparedAdmissionAcknowledgement(ctx context.Context, client *http.Client, token, payload string) (raw []byte, resultErr error) {
	request, err := clusterbootstrap.DecodePreparedAdmissionRequest(payload)
	if err != nil || client == nil || ctx.Err() != nil || len(token) < 32 || len(token) > 4096 {
		return nil, errPreparedAdmission
	}
	body, err := json.Marshal(actionRequest{Verb: clusterbootstrap.PreparedAdmissionVerb, Params: map[string]any{"payload": payload}})
	if err != nil {
		return nil, errPreparedAdmission
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://node-manager/v1/action", bytes.NewReader(body))
	if err != nil {
		return nil, errPreparedAdmission
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(managerTokenHeader, token)
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	copyClient.Transport = sourceNetworkResponseTransport{transport}
	response, err := copyClient.Do(req)
	if err != nil {
		return nil, errPreparedAdmission
	}
	defer func() {
		if response.Body.Close() != nil || ctx.Err() != nil {
			clear(raw)
			raw, resultErr = nil, errPreparedAdmission
		}
	}()
	if response.StatusCode != http.StatusOK {
		return nil, errPreparedAdmission
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || ctx.Err() != nil || clusterbootstrap.ValidatePreparedAdmissionAcknowledgement(raw, request) != nil {
		return nil, errPreparedAdmission
	}
	return raw, nil
}
