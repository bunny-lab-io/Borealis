////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/web-interface/src/Devices/Device_Details.jsx

import React, { useState, useEffect, useMemo, useCallback, useRef } from "react";
import {
  Box,
  Stack,
  Tabs,
  Tab,
  Typography,
  Button,
  Menu,
  MenuItem,
  TextField,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
} from "@mui/material";
import InfoOutlinedIcon from "@mui/icons-material/InfoOutlined";
import StorageRoundedIcon from "@mui/icons-material/StorageRounded";
import MemoryRoundedIcon from "@mui/icons-material/MemoryRounded";
import LanRoundedIcon from "@mui/icons-material/LanRounded";
import AppsRoundedIcon from "@mui/icons-material/AppsRounded";
import ListAltRoundedIcon from "@mui/icons-material/ListAltRounded";
import TerminalRoundedIcon from "@mui/icons-material/TerminalRounded";
import DesktopWindowsRoundedIcon from "@mui/icons-material/DesktopWindowsRounded";
import SpeedRoundedIcon from "@mui/icons-material/SpeedRounded";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import MoreHorizIcon from "@mui/icons-material/MoreHoriz";
import { ClearDeviceActivityDialog } from "../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import ReverseTunnelRemoteShell from "./ReverseTunnel/RemoteShell.jsx";
import ReverseTunnelVnc from "./ReverseTunnel/VNC.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const MAGIC_UI = {
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

const DEVICE_LIST_STATUS_COLORS = Object.freeze({
  online: "#00d18c",
  recovering: "#ffb347",
  offline: "#b0b8c8",
});
const TUNNEL_STATUS_POLL_INTERVAL_MS = 15000;

const PAGE_ICON = DeveloperBoardRoundedIcon;

const NAV_TAB_HEIGHT = 32;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};
const SUMMARY_SECTION_ACTIVE_BG =
  "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)";

const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const BASE_GRID_HEIGHTS = {
  topLevel: 300,
  storage: 300,
  memory: 260,
  network: 260,
};

const TOP_TABS = [
  { key: "summary", label: "Device Summary", icon: InfoOutlinedIcon },
  { key: "software", label: "Installed Software", icon: AppsRoundedIcon },
  { key: "activity", label: "Activity History", icon: ListAltRoundedIcon },
  { key: "shell", label: "Remote Shell", icon: TerminalRoundedIcon },
  { key: "vnc", label: "Remote Desktop (VNC)", icon: DesktopWindowsRoundedIcon },
];
const DEVICE_DETAILS_TAB_URL_BY_KEY = Object.freeze({
  summary: "device_summary",
  software: "installed_software",
  activity: "activity_history",
  shell: "remote_shell",
  vnc: "remote_desktop",
});
const DEVICE_DETAILS_TAB_KEY_BY_URL = Object.freeze({
  device_summary: "summary",
  summary: "summary",
  installed_software: "software",
  software: "software",
  activity_history: "activity",
  activity: "activity",
  remote_shell: "shell",
  shell: "shell",
  remote_desktop: "vnc",
  vnc: "vnc",
});

const SUMMARY_SECTIONS = [
  { key: "top-level", label: "Top-Level", icon: InfoOutlinedIcon },
  { key: "storage", label: "Storage", icon: StorageRoundedIcon },
  { key: "memory", label: "Memory", icon: MemoryRoundedIcon },
  { key: "network", label: "Network", icon: LanRoundedIcon },
];

function getWireguardTunnelPresentation(tunnelInfo) {
  const peerIp = tunnelInfo?.virtual_ip ? String(tunnelInfo.virtual_ip).split("/")[0] : "";
  const tunnelState = String(tunnelInfo?.status || "").toLowerCase();
  const recoveryInProgress = Boolean(tunnelInfo?.recovery_in_progress);
  const listenerHealthy = tunnelInfo?.listener_healthy !== false;
  const isTunnelOnline =
    tunnelState === "up" &&
    listenerHealthy &&
    Boolean(peerIp);
  const statusText = recoveryInProgress ? "Recovering" : isTunnelOnline ? "Online" : "Offline";
  const statusColor = recoveryInProgress
    ? DEVICE_LIST_STATUS_COLORS.recovering
    : isTunnelOnline
      ? DEVICE_LIST_STATUS_COLORS.online
      : DEVICE_LIST_STATUS_COLORS.offline;
  return { peerIp, statusText, statusColor, isTunnelOnline };
}

const myTheme = themeQuartz.withParams({
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

const gridThemeClass = myTheme.themeName || "ag-theme-quartz";

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

const GridShell = ({ children, sx }) => (
  <Box className={gridThemeClass} sx={{ ...GRID_SHELL_BASE_SX, ...(sx || {}) }}>
    {children}
  </Box>
);

const SUMMARY_GRID_STYLE = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
};
const SUMMARY_FIELD_TEXT_COLOR = "#58a6ff"; // matches hostname blue in Device_List.jsx
const SUMMARY_DEFAULT_TEXT_COLOR = NAV_TAB_COLORS.textActive;
const UNABLE_TO_RETRIEVE_SN = "<Unable to Retrieve S/N>";
const STORAGE_USAGE_ALERT_THRESHOLD_PCT = 90;
const STORAGE_USAGE_ALERT_LABEL = `Usage Exceeding ${STORAGE_USAGE_ALERT_THRESHOLD_PCT}%`;
const STORAGE_USAGE_ALERT_COLOR = "#facc15";

const isStorageUsageAlert = (usageValue) =>
  typeof usageValue === "number" &&
  !Number.isNaN(usageValue) &&
  usageValue > STORAGE_USAGE_ALERT_THRESHOLD_PCT;

const formatHostnameForDisplay = (value) => {
  const text = typeof value === "string" ? value.trim() : value == null ? "" : String(value).trim();
  if (!text) return "";
  const dotIndex = text.indexOf(".");
  return dotIndex > 0 ? text.slice(0, dotIndex) : text;
};

const SUMMARY_GRID_DEBUG_STORAGE_KEY = "borealis.debug.summaryGrid";

const isSummaryGridDebugEnabled = () => {
  try {
    if (typeof window === "undefined") return false;
    const params = new URLSearchParams(window.location.search || "");
    const queryValue = String(params.get("debugSummaryGrid") || "").toLowerCase();
    if (queryValue === "1" || queryValue === "true") return true;
    return window.localStorage?.getItem(SUMMARY_GRID_DEBUG_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
};

const summaryGridDebugLog = (...parts) => {
  if (!isSummaryGridDebugEnabled()) return;
  const stamp = new Date().toISOString();
  console.debug(`[DeviceDetails][SummaryDebug][${stamp}]`, ...parts);
};

const summarySectionRowId = (params) => params.data?.id ?? params.rowIndex;

const SummarySectionGrid = React.memo(function SummarySectionGrid({
  sectionKey,
  rowData,
  columnDefs,
  defaultColDef,
  height,
}) {
  const renderCountRef = useRef(0);
  const modelUpdateCountRef = useRef(0);
  renderCountRef.current += 1;

  const rowCount = Array.isArray(rowData) ? rowData.length : 0;
  const summaryDefaultColDef = useMemo(() => {
    const baseColDef = defaultColDef || {};
    const existingCellStyle = baseColDef.cellStyle;
    return {
      ...baseColDef,
      cellStyle: (params) => {
        const resolvedStyle =
          typeof existingCellStyle === "function"
            ? existingCellStyle(params)
            : existingCellStyle || {};
        return {
          ...(resolvedStyle || {}),
          color: SUMMARY_DEFAULT_TEXT_COLOR,
        };
      },
    };
  }, [defaultColDef]);

  const handleGridReady = useCallback(
    (params) => {
      summaryGridDebugLog("gridReady", {
        sectionKey,
        displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
        rowCount,
      });
    },
    [sectionKey, rowCount]
  );

  const handleFirstDataRendered = useCallback(
    (params) => {
      summaryGridDebugLog("firstDataRendered", {
        sectionKey,
        displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
      });
    },
    [sectionKey]
  );

  const handleRowDataUpdated = useCallback(
    (params) => {
      summaryGridDebugLog("rowDataUpdated", {
        sectionKey,
        displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
        rowCount,
      });
    },
    [sectionKey, rowCount]
  );

  const handleModelUpdated = useCallback(
    (params) => {
      modelUpdateCountRef.current += 1;
      const updateCount = modelUpdateCountRef.current;
      if (updateCount <= 12 || updateCount % 25 === 0) {
        summaryGridDebugLog("modelUpdated", {
          sectionKey,
          updateCount,
          displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
        });
      }
    },
    [sectionKey]
  );

  useEffect(() => {
    summaryGridDebugLog("mount", { sectionKey });
    return () => summaryGridDebugLog("unmount", { sectionKey });
  }, [sectionKey]);

  useEffect(() => {
    summaryGridDebugLog("render", {
      sectionKey,
      renderCount: renderCountRef.current,
      rowCount,
      height,
    });
  });

  return (
    <GridShell sx={{ height }}>
      <AgGridReact
        rowData={rowData}
        columnDefs={columnDefs}
        defaultColDef={summaryDefaultColDef}
        pagination={false}
        animateRows={false}
        suppressCellFocus
        onGridReady={handleGridReady}
        onFirstDataRendered={handleFirstDataRendered}
        onRowDataUpdated={handleRowDataUpdated}
        onModelUpdated={handleModelUpdated}
        getRowId={summarySectionRowId}
        theme={myTheme}
        style={SUMMARY_GRID_STYLE}
      />
    </GridShell>
  );
});

const SummaryGridPlaceholder = React.memo(function SummaryGridPlaceholder({ height }) {
  return (
    <GridShell
      sx={{
        height,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, letterSpacing: 0.2 }}>
        Loading telemetry...
      </Typography>
    </GridShell>
  );
});

const SummarySectionsNav = React.memo(function SummarySectionsNav({ onSelectSection }) {
  const [activeSectionKey, setActiveSectionKey] = useState(SUMMARY_SECTIONS[0]?.key || "top-level");
  const observerEventCountRef = useRef(0);

  const handleSelect = useCallback(
    (sectionKey) => {
      summaryGridDebugLog("navClick", { sectionKey });
      setActiveSectionKey(sectionKey);
      onSelectSection?.(sectionKey);
    },
    [onSelectSection]
  );

  useEffect(() => {
    if (typeof window === "undefined" || typeof IntersectionObserver === "undefined") return undefined;
    const sectionElements = SUMMARY_SECTIONS
      .map((section) => document.getElementById(`device-summary-${section.key}`))
      .filter(Boolean);
    if (!sectionElements.length) return undefined;

    summaryGridDebugLog("navObserverStart", {
      sections: sectionElements.map((el) => el.id),
    });

    const observer = new IntersectionObserver(
      (entries) => {
        observerEventCountRef.current += 1;
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio);
        if (!visible.length) return;
        const nextKey = String(visible[0].target.id || "").replace("device-summary-", "");
        if (!nextKey) return;
        setActiveSectionKey((prev) => {
          if (prev === nextKey) return prev;
          summaryGridDebugLog("navObserverSectionChange", {
            from: prev,
            to: nextKey,
            eventCount: observerEventCountRef.current,
            ratio: Number(visible[0].intersectionRatio || 0).toFixed(3),
          });
          return nextKey;
        });
      },
      {
        root: null,
        rootMargin: "-120px 0px -46% 0px",
        threshold: [0.15, 0.35, 0.6],
      }
    );

    sectionElements.forEach((el) => observer.observe(el));
    return () => {
      summaryGridDebugLog("navObserverStop");
      observer.disconnect();
    };
  }, []);

  return (
    <Box
      id="device-summary-sections-nav"
      sx={{
        position: { xs: "static", lg: "sticky" },
        top: { lg: 0 },
        mt: { lg: 0 },
        alignSelf: "start",
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background:
          "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), rgba(15,20,28,0.92)",
        backdropFilter: "blur(8px) saturate(130%)",
        overflow: "hidden",
        width: 220,
        minWidth: 220,
        flexShrink: 0,
      }}
    >
      <Box sx={{ px: 1.4, py: 1 }}>
        <Typography
          sx={{
            fontSize: "0.72rem",
            color: "#7db7ff",
            fontWeight: 700,
            letterSpacing: 0.35,
            textTransform: "uppercase",
          }}
        >
          Summary Sections
        </Typography>
      </Box>
      <Box sx={{ py: 0.25 }}>
        {SUMMARY_SECTIONS.map((section) => {
          const active = activeSectionKey === section.key;
          const SectionIcon = section.icon;
          return (
            <Button
              key={section.key}
              onClick={() => handleSelect(section.key)}
              startIcon={<SectionIcon sx={{ fontSize: 18 }} />}
              sx={{
                width: "100%",
                justifyContent: "flex-start",
                textTransform: "none",
                minHeight: NAV_TAB_HEIGHT,
                height: NAV_TAB_HEIGHT,
                py: 0.35,
                px: 1.6,
                borderRadius: 0,
                fontFamily: "inherit",
                fontSize: "0.8rem",
                fontWeight: active ? 600 : 400,
                color: active ? NAV_TAB_COLORS.textActive : NAV_TAB_COLORS.text,
                position: "relative",
                background: active ? SUMMARY_SECTION_ACTIVE_BG : "transparent",
                transition: "background 160ms ease, color 160ms ease, transform 120ms ease",
                "& .MuiButton-startIcon": {
                  color: active ? NAV_TAB_COLORS.iconActive : NAV_TAB_COLORS.icon,
                  mr: 0.9,
                  transition: "color 160ms ease",
                },
                "&:hover": {
                  background: active ? SUMMARY_SECTION_ACTIVE_BG : NAV_TAB_COLORS.hover,
                },
                "&:active": {
                  transform: "translateY(0.5px)",
                },
              }}
            >
              {section.label}
            </Button>
          );
        })}
      </Box>
    </Box>
  );
});

const HISTORY_STATUS_THEME = {
  running: {
    text: "#58a6ff",
    background: "rgba(88,166,255,0.15)",
    border: "1px solid rgba(88,166,255,0.4)",
    dot: "#58a6ff",
  },
  success: {
    text: "#00d18c",
    background: "rgba(0,209,140,0.16)",
    border: "1px solid rgba(0,209,140,0.35)",
    dot: "#00d18c",
  },
  failed: {
    text: "#ff7b89",
    background: "rgba(255,123,137,0.16)",
    border: "1px solid rgba(255,123,137,0.35)",
    dot: "#ff7b89",
  },
  default: {
    text: "#e2e8f0",
    background: "rgba(226,232,240,0.12)",
    border: "1px solid rgba(226,232,240,0.25)",
    dot: "#e2e8f0",
  },
};

const StatusPillCell = React.memo(function StatusPillCell(props) {
  const value = String(props?.value || "");
  if (!value) return null;
  const theme = HISTORY_STATUS_THEME[value.toLowerCase()] || HISTORY_STATUS_THEME.default;
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
        backgroundColor: theme.background,
        border: theme.border,
        color: theme.text,
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
          backgroundColor: theme.dot,
          boxShadow: "0 0 0 2px rgba(0, 0, 0, 0.22)",
        }}
      />
      {value}
    </Box>
  );
});

