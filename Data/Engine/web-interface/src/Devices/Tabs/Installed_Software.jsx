import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import CheckCircleOutlineRoundedIcon from "@mui/icons-material/CheckCircleOutlineRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, LinearProgress, Typography } from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import { ConfirmDeleteDialog } from "../../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import { CountSliderGroup } from "../../Automation/Watchdogs/shared.jsx";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
} from "./Shared.jsx";

const ACTION_BUTTON_SX = {
  minWidth: 118,
  minHeight: 34,
  borderRadius: 999,
  px: 1.8,
  textTransform: "none",
  fontWeight: 600,
  color: MAGIC_UI.textBright,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.76)",
    borderColor: "rgba(148,163,184,0.22)",
    background: "rgba(15,23,42,0.42)",
  },
};

const SOFTWARE_DISTRIBUTION_ICON_SX = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  minWidth: 24,
  color: "#8fbfff",
  fontSize: "1.3rem",
  filter: "drop-shadow(0 0 8px rgba(59,130,246,0.2))",
};

const SOFTWARE_ICON_IMAGE_SX = {
  width: 27,
  height: 27,
  objectFit: "contain",
  flexShrink: 0,
  borderRadius: 0,
  background: "transparent",
  border: "none",
};

const SOFTWARE_DISTRIBUTION_BADGE_SX = {
  ...SOFTWARE_DISTRIBUTION_ICON_SX,
  minWidth: 18,
  fontSize: "1rem",
  color: "#8fbfff",
};

const SOFTWARE_FILTER_OPTIONS = [
  { key: "locally_installed", label: "Locally Installed" },
  { key: "windows_store", label: "Windows Store" },
  { key: "snap_package", label: "Snap Package" },
];

