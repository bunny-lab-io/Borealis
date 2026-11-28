package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The controller owns verification. Only the API process opens the configured
// GitHub credential. Neither a URL nor an outbound header is a request input.
const (
	clusterPreparationReleasePath         = "/internal/cluster/preparation-release"
	clusterPreparationReleaseScope        = "preparation-release"
	clusterPreparationReleaseRequestLimit = 8 << 10
	clusterPreparationReleaseReplyLimit   = 256 << 10
	clusterPreparationReleaseChunk        = 64 << 10
)

type clusterPreparationReleaseKey struct{}
type clusterPreparationReleaseRequest struct {
	ID, Operation, Holder, Confirmation string
	Inspection                          string `json:"Inspection,omitempty"`
	Attempt, Expires                    int64
	Expected                            clusterbootstrap.Expected
	AssetName                           string
	AssetID                             int64
}
type clusterPreparationReleaseReply struct {
	ID, SHA, Redirect string
	Catalog           []byte `json:"catalog,omitempty"`
	Release           *clusterBootstrapRelease
	Direct            bool
	assetResponse     *http.Response
}
type clusterPreparationReleaseReader struct {
	request, response cipher.AEAD
	endpoint          func(context.Context) (string, error)
	client            *http.Client
	expected          clusterbootstrap.Expected
	binding           clusterPreparationReleaseRequest
}

func clusterPreparationReleaseCiphers() (cipher.AEAD, cipher.AEAD, error) {
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	request, err := clusterExistingIdentityScopedCipher(secret, "request", clusterPreparationReleaseScope)
	if err != nil {
		return nil, nil, err
	}
	response, err := clusterExistingIdentityScopedCipher(secret, "response", clusterPreparationReleaseScope)
	return request, response, err
}

func clusterPreparationReleaseContext(ctx context.Context, client *clusterSSHPreparationCleanBrokerClient, snapshot clusterSSHPreparationApprovalSnapshot) (context.Context, error) {
	var intent struct {
		Request clusterSSHPreparationConfirmation `json:"ssh_preparation_request"`
	}
	if json.Unmarshal([]byte(snapshot.work.Payload), &intent) != nil || !vipScopeUUID(intent.Request.RequestID) {
		return nil, errClusterConflict
	}
	return clusterBoundReleaseContext(ctx, client, snapshot.work.Baseline, clusterPreparationReleaseRequest{Operation: snapshot.work.Authorization.OperationID, Attempt: snapshot.work.Authorization.Attempt, Holder: snapshot.work.Authorization.ControllerHolder, Confirmation: intent.Request.RequestID})
}

func clusterBoundReleaseContext(ctx context.Context, client *clusterSSHPreparationCleanBrokerClient, expected clusterbootstrap.Expected, binding clusterPreparationReleaseRequest) (context.Context, error) {
	request, response, err := clusterPreparationReleaseCiphers()
	if err != nil {
		return nil, err
	}
	httpClient := *client.httpClient
	httpClient.Timeout = 0 // Caller owns transfer cancellation; header and admission deadlines stay bounded.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	reader := &clusterPreparationReleaseReader{request: request, response: response, endpoint: client.endpoint, client: &httpClient, expected: expected, binding: binding}
	return context.WithValue(ctx, clusterPreparationReleaseKey{}, reader), nil
}

