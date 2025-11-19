import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  IconButton,
  Stack,
  Tooltip,
  Chip,
} from "@mui/material";
import {
  FilterAlt as HeaderIcon,
  Cached as CachedIcon,
  Add as AddIcon,
  OpenInNew as DetailsIcon,
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

const AUTO_SIZE_COLUMNS = ["name", "type", "site", "lastEditedBy", "lastEdited"];

const SAMPLE_ROWS = [
  {
    id: "sample-global",
    name: "Windows Workstations",
    type: "global",
    site: null,
    lastEditedBy: "System",
    lastEdited: new Date().toISOString(),
  },
  {
    id: "sample-site",
    name: "West Campus Servers",
    type: "site",
    site: "West Campus",
    lastEditedBy: "Demo User",
    lastEdited: new Date(Date.now() - 1000 * 60 * 60 * 24 * 3).toISOString(),
  },
];

function formatTimestamp(ts) {
  if (!ts) return "—";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString();
}

function normalizeFilters(raw) {
  if (!Array.isArray(raw)) return [];
  return raw.map((f, idx) => ({
    id: f.id || f.filter_id || `filter-${idx}`,
    name: f.name || f.title || "Unnamed Filter",
    type: (f.site_scope || f.scope || f.type || "global") === "scoped" ? "site" : "global",
    site: f.site || f.site_scope || f.site_name || f.target_site || null,
    lastEditedBy: f.last_edited_by || f.owner || f.updated_by || "Unknown",
    lastEdited: f.last_edited || f.updated_at || f.updated || f.created_at || null,
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
        width: 120,
        cellRenderer: (params) => {
          const type = String(params.value || "").toLowerCase() === "site" ? "Site" : "Global";
          const color = type === "Global" ? "success" : "info";
          return <Chip size="small" label={type} color={color} sx={{ fontSize: "0.75rem" }} />;
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
        valueFormatter: (params) => formatTimestamp(params.value),
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Details",
        field: "details",
        width: 120,
        minWidth: 140,
        flex: 1,
        cellRenderer: (params) => (
          <IconButton
            aria-label="Open filter details"
            size="small"
            onClick={() => onEditFilter?.(params.data)}
            sx={{
              color: "#7dd3fc",
              border: "1px solid rgba(148,163,184,0.4)",
              borderRadius: 1.5,
              backgroundColor: "rgba(255,255,255,0.03)",
              "&:hover": { backgroundColor: "rgba(125,183,255,0.12)" },
            }}
          >
            <DetailsIcon fontSize="inherit" />
          </IconButton>
        ),
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
    }),
    []
  );

  return (
    <Paper
      elevation={0}
      sx={{
        minHeight: "100vh",
        background: AURORA_SHELL.background,
        color: AURORA_SHELL.text,
        p: 3,
        borderRadius: 0,
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
        sx={{
          background: "rgba(10,16,31,0.85)",
          border: "1px solid rgba(148,163,184,0.3)",
          borderRadius: 2,
          boxShadow: "0 18px 38px rgba(3,7,18,0.65)",
          overflow: "hidden",
        }}
      >
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            px: 2,
            py: 1.25,
            borderBottom: "1px solid rgba(148,163,184,0.2)",
            background: "linear-gradient(90deg, rgba(148,163,184,0.08), rgba(148,163,184,0.04))",
          }}
        >
          <Typography sx={{ color: "#e2e8f0", fontWeight: 600 }}>Filters</Typography>
          <Typography sx={{ color: "rgba(226,232,240,0.7)", fontSize: "0.9rem" }}>
            {loading ? "Loading…" : `${rows.length} filter${rows.length === 1 ? "" : "s"}`}
          </Typography>
        </Box>

        {error ? (
          <Box sx={{ px: 2, py: 1.5, color: "#ffb4b4", borderBottom: "1px solid rgba(255,179,179,0.4)" }}>
            {error}
          </Box>
        ) : null}

        <Box
          className={gridTheme.themeName}
          sx={{
            height: "calc(100vh - 220px)",
            "& .ag-root-wrapper": { borderRadius: 0 },
            "& .ag-cell.auto-col-tight": { paddingLeft: 8, paddingRight: 6 },
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
            overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No device filters found.</span>"
            onGridReady={handleGridReady}
            theme={gridTheme}
            style={{ width: "100%", height: "100%", fontFamily: gridFontFamily }}
          />
        </Box>
      </Box>
    </Paper>
  );
}
