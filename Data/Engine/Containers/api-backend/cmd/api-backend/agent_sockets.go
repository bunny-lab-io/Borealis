package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type agentSocketConnectionStore interface {
	listAgentSocketRoutes(ctx context.Context, profile operatorProfile) ([]agentWorkerRoute, error)
}

func agentSocketConnectionHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(agentSocketConnectionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_socket_connections_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		routes, err := store.listAgentSocketRoutes(ctx, profile)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		agents := collectAgentSocketConnections(ctx, auth, routes)
		writeJSON(w, http.StatusOK, map[string]any{"count": len(agents), "agents": agents})
	}
}

func (s *postgresOperatorStore) listAgentSocketRoutes(ctx context.Context, profile operatorProfile) ([]agentWorkerRoute, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []agentWorkerRoute{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	sqlText := `
		SELECT worker_guid, site_id, route_path_prefix, upstream_scheme, upstream_host, upstream_port, generation
		  FROM engine.job_scheduler_worker_routes
		 WHERE status=$1
		   AND retired_at IS NULL
	`
	params := []any{schedulerRouteStatusActive}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " AND site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	sqlText += " ORDER BY site_id, updated_at DESC, generation DESC"

	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := []agentWorkerRoute{}
	for rows.Next() {
		var route agentWorkerRoute
		if err := rows.Scan(
			&route.WorkerGUID,
			&route.SiteID,
			&route.RoutePathPrefix,
			&route.UpstreamScheme,
			&route.UpstreamHost,
			&route.UpstreamPort,
			&route.Generation,
		); err != nil {
			return nil, err
		}
		if route.SiteID <= 0 {
			continue
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func collectAgentSocketConnections(ctx context.Context, auth *authService, routes []agentWorkerRoute) []map[string]any {
	client := &http.Client{Timeout: 2 * time.Second}
	results := make(chan []map[string]any, len(routes))
	for _, route := range routes {
		route := route
		go func() {
			results <- fetchAgentSocketConnectionsForRoute(ctx, auth, client, route)
		}()
	}

	connections := []map[string]any{}
	seen := map[string]bool{}
	for range routes {
		for _, connection := range <-results {
			hostMode := ""
			if hostname := cleanText(connection["hostname"]); hostname != "" {
				hostMode = hostname + "|" + cleanText(connection["service_mode"])
			}
			identity := firstText(
				cleanText(connection["agent_id"]),
				cleanText(connection["agent_guid"]),
				hostMode,
			)
			if identity == "" {
				continue
			}
			seenKey := strings.ToLower(strconv.FormatInt(coerceInt64(connection["site_id"]), 10) + "|" + identity)
			if seen[seenKey] {
				continue
			}
			seen[seenKey] = true
			connections = append(connections, connection)
		}
	}
	sort.Slice(connections, func(i, j int) bool {
		return agentSocketConnectionSortKey(connections[i]) < agentSocketConnectionSortKey(connections[j])
	})
	return connections
}

func agentSocketConnectionSortKey(connection map[string]any) string {
	return strconv.FormatInt(coerceInt64(connection["site_id"]), 10) +
		"|" + strings.ToLower(cleanText(connection["hostname"])) +
		"|" + strings.ToLower(cleanText(connection["agent_id"]))
}

func fetchAgentSocketConnectionsForRoute(ctx context.Context, auth *authService, client *http.Client, route agentWorkerRoute) []map[string]any {
	connections := []map[string]any{}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	target := workerInternalURL(&route, "/agents")
	if target == "" {
		return connections
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return connections
	}
	req.Header.Set("Accept", "application/json")
	if auth != nil {
		req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	}
	resp, err := client.Do(req)
	if err != nil {
		return connections
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return connections
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return connections
	}
	agents, _ := payload["agents"].(map[string]any)
	for key, raw := range agents {
		record, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		agentID := firstText(cleanText(record["agent_id"]), cleanText(key))
		hostname := cleanText(record["hostname"])
		serviceMode := normalizeServiceMode(record["service_mode"], agentID)
		guid := normalizeGUID(record["guid"])
		if guid == "" {
			guid = normalizeGUID(record["agent_guid"])
		}
		connections = append(connections, map[string]any{
			"agent_id":        agentID,
			"agent_guid":      guid,
			"hostname":        hostname,
			"service_mode":    serviceMode,
			"helper_contexts": anySlice(record["helper_contexts"]),
			"site_id":         route.SiteID,
			"worker_guid":     route.WorkerGUID,
			"connected":       true,
		})
	}
	return connections
}