const WINDOWS_GUID_RE = /^\{[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}$/;
const WINDOWS_GUID_IN_TEXT_RE = /\{[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}/;
const WINDOWS_QUIET_SWITCH_RE =
  /(^|\s)(\/quiet|\/qn|\/qb!?|\/passive|\/s(\s|$)|\/silent|\/verysilent|--silent|--quiet|\/suppressmsgboxes)(\s|$)/i;
const STEAM_UNINSTALL_PROTOCOL_RE = /\bsteam:\/\/uninstall\/(?<appId>\d+)\b/i;
const STEAM_LIBRARY_PATH_RE = /(^|[\\/])steamapps[\\/]+common([\\/]|$)/i;
const SIZE_UNITS = ["KB", "MB", "GB", "TB"];
const UNINSTALL_JOB_TERMINAL_STATUSES = new Set(["success", "failed"]);

const UNINSTALL_STATUS_THEME = {
  queued: {
    label: "Queued",
    color: "#8fbfff",
    accent: "rgba(96,165,250,0.18)",
    border: "rgba(96,165,250,0.34)",
    progress: "linear-gradient(90deg, rgba(125,211,252,0.88) 0%, rgba(96,165,250,0.9) 100%)",
    helper: "Borealis queued the uninstall job and is waiting for the device to start it.",
    Icon: AutorenewRoundedIcon,
  },
  running: {
    label: "Running",
    color: "#7dd3fc",
    accent: "rgba(34,211,238,0.16)",
    border: "rgba(34,211,238,0.32)",
    progress: "linear-gradient(90deg, rgba(125,211,252,0.92) 0%, rgba(192,132,252,0.92) 100%)",
    helper: "The device is actively running the uninstall. Borealis will update this panel when the job finishes.",
    Icon: AutorenewRoundedIcon,
  },
  success: {
    label: "Complete",
    color: "#86efac",
    accent: "rgba(34,197,94,0.16)",
    border: "rgba(74,222,128,0.34)",
    progress: "linear-gradient(90deg, rgba(110,255,187,0.92) 0%, rgba(52,211,153,0.94) 100%)",
    helper: "The uninstall finished successfully. Full output remains available in Activity History.",
    Icon: CheckCircleOutlineRoundedIcon,
  },
  failed: {
    label: "Failed",
    color: "#fda4af",
    accent: "rgba(244,63,94,0.16)",
    border: "rgba(251,113,133,0.34)",
    progress: "linear-gradient(90deg, rgba(251,113,133,0.92) 0%, rgba(248,113,113,0.94) 100%)",
    helper: "The uninstall finished with an error. Review the captured output in Activity History for the full command log.",
    Icon: ErrorOutlineRoundedIcon,
  },
};

function getSoftwareMetadata(row = {}) {
  const metadata =
    row?.metadata && typeof row.metadata === "object" && !Array.isArray(row.metadata)
      ? { ...row.metadata }
      : {};
  return Object.entries(row || {}).reduce((accumulator, [key, value]) => {
    if (
      ["name", "version", "source", "metadata", "uninstall", "distribution_platform", "distribution_app_id"].includes(
        key
      )
    ) {
      return accumulator;
    }
    if (value == null || value === "" || (Array.isArray(value) && value.length === 0)) {
      return accumulator;
    }
    if (typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0) {
      return accumulator;
    }
    if (
      accumulator[key] == null ||
      accumulator[key] === "" ||
      (Array.isArray(accumulator[key]) && accumulator[key].length === 0) ||
      (typeof accumulator[key] === "object" &&
        !Array.isArray(accumulator[key]) &&
        Object.keys(accumulator[key] || {}).length === 0)
    ) {
      accumulator[key] = value;
    }
    return accumulator;
  }, metadata);
}

function getSoftwareDistribution(row = {}) {
  const platform = String(row?.distribution_platform || "").trim().toLowerCase();
  const appId = String(row?.distribution_app_id || "").trim();
  if (platform) {
    return { platform, appId };
  }
  const metadata = getSoftwareMetadata(row);
  const uninstallString = String(metadata?.uninstall_string || "").trim();
  const installLocation = trimWindowsPath(metadata?.install_location || "");
  const steamMatch = uninstallString.match(STEAM_UNINSTALL_PROTOCOL_RE);
  if (steamMatch || STEAM_LIBRARY_PATH_RE.test(installLocation)) {
    return {
      platform: "steam",
      appId: String(steamMatch?.groups?.appId || steamMatch?.[1] || "").trim(),
    };
  }
  return { platform: "", appId: "" };
}

function splitWindowsCommandLine(commandLine = "") {
  const text = String(commandLine || "").trim();
  if (!text) return null;
  const quotedMatch = text.match(/^\s*"([^"]+)"\s*(.*)$/);
  if (quotedMatch) {
    return {
      filePath: String(quotedMatch[1] || "").trim(),
      arguments: String(quotedMatch[2] || "").trim(),
    };
  }
  const extensionMatch = text.match(
    /^\s*((?:(?:[A-Za-z]:|\\\\[^\\/]+\\[^\\/]+)[^\r\n"]*?\.(?:exe|com|cmd|bat|msi|ps1)|[^\\/\s"]+\.(?:exe|com|cmd|bat|msi|ps1)))\s*(.*)$/i
  );
  if (extensionMatch) {
    return {
      filePath: String(extensionMatch[1] || "").trim(),
      arguments: String(extensionMatch[2] || "").trim(),
    };
  }
  const parts = text.split(/\s+/, 2);
  if (!parts.length) return null;
  return {
    filePath: String(parts[0] || "").trim(),
    arguments: String(parts[1] || "").trim(),
  };
}

function trimWindowsPath(value = "") {
  return String(value || "").trim().replace(/[\\/]+$/, "");
}

function deriveFallbackUninstallCapability(row = {}) {
  const source = String(row?.source || "").trim().toLowerCase();
  const metadata = getSoftwareMetadata(row);
  const distribution = getSoftwareDistribution(row);
  if (distribution.platform === "steam") {
    return { supported: false, reason: "", summary: "" };
  }
  if (["windows_store", "appx", "ms_store", "store"].includes(source)) {
    const packageFamilyName = String(metadata?.package_family_name || "").trim();
    const nonRemovable = metadata?.non_removable;
    if (nonRemovable !== true && packageFamilyName) {
      return { supported: true, reason: "", summary: "Windows Store package uninstall." };
    }
    return { supported: false, reason: "", summary: "" };
  }

  if (!["local_installed", "installed", "registry", "local", "uninstall_registry"].includes(source)) {
    return { supported: false, reason: "", summary: "" };
  }

  const quietUninstallString = String(metadata?.quiet_uninstall_string || "").trim();
  const uninstallString = String(metadata?.uninstall_string || "").trim();
  const productCode = String(metadata?.product_code || "").trim();
  const installLocation = trimWindowsPath(metadata?.install_location || "");
  const publisher = String(metadata?.publisher || "").trim().toLowerCase();
  const softwareName = String(row?.name || "").trim().toLowerCase();
  const version = String(row?.version || "").trim();
  if (quietUninstallString) {
    return { supported: true, reason: "", summary: "Uses the registry quiet uninstall string." };
  }
  if (WINDOWS_GUID_RE.test(productCode) || WINDOWS_GUID_IN_TEXT_RE.test(uninstallString)) {
    return { supported: true, reason: "", summary: "Uses MSI uninstall metadata." };
  }
  if (WINDOWS_QUIET_SWITCH_RE.test(uninstallString)) {
    return { supported: true, reason: "", summary: "The uninstall string already includes quiet flags." };
  }
  const parsed = splitWindowsCommandLine(uninstallString);
  const executableName = String(parsed?.filePath || "")
    .split("\\")
    .pop()
    ?.trim()
    .toLowerCase();
  const existingArguments = String(parsed?.arguments || "").trim();
  if (parsed && executableName?.startsWith("unins")) {
    return { supported: true, reason: "", summary: "Derived Inno Setup silent uninstall." };
  }
  if (parsed && executableName === "update.exe") {
    return { supported: true, reason: "", summary: "Derived Squirrel-style silent uninstall." };
  }
  if (
    parsed &&
    executableName === "setup.exe" &&
    /\b--uninstall\b/i.test(existingArguments) &&
    /(google chrome|microsoft edge|webview2 runtime)/i.test(String(row?.name || ""))
  ) {
    return { supported: true, reason: "", summary: "Derived setup.exe uninstall." };
  }
  if (installLocation && publisher.includes("igor pavlov") && softwareName.includes("7-zip")) {
    return { supported: true, reason: "", summary: "Derived 7-Zip uninstall from install location." };
  }
  if (
    installLocation &&
    ((publisher.includes("mozilla") && softwareName.includes("firefox")) ||
      (publisher.includes("betterbird project") && softwareName.includes("betterbird")))
  ) {
    return { supported: true, reason: "", summary: "Derived helper.exe uninstall from install location." };
  }
  if (installLocation && publisher.includes("irfan skiljan") && softwareName.includes("irfanview")) {
    return { supported: true, reason: "", summary: "Derived IrfanView uninstall from install location." };
  }
  if (
    installLocation &&
    version &&
    publisher.includes("microsoft corporation") &&
    (softwareName === "microsoft edge" || softwareName.includes("microsoft edge webview2 runtime"))
  ) {
    return { supported: true, reason: "", summary: "Derived Edge uninstall from install location and version." };
  }
  return { supported: false, reason: "", summary: "" };
}

function buildSoftwareActionKey(row = {}) {
  return [
    String(row?.name || "").trim().toLowerCase(),
    String(row?.version || "").trim().toLowerCase(),
    String(row?.source || "").trim().toLowerCase(),
  ].join("::");
}

function getSoftwareIconHash(row = {}) {
  const metadata = getSoftwareMetadata(row);
  return String(metadata?.icon_hash || "").trim().toLowerCase();
}

function buildSoftwareIconUrl(iconHash = "") {
  const normalizedHash = String(iconHash || "").trim().toLowerCase();
  return normalizedHash ? `/api/device/software/icon/${encodeURIComponent(normalizedHash)}` : "";
}

function SoftwareIconGlyph({ row = {} }) {
  const distribution = getSoftwareDistribution(row);
  const isSteam = distribution.platform === "steam";
  const iconHash = getSoftwareIconHash(row);
  const [iconFailed, setIconFailed] = useState(false);

  useEffect(() => {
    setIconFailed(false);
  }, [iconHash]);

  const iconUrl = useMemo(() => buildSoftwareIconUrl(iconHash), [iconHash]);
  const showImage = Boolean(iconUrl) && !iconFailed;
  const showSteamBadge = isSteam && (showImage || !iconHash);

  if (!showImage && !showSteamBadge) {
    return null;
  }

  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: showImage && showSteamBadge ? 0.5 : 0,
        flexShrink: 0,
      }}
    >
      {showImage ? (
        <Box
          component="img"
          src={iconUrl}
          alt=""
          loading="lazy"
          onError={() => setIconFailed(true)}
          sx={SOFTWARE_ICON_IMAGE_SX}
        />
      ) : null}
      {showSteamBadge ? (
        <Box
          component="span"
          sx={showImage ? SOFTWARE_DISTRIBUTION_BADGE_SX : SOFTWARE_DISTRIBUTION_ICON_SX}
          title={distribution.appId ? `Steam-managed title (AppID ${distribution.appId})` : "Steam-managed title"}
        >
          <i className="fa-brands fa-steam" aria-hidden="true" />
        </Box>
      ) : null}
    </Box>
  );
}

