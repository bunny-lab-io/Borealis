import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Autocomplete,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import {
  Cached as RefreshIcon,
  DeveloperBoardRounded as ServerIcon,
  GitHub as GitHubIcon,
  InfoOutlined as InfoIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { themeQuartz } from "ag-grid-community";

import { CreditsDialog } from "../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_MAGIC_UI,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../app/providers/AuthContext.jsx";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_GRID_STYLE,
  MAGIC_UI,
  gridFontFamily,
  iconFontFamily,
} from "../Devices/Tabs/Shared.jsx";

const PAGE_ICON = ServerIcon;
const DEFAULT_POLL_INTERVAL_MS = 30 * 1000;
const BOOSTED_POLL_INTERVAL_MS = 3 * 1000;
const BOOSTED_POLL_DURATION_MS = 30 * 1000;
const MASTER_AUTO_SIZE_COLUMNS = ["domain", "name", "health", "state", "enabled", "started"];
const NAME_COLUMN_PRIMARY_COLOR = "#58a6ff";

const SERVER_GRID_THEME = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});

const SERVER_GRID_THEME_CLASS = SERVER_GRID_THEME.themeName || "ag-theme-quartz";

const STATUS_COLOR_BY_CODE = Object.freeze({
  healthy: {
    border: "1px solid rgba(52, 211, 153, 0.45)",
    text: "#34d399",
    background: "rgba(52, 211, 153, 0.16)",
    dot: "#34d399",
  },
  warning: {
    border: "1px solid rgba(255, 179, 71, 0.45)",
    text: "#ffb347",
    background: "rgba(255, 179, 71, 0.16)",
    dot: "#ffb347",
  },
  critical: {
    border: "1px solid rgba(248, 113, 113, 0.45)",
    text: "#f87171",
    background: "rgba(248, 113, 113, 0.16)",
    dot: "#f87171",
  },
  unknown: {
    border: "1px solid rgba(176, 184, 200, 0.35)",
    text: "#b0b8c8",
    background: "rgba(176, 184, 200, 0.14)",
    dot: "#c3cada",
  },
});

const SERVER_GRID_STYLE = {
  ...DEVICE_GRID_STYLE,
  position: "relative",
  fontFamily: gridFontFamily,
  "--ag-font-family": gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "--ag-background-color": "#070b1a",
  "--ag-foreground-color": "#f4f7ff",
  "--ag-header-background-color": "#0f172a",
  "--ag-header-foreground-color": "#cfe0ff",
  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
  "--ag-border-color": "rgba(125,183,255,0.18)",
  "--ag-row-border-color": "rgba(125,183,255,0.14)",
  "--ag-border-radius": "12px",
  "--ag-checkbox-border-radius": "3px",
  "--ag-checkbox-background-color": "rgba(255,255,255,0.06)",
  "--ag-checkbox-border-color": "rgba(180,200,220,0.6)",
  "--ag-checkbox-checked-color": "#7dd3fc",
  "--ag-cell-horizontal-padding": "18px",
};

const SERVER_GRID_SHELL_SX = {
  width: "100%",
  flexGrow: 1,
  minHeight: 0,
  height: "100%",
  overflow: "hidden",
  color: MAGIC_UI.textBright,
  "& .ag-root-wrapper": {
    minHeight: "100%",
    border: "none",
    borderRadius: 0,
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-paging-panel, & .ag-center-cols-container": {
    background: "transparent",
  },
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
  "& .ag-header": {
    backgroundColor: "rgba(11,18,36,0.92)",
    borderBottom: "1px solid rgba(148,163,184,0.18)",
  },
  "& .ag-header-cell, & .ag-header-group-cell": {
    borderColor: "rgba(148,163,184,0.12)",
  },
  "& .ag-header-cell-label": {
    color: "#f4f7ff",
    fontWeight: 600,
    letterSpacing: 0.24,
  },
  "& .ag-header-row-column-filter": {
    display: "none !important",
    height: "0 !important",
    minHeight: "0 !important",
    border: "0 !important",
  },
  "& .ag-floating-filter, & .ag-floating-filter-body, & .ag-floating-filter-body-wrapper": {
    display: "none !important",
    height: "0 !important",
    minHeight: "0 !important",
  },
  "& .ag-header-cell-menu-button, & .ag-header-cell-filter-button": {
    opacity: 1,
    color: "#9fb4d4",
    transition: "color 160ms ease, background 160ms ease",
    borderRadius: 8,
  },
  "& .ag-header-cell-menu-button:hover, & .ag-header-cell-filter-button:hover": {
    color: MAGIC_UI.accentA,
    background: "rgba(125,211,252,0.08)",
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    padding: "8px 12px 8px 18px",
    color: "#f4f7ff",
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    padding: 0,
    color: "#f4f7ff",
  },
  "& .ag-cell-value": {
    color: "#f4f7ff !important",
    fontWeight: 500,
  },
  "& .ag-cell-value a, & .ag-cell-value span": {
    color: "inherit",
  },
  "& .health-pill-cell": {
    display: "flex",
    alignItems: "center",
  },
  "& .health-pill-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    paddingTop: 0,
    paddingBottom: 0,
    lineHeight: "normal",
  },
  "& .health-pill-cell .ag-cell-value": {
    width: "100%",
    display: "flex",
    justifyContent: "center",
    alignItems: "center",
    height: "100%",
  },
  "& .health-pill-cell .ag-cell-value > span": {
    margin: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
    paddingLeft: "12px",
    paddingRight: "9px",
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
    transition: "background 160ms ease",
  },
  "& .ag-row:nth-of-type(odd)": {
    backgroundColor: "rgba(15,23,42,0.16)",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.34)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.2) !important",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
  "& .ag-paging-panel": {
    borderTop: "1px solid rgba(148,163,184,0.2)",
    backgroundColor: "rgba(3,7,18,0.8)",
  },
};

