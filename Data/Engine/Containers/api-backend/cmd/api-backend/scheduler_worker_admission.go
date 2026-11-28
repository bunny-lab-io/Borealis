package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const schedulerWorkerPlacementKey = "worker_placement"
const schedulerWorkerAdmissionKey = "worker_admission"
const schedulerWorkerOperationKey = "worker_operation"

var schedulerWorkerOperationToken = regexp.MustCompile(`^[0-9a-f]{32}$`)
var schedulerWorkerOperationDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var schedulerWorkerOperationKind = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Deletion is an operator action, never terminal execution evidence. Only an
// already committed, fully bound receipt tombstone permits tracked history to
// disappear. Caller must lock/recheck every target before deleting any rows.
func workerOperationActivityDeletionAllowed(activityID int64, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	metadata, err := schedulerAdmissionMetadata(raw)
	if err != nil {
		return false
	}
	value, tracked := metadata[schedulerWorkerOperationKey]
	if !tracked {
		return true
	}
	operation, err := schedulerAdmissionMetadata(string(value))
	if err != nil || (len(operation) != 11 && len(operation) != 12) || string(operation["version"]) != "1" {
		return false
	}
	text := func(key string) string {
		var value string
		_ = json.Unmarshal(operation[key], &value)
		return value
	}
	for _, key := range []string{"job_id", "site_id", "route_generation"} {
		value, err := strconv.ParseInt(string(operation[key]), 10, 64)
		if err != nil || value <= 0 || value > (1<<53)-1 || strconv.FormatInt(value, 10) != string(operation[key]) {
			return false
		}
		if key == "job_id" && value != activityID {
			return false
		}
	}
	var agent *string
	if json.Unmarshal(operation["agent_guid"], &agent) != nil || agent == nil || (*agent != "" && normalizeCanonicalGUID(*agent) != *agent) {
		return false
	}
	grant, granted := operation["grant_nonce"]
	if *agent == "" {
		if granted || len(operation) != 11 {
			return false
		}
	} else {
		var nonce string
		if !granted || len(operation) != 12 || json.Unmarshal(grant, &nonce) != nil || !schedulerWorkerOperationToken.MatchString(nonce) {
			return false
		}
	}
	return schedulerWorkerOperationToken.MatchString(text("token")) &&
		clusterUUIDRE.MatchString(text("worker_guid")) && clusterUUIDRE.MatchString(text("pod_uid")) &&
		schedulerPlacementName(text("node_name")) && schedulerWorkerOperationKind.MatchString(text("kind")) &&
		schedulerWorkerOperationDigest.MatchString(text("receipt_sha256")) && text("receipt_sha256") != strings.Repeat("0", 64)
}

// Ordinary activity writers may change progress/status, but only the terminal
// receipt transaction owns this private binding and its committed tombstone.
func schedulerActivityMetadata(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	fields, err := schedulerAdmissionMetadata(raw)
	if err != nil {
		return nil, err
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, err
	}
	if operation, exists := fields[schedulerWorkerOperationKey]; exists {
		metadata[schedulerWorkerOperationKey] = operation
	}
	return metadata, nil
}

