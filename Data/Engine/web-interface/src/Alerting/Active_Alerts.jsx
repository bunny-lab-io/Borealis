import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  LinearProgress,
  MenuItem,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import {
  CachedRounded as RefreshIcon,
  CheckCircleOutlineRounded as AcknowledgeIcon,
  NotificationsActiveRounded as HeaderIcon,
  OpenInNewRounded as OpenIcon,
  PauseCircleOutlineRounded as SuppressIcon,
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
  summarizeRuleResults,
} from "../Automation/Watchdogs/shared.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Active Alerts";
const PAGE_SUBTITLE =
  "Work the live watchdog incident queue, acknowledge noisy signals, and jump directly to affected devices or watchdog policies.";

function normalizeFilterValue(value) {
  return String(value || "").trim().toLowerCase();
}

export default function ActiveAlerts() {
  const navigate = useNavigate();
  const gridRef = useRef(null);
  const [stateTab, setStateTab] = useState("open");
  const [incidents, setIncidents] = useState([]);
  const [loading, setLoading] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [error, setError] = useState("");
  const [selectedRows, setSelectedRows] = useState([]);
  const [filters, setFilters] = useState({
    severity: "all",
    site: "",
    device: "",
    watchdog: "",
    acknowledged: "all",
  });

  const loadIncidents = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await fetch(`/api/watchdogs/incidents?state=${encodeURIComponent(stateTab)}`, {
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.errors?.[0] || payload?.error || payload?.message || `HTTP ${response.status}`);
      }
      setIncidents(Array.isArray(payload?.items) ? payload.items : []);
    } catch (err) {
      setIncidents([]);
      setError(String(err?.message || err || "Failed to load incidents."));
    } finally {
      setLoading(false);
    }
  }, [stateTab]);

  useEffect(() => {
    loadIncidents();
  }, [loadIncidents]);

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

  const visibleRows = useMemo(() => {
    return incidents.filter((item) => {
      const severity = normalizeFilterValue(item?.severity);
      const siteName = normalizeFilterValue(item?.site_name);
      const hostname = normalizeFilterValue(item?.hostname);
      const watchdogName = normalizeFilterValue(item?.watchdog_name);
      const acknowledged = Boolean(item?.acknowledged_at);
      if (filters.severity !== "all" && severity !== filters.severity) return false;
      if (filters.site && !siteName.includes(normalizeFilterValue(filters.site))) return false;
      if (filters.device && !hostname.includes(normalizeFilterValue(filters.device))) return false;
      if (filters.watchdog && !watchdogName.includes(normalizeFilterValue(filters.watchdog))) return false;
      if (filters.acknowledged === "yes" && !acknowledged) return false;
      if (filters.acknowledged === "no" && acknowledged) return false;
      return true;
    });
  }, [filters, incidents]);

  const selectedOpenRows = useMemo(
    () => selectedRows.filter((item) => String(item?.state || "").toLowerCase() === "open"),
    [selectedRows]
  );

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
      setSelectedRows([]);
      loadIncidents();
    } catch (err) {
      setError(String(err?.message || err || "Failed to acknowledge selected incidents."));
    } finally {
      setActionBusy(false);
    }
  }, [loadIncidents, selectedOpenRows]);

  const suppressSelected = useCallback(async () => {
    if (!selectedOpenRows.length) return;
    const reason =
      window.prompt(
        "Optional suppression reason for the selected device watchdog overrides:",
        "Temporarily suppressed from Active Alerts."
      ) || "Temporarily suppressed from Active Alerts.";
    setActionBusy(true);
    setError("");
    try {
      await Promise.all(
        selectedOpenRows.map((row) =>
          fetch(`/api/devices/${encodeURIComponent(row.device_guid || row.hostname)}/watchdogs/overrides`, {
            method: "POST",
            credentials: "include",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              watchdog_id: row.watchdog_id,
              state: "suppressed",
              reason,
            }),
          })
        )
      );
      setSelectedRows([]);
      loadIncidents();
    } catch (err) {
      setError(String(err?.message || err || "Failed to suppress selected incidents."));
    } finally {
      setActionBusy(false);
    }
  }, [loadIncidents, selectedOpenRows]);

  const openSelectedDevice = useCallback(() => {
    const target = selectedRows[0];
    if (!target?.hostname) return;
    navigate(APP_PATHS.device(target.hostname), { state: { initialDevice: { hostname: target.hostname } } });
  }, [navigate, selectedRows]);

  const openSelectedPolicy = useCallback(() => {
    const target = selectedRows[0];
    if (!target?.watchdog_id) return;
    navigate(APP_PATHS.watchdog(target.watchdog_id));
  }, [navigate, selectedRows]);

  const columnDefs = useMemo(
    () => [
      {
        checkboxSelection: true,
        headerCheckboxSelection: true,
        width: 54,
        pinned: "left",
        sortable: false,
        filter: false,
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
        minWidth: 260,
        flex: 1.4,
      },
      {
        field: "sample",
        headerName: "Matched Details",
        minWidth: 320,
        flex: 1.4,
        valueGetter: (params) => summarizeRuleResults(params?.data?.sample),
      },
      {
        field: "opened_at",
        headerName: "Opened",
        minWidth: 180,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: "updated_at",
        headerName: "Updated",
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
  });

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={
        <Stack spacing={1.5}>
          <Stack direction={{ xs: "column", lg: "row" }} spacing={1.5} justifyContent="space-between">
            <Tabs value={stateTab} onChange={(_event, nextValue) => setStateTab(nextValue)} sx={buildNavTabsSx()}>
              <Tab value="open" label={`Open (${incidents.filter((item) => item?.state === "open").length})`} />
              <Tab
                value="resolved"
                label={`Resolved (${incidents.filter((item) => item?.state === "resolved").length})`}
              />
            </Tabs>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              <Button startIcon={<RefreshIcon />} onClick={loadIncidents} disabled={loading || actionBusy}>
                Refresh
              </Button>
              <Button
                startIcon={<AcknowledgeIcon />}
                onClick={acknowledgeSelected}
                disabled={!selectedOpenRows.length || actionBusy || stateTab !== "open"}
              >
                Acknowledge
              </Button>
              <Button
                startIcon={<SuppressIcon />}
                onClick={suppressSelected}
                disabled={!selectedOpenRows.length || actionBusy || stateTab !== "open"}
              >
                Suppress
              </Button>
              <Button
                startIcon={<OpenIcon />}
                onClick={openSelectedDevice}
                disabled={selectedRows.length !== 1}
              >
                Open Device
              </Button>
              <Button
                startIcon={<OpenIcon />}
                onClick={openSelectedPolicy}
                disabled={selectedRows.length !== 1}
              >
                Open Policy
              </Button>
            </Stack>
          </Stack>
          <Stack direction={{ xs: "column", xl: "row" }} spacing={1.25}>
            <TextField
              select
              label="Severity"
              value={filters.severity}
              onChange={(event) => setFilters((prev) => ({ ...prev, severity: event.target.value }))}
              sx={{ minWidth: 150 }}
            >
              <MenuItem value="all">All</MenuItem>
              <MenuItem value="info">Info</MenuItem>
              <MenuItem value="warning">Warning</MenuItem>
              <MenuItem value="error">Error</MenuItem>
            </TextField>
            <TextField
              label="Site"
              value={filters.site}
              onChange={(event) => setFilters((prev) => ({ ...prev, site: event.target.value }))}
            />
            <TextField
              label="Device"
              value={filters.device}
              onChange={(event) => setFilters((prev) => ({ ...prev, device: event.target.value }))}
            />
            <TextField
              label="Watchdog"
              value={filters.watchdog}
              onChange={(event) => setFilters((prev) => ({ ...prev, watchdog: event.target.value }))}
            />
            <TextField
              select
              label="Acknowledged"
              value={filters.acknowledged}
              onChange={(event) => setFilters((prev) => ({ ...prev, acknowledged: event.target.value }))}
              sx={{ minWidth: 170 }}
            >
              <MenuItem value="all">All</MenuItem>
              <MenuItem value="yes">Acknowledged</MenuItem>
              <MenuItem value="no">Unacknowledged</MenuItem>
            </TextField>
          </Stack>
          {loading ? <LinearProgress /> : null}
          {error ? <Alert severity="error">{error}</Alert> : null}
          <Typography variant="body2" sx={{ color: "rgba(203, 213, 225, 0.82)" }}>
            Alerts stay separate from Watchdog authoring so operators can focus on incidents here while automation engineers refine policy behavior under Automation.
          </Typography>
        </Stack>
      }
      main={
        <Box sx={GRID_WRAPPER_SX}>
          <AgGridReact
            ref={gridRef}
            theme={gridTheme}
            rowData={visibleRows}
            columnDefs={columnDefs}
            rowSelection={{ mode: "multiRow" }}
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
