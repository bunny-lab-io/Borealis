package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Pinned K3s separates its HTTP gateway (2382) from gRPC (2379). The
// existing listener scope retains both plus 6443 on the same measured process.
// This fixed count-only Range performs no write and returns no stored values.
const bootstrapMembershipQuorumURL = "https://127.0.0.1:2382/v3/kv/range"
const bootstrapMembershipQuorumBody = `{"key":"Ym9yZWFsaXMvYWRtaXNzaW9uLXByb2Jl","count_only":true,"serializable":false}`

type bootstrapMembershipQuorumRead struct {
	identity       clusterbootstrap.MembershipQuorumRead
	revision, term uint64
}

func parseBootstrapMembershipQuorum(raw []byte, memberID uint64) (bootstrapMembershipQuorumRead, error) {
	fail := func() (bootstrapMembershipQuorumRead, error) {
		return bootstrapMembershipQuorumRead{}, errBootstrapSource
	}
	object, err := clusterbootstrap.ParseK3sObservationObject(raw)
	if err != nil || len(raw) > 4096 || memberID == 0 || len(object) < 1 || len(object) > 2 {
		return fail()
	}
	for key := range object {
		if key != "header" && key != "count" {
			return fail()
		}
	}
	header, err := clusterbootstrap.ParseK3sObservationObject(object["header"])
	if err != nil || len(header) != 4 {
		return fail()
	}
	number := func(raw json.RawMessage, positive bool) (uint64, bool) {
		var value string
		if len(raw) < 3 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || string(raw) != strconv.Quote(value) {
			return 0, false
		}
		n, err := strconv.ParseUint(value, 10, 64)
		return n, err == nil && strconv.FormatUint(n, 10) == value && (!positive || n != 0)
	}
	cluster, clusterOK := number(header["cluster_id"], true)
	member, memberOK := number(header["member_id"], true)
	revision, revisionOK := number(header["revision"], true)
	term, termOK := number(header["raft_term"], true)
	if !clusterOK || !memberOK || !revisionOK || !termOK || member != memberID || revision > 1<<63-1 {
		return fail()
	}
	// Proto JSON omits zero. One fixed key can match at most one record.
	if raw, present := object["count"]; present {
		if count, ok := number(raw, false); !ok || count > 1 {
			return fail()
		}
	}
	return bootstrapMembershipQuorumRead{clusterbootstrap.MembershipQuorumRead{ClusterID: cluster, MemberID: member}, revision, term}, nil
}

func readBootstrapMembershipQuorum(parent context.Context, files []bootstrapHostFile, memberID uint64,
	refresh, listener func(context.Context) error, clientFor func(*tls.Config) *http.Client) (bootstrapMembershipQuorumRead, error) {
	fail := func() (bootstrapMembershipQuorumRead, error) {
		return bootstrapMembershipQuorumRead{}, errBootstrapSource
	}
	if len(files) != 3 || refresh == nil || listener == nil || clientFor == nil || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if refresh(ctx) != nil || listener(ctx) != nil {
		return fail()
	}
	pair, err := tls.X509KeyPair(files[1].data, files[2].data)
	if err != nil || len(pair.Certificate) == 0 {
		return fail()
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.Subject.CommonName != "etcd-client" || len(leaf.Subject.Organization) != 0 {
		return fail()
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(files[0].data) {
		return fail()
	}
	config := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	defer func() { pair.PrivateKey = nil; config.Certificates = nil; config.RootCAs = nil }()
	client := clientFor(config)
	if client == nil {
		return fail()
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, bootstrapMembershipQuorumURL, strings.NewReader(bootstrapMembershipQuorumBody))
	if err != nil {
		return fail()
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	raw, err := sourceNetworkHTTPRequest(client, request, 4096)
	defer clear(raw)
	if err != nil || ctx.Err() != nil || listener(ctx) != nil || refresh(ctx) != nil {
		return fail()
	}
	return parseBootstrapMembershipQuorum(raw, memberID)
}
