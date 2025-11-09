import React, { useMemo, useRef } from "react";
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
  Dashboard as TemplateIcon,
  Cached as RefreshIcon,
  Add as AddIcon,
  Tune as TuneIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

/**
 * ============================================================================
 *  Borealis MagicUI — Page Template (Strict Checkbox Fix)
 *  ---------------------------------------------------------------------------
 *  Fixes:
 *   • Ensures AG Grid uses the Quartz icon font family (agGridQuartz).
 *   • Forces the checkbox pseudo-element to use the icon font to avoid any
 *     global `*::before`/`*::after { font-family: ... }` overrides.
 *   • Centers the selection column content in both header and body.
 * ============================================================================
 */

ModuleRegistry.registerModules([AllCommunityModule]);

const gridTheme = themeQuartz.withParams({
  accentColor: "#8b5cf6",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";

const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
// IMPORTANT: use the *actual* Quartz icon font family
const iconFontFamily = "agGridQuartz";

const AURORA_SHELL = {
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  text: "#e2e8f0",
  subtext: "#9ba3b4",
  accent: "#7dd3fc",
};

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 12px 34px rgba(124,58,237,0.38)",
    filter: "none",
  },
};

const SAMPLE_ROWS = [
  { id: "ROW-001", category: "Example", name: "Gemini Borealis", owner: "alice", updated: "2025-06-12 10:32" },
  { id: "ROW-002", category: "Demo",    name: "Aurora Runner",   owner: "bob",   updated: "2025-07-01 14:05" },
  { id: "ROW-003", category: "Sample",  name: "Quartz Tables",   owner: "carol", updated: "2025-08-20 09:18" },
  { id: "ROW-004", category: "Guide",   name: "MagicUI Rules",   owner: "dave",  updated: "2025-09-03 16:41" },
  { id: "ROW-005", category: "Pattern", name: "Borealis Blue",   owner: "erin",  updated: "2025-10-11 08:27" },
];

const selectionCol = {
  headerName: "",
  field: "__select__",
  width: 52,
  maxWidth: 52,
  checkboxSelection: true,
  headerCheckboxSelection: true,
  resizable: false,
  sortable: false,
  suppressMenu: true,
  filter: false,
  pinned: "left",
  lockPosition: true,
};

const sampleColumnDefs = [
  selectionCol,
  { headerName: "ID",        field: "id",       minWidth: 140, sortable: true, filter: "agTextColumnFilter" },
  { headerName: "Category",  field: "category", minWidth: 140, sortable: true, filter: "agTextColumnFilter" },
  { headerName: "Name",      field: "name",     minWidth: 220, sortable: true, filter: "agTextColumnFilter" },
  { headerName: "Owner",     field: "owner",    minWidth: 140, sortable: true, filter: "agTextColumnFilter" },
  { headerName: "Updated",   field: "updated",  minWidth: 180, sortable: true, filter: "agTextColumnFilter" },
];

const defaultColDef = {
  sortable: true,
  filter: "agTextColumnFilter",
  resizable: true,
  minWidth: 140,
};

export default function PageTemplate() {
  const gridRef = useRef(null);
  const columnDefs = useMemo(() => sampleColumnDefs, []);

  const handleRefresh = () => {
    console.log("Refresh clicked (template; no-op).");
  };

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        background: AURORA_SHELL.background,
        border: "none",
        boxShadow: "none",
        borderRadius: 0,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
        color: AURORA_SHELL.text,
        fontFamily: gridFontFamily,
      }}
      elevation={0}
    >
      <Box sx={{ px: 3, pt: 3, pb: 1 }}>
        <Stack direction="row" alignItems="center" spacing={1.25}>
          <TemplateIcon sx={{ fontSize: 22, color: AURORA_SHELL.accent }} />
          <Typography variant="h6" sx={{ fontWeight: 700, letterSpacing: 0.5 }}>
            Page Template
          </Typography>
          <Box sx={{ flexGrow: 1 }} />
          <Stack direction="row" spacing={1}>
            <Tooltip title="Refresh">
              <span>
                <IconButton size="small" onClick={handleRefresh}
                  sx={{ color: "#cbd5e1", borderRadius: 1, "&:hover": { color: "#ffffff" } }}
                >
                  <RefreshIcon fontSize="small" />
                </IconButton>
              </span>
            </Tooltip>
            <Tooltip title="New (example)">
              <span>
                <Button size="small" startIcon={<AddIcon />} sx={gradientButtonSx}>
                  New Item
                </Button>
              </span>
            </Tooltip>
            <Tooltip title="Settings (example)">
              <span>
                <Button size="small" variant="outlined" startIcon={<TuneIcon />}
                  sx={{
                    borderColor: "rgba(148,163,184,0.35)",
                    color: "#e2e8f0",
                    textTransform: "none",
                    borderRadius: 999,
                    "&:hover": { borderColor: "rgba(148,163,184,0.55)" },
                  }}
                >
                  Settings
                </Button>
              </span>
            </Tooltip>
          </Stack>
        </Stack>
        <Typography variant="body2" sx={{ color: AURORA_SHELL.subtext, mt: 0.75 }}>
          Page Styling Guide and Template - Use as a baseline when designing new pages.
        </Typography>
      </Box>

      <Box sx={{ mt: "10px", px: 2, pb: 2, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <Box
          className={themeClassName}
          sx={{
            background: "transparent",
            borderRadius: 0,
            border: "none",
            boxShadow: "none",
            p: 0,
            width: "100%",
            flexGrow: 1,
            minHeight: 0,
            height: "100%",
            position: "relative",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,

            /* Center selection column */
            "& .ag-pinned-left-cols-container .ag-cell": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              paddingTop: 0,
              paddingBottom: 0,
            },
            "& .ag-header .ag-header-select-all, & .ag-header .ag-checkbox-input-wrapper": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            },
            "& .ag-selection-checkbox .ag-cell-wrapper, & .ag-header-select-all .ag-cell-wrapper": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              width: "100%",
              height: "100%",
            },
            /* Force Quartz icon font on the checkbox pseudo-element */
            "& .ag-checkbox-input-wrapper::before, & .ag-checkbox-input-wrapper::after": {
              fontFamily: "var(--ag-icon-font-family) !important",
            },
            /* Make the box look square and slightly larger */
            "& .ag-checkbox-input-wrapper::before": {
              borderRadius: "3px",
              transform: "scale(1.06)",
            },
          }}
          style={{
            "--ag-background-color": "#070b1a",
            "--ag-foreground-color": "#f4f7ff",
            "--ag-header-background-color": "#0f172a",
            "--ag-header-foreground-color": "#cfe0ff",
            "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
            "--ag-row-hover-color": "rgba(125,183,255,0.08)",
            "--ag-selected-row-background-color": "rgba(64,164,255,0.18)",
            "--ag-border-color": "rgba(125,183,255,0.18)",
            "--ag-row-border-color": "rgba(125,183,255,0.14)",
            "--ag-border-radius": "8px",
          }}
        >
          <AgGridReact
            ref={gridRef}
            rowData={SAMPLE_ROWS}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            rowSelection="multiple"
            rowMultiSelectWithClick
            pagination
            paginationPageSize={5}
            animateRows
            theme={gridTheme}
            rowHeight={44}
            style={{
              width: "100%",
              height: "100%",
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
            }}
          />
        </Box>
      </Box>
    </Paper>
  );
}
