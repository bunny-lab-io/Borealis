import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Chip,
  LinearProgress,
  Stack,
  Typography,
} from "@mui/material";
import {
  CachedRounded as RefreshIcon,
  CheckCircleOutlineRounded as AcknowledgeIcon,
  NotificationsActiveRounded as HeaderIcon,
  PauseCircleOutlineRounded as SuppressIcon,
  ReplayRounded as ReopenIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry } from "ag-grid-community";

import PageBodyFrame from "../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";
import {
  BOREALIS_BLUE,
  CountSliderGroup,
  GRID_WRAPPER_SX,
  formatTimestamp,
  gridTheme,
  severityColor,
} from "../Automation/Watchdogs/shared.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Alerts";
const PAGE_SUBTITLE =
  "Work watchdog incidents, acknowledge noisy signals, and suppress active issues directly from the queue.";
const STATUS_FILTER_OPTIONS = [
  { key: "open", label: "Open" },
  { key: "suppressed", label: "Suppressed" },
  { key: "resolved", label: "Resolved" },
];

const INCIDENT_STATUS_META = {
  open: {
    label: "Open",
    color: "#fcd34d",
    borderColor: "rgba(252, 211, 77, 0.38)",
    backgroundColor: "rgba(252, 211, 77, 0.12)",
  },
  suppressed: {
    label: "Suppressed",
    color: "#93c5fd",
    borderColor: "rgba(147, 197, 253, 0.38)",
    backgroundColor: "rgba(147, 197, 253, 0.12)",
  },
  resolved: {
    label: "Resolved",
    color: "#86efac",
    borderColor: "rgba(134, 239, 172, 0.38)",
    backgroundColor: "rgba(134, 239, 172, 0.12)",
  },
};

function normalizeFilterValue(value) {
  return String(value || "").trim().toLowerCase();
}

function resolveIncidentStatusMeta(value) {
  const normalized = normalizeFilterValue(value);
  return INCIDENT_STATUS_META[normalized] || INCIDENT_STATUS_META.open;
}

function buildQueueCounts(payloadCounts = {}, items = []) {
  const counts = {
    open: 0,
    suppressed: 0,
    resolved: 0,
  };

  Object.entries(payloadCounts || {}).forEach(([state, count]) => {
    const normalizedState = normalizeFilterValue(state);
    if (Object.prototype.hasOwnProperty.call(counts, normalizedState)) {
      counts[normalizedState] = Number(count || 0);
    }
  });

  if (!Object.values(counts).some((value) => value > 0)) {
    normalizeArray(items).forEach((item) => {
      const normalizedState = normalizeFilterValue(item?.state);
      if (Object.prototype.hasOwnProperty.call(counts, normalizedState)) {
        counts[normalizedState] += 1;
      }
    });
  }

  return counts;
}

function normalizeArray(value) {
  return Array.isArray(value) ? value : [];
}

