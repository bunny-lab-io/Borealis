import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  LinearProgress,
  Menu,
  MenuItem,
  Stack,
  Typography,
} from "@mui/material";
import {
  PowerSettingsNewRounded as ActionIcon,
  PlayArrowRounded as StartIcon,
  StopRounded as StopIcon,
  CachedRounded as RestartIcon,
  KeyboardArrowDownRounded as ExpandIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});

const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';
const STANDARD_POLL_MS = 60_000;
const BOOST_POLL_MS = 3_000;
const BOOST_WINDOW_MS = 30_000;
const AUTO_SIZE_COLUMNS = ["name", "description"];

const MAGIC_UI = {
  panelBg: "rgba(7,11,24,0.92)",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
};

const STATUS_META = {
  running: { label: "Running", bg: "rgba(16,185,129,0.18)", color: "#6ee7b7", border: "rgba(16,185,129,0.35)" },
  stopped: { label: "Stopped", bg: "rgba(148,163,184,0.16)", color: "#cbd5e1", border: "rgba(148,163,184,0.35)" },
  starting: { label: "Starting", bg: "rgba(125,183,255,0.18)", color: "#a5d8ff", border: "rgba(125,183,255,0.35)" },
  stopping: { label: "Stopping", bg: "rgba(251,191,36,0.18)", color: "#fcd34d", border: "rgba(251,191,36,0.35)" },
  paused: { label: "Paused", bg: "rgba(192,132,252,0.18)", color: "#d8b4fe", border: "rgba(192,132,252,0.35)" },
  failed: { label: "Failed", bg: "rgba(244,63,94,0.18)", color: "#fda4af", border: "rgba(244,63,94,0.35)" },
  unknown: { label: "Unknown", bg: "rgba(71,85,105,0.24)", color: "#cbd5e1", border: "rgba(71,85,105,0.4)" },
};

const ACTION_META = {
  start: { label: "Start", pending: "Starting...", icon: <StartIcon fontSize="small" /> },
  stop: { label: "Stop", pending: "Stopping...", icon: <StopIcon fontSize="small" /> },
  restart: { label: "Restart", pending: "Restarting...", icon: <RestartIcon fontSize="small" /> },
};

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function formatStatusMeta(statusCode) {
  const normalized = normalizeText(statusCode).toLowerCase();
  return STATUS_META[normalized] || STATUS_META.unknown;
}

function formatUpdatedAt(epochSeconds) {
  const value = Number(epochSeconds || 0);
  if (!value) return "Awaiting service telemetry";
  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) return "Awaiting service telemetry";
  return `Updated ${date.toLocaleString()}`;
}

function StatusCell({ data }) {
  const statusMeta = formatStatusMeta(data?.status_code);
  const pendingMeta = ACTION_META[normalizeText(data?.pending_action).toLowerCase()] || null;
  return (
    <Stack direction="row" spacing={0.9} alignItems="center" flexWrap="wrap" useFlexGap>
      <Box
        component="span"
        sx={{
          display: "inline-flex",
          alignItems: "center",
          px: 1.15,
          py: 0.45,
          borderRadius: 999,
          fontSize: "0.76rem",
          fontWeight: 700,
          letterSpacing: "0.01em",
          color: statusMeta.color,
          backgroundColor: statusMeta.bg,
          border: `1px solid ${statusMeta.border}`,
          whiteSpace: "nowrap",
        }}
      >
        {statusMeta.label}
      </Box>
      {pendingMeta ? (
        <Box
          component="span"
          sx={{
            display: "inline-flex",
            alignItems: "center",
            px: 1.15,
            py: 0.45,
            borderRadius: 999,
            fontSize: "0.76rem",
            fontWeight: 700,
            letterSpacing: "0.01em",
            color: "#d8b4fe",
            backgroundColor: "rgba(192,132,252,0.16)",
            border: "1px solid rgba(192,132,252,0.35)",
            whiteSpace: "nowrap",
          }}
        >
          {pendingMeta.pending}
        </Box>
      ) : null}
    </Stack>
  );
}

