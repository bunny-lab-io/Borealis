package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClusterPreparationReleaseAuthenticatedReads(t *testing.T) {
	f := newBootstrapReleaseFixture(t)
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
	request, response, err := clusterPreparationReleaseCiphers()
	if err != nil {
		t.Fatal(err)
	}
	reader := &clusterPreparationReleaseReader{request: request, response: response, expected: f.expected, binding: clusterPreparationReleaseRequest{Operation: newClusterUUID(), Attempt: 1, Holder: "original", Confirmation: newClusterUUID()}}
	allow := true
	broker := &clusterPreparationReleaseBroker{request: request, response: response, slots: make(chan struct{}, 2), check: func(_ context.Context, q clusterPreparationReleaseRequest) bool {
		return allow && q.Expected == f.expected && q.Operation == reader.binding.Operation && q.Holder == "original"
	}, credentials: func(ctx context.Context) context.Context {
		return clusterContextWithGitHubToken(ctx, &fakeOperatorStore{githubTokenRecord: githubTokenRecord{Token: bootstrapTestToken}}, &authLoginTestAegis{})
	}}
	server := httptest.NewServer(http.HandlerFunc(broker.handle))
	defer server.Close()
	reader.endpoint = func(context.Context) (string, error) { return server.URL, nil }
	reader.client = server.Client()
	ctx := context.WithValue(context.Background(), clusterPreparationReleaseKey{}, reader)
	// The mock GitHub transport rejects anonymous REST. The credential-bearing
	// first hop and the credential-free CDN hop have separate request objects.
	original := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = original })
	transport := http.DefaultTransport
	cdn := "https://release-assets.githubusercontent.com/github-production-release-asset/unit/42?fixture=1"
	redirect := false
	http.DefaultClient = &http.Client{Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == cdn {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
				t.Error("credential on CDN hop")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("bundle")), ContentLength: 6}, nil
		}
		if r.Header.Get("Authorization") != "Bearer "+bootstrapTestToken {
			return &http.Response{StatusCode: 403, Header: make(http.Header), Body: http.NoBody}, nil
		}
		if redirect && strings.HasSuffix(r.URL.Path, "/releases/assets/2") {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{cdn}}, Body: http.NoBody}, nil
		}
		return transport.RoundTrip(r)
	})}
	if _, _, err := readClusterSSHBootstrapPublication(context.Background(), f.expected); err == nil {
		t.Fatal("anonymous control passed")
	}
	_, assets, err := readClusterSSHBootstrapPublication(ctx, f.expected)
	if err != nil {
		t.Fatal("broker publication", err)
	}
	read := func(asset clusterBootstrapAsset) string {
		t.Helper()
		res, err := openClusterBootstrapAsset(ctx, asset)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	asset := assets[clusterbootstrap.BundleName]
	if got := read(asset); got != "bundle" {
		t.Fatal("direct asset mismatch")
	}
	redirect = true
	if got := read(asset); got != "bundle" {
		t.Fatal("redirect asset mismatch")
	}
	changed := asset
	changed.ID++
	changed.URL = strings.TrimSuffix(changed.URL, "2") + "3"
	if _, err := openClusterBootstrapAsset(ctx, changed); err == nil {
		t.Fatal("unpublished asset ID accepted")
	}
	f.sha = strings.Repeat("b", 40)
	if _, err := resolveClusterBootstrapAssets(ctx, f.expected); err == nil {
		t.Fatal("explicit tag recheck reused cached release")
	}
	allow = false
	if _, err := openClusterBootstrapAsset(ctx, asset); err == nil {
		t.Fatal("cancelled owner opened cached asset")
	}
}

func TestClusterPreparationReleaseRejectsInvalidOrRepeatedRequests(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
	reqKey, replyKey, err := clusterPreparationReleaseCiphers()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	b := clusterPreparationReleaseBroker{request: reqKey, response: replyKey, slots: make(chan struct{}, 2),
		check: func(context.Context, clusterPreparationReleaseRequest) bool { checked++; return false },
		credentials: func(ctx context.Context) context.Context {
			t.Fatal("opened credential for unauthorized proposal")
			return ctx
		}}
	now := time.Now().Unix()
	q := clusterPreparationReleaseRequest{ID: newClusterUUID(), Operation: newClusterUUID(), Confirmation: newClusterUUID(), Attempt: 1, Holder: "original", Expires: now + 25,
		Expected: clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: "2026.11.1-rc.15", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}}
	send := func(q clusterPreparationReleaseRequest, damage bool) {
		raw, err := sealClusterSSHVIP(reqKey, clusterPreparationReleasePath, q, clusterPreparationReleaseRequestLimit)
		if err != nil {
			t.Fatal(err)
		}
		if damage {
			raw[len(raw)-1] ^= 1
		}
		r := httptest.NewRequest(http.MethodPost, clusterPreparationReleasePath, bytes.NewReader(raw))
		r.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		w := httptest.NewRecorder()
		b.handle(w, r)
		if w.Code == http.StatusOK {
			t.Fatal("request unexpectedly admitted")
		}
	}
	send(q, true)
	expired := q
	expired.Expires = now
	send(expired, false)
	other := q
	other.Expected.Repository = "unrelated/project"
	send(other, false)
	if checked != 0 {
		t.Fatal("invalid requests reached operation store")
	}
	send(q, false)
	send(q, false)
	if checked != 1 {
		t.Fatal("replayed request reached operation store")
	}
}