function SoftwareNameCell({ row = {} }) {
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 1,
        minWidth: 0,
      }}
    >
      <SoftwareIconGlyph row={row} />
      <Box
        component="span"
        sx={{
          minWidth: 0,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {String(row?.name || "—").trim() || "—"}
      </Box>
    </Box>
  );
}

function getUninstallCapability(row = {}, hostname = "") {
  const fallback = deriveFallbackUninstallCapability(row);
  const uninstall = row?.uninstall && typeof row.uninstall === "object" ? row.uninstall : null;
  if (uninstall) {
    if (!uninstall.supported && fallback.supported) {
      return fallback;
    }
    return {
      supported: Boolean(uninstall.supported),
      reason: String(uninstall.reason || "").trim(),
      summary: String(uninstall.summary || "").trim(),
    };
  }
  if (fallback.supported) {
    return fallback;
  }
  if (!hostname) {
    return { supported: false, reason: "The selected device is missing a hostname.", summary: "" };
  }
  return {
    supported: false,
    reason: "Borealis has not resolved uninstall capability for this software row yet.",
    summary: "",
  };
}

function resolveSoftwareFilterCategory(source = "") {
  const normalizedSource = String(source || "").trim().toLowerCase();
  if (normalizedSource === "windows_store" || normalizedSource === "appx") {
    return "windows_store";
  }
  if (normalizedSource === "snap" || normalizedSource === "snap_package") {
    return "snap_package";
  }
  if (["local_installed", "installed", "registry", "dpkg", "rpm"].includes(normalizedSource)) {
    return "locally_installed";
  }
  return "locally_installed";
}

