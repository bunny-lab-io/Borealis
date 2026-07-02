import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Typography } from "@mui/material";
import DevicesRoundedIcon from "@mui/icons-material/DevicesRounded";
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import { AgGridReact } from "ag-grid-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { BOREALIS_BLUE, CountSliderGroup } from "../Automation/Watchdogs/shared.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
} from "../Devices/Tabs/Shared.jsx";

const PAGE_TITLE = "Patch Management";
const PAGE_SUBTITLE = "Windows patch inventory across deployed agents.";

const STATE_FILTER_OPTIONS = [
  { key: "pending", label: "Pending" },
  { key: "installed", label: "Installed" },
];

const SEVERITY_FILTER_OPTIONS = [
  { key: "critical", label: "Critical" },
  { key: "important", label: "Important" },
  { key: "moderate", label: "Moderate" },
  { key: "low", label: "Low" },
  { key: "unspecified", label: "Unspecified" },
];

const FILTER_LABEL_SX = {
  color: BOREALIS_BLUE,
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

function text(value) {
  return String(value ?? "").trim();
}

function normalizeSeverity(value) {
  const normalized = text(value).toLowerCase();
  if (["critical", "important", "moderate", "low"].includes(normalized)) {
    return normalized;
  }
  return "unspecified";
}

function formatState(value) {
  const normalized = text(value).toLowerCase();
  if (normalized === "pending") return "Pending";
  if (normalized === "installed") return "Installed";
  return text(value) || "Unknown";
}

function formatSource(value) {
  const normalized = text(value).toLowerCase();
  if (normalized === "wua_pending") return "Windows Update";
  if (normalized === "wua_history") return "WUA History";
  if (normalized === "quick_fix_engineering") return "Get-HotFix";
  return text(value) || "Unknown";
}

function formatTimestamp(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "";
  return new Date(numeric * 1000).toLocaleString();
}

function normalizedPatchKey(row = {}) {
  return (
    text(row.patch_key) ||
    (text(row.kb) ? `kb:${text(row.kb).toUpperCase()}:state:${text(row.state).toLowerCase()}` : "") ||
    `${text(row.title).toLowerCase()}|${text(row.state).toLowerCase()}`
  );
}

function FilterSliderBlock({ label = "", children }) {
  return (
    <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
      <Typography component="span" sx={FILTER_LABEL_SX}>
        {label}
      </Typography>
      {children}
    </Box>
  );
}

function buildPatchFleetRows(rows = []) {
  const groups = new Map();
  rows.forEach((row) => {
    const key = `${normalizedPatchKey(row)}|${text(row.state).toLowerCase()}`;
    if (!groups.has(key)) {
      groups.set(key, {
        id: key,
        patch_key: text(row.patch_key),
        kb: text(row.kb),
        title: text(row.title),
        state: text(row.state).toLowerCase(),
        severity: text(row.severity),
        classification: text(row.classification),
        source: text(row.source),
        captured_at: Number(row.captured_at || 0) || 0,
        published_at: Number(row.published_at || 0) || 0,
        installed_on: Number(row.installed_on || 0) || 0,
        device_count: 0,
        hostnames: [],
        site_names: [],
        childRows: [],
      });
    }
    const current = groups.get(key);
    current.childRows.push(row);
    current.device_count += 1;
    if (Number(row.captured_at || 0) > current.captured_at) current.captured_at = Number(row.captured_at || 0);
    if (!current.severity && text(row.severity)) current.severity = text(row.severity);
    if (!current.classification && text(row.classification)) current.classification = text(row.classification);
    if (!current.source && text(row.source)) current.source = text(row.source);
    const hostname = text(row.hostname);
    if (hostname && !current.hostnames.includes(hostname)) current.hostnames.push(hostname);
    const siteName = text(row.site_name);
    if (siteName && !current.site_names.includes(siteName)) current.site_names.push(siteName);
  });
  return [...groups.values()].sort((left, right) => {
    if (left.state !== right.state) return left.state === "pending" ? -1 : 1;
    return text(left.kb || left.title).localeCompare(text(right.kb || right.title), undefined, {
      numeric: true,
      sensitivity: "base",
    });
  });
}

export default function PatchManagement() {
  const gridRef = useRef(null);
  const patchRefreshTimersRef = useRef([]);
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [patchRows, setPatchRows] = useState([]);
  const [loadError, setLoadError] = useState("");
  const [stateFilter, setStateFilter] = useState("pending");
  const [severityFilter, setSeverityFilter] = useState("");
  const selectedSiteId = useMemo(
    () => String(searchParams.get("site") || "").trim(),
    [searchParams]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: SystemUpdateAltRoundedIcon,
  });

  const loadPatchRows = useCallback(async () => {
    try {
      const response = await fetch("/api/patches/audit", { credentials: "include", cache: "no-store" });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      setPatchRows(Array.isArray(payload?.patches) ? payload.patches : []);
      setLoadError("");
    } catch (error) {
      setLoadError(String(error?.message || error));
      setPatchRows([]);
    }
  }, []);

  useEffect(() => {
    void loadPatchRows();
  }, [loadPatchRows]);

  const clearPatchRefreshTimers = useCallback(() => {
    if (typeof window === "undefined") return;
    patchRefreshTimersRef.current.forEach((timerId) => window.clearTimeout(timerId));
    patchRefreshTimersRef.current = [];
  }, []);

  const requestPatchAuditRefresh = useCallback(
    ({ burst = false } = {}) => {
      clearPatchRefreshTimers();
      void loadPatchRows();
      if (!burst || typeof window === "undefined") return;
      [1200, 2500, 5000, 10000, 20000].forEach((delayMs) => {
        const timerId = window.setTimeout(() => {
          void loadPatchRows();
        }, delayMs);
        patchRefreshTimersRef.current.push(timerId);
      });
    },
    [clearPatchRefreshTimers, loadPatchRows]
  );

  useEffect(() => () => clearPatchRefreshTimers(), [clearPatchRefreshTimers]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket) return undefined;
    const handleInventoryChanged = (payload = {}) => {
      const payloadChange = text(payload?.change).toLowerCase();
      if (payloadChange && payloadChange !== "patches_updated" && payloadChange !== "updated") return;
      requestPatchAuditRefresh({ burst: true });
    };
    socket.on("device_inventory_changed", handleInventoryChanged);
    return () => {
      socket.off("device_inventory_changed", handleInventoryChanged);
    };
  }, [requestPatchAuditRefresh]);

  const siteName = useMemo(() => {
    if (!selectedSiteId) return "";
    const row = patchRows.find((item) => String(item?.site_id ?? "") === selectedSiteId);
    return text(row?.site_name) || `Site ${selectedSiteId}`;
  }, [patchRows, selectedSiteId]);

  const siteScopedRows = useMemo(
    () =>
      selectedSiteId
        ? patchRows.filter((row) => String(row?.site_id ?? "") === selectedSiteId)
        : patchRows,
    [patchRows, selectedSiteId]
  );

  const severityCountRows = useMemo(
    () =>
      stateFilter
        ? siteScopedRows.filter((row) => text(row.state).toLowerCase() === stateFilter)
        : siteScopedRows,
    [siteScopedRows, stateFilter]
  );

  const stateCountRows = useMemo(
    () =>
      severityFilter
        ? siteScopedRows.filter((row) => normalizeSeverity(row.severity) === severityFilter)
        : siteScopedRows,
    [severityFilter, siteScopedRows]
  );

  const stateCounts = useMemo(
    () =>
      stateCountRows.reduce(
        (counts, row) => {
          const state = text(row.state).toLowerCase();
          if (state in counts) counts[state] += 1;
          return counts;
        },
        { pending: 0, installed: 0 }
      ),
    [stateCountRows]
  );

  const severityCounts = useMemo(
    () =>
      severityCountRows.reduce(
        (counts, row) => {
          const severity = normalizeSeverity(row.severity);
          counts[severity] = (counts[severity] || 0) + 1;
          return counts;
        },
        SEVERITY_FILTER_OPTIONS.reduce((counts, option) => ({ ...counts, [option.key]: 0 }), {})
      ),
    [severityCountRows]
  );

  const visibleRows = useMemo(
    () =>
      siteScopedRows.filter((row) => {
        if (stateFilter && text(row.state).toLowerCase() !== stateFilter) return false;
        if (severityFilter && normalizeSeverity(row.severity) !== severityFilter) return false;
        return true;
      }),
    [severityFilter, siteScopedRows, stateFilter]
  );

  const displayRows = useMemo(() => buildPatchFleetRows(visibleRows), [visibleRows]);

  const openDevicesForPatch = useCallback(
    (row = {}) => {
      const hostnames = Array.isArray(row.hostnames) ? row.hostnames.filter(Boolean) : [];
      if (!hostnames.length) return;
      try {
        window.localStorage.setItem("device_list_initial_hostnames_filter", JSON.stringify(hostnames));
        if (text(row.kb)) {
          window.localStorage.setItem("device_list_initial_filters", JSON.stringify({ patch: text(row.kb) }));
        }
      } catch {
        /* noop */
      }
      navigate(APP_PATHS.devices);
    },
    [navigate]
  );

  const columnDefs = useMemo(
    () => [
      {
        field: "kb",
        headerName: "KB",
        width: 120,
        minWidth: 120,
        valueGetter: (params) => text(params.data?.kb) || "No KB",
      },
      {
        field: "title",
        headerName: "Title",
        flex: 1.6,
        minWidth: 320,
        tooltipField: "title",
      },
      {
        field: "state",
        headerName: "State",
        width: 135,
        minWidth: 135,
        valueFormatter: (params) => formatState(params.value),
      },
      {
        field: "severity",
        headerName: "Severity",
        width: 150,
        minWidth: 135,
        valueFormatter: (params) => text(params.value) || "Unspecified",
      },
      {
        field: "classification",
        headerName: "Classification",
        width: 190,
        minWidth: 170,
      },
      {
        field: "source",
        headerName: "Source",
        width: 150,
        minWidth: 135,
        valueFormatter: (params) => formatSource(params.value),
      },
      {
        field: "device_count",
        headerName: "Devices",
        width: 145,
        minWidth: 130,
        filter: "agNumberColumnFilter",
        cellRenderer: (params) => (
          <Button
            size="small"
            startIcon={<DevicesRoundedIcon fontSize="small" />}
            onClick={() => openDevicesForPatch(params.data)}
            sx={{
              minWidth: 110,
              borderRadius: 999,
              px: 1.25,
              py: 0.25,
              color: MAGIC_UI.textBright,
              textTransform: "none",
              border: "1px solid rgba(148,163,184,0.3)",
              "&:hover": { borderColor: "rgba(125,211,252,0.5)", background: "rgba(125,211,252,0.1)" },
            }}
          >
            {Number(params.value || 0).toLocaleString()}
          </Button>
        ),
      },
      {
        field: "captured_at",
        headerName: "Updated",
        width: 215,
        minWidth: 190,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
    ],
    [openDevicesForPatch]
  );

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={
        <Box sx={{ display: "flex", alignItems: "flex-start", columnGap: 1, rowGap: 1, flexWrap: "wrap" }}>
          <FilterSliderBlock label="State">
            <CountSliderGroup
              options={STATE_FILTER_OPTIONS}
              activeKey={stateFilter}
              counts={stateCounts}
              onChange={setStateFilter}
            />
          </FilterSliderBlock>
          <FilterSliderBlock label="Severity">
            <CountSliderGroup
              options={SEVERITY_FILTER_OPTIONS}
              activeKey={severityFilter}
              counts={severityCounts}
              onChange={setSeverityFilter}
            />
          </FilterSliderBlock>
          {siteName ? (
            <Box
              sx={{
                display: "inline-flex",
                alignItems: "center",
                alignSelf: "flex-end",
                gap: 0.8,
                borderRadius: 999,
                border: "1px solid rgba(148,163,184,0.35)",
                background: "rgba(8,12,24,0.78)",
                px: 1.35,
                py: 0.55,
              }}
            >
              <Typography sx={{ color: "rgba(191,219,254,0.92)", fontSize: "0.82rem", fontWeight: 600 }}>
                {`Site: ${siteName}`}
              </Typography>
              <Button
                size="small"
                onClick={() => {
                  const nextParams = new URLSearchParams(searchParams);
                  nextParams.delete("site");
                  setSearchParams(nextParams, { replace: true });
                }}
                sx={{
                  minWidth: 0,
                  px: 1,
                  py: 0.15,
                  borderRadius: 999,
                  color: MAGIC_UI.textBright,
                  textTransform: "none",
                  fontSize: "0.76rem",
                  border: "1px solid rgba(148,163,184,0.28)",
                  "&:hover": { borderColor: "rgba(125,211,252,0.5)", background: "rgba(125,211,252,0.1)" },
                }}
              >
                Clear
              </Button>
            </Box>
          ) : null}
        </Box>
      }
    >
      {loadError ? (
        <Box sx={{ p: 2, color: "rgba(248,113,113,0.95)" }}>Failed to load patch audit: {loadError}</Box>
      ) : null}
      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 520,
          borderRadius: 0,
          border: "none",
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
          "& .ag-row-hover": { backgroundColor: "rgba(73,156,196,0.2) !important" },
        }}
      >
        <AgGridReact
          ref={gridRef}
          rowData={displayRows}
          columnDefs={columnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={{ mode: "singleRow", checkboxes: false, headerCheckbox: false, enableClickSelection: true }}
          suppressCellFocus
          pagination
          paginationPageSize={100}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          getRowId={(params) => String(params.data?.id || params.rowIndex)}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>
    </PageBodyFrame>
  );
}