function formatTitleCase(value) {
  const raw = String(value || "").trim();
  if (!raw) return "Unknown";
  return raw.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function formatStatusLabel(value) {
  const raw = String(value || "").trim().toLowerCase();
  if (!raw) return "Unknown";
  if (raw === "healthy") return "Healthy";
  if (raw === "warning") return "Warning";
  if (raw === "critical") return "Critical";
  if (raw === "unknown") return "Unknown";
  if (raw === "idle") return "Idle";
  if (raw === "locked") return "Locked";
  if (raw === "unlocked") return "Unlocked";
  return formatTitleCase(raw);
}

function formatBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let unitIndex = 0;
  let remainder = bytes;
  while (remainder >= 1024 && unitIndex < units.length - 1) {
    remainder /= 1024;
    unitIndex += 1;
  }
  const fractionDigits = remainder >= 100 || unitIndex === 0 ? 0 : remainder >= 10 ? 1 : 2;
  return `${remainder.toFixed(fractionDigits)} ${units[unitIndex]}`;
}

function formatPercent(value) {
  const num = Number(value);
  if (!Number.isFinite(num)) return "0%";
  return `${num.toFixed(num >= 10 ? 0 : 1)}%`;
}

function formatDateTime(value) {
  const raw = String(value || "").trim();
  if (!raw) return "Unavailable";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleString();
}

function formatDateOnly(value) {
  const raw = String(value || "").trim();
  if (!raw) return "Unavailable";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  const month = String(parsed.getUTCMonth() + 1).padStart(2, "0");
  const day = String(parsed.getUTCDate()).padStart(2, "0");
  const year = parsed.getUTCFullYear();
  return `${month}/${day}/${year}`;
}

function formatUnixDateTime(value) {
  const epoch = Number(value);
  if (!Number.isFinite(epoch) || epoch <= 0) return "Unavailable";
  return new Date(epoch * 1000).toLocaleString();
}

function formatDurationSeconds(value) {
  const total = Number(value);
  if (!Number.isFinite(total) || total <= 0) return "0m";
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function formatServerClockValue(serverTime, timezoneLabel) {
  const display = String(serverTime?.display || "").trim();
  if (!display) return "Unavailable";
  const zone = String(timezoneLabel || serverTime?.timezone || "").trim();
  if (zone && display.endsWith(zone)) {
    return display.slice(0, -zone.length).trim();
  }
  return display;
}

function formatLoadAverageValue(values) {
  if (!Array.isArray(values) || values.length < 3) return "Unavailable";
  const [oneMinute, fiveMinutes, fifteenMinutes] = values;
  return `1 min ${oneMinute} · 5 min ${fiveMinutes} · 15 min ${fifteenMinutes}`;
}

function formatServerClockDisplay(serverTime, timezoneId) {
  const formatForTimezone = (dateValue) => {
    const dateText = new Intl.DateTimeFormat("en-GB", {
      timeZone: timezoneId || undefined,
      day: "2-digit",
      month: "2-digit",
      year: "numeric",
    }).format(dateValue);
    const timeText = new Intl.DateTimeFormat("en-US", {
      timeZone: timezoneId || undefined,
      hour: "2-digit",
      minute: "2-digit",
      hour12: true,
    }).format(dateValue);
    return `${dateText} @ ${timeText}`;
  };

  const raw = String(serverTime?.iso || serverTime?.utc || "").trim();
  if (!raw) {
    if (timezoneId) {
      try {
        return formatForTimezone(new Date());
      } catch {
        /* fallback below */
      }
    }
    return formatServerClockValue(serverTime, timezoneId);
  }
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) {
    if (timezoneId) {
      try {
        return formatForTimezone(new Date());
      } catch {
        /* fallback below */
      }
    }
    return formatServerClockValue(serverTime, timezoneId);
  }
  return formatForTimezone(parsed);
}

function severityTone(value) {
  const normalized = String(value || "").trim().toLowerCase() || "unknown";
  return STATUS_COLOR_BY_CODE[normalized] || STATUS_COLOR_BY_CODE.unknown;
}

function getWorstCertificate(certificates = []) {
  const rank = { critical: 3, warning: 2, healthy: 1, unknown: 0 };
  return (Array.isArray(certificates) ? certificates : []).reduce((current, item) => {
    if (!current) return item;
    const currentRank = rank[String(current?.severity || "").toLowerCase()] || 0;
    const nextRank = rank[String(item?.severity || "").toLowerCase()] || 0;
    if (nextRank > currentRank) return item;
    if (nextRank === currentRank && Number(item?.days_remaining) < Number(current?.days_remaining)) return item;
    return current;
  }, null);
}

function statusChip(label, toneKey) {
  const tone = severityTone(toneKey);
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        minWidth: 76,
        px: 1.5,
        py: 0.4,
        borderRadius: 999,
        backgroundColor: tone.background,
        border: tone.border,
        color: tone.text,
        fontWeight: 600,
        fontSize: "13px",
        lineHeight: 1,
        fontFamily: gridFontFamily,
        textTransform: "capitalize",
        gap: 0.75,
      }}
    >
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          backgroundColor: tone.dot,
          boxShadow: "0 0 0 2px rgba(0, 0, 0, 0.22)",
        }}
      />
      {label}
    </Box>
  );
}

function SummaryTable({ rows, columnDefs, defaultColDef, loading = false, emptyMessage = "No server overview data available." }) {
  const gridApiRef = useRef(null);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current;
    if (!api || loading || !rows?.length) return;
    const doSize = () => {
      try {
        api.autoSizeColumns(MASTER_AUTO_SIZE_COLUMNS, true);
      } catch {
        /* grid may not be ready yet */
      }
    };
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(doSize);
    } else {
      setTimeout(doSize, 0);
    }
  }, [loading, rows]);

  useEffect(() => {
    autoSizeColumns();
  }, [autoSizeColumns]);

  return (
    <Box
      className={SERVER_GRID_THEME_CLASS}
      sx={{
        ...SERVER_GRID_STYLE,
        ...SERVER_GRID_SHELL_SX,
      }}
    >
      <AgGridReact
        rowData={rows}
        columnDefs={columnDefs}
        defaultColDef={defaultColDef}
        theme={SERVER_GRID_THEME}
        rowHeight={50}
        headerHeight={46}
        floatingFiltersHeight={0}
        animateRows
        pagination
        paginationPageSize={20}
        paginationPageSizeSelector={[20, 50, 100]}
        suppressCellFocus
        loading={loading}
        overlayLoadingTemplate={"<span class='ag-overlay-loading-center'>Loading server overview…</span>"}
        overlayNoRowsTemplate={`<span class='ag-overlay-no-rows-center'>${emptyMessage}</span>`}
        getRowId={(params) => String(params.data?.id || params.rowIndex)}
        onGridReady={(params) => {
          gridApiRef.current = params.api;
          autoSizeColumns();
        }}
      />
    </Box>
  );
}

