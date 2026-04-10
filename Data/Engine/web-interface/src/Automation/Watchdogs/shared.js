import { themeQuartz } from "ag-grid-community";

export const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
export const iconFontFamily = "'Quartz Regular'";
export const NAV_TAB_HEIGHT = 32;
export const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

export const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});

export const GRID_WRAPPER_SX = {
  width: "100%",
  height: "100%",
  minHeight: 520,
  fontFamily: gridFontFamily,
  "--ag-font-family": gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "& .ag-root-wrapper": {
    minHeight: "100%",
    border: "none",
    borderRadius: 0,
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: gridFontFamily,
  },
  "& .ag-header": {
    backgroundColor: "rgba(15,23,42,0.9)",
    borderBottom: "1px solid rgba(148,163,184,0.25)",
  },
  "& .ag-header-cell-label": {
    color: "#e2e8f0",
    fontWeight: 600,
    letterSpacing: 0.3,
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    padding: "8px 12px 8px 18px",
    color: "#e2e8f0",
    fontSize: "0.88rem",
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
    transition: "background 0.2s ease",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.45)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.2) !important",
  },
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
};

export const buildNavTabsSx = (minHeight = NAV_TAB_HEIGHT) => ({
  borderBottom: "1px solid rgba(148, 163, 184, 0.16)",
  minHeight,
  height: minHeight,
  "& .MuiTabs-flexContainer": {
    minHeight,
    height: minHeight,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    textTransform: "none",
    fontWeight: 400,
    fontFamily: "inherit",
    fontSize: "0.8rem",
    minHeight,
    height: minHeight,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "&:hover": {
      background: NAV_TAB_COLORS.hover,
    },
    "&:active": {
      transform: "translateY(0.5px)",
    },
  },
  "& .MuiTab-root.Mui-selected": {
    color: NAV_TAB_COLORS.textActive,
    fontWeight: 600,
    background: NAV_TAB_COLORS.activeBg,
    "&:hover": {
      background: NAV_TAB_COLORS.activeBg,
    },
  },
});

export function formatTimestamp(value) {
  if (!value) return "";
  const numeric = Number(value);
  const date = Number.isFinite(numeric) && numeric > 0 ? new Date(numeric * 1000) : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}

export function siteModeSummary(record) {
  const siteNames = Array.isArray(record?.site_names) ? record.site_names.filter(Boolean) : [];
  const siteMode = String(record?.site_mode || "global").toLowerCase();
  const label =
    siteMode === "specific_sites"
      ? "Specific Sites"
      : siteMode === "global_exclusions"
        ? "Global w/ Exclusions"
        : "Global";
  if (!siteNames.length) return label;
  return `${label}: ${siteNames.join(", ")}`;
}

export function severityColor(severity) {
  const normalized = String(severity || "warning").toLowerCase();
  if (normalized === "error") return "#fca5a5";
  if (normalized === "info") return "#93c5fd";
  return "#fcd34d";
}

export function summarizeRuleResults(sample) {
  const entries = Array.isArray(sample?.results) ? sample.results : [];
  return entries
    .map((entry) => String(entry?.summary || "").trim())
    .filter(Boolean)
    .join(" | ");
}