export default function ServiceList({ device }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reportedAt, setReportedAt] = useState(0);
  const [agentSocket, setAgentSocket] = useState(false);
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [actionBusy, setActionBusy] = useState("");
  const [selectedServiceId, setSelectedServiceId] = useState("");
  const [pollSeed, setPollSeed] = useState(0);

  const gridApiRef = useRef(null);
  const inFlightRef = useRef(false);
  const fastPollUntilRef = useRef(0);

  const hostname = useMemo(() => {
    return (
      normalizeText(device?.hostname) ||
      normalizeText(device?.summary?.hostname) ||
      normalizeText(device?.device_hostname)
    );
  }, [device]);

  const selectedService = useMemo(
    () => rows.find((row) => row.service_id === selectedServiceId) || null,
    [rows, selectedServiceId]
  );

  useEffect(() => {
    setRows([]);
    setError("");
    setReportedAt(0);
    setAgentSocket(false);
    setSelectedServiceId("");
    setLoading(Boolean(hostname));
  }, [hostname]);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current;
    if (!api || !rows.length) return;
    const run = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* ignore auto-size timing errors */
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, [rows.length]);

  const startBoostPolling = useCallback(() => {
    fastPollUntilRef.current = Date.now() + BOOST_WINDOW_MS;
    setPollSeed((value) => value + 1);
  }, []);

  const loadServices = useCallback(
    async ({ silent = false } = {}) => {
      if (!hostname || inFlightRef.current) return;
      inFlightRef.current = true;
      if (!silent) {
        setLoading(true);
      }
      try {
        const response = await fetch(`/api/device/services/${encodeURIComponent(hostname)}`);
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.error || `HTTP ${response.status}`);
        }
        const nextRows = Array.isArray(payload?.services) ? payload.services : [];
        setRows(nextRows);
        setReportedAt(Number(payload?.reported_at || 0) || 0);
        setAgentSocket(Boolean(payload?.agent_socket));
        setError("");
        setSelectedServiceId((current) =>
          current && !nextRows.some((row) => row.service_id === current) ? "" : current
        );
      } catch (err) {
        setError(String(err?.message || err || "Failed to load services."));
      } finally {
        inFlightRef.current = false;
        if (!silent) {
          setLoading(false);
        }
      }
    },
    [hostname]
  );

  const handleAction = useCallback(
    async (action) => {
      if (!hostname || !selectedService || actionBusy) return;
      setActionBusy(action);
      setError("");
      setMenuAnchor(null);
      try {
        const response = await fetch(`/api/device/services/${encodeURIComponent(hostname)}/action`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            service_name: selectedService.name,
            action,
          }),
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.error || `HTTP ${response.status}`);
        }
        const nextRows = Array.isArray(payload?.services) ? payload.services : [];
        setRows(nextRows);
        setReportedAt(Number(payload?.reported_at || reportedAt || 0) || 0);
        setAgentSocket(true);
        startBoostPolling();
      } catch (err) {
        setError(String(err?.message || err || "Service action failed."));
      } finally {
        setActionBusy("");
      }
    },
    [actionBusy, hostname, reportedAt, selectedService, startBoostPolling]
  );

  const columnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Name",
        minWidth: 210,
        sortable: true,
        cellClass: "auto-col-tight",
        cellStyle: { color: "#dbeafe", fontWeight: 600 },
      },
      {
        field: "description",
        headerName: "Description",
        minWidth: 320,
        sortable: true,
        cellClass: "auto-col-tight",
        valueFormatter: (params) => normalizeText(params.value) || "No description",
      },
      {
        field: "status",
        headerName: "Status",
        minWidth: 220,
        flex: 1,
        sortable: true,
        cellClass: "auto-col-tight",
        cellRenderer: StatusCell,
      },
    ],
    []
  );

  const defaultColDef = useMemo(
    () => ({
      resizable: true,
      filter: false,
      minWidth: 150,
      sortable: true,
    }),
    []
  );

  useEffect(() => {
    let cancelled = false;
    let timer = null;

    const scheduleNext = (delay) => {
      if (cancelled) return;
      timer = setTimeout(async () => {
        await loadServices({ silent: true });
        if (cancelled) return;
        const nextDelay = Date.now() < fastPollUntilRef.current ? BOOST_POLL_MS : STANDARD_POLL_MS;
        scheduleNext(nextDelay);
      }, delay);
    };

    loadServices({ silent: false });
    scheduleNext(Date.now() < fastPollUntilRef.current ? BOOST_POLL_MS : STANDARD_POLL_MS);

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [hostname, loadServices, pollSeed]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !hostname) return undefined;

    const normalizedHostname = hostname.toLowerCase();
    const handleServicesChanged = (payload = {}) => {
      const payloadHostname = normalizeText(payload?.hostname).toLowerCase();
      if (!payloadHostname || payloadHostname !== normalizedHostname) return;
      const change = normalizeText(payload?.change).toLowerCase();
      if (change === "requested") {
        startBoostPolling();
        return;
      }
      loadServices({ silent: true });
    };

    socket.on("device_services_changed", handleServicesChanged);
    return () => {
      socket.off("device_services_changed", handleServicesChanged);
    };
  }, [hostname, loadServices, startBoostPolling]);

  useEffect(() => {
    autoSizeColumns();
  }, [autoSizeColumns, rows]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    if (!selectedServiceId) {
      api.deselectAll();
      return;
    }
    api.forEachNode((node) => {
      node.setSelected(node.data?.service_id === selectedServiceId);
    });
  }, [rows, selectedServiceId]);

  const headerNote = useMemo(() => {
    if (!hostname) return "Select a device to view service inventory.";
    if (!agentSocket) return "System agent socket unavailable. Showing cached service data only.";
    return "Service inventory refreshes every 60 seconds, with short accelerated refreshes after an action.";
  }, [agentSocket, hostname]);

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <Stack
        direction={{ xs: "column", md: "row" }}
        spacing={1.5}
        justifyContent="space-between"
        alignItems={{ xs: "flex-start", md: "center" }}
      >
        <Box sx={{ minWidth: 0 }}>
          <Typography
            variant="h5"
            sx={{
              color: MAGIC_UI.textBright,
              fontWeight: 700,
              letterSpacing: "0.01em",
            }}
          >
            Service Management
          </Typography>
          <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.4 }}>
            Query, start, stop, and restart system services.
          </Typography>
          <Typography variant="caption" sx={{ display: "block", color: MAGIC_UI.textMuted, mt: 0.7 }}>
            {headerNote}
          </Typography>
          <Typography variant="caption" sx={{ display: "block", color: "rgba(203,213,225,0.78)", mt: 0.35 }}>
            {formatUpdatedAt(reportedAt)}
          </Typography>
        </Box>
        <Stack direction="row" spacing={1} alignItems="center">
          <Button
            variant="contained"
            onClick={(event) => setMenuAnchor(event.currentTarget)}
            disabled={!selectedService || !agentSocket || Boolean(actionBusy)}
            endIcon={<ExpandIcon />}
            startIcon={<ActionIcon />}
            sx={{
              backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
              color: "#0b1220",
              borderRadius: 999,
              textTransform: "none",
              fontWeight: 700,
              px: 2.25,
              minWidth: 132,
              "&:hover": {
                backgroundImage: "linear-gradient(135deg,#8ce2ff,#d7abff)",
              },
              "&.Mui-disabled": {
                color: "rgba(15,23,42,0.55)",
                backgroundImage: "linear-gradient(135deg,rgba(125,211,252,0.55),rgba(192,132,252,0.45))",
              },
            }}
          >
            {actionBusy ? ACTION_META[actionBusy]?.pending || "Working..." : "Action"}
          </Button>
        </Stack>
      </Stack>

      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={() => setMenuAnchor(null)}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
          },
        }}
      >
        {Object.entries(ACTION_META).map(([action, meta]) => (
          <MenuItem key={action} onClick={() => handleAction(action)} disabled={Boolean(actionBusy)}>
            <Stack direction="row" spacing={1} alignItems="center">
              {meta.icon}
              <span>{meta.label}</span>
            </Stack>
          </MenuItem>
        ))}
      </Menu>

      {(loading || actionBusy) && (
        <LinearProgress
          sx={{
            borderRadius: 999,
            backgroundColor: "rgba(15,23,42,0.75)",
            "& .MuiLinearProgress-bar": {
              backgroundImage: "linear-gradient(90deg,#7dd3fc,#c084fc)",
            },
          }}
        />
      )}

      {error ? (
        <Box
          sx={{
            borderRadius: 2,
            border: "1px solid rgba(248,113,113,0.28)",
            backgroundColor: "rgba(127,29,29,0.18)",
            color: "#fecaca",
            px: 1.5,
            py: 1,
          }}
        >
          <Typography variant="body2">{error}</Typography>
        </Box>
      ) : null}

      <Box
        sx={{
          flexGrow: 1,
          minHeight: 360,
          borderRadius: 3,
          border: `1px solid ${MAGIC_UI.panelBorder}`,
          background: MAGIC_UI.panelBg,
          boxShadow: "0 24px 48px rgba(2, 6, 23, 0.38)",
          overflow: "hidden",
          width: "100%",
          "& .ag-root-wrapper": {
            minHeight: "100%",
            border: "none",
            borderRadius: 0,
            background: "transparent",
          },
          "& .ag-header": {
            background: "linear-gradient(180deg, rgba(15,23,42,0.98), rgba(9,14,28,0.96))",
            borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
          },
          "& .ag-header-cell": {
            fontWeight: 600,
            color: "#cbd5e1",
            letterSpacing: "0.02em",
          },
          "& .ag-row": {
            backgroundColor: "rgba(8,13,28,0.72)",
            borderBottom: "1px solid rgba(148,163,184,0.08)",
          },
          "& .ag-row:nth-of-type(even)": {
            backgroundColor: "rgba(10,16,32,0.9)",
          },
          "& .ag-row-selected": {
            backgroundColor: "rgba(125,211,252,0.2) !important",
            boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
          },
          "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            textAlign: "left",
            padding: "8px 12px 8px 18px",
          },
          "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            padding: 0,
          },
          "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
            paddingLeft: "12px",
            paddingRight: "9px",
          },
          "--ag-font-family": gridFontFamily,
          "--ag-icon-font-family": iconFontFamily,
          "--ag-background-color": "transparent",
          "--ag-foreground-color": "#f4f7ff",
          "--ag-header-background-color": "transparent",
          "--ag-border-color": "rgba(148,163,184,0.18)",
          "--ag-wrapper-border-radius": "0px",
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
          "--ag-checkbox-checked-color": "#7dd3fc",
        }}
      >
        <AgGridReact
          rowData={rows}
          columnDefs={columnDefs}
          defaultColDef={defaultColDef}
          rowSelection="single"
          suppressCellFocus
          animateRows
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          theme={gridTheme}
          getRowId={(params) => String(params.data?.service_id || params.data?.name || "")}
          overlayNoRowsTemplate={
            hostname
              ? '<span style="color:#94a3b8;">No cached services reported yet.</span>'
              : '<span style="color:#94a3b8;">No device selected.</span>'
          }
          onGridReady={(params) => {
            gridApiRef.current = params.api;
            autoSizeColumns();
          }}
          onSelectionChanged={(event) => {
            const selected = event.api.getSelectedRows?.()?.[0] || null;
            setSelectedServiceId(selected?.service_id || "");
          }}
          style={{
            width: "100%",
            height: "100%",
            fontFamily: gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
          }}
        />
      </Box>
    </Box>
  );
}