const HistoryActionsCell = React.memo(function HistoryActionsCell(props) {
  const row = props.data || {};
  const onViewOutput = props.context?.onViewOutput;

  return (
    <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
      {row.has_stdout ? (
        <Button
          size="small"
          sx={{ color: MAGIC_UI.accentA, textTransform: "none", minWidth: 0, p: 0 }}
          onClick={() => onViewOutput && onViewOutput(row, "stdout")}
        >
          StdOut
        </Button>
      ) : null}
      {row.has_stderr ? (
        <Button
          size="small"
          sx={{ color: "#ff7b89", textTransform: "none", minWidth: 0, p: 0 }}
          onClick={() => onViewOutput && onViewOutput(row, "stderr")}
        >
          StdErr
        </Button>
      ) : null}
    </Box>
  );
});

const GRID_COMPONENTS = {
  StatusPillCell,
  HistoryActionsCell,
};

export default function DeviceDetails({ device, onBack, onQuickJobLaunch, onPageMetaChange }) {
  const initialTabIndex = useMemo(() => {
    try {
      const params = new URLSearchParams(window.location.search || "");
      const tabRaw = (params.get("tab") || "").trim().toLowerCase();
      if (!tabRaw) return 0;
      const internalTabKey = DEVICE_DETAILS_TAB_KEY_BY_URL[tabRaw] || null;
      if (!internalTabKey) return 0;
      const matchIndex = TOP_TABS.findIndex((tabDef) => tabDef.key === internalTabKey);
      return matchIndex >= 0 ? matchIndex : 0;
    } catch {
      return 0;
    }
  }, []);
  const [tab, setTab] = useState(initialTabIndex);
  const [agent, setAgent] = useState(device || {});
  const [details, setDetails] = useState({});
  const [meta, setMeta] = useState({});
  const [softwareSearch, setSoftwareSearch] = useState("");
  const [description, setDescription] = useState("");
  const [connectionType, setConnectionType] = useState("");
  const [connectionEndpoint, setConnectionEndpoint] = useState("");
  const [connectionDraft, setConnectionDraft] = useState("");
  const [connectionSaving, setConnectionSaving] = useState(false);
  const [connectionMessage, setConnectionMessage] = useState("");
  const [connectionError, setConnectionError] = useState("");
  const [summaryDataReady, setSummaryDataReady] = useState(false);
  const [summaryScrollOffset, setSummaryScrollOffset] = useState(0);
  const [summaryBottomSpacer, setSummaryBottomSpacer] = useState(0);
  const [tunnelInfo, setTunnelInfo] = useState({
    status: "idle",
    tunnel_id: "",
    virtual_ip: "",
    agent_socket: false,
    listener_healthy: false,
    recovery_in_progress: false,
    last_recovery_attempt_at: null,
    last_recovery_attempt_at_iso: "",
  });
  const [historyRows, setHistoryRows] = useState([]);
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputContent, setOutputContent] = useState("");
  const [outputLang, setOutputLang] = useState("powershell");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [assemblyNameMap, setAssemblyNameMap] = useState({});
  // Snapshotted status for the lifetime of this page
  const [lockedStatus, setLockedStatus] = useState(() => {
    // Prefer status provided by the device list row if available
    if (device?.status) return device.status;
    // Fallback: compute once from the provided lastSeen timestamp
    const tsSec = device?.lastSeen;
    if (!tsSec) return "Offline";
    const now = Date.now() / 1000;
    return now - tsSec <= 300 ? "Online" : "Offline";
  });
  const pageRenderCountRef = useRef(0);
  pageRenderCountRef.current += 1;
  const summary = details.summary || {};

  useEffect(() => {
    const activeTabKey = TOP_TABS[tab]?.key || "";
    if (!activeTabKey) return;
    const urlTabKey = DEVICE_DETAILS_TAB_URL_BY_KEY[activeTabKey] || activeTabKey;
    try {
      const params = new URLSearchParams(window.location.search || "");
      if ((params.get("tab") || "").trim().toLowerCase() === urlTabKey) {
        return;
      }
      params.set("tab", urlTabKey);
      const query = params.toString();
      const nextLocation = query ? `${window.location.pathname}?${query}` : window.location.pathname;
      const currentLocation = window.location.pathname + window.location.search;
      if (nextLocation !== currentLocation) {
        window.history.replaceState({}, "", nextLocation);
      }
    } catch {
      /* URL update failures are non-blocking */
    }
  }, [tab]);

  useEffect(() => {
    if (!isSummaryGridDebugEnabled()) return;
    summaryGridDebugLog("debugEnabled", {
      queryParam: "debugSummaryGrid=1",
      storageKey: SUMMARY_GRID_DEBUG_STORAGE_KEY,
    });
  }, []);
  const tunnelDevice = useMemo(
    () => ({
      ...(device || {}),
      ...(agent || {}),
      summary,
      hostname: meta.hostname || summary.hostname || device?.hostname || agent?.hostname,
      agent_id: meta.agentId || summary.agent_id || agent?.agent_id || agent?.id || device?.agent_id || device?.agent_guid,
      agent_guid: meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || agent?.agent_guid || agent?.guid,
    }),
    [agent, device, meta.agentGuid, meta.agentId, meta.hostname, summary]
  );
  const tunnelAgentId = useMemo(() => {
    const raw = tunnelDevice?.agent_id ?? "";
    try {
      return String(raw).trim();
    } catch {
      return "";
    }
  }, [tunnelDevice]);
  const quickJobTargets = useMemo(() => {
    const values = [];
    const push = (value) => {
      const normalized = typeof value === "string" ? value.trim() : "";
      if (!normalized) return;
      if (!values.includes(normalized)) values.push(normalized);
    };
    push(agent?.hostname);
    push(device?.hostname);
    return values;
  }, [agent, device]);
  const canLaunchQuickJob = quickJobTargets.length > 0 && typeof onQuickJobLaunch === "function";
  const tunnelIndicators = useMemo(() => {
    const items = [];
    const { peerIp, statusText, statusColor } = getWireguardTunnelPresentation(tunnelInfo);
    items.push({
      key: "peer-ip",
      label: "WireGuard Peer IP",
      value: `${statusText} - ${peerIp || "Inactive"}`,
      color: statusColor,
      icon: <LanRoundedIcon sx={{ fontSize: 16 }} />,
    });
    return items;
  }, [tunnelInfo]);

  useEffect(() => {
    setConnectionError("");
  }, [connectionDraft]);

  useEffect(() => {
    if (connectionType !== "ssh") {
      setConnectionMessage("");
      setConnectionError("");
    }
  }, [connectionType]);

  useEffect(() => {
    let canceled = false;
    let inFlight = false;
    let pollingTimer = null;
    if (!tunnelAgentId) {
      setTunnelInfo({
        status: "idle",
        tunnel_id: "",
        virtual_ip: "",
        agent_socket: false,
        listener_healthy: false,
        recovery_in_progress: false,
        last_recovery_attempt_at: null,
        last_recovery_attempt_at_iso: "",
      });
      return () => {};
    }
    const loadTunnelStatus = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const resp = await fetch(
          `/api/tunnel/status?agent_id=${encodeURIComponent(tunnelAgentId)}`
        );
        const data = await resp.json().catch(() => ({}));
        if (canceled) return;
        if (!resp.ok) {
          setTunnelInfo({
            status: "error",
            tunnel_id: "",
            virtual_ip: "",
            agent_socket: false,
            listener_healthy: false,
            recovery_in_progress: false,
            last_recovery_attempt_at: null,
            last_recovery_attempt_at_iso: "",
          });
          return;
        }
        if (data?.status === "down") {
          setTunnelInfo({
            status: "down",
            tunnel_id: "",
            virtual_ip: "",
            agent_socket: Boolean(data?.agent_socket),
            listener_healthy: Boolean(data?.listener_healthy),
            recovery_in_progress: Boolean(data?.recovery_in_progress),
            last_recovery_attempt_at: data?.last_recovery_attempt_at ?? null,
            last_recovery_attempt_at_iso: data?.last_recovery_attempt_at_iso || "",
          });
          return;
        }
        setTunnelInfo({
          status: data?.status || "up",
          tunnel_id: data?.tunnel_id || "",
          virtual_ip: data?.virtual_ip || "",
          agent_socket: Boolean(data?.agent_socket),
          listener_healthy: data?.listener_healthy !== false,
          recovery_in_progress: Boolean(data?.recovery_in_progress),
          last_recovery_attempt_at: data?.last_recovery_attempt_at ?? null,
          last_recovery_attempt_at_iso: data?.last_recovery_attempt_at_iso || "",
        });
      } catch {
        if (!canceled) {
          setTunnelInfo({
            status: "error",
            tunnel_id: "",
            virtual_ip: "",
            agent_socket: false,
            listener_healthy: false,
            recovery_in_progress: false,
            last_recovery_attempt_at: null,
            last_recovery_attempt_at_iso: "",
          });
        }
      } finally {
        inFlight = false;
      }
    };
    loadTunnelStatus();
    pollingTimer = setInterval(loadTunnelStatus, TUNNEL_STATUS_POLL_INTERVAL_MS);
    return () => {
      canceled = true;
      if (pollingTimer) clearInterval(pollingTimer);
    };
  }, [tunnelAgentId]);

  useEffect(() => {
    let canceled = false;
    const loadAssemblyNames = async () => {
      const next = {};
      const storeName = (rawPath, rawName) => {
        const name = typeof rawName === "string" ? rawName.trim() : "";
        if (!name) return;
        const normalizedPath = String(rawPath || "")
          .replace(/\\/g, "/")
          .replace(/^\/+/, "")
          .trim();
        if (!normalizedPath) return;
        if (!next[normalizedPath]) next[normalizedPath] = name;
        const base = normalizedPath.split("/").pop() || "";
        if (base && !next[base]) next[base] = name;
        const dot = base.lastIndexOf(".");
        if (dot > 0) {
          const baseNoExt = base.slice(0, dot);
          if (baseNoExt && !next[baseNoExt]) next[baseNoExt] = name;
        }
      };
      try {
        const resp = await fetch("/api/assemblies");
        if (!resp.ok) return;
        const data = await resp.json();
        const items = Array.isArray(data?.items) ? data.items : [];
        items.forEach((item) => {
          if (!item || typeof item !== "object") return;
          const displayName =
            (item.display_name || "").trim() ||
            item.assembly_guid ||
            "";
          if (!displayName) return;
          storeName(item.virtual_path || item.path || "", displayName);
          if (item.assembly_guid && !next[item.assembly_guid]) {
            next[item.assembly_guid] = displayName;
          }
          if (item.payload_guid && !next[item.payload_guid]) {
            next[item.payload_guid] = displayName;
          }
        });
      } catch {
        // ignore failures; map remains partial
      }
      if (!canceled) {
        setAssemblyNameMap(next);
      }
    };
    loadAssemblyNames();
    return () => {
      canceled = true;
    };
  }, []);

  const statusFromHeartbeat = (tsSec, offlineAfter = 300) => {
    if (!tsSec) return "Offline";
    const now = Date.now() / 1000;
    return now - tsSec <= offlineAfter ? "Online" : "Offline";
  };

  const resolveAssemblyName = useCallback((scriptName, scriptPath) => {
    const normalized = String(scriptPath || "").replace(/\\/g, "/").trim();
    const base = normalized ? normalized.split("/").pop() || "" : "";
    const baseNoExt = base && base.includes(".") ? base.slice(0, base.lastIndexOf(".")) : base;
    return (
      assemblyNameMap[normalized] ||
      (base ? assemblyNameMap[base] : "") ||
      (baseNoExt ? assemblyNameMap[baseNoExt] : "") ||
      scriptName ||
      base ||
      scriptPath ||
      ""
    );
  }, [assemblyNameMap]);

  useEffect(() => {
    let canceled = false;
    if (device) {
      setLockedStatus(device.status || statusFromHeartbeat(device.lastSeen));
    }

    const guid = device?.agent_guid || device?.guid || device?.agentGuid || device?.summary?.agent_guid;
    const agentId = device?.agentId || device?.summary?.agent_id || device?.id;
    const hostname = device?.hostname || device?.summary?.hostname;
    if (!device || (!guid && !hostname)) {
      setSummaryDataReady(true);
      return () => {
        canceled = true;
      };
    }

    setSummaryDataReady(false);

    const load = async () => {
      try {
        const agentsPromise = fetch("/api/agents").catch(() => null);
        let detailResponse = null;
        if (guid) {
          try {
            detailResponse = await fetch(`/api/devices/${encodeURIComponent(guid)}`);
          } catch (err) {
            detailResponse = null;
          }
        }
        if ((!detailResponse || !detailResponse.ok) && hostname) {
          try {
            detailResponse = await fetch(`/api/device/details/${encodeURIComponent(hostname)}`);
          } catch (err) {
            detailResponse = null;
          }
        }
        if (!detailResponse || !detailResponse.ok) {
          throw new Error(`Failed to load device record (${detailResponse ? detailResponse.status : 'no response'})`);
        }

        const [agentsData, detailData] = await Promise.all([
          agentsPromise?.then((r) => (r ? r.json() : {})).catch(() => ({})),
          detailResponse.json(),
        ]);
        if (canceled) return;

        if (agentsData && agentId && agentsData[agentId]) {
          setAgent({ id: agentId, ...agentsData[agentId] });
        }

        const summary =
          detailData?.summary && typeof detailData.summary === "object"
            ? detailData.summary
            : (detailData?.details?.summary || {});
        const normalizedSummary = { ...(summary || {}) };
        if (detailData?.description) {
          normalizedSummary.description = detailData.description;
        }

        const connectionTypeValue =
          (normalizedSummary.connection_type ||
            normalizedSummary.remote_type ||
            "").toLowerCase();
        const connectionEndpointValue =
          normalizedSummary.connection_endpoint ||
          normalizedSummary.connection_address ||
          detailData?.connection_endpoint ||
          "";
        setConnectionType(connectionTypeValue);
        setConnectionEndpoint(connectionEndpointValue);
        setConnectionDraft(connectionEndpointValue);
        setConnectionMessage("");
        setConnectionError("");

        const normalized = {
          summary: normalizedSummary,
          memory: Array.isArray(detailData?.memory)
            ? detailData.memory
            : Array.isArray(detailData?.details?.memory)
              ? detailData.details.memory
              : [],
          network: Array.isArray(detailData?.network)
            ? detailData.network
            : Array.isArray(detailData?.details?.network)
              ? detailData.details.network
              : [],
          software: Array.isArray(detailData?.software)
            ? detailData.software
            : Array.isArray(detailData?.details?.software)
              ? detailData.details.software
              : [],
          storage: Array.isArray(detailData?.storage)
            ? detailData.storage
            : Array.isArray(detailData?.details?.storage)
              ? detailData.details.storage
              : [],
          cpu: detailData?.cpu || detailData?.details?.cpu || {},
        };
        const readNonEmpty = (...values) => {
          for (const value of values) {
            if (value === undefined || value === null) continue;
            const text = String(value).trim();
            if (text) return text;
          }
          return "";
        };
        const cpuIdentity = normalized.cpu && typeof normalized.cpu === "object" ? normalized.cpu : {};
        const manufacturerValue = readNonEmpty(
          detailData?.manufacturer,
          normalizedSummary.manufacturer,
          normalizedSummary.vendor,
          detailData?.details?.summary?.manufacturer,
          detailData?.details?.summary?.vendor,
          cpuIdentity.system_manufacturer
        );
        const systemModelValue = readNonEmpty(
          detailData?.system_model,
          normalizedSummary.system_model,
          normalizedSummary.device_model,
          detailData?.details?.summary?.system_model,
          detailData?.details?.summary?.device_model,
          cpuIdentity.system_model_raw
        );
        const combinedModelValue =
          readNonEmpty(
            detailData?.model,
            normalizedSummary.model,
            detailData?.details?.summary?.model,
            cpuIdentity.system_model
          ) || [manufacturerValue, systemModelValue].filter(Boolean).join(" ").trim();
        const serialNumberValue =
          readNonEmpty(
            detailData?.serial_number,
            normalizedSummary.serial_number,
            normalizedSummary.serial,
            normalizedSummary.bios_serial,
            detailData?.details?.summary?.serial_number,
            detailData?.details?.summary?.serial,
            detailData?.details?.summary?.bios_serial,
            cpuIdentity.system_serial_number,
            cpuIdentity.serial_number,
            detailData?.asset_tag,
            normalizedSummary.asset_tag
          ) || UNABLE_TO_RETRIEVE_SN;
        if (!normalizedSummary.manufacturer && manufacturerValue) {
          normalizedSummary.manufacturer = manufacturerValue;
        }
        if (!normalizedSummary.system_model && systemModelValue) {
          normalizedSummary.system_model = systemModelValue;
        }
        if (!normalizedSummary.device_model && systemModelValue) {
          normalizedSummary.device_model = systemModelValue;
        }
        if (!normalizedSummary.model && combinedModelValue) {
          normalizedSummary.model = combinedModelValue;
        }
        if (!normalizedSummary.serial_number && serialNumberValue) {
          normalizedSummary.serial_number = serialNumberValue;
        }
        if (!normalizedSummary.serial && serialNumberValue) {
          normalizedSummary.serial = serialNumberValue;
        }
        if (!normalizedSummary.bios_serial && serialNumberValue) {
          normalizedSummary.bios_serial = serialNumberValue;
        }
        if (normalized.cpu && typeof normalized.cpu === "object") {
          if (manufacturerValue && !normalized.cpu.system_manufacturer) {
            normalized.cpu.system_manufacturer = manufacturerValue;
          }
          if (systemModelValue && !normalized.cpu.system_model_raw) {
            normalized.cpu.system_model_raw = systemModelValue;
          }
          if (combinedModelValue && !normalized.cpu.system_model) {
            normalized.cpu.system_model = combinedModelValue;
          }
          if (serialNumberValue && !normalized.cpu.system_serial_number) {
            normalized.cpu.system_serial_number = serialNumberValue;
          }
        }
        setDetails(normalized);

        const toYmdHms = (dateObj) => {
          if (!dateObj || Number.isNaN(dateObj.getTime())) return '';
          const pad = (v) => String(v).padStart(2, '0');
          return `${dateObj.getUTCFullYear()}-${pad(dateObj.getUTCMonth() + 1)}-${pad(dateObj.getUTCDate())} ${pad(dateObj.getUTCHours())}:${pad(dateObj.getUTCMinutes())}:${pad(dateObj.getUTCSeconds())}`;
        };

        let createdDisplay = normalizedSummary.created || '';
        if (!createdDisplay) {
          if (detailData?.created_at && Number(detailData.created_at)) {
            createdDisplay = toYmdHms(new Date(Number(detailData.created_at) * 1000));
          } else if (detailData?.created_at_iso) {
            createdDisplay = toYmdHms(new Date(detailData.created_at_iso));
          }
        }

        const metaPayload = {
          hostname: detailData?.hostname || normalizedSummary.hostname || hostname || "",
          lastUser: detailData?.last_user || normalizedSummary.last_user || "",
          deviceType: detailData?.device_type || normalizedSummary.device_type || "",
          created: createdDisplay,
          createdAtIso: detailData?.created_at_iso || "",
          lastSeen: detailData?.last_seen || normalizedSummary.last_seen || 0,
          lastReboot: detailData?.last_reboot || normalizedSummary.last_reboot || "",
          operatingSystem:
            detailData?.operating_system || normalizedSummary.operating_system || normalizedSummary.agent_operating_system || "",
          agentId: detailData?.agent_id || normalizedSummary.agent_id || agentId || "",
          agentGuid: detailData?.agent_guid || normalizedSummary.agent_guid || guid || "",
          agentBuildId:
            detailData?.agent_build_id ||
            detailData?.agent_hash ||
            normalizedSummary.agent_build_id ||
            normalizedSummary.agent_hash ||
            "",
          agentVersionStatus:
            detailData?.agent_version_status ||
            normalizedSummary.agent_version_status ||
            detailData?.details?.summary?.agent_version_status ||
            "Needs Updated",
          internalIp: detailData?.internal_ip || normalizedSummary.internal_ip || "",
          externalIp: detailData?.external_ip || normalizedSummary.external_ip || "",
          manufacturer: manufacturerValue || "",
          systemModel: systemModelValue || "",
          model: combinedModelValue || "",
          serialNumber: serialNumberValue || UNABLE_TO_RETRIEVE_SN,
          siteId: detailData?.site_id,
          siteName: detailData?.site_name || "",
          siteDescription: detailData?.site_description || "",
          status: detailData?.status || "",
          connectionType: connectionTypeValue,
          connectionEndpoint: connectionEndpointValue,
        };
        setMeta(metaPayload);
        setDescription(normalizedSummary.description || detailData?.description || "");

        setAgent((prev) => ({
          ...(prev || {}),
          id: agentId || prev?.id,
          hostname: metaPayload.hostname || prev?.hostname,
          agent_hash: metaPayload.agentBuildId || prev?.agent_hash,
          agent_build_id: metaPayload.agentBuildId || prev?.agent_build_id,
          agent_operating_system: metaPayload.operatingSystem || prev?.agent_operating_system,
          device_type: metaPayload.deviceType || prev?.device_type,
          last_seen: metaPayload.lastSeen || prev?.last_seen,
        }));

        if (metaPayload.status) {
          setLockedStatus(metaPayload.status);
        } else if (metaPayload.lastSeen) {
          setLockedStatus(statusFromHeartbeat(metaPayload.lastSeen));
        }
      } catch (e) {
        if (canceled) return;
        console.warn("Failed to load device info", e);
        setMeta({});
      } finally {
        if (!canceled) {
          setSummaryDataReady(true);
        }
      }
    };
    load();
    return () => {
      canceled = true;
    };
  }, [device]);

  const activityHostname = useMemo(() => {
    return (meta?.hostname || agent?.hostname || device?.hostname || "").trim();
  }, [meta?.hostname, agent?.hostname, device?.hostname]);

  const saveConnectionEndpoint = useCallback(async () => {
    if (connectionType !== "ssh") return;
    const host = activityHostname;
    if (!host) return;
    const trimmed = connectionDraft.trim();
    if (!trimmed) {
      setConnectionError("Address is required.");
      return;
    }
    if (trimmed === connectionEndpoint.trim()) {
      setConnectionMessage("No changes to save.");
      return;
    }
    setConnectionSaving(true);
    setConnectionError("");
    setConnectionMessage("");
    try {
      const resp = await fetch(`/api/ssh_devices/${encodeURIComponent(host)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ address: trimmed })
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      const updated = data?.device?.connection_endpoint || trimmed;
      setConnectionEndpoint(updated);
      setConnectionDraft(updated);
      setMeta((prev) => ({ ...(prev || {}), connectionEndpoint: updated }));
      setConnectionMessage("SSH endpoint updated.");
      setTimeout(() => setConnectionMessage(""), 3000);
    } catch (err) {
      setConnectionError(String(err.message || err));
    } finally {
      setConnectionSaving(false);
    }
  }, [connectionType, connectionDraft, connectionEndpoint, activityHostname]);

  const loadHistory = useCallback(async () => {
    if (!activityHostname) return;
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(activityHostname)}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      setHistoryRows(data.history || []);
    } catch (e) {
      console.warn("Failed to load activity history", e);
      setHistoryRows([]);
    }
  }, [activityHostname]);

  useEffect(() => { loadHistory(); }, [loadHistory]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !activityHostname) return undefined;

    let refreshTimer = null;
    const normalizedHost = activityHostname.toLowerCase();
    const scheduleRefresh = (delay = 200) => {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        loadHistory();
      }, delay);
    };

    const handleActivityChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      if (!payloadHost) return;
      if (payloadHost === normalizedHost) {
        const delay = payload?.change === "updated" ? 150 : 0;
        scheduleRefresh(delay);
      }
    };

    socket.on("device_activity_changed", handleActivityChanged);

    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      socket.off("device_activity_changed", handleActivityChanged);
    };
  }, [activityHostname, loadHistory]);

  // No explicit live recap tab; recaps are recorded into Activity History

  const clearHistory = async () => {
    if (!activityHostname) return;
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(activityHostname)}`, { method: "DELETE" });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setHistoryRows([]);
    } catch (e) {
      console.warn("Failed to clear activity history", e);
    }
  };

  const saveDescription = async () => {
    const targetHost = meta.hostname || details.summary?.hostname;
    if (!targetHost) return;
    try {
      await fetch(`/api/device/description/${targetHost}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description })
      });
      setDetails((d) => ({
        ...d,
        summary: { ...(d.summary || {}), description }
      }));
      setMeta((m) => ({ ...(m || {}), hostname: targetHost }));
    } catch (e) {
      console.warn("Failed to save description", e);
    }
  };

  const formatMac = (mac) => (mac ? mac.replace(/-/g, ":").toUpperCase() : "unknown");

  const formatBytes = (val) => {
    if (val === undefined || val === null || val === "unknown") return "unknown";
    let num = Number(val);
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (num >= 1024 && i < units.length - 1) {
      num /= 1024;
      i++;
    }
    return `${num.toFixed(1)} ${units[i]}`;
  };

  const formatTimestamp = useCallback((epochSec) => {
    const ts = Number(epochSec || 0);
    if (!ts) return "unknown";
    const d = new Date(ts * 1000);
    const mm = String(d.getMonth() + 1).padStart(2, "0");
    const dd = String(d.getDate()).padStart(2, "0");
    const yyyy = d.getFullYear();
    let hh = d.getHours();
    const ampm = hh >= 12 ? "PM" : "AM";
    hh = hh % 12 || 12;
    const min = String(d.getMinutes()).padStart(2, "0");
    return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
  }, []);

  const formatDateValue = useCallback((rawValue, emptyFallback = "unknown") => {
    if (rawValue === undefined || rawValue === null || String(rawValue).trim() === "") {
      return emptyFallback;
    }
    const rawText = String(rawValue).trim();
    const asNumber = Number(rawText);
    if (Number.isFinite(asNumber) && /^[0-9]+(\.[0-9]+)?$/.test(rawText)) {
      const ms = asNumber > 1e12 ? asNumber : asNumber * 1000;
      const date = new Date(ms);
      if (!Number.isNaN(date.getTime())) {
        const mm = String(date.getMonth() + 1).padStart(2, "0");
        const dd = String(date.getDate()).padStart(2, "0");
        const yyyy = date.getFullYear();
        let hh = date.getHours();
        const ampm = hh >= 12 ? "PM" : "AM";
        hh = hh % 12 || 12;
        const min = String(date.getMinutes()).padStart(2, "0");
        return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
      }
    }
    const date = new Date(rawText);
    if (!Number.isNaN(date.getTime())) {
      const mm = String(date.getMonth() + 1).padStart(2, "0");
      const dd = String(date.getDate()).padStart(2, "0");
      const yyyy = date.getFullYear();
      let hh = date.getHours();
      const ampm = hh >= 12 ? "PM" : "AM";
      hh = hh % 12 || 12;
      const min = String(date.getMinutes()).padStart(2, "0");
      return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
    }
    return rawText;
  }, []);

  const scrollToSummarySection = useCallback((sectionKey) => {
    if (typeof document === "undefined" || typeof window === "undefined") return;
    const target = document.getElementById(`device-summary-${sectionKey}`);
    if (!target) return;
    const nav = document.getElementById("device-summary-sections-nav");
    if (!nav) return;

    const docScroller = document.scrollingElement || document.documentElement;
    let scrollHost = docScroller;
    let node = target.parentElement;
    while (node && node !== document.body) {
      const styles = window.getComputedStyle(node);
      const overflowY = String(styles.overflowY || "").toLowerCase();
      const overflow = String(styles.overflow || "").toLowerCase();
      const canScroll = node.scrollHeight > node.clientHeight + 1;
      const allowsScroll =
        /(auto|scroll|overlay)/.test(overflowY) ||
        /(auto|scroll|overlay)/.test(overflow) ||
        overflowY === "hidden";
      if (canScroll && allowsScroll) {
        scrollHost = node;
        break;
      }
      node = node.parentElement;
    }

    const navRect = nav.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const hostRect =
      scrollHost === docScroller
        ? { top: 0, height: window.innerHeight || document.documentElement.clientHeight || 0 }
        : scrollHost.getBoundingClientRect();
    const navTopInHost = Math.max(0, Math.round(navRect.top - hostRect.top));
    const hostViewportHeight =
      scrollHost === docScroller
        ? window.innerHeight || document.documentElement.clientHeight || 0
        : scrollHost.clientHeight || hostRect.height || 0;
    const spacer = Math.max(0, Math.round(hostViewportHeight - navTopInHost + 28));
    const delta = targetRect.top - navRect.top;

    summaryGridDebugLog("scrollToSection", {
      sectionKey,
      scrollHostTag: scrollHost?.tagName || "document",
      navTopInHost,
      spacer,
      delta: Math.round(delta),
    });

    setSummaryScrollOffset((prev) => (prev === navTopInHost ? prev : navTopInHost));
    setSummaryBottomSpacer((prev) => (prev === spacer ? prev : spacer));

    if (scrollHost === docScroller) {
      const current = window.scrollY || docScroller.scrollTop || 0;
      window.scrollTo({ top: Math.max(0, Math.round(current + delta)), behavior: "smooth" });
      return;
    }
    const current = scrollHost.scrollTop || 0;
    scrollHost.scrollTo({ top: Math.max(0, Math.round(current + delta)), behavior: "smooth" });
  }, []);

  const softwareRows = useMemo(() => details.software || [], [details.software]);

  const getSoftwareRowId = useCallback(
    (params) => `${params.data?.name || "software"}-${params.data?.version || ""}-${params.rowIndex}`,
    []
  );

  // Build a best-effort CPU display from summary fields
  const cpuInfo = useMemo(() => {
    const cpu = details.cpu || summary.cpu || {};
    const cores = cpu.logical_cores || cpu.cores || cpu.physical_cores;
    let ghz = cpu.base_clock_ghz;
    if (!ghz && typeof (summary.processor || '') === 'string') {
      const m = String(summary.processor).match(/\(([^)]*?)ghz\)/i);
      if (m && m[1]) {
        const n = parseFloat(m[1]);
        if (!Number.isNaN(n)) ghz = n;
      }
    }
    const name = (cpu.name || '').trim();
    const fromProcessor = (summary.processor || '').trim();
    const display = fromProcessor || [name, ghz ? `(${Number(ghz).toFixed(1)}GHz)` : null, cores ? `@ ${cores} Cores` : null].filter(Boolean).join(' ');
    return { cores, ghz, name, display };
  }, [summary]);

  const defaultGridColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: "agTextColumnFilter",
      flex: 1,
      minWidth: 140,
    }),
    []
  );

  const softwareColumnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Software Name",
        flex: 1.2,
        minWidth: 240,
        filter: "agTextColumnFilter",
      },
      {
        field: "version",
        headerName: "Version",
        width: 180,
        minWidth: 160,
        filter: "agTextColumnFilter",
      },
      {
        field: "source",
        headerName: "Source",
        width: 180,
        minWidth: 160,
        filter: "agTextColumnFilter",
        valueFormatter: (params) => {
          const value = String(params.value || "").trim().toLowerCase();
          if (value === "local_installed") return "Locally Installed";
          if (value === "windows_store") return "Windows Store";
          if (value === "dpkg") return "DPKG";
          if (value === "rpm") return "RPM";
          return params.value || "—";
        },
      },
    ],
    []
  );

  const formatScriptType = useCallback((raw) => {
    const value = String(raw || "").toLowerCase();
    if (value === "ansible") return "Ansible Playbook";
    if (value === "reverse_tunnel" || value === "vpn_tunnel") return "Reverse VPN Tunnel";
    return "Script";
  }, []);

  const historyColumnDefs = useMemo(
    () => [
      {
        headerName: "Activity",
        field: "script_type",
        minWidth: 180,
        valueGetter: (params) => formatScriptType(params.data?.script_type),
      },
      {
        headerName: "Task",
        field: "script_display_name",
        flex: 1.2,
        minWidth: 240,
        filter: "agTextColumnFilter",
      },
      {
        headerName: "Ran On",
        field: "ran_at",
        width: 210,
        valueFormatter: (params) => formatTimestamp(params.value),
        sort: "desc",
        comparator: (a, b) => (a || 0) - (b || 0),
      },
      {
        headerName: "Job Status",
        field: "status",
        width: 160,
        cellRenderer: "StatusPillCell",
      },
      {
        headerName: "StdOut / StdErr",
        colId: "stdout",
        width: 220,
        sortable: false,
        filter: false,
        cellRenderer: "HistoryActionsCell",
      },
    ],
    [formatScriptType, formatTimestamp]
  );

const MetricCard = ({ icon, title, main, sub, compact = false, sx }) => (
    <Box
      sx={{
        px: compact ? 1.5 : 2.4,
        py: compact ? 1.4 : 2,
        borderRadius: compact ? 2 : 3,
        border: 'none',
        background: 'transparent',
        boxShadow: 'none',
        minWidth: compact ? 0 : 220,
        width: compact ? "100%" : "auto",
        minHeight: compact ? 110 : 140,
        display: "flex",
        flexDirection: "column",
        gap: 0.75,
        ...(sx || {}),
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
        <Box
          sx={{
            width: compact ? 22 : 36,
            height: compact ? 22 : 36,
            borderRadius: 2,
            background: "transparent",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            color: NAV_TAB_COLORS.icon,
          }}
        >
          {icon}
        </Box>
        <Typography
          sx={{
            fontSize: compact ? "0.8rem" : "0.75rem",
            letterSpacing: compact ? 0 : 0.6,
            textTransform: compact ? "none" : "uppercase",
            color: compact ? NAV_TAB_COLORS.text : "rgba(255,255,255,0.72)",
            fontWeight: compact ? 400 : 600,
          }}
        >
          {title}
        </Typography>
      </Box>
        <Typography
          sx={{
            fontSize: compact ? "0.8rem" : { xs: "1.4rem", md: "1.6rem" },
            fontWeight: compact ? 600 : 700,
            color: compact ? NAV_TAB_COLORS.textActive : "#f8fafc",
            lineHeight: compact ? 1.15 : 1.1,
            ...(compact
              ? {
                  whiteSpace: "normal",
                  overflowWrap: "anywhere",
                  wordBreak: "break-word",
                }
              : {}),
          }}
        >
          {main}
        </Typography>
      {sub ? (
        <Typography
          sx={{
            fontSize: compact ? "0.8rem" : "0.9rem",
            color: compact ? NAV_TAB_COLORS.text : "rgba(226,232,240,0.78)",
            fontWeight: compact ? 400 : 400,
            ...(compact
              ? {
                  whiteSpace: "normal",
                  overflowWrap: "anywhere",
                  wordBreak: "break-word",
                }
              : {}),
          }}
        >
          {sub}
        </Typography>
      ) : null}
    </Box>
  );

  const Island = ({ title, icon = null, meta = "", children, sx }) => (
    <Box
      sx={{
        p: 2,
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: "rgba(7,11,24,0.58)",
        boxShadow: "0 18px 40px rgba(2,6,23,0.55)",
        mb: 1.5,
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        ...(sx || {}),
      }}
    >
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1,
          mb: 1.4,
          flexWrap: "wrap",
        }}
      >
        <Stack direction="row" spacing={0.75} alignItems="center">
          {icon ? (
            <Box sx={{ color: MAGIC_UI.accentA, display: "inline-flex", alignItems: "center" }}>
              {icon}
            </Box>
          ) : null}
          <Typography
            variant="caption"
            sx={{
              color: MAGIC_UI.accentA,
              fontWeight: 600,
              fontSize: "0.82rem",
              letterSpacing: 0.3,
              textTransform: "uppercase",
              display: "block",
            }}
          >
            {title}
          </Typography>
        </Stack>
        {meta ? (
          <Typography
            variant="caption"
            sx={{ color: "rgba(226,232,240,0.72)", fontSize: "0.74rem", letterSpacing: 0.15 }}
          >
            {meta}
          </Typography>
        ) : null}
      </Box>
      <Box sx={{ flexGrow: 1, minHeight: 0 }}>{children}</Box>
    </Box>
  );

  const deviceMetricData = useMemo(() => {
    const cpuMain = (cpuInfo.name || (summary.processor || "") || "").split("\n")[0] || "Unknown CPU";
    const cpuSub =
      cpuInfo.ghz || cpuInfo.cores
        ? `${cpuInfo.ghz ? `${Number(cpuInfo.ghz).toFixed(2)}GHz ` : ""}${
            cpuInfo.cores ? `(${cpuInfo.cores}-Cores)` : ""
          }`.trim()
        : "";
    let totalRam = summary.total_ram;
    if (!totalRam && Array.isArray(details.memory)) {
      totalRam = details.memory.reduce((a, m) => a + (Number(m.capacity || 0) || 0), 0);
    }
    const memVal = totalRam ? `${formatBytes(totalRam)}` : "Unknown";
    let memSpeed = "";
    try {
      const speeds = (details.memory || [])
        .map((m) => parseInt(String(m.speed || "").replace(/[^0-9]/g, ""), 10))
        .filter((v) => !Number.isNaN(v) && v > 0);
      if (speeds.length) memSpeed = `Speed: ${Math.max(...speeds)} MT/s`;
    } catch {}
    const toNum = (val) => {
      if (val === undefined || val === null) return undefined;
      if (typeof val === "number") return Number.isNaN(val) ? undefined : val;
      const parsed = parseFloat(String(val).replace(/[^0-9.]+/g, ""));
      return Number.isNaN(parsed) ? undefined : parsed;
    };
    let storageTotalBytes = 0;
    let storageVolumeCount = 0;
    if (Array.isArray(details.storage)) {
      details.storage.forEach((drive) => {
        const total = toNum(drive?.total);
        if (total === undefined) return;
        storageTotalBytes += total;
        storageVolumeCount += 1;
      });
    }
    const storageMain = storageTotalBytes > 0 ? `${formatBytes(storageTotalBytes)}` : "Unknown";
    const storageSub =
      storageVolumeCount > 0
        ? `${storageVolumeCount} ${storageVolumeCount === 1 ? "Volume" : "Volumes"}`
        : "";
    const primaryIp = (summary.internal_ip || "").trim();
    let nic = null;
    if (Array.isArray(details.network)) {
      nic = details.network.find((n) => (n.ips || []).includes(primaryIp)) || details.network[0] || null;
    }
    const normalizeSpeed = (val) => {
      const s = String(val || "").trim();
      if (!s) return "Unknown";
      const low = s.toLowerCase();
      if (low.includes("gbps") || low.includes("mbps")) return s;
      const m = low.match(/(\d+\.?\d*)\s*([gmk]?)(bps)/);
      if (!m) return s;
      let num = parseFloat(m[1]);
      const unit = m[2];
      if (unit === "g") return `${num} Gbps`;
      if (unit === "m") return `${num} Mbps`;
      if (unit === "k") return `${(num / 1000).toFixed(1)} Mbps`;
      if (num >= 1e9) return `${(num / 1e9).toFixed(1)} Gbps`;
      if (num >= 1e6) return `${(num / 1e6).toFixed(0)} Mbps`;
      return s;
    };
    const netVal = nic ? normalizeSpeed(nic.link_speed || nic.speed) : "Unknown";
    return {
      cpuMain,
      cpuSub,
      memVal,
      memSpeed,
      storageMain,
      storageSub,
      netVal,
      nicLabel: nic?.adapter || " ",
    };
  }, [summary, details.memory, details.storage, details.network, cpuInfo]);

  const renderDeviceSummaryTab = () => {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 3, minHeight: 0, minWidth: 0, width: "100%" }}>
        <Box
          sx={{
            borderRadius: 3,
            background: "transparent",
            p: { xs: 2, md: 3 },
            display: "flex",
            flexDirection: "column",
            gap: 2,
            minHeight: 0,
            minWidth: 0,
            overflowX: "hidden",
          }}
        >
          <Box sx={{ display: "flex", flexDirection: "column", gap: 1.4, mt: 0.4 }}>
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", lg: "220px minmax(0,1fr)" },
                gap: 1.5,
                alignItems: "start",
                minWidth: 0,
              }}
            >
              <SummarySectionsNav onSelectSection={scrollToSummarySection} />

              <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, minWidth: 0, width: "100%" }}>
                <Box id="device-summary-top-level" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Top-Level Information"
                    icon={<InfoOutlinedIcon sx={{ fontSize: 18 }} />}
                    meta="Identity and lifecycle"
                    sx={{ mb: 0 }}
                  >
                    <Box
                      sx={{
                        display: "grid",
                        gridTemplateColumns: { xs: "1fr", xl: "minmax(260px,0.72fr) minmax(0,1.6fr)" },
                        alignItems: { xs: "start", xl: "end" },
                        gap: { xs: 1.1, md: 1.5 },
                        mb: 1.9,
                        minWidth: 0,
                      }}
                    >
                      <Box sx={{ display: "flex", flexDirection: "column", gap: 1.1, minWidth: 0 }}>
                        <Box sx={{ width: "100%", maxWidth: { xs: "100%", xl: 440 } }}>
                          <TextField
                            size="small"
                            label="Description"
                            value={description}
                            onChange={(e) => setDescription(e.target.value)}
                            onBlur={saveDescription}
                            placeholder="Add a friendly label"
                            sx={{
                              width: "100%",
                              input: { color: "#fff" },
                              "& .MuiOutlinedInput-root": {
                                backgroundColor: "rgba(4,7,17,0.65)",
                                borderRadius: 3,
                                "& fieldset": {
                                  borderColor: "rgba(148,163,184,0.45)",
                                  borderRadius: 3,
                                },
                                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
                              },
                              label: { color: MAGIC_UI.textMuted },
                            }}
                          />
                        </Box>
                        {connectionType === "ssh" && (
                          <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1.1, alignItems: "center" }}>
                            <TextField
                              size="small"
                              label="SSH Endpoint"
                              value={connectionDraft}
                              onChange={(e) => setConnectionDraft(e.target.value)}
                              placeholder="user@host or host"
                              sx={{
                                minWidth: 250,
                                maxWidth: 360,
                                input: { color: "#fff" },
                                "& .MuiOutlinedInput-root": {
                                  backgroundColor: "rgba(4,7,17,0.65)",
                                  "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                                  "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
                                },
                                label: { color: MAGIC_UI.textMuted },
                              }}
                            />
                            <Button
                              size="small"
                              variant="outlined"
                              onClick={saveConnectionEndpoint}
                              disabled={connectionSaving || connectionDraft.trim() === connectionEndpoint.trim()}
                              sx={{
                                textTransform: "none",
                                borderColor: MAGIC_UI.accentA,
                                color: MAGIC_UI.accentA,
                                borderRadius: 999,
                                px: 2,
                              }}
                            >
                              {connectionSaving ? "Saving..." : "Save"}
                            </Button>
                            <Box sx={{ display: "flex", flexDirection: "column" }}>
                              {connectionMessage && (
                                <Typography variant="caption" sx={{ color: MAGIC_UI.accentA }}>
                                  {connectionMessage}
                                </Typography>
                              )}
                              {connectionError && (
                                <Typography variant="caption" sx={{ color: "#ff7b89" }}>
                                  {connectionError}
                                </Typography>
                              )}
                            </Box>
                          </Box>
                        )}
                      </Box>

                      <Box
                        sx={{
                          display: "grid",
                          minWidth: 0,
                          gridTemplateColumns: {
                            xs: "1fr",
                            sm: "repeat(2, minmax(0,1fr))",
                            xl: "minmax(0,1.5fr) repeat(3, minmax(0,1fr))",
                          },
                          gap: 1.1,
                          width: "100%",
                          mt: { xs: 0.4, xl: 0 },
                          pt: { xs: 0, xl: 0 },
                          alignSelf: { xs: "start", xl: "end" },
                          "& > *": {
                            background: "transparent !important",
                            border: "none !important",
                            boxShadow: "none !important",
                            borderRadius: 0,
                          },
                        }}
                      >
                        <MetricCard
                          compact
                          icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />}
                          title="Processor"
                          main={deviceMetricData.cpuMain}
                          sub={deviceMetricData.cpuSub}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                        <MetricCard
                          compact
                          icon={<MemoryRoundedIcon sx={{ fontSize: 18 }} />}
                          title="RAM"
                          main={deviceMetricData.memVal}
                          sub={deviceMetricData.memSpeed || " "}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                        <MetricCard
                          compact
                          icon={<StorageRoundedIcon sx={{ fontSize: 18 }} />}
                          title="Storage"
                          main={deviceMetricData.storageMain}
                          sub={deviceMetricData.storageSub || " "}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                        <MetricCard
                          compact
                          icon={<SpeedRoundedIcon sx={{ fontSize: 18 }} />}
                          title="Network"
                          main={deviceMetricData.netVal}
                          sub={deviceMetricData.nicLabel}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                      </Box>
                    </Box>

                    <Box
                      sx={{
                        display: "grid",
                        gridTemplateColumns: { xs: "1fr", lg: "repeat(2, minmax(0,1fr))" },
                        gap: 1.4,
                        minWidth: 0,
                        width: "100%",
                      }}
                    >
                      <Box sx={{ minWidth: 0, display: "flex", flexDirection: "column", gap: 0.7 }}>
                        <Typography
                          variant="caption"
                          sx={{
                            color: MAGIC_UI.accentA,
                            fontWeight: 600,
                            fontSize: "0.82rem",
                            letterSpacing: 0.3,
                            textTransform: "none",
                            display: "block",
                          }}
                        >
                          Overview
                        </Typography>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="top-level-overview"
                            rowData={overviewInfoRows}
                            columnDefs={topLevelSplitColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={topLevelSplitGridHeight}
                          />
                        ) : (
                          <SummaryGridPlaceholder height={topLevelSplitGridHeight} />
                        )}
                      </Box>
                      <Box sx={{ minWidth: 0, display: "flex", flexDirection: "column", gap: 0.7 }}>
                        <Typography
                          variant="caption"
                          sx={{
                            color: MAGIC_UI.accentA,
                            fontWeight: 600,
                            fontSize: "0.82rem",
                            letterSpacing: 0.3,
                            textTransform: "none",
                            display: "block",
                          }}
                        >
                          Borealis Agent
                        </Typography>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="top-level-agent"
                            rowData={borealisAgentRows}
                            columnDefs={topLevelSplitColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={topLevelSplitGridHeight}
                          />
                        ) : (
                          <SummaryGridPlaceholder height={topLevelSplitGridHeight} />
                        )}
                      </Box>
                    </Box>
                  </Island>
                </Box>

                <Box id="device-summary-storage" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Storage"
                    icon={<StorageRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={
                      hardwareOverview.storageCount
                        ? `${hardwareOverview.storageCount} volumes${
                            hardwareOverview.storageCritical > 0
                              ? ` • ${hardwareOverview.storageCritical} exceeding ${STORAGE_USAGE_ALERT_THRESHOLD_PCT}% usage`
                              : ""
                          }`
                        : "No storage telemetry"
                    }
                    sx={{ mb: 0 }}
                  >
                    {summaryDataReady ? (
                      <SummarySectionGrid
                        sectionKey="storage"
                        rowData={storageRows}
                        columnDefs={storageColumnDefs}
                        defaultColDef={defaultGridColDef}
                        height={storageGridHeight}
                      />
                    ) : (
                      <SummaryGridPlaceholder height={storageGridHeight} />
                    )}
                  </Island>
                </Box>

                <Box id="device-summary-memory" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Memory"
                    icon={<MemoryRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={
                      hardwareOverview.memoryCount
                        ? `${hardwareOverview.installedMemory}/${hardwareOverview.memoryCount} populated slots`
                        : "No memory telemetry"
                    }
                    sx={{ mb: 0 }}
                  >
                    {summaryDataReady ? (
                      <SummarySectionGrid
                        sectionKey="memory"
                        rowData={memoryRows}
                        columnDefs={memoryColumnDefs}
                        defaultColDef={defaultGridColDef}
                        height={memoryGridHeight}
                      />
                    ) : (
                      <SummaryGridPlaceholder height={memoryGridHeight} />
                    )}
                  </Island>
                </Box>

                <Box id="device-summary-network" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Network"
                    icon={<LanRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={`Internal ${hardwareOverview.internalIp} • External ${hardwareOverview.externalIp}`}
                    sx={{ mb: 0 }}
                  >
                    {summaryDataReady ? (
                      <SummarySectionGrid
                        sectionKey="network"
                        rowData={networkRows}
                        columnDefs={networkColumnDefs}
                        defaultColDef={defaultGridColDef}
                        height={networkGridHeight}
                      />
                    ) : (
                      <SummaryGridPlaceholder height={networkGridHeight} />
                    )}
                  </Island>
                </Box>
                <Box sx={{ width: "100%", height: `${summaryBottomSpacer}px`, flexShrink: 0 }} />
              </Box>
            </Box>
          </Box>
        </Box>
      </Box>
    );
  };

  const renderSoftware = () => (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <TextField
        size="small"
        placeholder="Search software..."
        value={softwareSearch}
        onChange={(e) => setSoftwareSearch(e.target.value)}
        sx={{
          maxWidth: 320,
          input: { color: "#fff" },
          "& .MuiOutlinedInput-root": {
            backgroundColor: "rgba(4,7,17,0.65)",
            "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
            "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
          },
          "& .MuiInputLabel-root": { color: MAGIC_UI.textMuted },
        }}
      />
      <GridShell sx={{ flexGrow: 1, minHeight: 360 }}>
        <AgGridReact
          rowData={softwareRows}
          columnDefs={softwareColumnDefs}
          defaultColDef={defaultGridColDef}
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          quickFilterText={softwareSearch}
          getRowId={getSoftwareRowId}
          components={GRID_COMPONENTS}
          theme={myTheme}
          style={{
            width: "100%",
            height: "100%",
            fontFamily: gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
          }}
        />
      </GridShell>
    </Box>
  );

  const renderRemoteShellTab = () => (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <ReverseTunnelRemoteShell device={tunnelDevice} />
    </Box>
  );

  const renderRemoteDesktopTab = () => (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <ReverseTunnelVnc device={tunnelDevice} />
    </Box>
  );

  const memoryRows = useMemo(
    () =>
      (details.memory || []).map((m, idx) => ({
        id: `${m.slot || idx}`,
        slot: m.slot || `BANK ${idx}`,
        speed: m.speed || "unknown",
        serial: m.serial || "unknown",
        capacity: m.capacity,
      })),
    [details.memory]
  );

  const memoryColumnDefs = useMemo(
    () => [
      {
        field: "slot",
        headerName: "Slot",
        width: 120,
        flex: 0,
        sortable: false,
        cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      },
      { field: "speed", headerName: "Speed", width: 130, flex: 0, sortable: false },
      { field: "serial", headerName: "Serial Number", width: 170, flex: 0, sortable: false },
      {
        field: "capacity",
        headerName: "Capacity",
        width: 140,
        minWidth: 140,
        flex: 1,
        valueFormatter: (params) => formatBytes(params.value),
      },
    ],
    []
  );

  const storageRows = useMemo(() => {
    const toNum = (val) => {
      if (val === undefined || val === null) return undefined;
      if (typeof val === "number") {
        return Number.isNaN(val) ? undefined : val;
      }
      const n = parseFloat(String(val).replace(/[^0-9.]+/g, ""));
      return Number.isNaN(n) ? undefined : n;
    };
    return (details.storage || []).map((d, idx) => {
      const total = toNum(d.total);
      let usagePct = toNum(d.usage);
      let usedBytes = toNum(d.used);
      let freeBytes = toNum(d.free);
      if (usagePct !== undefined && usagePct <= 1) usagePct *= 100;
      if (usedBytes === undefined && total !== undefined && usagePct !== undefined) {
        usedBytes = (usagePct / 100) * total;
      }
      if (freeBytes === undefined && total !== undefined && usedBytes !== undefined) {
        freeBytes = total - usedBytes;
      }
      if (usagePct === undefined && total && usedBytes !== undefined) {
        usagePct = (usedBytes / total) * 100;
      }
      return {
        id: `${d.drive || idx}`,
        driveLabel: String(d.drive || "").replace("\\\\", ""),
        disk_type: d.disk_type || "Fixed Disk",
        total,
        used: usedBytes,
        freeBytes,
        usage: usagePct,
      };
    });
  }, [details.storage]);

  const storageColumnDefs = useMemo(
    () => [
      {
        field: "driveLabel",
        headerName: "Drive",
        width: 130,
        flex: 0,
        sortable: false,
        filter: "agTextColumnFilter",
        cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      },
      { field: "disk_type", headerName: "Type", width: 120, flex: 0, sortable: false },
      {
        field: "total",
        headerName: "Capacity",
        valueFormatter: (params) => formatBytes(params.value),
        width: 140,
        flex: 0,
      },
      {
        field: "used",
        headerName: "Used",
        valueFormatter: (params) => formatBytes(params.value),
        width: 130,
        flex: 0,
      },
      {
        field: "freeBytes",
        headerName: "Free",
        valueFormatter: (params) => formatBytes(params.value),
        width: 130,
        flex: 0,
      },
      {
        field: "usage",
        headerName: "Usage",
        valueFormatter: (params) =>
          params.value === undefined || Number.isNaN(params.value) ? "unknown" : `${Math.round(params.value)}%`,
        width: 110,
        minWidth: 110,
        flex: 0,
      },
      {
        field: "alerts",
        headerName: "Alerts",
        width: 240,
        minWidth: 240,
        flex: 1,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          if (!isStorageUsageAlert(params?.data?.usage)) return "";
          return (
            <Box
              component="span"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                gap: 0.8,
                whiteSpace: "nowrap",
                lineHeight: 1.1,
              }}
              title={STORAGE_USAGE_ALERT_LABEL}
            >
              <Box
                component="i"
                className="fa-solid fa-triangle-exclamation fa-exclamation-triangle"
                sx={{ color: STORAGE_USAGE_ALERT_COLOR, fontSize: "1.15rem", lineHeight: 1 }}
              />
              <Box component="span">{STORAGE_USAGE_ALERT_LABEL}</Box>
            </Box>
          );
        },
      },
    ],
    []
  );

  const networkRows = useMemo(
    () =>
      (details.network || []).map((n, idx) => ({
        id: `${n.adapter || idx}`,
        adapter: n.adapter || "Adapter",
        ips: (n.ips || []).join(", "),
        mac: formatMac(n.mac),
        link_speed: n.link_speed || n.speed || "unknown",
      })),
    [details.network]
  );

  const networkColumnDefs = useMemo(
    () => [
      { field: "adapter", headerName: "Adapter", width: 170, flex: 0, cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR } },
      { field: "ips", headerName: "IP Address(es)", width: 260, flex: 0 },
      { field: "mac", headerName: "MAC Address", width: 180, flex: 0 },
      { field: "link_speed", headerName: "Link Speed", width: 140, flex: 1, minWidth: 140 },
    ],
    []
  );

  const hardwareOverview = useMemo(() => {
    const storageCritical = storageRows.filter(
      (row) => isStorageUsageAlert(row.usage)
    ).length;
    const installedMemory = memoryRows.filter((row) => {
      if (typeof row.capacity === "number") return row.capacity > 0;
      const parsed = parseFloat(String(row.capacity || "").replace(/[^0-9.]+/g, ""));
      return !Number.isNaN(parsed) && parsed > 0;
    }).length;
    const internalIp = String(meta.internalIp || summary.internal_ip || "unknown").trim() || "unknown";
    const externalIp = String(meta.externalIp || summary.external_ip || "unknown").trim() || "unknown";
    return {
      storageCount: storageRows.length,
      storageCritical,
      memoryCount: memoryRows.length,
      installedMemory,
      internalIp,
      externalIp,
    };
  }, [
    storageRows,
    memoryRows,
    meta.internalIp,
    meta.externalIp,
    summary.internal_ip,
    summary.external_ip,
  ]);

  const overviewInfoRows = useMemo(
    () => {
      const cpuIdentity = details.cpu && typeof details.cpu === "object" ? details.cpu : {};
      const manufacturerValue = String(
        summary.manufacturer || summary.vendor || meta.manufacturer || cpuIdentity.system_manufacturer || ""
      ).trim();
      const systemModelValue = String(
        summary.system_model || summary.device_model || meta.systemModel || cpuIdentity.system_model_raw || ""
      ).trim();
      const preCombinedModel = String(summary.model || meta.model || cpuIdentity.system_model || "").trim();
      const combinedModel = [manufacturerValue, systemModelValue].filter(Boolean).join(" ").trim() || preCombinedModel || "unknown";
      const serialValue =
        String(
          summary.serial_number ||
            summary.serial ||
            summary.bios_serial ||
            cpuIdentity.system_serial_number ||
            summary.asset_tag ||
            meta.serialNumber ||
            UNABLE_TO_RETRIEVE_SN
        ).trim() || UNABLE_TO_RETRIEVE_SN;
      return [
        {
          id: "site",
          label: "Site",
          value: meta.siteName || summary.site_name || summary.site || device?.site_name || "placeholder",
        },
        {
          id: "device-type",
          label: "Device Type",
          value: meta.deviceType || summary.device_type || agent.device_type || device?.device_type || "unknown",
        },
        {
          id: "last-user",
          label: "Last User",
          value: meta.lastUser || summary.last_user || "unknown",
        },
        {
          id: "operating-system",
          label: "Operating System",
          value: meta.operatingSystem || summary.operating_system || agent.agent_operating_system || "unknown",
        },
        {
          id: "model",
          label: "Model",
          value: combinedModel,
        },
        {
          id: "serial-number",
          label: "Serial Number",
          value: serialValue,
        },
        {
          id: "last-reboot",
          label: "Last Reboot",
          value: formatDateValue(meta.lastReboot || summary.last_reboot || "", "unknown"),
        },
      ];
    },
    [
      meta.siteName,
      meta.deviceType,
      meta.lastUser,
      meta.operatingSystem,
      meta.manufacturer,
      meta.systemModel,
      meta.model,
      meta.serialNumber,
      meta.lastReboot,
      details.cpu,
      summary.site_name,
      summary.site,
      summary.device_type,
      summary.manufacturer,
      summary.vendor,
      summary.model,
      summary.system_model,
      summary.device_model,
      summary.serial_number,
      summary.serial,
      summary.bios_serial,
      summary.asset_tag,
      summary.operating_system,
      summary.last_user,
      summary.last_reboot,
      device?.site_name,
      device?.device_type,
      agent.device_type,
      agent.agent_operating_system,
      formatDateValue,
    ]
  );

  const borealisAgentRows = useMemo(
    () => {
      const peerIp = tunnelInfo?.virtual_ip ? String(tunnelInfo.virtual_ip).split("/")[0] : "";
      return [
        {
          id: "agent-guid",
          label: "AGENT_GUID",
          value: meta.agentGuid || summary.agent_guid || device?.agent_guid || agent?.agent_guid || "unknown",
        },
        {
          id: "agent-version",
          label: "Agent Version",
          value: meta.agentVersionStatus || summary.agent_version_status || "Needs Updated",
        },
        {
          id: "enrollment-date",
          label: "Enrollment Date",
          value: formatDateValue(
            meta.created || summary.created || device?.created || device?.created_at || "",
            "placeholder"
          ),
        },
        {
          id: "wireguard-peer-ip",
          label: "Wireguard Peer IP",
          value: peerIp || "Inactive",
        },
        {
          id: "vpn-tunnel-id",
          label: "VPN Tunnel ID",
          value: tunnelInfo?.tunnel_id || "Inactive",
        },
      ];
    },
    [
      meta.agentGuid,
      meta.agentVersionStatus,
      meta.created,
      summary.agent_guid,
      summary.agent_version_status,
      summary.created,
      device?.agent_guid,
      device?.created,
      device?.created_at,
      agent?.agent_guid,
      tunnelInfo?.virtual_ip,
      tunnelInfo?.tunnel_id,
      formatDateValue,
    ]
  );

  const topLevelSplitColumnDefs = useMemo(
    () => [
      {
        field: "label",
        headerName: "Field",
        width: 170,
        flex: 0,
        sortable: false,
        filter: false,
        cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      },
      {
        field: "value",
        headerName: "Value",
        flex: 1,
        minWidth: 220,
        sortable: false,
        filter: false,
      },
    ],
    []
  );

  const resolveGridHeight = useCallback((rowCount, options = {}) => {
    const minHeight = Number(options.minHeight || 200);
    const maxHeight = Number(options.maxHeight || 420);
    const rowHeight = Number(options.rowHeight || 36);
    const headerHeight = Number(options.headerHeight || 42);
    const padding = Number(options.padding || 14);
    const rows = Math.max(1, Number(rowCount || 0));
    return Math.max(minHeight, Math.min(maxHeight, headerHeight + rows * rowHeight + padding));
  }, []);

  const topLevelSplitGridHeight = useMemo(() => {
    const baseHeight = resolveGridHeight(Math.max(overviewInfoRows.length, borealisAgentRows.length), {
      minHeight: BASE_GRID_HEIGHTS.topLevel,
      maxHeight: 420,
    });
    return Math.round(baseHeight * 1.2);
  }, [overviewInfoRows.length, borealisAgentRows.length, resolveGridHeight]);
  const storageGridHeight = useMemo(
    () =>
      resolveGridHeight(storageRows.length, {
        minHeight: BASE_GRID_HEIGHTS.storage,
        maxHeight: 420,
      }),
    [storageRows.length, resolveGridHeight]
  );
  const memoryGridHeight = useMemo(
    () =>
      resolveGridHeight(memoryRows.length, {
        minHeight: BASE_GRID_HEIGHTS.memory,
        maxHeight: 340,
      }),
    [memoryRows.length, resolveGridHeight]
  );
  const networkGridHeight = useMemo(
    () =>
      resolveGridHeight(networkRows.length, {
        minHeight: BASE_GRID_HEIGHTS.network,
        maxHeight: 340,
      }),
    [networkRows.length, resolveGridHeight]
  );

  useEffect(() => {
    if (!isSummaryGridDebugEnabled()) return;
    const topTabKey = TOP_TABS[tab]?.key || "";
    if (topTabKey !== "summary") return;
    summaryGridDebugLog("deviceDetailsRender", {
      renderCount: pageRenderCountRef.current,
      topTabKey,
      summaryScrollOffset,
      summaryBottomSpacer,
      overviewRows: overviewInfoRows.length,
      agentRows: borealisAgentRows.length,
      storageRows: storageRows.length,
      memoryRows: memoryRows.length,
      networkRows: networkRows.length,
    });
  }, [
    tab,
    summaryScrollOffset,
    summaryBottomSpacer,
    overviewInfoRows.length,
    borealisAgentRows.length,
    storageRows.length,
    memoryRows.length,
    networkRows.length,
  ]);

  const highlightCode = (code, lang) => {
    try {
      return Prism.highlight(code ?? "", Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return String(code || "");
    }
  };

  const handleViewOutput = useCallback(async (row, which) => {
    if (!row || !row.id) return;
    try {
      const resp = await fetch(`/api/device/activity/job/${row.id}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const lang = ((data.script_path || "").toLowerCase().endsWith(".ps1")) ? "powershell"
        : ((data.script_path || "").toLowerCase().endsWith(".bat")) ? "batch"
        : ((data.script_path || "").toLowerCase().endsWith(".sh")) ? "bash"
        : ((data.script_path || "").toLowerCase().endsWith(".yml")) ? "yaml" : "powershell";
      setOutputLang(lang);
      const friendly = resolveAssemblyName(data.script_name, data.script_path);
      setOutputTitle(`${which === 'stderr' ? 'StdErr' : 'StdOut'} - ${friendly}`);
      setOutputContent(which === 'stderr' ? (data.stderr || "") : (data.stdout || ""));
      setOutputOpen(true);
    } catch (e) {
      console.warn("Failed to load output", e);
    }
  }, [resolveAssemblyName]);

  const historyDisplayRows = useMemo(() => {
    return (historyRows || []).map((row) => ({
      ...row,
      script_display_name: resolveAssemblyName(row.script_name, row.script_path),
    }));
  }, [historyRows, resolveAssemblyName]);

  const getHistoryRowId = useCallback((params) => String(params.data?.id || params.rowIndex), []);

  const historyGridContext = useMemo(
    () => ({
      onViewOutput: handleViewOutput,
    }),
    [handleViewOutput]
  );


  const renderHistory = () => (
    <GridShell sx={{ flexGrow: 1, minHeight: 360 }}>
      <AgGridReact
        rowData={historyDisplayRows}
        columnDefs={historyColumnDefs}
        defaultColDef={defaultGridColDef}
        pagination
        paginationPageSize={20}
        paginationPageSizeSelector={[20, 50, 100]}
        animateRows
        components={GRID_COMPONENTS}
        context={historyGridContext}
        getRowId={getHistoryRowId}
        suppressCellFocus
        theme={myTheme}
        style={{
          width: "100%",
          height: "100%",
          fontFamily: gridFontFamily,
          "--ag-icon-font-family": iconFontFamily,
        }}
      />
    </GridShell>
  );

  

  const status = lockedStatus || statusFromHeartbeat(agent.last_seen || device?.lastSeen);

  const rawDisplayHostname = meta.hostname || summary.hostname || agent.hostname || device?.hostname || "";
  const displayHostname = formatHostnameForDisplay(rawDisplayHostname) || "Device Details";
  const pageSubtitle = status ? `Status: ${status}` : "";

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "device-details-actions",
        label: "Actions",
        icon: <MoreHorizIcon />,
        tone: "primary",
        disabled: !(agent?.hostname || device?.hostname),
        onClick: (event) => setMenuAnchor(event.currentTarget),
      },
    ],
    [agent?.hostname, device?.hostname]
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: displayHostname,
      page_subtitle: pageSubtitle,
      page_icon: PAGE_ICON,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [displayHostname, onPageMetaChange, pageHeaderActions, pageSubtitle]);

  const topTabRenderers = [
    renderDeviceSummaryTab,
    renderSoftware,
    renderHistory,
    renderRemoteShellTab,
    renderRemoteDesktopTab,
  ];
  const tabContent = (topTabRenderers[tab] || renderDeviceSummaryTab)();

  return (
    <Box
      sx={{
        m: 0,
        p: { xs: 2, md: 3 },
        borderRadius: 0,
        background: "transparent",
        boxShadow: "none",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        width: "100%",
        maxWidth: "100%",
        minWidth: 0,
        height: "100%",
        overflowX: "hidden",
      }}
    >
      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={() => setMenuAnchor(null)}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
          },
        }}
      >
        <MenuItem
          disabled={!canLaunchQuickJob}
          onClick={() => {
            setMenuAnchor(null);
            if (!canLaunchQuickJob) return;
            onQuickJobLaunch && onQuickJobLaunch(quickJobTargets);
          }}
        >
          Quick Job
        </MenuItem>
        <MenuItem
          onClick={() => {
            setMenuAnchor(null);
            setClearDialogOpen(true);
          }}
        >
          Clear Device Activity
        </MenuItem>
      </Menu>
      {tunnelIndicators.length ? (
        <Box sx={{ display: "flex", justifyContent: "flex-end", mb: 1.25 }}>
          <Stack direction="row" spacing={1.2} alignItems="center" flexWrap="wrap" useFlexGap>
            {tunnelIndicators.map((item) => {
              const itemColor = item.color || (item.muted ? "rgba(125, 211, 252, 0.6)" : MAGIC_UI.accentA);
              return (
                <Stack
                  key={item.key}
                  direction="row"
                  spacing={0.6}
                  alignItems="center"
                  sx={{ color: itemColor }}
                >
                  {item.icon}
                  <Typography
                    variant="caption"
                    sx={{ color: itemColor, fontWeight: 600, letterSpacing: 0.2 }}
                  >
                    {item.label}:
                  </Typography>
                  <Typography
                    variant="caption"
                    sx={{
                      color: itemColor,
                      fontWeight: 600,
                      maxWidth: 280,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                    title={item.value}
                  >
                    {item.value}
                  </Typography>
                </Stack>
              );
            })}
          </Stack>
        </Box>
      ) : null}
      <Tabs
        value={tab}
        onChange={(e, v) => setTab(v)}
        variant="scrollable"
        scrollButtons="auto"
        TabIndicatorProps={{
          style: {
            height: 3,
            borderRadius: 3,
            background: NAV_TAB_COLORS.iconActive,
          },
        }}
        sx={{
          borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
          mb: 2,
          minHeight: NAV_TAB_HEIGHT,
          height: NAV_TAB_HEIGHT,
          minWidth: 0,
          maxWidth: "100%",
          "& .MuiTabs-flexContainer": {
            minHeight: NAV_TAB_HEIGHT,
            height: NAV_TAB_HEIGHT,
            alignItems: "stretch",
          },
          "& .MuiTab-root": {
            color: NAV_TAB_COLORS.text,
            textTransform: "none",
            fontWeight: 400,
            fontFamily: "inherit",
            fontSize: "0.8rem",
            minHeight: NAV_TAB_HEIGHT,
            height: NAV_TAB_HEIGHT,
            opacity: 1,
            borderRadius: 1,
            py: 0.35,
            transition:
              "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
            "& .MuiTab-iconWrapper": {
              color: NAV_TAB_COLORS.icon,
            },
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
            "& .MuiTab-iconWrapper": {
              color: NAV_TAB_COLORS.iconActive,
            },
            "&:hover": {
              background: NAV_TAB_COLORS.activeBg,
            },
          },
        }}
      >
        {TOP_TABS.map((tabDef) => (
          <Tab
            key={tabDef.key || tabDef.label}
            label={tabDef.label}
            icon={<tabDef.icon sx={{ fontSize: 18 }} />}
            iconPosition="start"
          />
        ))}
      </Tabs>
      <Box
        sx={{
          mt: 1,
          flexGrow: 1,
          minHeight: 0,
          minWidth: 0,
          width: "100%",
          overflowX: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {tabContent}
      </Box>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={outputTitle} subtitle="Review command output and captured response data." />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box
            sx={{
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              bgcolor: "rgba(4,7,17,0.65)",
              maxHeight: "56vh",
              overflowY: "auto",
              overflowX: "auto",
              overscrollBehavior: "contain",
              scrollbarGutter: "stable both-edges",
              "&::-webkit-scrollbar": {
                width: 10,
                height: 10,
              },
              "&::-webkit-scrollbar-track": {
                background: "rgba(15,23,42,0.45)",
                borderRadius: 999,
              },
              "&::-webkit-scrollbar-thumb": {
                background: "rgba(125,183,255,0.35)",
                borderRadius: 999,
                border: "2px solid rgba(15,23,42,0.45)",
              },
            }}
          >
            <Editor
              value={outputContent}
              onValueChange={() => {}}
              highlight={(code) => highlightCode(code, outputLang)}
              padding={12}
              style={{
                fontFamily:
                  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                fontSize: 12,
                color: "#e6edf3",
                minHeight: 200,
                whiteSpace: "pre",
                overflowWrap: "normal",
                wordBreak: "normal",
              }}
              textareaProps={{ readOnly: true, wrap: "off", spellCheck: false }}
            />
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={() => setOutputOpen(false)}
            sx={DIALOG_BUTTON_SX}
          >
            Close
          </Button>
        </DialogActions>
      </Dialog>

      <ClearDeviceActivityDialog
        open={clearDialogOpen}
        onCancel={() => setClearDialogOpen(false)}
        onConfirm={() => {
          clearHistory();
          setClearDialogOpen(false);
        }}
      />

    </Box>
  );
}