func TestClusterReleaseBodyAuthenticatesEveryRecord(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
	_, aead, err := clusterPreparationReleaseCiphers()
	if err != nil {
		t.Fatal(err)
	}
	id := newClusterUUID()
	data := bytes.Repeat([]byte("a"), clusterPreparationReleaseChunk+1)
	first := aead.Seal(nil, nil, data[:clusterPreparationReleaseChunk], clusterReleaseRecordAAD(id, 0))
	last := aead.Seal(nil, nil, data[clusterPreparationReleaseChunk:], clusterReleaseRecordAAD(id, clusterPreparationReleaseChunk))
	read := func(id string, tail []byte) ([]byte, error) {
		return io.ReadAll(&clusterReleaseBody{body: io.NopCloser(io.MultiReader(bytes.NewReader(first), bytes.NewReader(tail))), aead: aead, id: id, remaining: int64(len(data))})
	}
	if got, err := read(id, last); err != nil || !bytes.Equal(got, data) {
		t.Fatal("multi-record direct response failed", err)
	}
	last[len(last)-1] ^= 1
	for _, tc := range []struct {
		id   string
		tail []byte
	}{{id, last}, {id, nil}, {newClusterUUID(), last}} {
		if _, err := read(tc.id, tc.tail); err == nil {
			t.Fatal("altered, truncated or misbound transfer accepted")
		}
	}
}

func testPreparationReleasePhaseBinding(t *testing.T) {
	f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, false)
	q := clusterPreparationReleaseRequest{Operation: f.op.ID, Attempt: f.op.Attempt, Holder: f.c.holder, Expected: snapshot.work.Baseline, Confirmation: confirmation.RequestID}
	current := func(q clusterPreparationReleaseRequest) bool { return f.c.store.preparationReleaseCurrent(f.ctx, q) }
	if !current(q) {
		t.Fatal("confirmed waiting phase missing")
	}
	for _, edit := range []func(*clusterPreparationReleaseRequest){
		func(q *clusterPreparationReleaseRequest) { q.Holder = "another" }, func(q *clusterPreparationReleaseRequest) { q.Attempt++ }, func(q *clusterPreparationReleaseRequest) { q.Confirmation = newClusterUUID() }, func(q *clusterPreparationReleaseRequest) { q.Expected.SourceSHA = strings.Repeat("f", 40) },
	} {
		other := q
		edit(&other)
		if current(other) {
			t.Fatal("changed release binding accepted")
		}
	}
	f.exec(t, `UPDATE engine.cluster_operations SET error_text=$2 WHERE id=$1`, f.op.ID, clusterSSHPreparationCleanupPending)
	if err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof); err != nil {
		t.Fatal(err)
	}
	if !current(q) {
		t.Fatal("terminal verification lost release read")
	}
	f.exec(t, `UPDATE engine.cluster_operations SET error_text=NULL WHERE id=$1`, f.op.ID)
	if current(q) {
		t.Fatal("released preparation retained broker access")
	}
}

