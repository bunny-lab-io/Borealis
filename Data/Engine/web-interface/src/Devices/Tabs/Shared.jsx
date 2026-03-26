import React from "react";
import { Box } from "@mui/material";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

export const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBg: "rgba(7,11,24,0.92)",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  glassBorder: "rgba(94, 234, 212, 0.35)",
  glow: "0 35px 80px rgba(2, 6, 23, 0.65)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
};

export const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
export const iconFontFamily = '"Quartz Regular"';

export const DEVICE_DETAILS_GRID_THEME = themeQuartz.withParams({
  accentColor: "#8b5cf6",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor",
  },
  fontFamily: {
    googleFont: "IBM Plex Sans",
  },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const gridThemeClass = DEVICE_DETAILS_GRID_THEME.themeName || "ag-theme-quartz";

const GRID_SHELL_BASE_SX = {
  width: "100%",
  borderRadius: 3,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(5,8,20,0.9)",
  boxShadow: "0 18px 45px rgba(2,6,23,0.6)",
  position: "relative",
  overflow: "hidden",
  "& .ag-root-wrapper": {
    borderRadius: 3,
    minHeight: "100%",
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: gridFontFamily,
    background: "transparent",
  },
  "& .ag-header": {
    backgroundColor: "rgba(3,7,18,0.85)",
    borderBottom: "1px solid rgba(148,163,184,0.25)",
  },
  "& .ag-header-cell-label": {
    color: "#e2e8f0",
    fontWeight: 600,
    letterSpacing: 0.3,
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
    transition: "background 0.2s ease",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.35)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(124,58,237,0.12) !important",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(56,189,248,0.16) !important",
    boxShadow: "inset 0 0 0 1px rgba(56,189,248,0.3)",
  },
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
  "& .ag-paging-panel": {
    borderTop: "1px solid rgba(148,163,184,0.2)",
    backgroundColor: "rgba(3,7,18,0.8)",
  },
};

export const DEFAULT_GRID_COL_DEF = {
  sortable: true,
  resizable: true,
  filter: "agTextColumnFilter",
  flex: 1,
  minWidth: 140,
};

export const DEVICE_GRID_STYLE = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
};

export const GridShell = ({ children, sx }) => (
  <Box className={gridThemeClass} sx={{ ...GRID_SHELL_BASE_SX, ...(sx || {}) }}>
    {children}
  </Box>
);