function parseEstimatedSizeKb(value) {
  if (value == null || value === "" || typeof value === "boolean") return null;
  const normalizedValue =
    typeof value === "number" ? value : Number.parseInt(String(value).trim().replace(/,/g, ""), 10);
  if (!Number.isFinite(normalizedValue) || normalizedValue <= 0) return null;
  return normalizedValue;
}

function getEstimatedSizeKb(row = {}) {
  const metadata = getSoftwareMetadata(row);
  return parseEstimatedSizeKb(metadata?.estimated_size_kb);
}

function formatEstimatedSizeKb(sizeKb) {
  const normalizedSizeKb = parseEstimatedSizeKb(sizeKb);
  if (normalizedSizeKb == null) return "—";
  if (normalizedSizeKb < 1024) {
    return `${Math.round(normalizedSizeKb).toLocaleString()} KB`;
  }
  let value = normalizedSizeKb;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < SIZE_UNITS.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const decimals = value >= 10 ? 0 : 1;
  return `${value.toFixed(decimals)} ${SIZE_UNITS[unitIndex]}`;
}

function normalizeTrackedUninstallStatus(rawStatus = "") {
  const normalized = String(rawStatus || "").trim().toLowerCase();
  if (!normalized) return "Queued";
  if (normalized === "queued" || normalized === "pending" || normalized === "created") return "Queued";
  if (normalized === "running" || normalized === "started" || normalized === "in_progress") return "Running";
  if (normalized === "success" || normalized === "completed" || normalized === "complete") return "Success";
  if (normalized === "failed" || normalized === "error" || normalized === "timeout" || normalized === "timed_out") {
    return "Failed";
  }
  return normalized.charAt(0).toUpperCase() + normalized.slice(1);
}

function isTrackedUninstallTerminal(status = "") {
  return UNINSTALL_JOB_TERMINAL_STATUSES.has(String(status || "").trim().toLowerCase());
}

function getTrackedUninstallProgressVariant(status = "") {
  return isTrackedUninstallTerminal(status) ? "determinate" : "indeterminate";
}

function getTrackedUninstallProgressValue(status = "") {
  if (!isTrackedUninstallTerminal(status)) return undefined;
  return 100;
}

function getTrackedUninstallTheme(status = "") {
  const normalized = String(status || "").trim().toLowerCase();
  return UNINSTALL_STATUS_THEME[normalized] || UNINSTALL_STATUS_THEME.queued;
}

