import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Typography } from "@mui/material";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import { AgGridReact } from "ag-grid-react";
import { CountSliderGroup } from "../../Automation/Watchdogs/shared.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
} from "./Shared.jsx";

const STATE_FILTER_OPTIONS = [
  { key: "pending", label: "Pending" },
  { key: "installed", label: "Installed" },
];

const FILTER_LABEL_SX = {
  color: "#58a6ff",
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

const PRIMARY_REFRESH_BUTTON_SX = {
  minWidth: 184,
  minHeight: 38,
  borderRadius: 999,
  px: 2.2,
  textTransform: "none",
  fontWeight: 700,
  color: "#031525",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 18px 38px rgba(76, 110, 245, 0.18)",
  "&:hover": {
    background: "linear-gradient(135deg, #93ddff 0%, #d0a0ff 100%)",
    boxShadow: "0 22px 42px rgba(76, 110, 245, 0.24)",
  },
  "&.Mui-disabled": {
    color: "rgba(2, 6, 23, 0.45)",
    background: "linear-gradient(135deg, rgba(125,211,252,0.4) 0%, rgba(192,132,252,0.4) 100%)",
    boxShadow: "none",
  },
};

function text(value) {
  return String(value ?? "").trim();
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

function PatchStateBadge({ value }) {
  const state = text(value).toLowerCase();
  const pending = state === "pending";
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        minWidth: 78,
        borderRadius: 999,
        px: 1,
        py: 0.25,
        fontSize: 11,
        fontWeight: 700,
        color: pending ? "#fef3c7" : "#bbf7d0",
        border: pending ? "1px solid rgba(251,191,36,0.42)" : "1px solid rgba(52,211,153,0.38)",
        background: pending ? "rgba(251,191,36,0.12)" : "rgba(52,211,153,0.12)",
      }}
    >
      {formatState(value)}
    </Box>
  );
}