function SummaryNameWithDetailsCell(props) {
  return (
    <Box sx={{ py: 0.35, minWidth: 0 }}>
      <Typography
        sx={{
          color: NAME_COLUMN_PRIMARY_COLOR,
          fontWeight: 600,
          fontSize: "0.9rem",
          lineHeight: 1.35,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {props?.data?.name || props?.value || "—"}
      </Typography>
      <Typography
        sx={{
          color: MAGIC_UI.textMuted,
          fontSize: "0.78rem",
          lineHeight: 1.35,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          mt: 0.2,
        }}
      >
        {props?.data?.details || "—"}
      </Typography>
    </Box>
  );
}

function SummaryValueCell(props) {
  return (
    <Box sx={{ py: 0.35, minWidth: 0 }}>
      <Typography
        sx={{
          color: MAGIC_UI.textBright,
          fontWeight: 600,
          fontSize: "0.9rem",
          lineHeight: 1.35,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {props?.value || "—"}
      </Typography>
    </Box>
  );
}

function SummaryActionCell(props) {
  const actions = Array.isArray(props?.data?.actions) ? props.data.actions.filter(Boolean) : [];
  if (!actions.length) {
    return (
      <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem", lineHeight: 1.35 }}>
        —
      </Typography>
    );
  }

  return (
    <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
      {actions.map((action) => (
        <Button
          key={String(action?.id || action?.label || "action")}
          size="small"
          disabled={Boolean(action?.disabled)}
          onClick={() => action?.onClick?.()}
          sx={{
            minWidth: 118,
            minHeight: 34,
            borderRadius: 999,
            px: 1.8,
            textTransform: "none",
            fontWeight: 600,
            color: action?.disabled ? "rgba(148,163,184,0.76)" : MAGIC_UI.textBright,
            border: `1px solid ${action?.disabled ? "rgba(148,163,184,0.22)" : "rgba(148,163,184,0.36)"}`,
            background: action?.disabled ? "rgba(15,23,42,0.42)" : "rgba(5,10,24,0.82)",
            "&:hover": {
              background: action?.disabled ? undefined : "rgba(9, 16, 34, 0.94)",
              borderColor: action?.disabled ? undefined : "rgba(125,211,252,0.46)",
            },
          }}
        >
          {action?.label || "Action"}
        </Button>
      ))}
    </Stack>
  );
}

function MasterHealthCell(props) {
  const value = String(props?.data?.health || "").trim().toLowerCase();
  if (!value) {
    return (
      <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem", lineHeight: 1.35 }}>
        —
      </Typography>
    );
  }
  return statusChip(formatStatusLabel(value), value);
}

function MasterTextCell(props) {
  const value = String(props?.value || "").trim();
  return (
    <Typography sx={{ color: value ? MAGIC_UI.textBright : MAGIC_UI.textMuted, fontSize: "0.86rem", lineHeight: 1.35 }}>
      {value || "—"}
    </Typography>
  );
}

export default function ServerInfo() {
  const { isAdmin } = useAuth();
  const [aboutOpen, setAboutOpen] = useState(false);
  const [overview, setOverview] = useState(null);
  const [serverTimeSnapshot, setServerTimeSnapshot] = useState(null);
  const [serverTimeLoading, setServerTimeLoading] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [actionBusyKey, setActionBusyKey] = useState("");
  const [confirmAction, setConfirmAction] = useState(null);
  const [boostPollingUntil, setBoostPollingUntil] = useState(0);
  const [timezoneDialogOpen, setTimezoneDialogOpen] = useState(false);
  const [timezoneOptions, setTimezoneOptions] = useState([]);
  const [timezoneOptionsLoaded, setTimezoneOptionsLoaded] = useState(false);
  const [timezoneLoading, setTimezoneLoading] = useState(false);
  const [timezoneSaving, setTimezoneSaving] = useState(false);
  const [timezoneError, setTimezoneError] = useState("");
  const [selectedTimezone, setSelectedTimezone] = useState("");
  const [ansibleRunnerDialogOpen, setAnsibleRunnerDialogOpen] = useState(false);
  const [ansibleRunnerSaving, setAnsibleRunnerSaving] = useState(false);
  const [ansibleRunnerError, setAnsibleRunnerError] = useState("");
  const [ansibleRunnerJobLimit, setAnsibleRunnerJobLimit] = useState("20");
  const [ansibleRunnerGlobalLimit, setAnsibleRunnerGlobalLimit] = useState("50");
  const [timezoneMeta, setTimezoneMeta] = useState({
    currentTimezone: "",
    changeSupported: null,
  });
  const hasOverviewRef = useRef(false);

  const sendScopedNotification = useAppNotifications();

  const fetchOverview = useCallback(async ({ background = false } = {}) => {
    if (!background) {
      if (hasOverviewRef.current) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }
    }
    try {
      const response = await fetch("/api/server/overview", {
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      setOverview(payload);
      hasOverviewRef.current = true;
      setError("");
    } catch (requestError) {
      setError(
        requestError instanceof Error && requestError.message
          ? requestError.message
          : "Borealis could not load the server overview."
      );
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  const fetchTimezoneOptions = useCallback(async () => {
    setTimezoneLoading(true);
    setTimezoneError("");
    try {
      const response = await fetch("/api/server/timezones", {
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      setTimezoneOptions(Array.isArray(payload?.timezones) ? payload.timezones : []);
      setTimezoneOptionsLoaded(true);
      const currentTimezone = String(payload?.current_timezone || "").trim();
      const changeSupported =
        typeof payload?.change_supported === "boolean" ? payload.change_supported : null;
      setTimezoneMeta({
        currentTimezone,
        changeSupported,
      });
      if (currentTimezone) {
        setSelectedTimezone(currentTimezone);
      }
    } catch (requestError) {
      setTimezoneError(
        requestError instanceof Error && requestError.message
          ? requestError.message
          : "Borealis could not load server timezones."
      );
    } finally {
      setTimezoneLoading(false);
    }
  }, []);

  const fetchServerTimeSnapshot = useCallback(async () => {
    setServerTimeLoading(true);
    try {
      const response = await fetch("/api/server/time", {
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      setServerTimeSnapshot(payload);
      const snapshotTimezoneId = String(payload?.timezone_id || "").trim();
      const snapshotTimezone = String(payload?.timezone || "").trim();
      if (snapshotTimezoneId || snapshotTimezone) {
        setTimezoneMeta((current) => ({
          currentTimezone: current?.currentTimezone || snapshotTimezoneId || snapshotTimezone,
          changeSupported: current?.changeSupported ?? null,
        }));
      }
    } catch {
      /* server time fallback is best-effort */
    } finally {
      setServerTimeLoading(false);
    }
  }, []);

  const handleRefresh = useCallback(() => {
    fetchOverview({ background: false });
  }, [fetchOverview]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "server-refresh",
        label: "Refresh",
        icon: <RefreshIcon />,
        tone: "secondary",
        onClick: handleRefresh,
      },
      {
        id: "server-github-project",
        label: "GitHub Project",
        icon: <GitHubIcon />,
        tone: "secondary",
        onClick: () => window.open("https://github.com/bunny-lab-io/Borealis", "_blank"),
      },
      {
        id: "server-about",
        label: "About Borealis",
        icon: <InfoIcon />,
        tone: "primary",
        onClick: () => setAboutOpen(true),
      },
    ],
    [handleRefresh]
  );

  useRoutePageChrome({
    title: "Server Overview",
    subtitle: "Unified administrative view across Runtime, Services, Resources, Access, and Security.",
    Icon: PAGE_ICON,
    actions: pageHeaderActions,
  });

  useEffect(() => {
    fetchOverview({ background: false });
  }, [fetchOverview]);

  useEffect(() => {
    if (!isAdmin) return;
    fetchTimezoneOptions();
    fetchServerTimeSnapshot();
  }, [fetchServerTimeSnapshot, fetchTimezoneOptions, isAdmin]);

  useEffect(() => {
    if (!timezoneDialogOpen) return;
    if (!timezoneOptionsLoaded) {
      fetchTimezoneOptions();
    }
  }, [fetchTimezoneOptions, timezoneDialogOpen, timezoneOptionsLoaded]);

  useEffect(() => {
    if (!isAdmin) return;
    const hostPayload = overview?.host || {};
    const missingClock =
      !String(hostPayload?.server_time?.iso || "").trim() &&
      !String(hostPayload?.server_time?.utc || "").trim() &&
      !String(hostPayload?.server_time?.display || "").trim();
    const missingTimezoneId = !String(hostPayload?.timezone_id || "").trim();
    const missingChangeSupport = typeof hostPayload?.timezone_change_supported !== "boolean";
    if (missingClock || missingTimezoneId) {
      fetchServerTimeSnapshot();
    }
    if (missingTimezoneId || missingChangeSupport) {
      fetchTimezoneOptions();
    }
  }, [fetchOverview, fetchServerTimeSnapshot, fetchTimezoneOptions, isAdmin, overview]);

  useEffect(() => {
    if (!boostPollingUntil || boostPollingUntil <= Date.now()) return undefined;
    const timeoutId = window.setTimeout(() => setBoostPollingUntil(0), boostPollingUntil - Date.now());
    return () => window.clearTimeout(timeoutId);
  }, [boostPollingUntil]);

  const pollIntervalMs = boostPollingUntil > Date.now() ? BOOSTED_POLL_INTERVAL_MS : DEFAULT_POLL_INTERVAL_MS;

  useEffect(() => {
    if (!isAdmin) return undefined;
    const intervalId = window.setInterval(() => {
      fetchOverview({ background: true });
    }, pollIntervalMs);
    return () => window.clearInterval(intervalId);
  }, [fetchOverview, isAdmin, pollIntervalMs]);

  useEffect(() => {
    const socket = window.BorealisSocket;
    if (!socket || typeof socket.on !== "function") return undefined;
    const handler = () => fetchOverview({ background: true });
    socket.on("server_operator_presence_changed", handler);
    return () => {
      try {
        socket.off("server_operator_presence_changed", handler);
      } catch {
        /* noop */
      }
    };
  }, [fetchOverview]);

  const requestRestart = useCallback((row) => {
    if (!row?.restart_supported) return;
    setActionError("");
    setConfirmAction({
      kind: "restart",
      title: "Queue Safe Restart",
      subtitle: "Borealis will schedule this restart through a detached transient unit so the admin request can return cleanly before the service restarts.",
      confirmLabel: "Queue Restart",
      payload: row,
      message: `Queue a restart for ${row.label}? Borealis will restart ${row.unit_name} after a short delay.`,
    });
  }, []);

  const requestWireGuardRecovery = useCallback(() => {
    setActionError("");
    setConfirmAction({
      kind: "wireguard_recover",
      title: "Recover WireGuard Listener",
      subtitle: "This asks Borealis to reconcile the WireGuard listener while active tunnels exist. It does not restart Borealis services.",
      confirmLabel: "Recover Listener",
      payload: null,
      message:
        "Recover the Borealis WireGuard listener now? This is intended for live tunnel transport issues while operator sessions are active.",
    });
  }, []);

  const closeTimezoneDialog = useCallback(() => {
    if (timezoneSaving) return;
    setTimezoneDialogOpen(false);
    setTimezoneError("");
  }, [timezoneSaving]);

  const openAnsibleRunnerDialog = useCallback(() => {
    const currentSettings = overview?.ansible_runner || {};
    setAnsibleRunnerJobLimit(String(Number(currentSettings?.job_concurrency_limit || 20)));
    setAnsibleRunnerGlobalLimit(String(Number(currentSettings?.global_concurrency_limit || 50)));
    setAnsibleRunnerError("");
    setAnsibleRunnerDialogOpen(true);
  }, [overview]);

  const closeAnsibleRunnerDialog = useCallback(() => {
    if (ansibleRunnerSaving) return;
    setAnsibleRunnerDialogOpen(false);
    setAnsibleRunnerError("");
  }, [ansibleRunnerSaving]);

  const applyAnsibleRunnerSettings = useCallback(async () => {
    const jobLimit = Number(ansibleRunnerJobLimit);
    const globalLimit = Number(ansibleRunnerGlobalLimit);
    if (!Number.isFinite(jobLimit) || jobLimit < 1 || !Number.isInteger(jobLimit)) {
      setAnsibleRunnerError("Per-job concurrency must be a whole number greater than 0.");
      return;
    }
    if (!Number.isFinite(globalLimit) || globalLimit < 1 || !Number.isInteger(globalLimit)) {
      setAnsibleRunnerError("Global concurrency must be a whole number greater than 0.");
      return;
    }
    setAnsibleRunnerSaving(true);
    setAnsibleRunnerError("");
    try {
      const response = await fetch("/api/server/ansible-runner-settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          job_concurrency_limit: jobLimit,
          global_concurrency_limit: globalLimit,
        }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      setAnsibleRunnerDialogOpen(false);
      await sendScopedNotification({
        title: "Ansible Limits Updated",
        message: `Scheduled Ansible concurrency updated to ${jobLimit} per job and ${globalLimit} globally.`,
        icon: "settings",
        variant: "info",
      });
      setBoostPollingUntil(Date.now() + BOOSTED_POLL_DURATION_MS);
      await fetchOverview({ background: false });
    } catch (requestError) {
      const message =
        requestError instanceof Error && requestError.message
          ? requestError.message
          : "Borealis could not update Ansible runner settings.";
      setAnsibleRunnerError(message);
      await sendScopedNotification({
        title: "Ansible Limits Update Failed",
        message,
        icon: "warning",
        variant: "error",
      });
    } finally {
      setAnsibleRunnerSaving(false);
    }
  }, [ansibleRunnerGlobalLimit, ansibleRunnerJobLimit, fetchOverview, sendScopedNotification]);

  const applyTimezoneChange = useCallback(async () => {
    const timezoneId = String(selectedTimezone || "").trim();
    if (!timezoneId) {
      setTimezoneError("Choose a timezone before applying the change.");
      return;
    }
    setTimezoneSaving(true);
    setTimezoneError("");
    try {
      const response = await fetch("/api/server/timezone", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ timezone: timezoneId }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      setTimezoneDialogOpen(false);
      await sendScopedNotification({
        title: "Timezone Updated",
        message: `Server timezone changed to ${timezoneId}.`,
        icon: "schedule",
        variant: "info",
      });
      setBoostPollingUntil(Date.now() + BOOSTED_POLL_DURATION_MS);
      await fetchOverview({ background: false });
    } catch (requestError) {
      const message =
        requestError instanceof Error && requestError.message
          ? requestError.message
          : "Borealis could not change the server timezone.";
      setTimezoneError(message);
      await sendScopedNotification({
        title: "Timezone Update Failed",
        message,
        icon: "warning",
        variant: "error",
      });
    } finally {
      setTimezoneSaving(false);
    }
  }, [fetchOverview, selectedTimezone, sendScopedNotification]);

  const executeAction = useCallback(async () => {
    if (!confirmAction) return;
    const targetRow = confirmAction?.payload || null;
    const busyKey =
      confirmAction.kind === "restart"
        ? String(targetRow?.unit_name || "")
        : "wireguard_recover";
    setActionBusyKey(busyKey);
    setActionError("");
    try {
      if (confirmAction.kind === "restart") {
        const body =
          targetRow?.key === "postgresql_cluster" && targetRow?.instance
            ? { instance: targetRow.instance }
            : {};
        const response = await fetch(`/api/server/services/${encodeURIComponent(targetRow?.key || "")}/restart`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "include",
          body: JSON.stringify(body),
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
        }
        await sendScopedNotification({
          title: "Service Restart Queued",
          message: `${targetRow?.label || "Service"} restart queued safely on the engine host.`,
          icon: "pendingactions",
          variant: "info",
        });
      } else {
        const response = await fetch("/api/server/wireguard/recover", {
          method: "POST",
          credentials: "include",
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
        }
        await sendScopedNotification({
          title: "WireGuard Recovery Started",
          message: "Borealis started a WireGuard listener recovery attempt.",
          icon: "update",
          variant: "info",
        });
      }
      setConfirmAction(null);
      setBoostPollingUntil(Date.now() + BOOSTED_POLL_DURATION_MS);
      fetchOverview({ background: true });
    } catch (requestError) {
      const message =
        requestError instanceof Error && requestError.message
          ? requestError.message
          : "Borealis could not complete the requested admin action.";
      setActionError(message);
      await sendScopedNotification({
        title: "Admin Action Failed",
        message,
        icon: "warning",
        variant: "error",
      });
    } finally {
      setActionBusyKey("");
    }
  }, [confirmAction, fetchOverview, sendScopedNotification]);

  const host = overview?.host || {};
  const resources = overview?.resources || {};
  const services = Array.isArray(overview?.services) ? overview.services : [];
  const operatorSessionCount = Math.max(0, Number(overview?.operator_session_count || 0));
  const wireguard = overview?.wireguard || {};
  const publicEdge = overview?.public_edge || {};
  const ansibleRunner = overview?.ansible_runner || {};
  const certificates = Array.isArray(publicEdge?.certificates) ? publicEdge.certificates : [];
  const worstCert = getWorstCertificate(certificates);
  const aegis = overview?.security?.aegis || {};
  const effectiveTimezoneId = String(
    host?.timezone_id || timezoneMeta.currentTimezone || serverTimeSnapshot?.timezone_id || ""
  ).trim();
  const effectiveTimezoneLabel = String(
    effectiveTimezoneId || host?.timezone || serverTimeSnapshot?.timezone || ""
  ).trim();
  const effectiveServerTime = host?.server_time || serverTimeSnapshot || {};
  const timezoneChangeSupported =
    typeof host?.timezone_change_supported === "boolean"
      ? host.timezone_change_supported
      : typeof timezoneMeta.changeSupported === "boolean"
      ? timezoneMeta.changeSupported
      : true;
  const rawClockValue = formatServerClockDisplay(effectiveServerTime, effectiveTimezoneId);
  const clockValue =
    rawClockValue === "Unavailable" && (loading || serverTimeLoading) ? "Loading..." : rawClockValue;
  const timezoneDisplayValue =
    effectiveTimezoneLabel || (loading || serverTimeLoading ? "Loading..." : "Unavailable");
  const loadAverageValue = formatLoadAverageValue(resources?.load_average);
  const loadAverageCaption = `1, 5, and 15 minute averages · CPU Count ${resources?.cpu_count || 0}`;

  const openTimezoneDialog = useCallback(() => {
    setTimezoneError("");
    setSelectedTimezone(effectiveTimezoneId);
    setTimezoneDialogOpen(true);
  }, [effectiveTimezoneId]);

  const gridDefaultColDef = useMemo(
    () => ({
      ...DEFAULT_GRID_COL_DEF,
      cellClass: "auto-col-tight",
      floatingFilter: false,
    }),
    []
  );

  const masterColumnDefs = useMemo(
    () => [
      {
        headerName: "Domain",
        field: "domain",
        minWidth: 130,
        maxWidth: 150,
        filter: "agTextColumnFilter",
        cellRenderer: MasterTextCell,
      },
      {
        headerName: "Name",
        field: "name",
        minWidth: 260,
        flex: 1.15,
        filter: "agTextColumnFilter",
        cellRenderer: SummaryNameWithDetailsCell,
      },
      {
        headerName: "Value",
        field: "value",
        minWidth: 240,
        flex: 1,
        filter: "agTextColumnFilter",
        cellRenderer: SummaryValueCell,
      },
      {
        headerName: "Health",
        field: "health",
        minWidth: 130,
        maxWidth: 140,
        filter: "agTextColumnFilter",
        cellClass: "health-pill-cell",
        cellRenderer: MasterHealthCell,
      },
      {
        headerName: "State",
        field: "state",
        minWidth: 130,
        filter: "agTextColumnFilter",
        cellRenderer: MasterTextCell,
      },
      {
        headerName: "Enabled",
        field: "enabled",
        minWidth: 130,
        filter: "agTextColumnFilter",
        cellRenderer: MasterTextCell,
      },
      {
        headerName: "Started",
        field: "started",
        minWidth: 190,
        filter: "agTextColumnFilter",
        cellRenderer: MasterTextCell,
      },
      {
        headerName: "Actions",
        field: "actions_label",
        minWidth: 150,
        maxWidth: 180,
        sortable: false,
        filter: false,
        floatingFilter: false,
        resizable: false,
        cellClass: "auto-col-tight",
        cellRenderer: SummaryActionCell,
      },
    ],
    []
  );

  const runtimeRows = useMemo(
    () => [
      {
        id: "public_url",
        name: "Public URL",
        value: host?.public_base_url || "Unavailable",
        details: "Traefik Reverse-Proxied FQDN",
        actions: [],
      },
      {
        id: "engine_mode",
        name: "Engine Mode",
        value: formatTitleCase(host?.engine_mode),
        details: "Current Engine Operational State",
        actions: [],
      },
      {
        id: "ansible_runner_limits",
        name: "Scheduled Ansible Runner",
        value: `${Number(ansibleRunner?.job_concurrency_limit || 20)} / ${Number(ansibleRunner?.global_concurrency_limit || 50)}`,
        details: "Per-job / global runner limits for scheduled Ansible execution on this engine",
        actions: [
          {
            id: "edit_ansible_runner_limits",
            label: ansibleRunnerSaving ? "Saving..." : "Edit Limits",
            disabled: ansibleRunnerSaving,
            onClick: openAnsibleRunnerDialog,
          },
        ],
      },
      {
        id: "system_time",
        name: "System Time",
        value: clockValue,
        details: timezoneDisplayValue,
        actions: [
          {
            id: "change_timezone",
            label: timezoneSaving ? "Applying..." : "Change Timezone",
            disabled: !timezoneChangeSupported || timezoneSaving,
            onClick: openTimezoneDialog,
          },
        ],
      },
      {
        id: "cpu_load_average",
        name: "CPU Load Average",
        value: loadAverageValue,
        details: loadAverageCaption,
        actions: [],
      },
      {
        id: "host_uptime",
        name: "Engine Host Uptime",
        value: formatDurationSeconds(host?.uptime_seconds),
        details: "Engine host runtime",
        actions: [],
      },
    ],
    [ansibleRunner, ansibleRunnerSaving, clockValue, timezoneDisplayValue, loadAverageCaption, loadAverageValue, openAnsibleRunnerDialog, openTimezoneDialog, timezoneChangeSupported, timezoneSaving, host]
  );

  const resourceRows = useMemo(
    () => [
      {
        id: "memory_usage",
        name: "Memory Usage",
        value: formatPercent(resources?.memory?.used_percent),
        details: `${formatBytes(resources?.memory?.used_bytes)} used of ${formatBytes(resources?.memory?.total_bytes)} · ${formatBytes(resources?.memory?.free_bytes)} free`,
        actions: [],
      },
      {
        id: "root_disk",
        name: "Root Disk",
        value: formatPercent(resources?.disk_root?.used_percent),
        details: `${formatBytes(resources?.disk_root?.used_bytes)} used of ${formatBytes(resources?.disk_root?.total_bytes)} · ${resources?.disk_root?.path || "/"}`,
        actions: [],
      },
      {
        id: "engine_disk_usage",
        name: "Engine Disk Usage",
        value: formatPercent(resources?.disk_project?.used_percent),
        details: `${formatBytes(resources?.disk_project?.used_bytes)} used of ${formatBytes(resources?.disk_project?.total_bytes)} · ${resources?.disk_project?.path || "Project root"}`,
        actions: [],
      },
    ],
    [resources]
  );

  const accessRows = useMemo(
    () => [
      {
        id: "active_vpn_tunnels",
        name: "Active VPN Tunnels",
        value: String(Number(wireguard?.active_tunnel_count || 0)),
        details:
          Number(wireguard?.active_tunnel_count || 0) > 0
            ? `Borealis Wireguard Listener · ${wireguard?.listener_healthy ? "Listener healthy" : formatTitleCase(wireguard?.listener_reason || "warning")}`
            : "WireGuard idle",
        actions: [],
      },
      {
        id: "active_operator_sessions",
        name: "Active Operator Sessions",
        value: String(operatorSessionCount),
        details: operatorSessionCount ? "Live browser sessions tracked by Borealis" : "No live operator sessions",
        actions: [],
      },
      {
        id: "wireguard_interface",
        name: "WireGuard Interface",
        value: wireguard?.interface_name || "borealis-wg",
        details: `Shell ${wireguard?.shell_port || "—"} - VNC ${wireguard?.vnc_port || "—"} - WS ${wireguard?.vnc_ws_port || "—"}`,
        actions: [
          {
            id: "recover_listener",
            label: actionBusyKey === "wireguard_recover" ? "Recovering..." : "Recover Listener",
            disabled: actionBusyKey === "wireguard_recover" || !Number(wireguard?.active_tunnel_count || 0),
            onClick: requestWireGuardRecovery,
          },
        ],
      },
    ],
    [actionBusyKey, operatorSessionCount, requestWireGuardRecovery, wireguard]
  );

  const securityRows = useMemo(
    () => [
      {
        id: "letsencrypt_ssl_certificate",
        name: "Let's Encrypt SSL Certificate",
        value:
          worstCert?.expires_at
            ? `Valid until ${formatDateOnly(worstCert.expires_at)}`
            : certificates.length
            ? "Tracked"
            : "Unavailable",
        details:
          publicEdge?.fqdn
            ? `${publicEdge.fqdn}${worstCert?.severity ? ` · ${formatStatusLabel(worstCert.severity)}` : ""}`
            : "No public FQDN configured",
        actions: [],
      },
      {
        id: "aegis_cipher",
        name: "Aegis Cipher",
        value: !aegis?.configured ? "Not Configured" : aegis?.locked ? "Locked" : "Unlocked",
        details: aegis?.configured ? `Unlock scope ${formatTitleCase(aegis?.unlock_scope)}` : "Protected secret storage is not configured",
        actions: [],
      },
    ],
    [aegis, certificates.length, publicEdge?.fqdn, worstCert]
  );

  const masterRows = useMemo(() => {
    const runtimeMasterRows = runtimeRows.map((row, index) => ({
      ...row,
      id: `runtime:${row.id}`,
      domain: "Runtime",
      health: "",
      state: "",
      enabled: "",
      started: "",
      sort_order: index,
    }));

    const serviceMasterRows = services.map((row, index) => {
      const busyKey = String(actionBusyKey || "");
      const rowKey = String(row?.unit_name || "");
      const queued = Boolean(row?.pending_action);
      const busy = busyKey === rowKey;
      const unsupported = !row?.restart_supported;
      const label = unsupported ? "Unavailable" : queued || busy ? "Restarting..." : "Restart";
      return {
        id: `services:${row?.unit_name || index}`,
        domain: "Services",
        name: row?.label || "Service",
        details: row?.unit_name || "—",
        value: row?.main_pid ? `PID ${row.main_pid}` : "Systemd Unit",
        health: String(row?.status || "").trim().toLowerCase(),
        state: formatTitleCase(row?.active_state),
        enabled: formatTitleCase(row?.enabled_state),
        started: row?.started_at ? formatDateTime(row.started_at) : "Unavailable",
        actions: [
          {
            id: `service_action:${row?.unit_name || index}`,
            label,
            disabled: queued || busy || unsupported,
            onClick: () => requestRestart(row),
          },
        ],
        sort_order: index,
      };
    });

    const resourceMasterRows = resourceRows.map((row, index) => ({
      ...row,
      id: `resources:${row.id}`,
      domain: "Resources",
      health: "",
      state: "",
      enabled: "",
      started: "",
      sort_order: index,
    }));

    const accessMasterRows = accessRows.map((row, index) => {
      const health =
        row.id === "active_vpn_tunnels"
          ? Number(wireguard?.active_tunnel_count || 0) > 0
            ? wireguard?.listener_healthy
              ? "healthy"
              : "warning"
            : "unknown"
          : row.id === "active_operator_sessions"
          ? operatorSessionCount > 0
            ? "healthy"
            : "unknown"
          : row.id === "wireguard_interface"
          ? wireguard?.interface_present && wireguard?.interface_up
            ? "healthy"
            : wireguard?.interface_present
            ? "warning"
            : "critical"
          : "";
      const state =
        row.id === "active_vpn_tunnels"
          ? Number(wireguard?.active_tunnel_count || 0) > 0
            ? "Active"
            : "Idle"
          : row.id === "active_operator_sessions"
          ? operatorSessionCount > 0
            ? "Connected"
            : "Idle"
          : row.id === "wireguard_interface"
          ? wireguard?.interface_up
            ? "Up"
            : wireguard?.interface_present
            ? "Down"
            : "Unavailable"
          : "";
      return {
        ...row,
        id: `access:${row.id}`,
        domain: "Access",
        health,
        state,
        enabled: "",
        started: "",
        sort_order: index,
      };
    });

    const securityMasterRows = securityRows.map((row, index) => ({
      ...row,
      id: `security:${row.id}`,
      domain: "Security",
      health: row.id === "letsencrypt_ssl_certificate" ? String(worstCert?.severity || "unknown") : "",
      state: row.id === "aegis_cipher" ? String(row.value || "") : "",
      enabled: "",
      started: "",
      sort_order: index,
    }));

    return [
      ...runtimeMasterRows,
      ...serviceMasterRows,
      ...resourceMasterRows,
      ...accessMasterRows,
      ...securityMasterRows,
    ];
  }, [accessRows, actionBusyKey, operatorSessionCount, requestRestart, resourceRows, runtimeRows, securityRows, services, wireguard, worstCert]);

  if (!isAdmin) return null;

  return (
    <>
      <PageBodyFrame variant="grid">
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0, width: "100%" }}>
          <SummaryTable
            rows={masterRows}
            columnDefs={masterColumnDefs}
            defaultColDef={gridDefaultColDef}
            loading={loading || refreshing}
            emptyMessage={error ? "Server overview unavailable." : "No server overview data available."}
          />
        </Box>
      </PageBodyFrame>

      <Dialog open={timezoneDialogOpen} onClose={closeTimezoneDialog} PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Change Server Timezone"
            subtitle="Update the timezone used by the entire Borealis engine host without signing into SSH."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Borealis will apply the selected timezone to the server itself. This affects the clock shown in the WebUI and any server-local time displays.
          </Typography>
          <Box sx={{ mt: 2 }}>
            <Autocomplete
              options={timezoneOptions}
              loading={timezoneLoading}
              value={selectedTimezone || null}
              onChange={(_event, value) => {
                setSelectedTimezone(String(value || ""));
                setTimezoneError("");
              }}
              onInputChange={() => {
                if (timezoneError) {
                  setTimezoneError("");
                }
              }}
              slotProps={{
                popper: {
                  sx: {
                    zIndex: 1600,
                    "& .MuiAutocomplete-paper": {
                      mt: 0.75,
                      borderRadius: 2.5,
                      background: DIALOG_MAGIC_UI.panelBg,
                      backdropFilter: "blur(18px)",
                      border: `1px solid ${DIALOG_MAGIC_UI.panelBorderStrong}`,
                      boxShadow: DIALOG_MAGIC_UI.glow,
                      color: DIALOG_MAGIC_UI.textBright,
                      overflow: "hidden",
                    },
                    "& .MuiAutocomplete-listbox": {
                      py: 0.6,
                      maxHeight: 360,
                    },
                    "& .MuiAutocomplete-option": {
                      minHeight: 40,
                      px: 1.6,
                      py: 0.75,
                      fontSize: "0.92rem",
                      lineHeight: 1.35,
                      color: DIALOG_MAGIC_UI.textBright,
                      background: "transparent",
                      transition: "background 160ms ease, color 160ms ease",
                    },
                    "& .MuiAutocomplete-option.Mui-focused": {
                      background: "rgba(125,211,252,0.08)",
                    },
                    "& .MuiAutocomplete-option[aria-selected='true']": {
                      background: "rgba(192,132,252,0.14)",
                      color: "#f2e8ff",
                    },
                    "& .MuiAutocomplete-option[aria-selected='true'].Mui-focused": {
                      background: "rgba(192,132,252,0.2)",
                    },
                    "& .MuiAutocomplete-loading, & .MuiAutocomplete-noOptions": {
                      color: DIALOG_MAGIC_UI.textMuted,
                      fontSize: "0.88rem",
                      px: 1.6,
                      py: 1.25,
                    },
                  },
                },
                clearIndicator: {
                  sx: {
                    color: DIALOG_MAGIC_UI.textMuted,
                    borderRadius: 999,
                    "&:hover": {
                      color: DIALOG_MAGIC_UI.textBright,
                      background: "rgba(125,211,252,0.08)",
                    },
                  },
                },
                popupIndicator: {
                  sx: {
                    color: DIALOG_MAGIC_UI.textMuted,
                    borderRadius: 999,
                    "&:hover": {
                      color: DIALOG_MAGIC_UI.textBright,
                      background: "rgba(125,211,252,0.08)",
                    },
                  },
                },
              }}
              renderInput={(params) => (
                <TextField
                  {...params}
                  label="Timezone"
                  placeholder="Select a timezone"
                  sx={DIALOG_INPUT_SX}
                  InputProps={{
                    ...params.InputProps,
                    endAdornment: (
                      <>
                        {timezoneLoading ? <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA, mr: 1 }} /> : null}
                        {params.InputProps.endAdornment}
                      </>
                    ),
                  }}
                />
              )}
            />
          </Box>
          {timezoneError ? (
            <Typography sx={{ mt: 1.4, color: "#ffb7b7", fontSize: "0.84rem", lineHeight: 1.45 }}>
              {timezoneError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={closeTimezoneDialog} sx={DIALOG_BUTTON_SX} disabled={timezoneSaving}>
            Cancel
          </Button>
          <Button onClick={applyTimezoneChange} sx={DIALOG_PRIMARY_BUTTON_SX} disabled={timezoneSaving || timezoneLoading}>
            {timezoneSaving ? "Applying..." : "Apply Timezone"}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={ansibleRunnerDialogOpen} onClose={closeAnsibleRunnerDialog} PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Scheduled Ansible Concurrency"
            subtitle="Tune how many scheduled Ansible runners Borealis can execute at once."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Borealis enforces both the per-job cap and the shared global cap whenever scheduled Ansible jobs dispatch Engine-side runners. Individual modes fan out across targets, while shared modes still consume runner slots per playbook component.
          </Typography>
          <Stack spacing={2} sx={{ mt: 2 }}>
            <TextField
              label="Ansible_Runner_Job_Concurrency_Limit"
              type="number"
              value={ansibleRunnerJobLimit}
              onChange={(event) => {
                setAnsibleRunnerJobLimit(event.target.value);
                if (ansibleRunnerError) setAnsibleRunnerError("");
              }}
              inputProps={{ min: 1, step: 1 }}
              sx={DIALOG_INPUT_SX}
              helperText="Maximum concurrent scheduled Ansible runners allowed per job occurrence."
            />
            <TextField
              label="Ansible_Runner_Global_Concurrency_Limit"
              type="number"
              value={ansibleRunnerGlobalLimit}
              onChange={(event) => {
                setAnsibleRunnerGlobalLimit(event.target.value);
                if (ansibleRunnerError) setAnsibleRunnerError("");
              }}
              inputProps={{ min: 1, step: 1 }}
              sx={DIALOG_INPUT_SX}
              helperText="Maximum concurrent scheduled Ansible runners allowed across all scheduled jobs on this engine."
            />
          </Stack>
          {ansibleRunnerError ? (
            <Typography sx={{ mt: 1.4, color: "#ffb7b7", fontSize: "0.84rem", lineHeight: 1.45 }}>
              {ansibleRunnerError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={closeAnsibleRunnerDialog} sx={DIALOG_BUTTON_SX} disabled={ansibleRunnerSaving}>
            Cancel
          </Button>
          <Button onClick={applyAnsibleRunnerSettings} sx={DIALOG_PRIMARY_BUTTON_SX} disabled={ansibleRunnerSaving}>
            {ansibleRunnerSaving ? "Saving..." : "Save Limits"}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={Boolean(confirmAction)} onClose={() => setConfirmAction(null)} PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={confirmAction?.title || "Confirm Action"} subtitle={confirmAction?.subtitle || ""} />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>{confirmAction?.message || ""}</Typography>
          {actionError ? (
            <Typography sx={{ mt: 1.25, color: "#ffb7b7", fontSize: "0.84rem", lineHeight: 1.45 }}>
              {actionError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setConfirmAction(null)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button
            onClick={executeAction}
            disabled={Boolean(actionBusyKey)}
            sx={DIALOG_PRIMARY_BUTTON_SX}
          >
            {confirmAction?.confirmLabel || "Confirm"}
          </Button>
        </DialogActions>
      </Dialog>

      <CreditsDialog open={aboutOpen} onClose={() => setAboutOpen(false)} />
    </>
  );
}