function summarizeUninstallOutput(job = {}) {
  const stdoutLines = String(job?.stdout || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const stderrLines = String(job?.stderr || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const detail =
    String(job?.status || "").trim().toLowerCase() === "failed"
      ? stderrLines[0] || stdoutLines[stdoutLines.length - 1] || ""
      : stdoutLines[stdoutLines.length - 1] || stderrLines[0] || "";
  return detail.length > 220 ? `${detail.slice(0, 217)}...` : detail;
}

function mergeTrackedUninstall(previousJob = {}, patch = {}) {
  const nextPatch = patch && typeof patch === "object" ? patch : {};
  return {
    ...previousJob,
    ...nextPatch,
    status: normalizeTrackedUninstallStatus(nextPatch.status ?? previousJob.status),
    stdout:
      nextPatch.stdout == null ? String(previousJob.stdout || "") : String(nextPatch.stdout || ""),
    stderr:
      nextPatch.stderr == null ? String(previousJob.stderr || "") : String(nextPatch.stderr || ""),
    updatedAt: Date.now(),
  };
}

function ActionCell({ data, hostname, busyKey, onRequestUninstall }) {
  const row = data || {};
  const eligibility = getUninstallCapability(row, hostname);
  if (!eligibility.supported) {
    return null;
  }
  const rowKey = buildSoftwareActionKey(row);
  const busy = rowKey === busyKey;
  const disabled = busy;
  const buttonLabel = busy ? "Queueing..." : "Uninstall";

  return (
    <span>
      <Button
        size="small"
        disabled={disabled}
        onClick={() => {
          if (disabled) return;
          onRequestUninstall?.(row);
        }}
        sx={ACTION_BUTTON_SX}
      >
        {buttonLabel}
      </Button>
    </span>
  );
}

export default function InstalledSoftwareTab({ softwareRows = [], hostname = "" }) {
  const [busyActionKey, setBusyActionKey] = useState("");
  const [confirmRow, setConfirmRow] = useState(null);
  const [softwareFilterMode, setSoftwareFilterMode] = useState("locally_installed");
  const [trackedUninstalls, setTrackedUninstalls] = useState([]);
  const [progressDialogJobId, setProgressDialogJobId] = useState("");
  const notifyOperator = useAppNotifications();
  const uninstallStatusRequestsRef = useRef(new Set());
  const uninstallCompletionToastsRef = useRef(new Set());
  const normalizedHostname = useMemo(() => String(hostname || "").trim(), [hostname]);

  useEffect(() => {
    if (!busyActionKey) return;
    const stillExists = softwareRows.some((row) => buildSoftwareActionKey(row) === busyActionKey);
    if (!stillExists) {
      setBusyActionKey("");
    }
  }, [busyActionKey, softwareRows]);

  const softwareFilterCounts = useMemo(
    () =>
      softwareRows.reduce(
        (counts, row) => {
          const category = resolveSoftwareFilterCategory(row?.source);
          counts[category] = (counts[category] || 0) + 1;
          return counts;
        },
        {
          locally_installed: 0,
          windows_store: 0,
          snap_package: 0,
        }
      ),
    [softwareRows]
  );

  const filteredSoftwareRows = useMemo(
    () =>
      softwareFilterMode
        ? softwareRows.filter((row) => resolveSoftwareFilterCategory(row?.source) === softwareFilterMode)
        : softwareRows,
    [softwareFilterMode, softwareRows]
  );

  const activeFilterLabel = useMemo(
    () =>
      SOFTWARE_FILTER_OPTIONS.find((option) => option.key === softwareFilterMode)?.label || "",
    [softwareFilterMode]
  );

  const activeTrackedUninstall = useMemo(
    () =>
      trackedUninstalls.find((job) => String(job?.jobId || "") === String(progressDialogJobId || "")) || null,
    [progressDialogJobId, trackedUninstalls]
  );

  const upsertTrackedUninstall = useCallback(
    (jobId, patch) => {
      const normalizedJobId = Number(jobId || 0);
      if (!Number.isFinite(normalizedJobId) || normalizedJobId <= 0) return;
      setTrackedUninstalls((previous) => {
        const index = previous.findIndex((job) => Number(job?.jobId || 0) === normalizedJobId);
        const baseJob =
          index >= 0
            ? previous[index]
            : {
                jobId: normalizedJobId,
                hostname: normalizedHostname,
                softwareName: "",
                softwareVersion: "",
                softwareSource: "",
                strategySummary: "",
                status: "Queued",
                stdout: "",
                stderr: "",
                queuedAt: Date.now(),
                updatedAt: Date.now(),
              };
        const nextJob = mergeTrackedUninstall(baseJob, patch);
        const remaining = previous.filter((job) => Number(job?.jobId || 0) !== normalizedJobId);
        return [nextJob, ...remaining].slice(0, 12);
      });
    },
    [normalizedHostname]
  );

  const loadTrackedUninstallStatus = useCallback(
    async (jobId) => {
      const normalizedJobId = Number(jobId || 0);
      if (!Number.isFinite(normalizedJobId) || normalizedJobId <= 0) return;
      if (uninstallStatusRequestsRef.current.has(normalizedJobId)) return;
      uninstallStatusRequestsRef.current.add(normalizedJobId);
      try {
        const response = await fetch(`/api/device/activity/job/${encodeURIComponent(normalizedJobId)}`, {
          credentials: "include",
        });
        if (!response.ok) {
          return;
        }
        const payload = await response.json().catch(() => ({}));
        upsertTrackedUninstall(normalizedJobId, {
          hostname: String(payload?.hostname || normalizedHostname || ""),
          status: payload?.status,
          stdout: payload?.stdout,
          stderr: payload?.stderr,
          ranAt: Number(payload?.ran_at || 0) || 0,
        });
      } catch (error) {
        console.warn("Failed to load uninstall activity status", error);
      } finally {
        uninstallStatusRequestsRef.current.delete(normalizedJobId);
      }
    },
    [normalizedHostname, upsertTrackedUninstall]
  );

  const requestUninstall = useCallback(
    (row) => {
      setConfirmRow(row || null);
    },
    []
  );

  const confirmUninstall = useCallback(async () => {
    const row = confirmRow;
    const rowKey = buildSoftwareActionKey(row || {});
    if (!row || !rowKey || !hostname || rowKey === busyActionKey) return;
    setBusyActionKey(rowKey);
    try {
      const response = await fetch(`/api/device/software/${encodeURIComponent(hostname)}/uninstall`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: row?.name || "",
          version: row?.version || "",
          source: row?.source || "",
        }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      const jobId = Number(payload?.job_id || 0);
      if (Number.isFinite(jobId) && jobId > 0) {
        upsertTrackedUninstall(jobId, {
          hostname: String(payload?.hostname || normalizedHostname || hostname || ""),
          softwareName: String(payload?.software?.name || row?.name || "Software"),
          softwareVersion: String(payload?.software?.version || row?.version || ""),
          softwareSource: String(payload?.software?.source || row?.source || ""),
          strategySummary: String(payload?.uninstall?.summary || ""),
          status: "Queued",
          stdout: "",
          stderr: "",
          queuedAt: Date.now(),
        });
        setProgressDialogJobId(String(jobId));
        void loadTrackedUninstallStatus(jobId);
      }
      setConfirmRow(null);
      await notifyOperator({
        title: "Software Uninstall Started",
        message: `Borealis started tracking the uninstall for <b>${String(row?.name || "Software")}</b> on <b>${hostname}</b>. A live progress panel is now open.`,
        icon: "info",
        variant: "success",
      });
    } catch (err) {
      await notifyOperator({
        title: "Software Uninstall Failed",
        message: `Could not queue the uninstall on <b>${hostname || "this device"}</b>: ${String(err?.message || err)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setBusyActionKey("");
    }
  }, [busyActionKey, confirmRow, hostname, loadTrackedUninstallStatus, normalizedHostname, notifyOperator, upsertTrackedUninstall]);

  useEffect(() => {
    const activeJobIds = trackedUninstalls
      .filter((job) => !isTrackedUninstallTerminal(job?.status))
      .map((job) => Number(job?.jobId || 0))
      .filter((jobId) => Number.isFinite(jobId) && jobId > 0);
    if (!activeJobIds.length) return undefined;
    const pollStatuses = () => {
      activeJobIds.forEach((jobId) => {
        void loadTrackedUninstallStatus(jobId);
      });
    };
    pollStatuses();
    const timer = window.setInterval(pollStatuses, 4000);
    return () => window.clearInterval(timer);
  }, [loadTrackedUninstallStatus, trackedUninstalls]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !normalizedHostname || !trackedUninstalls.length) return undefined;
    const trackedJobIds = new Set(
      trackedUninstalls
        .map((job) => Number(job?.jobId || 0))
        .filter((jobId) => Number.isFinite(jobId) && jobId > 0)
    );
    if (!trackedJobIds.size) return undefined;
    const expectedHost = normalizedHostname.toLowerCase();
    const handleActivityChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      const activityId = Number(payload?.activity_id || 0);
      if (!payloadHost || payloadHost !== expectedHost || !trackedJobIds.has(activityId)) return;
      upsertTrackedUninstall(activityId, {
        status: payload?.status || payload?.change,
      });
      void loadTrackedUninstallStatus(activityId);
    };
    socket.on("device_activity_changed", handleActivityChanged);
    return () => {
      socket.off("device_activity_changed", handleActivityChanged);
    };
  }, [loadTrackedUninstallStatus, normalizedHostname, trackedUninstalls, upsertTrackedUninstall]);

  useEffect(() => {
    trackedUninstalls.forEach((job) => {
      const jobId = Number(job?.jobId || 0);
      if (!Number.isFinite(jobId) || jobId <= 0) return;
      if (!isTrackedUninstallTerminal(job?.status)) return;
      if (uninstallCompletionToastsRef.current.has(jobId)) return;
      uninstallCompletionToastsRef.current.add(jobId);
      const detail = summarizeUninstallOutput(job);
      const succeeded = String(job?.status || "").trim().toLowerCase() === "success";
      const softwareLabel = String(job?.softwareName || "Software").trim() || "Software";
      const hostLabel = String(job?.hostname || normalizedHostname || hostname || "this device").trim() || "this device";
      void notifyOperator({
        title: succeeded ? "Software Uninstall Complete" : "Software Uninstall Failed",
        message: succeeded
          ? `The uninstall for <b>${softwareLabel}</b> on <b>${hostLabel}</b> finished.${detail ? ` ${detail}` : ""}`
          : `The uninstall for <b>${softwareLabel}</b> on <b>${hostLabel}</b> failed.${detail ? ` ${detail}` : ""}`,
        icon: succeeded ? "success" : "error",
        variant: succeeded ? "success" : "error",
      });
    });
  }, [hostname, normalizedHostname, notifyOperator, trackedUninstalls]);

  const softwareColumnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Software Name",
        flex: 1.2,
        minWidth: 240,
        filter: "agTextColumnFilter",
        cellRenderer: (params) => <SoftwareNameCell row={params.data} />,
      },
      {
        field: "version",
        headerName: "Version",
        width: 180,
        minWidth: 160,
        filter: "agTextColumnFilter",
      },
      {
        colId: "size",
        headerName: "Size",
        width: 140,
        minWidth: 130,
        filter: "agNumberColumnFilter",
        valueGetter: (params) => getEstimatedSizeKb(params.data),
        valueFormatter: (params) => formatEstimatedSizeKb(params.value),
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
      {
        field: "action",
        headerName: "Action",
        width: 170,
        minWidth: 150,
        sortable: false,
        filter: false,
        suppressHeaderMenuButton: true,
        cellRenderer: (params) => (
          <ActionCell
            data={params.data}
            hostname={hostname}
            busyKey={busyActionKey}
            onRequestUninstall={requestUninstall}
          />
        ),
      },
    ],
    [busyActionKey, hostname, requestUninstall]
  );

  const getSoftwareRowId = useCallback(
    (params) =>
      `${params.data?.name || "software"}-${params.data?.version || ""}-${params.data?.source || ""}-${params.rowIndex}`,
    []
  );

  const activeTrackedTheme = getTrackedUninstallTheme(activeTrackedUninstall?.status);
  const ActiveTrackedIcon = activeTrackedTheme.Icon;
  const activeTrackedProgressVariant = getTrackedUninstallProgressVariant(activeTrackedUninstall?.status);
  const activeTrackedProgressValue = getTrackedUninstallProgressValue(activeTrackedUninstall?.status);
  const activeTrackedDetail = summarizeUninstallOutput(activeTrackedUninstall);

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1.5,
        }}
      >
        <CountSliderGroup
          options={SOFTWARE_FILTER_OPTIONS}
          activeKey={softwareFilterMode}
          counts={softwareFilterCounts}
          onChange={setSoftwareFilterMode}
        />
        <Typography variant="body2" sx={{ color: "rgba(155, 163, 180, 0.96)" }}>
          {softwareFilterMode
            ? `Showing ${filteredSoftwareRows.length} ${activeFilterLabel.toLowerCase()} entr${filteredSoftwareRows.length === 1 ? "y" : "ies"}`
            : `Showing all ${filteredSoftwareRows.length} software entr${filteredSoftwareRows.length === 1 ? "y" : "ies"}`}
        </Typography>
      </Box>
      <GridShell sx={{ flexGrow: 1, minHeight: 360 }}>
        <AgGridReact
          rowData={filteredSoftwareRows}
          columnDefs={softwareColumnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          getRowId={getSoftwareRowId}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>
      <ConfirmDeleteDialog
        open={Boolean(confirmRow)}
        onCancel={() => {
          if (busyActionKey) return;
          setConfirmRow(null);
        }}
        onConfirm={confirmUninstall}
        title="Uninstall Software"
        confirmLabel={busyActionKey ? "Queueing..." : "Uninstall"}
        confirmDisabled={Boolean(busyActionKey)}
        message={
          confirmRow
            ? `Borealis will ask ${hostname || "this device"} to silently uninstall ${confirmRow.name}${
                confirmRow.version ? ` ${confirmRow.version}` : ""
              }. Output will be recorded in Activity History.`
            : ""
        }
      />
      <Dialog
        open={Boolean(activeTrackedUninstall)}
        onClose={() => setProgressDialogJobId("")}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={
              activeTrackedUninstall
                ? `Uninstalling ${activeTrackedUninstall.softwareName || "Software"}`
                : "Uninstalling Software"
            }
            subtitle={
              activeTrackedUninstall
                ? `Live activity tracking for ${activeTrackedUninstall.hostname || hostname || "this device"}. Borealis tracks job state here and records the full command output in Activity History.`
                : "Borealis is tracking the uninstall job."
            }
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          {activeTrackedUninstall ? (
            <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <Box
                sx={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 1.5,
                  p: 1.5,
                  borderRadius: 2.5,
                  border: `1px solid ${activeTrackedTheme.border}`,
                  background: `linear-gradient(135deg, ${activeTrackedTheme.accent} 0%, rgba(7,12,24,0.18) 100%)`,
                }}
              >
                <Box sx={{ display: "flex", alignItems: "center", gap: 1.2, minWidth: 0 }}>
                  <Box
                    sx={{
                      width: 34,
                      height: 34,
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      borderRadius: "50%",
                      color: activeTrackedTheme.color,
                      background: "rgba(5,10,24,0.66)",
                      border: `1px solid ${activeTrackedTheme.border}`,
                    }}
                  >
                    <ActiveTrackedIcon sx={{ fontSize: 20 }} />
                  </Box>
                  <Box sx={{ minWidth: 0 }}>
                    <Typography sx={{ fontWeight: 700, color: MAGIC_UI.textBright, lineHeight: 1.2 }}>
                      {activeTrackedTheme.label}
                    </Typography>
                    <Typography sx={{ mt: 0.35, fontSize: "0.84rem", color: MAGIC_UI.textMuted }}>
                      {activeTrackedTheme.helper}
                    </Typography>
                  </Box>
                </Box>
                <Box
                  component="span"
                  sx={{
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    px: 1.35,
                    py: 0.45,
                    borderRadius: 999,
                    fontSize: "0.78rem",
                    fontWeight: 700,
                    color: activeTrackedTheme.color,
                    border: `1px solid ${activeTrackedTheme.border}`,
                    background: "rgba(5,10,24,0.72)",
                    whiteSpace: "nowrap",
                  }}
                >
                  {activeTrackedTheme.label}
                </Box>
              </Box>
              <Box
                sx={{
                  p: 1.6,
                  borderRadius: 2.5,
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  background: "rgba(5,10,24,0.72)",
                  display: "flex",
                  flexDirection: "column",
                  gap: 1.2,
                }}
              >
                <Box sx={{ display: "flex", justifyContent: "space-between", gap: 2, flexWrap: "wrap" }}>
                  <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 600 }}>
                    {activeTrackedUninstall.softwareName || "Software"}
                    {activeTrackedUninstall.softwareVersion ? ` ${activeTrackedUninstall.softwareVersion}` : ""}
                  </Typography>
                  <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.84rem" }}>
                    Job #{activeTrackedUninstall.jobId}
                  </Typography>
                </Box>
                <LinearProgress
                  variant={activeTrackedProgressVariant}
                  value={activeTrackedProgressValue}
                  sx={{
                    height: 9,
                    borderRadius: 999,
                    backgroundColor: "rgba(148,163,184,0.16)",
                    overflow: "hidden",
                    "& .MuiLinearProgress-bar": {
                      borderRadius: 999,
                      backgroundImage: activeTrackedTheme.progress,
                    },
                  }}
                />
                <Box sx={{ display: "flex", justifyContent: "space-between", gap: 2, flexWrap: "wrap" }}>
                  <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.84rem" }}>
                    {activeTrackedUninstall.strategySummary || "Borealis is using the resolved silent uninstall path for this software."}
                  </Typography>
                  <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.84rem" }}>
                    {isTrackedUninstallTerminal(activeTrackedUninstall.status)
                      ? "Finished"
                      : "Waiting for completion..."}
                  </Typography>
                </Box>
              </Box>
              {activeTrackedDetail ? (
                <Box
                  sx={{
                    p: 1.4,
                    borderRadius: 2.25,
                    border: `1px solid ${MAGIC_UI.panelBorder}`,
                    background: "rgba(4,7,17,0.6)",
                  }}
                >
                  <Typography sx={{ fontSize: "0.78rem", fontWeight: 700, color: MAGIC_UI.textMuted, letterSpacing: 0.24 }}>
                    Latest Output
                  </Typography>
                  <Typography sx={{ mt: 0.55, color: MAGIC_UI.textBright, fontSize: "0.88rem", lineHeight: 1.5 }}>
                    {activeTrackedDetail}
                  </Typography>
                </Box>
              ) : null}
            </Box>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setProgressDialogJobId("")}>
            {isTrackedUninstallTerminal(activeTrackedUninstall?.status) ? "Close" : "Hide"}
          </Button>
          <Button
            sx={DIALOG_PRIMARY_BUTTON_SX}
            onClick={() => {
              if (activeTrackedUninstall?.jobId) {
                void loadTrackedUninstallStatus(activeTrackedUninstall.jobId);
              }
            }}
            disabled={!activeTrackedUninstall?.jobId || isTrackedUninstallTerminal(activeTrackedUninstall?.status)}
          >
            Refresh Status
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
