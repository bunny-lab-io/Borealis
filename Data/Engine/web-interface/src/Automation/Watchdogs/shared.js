import { Box } from "@mui/material";
import { themeQuartz } from "ag-grid-community";

export const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
export const iconFontFamily = "'Quartz Regular'";
export const BOREALIS_BLUE = "#58a6ff";
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

export function CountSliderGroup({ options, activeKey, counts, onChange }) {
  const entries = Array.isArray(options) ? options : [];
  const countMap = counts && typeof counts === "object" ? counts : {};
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.75,
        background: "linear-gradient(120deg, rgba(8,12,24,0.92), rgba(4,7,17,0.85))",
        borderRadius: 999,
        border: "1px solid rgba(148,163,184,0.35)",
        boxShadow: "0 18px 48px rgba(2,8,23,0.45)",
        padding: "4px",
      }}
    >
      {entries.map((option) => {
        const active = activeKey === option.key;
        return (
          <Box
            key={option.key}
            component="button"
            type="button"
            aria-pressed={active}
            onClick={() => onChange?.(option.key)}
            sx={{
              border: "none",
              outline: "none",
              background: active ? "linear-gradient(135deg,#7dd3fc,#c084fc)" : "transparent",
              color: active ? "#041224" : "#cbd5e1",
              fontWeight: 600,
              fontSize: 13,
              px: 2,
              py: 0.5,
              borderRadius: 999,
              cursor: "pointer",
              display: "inline-flex",
              alignItems: "center",
              gap: 0.6,
              boxShadow: active ? "0 0 18px rgba(125,211,252,0.35)" : "none",
              transition: "all 0.2s ease",
            }}
          >
            <Box component="span" sx={{ userSelect: "none" }}>
              {option.label}
            </Box>
            <Box
              component="span"
              sx={{
                minWidth: 28,
                textAlign: "center",
                borderRadius: 999,
                fontSize: 12,
                fontWeight: 600,
                px: 0.75,
                py: 0.1,
                color: active ? "#041224" : "#94a3b8",
                backgroundColor: active ? "rgba(4,18,36,0.2)" : "rgba(15,23,42,0.65)",
                border: active ? "1px solid rgba(4,18,36,0.3)" : "1px solid rgba(148,163,184,0.3)",
              }}
            >
              {countMap?.[option.key] ?? 0}
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}
