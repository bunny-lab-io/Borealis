import React, { useMemo, useRef, useCallback, useEffect } from "react";
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
  Cached as CachedIcon,
  Add as AddIcon,
  Tune as TuneIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

/**
 * ============================================================================
 *  Borealis MagicUI — Page Template (v2, with fixed square checkboxes)
 *  ---------------------------------------------------------------------------
 *  PURPOSE
 *    • A *visual-only* reference for building Borealis pages.
 *    • NO API calls, NO business logic. Everything is placeholder/demo.
 *    • Use this as a baseline for new pages to ensure perfect visual parity.
 *
 *  WHAT THIS TEMPLATE SHOWS
 *    1) Full-bleed aurora gradient shell using the Borealis pattern.
 *    2) Page header:
 *       - Material icon to the LEFT of the title (small, ~22px).
 *       - Title styled to match platform typography.
 *       - Subtitle directly beneath the title (muted color, smaller size).
 *       - Top-right utility buttons (e.g., Refresh, New, Settings).
 *    3) AG Grid using the Quartz theme with rounded corners, sorting, filtering,
 *       pagination, and example data/columns.
 *    4) Gradient-filled primary buttons consistent with MagicUI accents.
 *    5) **Selection column** with header “Select All” checkbox. Uses AG Grid’s
 *       built-in checkboxSelection + headerCheckboxSelection to ensure keyboard
 *       support and correct row-selection semantics.
 *
 *  DOs
 *    ✓ Keep pages full-bleed to their parent container (no gutters on the Paper).
 *    ✓ Use the same Aurora gradient shell across pages for cohesion.
 *    ✓ Use IBM Plex Sans for a consistent typographic tone.
 *    ✓ Keep header icon SMALL (around 20–24px) and aligned with the title baseline.
 *    ✓ Place a concise subtitle under the page title to orient the user.
 *    ✓ Prefer rounded corners (8px) for grid chrome and panels.
 *    ✓ Use AG Grid Quartz theme and scope theme CSS vars on the wrapper element.
 *    ✓ Use gradient-filled primary buttons for key actions.
 *    ✓ Keep column defs simple: sorting + text filters enabled by default.
 *
 *  DON'Ts
 *    ✗ Do not introduce API/data fetching in this template (copy into real pages).
 *    ✗ Do not change global AG Grid CSS (keep theme-scoped via the wrapper).
 *    ✗ Do not mix multiple font families; stick to IBM Plex Sans.
 *    ✗ Do not leave empty margins around the page shell.
 *    ✗ Do not hardcode fixed heights unless absolutely necessary.
 *
 *  HOW TO ADAPT THIS TEMPLATE
 *    • Replace the static sample rows/columns with your real grid model.
 *    • Wire the Refresh button to your data loader (keep the icon + placement).
 *    • Keep the theme params/vars to maintain cross-page visual cohesion.
 *    • Add/Remove columns by editing sampleColumnDefs below (defaults are sensible).
 *
 *  REQUIREMENT FOR BULK ACTION TABLES
 *    • When the page allows multi-edit/delete, include the selection column
 *      exactly as implemented below (matches Device_List.jsx):
 *        - Selection column FIRST and **pinned left**.
 *        - `checkboxSelection: true` on the col, `rowSelection: "multiple"` on the grid.
 *        - `headerCheckboxSelection: true` to enable “Select All” in the header.
 *        - Fixed width ~52px, not resizable, no menu, not sortable.
 *        - Header + row checkboxes are **SQUARE** and **HORIZONTALLY CENTERED**.
 *    • We keep square checkboxes by setting AG Grid theme variables:
 *        --ag-checkbox-border-radius: "3px"
 *      (No custom renderers are needed; this preserves accessibility and behavior.)
 *
 *  TITLE/SUBTITLE SPACING (MUST KEEP)
 *    • Title + subtitle block must keep padding: top 24px, left/right 24px (>= md: 24px).
 *      This ensures consistent alignment with the global layout.
 *
 *  VISUAL TOKENS (KEEP CONSISTENT ACROSS PAGES)
 *    • AURORA_SHELL.background — shared gradient.
 *    • AURORA_SHELL.text       — primary text color.
 *    • AURORA_SHELL.subtext    — subtitle/muted copy color.
 *    • Gradient buttons: `gradientButtonSx` (primary CTA look-and-feel).
 *
 *  HEADER CHECKBOX NUDGE
    • Some builds visually render the header checkbox ~1–2px left of center due to
      icon font metrics. We apply a tiny `transform: translateX(2px)` on the header
      checkbox wrapper for optical centering.

  NOTES ON SELECT-ALL RELIABILITY
 *    • Using AG Grid built-ins for selection (no custom header renderer) ensures
 *      the header checkbox correctly toggles rows and tracks indeterminate state.
 *    • We also provide a stable `getRowId` in case pages grow to use server-side
 *      updates; header selection relies on stable row identity.
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
  accentColor: "#8b5cf6",
  backgroundColor: "#070b1a",
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
  px: 2.6,            // wider for label
  minWidth: 116,      // ensure comfortable width for "New Item"
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
  // extra fake rows
  { id: "ROW-006", category: "Demo",    name: "Nebula Sync",     owner: "frank", updated: "2025-10-22 12:45" },
  { id: "ROW-007", category: "Example", name: "Polar Night",     owner: "gina",  updated: "2025-10-24 09:13" },
  { id: "ROW-008", category: "Sample",  name: "Crystal Fields",  owner: "henry", updated: "2025-10-26 17:01" },
  { id: "ROW-009", category: "Pattern", name: "Iris Drift",      owner: "ivy",   updated: "2025-10-27 08:09" },
  { id: "ROW-010", category: "Guide",   name: "Lumen Trails",    owner: "jack",  updated: "2025-10-28 13:20" },
  { id: "ROW-011", category: "Demo",    name: "Prism Forge",     owner: "kate",  updated: "2025-10-29 07:55" },
  { id: "ROW-012", category: "Sample",  name: "Halo Runner",     owner: "leo",   updated: "2025-10-30 18:22" },
  { id: "ROW-013", category: "Pattern", name: "Orchid Tide",     owner: "maya",  updated: "2025-11-01 10:14" },
  { id: "ROW-014", category: "Guide",   name: "Violet Peaks",    owner: "nina",  updated: "2025-11-02 15:37" },
  { id: "ROW-015", category: "Example", name: "Zephyr Quartz",   owner: "omar",  updated: "2025-11-03 11:48" },
];

// -----------------------------------------------------------------------------
//  Selection column (AG Grid built-in) — REQUIRED for bulk multi-edit/delete.
//  Mirrors Device_List.jsx: pinned left, header checkbox, fixed width.
// -----------------------------------------------------------------------------
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

// -----------------------------------------------------------------------------
//  Column definitions — keep sorting/filtering enabled; rounded edges come from
//  the theme vars on the wrapper element below.
// -----------------------------------------------------------------------------
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

const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";

// -----------------------------------------------------------------------------
//  Page Template Component
// -----------------------------------------------------------------------------
export default function PageTemplate({ onPageMetaChange }) {
  const gridRef = useRef(null);
  const columnDefs = useMemo(() => sampleColumnDefs, []);
  const getRowId = useCallback((params) => params.data?.id || String(params.rowIndex ?? ""), []);

  const handleRefresh = () => {
    console.log("Refresh clicked (template; no-op).");
  };

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Page Template",
      page_subtitle: "Page Styling Guide and Template — use as a baseline when designing new pages.",
      page_icon: TemplateIcon,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange]);

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        background: "transparent",
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
      <Box
        sx={{
          px: 2,
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1,
          mt: 1,
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.5 }}>
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
                <CachedIcon fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
        </Box>
        <Stack direction="row" spacing={1}>
          <Tooltip title="New (example)">
            <span>
              <Button size="small" startIcon={<AddIcon />} sx={gradientButtonSx}>
                New Item
              </Button>
            </span>
          </Tooltip>
          <Tooltip title="Settings (example)">
            <span>
              <Button
                size="small"
                variant="outlined"
                startIcon={<TuneIcon />}
                sx={{
                  borderColor: "rgba(148,163,184,0.35)",
                  color: "#e2e8f0",
                  textTransform: "none",
                  borderRadius: 999,
                  px: 1.7, // ~5% wider than previous
                  minWidth: 86,
                  "&:hover": { borderColor: "rgba(148,163,184,0.55)" },
                }}
              >
                Settings
              </Button>
            </span>
          </Tooltip>
        </Stack>
      </Box>

      {/* Content area — offset a little below the shared header */}
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

            "& .ag-cell": {
              display: "flex",
              alignItems: "center",
              paddingTop: "8px",
              paddingBottom: "8px",
            },

            /* Center the selection column (header + body) – class-based to beat defaults */
            "& .ag-header .ag-header-select-all, & .ag-header .ag-checkbox-input-wrapper": {
            /* Absolute centering for selection column (CSS fallback) */
            "& .ag-cell.ag-selection-centered": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              padding: 0,
            },
            "& .ag-cell.ag-selection-centered .ag-cell-wrapper": {
              gap: "0 !important",
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              padding: 0,
            },
            "& .ag-cell.ag-selection-centered .ag-selection-checkbox, & .ag-cell.ag-selection-centered .ag-checkbox-input-wrapper": {
              margin: "0 auto",
            },

              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            },
            "& .ag-cell.ag-selection-centered": {
              paddingLeft: 0,
              paddingRight: 0,
            },
            "& .ag-cell.ag-selection-centered .ag-cell-wrapper": {
              gap: "0 !important",
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              paddingTop: 0,
              paddingBottom: 0,
            },
            "& .ag-cell.ag-selection-centered .ag-selection-checkbox, & .ag-cell.ag-selection-centered .ag-checkbox-input-wrapper": {
              margin: "0 auto",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            },
            
            "& .ag-cell.ag-selection-centered .ag-selection-checkbox": {
              display: "flex !important",
              width: "100% !important",
              justifyContent: "center !important",
              margin: "0 !important",
            },
            "& .ag-cell.ag-selection-centered .ag-checkbox-input-wrapper": {
              margin: "0 !important",
            },

            "& .ag-center-cols-container .ag-cell.ag-selection-centered, & .ag-pinned-left-cols-container .ag-cell.ag-selection-centered": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
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
            "--ag-checkbox-border-radius": "3px",
            "--ag-checkbox-background-color": "rgba(255,255,255,0.06)",
            "--ag-checkbox-border-color": "rgba(180,200,220,0.6)",
            "--ag-checkbox-checked-color": "#7dd3fc",
          }}
        >
          <AgGridReact
            ref={gridRef}
            rowData={SAMPLE_ROWS}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            rowSelection="multiple"
            rowMultiSelectWithClick
            isRowSelectable={() => true}
            getRowId={getRowId}
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
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
