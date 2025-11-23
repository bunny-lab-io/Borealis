import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  IconButton,
  Stack,
  Tooltip,
} from "@mui/material";
import {
  FilterAlt as HeaderIcon,
  Cached as CachedIcon,
  Add as AddIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

const gridTheme = themeQuartz.withParams({
  accentColor: "#8b5cf6",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";

const AURORA_SHELL = {
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  text: "#e2e8f0",
  subtext: "#94a3b8",
  accent: "#7dd3fc",
};

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
  px: 2.4,
  minWidth: 126,
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 12px 34px rgba(124,58,237,0.38)",
  },
};

const AUTO_SIZE_COLUMNS = ["name", "type", "deviceCount", "site", "lastEditedBy", "lastEdited"];
const FILTER_TYPE_META = {
  global: {
    label: "Global",
    textColor: "#8fdaa2",
    backgroundColor: "rgba(56,161,105,0.16)",
    borderColor: "rgba(56,161,105,0.4)",
  },
  site: {
    label: "Site-Scoped",
    textColor: "#8ab4ff",
    backgroundColor: "rgba(125,180,255,0.16)",
    borderColor: "rgba(125,180,255,0.42)",
  },
};

const SAMPLE_ROWS = [
  {
    id: "sample-global",
    name: "Windows Workstations",
    type: "global",
    site: null,
    lastEditedBy: "System",
    lastEdited: new Date().toISOString(),
    deviceCount: 24,
  },
  {
    id: "sample-site",
    name: "West Campus Servers",
    type: "site",
    site: "West Campus",
    lastEditedBy: "Demo User",
    lastEdited: new Date(Date.now() - 1000 * 60 * 60 * 24 * 3).toISOString(),
    deviceCount: 6,
  },
];

function formatTimestamp(ts) {
  if (!ts) return "—";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString();
}

function resolveLastEditor(filter) {
  const candidate =
    filter?.last_edited_by_username ||
    filter?.last_edited_by_name ||
    filter?.last_edited_by ||
    filter?.lastEditedBy ||
    filter?.last_editor ||
    filter?.lastEditor ||
    filter?.updated_by ||
    filter?.updatedBy ||
    filter?.owner ||
    filter?.user ||
    filter?.modified_by;
  if (candidate && typeof candidate === "object") {
    if (candidate.name) return candidate.name;
    if (candidate.username) return candidate.username;
    if (candidate.user) return candidate.user;
  }
  if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  return "Unknown";
}

function FilterTypePill({ type }) {
  const key = String(type || "").toLowerCase() === "site" ? "site" : "global";
  const meta = FILTER_TYPE_META[key];
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        px: 1.1,
        py: 0.25,
        borderRadius: 8,
        minWidth: 58,
        justifyContent: "center",
        fontWeight: 600,
        fontSize: "0.72rem",
        letterSpacing: 0.2,
        color: meta.textColor,
        border: `1px solid ${meta.borderColor}`,
        backgroundColor: meta.backgroundColor,
        textTransform: "none",
      }}
    >
      {meta.label}
    </Box>
  );
}

function normalizeFilters(raw) {
  if (!Array.isArray(raw)) return [];
  return raw.map((f, idx) => ({
    id: f.id || f.filter_id || `filter-${idx}`,
    name: f.name || f.title || "Unnamed Filter",
    type: (f.site_scope || f.scope || f.type || "global") === "scoped" ? "site" : "global",
    site: f.site || f.site_scope || f.site_name || f.target_site || null,
    lastEditedBy: resolveLastEditor(f),
    lastEdited: f.last_edited || f.updated_at || f.updated || f.created_at || null,
    deviceCount:
      typeof f.matching_device_count === "number"
        ? f.matching_device_count
        : typeof f.devices_targeted === "number"
        ? f.devices_targeted
        : null,
    raw: f,
  }));
}

