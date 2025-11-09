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
 *  Borealis MagicUI — Page Template
 *  ---------------------------------------------------------------------------
 *  PURPOSE
 *    • A *visual-only* reference for building Borealis pages.
 *    • NO API calls, NO business logic. Everything is placeholder/demo.
 *    • Use this as a baseline for new pages to ensure perfect visual parity.
 *
 *  WHAT THIS TEMPLATE SHOWS
 *    1) Full-bleed gradient shell using the Borealis Aurora pattern.
 *    2) Page header with Material icon to the LEFT of the title.
 *    3) Subtitle directly beneath the title.
 *    4) Top-right utility buttons (e.g., Refresh).
 *    5) AG Grid using the Quartz theme with rounded corners, sorting, filtering,
 *       pagination, and example data/columns.
 *    6) Gradient buttons consistent with MagicUI accents.
 *
 *  DOs
 *    ✓ Keep pages full-bleed to their parent container (no gutters on the Paper).
 *    ✓ Use the same Aurora gradient shell across pages.
 *    ✓ Use IBM Plex Sans for a consistent typographic tone.
 *    ✓ Keep header icon SMALL (around 20–24px) and aligned with the title baseline.
 *    ✓ Put a concise subtitle under the page title to orient the user.
 *    ✓ Prefer rounded corners (8px) for grid chrome and panels.
 *    ✓ Use AG Grid Quartz theme and scope theme CSS vars on the wrapper.
 *    ✓ Use gradient-filled primary buttons for key actions.
 *
 *  DON'Ts
 *    ✗ Do not introduce API/data fetching in this template (copy into real pages).
 *    ✗ Do not change global AG Grid CSS (keep theme-scoped).
 *    ✗ Do not mix multiple font families; stick to IBM Plex Sans.
 *    ✗ Do not leave empty margins around the page shell.
 *    ✗ Do not hardcode fixed heights unless absolutely necessary.
 *
 *  HOW TO ADAPT THIS TEMPLATE
 *    • Replace the static sample rows/columns with your real grid model.
 *    • Wire the Refresh button to your data loader (keep the icon + placement).
 *    • Keep the theme params/vars to maintain cross-page visual cohesion.
 *
 *  REFERENCES
 *    • See other pages for style parity (aurora shell, Quartz theme usage,
 *      rounded grid edges, and gradient buttons).
 * ============================================================================
 */

// -----------------------------------------------------------------------------
//  AG Grid module registration (community only, consistent with other pages)
// -----------------------------------------------------------------------------
ModuleRegistry.registerModules([AllCommunityModule]);

// -----------------------------------------------------------------------------
//  MagicUI x Quartz Theme
//  Keep these params consistent across pages for color/typography parity.
// -----------------------------------------------------------------------------
const gridTheme = themeQuartz.withParams({
  accentColor: "#8b5cf6",           // Indigo/Violet accent (matches MagicUI)
  backgroundColor: "#070b1a",       // Deep navy panel background
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";

// -----------------------------------------------------------------------------
/** Aurora gradient shell colors — identical across pages for cohesion. */
// -----------------------------------------------------------------------------
const AURORA_SHELL = {
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  text: "#e2e8f0",
  subtext: "#9ba3b4",
  accent: "#7dd3fc",
};

// -----------------------------------------------------------------------------
//  Gradient button style — use for primary CTAs to feel 'alive' and branded.
// -----------------------------------------------------------------------------
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

// -----------------------------------------------------------------------------
//  Example grid data — placeholder only. Replace with real rows on real pages.
// -----------------------------------------------------------------------------
const SAMPLE_ROWS = [
  { id: "ROW-001", category: "Example", name: "Gemini Borealis", owner: "alice", updated: "2025-06-12 10:32" },
  { id: "ROW-002", category: "Demo",    name: "Aurora Runner",   owner: "bob",   updated: "2025-07-01 14:05" },
  { id: "ROW-003", category: "Sample",  name: "Quartz Tables",   owner: "carol", updated: "2025-08-20 09:18" },
  { id: "ROW-004", category: "Guide",   name: "MagicUI Rules",   owner: "dave",  updated: "2025-09-03 16:41" },
  { id: "ROW-005", category: "Pattern", name: "Borealis Blue",   owner: "erin",  updated: "2025-10-11 08:27" },
];

// -----------------------------------------------------------------------------
//  Column definitions — keep sorting/filtering enabled; rounded edges come from
//  the theme vars on the wrapper element below.
// -----------------------------------------------------------------------------
const sampleColumnDefs = [
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

const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";

// -----------------------------------------------------------------------------
//  Page Template Component
// -----------------------------------------------------------------------------
export default function PageTemplate() {
  const gridRef = useRef(null);

  // NOTE: No data fetching, no side effects — this is a pure visual template.
  const columnDefs = useMemo(() => sampleColumnDefs, []);

  const handleRefresh = () => {
    // In real pages, call your data loader. Here we do nothing (visual only).
    // Keeping the icon + placement ensures consistent muscle memory.
    // eslint-disable-next-line no-console
    console.log("Refresh clicked (template; no-op).");
  };

  return (
    <Paper
      sx={{
        // Full-bleed: no margins/padding on the shell Paper
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
      {/* Page header */}
      <Box sx={{ px: 3, pt: 3, pb: 1 }}>
        <Stack direction="row" alignItems="center" spacing={1.25}>
          <TemplateIcon sx={{ fontSize: 22, color: AURORA_SHELL.accent }} />
          <Typography variant="h6" sx={{ fontWeight: 700, letterSpacing: 0.5 }}>
            Page Template
          </Typography>
          <Box sx={{ flexGrow: 1 }} />
          {/* Top-right controls: keep order + sizes consistent project-wide */}
          <Stack direction="row" spacing={1}>
            <Tooltip title="Refresh">
              <span>
                <IconButton
                  size="small"
                  onClick={handleRefresh}
                  sx={{
                    color: "#cbd5e1",
                    borderRadius: 1,
                    "&:hover": { color: "#ffffff" },
                  }}
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

        {/* Subtitle directly under title */}
        <Typography variant="body2" sx={{ color: AURORA_SHELL.subtext, mt: 0.75 }}>
          Page Styling Guide and Template - Use as a baseline when designing new pages.
        </Typography>
      </Box>

      {/* Content area — the grid lives in a chromeless card that still keeps rounded corners */}
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

            // Soften cell height a bit for comfort
            "& .ag-cell": {
              display: "flex",
              alignItems: "center",
              paddingTop: "8px",
              paddingBottom: "8px",
            },
          }}
          style={{
            // Theme-scoped CSS variables ensure rounded corners + palette
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