function boolLabel(value) {
  if (value === true) return "Yes";
  if (value === false) return "No";
  return "";
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

export default function PatchManagementTab({ hostname = "" }) {
  const gridRef = useRef(null);
  const patchRefreshTimersRef = useRef([]);
  const notifyOperator = useAppNotifications();
  const [patchRows, setPatchRows] = useState([]);
  const [loadError, setLoadError] = useState("");
  const [stateFilter, setStateFilter] = useState("pending");
  const [refreshBusy, setRefreshBusy] = useState(false);
  const normalizedHostname = useMemo(() => text(hostname), [hostname]);

  const loadPatchRows = useCallback(async () => {
    if (!normalizedHostname) {
      setPatchRows([]);
      return;
    }
    try {
      const response = await fetch(`/api/device/patches/${encodeURIComponent(normalizedHostname)}`, {
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      setPatchRows(Array.isArray(payload?.patches) ? payload.patches : []);
      setLoadError("");
    } catch (error) {
      setLoadError(String(error?.message || error));
      setPatchRows([]);
    }
  }, [normalizedHostname]);

  useEffect(() => {
    void loadPatchRows();
  }, [loadPatchRows]);

  const clearPatchRefreshTimers = useCallback(() => {
    if (typeof window === "undefined") return;
    patchRefreshTimersRef.current.forEach((timerId) => window.clearTimeout(timerId));
    patchRefreshTimersRef.current = [];
  }, []);

  const requestPatchDataRefresh = useCallback(
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

  const handleQueryPatchInventory = useCallback(async () => {
    if (!normalizedHostname || refreshBusy) return;
    setRefreshBusy(true);
    try {
      const response = await fetch(`/api/device/patches/${encodeURIComponent(normalizedHostname)}/refresh`, {
        method: "POST",
        credentials: "include",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      requestPatchDataRefresh({ burst: true });
      await notifyOperator({
        title: "Patch Refresh Requested",
        message: `Borealis queued a patch inventory query for <b>${normalizedHostname}</b>.`,
        icon: "success",
        variant: "success",
      });
    } catch (error) {
      await notifyOperator({
        title: "Patch Refresh Failed",
        message: `Borealis could not query patch inventory for <b>${normalizedHostname || "this device"}</b>: ${String(error?.message || error)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setRefreshBusy(false);
    }
  }, [normalizedHostname, notifyOperator, refreshBusy, requestPatchDataRefresh]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !normalizedHostname) return undefined;
    const expectedHost = normalizedHostname.toLowerCase();
    const handleInventoryChanged = (payload = {}) => {
      const payloadHost = text(payload?.hostname).toLowerCase();
      const payloadChange = text(payload?.change).toLowerCase();
      if (!payloadHost || payloadHost !== expectedHost) return;
      if (payloadChange && payloadChange !== "patches_updated" && payloadChange !== "updated") return;
      requestPatchDataRefresh({ burst: true });
    };
    socket.on("device_inventory_changed", handleInventoryChanged);
    return () => {
      socket.off("device_inventory_changed", handleInventoryChanged);
    };
  }, [normalizedHostname, requestPatchDataRefresh]);

  const stateCounts = useMemo(
    () =>
      patchRows.reduce(
        (counts, row) => {
          const state = text(row.state).toLowerCase();
          if (state in counts) counts[state] += 1;
          return counts;
        },
        { pending: 0, installed: 0 }
      ),
    [patchRows]
  );

  const displayRows = useMemo(
    () =>
      patchRows.filter((row) => {
        if (stateFilter && text(row.state).toLowerCase() !== stateFilter) return false;
        return true;
      }),
    [patchRows, stateFilter]
  );

  const columnDefs = useMemo(
    () => [
      {
        field: "kb",
        headerName: "KB",
        width: 135,
        minWidth: 120,
        valueGetter: (params) => text(params.data?.kb) || "No KB",
      },
      {
        field: "title",
        headerName: "Title",
        flex: 1.5,
        minWidth: 300,
        tooltipField: "title",
      },
      {
        field: "state",
        headerName: "State",
        width: 135,
        minWidth: 125,
        cellRenderer: (params) => <PatchStateBadge value={params.value} />,
      },
      {
        field: "severity",
        headerName: "Severity",
        width: 145,
        minWidth: 130,
        valueFormatter: (params) => text(params.value) || "Unspecified",
      },
      {
        field: "classification",
        headerName: "Classification",
        width: 185,
        minWidth: 165,
      },
      {
        field: "source",
        headerName: "Source",
        width: 150,
        minWidth: 135,
        valueFormatter: (params) => formatSource(params.value),
      },
      {
        field: "is_downloaded",
        headerName: "Downloaded",
        width: 135,
        minWidth: 120,
        valueFormatter: (params) => boolLabel(params.value),
      },
      {
        field: "requires_reboot",
        headerName: "Reboot",
        width: 115,
        minWidth: 105,
        valueFormatter: (params) => boolLabel(params.value),
      },
      {
        field: "published_at",
        headerName: "Published",
        width: 205,
        minWidth: 185,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: "installed_on",
        headerName: "Installed",
        width: 205,
        minWidth: 185,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: "captured_at",
        headerName: "Captured",
        width: 205,
        minWidth: 185,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
    ],
    []
  );

  return (
    <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0, gap: 1.5 }}>
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1.5, flexWrap: "wrap" }}>
        <FilterSliderBlock label="State">
          <CountSliderGroup
            options={STATE_FILTER_OPTIONS}
            activeKey={stateFilter}
            counts={stateCounts}
            onChange={setStateFilter}
          />
        </FilterSliderBlock>
        <Button
          startIcon={<RefreshRoundedIcon />}
          disabled={!normalizedHostname || refreshBusy}
          onClick={handleQueryPatchInventory}
          sx={PRIMARY_REFRESH_BUTTON_SX}
        >
          {refreshBusy ? "Querying..." : "Query Patch Inventory"}
        </Button>
      </Box>
      {loadError ? (
        <Box sx={{ color: "rgba(248,113,113,0.95)", fontSize: "0.84rem" }}>
          Failed to load patch inventory: {loadError}
        </Box>
      ) : null}
      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 500,
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
          getRowId={(params) => String(params.data?.patch_key || params.data?.id || params.rowIndex)}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>
    </Box>
  );
}
