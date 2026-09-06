package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

const clusterAcceptedJoinTTL = 24 * time.Hour

// Initial invitation consumption stays limited to fifteen minutes. An already
// approved target can resume for one day from invitation creation; this does not
// authorize a new target or revive a cancelled/expired pending admission.
func clusterAdmissionAuthorizationExpiry(state string, createdAt, expiresAt int64) int64 {
	if state == "Approved" || state == "Admitted" || state == "Recovery Required" {
		return createdAt + int64(clusterAcceptedJoinTTL/time.Second)
	}
	return expiresAt
}

func clusterAdmissionJoinConfig(ctx context.Context, tx *sql.Tx, admissionIDs []string) (map[string]any, error) {
	if len(admissionIDs) < 1 || len(admissionIDs) > 2 || (len(admissionIDs) == 2 && admissionIDs[0] == admissionIDs[1]) {
		return nil, fmt.Errorf("%w: admission retry requires original one- or two-node cohort", errClusterConflict)
	}
	for _, id := range admissionIDs {
		if len(validateClusterUUID("id", id)) != 0 {
			return nil, fmt.Errorf("%w: admission cohort has invalid identity", errClusterConflict)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_admissions a JOIN engine.cluster_state c ON c.id=1 AND c.enabled=1 AND c.cluster_id=a.cluster_id
		WHERE a.id=ANY($1) AND a.state IN ('Approved','Recovery Required')`, pq.Array(admissionIDs)).Scan(&count); err != nil {
		return nil, err
	}
	if count != len(admissionIDs) {
		return nil, fmt.Errorf("%w: original admission cohort is no longer reserved", errClusterConflict)
	}
	var vip, configJSON string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(control_plane_vip,''),config_json FROM engine.cluster_state WHERE id=1`).Scan(&vip, &configJSON); err != nil {
		return nil, err
	}
	version := cleanText(parseClusterJSON(configJSON)["k3s_version"])
	address, err := netip.ParseAddr(vip)
	if err != nil || !address.Is4() || !address.IsPrivate() || !clusterK3sRE.MatchString(version) {
		return nil, errors.New("cluster admission requires authoritative private Cluster VIP and stable K3s version")
	}
	rows, err := tx.QueryContext(ctx, `SELECT management_ip FROM engine.cluster_nodes WHERE membership_state='Active'
		UNION SELECT management_ip FROM engine.cluster_admissions WHERE id=ANY($1)`, pq.Array(admissionIDs))
	if err != nil {
		return nil, err
	}
	var peers []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			rows.Close()
			return nil, err
		}
		peer, err := netip.ParseAddr(ip)
		if err != nil || !peer.Is4() || !peer.IsPrivate() {
			rows.Close()
			return nil, errors.New("cluster admission requires private IPv4 identities for current and approved peers")
		}
		peers = append(peers, peer.String()+"/32")
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(peers) != 3 {
		return nil, errors.New("cluster admission requires complete three-node peer roster")
	}
	sort.Strings(peers)
	return map[string]any{"k3s_server": "https://" + address.String() + ":6443", "k3s_version": version, "peer_cidrs": strings.Join(peers, ",")}, nil
}