func testInspectionReleaseStaging(t *testing.T) {
	f := newSSHQualificationFixture(t, false)
	s := f.c.store
	work, err := s.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := clusterSSHPreparationDigest(work.Proof)
	if err != nil {
		t.Fatal(err)
	}
	q := clusterPreparationReleaseRequest{Operation: f.op.ID, Attempt: f.op.Attempt, Holder: f.c.holder, Expected: work.Baseline, Inspection: digest}
	if status, err := s.sourceStageStatus(f.ctx, q); err != nil || status != clusterSSHStagePending {
		t.Fatal("source stage missing", status, err)
	}
	if !s.preparationReleaseCurrent(f.ctx, q) {
		t.Fatal("inspected source download denied")
	}
	for _, alter := range []func(*clusterPreparationReleaseRequest){
		func(q *clusterPreparationReleaseRequest) { q.Inspection = strings.Repeat("a", 64) },
		func(q *clusterPreparationReleaseRequest) { q.Attempt++ },
		func(q *clusterPreparationReleaseRequest) { q.Operation = newClusterUUID() },
		func(q *clusterPreparationReleaseRequest) { q.Holder = "another" },
		func(q *clusterPreparationReleaseRequest) { q.Expected.SourceSHA = strings.Repeat("1", 40) },
		func(q *clusterPreparationReleaseRequest) { q.Confirmation = newClusterUUID() },
	} {
		other := q
		alter(&other)
		if s.preparationReleaseCurrent(f.ctx, other) {
			t.Fatal("changed binding retained source authority")
		}
	}
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.awaitInspectionRelease(ctx, work); err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatal("qualification escaped pending stage", err)
	}
	if err := f.c.setStageStatus(f.ctx, q, clusterSSHStagePending, clusterSSHStageReady); err != nil {
		t.Fatal(err)
	}
	if s.preparationReleaseCurrent(f.ctx, q) || s.awaitInspectionRelease(f.ctx, work) != nil || s.db.Stats().InUse != 0 {
		t.Fatal("staged handoff retained transfer authority or blocked native qualification")
	}
	if f.c.setStageStatus(f.ctx, q, clusterSSHStagePending, clusterSSHStageReady) == nil {
		t.Fatal("stale stage receipt replayed")
	}
	lease := work.Claims[0].Lease
	s.recordSSHCheckStarted(f.ctx, work.Proof.Cohort, work.Payload, "host_profile", lease)
	s.recordSSHCheckStarted(f.ctx, work.Proof.Cohort, work.Payload, "host_profile", lease)
	lease.Generation++
	s.recordSSHCheckStarted(f.ctx, work.Proof.Cohort, work.Payload, "replica_capacity", lease)
	var checks, staged int
	if err := s.db.QueryRowContext(f.ctx, `SELECT count(*) FILTER (WHERE event_type='ssh_check_started'),
 count(*) FILTER (WHERE event_type='ssh_source_stage_ready' AND message=$2) FROM engine.cluster_operation_events WHERE operation_id=$1`, f.op.ID, clusterSSHStageReady).Scan(&checks, &staged); err != nil || checks != 1 || staged != 1 {
		t.Fatalf("operation history checks=%d staged=%d err=%v", checks, staged, err)
	}
	if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err == nil {
		t.Fatal("source staging authorized target mutation")
	}
	bytes := &sshApprovalArtifactBytes{}
	dir := t.TempDir()
	f.c.sourceStage = &clusterSSHSourceStage{clusterSSHPreparationArtifactBytes: bytes, directory: dir, binding: q}
	if err := f.c.pruneSourceStage(f.ctx); err != nil || bytes.closes.Load() != 0 {
		t.Fatal("discarded valid staged files", err)
	}
	if _, err := s.cancelClusterOperation(f.ctx, "fixture", f.op.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.c.pruneSourceStage(f.ctx); err != nil || bytes.closes.Load() != 1 || f.c.sourceStage != nil {
		t.Fatal("cancelled files retained", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cancelled directory retained")
	}
}

func TestClusterPreparationReleaseStageTransferAndRestart(t *testing.T) {
	q := clusterPreparationReleaseRequest{Operation: newClusterUUID(), Attempt: 1, Holder: "original", Inspection: strings.Repeat("a", 64)}
	bytes := &sshApprovalArtifactBytes{}
	root := t.TempDir()
	t.Setenv("BOREALIS_CLUSTER_SCRATCH_DIR", root)
	dir := filepath.Join(root, clusterSSHStageDirectory)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := clusterController{sourceStage: &clusterSSHSourceStage{clusterSSHPreparationArtifactBytes: bytes, directory: dir, binding: q, postgres: "original-image"}}
	other := q
	other.Attempt++
	if _, err := c.takeSourceStage(other, "original-image"); err == nil {
		t.Fatal("wrong attempt received files")
	}
	if _, err := c.takeSourceStage(q, "changed-image"); err == nil {
		t.Fatal("changed source image received files")
	}
	owned, err := c.takeSourceStage(q, "original-image")
	if err != nil || c.sourceStage != nil {
		t.Fatal("original transfer", err)
	}
	if _, err := c.takeSourceStage(q, "original-image"); err == nil {
		t.Fatal("replayed staged files")
	}
	if err := owned.Close(); err != nil || bytes.closes.Load() != 1 {
		t.Fatal("files remained open", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("directory retained")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(root, "unrelated")
	if err := os.Mkdir(otherDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := initializeSSHSourceScratch(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("abandoned staging retained")
	}
	if _, err := os.Stat(otherDir); err != nil {
		t.Fatal("unrelated directory lost", err)
	}
}
