import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  LinearProgress,
  Stack,
  Tab,
  Tabs,
} from "@mui/material";
import {
  CachedRounded as RefreshIcon,
  CheckCircleOutlineRounded as AcknowledgeIcon,
  NotificationsActiveRounded as HeaderIcon,
  OpenInNewRounded as OpenIcon,
  PauseCircleOutlineRounded as SuppressIcon,
  ReplayRounded as ReopenIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry } from "ag-grid-community";

import PageBodyFrame from "../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";
import {
  GRID_WRAPPER_SX,
  buildNavTabsSx,
  formatTimestamp,
  gridTheme,
  severityColor,
} from "../Automation/Watchdogs/shared.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Alerts";
const PAGE_SUBTITLE =
  "Work watchdog incidents, acknowledge noisy signals, suppress active issues, and jump directly to the source policy.";

function normalizeFilterValue(value) {
  return String(value || "").trim().toLowerCase();
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
  const [stateTab, setStateTab] = useState("open");
  const [incidents, setIncidents] = useState([]);
  const [queueCounts, setQueueCounts] = useState({ open: 0, suppressed: 0, resolved: 0 });
  const [loading, setLoading] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [error, setError] = useState("");
  const [selectedRows, setSelectedRows] = useState([]);

  const getRowId = useCallback((params) => String(params.data?.id ?? ""), []);

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableSelectionWithoutKeys: true,
      enableClickSelection: false,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      width: 52,
      minWidth: 52,
      maxWidth: 52,
      resizable: false,
      sortable: false,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      pinned: "left",
      lockPosition: true,
      suppressMovable: true,
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
      setSelectedRows([]);
    } catch (err) {
      setIncidents([]);
      setQueueCounts({ open: 0, suppressed: 0, resolved: 0 });
      setSelectedRows([]);
      setError(String(err?.message || err || "Failed to load incidents."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadIncidents();
  }, [loadIncidents]);

  useEffect(() => {
    setSelectedRows([]);
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

  const queueRows = useMemo(
    () => incidents.filter((item) => normalizeFilterValue(item?.state) === stateTab),
    [incidents, stateTab]
  );

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
      setStateTab(desiredState === "suppressed" ? "suppressed" : "open");
    } catch (err) {
      setError(String(err?.message || err || `Failed to ${desiredState === "open" ? "re-open" : "suppress"} selected incidents.`));
    } finally {
      setActionBusy(false);
    }
  }, [loadIncidents, queueToggleMode, selectedRows]);

  const openSelectedPolicy = useCallback(() => {
    const target = selectedRows[0];
    if (!target?.watchdog_id) return;
    navigate(APP_PATHS.watchdog(target.watchdog_id));
  }, [navigate, selectedRows]);

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
        id: "alerts-acknowledge",
        label: "Acknowledge",
        icon: <AcknowledgeIcon />,
        tone: "secondary",
        disabled: !selectedOpenRows.length || loading || actionBusy,
        onClick: acknowledgeSelected,
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
        id: "alerts-open-policy",
        label: "Open Policy",
        icon: <OpenIcon />,
        tone: "primary",
        disabled: selectedRows.length !== 1 || !selectedRows[0]?.watchdog_id,
        onClick: openSelectedPolicy,
      },
    ],
    [acknowledgeSelected, actionBusy, loadIncidents, loading, openSelectedPolicy, queueToggleMode, selectedOpenRows.length, selectedRows, toggleSelectedQueue]
  );

  const columnDefs = useMemo(
    () => [
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
        flex: 1,
        cellRenderer: (params) => (
          <Button
            color="inherit"
            onClick={() =>
              navigate(APP_PATHS.device(params?.data?.hostname), {
                state: { initialDevice: { hostname: params?.data?.hostname } },
              })
            }
            sx={{ justifyContent: "flex-start", px: 0, textTransform: "none" }}
          >
            {params.value}
          </Button>
        ),
      },
      {
        field: "watchdog_name",
        headerName: "Watchdog",
        minWidth: 220,
        flex: 1.1,
        cellRenderer: (params) => (
          <Button
            color="inherit"
            onClick={() => navigate(APP_PATHS.watchdog(params?.data?.watchdog_id))}
            sx={{ justifyContent: "flex-start", px: 0, textTransform: "none" }}
          >
            {params.value}
          </Button>
        ),
      },
      {
        field: "site_name",
        headerName: "Site",
        minWidth: 160,
        flex: 0.8,
      },
      {
        field: "message",
        headerName: "Message",
        minWidth: 300,
        flex: 1.6,
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
        field: "acknowledged_by",
        headerName: "Acknowledged By",
        minWidth: 160,
        flex: 0.7,
        valueGetter: (params) => params?.data?.acknowledged_by || "",
      },
    ],
    [navigate]
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
          <Tabs value={stateTab} onChange={(_event, nextValue) => setStateTab(nextValue)} sx={buildNavTabsSx()}>
            <Tab value="open" label={`Open (${queueCounts.open})`} />
            <Tab value="suppressed" label={`Suppressed (${queueCounts.suppressed})`} />
            <Tab value="resolved" label={`Resolved (${queueCounts.resolved})`} />
          </Tabs>
          {loading ? <LinearProgress /> : null}
          {error ? <Alert severity="error">{error}</Alert> : null}
        </Stack>
      }
      main={
        <Box sx={GRID_WRAPPER_SX}>
          <AgGridReact
            theme={gridTheme}
            rowData={queueRows}
            columnDefs={columnDefs}
            rowSelection={rowSelection}
            selectionColumnDef={selectionColumnDef}
            getRowId={getRowId}
            suppressRowClickSelection
            onSelectionChanged={(event) => setSelectedRows(event.api.getSelectedRows() || [])}
            defaultColDef={{
              sortable: true,
              filter: true,
              resizable: true,
            }}
            animateRows
            domLayout="autoHeight"
          />
        </Box>
      }
    />
  );
}