export default function ActiveAlerts() {
  const navigate = useNavigate();
  const gridRef = useRef(null);
  const [stateTab, setStateTab] = useState("");
  const [incidents, setIncidents] = useState([]);
  const [queueCounts, setQueueCounts] = useState({ open: 0, suppressed: 0, resolved: 0 });
  const [loading, setLoading] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [error, setError] = useState("");
  const [selectedIncidentIds, setSelectedIncidentIds] = useState([]);

  const getRowId = useCallback((params) => String(params.data?.id ?? ""), []);

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      width: 52,
      maxWidth: 52,
      minWidth: 52,
      pinned: "left",
      resizable: false,
      sortable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  const loadIncidents = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await fetch("/api/watchdogs/incidents?state=all", {
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.errors?.[0] || payload?.error || payload?.message || `HTTP ${response.status}`);
      }
      const nextItems = Array.isArray(payload?.items) ? payload.items : [];
      setIncidents(nextItems);
      setQueueCounts(buildQueueCounts(payload?.counts, nextItems));
      setSelectedIncidentIds((previousIds) => {
        const nextIdSet = new Set(nextItems.map((item) => String(item?.id ?? "")));
        return previousIds.filter((incidentId) => nextIdSet.has(incidentId));
      });
    } catch (err) {
      setIncidents([]);
      setQueueCounts({ open: 0, suppressed: 0, resolved: 0 });
      setSelectedIncidentIds([]);
      gridRef.current?.api?.deselectAll?.();
      setError(String(err?.message || err || "Failed to load incidents."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadIncidents();
  }, [loadIncidents]);

  useEffect(() => {
    setSelectedIncidentIds([]);
    gridRef.current?.api?.deselectAll?.();
  }, [stateTab]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || typeof socket.on !== "function") return undefined;
    const handler = () => loadIncidents();
    socket.on("watchdog_incidents_changed", handler);
    return () => {
      try {
        socket.off("watchdog_incidents_changed", handler);
      } catch {
        /* noop */
      }
    };
  }, [loadIncidents]);

  const queueRows = useMemo(() => {
    const filteredRows = stateTab
      ? incidents.filter((item) => normalizeFilterValue(item?.state) === stateTab)
      : incidents;
    return [...filteredRows].sort((left, right) => {
      const leftTime = Number(left?.opened_at || 0);
      const rightTime = Number(right?.opened_at || 0);
      return rightTime - leftTime;
    });
  }, [incidents, stateTab]);

  const selectedRows = useMemo(() => {
    const selectedIdSet = new Set(selectedIncidentIds);
    return incidents.filter((item) => selectedIdSet.has(String(item?.id ?? "")));
  }, [incidents, selectedIncidentIds]);

  const selectedOpenRows = useMemo(
    () => selectedRows.filter((item) => normalizeFilterValue(item?.state) === "open"),
    [selectedRows]
  );

  const queueToggleMode = useMemo(() => {
    if (!selectedRows.length) {
      return stateTab === "suppressed" ? "reopen" : stateTab === "open" ? "suppress" : null;
    }
    const states = Array.from(new Set(selectedRows.map((row) => normalizeFilterValue(row?.state)).filter(Boolean)));
    if (states.length !== 1) return null;
    if (states[0] === "open") return "suppress";
    if (states[0] === "suppressed") return "reopen";
    return null;
  }, [selectedRows, stateTab]);

  const acknowledgeSelected = useCallback(async () => {
    if (!selectedOpenRows.length) return;
    setActionBusy(true);
    setError("");
    try {
      await Promise.all(
        selectedOpenRows.map((row) =>
          fetch(`/api/watchdogs/incidents/${encodeURIComponent(row.id)}/acknowledge`, {
            method: "POST",
            credentials: "include",
          })
        )
      );
      await loadIncidents();
    } catch (err) {
      setError(String(err?.message || err || "Failed to acknowledge selected incidents."));
    } finally {
      setActionBusy(false);
    }
  }, [loadIncidents, selectedOpenRows]);

  const toggleSelectedQueue = useCallback(async () => {
    if (!selectedRows.length || !queueToggleMode) return;
    const desiredState = queueToggleMode === "reopen" ? "open" : "suppressed";
    const reason =
      desiredState === "suppressed"
        ? window.prompt("Optional suppression reason for the selected alerts:", "Temporarily suppressed from Alerts.") ||
          "Temporarily suppressed from Alerts."
        : "";
    setActionBusy(true);
    setError("");
    try {
      await Promise.all(
        selectedRows.map((row) =>
          fetch(`/api/watchdogs/incidents/${encodeURIComponent(row.id)}/state`, {
            method: "POST",
            credentials: "include",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              state: desiredState,
              reason,
            }),
          }).then(async (response) => {
            if (response.ok) return response;
            const payload = await response.json().catch(() => ({}));
            throw new Error(payload?.errors?.[0] || payload?.error || payload?.message || `HTTP ${response.status}`);
          })
        )
      );
      await loadIncidents();
    } catch (err) {
      setError(String(err?.message || err || `Failed to ${desiredState === "open" ? "re-open" : "suppress"} selected incidents.`));
    } finally {
      setActionBusy(false);
    }
  }, [loadIncidents, queueToggleMode, selectedRows]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "alerts-refresh",
        label: "Refresh",
        icon: <RefreshIcon />,
        tone: "secondary",
        disabled: loading || actionBusy,
        onClick: loadIncidents,
      },
      {
        id: "alerts-queue-toggle",
        label: queueToggleMode === "reopen" ? "RE-OPEN" : "Suppress",
        icon: queueToggleMode === "reopen" ? <ReopenIcon /> : <SuppressIcon />,
        tone: "secondary",
        disabled: !selectedRows.length || !queueToggleMode || loading || actionBusy,
        onClick: toggleSelectedQueue,
      },
      {
        id: "alerts-acknowledge",
        label: "Acknowledge",
        icon: <AcknowledgeIcon />,
        tone: "primary",
        disabled: !selectedOpenRows.length || loading || actionBusy,
        onClick: acknowledgeSelected,
      },
    ],
    [acknowledgeSelected, actionBusy, loadIncidents, loading, queueToggleMode, selectedOpenRows.length, selectedRows.length, toggleSelectedQueue]
  );

  const columnDefs = useMemo(
    () => [
      {
        field: "state",
        headerName: "Status",
        minWidth: 140,
        width: 140,
        filter: "agTextColumnFilter",
        cellRenderer: (params) => {
          const meta = resolveIncidentStatusMeta(params.value);
          return (
            <Chip
              size="small"
              label={meta.label}
              variant="outlined"
              sx={{
                color: meta.color,
                borderColor: meta.borderColor,
                backgroundColor: meta.backgroundColor,
                fontWeight: 600,
              }}
            />
          );
        },
      },
      {
        field: "severity",
        headerName: "Severity",
        width: 120,
        cellRenderer: (params) => (
          <Chip
            size="small"
            label={String(params.value || "warning").replace(/^./, (char) => char.toUpperCase())}
            variant="outlined"
            sx={{
              color: severityColor(params.value),
              borderColor: severityColor(params.value),
              backgroundColor: "rgba(15,23,42,0.5)",
            }}
          />
        ),
      },
      {
        field: "hostname",
        headerName: "Device",
        minWidth: 180,
        width: 220,
        cellRenderer: (params) => {
          const hostname = String(params?.data?.hostname || "").trim();
          if (!hostname) return null;
          const handleClick = (event) => {
            event.preventDefault();
            event.stopPropagation();
            navigate(APP_PATHS.device(hostname), {
              state: { initialDevice: { hostname } },
            });
          };
          return (
            <a
              href="#"
              onClick={handleClick}
              title={hostname}
              style={{ color: BOREALIS_BLUE, textDecoration: "none", fontWeight: 500 }}
            >
              {hostname}
            </a>
          );
        },
      },
      {
        field: "watchdog_name",
        headerName: "Watchdog",
        minWidth: 220,
        width: 260,
        cellRenderer: (params) => {
          const watchdogId = params?.data?.watchdog_id;
          const watchdogName = String(params?.value || "").trim();
          if (!watchdogId || !watchdogName) return watchdogName || "";
          const handleClick = (event) => {
            event.preventDefault();
            event.stopPropagation();
            navigate(APP_PATHS.watchdog(watchdogId));
          };
          return (
            <a
              href="#"
              onClick={handleClick}
              title={watchdogName}
              style={{ color: BOREALIS_BLUE, textDecoration: "none", fontWeight: 500 }}
            >
              {watchdogName}
            </a>
          );
        },
      },
      {
        field: "site_name",
        headerName: "Site",
        minWidth: 160,
        width: 180,
      },
      {
        field: "message",
        headerName: "Message",
        minWidth: 300,
        width: 420,
      },
      {
        field: "opened_at",
        headerName: "Opened",
        minWidth: 180,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: "updated_at",
        headerName: "Last Updated",
        minWidth: 180,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: stateTab === "suppressed" ? "resolution_reason" : "acknowledged_by",
        headerName:
          stateTab === "suppressed"
            ? "Suppression Reason"
            : stateTab
              ? "Acknowledged By"
              : "Acknowledged / Suppression",
        minWidth: stateTab === "suppressed" ? 240 : 180,
        width: stateTab === "suppressed" ? 280 : 220,
        valueGetter: (params) => {
          const incidentState = normalizeFilterValue(params?.data?.state);
          if (stateTab === "suppressed" || incidentState === "suppressed") {
            return params?.data?.resolution_reason || "";
          }
          return params?.data?.acknowledged_by || "";
        },
      },
    ],
    [navigate, stateTab]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: HeaderIcon,
    actions: pageHeaderActions,
  });

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={
        <Stack spacing={1.5}>
          <Box
            sx={{
              display: "flex",
              flexWrap: "wrap",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 1.5,
            }}
          >
            <CountSliderGroup
              options={STATUS_FILTER_OPTIONS}
              activeKey={stateTab}
              counts={queueCounts}
              onChange={setStateTab}
            />
            <Typography variant="body2" sx={{ color: "rgba(155, 163, 180, 0.96)" }}>
              {stateTab
                ? `Showing ${queueRows.length} ${resolveIncidentStatusMeta(stateTab).label.toLowerCase()} alert${queueRows.length === 1 ? "" : "s"}`
                : `Showing all ${queueRows.length} alert${queueRows.length === 1 ? "" : "s"}`}
            </Typography>
          </Box>
          {loading ? <LinearProgress /> : null}
          {error ? <Alert severity="error">{error}</Alert> : null}
        </Stack>
      }
      main={
        <Box sx={GRID_WRAPPER_SX}>
          <AgGridReact
            ref={gridRef}
            theme={gridTheme}
            rowData={queueRows}
            columnDefs={columnDefs}
            rowSelection={rowSelection}
            selectionColumnDef={selectionColumnDef}
            getRowId={getRowId}
            suppressRowClickSelection
            onSelectionChanged={(event) =>
              setSelectedIncidentIds((event.api.getSelectedRows() || []).map((row) => String(row?.id ?? "")))
            }
            defaultColDef={{
              sortable: true,
              filter: true,
              resizable: true,
            }}
            initialState={{
              sort: {
                sortModel: [{ colId: "opened_at", sort: "desc" }],
              },
            }}
            animateRows
            domLayout="autoHeight"
          />
        </Box>
      }
    />
  );
}