func (r *clusterPreparationReleaseReader) read(ctx context.Context, expected clusterbootstrap.Expected, asset *clusterBootstrapAsset) (clusterPreparationReleaseReply, error) {
	fail := func() (clusterPreparationReleaseReply, error) {
		return clusterPreparationReleaseReply{}, errClusterUnavailable
	}
	if expected != r.expected || ctx.Err() != nil {
		return fail()
	}
	q := r.binding
	q.ID, q.Expires, q.Expected = newClusterUUID(), time.Now().Add(25*time.Second).Unix(), expected
	if asset != nil {
		q.AssetName, q.AssetID = asset.Name, asset.ID
	}
	wire, err := sealClusterSSHVIP(r.request, clusterPreparationReleasePath, q, clusterPreparationReleaseRequestLimit)
	if err != nil {
		return fail()
	}
	endpoint, err := r.endpoint(ctx)
	if err != nil {
		return fail()
	}
	transfer, stop := context.WithTimeout(ctx, 5*time.Minute)
	direct := false
	defer func() {
		if !direct {
			stop()
		}
	}()
	admission := time.AfterFunc(time.Until(time.Unix(q.Expires, 0)), stop)
	defer admission.Stop()
	req, err := http.NewRequestWithContext(transfer, http.MethodPost, endpoint+clusterPreparationReleasePath, bytes.NewReader(wire))
	if err != nil {
		return fail()
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
	res, err := r.client.Do(req)
	if err != nil {
		return fail()
	}
	defer func() {
		if !direct {
			res.Body.Close()
		}
	}()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || res.Header.Get("Content-Encoding") != "" {
		return fail()
	}
	var size uint32
	if binary.Read(res.Body, binary.BigEndian, &size) != nil || size == 0 || size > clusterPreparationReleaseReplyLimit {
		return fail()
	}
	head := make([]byte, size)
	var reply clusterPreparationReleaseReply
	if _, err = io.ReadFull(res.Body, head); err != nil || openClusterSSHVIP(r.response, clusterPreparationReleasePath, head, &reply, clusterPreparationReleaseReplyLimit) != nil || reply.ID != q.ID || time.Now().Unix() >= q.Expires {
		return fail()
	}
	if !admission.Stop() || transfer.Err() != nil {
		return fail()
	}
	if asset == nil {
		development := expected.Development != ""
		if development != (len(reply.Catalog) > 0) || development == (reply.Release != nil) || reply.Redirect != "" || reply.Direct || reply.SHA != expected.SourceSHA {
			return fail()
		}
	} else {
		if len(reply.Catalog) > 0 || (expected.Development != "" && !reply.Direct) || reply.Release != nil || reply.SHA != "" || reply.Direct == (reply.Redirect != "") {
			return fail()
		}
		if reply.Direct {
			direct = true
			reply.assetResponse = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: asset.Size, Body: &clusterReleaseBody{body: res.Body, aead: r.response, id: q.ID, remaining: asset.Size, cancel: stop}}
		} else {
			if !validClusterAssetRedirect(reply.Redirect) {
				return fail()
			}
			header := make(http.Header)
			header.Set("Location", reply.Redirect)
			reply.assetResponse = &http.Response{StatusCode: http.StatusFound, Header: header, Body: http.NoBody}
		}
	}
	return reply, nil
}

// GitHub normally returns a signed CDN redirect. For direct binary responses,
// each bounded record is authenticated for this request and byte offset.
// The caller still verifies the complete published size and digest.
type clusterReleaseBody struct {
	body              io.ReadCloser
	aead              cipher.AEAD
	id                string
	remaining, offset int64
	buffer            []byte
	cancel            context.CancelFunc
}

func (b *clusterReleaseBody) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	return b.body.Close()
}
func (b *clusterReleaseBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(b.buffer) == 0 {
		if b.remaining == 0 {
			return 0, io.EOF
		}
		n := min(b.remaining, int64(clusterPreparationReleaseChunk))
		record := make([]byte, int(n)+b.aead.Overhead())
		if _, err := io.ReadFull(b.body, record); err != nil {
			return 0, io.ErrUnexpectedEOF
		}
		plain, err := b.aead.Open(record[:0], nil, record, clusterReleaseRecordAAD(b.id, b.offset))
		if err != nil {
			return 0, errClusterUnavailable
		}
		b.buffer = plain
		b.remaining -= n
		b.offset += n
	}
	n := copy(p, b.buffer)
	b.buffer = b.buffer[n:]
	return n, nil
}

func clusterReleaseRecordAAD(id string, offset int64) []byte {
	return []byte(fmt.Sprintf("%s/body/%s/%d", clusterPreparationReleasePath, id, offset))
}

