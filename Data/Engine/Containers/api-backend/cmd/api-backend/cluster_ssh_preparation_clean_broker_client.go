package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/cipher"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

const clusterSSHPreparationCleanBrokerHost = "api-backend.borealis.svc"

type clusterSSHPreparationCleanBrokerClient struct {
	requestCipher, responseCipher cipher.AEAD
	httpClient                    *http.Client
	endpoint                      func(context.Context) (string, error)
}

func newClusterSSHPreparationCleanBrokerClientFromEnv() (*clusterSSHPreparationCleanBrokerClient, error) {
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	request, err := clusterExistingIdentityScopedCipher(secret, "request", clusterSSHPreparationCleanBrokerScope)
	if err != nil {
		return nil, err
	}
	response, err := clusterExistingIdentityScopedCipher(secret, "response", clusterSSHPreparationCleanBrokerScope)
	if err != nil {
		return nil, err
	}
	return &clusterSSHPreparationCleanBrokerClient{requestCipher: request, responseCipher: response,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
			DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 30 * time.Second,
		}}, endpoint: clusterSSHPreparationCleanBrokerEndpoint}, nil
}

func clusterSSHPreparationCleanBrokerEndpoint(parent context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, clusterSSHPreparationCleanBrokerHost)
	// Existing API ClusterIP Service, fixed port only. Neither environment nor
	// proposal input can select another host, URL, port or proxy.
	if err != nil || len(addresses) != 1 || addresses[0].IP.To4() == nil || !addresses[0].IP.IsPrivate() || addresses[0].Zone != "" {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	return "http://" + net.JoinHostPort(addresses[0].IP.String(), "5000"), nil
}

func (c *clusterController) preparationCleanTargetsBrokerRead(snapshot clusterSSHPreparationApprovalSnapshot) func(context.Context, string) (string, error) {
	client, err := newClusterSSHPreparationCleanBrokerClientFromEnv()
	if c == nil || c.store == nil || c.holder != snapshot.work.Authorization.ControllerHolder || err != nil {
		return nil
	}
	return client.preparationRead(snapshot, c.preparationCleanTargetsProposalCheck(snapshot))
}

// Each invocation makes a new authenticated request and repeats native cohort
// observations. The original proposal checker remains local; receipt equality
// cannot substitute for fresh SQL authority or extend the approval's deadline.
func (c *clusterSSHPreparationCleanBrokerClient) preparationRead(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error) func(context.Context, string) (string, error) {
	read := c.preparationTargetsRead(snapshot, check)
	return func(ctx context.Context, plan string) (string, error) {
		value, err := read(ctx, plan)
		return value.Digest, err
	}
}

func (c *clusterSSHPreparationCleanBrokerClient) preparationTargetsRead(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error) clusterSSHPreparationTargetsRead {
	return c.preparationTargetsFilesystemRead(snapshot, check, nil)
}

func (c *clusterSSHPreparationCleanBrokerClient) preparationTargetsFilesystemRead(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error, paths []string) clusterSSHPreparationTargetsRead {
	paths = slices.Clone(paths)
	plan, proposal, bindingErr := clusterSSHPreparationCleanBrokerBinding(snapshot)
	authorization := snapshot.work.Authorization
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	retained := ""
	var retainedFilesystems []clusterSSHTargetFilesystemEvidence
	var cleanup *clusterSSHMembershipHTTP
	var client *http.Client
	if c != nil && c.httpClient != nil && c.httpClient.Jar == nil {
		copy := *c.httpClient
		transport := copy.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		cleanup = &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
		copy.Transport = cleanup
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if copy.Timeout <= 0 || copy.Timeout > 30*time.Second {
			copy.Timeout = 30 * time.Second
		}
		client = &copy
	}
	return func(parent context.Context, requested string) (clusterSSHPreparationCleanObservation, error) {
		fail := func() (clusterSSHPreparationCleanObservation, error) {
			failed.Store(true)
			return clusterSSHPreparationCleanObservation{}, clusterbootstrap.ErrPreparationConfig
		}
		if !validClusterSSHPreparationFilesystemPaths(paths) || c == nil || client == nil || cleanup == nil || c.endpoint == nil || check == nil || bindingErr != nil || requested != plan || failed.Load() || parent.Err() != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail()
		}
		if failed.Load() || !cleanup.clean() || check(ctx) != nil || ctx.Err() != nil {
			return fail()
		}
		deadline, _ := ctx.Deadline()
		request := clusterSSHPreparationCleanBrokerRequest{Version: clusterSSHPreparationCleanBrokerVersion, ID: newClusterUUID(), ExpiresAt: deadline.Unix(),
			OperationID: authorization.OperationID, Attempt: authorization.Attempt, ControllerHolder: authorization.ControllerHolder,
			PlanSHA256: plan, ProposalSHA256: proposal, FilesystemPaths: slices.Clone(paths)}
		if !request.valid(time.Now()) {
			return fail()
		}
		// Use the wire deadline locally too: rounding cannot leave a response valid
		// after its authenticated request expired during body cleanup.
		requestCtx, stop := context.WithDeadline(ctx, time.Unix(request.ExpiresAt, 0))
		defer stop()
		endpoint, err := c.endpoint(requestCtx)
		if err != nil {
			return fail()
		}
		wire, err := sealClusterSSHVIP(c.requestCipher, clusterSSHPreparationCleanBrokerPath, request, clusterSSHPreparationCleanBrokerLimit)
		if err != nil {
			return fail()
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint+clusterSSHPreparationCleanBrokerPath, bytes.NewReader(wire))
		if err != nil {
			return fail()
		}
		req.GetBody = nil // An ambiguous POST is never replayed by the transport.
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		req.Header.Set("Accept", clusterSSHSourceBrokerMedia)
		response, err := client.Do(req)
		if err != nil {
			return fail()
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, clusterSSHPreparationCleanBrokerLimit+1))
		closeErr := response.Body.Close() // Join every owned body before SQL or proof.
		var result clusterSSHPreparationCleanBrokerResponse
		requestDigest, digestErr := clusterSSHPreparationDigest(request)
		// Proposal evidence carries conservative current minima; the waiting
		// storage gate independently recalculates every fit before approval.
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || response.Header.Get("Content-Encoding") != "" ||
			openClusterSSHVIP(c.responseCipher, clusterSSHPreparationCleanBrokerPath, raw, &result, clusterSSHPreparationCleanBrokerLimit) != nil ||
			result.Version != clusterSSHPreparationCleanBrokerVersion || result.ID != request.ID || digestErr != nil || result.RequestSHA256 != requestDigest || (clusterSSHPreparationCleanObservation{Digest: result.Digest, Targets: result.Targets}).validate(snapshot) != nil ||
			!validClusterSSHPreparationFilesystems(snapshot, paths, result.Filesystems) ||
			!cleanup.clean() || requestCtx.Err() != nil || check(requestCtx) != nil || requestCtx.Err() != nil ||
			(retained != "" && retained != result.Digest) || retainedFilesystems != nil && !mergeClusterSSHFilesystemAvailability(retainedFilesystems, result.Filesystems, false) {
			return fail()
		}
		retained = result.Digest
		if retainedFilesystems == nil {
			retainedFilesystems = cloneClusterSSHPreparationFilesystems(result.Filesystems)
		}
		return clusterSSHPreparationCleanObservation{Digest: result.Digest, Targets: result.Targets, Filesystems: cloneClusterSSHPreparationFilesystems(retainedFilesystems)}, nil
	}
}
