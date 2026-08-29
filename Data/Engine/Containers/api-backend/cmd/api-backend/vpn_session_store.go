package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/lib/pq"
)

const vpnSessionStoreRetryLimit = 8

var errVPNSessionGenerationConflict = errors.New("vpn_session_generation_conflict")

const ensureVPNSessionStoreSQL = `
CREATE TABLE IF NOT EXISTS engine.device_vpn_sessions (
    agent_id TEXT PRIMARY KEY,
    tunnel_id TEXT NOT NULL UNIQUE,
    virtual_ip TEXT NOT NULL,
    endpoint_host TEXT NOT NULL DEFAULT '',
    allowed_ports_json TEXT NOT NULL DEFAULT '[]',
    operators_json TEXT NOT NULL DEFAULT '[]',
    state TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_activity_at TEXT NOT NULL,
    last_transport_probe_at TEXT,
    last_transport_confirmed_at TEXT,
    last_agent_ready_at TEXT,
    last_agent_ready_tunnel_id TEXT NOT NULL DEFAULT '',
    last_agent_ready_allowed_ports_json TEXT NOT NULL DEFAULT '[]',
    last_agent_ready_reason TEXT NOT NULL DEFAULT '',
    last_agent_ready_service_state TEXT NOT NULL DEFAULT '',
    generation BIGINT NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL
)`

const vpnSessionSelectColumns = `
s.agent_id,s.tunnel_id,s.virtual_ip,s.endpoint_host,s.allowed_ports_json,s.operators_json,
s.state,s.created_at,s.expires_at,s.last_activity_at,s.last_transport_probe_at,
s.last_transport_confirmed_at,s.last_agent_ready_at,s.last_agent_ready_tunnel_id,
s.last_agent_ready_allowed_ports_json,s.last_agent_ready_reason,
s.last_agent_ready_service_state,s.generation,s.updated_at,
k.client_private_key,k.client_public_key`

type vpnSessionRow struct {
	agentID                        string
	tunnelID                       string
	virtualIP                      string
	endpointHost                   string
	allowedPortsJSON               string
	operatorsJSON                  string
	state                          string
	createdAt                      string
	expiresAt                      string
	lastActivityAt                 string
	lastTransportProbeAt           sql.NullString
	lastTransportConfirmedAt       sql.NullString
	lastAgentReadyAt               sql.NullString
	lastAgentReadyTunnelID         string
	lastAgentReadyAllowedPortsJSON string
	lastAgentReadyReason           string
	lastAgentReadyServiceState     string
	generation                     int64
	updatedAt                      string
	clientPrivateKey               string
	clientPublicKey                string
}