type clusterPreparationReleaseBroker struct {
	request, response cipher.AEAD
	check             func(context.Context, clusterPreparationReleaseRequest) bool
	credentials       func(context.Context) context.Context
	objects           string
	slots             chan struct{}
	mu                sync.Mutex
	lastExpected      clusterbootstrap.Expected
	lastRelease       clusterBootstrapRelease
	readAt            time.Time
	seen              map[string]int64
}

// Asset transfers reuse the API's own recently verified asset IDs. Every
// explicit publication check still fetches the release and tag afresh.
func (b *clusterPreparationReleaseBroker) publication(ctx context.Context, expected clusterbootstrap.Expected, forAsset bool) (clusterBootstrapRelease, error) {
	if forAsset {
		b.mu.Lock()
		cached, valid := b.lastRelease, b.lastExpected == expected && time.Since(b.readAt) < time.Minute
		b.mu.Unlock()
		if valid {
			return cached, nil
		}
	}
	release, sha, err := fetchClusterPackagedRelease(ctx, expected)
	if err != nil || validateClusterPackagedRelease(expected, release, sha) != nil {
		return clusterBootstrapRelease{}, errClusterUnavailable
	}
	b.mu.Lock()
	b.lastExpected, b.lastRelease, b.readAt = expected, release, time.Now()
	b.mu.Unlock()
	return release, nil
}

func registerClusterPreparationReleaseRoute(mux *http.ServeMux, auth *authService) {
	b := &clusterPreparationReleaseBroker{slots: make(chan struct{}, 2), objects: os.Getenv("BOREALIS_ENGINE_VERIFIED_OBJECTS")}
	b.request, b.response, _ = clusterPreparationReleaseCiphers()
	if auth != nil {
		if store, ok := auth.store.(*postgresOperatorStore); ok && store != nil {
			b.check = store.preparationReleaseCurrent
			b.credentials = func(ctx context.Context) context.Context {
				return clusterContextWithGitHubToken(ctx, store, auth.aegis)
			}
		}
	}
	mux.HandleFunc("POST "+clusterPreparationReleasePath, b.handle)
}