export default function DeviceFilterList({ onCreateFilter, onEditFilter, refreshToken }) {
  const gridRef = useRef(null);
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const loadFilters = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await fetch("/api/device_filters");
      if (resp.status === 404) {
        // Endpoint not available yet; surface sample data without hard failure
        setRows(normalizeFilters(SAMPLE_ROWS));
        setError("Device filter API not found (404) — showing sample filters.");
      } else {
        if (!resp.ok) {
          throw new Error(`Failed to load filters (${resp.status})`);
        }
        const data = await resp.json();
        const normalized = normalizeFilters(data?.filters || data);
        setRows(normalized);
      }
    } catch (err) {
      setError(err?.message || "Unable to load filters");
      setRows((prev) => (prev.length ? prev : normalizeFilters(SAMPLE_ROWS)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadFilters();
  }, [loadFilters, refreshToken]);

  const handleGridReady = useCallback((params) => {
    gridRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* no-op */
      }
    });
  }, []);

  const autoSize = useCallback(() => {
    if (!gridRef.current || loading || !rows.length) return;
    const api = gridRef.current;
    requestAnimationFrame(() => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* ignore autosize failures */
      }
    });
  }, [loading, rows.length]);

  useEffect(() => {
    autoSize();
  }, [rows, loading, autoSize]);

  const columnDefs = useMemo(() => {
    return [
      {
        headerName: "Filter Name",
        field: "name",
        minWidth: 200,
        cellRenderer: (params) => {
          const value = params.value || "Unnamed Filter";
          return (
            <Button
              onClick={() => onEditFilter?.(params.data)}
              variant="text"
              size="small"
              sx={{
                textTransform: "none",
                color: "#7dd3fc",
                fontWeight: 600,
                px: 0,
                minWidth: "unset",
                "&:hover": { color: "#a5e7ff", textDecoration: "underline" },
              }}
            >
              {value}
            </Button>
          );
        },
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Type",
        field: "type",
        minWidth: 150,
        cellRenderer: (params) => {
          const type = String(params.value || "").toLowerCase() === "site" ? "Site" : "Global";
          return <FilterTypePill type={type} />;
        },
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Devices Targeted",
        field: "deviceCount",
        width: 160,
        valueFormatter: (params) => {
          if (typeof params.value === "number" && Number.isFinite(params.value)) {
            return params.value.toLocaleString();
          }
          return "—";
        },
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Site",
        field: "site",
        minWidth: 140,
        cellRenderer: (params) => {
          const value = params.value;
          return value ? value : "—";
        },
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Last Edited By",
        field: "lastEditedBy",
        minWidth: 160,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Last Edited",
        field: "lastEdited",
        minWidth: 180,
        flex: 1,
        valueFormatter: (params) => formatTimestamp(params.value),
        cellClass: "auto-col-tight",
      },
    ];
  }, [onEditFilter]);

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      cellClass: "auto-col-tight",
      suppressMenu: true,
      cellStyle: {
        display: "flex",
        alignItems: "center",
        justifyContent: "flex-start",
        textAlign: "left",
      },
    }),
    []
  );

  return (
    <Paper
      elevation={0}
      sx={{
        height: "100vh",
        minHeight: "100vh",
        background: AURORA_SHELL.background,
        color: AURORA_SHELL.text,
        p: 3,
        borderRadius: 0,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
      }}
    >
      <Box sx={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", mb: 2.5 }}>
        <Box sx={{ display: "flex", gap: 1.5, alignItems: "flex-start" }}>
          <Box
            sx={{
              width: 36,
              height: 36,
              borderRadius: 2,
              background: "linear-gradient(135deg, rgba(125,211,252,0.28), rgba(192,132,252,0.32))",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              color: "#0f172a",
            }}
          >
            <HeaderIcon fontSize="small" />
          </Box>
          <Box>
            <Typography sx={{ fontSize: "1.35rem", fontWeight: 700, lineHeight: 1.2 }}>
              Device Filters
            </Typography>
            <Typography sx={{ color: AURORA_SHELL.subtext, mt: 0.2 }}>
              Build reusable filter definitions to target devices and assemblies without per-site duplication.
            </Typography>
          </Box>
        </Box>

        <Stack direction="row" gap={1}>
          <Tooltip title="Refresh">
            <IconButton
              aria-label="Refresh filters"
              onClick={loadFilters}
              sx={{
                color: "#a5e0ff",
                border: "1px solid rgba(148,163,184,0.4)",
                backgroundColor: "rgba(5,7,15,0.6)",
                "&:hover": { backgroundColor: "rgba(125,183,255,0.16)" },
              }}
            >
              <CachedIcon fontSize="small" />
            </IconButton>
          </Tooltip>
          <Button
            startIcon={<AddIcon />}
            variant="contained"
            onClick={() => onCreateFilter?.()}
            sx={gradientButtonSx}
          >
            New Filter
          </Button>
        </Stack>
      </Box>

      <Box
        className={gridTheme.themeName}
        sx={{
          flexGrow: 1,
          minHeight: 0,
          pb: 2,
          width: "100%",
          fontFamily: gridFontFamily,
          "& .ag-root-wrapper": { borderRadius: 0 },
          "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            textAlign: "left",
            paddingTop: "8px",
            paddingBottom: "8px",
            paddingLeft: "18px",
            paddingRight: "12px",
          },
          "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            padding: 0,
          },
          "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            textAlign: "left",
          },
          "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
            paddingLeft: "12px",
            paddingRight: "9px",
          },
          "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper": {
            justifyContent: "flex-start",
          },
          "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-value": {
            textAlign: "left",
            justifyContent: "flex-start",
          },
        }}
        style={{
          "--ag-icon-font-family": iconFontFamily,
          "--ag-background-color": "#070b1a",
          "--ag-foreground-color": "#f4f7ff",
          "--ag-header-background-color": "#0f172a",
          "--ag-header-foreground-color": "#cfe0ff",
          "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
          "--ag-row-hover-color": "rgba(125,183,255,0.08)",
          "--ag-selected-row-background-color": "rgba(64,164,255,0.18)",
          "--ag-border-color": "rgba(125,183,255,0.18)",
          "--ag-row-border-color": "rgba(125,183,255,0.14)",
          "--ag-border-radius": "0px",
          "--ag-checkbox-border-radius": "3px",
          "--ag-checkbox-background-color": "rgba(255,255,255,0.06)",
          "--ag-checkbox-border-color": "rgba(180,200,220,0.6)",
          "--ag-checkbox-checked-color": "#7dd3fc",
        }}
      >
        <AgGridReact
          rowData={rows}
          columnDefs={columnDefs}
          defaultColDef={defaultColDef}
          animateRows
          rowHeight={46}
          headerHeight={44}
          suppressCellFocus
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No device filters found.</span>"
          onGridReady={handleGridReady}
          theme={gridTheme}
          style={{ width: "100%", height: "100%", fontFamily: gridFontFamily }}
        />
      </Box>
    </Paper>
  );
}