func schedulerMergeActivityCustody(ctx context.Context, tx *sql.Tx, activityID int64, supplied map[string]any) ([]byte, error) {
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT metadata_json FROM engine.activity_history WHERE id=$1 FOR UPDATE`, activityID).Scan(&raw); err != nil {
		return nil, err
	}
	current, err := schedulerActivityMetadata(nullString(raw))
	if err != nil {
		return nil, err
	}
	metadata := copyMap(supplied)
	delete(metadata, schedulerWorkerOperationKey)
	if operation, exists := current[schedulerWorkerOperationKey]; exists {
		metadata[schedulerWorkerOperationKey] = operation
	}
	return json.Marshal(metadata)
}

// Placement comes only from the operator's native Pod observation and inherited
// scheduler configuration. An unassigned Pod or unknown profile grants no work
// admission. Pod UID is the incarnation; a reused name cannot inherit its charge.
func schedulerWorkerPlacement(snapshot map[string]any) map[string]any {
	text := func(key string) string { value, _ := snapshot[key].(string); return value }
	guid, uid, pod, node := text("worker_guid"), text("uid"), text("name"), text("node_name")
	rankText := os.Getenv("BOREALIS_DEPLOYMENT_PROFILE_RANK")
	rank, err := strconv.Atoi(rankText)
	site := schedulerPlacementSiteID(snapshot["site_id"])
	sizingRank, sizingPresent := os.LookupEnv("BOREALIS_CLUSTER_SIZING_RANK")
	if err != nil || rank < 0 || rank > 3 || strconv.Itoa(rank) != rankText ||
		(sizingPresent && sizingRank != rankText) ||
		guid != schedulerK3sSiteWorkerGUID(site) || !clusterUUIDRE.MatchString(uid) || site <= 0 ||
		!schedulerPlacementName(pod) || !schedulerPlacementName(node) {
		return nil
	}
	return map[string]any{"version": 1, "worker_guid": guid, "site_id": site,
		"pod_uid": uid, "pod_name": pod, "node_name": node, "profile_rank": rank}
}

func schedulerPlacementSiteID(raw any) int64 {
	switch value := raw.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		if value > 0 && value <= (1<<53)-1 && value == float64(int64(value)) {
			return int64(value)
		}
	}
	return 0
}

func schedulerPlacementName(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 || !clusterSSHStorageNameRE.MatchString(label) {
			return false
		}
	}
	return true
}

// Validate duplicates and encoding before extracting raw fields. In particular,
// a malformed reserved ledger must never become an apparently empty ledger.
func schedulerAdmissionMetadata(raw string) (map[string]json.RawMessage, error) {
	if !utf8.ValidString(raw) {
		return nil, errors.New("invalid worker route metadata")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	value, err := clusterSSHPrivateJSONValue(d, 0)
	object, ok := value.(map[string]any)
	if err != nil || !ok || object == nil {
		return nil, errors.New("invalid worker route metadata")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing worker route metadata")
	}
	var fields map[string]json.RawMessage
	err = json.Unmarshal([]byte(raw), &fields)
	return fields, err
}

func schedulerAdmissionEmpty(raw string) bool {
	fields, err := schedulerAdmissionMetadata(raw)
	if err != nil {
		return false
	}
	ledger, present := fields[schedulerWorkerAdmissionKey]
	if !present {
		return true
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(ledger, &state) != nil || len(state) != 2 || string(state["version"]) != "1" {
		return false
	}
	var operations map[string]json.RawMessage
	return json.Unmarshal(state["operations"], &operations) == nil && operations != nil && len(operations) == 0
}

// Route locks serialize with Python admission/completion (node advisory lock ->
// route -> worker). Go never changes the ledger. Incoming callers cannot create
// or clear it; its original node/incarnation survives replacement and retirement.
func schedulerMergeAdmissionMetadata(previous string, incoming map[string]any, generation int64, changed bool) (map[string]any, int64, error) {
	old, err := schedulerAdmissionMetadata(previous)
	if err != nil {
		return nil, 0, err
	}
	next := copyMap(incoming)
	delete(next, schedulerWorkerAdmissionKey)
	if ledger, exists := old[schedulerWorkerAdmissionKey]; exists {
		next[schedulerWorkerAdmissionKey] = ledger
	}
	placement, _ := next[schedulerWorkerPlacementKey].(map[string]any)
	if placement != nil {
		placement = copyMap(placement)
		placement["route_generation"] = generation
		next[schedulerWorkerPlacementKey] = placement
	} else {
		next[schedulerWorkerPlacementKey] = nil
	}
	var oldValue, nextValue any
	if err := json.Unmarshal([]byte(previous), &oldValue); err != nil {
		return nil, 0, err
	}
	if err := json.Unmarshal([]byte(mustJSON(next)), &nextValue); err != nil {
		return nil, 0, err
	}
	if changed || !reflect.DeepEqual(oldValue, nextValue) {
		if generation == int64(^uint64(0)>>1) {
			return nil, 0, errors.New("worker route generation exhausted")
		}
		generation++
		if placement != nil {
			placement["route_generation"] = generation
		}
	}
	return next, generation, nil
}

func (m *goSchedulerManager) pruneWorkerAdmissionHistory(ctx context.Context, retentionSeconds int) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	cutoff := time.Now().Unix() - int64(retentionSeconds)
	rows, err := tx.QueryContext(ctx, `SELECT worker_guid,metadata_json,route_file_path
		FROM engine.job_scheduler_worker_routes WHERE status IN ($1,$2)
		AND COALESCE(retired_at,updated_at,created_at)<$3 ORDER BY worker_guid FOR UPDATE`, schedulerRouteStatusRetired, schedulerRouteStatusLost, cutoff)
	if err != nil {
		return err
	}
	type candidate struct{ guid, metadata, file string }
	var candidates []candidate
	for rows.Next() {
		var row candidate
		if err := rows.Scan(&row.guid, &row.metadata, &row.file); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, row)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	var files []string
	for _, row := range candidates {
		if !schedulerAdmissionEmpty(row.metadata) {
			continue
		}
		var count int64
		err := tx.QueryRowContext(ctx, `SELECT claimed_count FROM engine.job_scheduler_workers WHERE worker_guid=$1 FOR UPDATE`, row.guid).Scan(&count)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if count != 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, row.guid); err != nil {
			return err
		}
		files = append(files, row.file)
	}
	// Admission requires a route and locks it before touching its worker. Workers
	// whose route remains (including malformed custody) cannot be TTL-pruned.
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.job_scheduler_workers w
		WHERE status IN ($1,$2) AND COALESCE(stopped_at,updated_at,last_seen_at)<$3
		AND claimed_count=0 AND NOT EXISTS (SELECT 1 FROM engine.job_scheduler_worker_routes r WHERE r.worker_guid=w.worker_guid)`, schedulerWorkerStatusStopped, schedulerWorkerStatusLost, cutoff); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}
	schedulerRemoveRouteFiles(files)
	return nil
}