// The terminal artifact proof runs just after the atomic approval commit.
// Inspection-only staging uses a separate binding that expires when the
// controller has the release. No SQL connection spans GitHub IO.
func (s *postgresOperatorStore) preparationReleaseCurrent(ctx context.Context, q clusterPreparationReleaseRequest) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if !q.validReleaseAuthority() {
		return false
	}
	if q.Inspection != "" {
		status, err := s.sourceStageStatus(ctx, q)
		return err == nil && status == clusterSSHStagePending
	}
	var present int
	return s != nil && s.db != nil && s.db.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations o,engine.cluster_state s,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.attempt=$2 AND o.kind='ssh_onboarding' AND s.id=1 AND s.active_operation_id=o.id
 AND s.baseline_release=$3 AND s.baseline_sha=$4 AND l.name=$5 AND l.holder=$6 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND o.payload_json::jsonb #>> '{ssh_inspection,proof,cohort,ControllerHolder}'=$6
 AND o.payload_json::jsonb #>> '{ssh_preparation_request,request_id}'=$7
 AND (o.payload_json::jsonb->'ssh_preparation_request'->>'confirmed_at')::bigint BETWEEN floor(extract(epoch FROM clock_timestamp()))::bigint-$11+1 AND floor(extract(epoch FROM clock_timestamp()))::bigint
 AND (o.payload_json::jsonb->'ssh_qualification'->>'observed_at')::bigint BETWEEN floor(extract(epoch FROM clock_timestamp()))::bigint-$11+1 AND floor(extract(epoch FROM clock_timestamp()))::bigint
 AND ((o.state='waiting' AND o.current_step=$8) OR (o.state='running' AND o.current_step=$9 AND o.error_text=$10))`,
		q.Operation, q.Attempt, q.Expected.Release, q.Expected.SourceSHA, clusterControllerLeaseName, q.Holder, q.Confirmation, clusterSSHQualificationStep, clusterSSHPreparationOperationStep, clusterSSHPreparationCleanupPending, int64(clusterSSHFreshEvidenceSeconds)).Scan(&present) == nil && present == 1
}
func (q clusterPreparationReleaseRequest) validReleaseAuthority() bool {
	return (vipScopeUUID(q.Confirmation) && q.Inspection == "") ||
		(q.Confirmation == "" && validClusterSSHPreparationDigest(q.Inspection))
}

func (b *clusterPreparationReleaseBroker) acceptRequest(id string, expires, now int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen == nil {
		b.seen = make(map[string]int64)
	}
	for id, until := range b.seen {
		if until <= now {
			delete(b.seen, id)
		}
	}
	if _, exists := b.seen[id]; exists || len(b.seen) >= 256 {
		return false
	}
	b.seen[id] = expires
	return true
}

func (b *clusterPreparationReleaseBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func() { http.Error(w, "Preparation release read unavailable", http.StatusServiceUnavailable) }
	if r.URL.Path != clusterPreparationReleasePath || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Method != http.MethodPost || r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" || b.check == nil {
		fail()
		return
	}
	control := http.NewResponseController(w)
	_ = control.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, err := io.ReadAll(io.LimitReader(r.Body, clusterPreparationReleaseRequestLimit+1))
	closeErr := r.Body.Close()
	var q clusterPreparationReleaseRequest
	now := time.Now().Unix()
	if err != nil || closeErr != nil || openClusterSSHVIP(b.request, clusterPreparationReleasePath, raw, &q, clusterPreparationReleaseRequestLimit) != nil || !vipScopeUUID(q.ID) || !vipScopeUUID(q.Operation) || !q.validReleaseAuthority() || q.Attempt < 1 || q.Holder == "" || len(q.Holder) > 1024 || q.Expires <= now || q.Expires > now+30 || q.Expected.Validate() != nil || q.Expected.Repository != clusterGitHubRepo() {
		fail()
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		fail()
		return
	}
	ctx, stop := context.WithDeadline(r.Context(), time.Unix(q.Expires, 0))
	defer stop()
	if !b.acceptRequest(q.ID, q.Expires, now) || !b.check(ctx, q) {
		fail()
		return
	}
	// This is an existing bounded release download, including when GitHub elects
	// to stream a direct 200 instead of redirecting. Admission is checked under
	// the shorter request deadline before returning any response.
	transfer, done := context.WithTimeout(r.Context(), 5*time.Minute)
	defer done()
	reply := clusterPreparationReleaseReply{ID: q.ID}
	var direct io.ReadCloser
	var asset clusterBootstrapAsset
	if (q.AssetID == 0) != (q.AssetName == "") || q.AssetID < 0 || len(q.AssetName) > 256 {
		fail()
		return
	}
	if q.Expected.Development != "" {
		catalog, raw, err := clusterLocalDevelopmentCatalog(q.Expected)
		if err != nil {
			fail()
			return
		}
		if q.AssetID == 0 {
			reply.Catalog, reply.SHA = raw, q.Expected.SourceSHA
		} else {
			asset, err = clusterDevelopmentAsset(catalog, q.AssetName, clusterbootstrap.MaxImageArchiveBytes)
			if err != nil || asset.ID != q.AssetID {
				fail()
				return
			}
			res, err := openClusterDevelopmentAsset(catalog, asset)
			if err != nil {
				fail()
				return
			}
			defer res.Body.Close()
			reply.Direct, direct = true, res.Body
		}
	} else {
		if b.credentials == nil {
			fail()
			return
		}
		authenticated := b.credentials(transfer)
		if token, ok := authenticated.Value(clusterGitHubTokenContextKey{}).(string); ok {
			ctx = context.WithValue(ctx, clusterGitHubTokenContextKey{}, token)
		}
		release, err := b.publication(ctx, q.Expected, q.AssetID != 0)
		if err != nil {
			fail()
			return
		}
		if q.AssetID == 0 {
			reply.Release, reply.SHA = &release, q.Expected.SourceSHA
		} else {
			assets, err := selectClusterPackagedAssets(q.Expected, release, map[string]int64{q.AssetName: clusterbootstrap.MaxImageArchiveBytes})
			asset = assets[q.AssetName]
			if err != nil || asset.ID != q.AssetID {
				fail()
				return
			}
			cancelAdmission := time.AfterFunc(time.Until(time.Unix(q.Expires, 0)), done)
			res, err := b.openPublishedAsset(authenticated, asset)
			cancelAdmission.Stop()
			if err != nil {
				fail()
				return
			}
			defer res.Body.Close()
			switch res.StatusCode {
			case http.StatusFound:
				reply.Redirect = res.Header.Get("Location")
				if !validClusterAssetRedirect(reply.Redirect) {
					fail()
					return
				}
			case http.StatusOK:
				if res.Header.Get("Content-Encoding") != "" || (res.ContentLength >= 0 && res.ContentLength != asset.Size) {
					fail()
					return
				}
				reply.Direct, direct = true, res.Body
			default:
				fail()
				return
			}
		}
	}
	if !b.check(ctx, q) || ctx.Err() != nil {
		fail()
		return
	}
	sealed, err := sealClusterSSHVIP(b.response, clusterPreparationReleasePath, reply, clusterPreparationReleaseReplyLimit)
	if err != nil {
		fail()
		return
	}
	w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
	if direct != nil {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	}
	if binary.Write(w, binary.BigEndian, uint32(len(sealed))) != nil {
		return
	}
	if _, err = w.Write(sealed); err != nil || direct == nil {
		return
	}
	_ = control.Flush()
	b.writeAsset(transfer, w, q, asset, direct)
}

// The broker has already matched the operation, publication, tag and asset ID.
// Only the digest selects a file. A hit still travels through the same
// encrypted, authority-checked transfer as a direct GitHub response.
func (b *clusterPreparationReleaseBroker) openPublishedAsset(ctx context.Context, asset clusterBootstrapAsset) (*http.Response, error) {
	if b.objects != "" {
		if !clusterBootstrapDigestPattern.MatchString(asset.Digest) {
			return nil, errClusterUnavailable
		}
		root, err := os.OpenRoot(b.objects)
		if err != nil {
			return nil, errClusterUnavailable
		}
		defer root.Close()
		name := strings.TrimPrefix(asset.Digest, "sha256:")
		info, err := root.Lstat(name)
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() != asset.Size {
				return nil, errClusterUnavailable
			}
			file, err := root.Open(name)
			if err != nil {
				return nil, errClusterUnavailable
			}
			opened, err := file.Stat()
			if err != nil || !os.SameFile(opened, info) {
				file.Close()
				return nil, errClusterUnavailable
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: asset.Size, Body: file}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, errClusterUnavailable
		}
	}
	return openClusterAssetFirstHop(ctx, clusterPackagedAssetAPIURL(asset.ID))
}

func (b *clusterPreparationReleaseBroker) writeAsset(transfer context.Context, w io.Writer, q clusterPreparationReleaseRequest, asset clusterBootstrapAsset, direct io.Reader) {
	size := asset.Size
	digest := sha256.New()
	buf := make([]byte, clusterPreparationReleaseChunk)
	nextCheck := time.Now().Add(5 * time.Second)
	for offset := int64(0); offset < size; {
		if !time.Now().Before(nextCheck) {
			if !b.check(transfer, q) {
				return
			}
			nextCheck = time.Now().Add(5 * time.Second)
		}
		n := min(int64(len(buf)), size-offset)
		if _, err := io.ReadFull(direct, buf[:n]); err != nil {
			return
		}
		digest.Write(buf[:n])
		if offset+n == size {
			var extra [1]byte
			if count, err := io.ReadFull(direct, extra[:]); count != 0 || err != io.EOF || "sha256:"+hex.EncodeToString(digest.Sum(nil)) != asset.Digest || !b.check(transfer, q) {
				return
			}
		}
		block := b.response.Seal(nil, nil, buf[:n], clusterReleaseRecordAAD(q.ID, offset))
		if _, err := w.Write(block); err != nil {
			return
		}
		offset += n
	}
}
