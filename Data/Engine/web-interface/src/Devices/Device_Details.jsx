////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_Details.js

import React, { useState, useEffect, useMemo, useCallback, useRef } from "react";
import {
  Box,
  Tabs,
  Tab,
  Typography,
  Button,
  IconButton,
  Menu,
  MenuItem,
  TextField,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions
} from "@mui/material";
import StorageRoundedIcon from "@mui/icons-material/StorageRounded";
import MemoryRoundedIcon from "@mui/icons-material/MemoryRounded";
import SpeedRoundedIcon from "@mui/icons-material/SpeedRounded";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import MoreHorizIcon from "@mui/icons-material/MoreHoriz";
import { ClearDeviceActivityDialog } from "../Dialogs.jsx";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import QuickJob from "../Scheduling/Quick_Job.jsx";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

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

const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const SECTION_HEIGHTS = {
  summary: 360,
  storage: 360,
  memory: 260,
  network: 260,
};

const TOP_TABS = ["Device Summary", "Storage", "Memory", "Network", "Installed Software", "Activity History"];

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
  const onCancelAnsible = props.context?.onCancelAnsible;
  const scriptType = String(row.script_type || "").toLowerCase();
  const isRunningAnsible = scriptType === "ansible" && String(row.status || "").toLowerCase() === "running";

  return (
    <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
      {isRunningAnsible ? (
        <Button
          size="small"
          sx={{ color: "#ff7b89", textTransform: "none", minWidth: 0, p: 0 }}
          onClick={() => onCancelAnsible && onCancelAnsible(row)}
        >
          Cancel
        </Button>
      ) : null}
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

export default function DeviceDetails({ device, onBack }) {
  const [tab, setTab] = useState(0);
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
  const [historyRows, setHistoryRows] = useState([]);
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputContent, setOutputContent] = useState("");
  const [outputLang, setOutputLang] = useState("powershell");
  const [quickJobOpen, setQuickJobOpen] = useState(false);
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [assemblyNameMap, setAssemblyNameMap] = useState({});
  const softwareGridApiRef = useRef(null);
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
          const metadata = item.metadata && typeof item.metadata === "object" ? item.metadata : {};
          const displayName =
            (item.display_name || "").trim() ||
            (metadata.display_name ? String(metadata.display_name).trim() : "") ||
            item.assembly_guid ||
            "";
          if (!displayName) return;
          storeName(metadata.source_path || metadata.legacy_path || "", displayName);
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

  const statusColor = (s) => (s === "Online" ? "#00d18c" : "#ff4f4f");

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

  const formatLastSeen = (tsSec, offlineAfter = 120) => {
    if (!tsSec) return "unknown";
    const now = Date.now() / 1000;
    if (now - tsSec <= offlineAfter) return "Currently Online";
    const d = new Date(tsSec * 1000);
    const date = d.toLocaleDateString("en-US", {
      month: "2-digit",
      day: "2-digit",
      year: "numeric",
    });
    const time = d.toLocaleTimeString("en-US", {
      hour: "numeric",
      minute: "2-digit",
    });
    return `${date} @ ${time}`;
  };

  useEffect(() => {
    if (device) {
      setLockedStatus(device.status || statusFromHeartbeat(device.lastSeen));
    }

    const guid = device?.agent_guid || device?.guid || device?.agentGuid || device?.summary?.agent_guid;
    const agentId = device?.agentId || device?.summary?.agent_id || device?.id;
    const hostname = device?.hostname || device?.summary?.hostname;
    if (!device || (!guid && !hostname)) return;

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
          agentHash: detailData?.agent_hash || normalizedSummary.agent_hash || "",
          internalIp: detailData?.internal_ip || normalizedSummary.internal_ip || "",
          externalIp: detailData?.external_ip || normalizedSummary.external_ip || "",
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
          agent_hash: metaPayload.agentHash || prev?.agent_hash,
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
        console.warn("Failed to load device info", e);
        setMeta({});
      }
    };
    load();
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

  const formatDateTime = (str) => {
    if (!str) return "unknown";
    try {
      const [datePart, timePart] = str.split(" ");
      const [y, m, d] = datePart.split("-").map(Number);
      let [hh, mm, ss] = timePart.split(":").map(Number);
      const ampm = hh >= 12 ? "PM" : "AM";
      hh = hh % 12 || 12;
      return `${m.toString().padStart(2, "0")}/${d.toString().padStart(2, "0")}/${y} @ ${hh}:${mm
        .toString()
        .padStart(2, "0")} ${ampm}`;
    } catch {
      return str;
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

  const softwareRows = useMemo(() => details.software || [], [details.software]);
  const handleSoftwareGridReady = useCallback(
    (params) => {
      softwareGridApiRef.current = params.api;
      params.api.setQuickFilter(softwareSearch);
    },
    [softwareSearch]
  );

  useEffect(() => {
    if (softwareGridApiRef.current) {
      softwareGridApiRef.current.setQuickFilter(softwareSearch);
    }
  }, [softwareSearch]);

  const getSoftwareRowId = useCallback(
    (params) => `${params.data?.name || "software"}-${params.data?.version || ""}-${params.rowIndex}`,
    []
  );

  const summary = details.summary || {};
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

  const summaryItems = useMemo(
    () => [
      {
        label: "Hostname",
        value: meta.hostname || summary.hostname || agent.hostname || device?.hostname || "unknown",
      },
      {
        label: "Last User",
        value: meta.lastUser || summary.last_user || "unknown",
      },
      { label: "Device Type", value: meta.deviceType || summary.device_type || "unknown" },
      {
        label: "Created",
        value: meta.created
          ? formatDateTime(meta.created)
          : summary.created
            ? formatDateTime(summary.created)
            : "unknown",
      },
      {
        label: "Last Seen",
        value: formatLastSeen(meta.lastSeen || agent.last_seen || device?.lastSeen),
      },
      {
        label: "Last Reboot",
        value: meta.lastReboot
          ? formatDateTime(meta.lastReboot)
          : summary.last_reboot
            ? formatDateTime(summary.last_reboot)
            : "unknown",
      },
      {
        label: "Operating System",
        value:
          meta.operatingSystem || summary.operating_system || agent.agent_operating_system || "unknown",
      },
      { label: "Agent ID", value: meta.agentId || summary.agent_id || "unknown" },
      { label: "Agent GUID", value: meta.agentGuid || summary.agent_guid || "unknown" },
      { label: "Agent Hash", value: meta.agentHash || summary.agent_hash || "unknown" },
    ],
    [meta, summary, agent, device, formatDateTime, formatLastSeen]
  );

  const summaryFieldWidth = useMemo(() => {
    const longest = summaryItems.reduce((max, item) => Math.max(max, item.label.length), 0);
    return Math.min(260, Math.max(160, longest * 8));
  }, [summaryItems]);

  const summaryGridColumns = useMemo(
    () => [
      {
        field: "label",
        headerName: "Field",
        width: summaryFieldWidth,
        flex: 0,
        sortable: false,
        filter: false,
        suppressSizeToFit: true,
      },
      { field: "value", headerName: "Value", flex: 1, minWidth: 220, sortable: false, filter: "agTextColumnFilter" },
    ],
    [summaryFieldWidth]
  );

  const summaryGridRows = useMemo(
    () =>
      summaryItems.map((item, idx) => ({
        id: `${item.label}-${idx}`,
        label: item.label,
        value: typeof item.value === "string" ? item.value : String(item.value),
      })),
    [summaryItems]
  );

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
    ],
    []
  );

  const historyColumnDefs = useMemo(
    () => [
      {
        headerName: "Assembly",
        field: "script_type",
        minWidth: 180,
        valueGetter: (params) =>
          String(params.data?.script_type || "").toLowerCase() === "ansible"
            ? "Ansible Playbook"
            : "Script",
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
    [formatTimestamp]
  );

  const MetricCard = ({ icon, title, main, sub, compact = false }) => (
    <Box
      sx={{
        px: compact ? 1.5 : 2.4,
        py: compact ? 1.4 : 2,
        borderRadius: compact ? 2 : 3,
        border: "1px solid rgba(255,255,255,0.08)",
        background: MAGIC_UI.panelBg,
        boxShadow: "0 20px 45px rgba(2,6,23,0.5)",
        minWidth: compact ? 170 : 220,
        minHeight: compact ? 110 : 140,
        display: "flex",
        flexDirection: "column",
        gap: 0.75,
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
        <Box
          sx={{
            width: 36,
            height: 36,
            borderRadius: 2,
            background: "rgba(4,7,17,0.4)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            color: "#fff",
          }}
        >
          {icon}
        </Box>
        <Typography
          sx={{
            fontSize: "0.75rem",
            letterSpacing: 0.6,
            textTransform: "uppercase",
            color: "rgba(255,255,255,0.72)",
            fontWeight: 600,
          }}
        >
          {title}
        </Typography>
      </Box>
        <Typography
          sx={{
            fontSize: compact ? "1.2rem" : { xs: "1.4rem", md: "1.6rem" },
            fontWeight: 700,
            color: "#f8fafc",
            lineHeight: 1.1,
          }}
        >
          {main}
        </Typography>
      {sub ? (
        <Typography sx={{ fontSize: "0.9rem", color: "rgba(226,232,240,0.78)" }}>{sub}</Typography>
      ) : null}
    </Box>
  );

  const Island = ({ title, children, sx }) => (
    <Box
      sx={{
        p: 2,
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: MAGIC_UI.panelBg,
        boxShadow: "0 18px 40px rgba(2,6,23,0.55)",
        mb: 1.5,
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        ...(sx || {}),
      }}
    >
      <Typography
        variant="caption"
        sx={{
          color: MAGIC_UI.accentA,
          fontWeight: 500,
          fontSize: "0.9rem",
          letterSpacing: 0.35,
          textTransform: "uppercase",
          display: "block",
          mb: 1.5,
        }}
      >
        {title}
      </Typography>
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
    let osDrive = null;
    if (Array.isArray(details.storage)) {
      osDrive =
        details.storage.find((d) => String(d.drive || "").toUpperCase().startsWith("C:")) ||
        details.storage[0] ||
        null;
    }
    const storageMain = osDrive && osDrive.total != null ? `${formatBytes(osDrive.total)}` : "Unknown";
    const storageSub =
      osDrive && osDrive.used != null && osDrive.total != null
        ? `${formatBytes(osDrive.used)} of ${formatBytes(osDrive.total)} used`
        : osDrive && osDrive.free != null && osDrive.total != null
          ? `${formatBytes(osDrive.total - osDrive.free)} of ${formatBytes(osDrive.total)} used`
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
      <Box sx={{ display: "flex", flexDirection: "column", gap: 3, minHeight: 0 }}>
        <Box
          sx={{
            borderRadius: 3,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: MAGIC_UI.panelBg,
            boxShadow: MAGIC_UI.glow,
            p: { xs: 2, md: 3 },
            display: "flex",
            flexDirection: "column",
            gap: 2,
            minHeight: 0,
          }}
        >
          <TextField
            size="small"
            label="Description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            onBlur={saveDescription}
            placeholder="Add a friendly label"
            sx={{
              maxWidth: 420,
              input: { color: "#fff" },
              "& .MuiOutlinedInput-root": {
                backgroundColor: "rgba(4,7,17,0.65)",
                "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
              },
              label: { color: MAGIC_UI.textMuted },
            }}
          />
          {connectionType === "ssh" && (
            <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1.5, alignItems: "center" }}>
              <TextField
                size="small"
                label="SSH Endpoint"
                value={connectionDraft}
                onChange={(e) => setConnectionDraft(e.target.value)}
                placeholder="user@host or host"
                sx={{
                  minWidth: 260,
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
          <GridShell sx={{ flexGrow: 1, minHeight: 0, height: SECTION_HEIGHTS.summary }}>
            <AgGridReact
              rowData={summaryGridRows}
              columnDefs={summaryGridColumns}
              defaultColDef={defaultGridColDef}
              pagination={false}
              suppressCellFocus
              getRowId={(params) => params.data?.id ?? params.rowIndex}
              theme={myTheme}
              style={{
                width: "100%",
                height: "100%",
                fontFamily: gridFontFamily,
              }}
            />
          </GridShell>
        </Box>
      </Box>
    );
  };

  const renderStorageTab = () => (
    <GridShell sx={{ height: SECTION_HEIGHTS.storage }}>
      <AgGridReact
        rowData={storageRows}
        columnDefs={storageColumnDefs}
        defaultColDef={defaultGridColDef}
        pagination={false}
        suppressCellFocus
        getRowId={(params) => params.data?.id ?? params.rowIndex}
        theme={myTheme}
        style={{
          width: "100%",
          height: "100%",
          fontFamily: gridFontFamily,
        }}
      />
    </GridShell>
  );

  const renderMemoryTab = () => (
    <GridShell sx={{ height: SECTION_HEIGHTS.memory }}>
      <AgGridReact
        rowData={memoryRows}
        columnDefs={memoryColumnDefs}
        defaultColDef={defaultGridColDef}
        pagination={false}
        suppressCellFocus
        getRowId={(params) => params.data?.id ?? params.rowIndex}
        theme={myTheme}
        style={{
          width: "100%",
          height: "100%",
          fontFamily: gridFontFamily,
        }}
      />
    </GridShell>
  );

  const renderNetworkTab = () => (
    <GridShell sx={{ height: SECTION_HEIGHTS.network }}>
      <AgGridReact
        rowData={networkRows}
        columnDefs={networkColumnDefs}
        defaultColDef={defaultGridColDef}
        pagination={false}
        suppressCellFocus
        getRowId={(params) => params.data?.id ?? params.rowIndex}
        theme={myTheme}
        style={{
          width: "100%",
          height: "100%",
          fontFamily: gridFontFamily,
        }}
      />
    </GridShell>
  );

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
          onGridReady={handleSoftwareGridReady}
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
      { field: "slot", headerName: "Slot", width: 120, flex: 0, sortable: false },
      { field: "speed", headerName: "Speed", width: 130, flex: 0, sortable: false },
      { field: "serial", headerName: "Serial Number", width: 170, flex: 0, sortable: false },
      {
        field: "capacity",
        headerName: "Capacity",
        width: 140,
        flex: 0,
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
        driveLabel: `Drive ${String(d.drive || "").replace("\\\\", "")}`,
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
        flex: 0,
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
        internalIp: meta.internalIp || summary.internal_ip || "unknown",
        externalIp: meta.externalIp || summary.external_ip || "unknown",
      })),
    [details.network, meta.internalIp, meta.externalIp, summary.internal_ip, summary.external_ip]
  );

  const networkColumnDefs = useMemo(
    () => [
      { field: "adapter", headerName: "Adapter", width: 170, flex: 0 },
      { field: "ips", headerName: "IP Address(es)", width: 220, flex: 0 },
      { field: "mac", headerName: "MAC Address", width: 160, flex: 0 },
      { field: "internalIp", headerName: "Internal IP", width: 150, flex: 0 },
      { field: "externalIp", headerName: "External IP", width: 150, flex: 0 },
      { field: "link_speed", headerName: "Link Speed", width: 140, flex: 0 },
    ],
    []
  );

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

  const handleCancelAnsibleRun = useCallback(async (row) => {
    if (!row?.id) return;
    try {
      const resp = await fetch(`/api/ansible/run_for_activity/${encodeURIComponent(row.id)}`);
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      const runId = data.run_id;
      if (runId) {
        try {
          const socket = window.BorealisSocket;
          socket && socket.emit("ansible_playbook_cancel", { run_id: runId });
        } catch {
          /* ignore socket errors */
        }
      } else {
        alert("Unable to locate run id for this playbook run.");
      }
    } catch (e) {
      alert(String(e.message || e));
    }
  }, []);

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
      onCancelAnsible: handleCancelAnsibleRun,
    }),
    [handleViewOutput, handleCancelAnsibleRun]
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

  const topTabRenderers = [
    renderDeviceSummaryTab,
    renderStorageTab,
    renderMemoryTab,
    renderNetworkTab,
    renderSoftware,
    renderHistory,
  ];
  const tabContent = (topTabRenderers[tab] || renderDeviceSummaryTab)();

  return (
    <Box
      sx={{
        m: 0,
        p: { xs: 2, md: 3 },
        borderRadius: 0,
        background: MAGIC_UI.shellBg,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        boxShadow: MAGIC_UI.glow,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
      }}
    >
      <Box
        sx={{
          mb: 3,
          display: "grid",
          gridTemplateColumns: { xs: "1fr", lg: "1.5fr auto auto" },
          alignItems: "center",
          gap: 2,
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: 2, flexWrap: "wrap", minWidth: 0 }}>
          {onBack && (
            <Button
              variant="outlined"
              size="small"
              onClick={onBack}
              sx={{
                textTransform: "none",
                borderColor: "rgba(148,163,184,0.45)",
                color: MAGIC_UI.textBright,
                borderRadius: 999,
                px: 2,
              }}
            >
              Back
            </Button>
          )}
          <Box>
            <Typography
              variant="h6"
              sx={{ color: MAGIC_UI.textBright, display: "flex", alignItems: "center", gap: 1 }}
            >
              <Box
                component="span"
                sx={{
                  width: 10,
                  height: 10,
                  borderRadius: 10,
                  backgroundColor: statusColor(status),
                  boxShadow: `0 0 12px ${statusColor(status)}`,
                }}
              />
              {agent.hostname || "Device Details"}
            </Typography>
            <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
              GUID: {meta.agentGuid || summary.agent_guid || "unknown"}
            </Typography>
          </Box>
        </Box>

        <Box
          sx={{
            display: "flex",
            flexWrap: "wrap",
            gap: 1.2,
            justifyContent: { xs: "flex-start", lg: "center" },
          }}
        >
          <MetricCard
            compact
            icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 24 }} />}
            title="Processor"
            main={deviceMetricData.cpuMain}
            sub={deviceMetricData.cpuSub}
          />
          <MetricCard
            compact
            icon={<MemoryRoundedIcon sx={{ fontSize: 24 }} />}
            title="RAM"
            main={deviceMetricData.memVal}
            sub={deviceMetricData.memSpeed || " "}
          />
          <MetricCard
            compact
            icon={<StorageRoundedIcon sx={{ fontSize: 24 }} />}
            title="Storage"
            main={deviceMetricData.storageMain}
            sub={deviceMetricData.storageSub || " "}
          />
          <MetricCard
            compact
            icon={<SpeedRoundedIcon sx={{ fontSize: 24 }} />}
            title="Network"
            main={deviceMetricData.netVal}
            sub={deviceMetricData.nicLabel}
          />
        </Box>

        <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, justifyContent: "flex-end" }}>
          <IconButton
            size="small"
            disabled={!(agent?.hostname || device?.hostname)}
            onClick={(e) => setMenuAnchor(e.currentTarget)}
            sx={{
              color: !(agent?.hostname || device?.hostname) ? MAGIC_UI.textMuted : MAGIC_UI.textBright,
              border: "1px solid rgba(148,163,184,0.45)",
              borderRadius: 2,
              width: 38,
              height: 38,
              backgroundColor: "rgba(4,7,17,0.6)",
            }}
          >
            <MoreHorizIcon fontSize="small" />
          </IconButton>
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
              onClick={() => {
                setMenuAnchor(null);
                setQuickJobOpen(true);
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
        </Box>
      </Box>
      <Tabs
        value={tab}
        onChange={(e, v) => setTab(v)}
        variant="scrollable"
        scrollButtons="auto"
        TabIndicatorProps={{
          style: {
            height: 3,
            borderRadius: 3,
            background: "linear-gradient(90deg, #7dd3fc, #c084fc)",
          },
        }}
        sx={{
          borderBottom: `1px solid rgba(148,163,184,0.35)`,
          mb: 2,
          "& .MuiTab-root": {
            color: MAGIC_UI.textMuted,
            textTransform: "none",
            fontWeight: 600,
          },
          "& .Mui-selected": { color: MAGIC_UI.textBright },
        }}
      >
        {TOP_TABS.map((label) => (
          <Tab key={label} label={label} />
        ))}
      </Tabs>
      <Box sx={{ mt: 1, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        {tabContent}
      </Box>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            boxShadow: "0 25px 80px rgba(2,6,23,0.85)",
          },
        }}
      >
        <DialogTitle>{outputTitle}</DialogTitle>
        <DialogContent>
          <Box
            sx={{
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              bgcolor: "rgba(4,7,17,0.65)",
              maxHeight: 500,
              overflow: "auto",
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
              }}
              textareaProps={{ readOnly: true }}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button
            onClick={() => setOutputOpen(false)}
            sx={{ color: MAGIC_UI.accentA, textTransform: "none" }}
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

      {quickJobOpen && (
        <QuickJob
          open={quickJobOpen}
          onClose={() => setQuickJobOpen(false)}
          hostnames={[agent?.hostname || device?.hostname].filter(Boolean)}
        />
      )}
    </Box>
  );
}