func (s *vpnTunnelService) ensurePersistentSessionStore(ctx context.Context) error {
	s.mu.Lock()
	ready := s.sessionStoreReady
	s.mu.Unlock()
	if ready {
		return nil
	}
	db := vpnDB(s.auth)
	if db == nil {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, ensureVPNSessionStoreSQL); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_device_vpn_sessions_state_expires ON engine.device_vpn_sessions(state, expires_at)`)
	if err == nil {
		s.mu.Lock()
		s.sessionStoreReady = true
		s.mu.Unlock()
	}
	return err
}

func scanVPNSessionRow(scanner interface{ Scan(...any) error }) (vpnSessionRow, error) {
	var row vpnSessionRow
	err := scanner.Scan(
		&row.agentID, &row.tunnelID, &row.virtualIP, &row.endpointHost,
		&row.allowedPortsJSON, &row.operatorsJSON, &row.state, &row.createdAt,
		&row.expiresAt, &row.lastActivityAt, &row.lastTransportProbeAt,
		&row.lastTransportConfirmedAt, &row.lastAgentReadyAt,
		&row.lastAgentReadyTunnelID, &row.lastAgentReadyAllowedPortsJSON,
		&row.lastAgentReadyReason, &row.lastAgentReadyServiceState, &row.generation,
		&row.updatedAt, &row.clientPrivateKey, &row.clientPublicKey,
	)
	return row, err
}

func decodeVPNSessionRow(row vpnSessionRow) (*vpnSession, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.createdAt)
	if err != nil {
		return nil, fmt.Errorf("invalid vpn session created_at: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, row.expiresAt)
	if err != nil {
		return nil, fmt.Errorf("invalid vpn session expires_at: %w", err)
	}
	lastActivity, err := time.Parse(time.RFC3339Nano, row.lastActivityAt)
	if err != nil {
		return nil, fmt.Errorf("invalid vpn session last_activity_at: %w", err)
	}
	allowedPorts := []int{}
	if err := json.Unmarshal([]byte(row.allowedPortsJSON), &allowedPorts); err != nil {
		return nil, fmt.Errorf("invalid vpn session allowed ports: %w", err)
	}
	readyPorts := []int{}
	if err := json.Unmarshal([]byte(row.lastAgentReadyAllowedPortsJSON), &readyPorts); err != nil {
		return nil, fmt.Errorf("invalid vpn session ready ports: %w", err)
	}
	operatorIDs := []string{}
	if err := json.Unmarshal([]byte(row.operatorsJSON), &operatorIDs); err != nil {
		return nil, fmt.Errorf("invalid vpn session operators: %w", err)
	}
	operators := map[string]struct{}{}
	for _, operatorID := range operatorIDs {
		if operatorID = cleanText(operatorID); operatorID != "" {
			operators[operatorID] = struct{}{}
		}
	}
	return &vpnSession{
		TunnelID:                   cleanText(row.tunnelID),
		AgentID:                    cleanText(row.agentID),
		VirtualIP:                  cleanText(row.virtualIP),
		ClientPrivateKey:           cleanText(row.clientPrivateKey),
		ClientPublicKey:            cleanText(row.clientPublicKey),
		AllowedPorts:               uniquePorts(allowedPorts),
		CreatedAt:                  createdAt.UTC(),
		ExpiresAt:                  expiresAt.UTC(),
		LastActivity:               lastActivity.UTC(),
		LastTransportProbeAt:       parseNullableVPNTime(row.lastTransportProbeAt),
		LastTransportConfirmedAt:   parseNullableVPNTime(row.lastTransportConfirmedAt),
		LastAgentReadyAt:           parseNullableVPNTime(row.lastAgentReadyAt),
		LastAgentReadyTunnelID:     cleanText(row.lastAgentReadyTunnelID),
		LastAgentReadyAllowedPorts: uniquePorts(readyPorts),
		LastAgentReadyReason:       cleanText(row.lastAgentReadyReason),
		LastAgentReadyServiceState: cleanText(row.lastAgentReadyServiceState),
		Operators:                  operators,
		EndpointHost:               cleanText(row.endpointHost),
		State:                      firstText(cleanText(row.state), "active"),
		Generation:                 row.generation,
	}, nil
}

func parseNullableVPNTime(value sql.NullString) time.Time {
	if !value.Valid || cleanText(value.String) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func (s *vpnTunnelService) loadPersistentSession(ctx context.Context, agentID string) (*vpnSession, error) {
	db := vpnDB(s.auth)
	if db == nil {
		return nil, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	row, scanErr := scanVPNSessionRow(conn.QueryRowContext(ctx, `SELECT `+vpnSessionSelectColumns+`
		FROM engine.device_vpn_sessions s
		JOIN engine.device_vpn_key_leases k ON k.agent_id=s.agent_id
		WHERE s.agent_id=$1`, cleanText(agentID)))
	conn.Close()
	if errors.Is(scanErr, sql.ErrNoRows) {
		return nil, nil
	}
	if scanErr != nil {
		return nil, scanErr
	}
	return decodeVPNSessionRow(row)
}

func (s *vpnTunnelService) loadPersistentSessions(ctx context.Context) ([]*vpnSession, error) {
	db := vpnDB(s.auth)
	if db == nil {
		return nil, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT `+vpnSessionSelectColumns+`
		FROM engine.device_vpn_sessions s
		JOIN engine.device_vpn_key_leases k ON k.agent_id=s.agent_id
		WHERE s.state='active'
		ORDER BY s.agent_id`)
	if err != nil {
		conn.Close()
		return nil, err
	}
	records := []vpnSessionRow{}
	for rows.Next() {
		record, scanErr := scanVPNSessionRow(rows)
		if scanErr != nil {
			rows.Close()
			conn.Close()
			return nil, scanErr
		}
		records = append(records, record)
	}
	rowsErr := rows.Err()
	rows.Close()
	conn.Close()
	if rowsErr != nil {
		return nil, rowsErr
	}
	sessions := make([]*vpnSession, 0, len(records))
	for _, record := range records {
		session, decodeErr := decodeVPNSessionRow(record)
		if decodeErr != nil {
			return nil, decodeErr
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func encodeVPNSessionLists(session *vpnSession) ([]byte, []byte, []byte, error) {
	allowedValues := uniquePorts(session.AllowedPorts)
	readyValues := uniquePorts(session.LastAgentReadyAllowedPorts)
	sort.Ints(allowedValues)
	sort.Ints(readyValues)
	allowedPorts, err := json.Marshal(allowedValues)
	if err != nil {
		return nil, nil, nil, err
	}
	readyPorts, err := json.Marshal(readyValues)
	if err != nil {
		return nil, nil, nil, err
	}
	operatorIDs := make([]string, 0, len(session.Operators))
	for operatorID := range session.Operators {
		if operatorID = cleanText(operatorID); operatorID != "" {
			operatorIDs = append(operatorIDs, operatorID)
		}
	}
	sort.Strings(operatorIDs)
	operators, err := json.Marshal(operatorIDs)
	return allowedPorts, operators, readyPorts, err
}

func nullableVPNTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *vpnTunnelService) savePersistentSession(ctx context.Context, session *vpnSession, expectedGeneration int64) error {
	allowedPorts, operators, readyPorts, err := encodeVPNSessionLists(session)
	if err != nil {
		return err
	}
	db := vpnDB(s.auth)
	if db == nil {
		return nil
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	args := []any{
		session.AgentID, session.TunnelID, session.VirtualIP, session.EndpointHost,
		string(allowedPorts), string(operators), firstText(cleanText(session.State), "active"),
		session.CreatedAt.UTC().Format(time.RFC3339Nano), session.ExpiresAt.UTC().Format(time.RFC3339Nano),
		session.LastActivity.UTC().Format(time.RFC3339Nano), nullableVPNTime(session.LastTransportProbeAt),
		nullableVPNTime(session.LastTransportConfirmedAt), nullableVPNTime(session.LastAgentReadyAt),
		session.LastAgentReadyTunnelID, string(readyPorts), session.LastAgentReadyReason,
		session.LastAgentReadyServiceState, updatedAt,
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var result sql.Result
	if expectedGeneration == 0 {
		result, err = conn.ExecContext(ctx, `INSERT INTO engine.device_vpn_sessions(
			agent_id,tunnel_id,virtual_ip,endpoint_host,allowed_ports_json,operators_json,state,
			created_at,expires_at,last_activity_at,last_transport_probe_at,last_transport_confirmed_at,
			last_agent_ready_at,last_agent_ready_tunnel_id,last_agent_ready_allowed_ports_json,
			last_agent_ready_reason,last_agent_ready_service_state,generation,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,1,$18)
			ON CONFLICT(agent_id) DO NOTHING`, args...)
	} else {
		args = append(args, expectedGeneration)
		result, err = conn.ExecContext(ctx, `UPDATE engine.device_vpn_sessions SET
			tunnel_id=$2,virtual_ip=$3,endpoint_host=$4,allowed_ports_json=$5,operators_json=$6,
			state=$7,created_at=$8,expires_at=$9,last_activity_at=$10,last_transport_probe_at=$11,
			last_transport_confirmed_at=$12,last_agent_ready_at=$13,last_agent_ready_tunnel_id=$14,
			last_agent_ready_allowed_ports_json=$15,last_agent_ready_reason=$16,
			last_agent_ready_service_state=$17,generation=generation+1,updated_at=$18
			WHERE agent_id=$1 AND generation=$19`, args...)
	}
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return errVPNSessionGenerationConflict
	}
	session.Generation = expectedGeneration + 1
	return nil
}

func (s *vpnTunnelService) mutatePersistentSession(ctx context.Context, agentID string, mutate func(*vpnSession) bool) (*vpnSession, error) {
	for attempt := 0; attempt < vpnSessionStoreRetryLimit; attempt++ {
		session, err := s.loadPersistentSession(ctx, agentID)
		if err != nil || session == nil {
			return session, err
		}
		if !mutate(session) {
			return session, nil
		}
		expected := session.Generation
		if err := s.savePersistentSession(ctx, session, expected); err != nil {
			if errors.Is(err, errVPNSessionGenerationConflict) {
				continue
			}
			return nil, err
		}
		s.cachePersistentSession(session)
		return session, nil
	}
	return nil, errVPNSessionGenerationConflict
}

func cloneVPNSession(session *vpnSession) *vpnSession {
	if session == nil {
		return nil
	}
	clone := *session
	clone.AllowedPorts = append([]int(nil), session.AllowedPorts...)
	clone.LastAgentReadyAllowedPorts = append([]int(nil), session.LastAgentReadyAllowedPorts...)
	clone.Token = copyMap(session.Token)
	clone.Operators = map[string]struct{}{}
	for operatorID := range session.Operators {
		clone.Operators[operatorID] = struct{}{}
	}
	return &clone
}

func (s *vpnTunnelService) cachePersistentSession(session *vpnSession) {
	if session == nil {
		return
	}
	cached := cloneVPNSession(session)
	s.mu.Lock()
	if existing := s.sessionsByAgent[cached.AgentID]; existing != nil && existing.TunnelID != cached.TunnelID {
		delete(s.sessionsByID, existing.TunnelID)
	}
	if cached.State == "active" {
		s.sessionsByAgent[cached.AgentID] = cached
		s.sessionsByID[cached.TunnelID] = cached
	} else {
		delete(s.sessionsByAgent, cached.AgentID)
		delete(s.sessionsByID, cached.TunnelID)
	}
	s.mu.Unlock()
}

func (s *vpnTunnelService) getOrCreatePersistentClientKeys(ctx context.Context, agentID string) (vpnClientKeys, error) {
	candidate, err := generateWireGuardKeyPair()
	if err != nil {
		return vpnClientKeys{}, err
	}
	db := vpnDB(s.auth)
	if db == nil {
		return candidate, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return vpnClientKeys{}, err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO engine.device_vpn_key_leases(agent_id,client_private_key,client_public_key,updated_at)
		VALUES($1,$2,$3,$4) ON CONFLICT(agent_id) DO NOTHING`, agentID, candidate.Private, candidate.Public, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		conn.Close()
		return vpnClientKeys{}, err
	}
	var keys vpnClientKeys
	err = conn.QueryRowContext(ctx, `SELECT client_private_key,client_public_key FROM engine.device_vpn_key_leases WHERE agent_id=$1`, agentID).Scan(&keys.Private, &keys.Public)
	if err == nil && (cleanText(keys.Private) == "" || cleanText(keys.Public) == "") {
		_, err = conn.ExecContext(ctx, `UPDATE engine.device_vpn_key_leases
			SET client_private_key=$2,client_public_key=$3,updated_at=$4
			WHERE agent_id=$1 AND (client_private_key='' OR client_public_key='')`, agentID, candidate.Private, candidate.Public, time.Now().UTC().Format(time.RFC3339Nano))
		if err == nil {
			err = conn.QueryRowContext(ctx, `SELECT client_private_key,client_public_key FROM engine.device_vpn_key_leases WHERE agent_id=$1`, agentID).Scan(&keys.Private, &keys.Public)
		}
	}
	conn.Close()
	if err == nil && (cleanText(keys.Private) == "" || cleanText(keys.Public) == "") {
		return vpnClientKeys{}, errors.New("vpn_key_lease_invalid")
	}
	return keys, err
}

func (s *vpnTunnelService) getOrCreatePersistentVirtualIP(ctx context.Context, agentID string) (string, error) {
	db := vpnDB(s.auth)
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.allocateVirtualIPLocked(agentID)
	}
	for attempt := 0; attempt < vpnSessionStoreRetryLimit; attempt++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			return "", err
		}
		var existing string
		err = conn.QueryRowContext(ctx, `SELECT virtual_ip FROM engine.device_vpn_ip_leases WHERE agent_id=$1`, agentID).Scan(&existing)
		if err == nil && s.usablePeerVirtualIP(existing) {
			conn.Close()
			return cleanText(existing), nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			conn.Close()
			return "", err
		}
		replaceExisting := err == nil
		rows, queryErr := conn.QueryContext(ctx, `SELECT agent_id,virtual_ip FROM engine.device_vpn_ip_leases`)
		if queryErr != nil {
			conn.Close()
			return "", queryErr
		}
		leases := map[string]string{}
		for rows.Next() {
			var owner, virtualIP string
			if scanErr := rows.Scan(&owner, &virtualIP); scanErr != nil {
				rows.Close()
				conn.Close()
				return "", scanErr
			}
			leases[cleanText(owner)] = cleanText(virtualIP)
		}
		rowsErr := rows.Err()
		rows.Close()
		conn.Close()
		if rowsErr != nil {
			return "", rowsErr
		}
		s.mu.Lock()
		s.ipLeases = leases
		candidate, allocationErr := s.allocateVirtualIPLocked(agentID)
		s.mu.Unlock()
		if allocationErr != nil {
			return "", allocationErr
		}
		conn, err = db.Conn(ctx)
		if err != nil {
			return "", err
		}
		var result sql.Result
		var insertErr error
		if replaceExisting {
			result, insertErr = conn.ExecContext(ctx, `UPDATE engine.device_vpn_ip_leases
				SET virtual_ip=$2,updated_at=$3 WHERE agent_id=$1 AND virtual_ip=$4`, agentID, candidate, time.Now().UTC().Format(time.RFC3339Nano), existing)
		} else {
			result, insertErr = conn.ExecContext(ctx, `INSERT INTO engine.device_vpn_ip_leases(agent_id,virtual_ip,updated_at)
				VALUES($1,$2,$3) ON CONFLICT(agent_id) DO NOTHING`, agentID, candidate, time.Now().UTC().Format(time.RFC3339Nano))
		}
		conn.Close()
		if insertErr != nil {
			continue
		}
		inserted, resultErr := result.RowsAffected()
		if resultErr == nil && inserted == 1 {
			return candidate, nil
		}
	}
	return "", errors.New("vpn_ip_allocation_conflict")
}

func (s *vpnTunnelService) connectPersistent(ctx context.Context, request vpnConnectRequest) (map[string]any, error) {
	agentID := cleanText(request.AgentID)
	if err := s.ensurePersistentSessionStore(ctx); err != nil {
		return nil, err
	}
	keys, err := s.getOrCreatePersistentClientKeys(ctx, agentID)
	if err != nil {
		return nil, err
	}
	virtualIP, err := s.getOrCreatePersistentVirtualIP(ctx, agentID)
	if err != nil {
		return nil, err
	}
	requiredPorts := uniquePorts(request.RequiredPorts)
	now := time.Now().UTC()
	for attempt := 0; attempt < vpnSessionStoreRetryLimit; attempt++ {
		session, loadErr := s.loadPersistentSession(ctx, agentID)
		if loadErr != nil {
			return nil, loadErr
		}
		expectedGeneration := int64(0)
		if session == nil || session.State != "active" {
			tunnelID, randomErr := randomHex(16)
			if randomErr != nil {
				return nil, randomErr
			}
			if session != nil {
				expectedGeneration = session.Generation
			}
			session = &vpnSession{
				TunnelID:             tunnelID,
				AgentID:              agentID,
				VirtualIP:            virtualIP,
				ClientPrivateKey:     keys.Private,
				ClientPublicKey:      keys.Public,
				AllowedPorts:         mergePorts(s.allowPorts, requiredPorts),
				CreatedAt:            now,
				ExpiresAt:            now.Add(defaultVPNTokenTTL),
				LastActivity:         now,
				EndpointHost:         cleanText(request.EndpointHost),
				Operators:            map[string]struct{}{},
				LastTransportProbeAt: time.Time{},
				State:                "active",
			}
		} else {
			expectedGeneration = session.Generation
			session.VirtualIP = virtualIP
			session.ClientPrivateKey = keys.Private
			session.ClientPublicKey = keys.Public
			session.AllowedPorts = mergePorts(s.allowPorts, session.AllowedPorts, requiredPorts)
			if session.Operators == nil {
				session.Operators = map[string]struct{}{}
			}
			if cleanText(request.EndpointHost) != "" && session.EndpointHost == "" {
				session.EndpointHost = cleanText(request.EndpointHost)
			}
			if session.ExpiresAt.Before(now.Add(30 * time.Second)) {
				session.ExpiresAt = now.Add(defaultVPNTokenTTL)
			}
		}
		if request.MarkActivity {
			session.LastActivity = now
			session.LastTransportProbeAt = now
		}
		if operatorID := cleanText(request.OperatorID); operatorID != "" {
			session.Operators[operatorID] = struct{}{}
		}
		if saveErr := s.savePersistentSession(ctx, session, expectedGeneration); saveErr != nil {
			if errors.Is(saveErr, errVPNSessionGenerationConflict) {
				continue
			}
			return nil, saveErr
		}
		session.Token = s.issueToken(agentID, session.TunnelID, session.ExpiresAt)
		s.cachePersistentSession(session)
		if err := s.upsertListenerPeer(session); err != nil {
			_, _ = s.mutatePersistentSession(ctx, agentID, func(current *vpnSession) bool {
				if current.TunnelID != session.TunnelID {
					return false
				}
				current.State = "failed"
				return true
			})
			return nil, err
		}
		payload := session.payload(true)
		s.emitStart(ctx, payload, true)
		return payload, nil
	}
	return nil, errVPNSessionGenerationConflict
}

func (s *vpnTunnelService) sharedSession(ctx context.Context, agentID string) *vpnSession {
	if s != nil && s.persistent && vpnDB(s.auth) != nil {
		if err := s.ensurePersistentSessionStore(ctx); err != nil {
			return nil
		}
		session, err := s.loadPersistentSession(ctx, cleanText(agentID))
		if err != nil || session == nil || session.State != "active" {
			return nil
		}
		s.cachePersistentSession(session)
		return session
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneVPNSession(s.sessionsByAgent[cleanText(agentID)])
}

func (s *vpnTunnelService) sharedSessions(ctx context.Context) []*vpnSession {
	if s != nil && s.persistent && vpnDB(s.auth) != nil {
		if err := s.ensurePersistentSessionStore(ctx); err == nil {
			if sessions, loadErr := s.loadPersistentSessions(ctx); loadErr == nil {
				for _, session := range sessions {
					s.cachePersistentSession(session)
				}
				return sessions
			}
		}
		return []*vpnSession{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions := make([]*vpnSession, 0, len(s.sessionsByAgent))
	for _, session := range s.sessionsByAgent {
		sessions = append(sessions, cloneVPNSession(session))
	}
	return sessions
}

func (s *vpnTunnelService) confirmPersistentPeerHealth(ctx context.Context, agentIDs []string) error {
	if len(agentIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	cutoff := now.Add(-15 * time.Second).Format(time.RFC3339Nano)
	db := vpnDB(s.auth)
	if db == nil {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `UPDATE engine.device_vpn_sessions
		SET last_transport_confirmed_at=$2,generation=generation+1,updated_at=$2
		WHERE state='active' AND agent_id=ANY($1)
		  AND (last_transport_confirmed_at IS NULL OR last_transport_confirmed_at<$3)`, pq.Array(agentIDs), now.Format(time.RFC3339Nano), cutoff)
	return err
}

func (s *vpnTunnelService) sharedOwnerTransportConfirmed(session *vpnSession, now time.Time) bool {
	if s == nil || session == nil || !s.persistent || cleanText(os.Getenv("BOREALIS_CLUSTER_EDGE_VIP")) == "" || session.LastTransportConfirmedAt.IsZero() {
		return false
	}
	age := now.Sub(session.LastTransportConfirmedAt)
	return age >= 0 && age <= sharedVPNConfirmationMaxAge
}
