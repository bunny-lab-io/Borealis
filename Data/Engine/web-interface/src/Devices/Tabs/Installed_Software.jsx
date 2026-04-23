import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Menu,
  MenuItem,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import { AgGridReact } from "ag-grid-react";
import { ConfirmDeleteDialog } from "../../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
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
import UninstallProgressDialog, {
  isTrackedUninstallTerminal,
  mergeTrackedUninstall,
  summarizeUninstallOutput,
} from "./Uninstall_Progress_Dialog.jsx";

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

const PRIMARY_REFRESH_BUTTON_SX = {
  minWidth: 190,
  minHeight: 38,
  borderRadius: 999,
  px: 2.2,
  textTransform: "none",
  fontWeight: 700,
  color: "#031525",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 18px 38px rgba(76, 110, 245, 0.18)",
  "&:hover": {
    background: "linear-gradient(135deg, #93ddff 0%, #d0a0ff 100%)",
    boxShadow: "0 22px 42px rgba(76, 110, 245, 0.24)",
  },
  "&.Mui-disabled": {
    color: "rgba(2, 6, 23, 0.45)",
    background: "linear-gradient(135deg, rgba(125,211,252,0.4) 0%, rgba(192,132,252,0.4) 100%)",
    boxShadow: "none",
  },
};

const SOFTWARE_DIALOG_TEXT_FIELD_SX = {
  "& .MuiOutlinedInput-root": {
    borderRadius: 2,
    background: "rgba(7, 12, 26, 0.82)",
    color: MAGIC_UI.textBright,
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.28)",
    },
    "&:hover fieldset": {
      borderColor: "rgba(125,211,252,0.4)",
    },
    "&.Mui-focused fieldset": {
      borderColor: "rgba(125,211,252,0.62)",
      boxShadow: "0 0 0 1px rgba(125,211,252,0.12)",
    },
  },
  "& .MuiInputLabel-root": {
    color: "rgba(191,219,254,0.88)",
  },
  "& .MuiInputLabel-root.Mui-focused": {
    color: "#9bd7ff",
  },
  "& .MuiFormHelperText-root": {
    color: "rgba(148,163,184,0.86)",
    marginLeft: 0,
  },
  "& .MuiSelect-icon": {
    color: "#9bd7ff",
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

const STEAM_ACTION_BADGE_SX = {
  ...SOFTWARE_DISTRIBUTION_ICON_SX,
  minWidth: 34,
  minHeight: 34,
  borderRadius: 999,
  border: "1px solid rgba(148,163,184,0.28)",
  background: "rgba(5,10,24,0.74)",
  cursor: "help",
};

const SOFTWARE_SOURCE_BADGE_META = {
  locally_installed: {
    label: "Locally Installed",
    textColor: "#89c2ff",
    backgroundColor: "rgba(88, 166, 255, 0.16)",
    borderColor: "rgba(88, 166, 255, 0.45)",
  },
  windows_store: {
    label: "Windows Store",
    textColor: "#8fdaa2",
    backgroundColor: "rgba(56, 161, 105, 0.16)",
    borderColor: "rgba(56, 161, 105, 0.4)",
  },
  dpkg: {
    label: "DPKG",
    textColor: "#d4b5ff",
    backgroundColor: "rgba(180, 137, 255, 0.18)",
    borderColor: "rgba(180, 137, 255, 0.38)",
  },
  rpm: {
    label: "RPM",
    textColor: "#d4b5ff",
    backgroundColor: "rgba(180, 137, 255, 0.18)",
    borderColor: "rgba(180, 137, 255, 0.38)",
  },
};

const SOFTWARE_FILTER_OPTIONS = [
  { key: "locally_installed", label: "Locally Installed" },
  { key: "windows_store", label: "Windows Store" },
];

const WINDOWS_GUID_RE = /^\{[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}$/;
const WINDOWS_GUID_IN_TEXT_RE = /\{[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\}/;
const WINDOWS_QUIET_SWITCH_RE =
  /(^|\s)(\/quiet|\/qn|\/qb!?|\/passive|\/s(\s|$)|\/silent|\/verysilent|--silent|--quiet|\/suppressmsgboxes)(\s|$)/i;
const STEAM_UNINSTALL_PROTOCOL_RE = /\bsteam:\/\/uninstall\/(?<appId>\d+)\b/i;
const STEAM_LIBRARY_PATH_RE = /(^|[\\/])steamapps[\\/]+common([\\/]|$)/i;
const SIZE_UNITS = ["KB", "MB", "GB", "TB"];
const WINDOWS_SYSTEM_EXECUTABLES = new Set([
  "msiexec.exe",
  "rundll32.exe",
  "cmd.exe",
  "powershell.exe",
  "pwsh.exe",
  "wscript.exe",
  "cscript.exe",
  "regsvr32.exe",
]);
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

function escapePowerShellSingleQuoted(value = "") {
  return String(value || "").replace(/'/g, "''");
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

function isLikelyWindowsIconResource(value = "") {
  const text = String(value || "").trim();
  if (!text) return false;
  return /^\s*"?.+?\.(?:exe|dll|ico|icl|cpl|ocx|scr)"?\s*(?:,\s*-?\d+)?\s*$/i.test(text);
}

function toWindowsResourceCandidate(filePath = "", iconIndex = 0) {
  const normalizedPath = trimWindowsPath(String(filePath || "").replace(/\//g, "\\"));
  if (!normalizedPath) return "";
  if (!/\.(?:exe|dll|ico|icl|cpl|ocx|scr)$/i.test(normalizedPath)) return "";
  if (/\.ico$/i.test(normalizedPath)) {
    return normalizedPath;
  }
  return `${normalizedPath},${Number.isFinite(Number(iconIndex)) ? Number(iconIndex) : 0}`;
}

function pushUniqueText(list, value) {
  const normalizedValue = String(value || "").trim();
  if (!normalizedValue) return;
  if (!list.includes(normalizedValue)) {
    list.push(normalizedValue);
  }
}

function buildSoftwareNamePathTokens(name = "") {
  const baseName = String(name || "").trim();
  if (!baseName) return [];
  const variants = [];
  const withoutParens = baseName.replace(/\s*\([^)]*\)\s*/g, " ").replace(/\s+/g, " ").trim();
  const sanitized = withoutParens.replace(/[<>:"/\\|?*()]/g, "").trim();
  pushUniqueText(variants, withoutParens);
  pushUniqueText(variants, sanitized);
  for (const candidate of [...variants]) {
    pushUniqueText(variants, candidate.replace(/\s+/g, ""));
    pushUniqueText(variants, candidate.replace(/\s+/g, "_"));
    pushUniqueText(variants, candidate.replace(/\s+/g, "-"));
  }
  return variants.filter(Boolean);
}

function buildSuggestedIconCandidates(row = {}, parsedQuiet = null, parsedUninstall = null) {
  const metadata = getSoftwareMetadata(row);
  const candidates = [];
  const installLocation = trimWindowsPath(metadata?.install_location || "");

  const addCommandCandidate = (parsedCommand) => {
    const rawPath = String(parsedCommand?.filePath || "").trim();
    if (!rawPath) return;
    if (/^(?:[a-z]:\\|\\\\)/i.test(rawPath)) {
      pushUniqueText(candidates, toWindowsResourceCandidate(rawPath, 0));
      return;
    }
    const rawBaseName = rawPath.split(/[/\\]+/).filter(Boolean).pop()?.toLowerCase() || "";
    if (!installLocation) return;
    if (!/\.(?:exe|dll|ico|icl|cpl|ocx|scr)$/i.test(rawPath)) return;
    if (WINDOWS_SYSTEM_EXECUTABLES.has(rawBaseName)) return;
    pushUniqueText(candidates, toWindowsResourceCandidate(`${installLocation}\\${rawPath}`, 0));
  };

  addCommandCandidate(parsedQuiet);
  addCommandCandidate(parsedUninstall);

  if (installLocation) {
    const installLeaf = installLocation.split(/[/\\]+/).filter(Boolean).pop() || "";
    const nameTokens = buildSoftwareNamePathTokens(row?.name || "");
    const pathTokens = [];
    for (const token of nameTokens) {
      pushUniqueText(pathTokens, token);
    }
    pushUniqueText(pathTokens, installLeaf);
    for (const token of pathTokens) {
      pushUniqueText(candidates, toWindowsResourceCandidate(`${installLocation}\\${token}.exe`, 0));
    }
  }

  return candidates.filter(Boolean).slice(0, 8);
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

function deriveSoftwareNameFromActivity(activity = {}) {
  const metadata = activity?.metadata && typeof activity.metadata === "object" ? activity.metadata : {};
  const metadataName = String(metadata?.software_name || "").trim();
  if (metadataName) return metadataName;
  const scriptName = String(activity?.script_name || activity?.scriptName || "").trim();
  if (scriptName.toLowerCase().startsWith("uninstall - ")) {
    return scriptName.slice("uninstall - ".length).trim();
  }
  return "";
}

function getTrackedUninstallToastStorageKey(hostname = "") {
  const normalizedHostname = String(hostname || "").trim().toLowerCase();
  return normalizedHostname ? `borealis:tracked-uninstall-toasts:${normalizedHostname}` : "";
}

function readTrackedUninstallToastIds(hostname = "") {
  if (typeof window === "undefined" || !window.sessionStorage) {
    return new Set();
  }
  const storageKey = getTrackedUninstallToastStorageKey(hostname);
  if (!storageKey) return new Set();
  try {
    const raw = window.sessionStorage.getItem(storageKey);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(
      parsed
        .map((value) => Number(value || 0))
        .filter((value) => Number.isFinite(value) && value > 0)
    );
  } catch {
    return new Set();
  }
}

function writeTrackedUninstallToastIds(hostname = "", values = new Set()) {
  if (typeof window === "undefined" || !window.sessionStorage) return;
  const storageKey = getTrackedUninstallToastStorageKey(hostname);
  if (!storageKey) return;
  try {
    const serialized = Array.from(values)
      .map((value) => Number(value || 0))
      .filter((value) => Number.isFinite(value) && value > 0);
    window.sessionStorage.setItem(storageKey, JSON.stringify(serialized));
  } catch {
    // Ignore sessionStorage failures.
  }
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
  const iconHash = getSoftwareIconHash(row);
  const [iconFailed, setIconFailed] = useState(false);

  useEffect(() => {
    setIconFailed(false);
  }, [iconHash]);

  const iconUrl = useMemo(() => buildSoftwareIconUrl(iconHash), [iconHash]);
  const showImage = Boolean(iconUrl) && !iconFailed;
  if (!showImage) {
    return null;
  }

  return (
    <Box
      component="img"
      src={iconUrl}
      alt=""
      loading="lazy"
      onError={() => setIconFailed(true)}
      sx={SOFTWARE_ICON_IMAGE_SX}
    />
  );
}

function SoftwareNameCell({ row = {}, onOpenContextMenu }) {
  return (
    <Box
      onContextMenu={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onOpenContextMenu?.(event, row);
      }}
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 1,
        minWidth: 0,
        cursor: "context-menu",
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

function getUninstallCommandPreview(row = {}) {
  const distribution = getSoftwareDistribution(row);
  if (distribution.platform === "steam") {
    return "";
  }

  const metadata = getSoftwareMetadata(row);
  const uninstall = row?.uninstall && typeof row.uninstall === "object" ? row.uninstall : null;
  const fallback = deriveFallbackUninstallCapability(row);
  const uninstallSupported = uninstall ? Boolean(uninstall.supported) || fallback.supported : fallback.supported;
  if (!uninstallSupported) {
    return "";
  }

  const quietUninstallString = String(
    uninstall?.quiet_uninstall_string || metadata?.quiet_uninstall_string || ""
  ).trim();
  if (quietUninstallString) {
    return quietUninstallString;
  }

  const productCode = String(uninstall?.product_code || metadata?.product_code || "").trim();
  if (WINDOWS_GUID_RE.test(productCode)) {
    return `msiexec.exe /x ${productCode.toUpperCase()} /qn /norestart`;
  }

  const uninstallString = String(uninstall?.uninstall_string || metadata?.uninstall_string || "").trim();
  const guidMatch = uninstallString.match(WINDOWS_GUID_IN_TEXT_RE);
  if (guidMatch?.[0]) {
    return `msiexec.exe /x ${String(guidMatch[0]).toUpperCase()} /qn /norestart`;
  }

  if (WINDOWS_QUIET_SWITCH_RE.test(uninstallString)) {
    return uninstallString;
  }

  const source = String(row?.source || "").trim().toLowerCase();
  if (["windows_store", "appx", "ms_store", "store"].includes(source)) {
    const packageFamilyName = String(
      uninstall?.package_family_name || metadata?.package_family_name || ""
    ).trim();
    if (packageFamilyName) {
      return `Get-AppxPackage -AllUsers | Where-Object { $_.PackageFamilyName -eq '${escapePowerShellSingleQuoted(
        packageFamilyName
      )}' } | Remove-AppxPackage -AllUsers`;
    }
    const softwareName = String(row?.name || "").trim();
    if (softwareName) {
      return `Get-AppxPackage -AllUsers | Where-Object { $_.Name -eq '${escapePowerShellSingleQuoted(
        softwareName
      )}' } | Remove-AppxPackage -AllUsers`;
    }
  }

  return "";
}

function getCurrentSoftwareIconOverride(row = {}) {
  const metadata = getSoftwareMetadata(row);
  if (isSoftwareIconCleared(row)) {
    return "";
  }
  return [
    String(metadata?.display_icon_override || "").trim(),
    String(metadata?.display_icon || "").trim(),
    String(metadata?.original_display_icon || "").trim(),
  ].find((value) => isLikelyWindowsIconResource(value)) || "";
}

function isSoftwareIconCleared(row = {}) {
  const metadata = getSoftwareMetadata(row);
  const flag = metadata?.display_icon_override_cleared;
  if (typeof flag === "boolean") {
    return flag;
  }
  return ["1", "true", "yes", "on"].includes(String(flag || "").trim().toLowerCase());
}

function buildIconOverrideDialogState(row = {}) {
  const metadata = getSoftwareMetadata(row);
  const parsedQuiet = splitWindowsCommandLine(String(metadata?.quiet_uninstall_string || "").trim());
  const parsedUninstall = splitWindowsCommandLine(String(metadata?.uninstall_string || "").trim());
  const candidates = buildSuggestedIconCandidates(row, parsedQuiet, parsedUninstall);
  const currentDisplayIcon = getCurrentSoftwareIconOverride(row);
  const clearIcon = isSoftwareIconCleared(row);
  return {
    selectedCandidate: !clearIcon && currentDisplayIcon && candidates.includes(currentDisplayIcon) ? currentDisplayIcon : "",
    manualPath: clearIcon ? "" : currentDisplayIcon || "",
    candidates,
    clearIcon,
  };
}

function buildUninstallOverrideDialogState(row = {}) {
  const metadata = getSoftwareMetadata(row);
  const commandPreview =
    getUninstallCommandPreview(row) ||
    String(row?.uninstall?.quiet_uninstall_string || "").trim() ||
    String(metadata?.quiet_uninstall_string || "").trim() ||
    String(metadata?.uninstall_string || "").trim();
  const parsed =
    splitWindowsCommandLine(commandPreview) ||
    splitWindowsCommandLine(String(metadata?.quiet_uninstall_string || "").trim()) ||
    splitWindowsCommandLine(String(metadata?.uninstall_string || "").trim()) || {
      filePath: "",
      arguments: "",
    };
  return {
    applicationPath: String(parsed?.filePath || "").trim(),
    arguments: String(parsed?.arguments || "").trim(),
    commandPreview,
  };
}

function getSoftwareActionSuccessLabel(kind = "") {
  switch (String(kind || "").trim()) {
    case "icon_override":
      return "Icon Override";
    case "uninstall_override":
      return "Uninstall Override";
    case "uninstall_block":
      return "Uninstall Block";
    case "uninstall_unblock":
      return "Uninstall Unblock";
    default:
      return "Software Action";
  }
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

function getSoftwareSourceMeta(source = "") {
  const normalizedSource = String(source || "").trim().toLowerCase();
  if (normalizedSource === "windows_store" || normalizedSource === "appx") {
    return SOFTWARE_SOURCE_BADGE_META.windows_store;
  }
  if (normalizedSource === "dpkg") {
    return SOFTWARE_SOURCE_BADGE_META.dpkg;
  }
  if (normalizedSource === "rpm") {
    return SOFTWARE_SOURCE_BADGE_META.rpm;
  }
  if (["local_installed", "installed", "registry", "local", "uninstall_registry"].includes(normalizedSource)) {
    return SOFTWARE_SOURCE_BADGE_META.locally_installed;
  }
  return {
    label: source || "Unknown",
    textColor: "#96a3b6",
    backgroundColor: "rgba(150, 163, 182, 0.14)",
    borderColor: "rgba(150, 163, 182, 0.32)",
  };
}

function SoftwareSourceCell({ row = {} }) {
  const meta = getSoftwareSourceMeta(row?.source);
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        borderRadius: 999,
        px: "6px",
        py: "2px",
        fontSize: 11,
        fontWeight: 500,
        color: meta.textColor,
        border: `1px solid ${meta.borderColor}`,
        backgroundColor: meta.backgroundColor,
        textTransform: "none",
      }}
    >
      <Typography
        component="span"
        sx={{
          fontSize: 11,
          color: meta.textColor,
          lineHeight: 1,
          fontWeight: 500,
        }}
      >
        {meta.label}
      </Typography>
    </Box>
  );
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

function ActionCell({ data, hostname, busyKey, onRequestUninstall }) {
  const row = data || {};
  const eligibility = getUninstallCapability(row, hostname);
  const distribution = getSoftwareDistribution(row);
  if (!eligibility.supported) {
    if (distribution.platform === "steam") {
      return (
        <Tooltip title="Software needs to be uninstalled via Steam directly." placement="top">
          <Box component="span" sx={STEAM_ACTION_BADGE_SX}>
            <i className="fa-brands fa-steam" aria-hidden="true" />
          </Box>
        </Tooltip>
      );
    }
    return null;
  }
  const rowKey = buildSoftwareActionKey(row);
  const busy = rowKey === busyKey;
  const disabled = busy;
  const buttonLabel = busy ? "Queueing..." : "Uninstall";
  const uninstallCommand = getUninstallCommandPreview(row);
  const tooltipTitle = uninstallCommand || "Borealis will send the resolved uninstall command to the agent.";

  return (
    <Tooltip
      title={tooltipTitle}
      placement="top"
      slotProps={{
        tooltip: {
          sx: {
            maxWidth: 520,
            fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
            fontSize: "0.72rem",
            lineHeight: 1.45,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          },
        },
      }}
    >
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
    </Tooltip>
  );
}

export default function InstalledSoftwareTab({
  softwareRows = [],
  hostname = "",
  onSoftwareDataRefresh = null,
}) {
  const softwareGridRef = useRef(null);
  const [busyActionKey, setBusyActionKey] = useState("");
  const [confirmRow, setConfirmRow] = useState(null);
  const [softwareFilterMode, setSoftwareFilterMode] = useState("locally_installed");
  const [trackedUninstalls, setTrackedUninstalls] = useState([]);
  const [progressDialogJobId, setProgressDialogJobId] = useState("");
  const [softwareActionMenu, setSoftwareActionMenu] = useState({
    open: false,
    top: 0,
    left: 0,
    row: null,
  });
  const [softwareActionDialog, setSoftwareActionDialog] = useState({
    open: false,
    kind: "",
    row: null,
    selectedCandidate: "",
    manualPath: "",
    candidatePaths: [],
    clearIcon: false,
    applicationPath: "",
    arguments: "",
    reason: "",
    commandPreview: "",
    submitting: false,
  });
  const [softwareRefreshBusy, setSoftwareRefreshBusy] = useState(false);
  const notifyOperator = useAppNotifications();
  const uninstallStatusRequestsRef = useRef(new Set());
  const uninstallCompletionToastsRef = useRef(new Set());
  const trackedUninstallStatusesRef = useRef(new Map());
  const trackedUninstallHydratedRef = useRef(false);
  const [trackedUninstallsLoaded, setTrackedUninstallsLoaded] = useState(false);
  const normalizedHostname = useMemo(() => String(hostname || "").trim(), [hostname]);

  const requestSoftwareDataRefresh = useCallback(
    (options = {}) => {
      if (typeof onSoftwareDataRefresh === "function") {
        onSoftwareDataRefresh(options);
      }
    },
    [onSoftwareDataRefresh]
  );

  const handleOpenSoftwareActionMenu = useCallback((event, row) => {
    setSoftwareActionMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row: row || null,
    });
  }, []);

  const handleCloseSoftwareActionMenu = useCallback(() => {
    setSoftwareActionMenu({
      open: false,
      top: 0,
      left: 0,
      row: null,
    });
  }, []);

  const handleCloseSoftwareActionDialog = useCallback(() => {
    setSoftwareActionDialog({
      open: false,
      kind: "",
      row: null,
      selectedCandidate: "",
      manualPath: "",
      candidatePaths: [],
      clearIcon: false,
      applicationPath: "",
      arguments: "",
      reason: "",
      commandPreview: "",
      submitting: false,
    });
  }, []);

  const handleOpenSoftwareActionDialog = useCallback(
    async (kind, rowOverride = null) => {
      const row = rowOverride || softwareActionMenu?.row || null;
      handleCloseSoftwareActionMenu();
      if (!row) {
        await notifyOperator({
          title: "Software Action Unavailable",
          message: "Borealis could not determine which software row to target.",
          icon: "warning",
          variant: "warning",
        });
        return;
      }

      if (kind === "icon_override") {
        const nextState = buildIconOverrideDialogState(row);
        setSoftwareActionDialog({
          open: true,
          kind,
          row,
          selectedCandidate: nextState.selectedCandidate,
          manualPath: nextState.manualPath,
          candidatePaths: nextState.candidates,
          clearIcon: nextState.clearIcon,
          applicationPath: "",
          arguments: "",
          reason: "",
          commandPreview: "",
          submitting: false,
        });
        return;
      }

      if (kind === "uninstall_override") {
        const nextState = buildUninstallOverrideDialogState(row);
        setSoftwareActionDialog({
          open: true,
          kind,
          row,
          selectedCandidate: "",
          manualPath: "",
          candidatePaths: [],
          clearIcon: false,
          applicationPath: nextState.applicationPath,
          arguments: nextState.arguments,
          reason: "",
          commandPreview: nextState.commandPreview,
          submitting: false,
        });
        return;
      }

      setSoftwareActionDialog({
        open: true,
        kind,
        row,
        selectedCandidate: "",
        manualPath: "",
        candidatePaths: [],
        clearIcon: false,
        applicationPath: "",
        arguments: "",
        reason: "",
        commandPreview: "",
        submitting: false,
      });
    },
    [handleCloseSoftwareActionMenu, notifyOperator, softwareActionMenu]
  );

  const submitSoftwareAction = useCallback(async () => {
    const row = softwareActionDialog?.row || null;
    const kind = String(softwareActionDialog?.kind || "").trim();
    if (!row || !kind || !normalizedHostname) {
      return;
    }
    const softwareName = String(row?.name || "").trim() || "Software";
    const payload = {
      name: String(row?.name || "").trim(),
      version: String(row?.version || "").trim(),
      source: String(row?.source || "").trim(),
    };
    let endpoint = "";
    let body = payload;
    let refreshOptions = { burst: false };

    if (kind === "icon_override") {
      const clearIcon = Boolean(softwareActionDialog?.clearIcon);
      const selectedPath =
        String(softwareActionDialog?.manualPath || "").trim() ||
        String(softwareActionDialog?.selectedCandidate || "").trim();
      if (!clearIcon && !selectedPath) {
        await notifyOperator({
          title: "Icon Override Required",
          message: "Choose a suggested icon path or enter one manually before saving.",
          icon: "warning",
          variant: "warning",
        });
        return;
      }
      endpoint = `/api/device/software/${encodeURIComponent(normalizedHostname)}/icon-override`;
      body = {
        ...payload,
        ...(clearIcon ? { clear_icon: true } : { display_icon: selectedPath }),
      };
      refreshOptions = { burst: true };
    } else if (kind === "uninstall_override") {
      const applicationPath = String(softwareActionDialog?.applicationPath || "").trim();
      if (!applicationPath) {
        await notifyOperator({
          title: "Application Path Required",
          message: "Enter the application path Borealis should run for this uninstall override.",
          icon: "warning",
          variant: "warning",
        });
        return;
      }
      endpoint = `/api/device/software/${encodeURIComponent(normalizedHostname)}/uninstall-override`;
      body = {
        ...payload,
        application_path: applicationPath,
        arguments: String(softwareActionDialog?.arguments || "").trim(),
      };
      refreshOptions = { burst: false };
    } else if (kind === "uninstall_block") {
      const reason = String(softwareActionDialog?.reason || "").trim();
      if (!reason) {
        await notifyOperator({
          title: "Reason Required",
          message: "Enter a reason so other operators understand why Borealis blocks this uninstall.",
          icon: "warning",
          variant: "warning",
        });
        return;
      }
      endpoint = `/api/device/software/${encodeURIComponent(normalizedHostname)}/uninstall-block`;
      body = {
        ...payload,
        reason,
      };
      refreshOptions = { burst: false };
    } else if (kind === "uninstall_unblock") {
      endpoint = `/api/device/software/${encodeURIComponent(normalizedHostname)}/uninstall-unblock`;
      body = payload;
      refreshOptions = { burst: false };
    } else {
      return;
    }

    setSoftwareActionDialog((current) => ({ ...current, submitting: true }));
    try {
      const response = await fetch(endpoint, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const responsePayload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(responsePayload?.message || responsePayload?.error || `HTTP ${response.status}`);
      }
      handleCloseSoftwareActionDialog();
      requestSoftwareDataRefresh(refreshOptions);
      await notifyOperator({
        title: "Software Management Updated",
        message: `<b>${softwareName}</b> ${getSoftwareActionSuccessLabel(kind)} performed successfully.`,
        icon: "success",
        variant: "success",
      });
    } catch (error) {
      await notifyOperator({
        title: "Software Action Failed",
        message: `Borealis could not update <b>${softwareName}</b>: ${String(error?.message || error)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setSoftwareActionDialog((current) => ({ ...current, submitting: false }));
    }
  }, [
    handleCloseSoftwareActionDialog,
    normalizedHostname,
    notifyOperator,
    requestSoftwareDataRefresh,
    softwareActionDialog,
  ]);

  const handleQuerySoftwareUpdates = useCallback(async () => {
    if (!normalizedHostname || softwareRefreshBusy) return;
    setSoftwareRefreshBusy(true);
    try {
      const response = await fetch(`/api/device/software/${encodeURIComponent(normalizedHostname)}/refresh`, {
        method: "POST",
        credentials: "include",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      requestSoftwareDataRefresh({ burst: true });
      await notifyOperator({
        title: "Software Refresh Requested",
        message: `Borealis queued a software change query for <b>${normalizedHostname}</b>. Updated inventory should start appearing shortly.`,
        icon: "success",
        variant: "success",
      });
    } catch (error) {
      await notifyOperator({
        title: "Software Refresh Failed",
        message: `Borealis could not query software changes for <b>${normalizedHostname || "this device"}</b>: ${String(error?.message || error)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setSoftwareRefreshBusy(false);
    }
  }, [normalizedHostname, notifyOperator, requestSoftwareDataRefresh, softwareRefreshBusy]);

  useEffect(() => {
    uninstallCompletionToastsRef.current = readTrackedUninstallToastIds(normalizedHostname);
    trackedUninstallStatusesRef.current = new Map();
    trackedUninstallHydratedRef.current = false;
    setTrackedUninstallsLoaded(false);
  }, [normalizedHostname]);

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

  const softwareGridRefreshSignature = useMemo(
    () =>
      filteredSoftwareRows
        .map((row) => {
          const metadata = getSoftwareMetadata(row);
          return [
            buildSoftwareActionKey(row),
            getSoftwareIconHash(row),
            String(metadata?.display_icon_override || "").trim(),
            String(metadata?.display_icon || "").trim(),
            String(metadata?.original_display_icon || "").trim(),
            String(metadata?.display_icon_override_cleared || "").trim(),
          ].join("|");
        })
        .join("||"),
    [filteredSoftwareRows]
  );

  useEffect(() => {
    const api = softwareGridRef.current?.api;
    if (!api || typeof window === "undefined") return undefined;
    const frameId = window.requestAnimationFrame(() => {
      api.refreshCells({ force: true, columns: ["name"] });
      api.redrawRows();
    });
    return () => window.cancelAnimationFrame(frameId);
  }, [softwareGridRefreshSignature]);

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
                commandPreview: "",
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
        const metadata = payload?.metadata && typeof payload.metadata === "object" ? payload.metadata : {};
        upsertTrackedUninstall(normalizedJobId, {
          hostname: String(payload?.hostname || normalizedHostname || ""),
          scriptName: String(payload?.script_name || "").trim(),
          softwareName: deriveSoftwareNameFromActivity(payload),
          softwareVersion: String(metadata?.software_version || "").trim(),
          softwareSource: String(metadata?.software_source || "").trim(),
          commandPreview: String(metadata?.command_preview || "").trim(),
          status: payload?.status,
          stdout: payload?.stdout,
          stderr: payload?.stderr,
          ranAt: Number(payload?.ran_at || 0) || 0,
          startedAt: Number(payload?.started_at || 0) || 0,
          updatedAt: Number(payload?.updated_at || 0) || 0,
          finishedAt: Number(payload?.finished_at || 0) || 0,
          queueLane: String(payload?.queue_lane || "").trim(),
          activityKind: String(payload?.activity_kind || "").trim(),
        });
      } catch (error) {
        console.warn("Failed to load uninstall activity status", error);
      } finally {
        uninstallStatusRequestsRef.current.delete(normalizedJobId);
      }
    },
    [normalizedHostname, upsertTrackedUninstall]
  );

  const loadTrackedUninstallsFromHistory = useCallback(async () => {
    if (!normalizedHostname) {
      setTrackedUninstalls([]);
      setTrackedUninstallsLoaded(true);
      return;
    }
    try {
      const response = await fetch(`/api/device/activity/${encodeURIComponent(normalizedHostname)}`, {
        credentials: "include",
      });
      if (!response.ok) return;
      const payload = await response.json().catch(() => ({}));
      const historyRows = Array.isArray(payload?.history) ? payload.history : [];
      const uninstallRows = historyRows
        .filter((row) => {
          const activityKind = String(row?.activity_kind || "").trim().toLowerCase();
          return activityKind === "software_uninstall" || String(row?.script_name || "").startsWith("Uninstall - ");
        })
        .slice(0, 12);
      setTrackedUninstalls((previous) =>
        uninstallRows.map((row) => {
          const metadata = row?.metadata && typeof row.metadata === "object" ? row.metadata : {};
          const existing =
            previous.find((item) => Number(item?.jobId || 0) === Number(row?.id || 0)) ||
            {
              jobId: Number(row?.id || 0),
              hostname: String(row?.hostname || normalizedHostname || ""),
              stdout: "",
              stderr: "",
            };
          return mergeTrackedUninstall(existing, {
            jobId: Number(row?.id || 0),
            hostname: String(row?.hostname || normalizedHostname || ""),
            scriptName: String(row?.script_name || "").trim(),
            softwareName: deriveSoftwareNameFromActivity(row),
            softwareVersion: String(metadata?.software_version || "").trim(),
            softwareSource: String(metadata?.software_source || "").trim(),
            commandPreview: String(metadata?.command_preview || "").trim(),
            status: row?.status,
            queueLane: String(row?.queue_lane || "").trim(),
            activityKind: String(row?.activity_kind || "").trim(),
            ranAt: Number(row?.ran_at || 0) || 0,
            queuedAt: Number(row?.ran_at || 0) || 0,
            startedAt: Number(row?.started_at || 0) || 0,
            updatedAt: Number(row?.updated_at || 0) || 0,
            finishedAt: Number(row?.finished_at || 0) || 0,
          });
        })
      );
      uninstallRows
        .filter((row) => !isTrackedUninstallTerminal(row?.status))
        .forEach((row) => {
          const jobId = Number(row?.id || 0);
          if (Number.isFinite(jobId) && jobId > 0) {
            void loadTrackedUninstallStatus(jobId);
          }
        });
    } catch (error) {
      console.warn("Failed to load tracked uninstall history", error);
    } finally {
      setTrackedUninstallsLoaded(true);
    }
  }, [loadTrackedUninstallStatus, normalizedHostname]);

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
          commandPreview: String(payload?.uninstall?.command_preview || "").trim() || getUninstallCommandPreview(row),
          status: "Queued",
          stdout: "",
          stderr: "",
          queuedAt: Date.now(),
        });
        setProgressDialogJobId(String(jobId));
        void loadTrackedUninstallStatus(jobId);
        void loadTrackedUninstallsFromHistory();
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
  }, [
    busyActionKey,
    confirmRow,
    hostname,
    loadTrackedUninstallStatus,
    loadTrackedUninstallsFromHistory,
    normalizedHostname,
    notifyOperator,
    upsertTrackedUninstall,
  ]);

  useEffect(() => {
    void loadTrackedUninstallsFromHistory();
  }, [loadTrackedUninstallsFromHistory]);

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
    if (!socket || !normalizedHostname) return undefined;
    const expectedHost = normalizedHostname.toLowerCase();
    const handleActivityChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      const activityId = Number(payload?.activity_id || 0);
      if (!payloadHost || payloadHost !== expectedHost) return;
      void loadTrackedUninstallsFromHistory();
      if (Number.isFinite(activityId) && activityId > 0) {
        void loadTrackedUninstallStatus(activityId);
      }
    };
    socket.on("device_activity_changed", handleActivityChanged);
    return () => {
      socket.off("device_activity_changed", handleActivityChanged);
    };
  }, [loadTrackedUninstallStatus, loadTrackedUninstallsFromHistory, normalizedHostname]);

  useEffect(() => {
    if (!trackedUninstallsLoaded) return;
    const previousStatuses = trackedUninstallStatusesRef.current;
    const nextStatuses = new Map();
    let persistedChanged = false;

    if (!trackedUninstallHydratedRef.current) {
      trackedUninstalls.forEach((job) => {
        const jobId = Number(job?.jobId || 0);
        if (!Number.isFinite(jobId) || jobId <= 0) return;
        nextStatuses.set(jobId, String(job?.status || "").trim().toLowerCase());
        if (isTrackedUninstallTerminal(job?.status) && !uninstallCompletionToastsRef.current.has(jobId)) {
          uninstallCompletionToastsRef.current.add(jobId);
          persistedChanged = true;
        }
      });
      trackedUninstallStatusesRef.current = nextStatuses;
      trackedUninstallHydratedRef.current = true;
      if (persistedChanged) {
        writeTrackedUninstallToastIds(normalizedHostname, uninstallCompletionToastsRef.current);
      }
      return;
    }

    trackedUninstalls.forEach((job) => {
      const jobId = Number(job?.jobId || 0);
      if (!Number.isFinite(jobId) || jobId <= 0) return;
      const normalizedStatus = String(job?.status || "").trim().toLowerCase();
      nextStatuses.set(jobId, normalizedStatus);
      if (!isTrackedUninstallTerminal(normalizedStatus)) return;
      if (isTrackedUninstallTerminal(previousStatuses.get(jobId))) return;
      if (uninstallCompletionToastsRef.current.has(jobId)) return;
      uninstallCompletionToastsRef.current.add(jobId);
      persistedChanged = true;
      const detail = summarizeUninstallOutput(job);
      const succeeded = normalizedStatus === "success";
      const softwareLabel =
        String(job?.softwareName || deriveSoftwareNameFromActivity(job) || "Software").trim() || "Software";
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
    trackedUninstallStatusesRef.current = nextStatuses;
    if (persistedChanged) {
      writeTrackedUninstallToastIds(normalizedHostname, uninstallCompletionToastsRef.current);
    }
  }, [hostname, normalizedHostname, notifyOperator, trackedUninstalls, trackedUninstallsLoaded]);

  const softwareColumnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Software Name",
        flex: 1.2,
        minWidth: 240,
        filter: "agTextColumnFilter",
        cellRenderer: (params) => (
          <SoftwareNameCell row={params.data} onOpenContextMenu={handleOpenSoftwareActionMenu} />
        ),
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
        valueGetter: (params) => getSoftwareSourceMeta(params.data?.source).label,
        cellRenderer: (params) => <SoftwareSourceCell row={params.data} />,
      },
      {
        field: "action",
        headerName: "Action",
        width: 170,
        minWidth: 150,
        sortable: false,
        filter: false,
        suppressHeaderMenuButton: true,
        cellStyle: {
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
        },
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
    [busyActionKey, handleOpenSoftwareActionMenu, hostname, requestUninstall]
  );

  const getSoftwareRowId = useCallback(
    (params) =>
      `${params.data?.name || "software"}-${params.data?.version || ""}-${params.data?.source || ""}-${params.rowIndex}`,
    []
  );

  const softwareActionDialogRow = softwareActionDialog?.row || null;
  const softwareActionDialogKind = String(softwareActionDialog?.kind || "").trim();
  const softwareActionDialogTitle = useMemo(() => {
    const softwareName = String(softwareActionDialogRow?.name || "").trim() || "Software";
    switch (softwareActionDialogKind) {
      case "icon_override":
        return `Create Global Icon Override - ${softwareName}`;
      case "uninstall_override":
        return `Create Global Uninstall Override - ${softwareName}`;
      case "uninstall_block":
        return `Block Uninstallation - ${softwareName}`;
      case "uninstall_unblock":
        return `Unblock Uninstallation - ${softwareName}`;
      default:
        return "Software Action";
    }
  }, [softwareActionDialogKind, softwareActionDialogRow]);
  const softwareActionDialogSubtitle = useMemo(() => {
    switch (softwareActionDialogKind) {
      case "icon_override":
        return "Choose a candidate icon resource, enter a verified EXE, DLL, or ICO path, or remove the icon entirely. If no icon index is provided, Borealis uses icon index 0.";
      case "uninstall_override":
        return "Provide the executable path and arguments Borealis should trust globally for this software uninstall.";
      case "uninstall_block":
        return "Explain why Borealis should block uninstall attempts for this software so other operators understand the risk.";
      case "uninstall_unblock":
        return "This removes the matching global uninstall block rule for the selected software row.";
      default:
        return "";
    }
  }, [softwareActionDialogKind]);
  const softwareActionDialogConfirmLabel = useMemo(() => {
    switch (softwareActionDialogKind) {
      case "icon_override":
        return "Create Override";
      case "uninstall_override":
        return "Create Override";
      case "uninstall_block":
        return "Block";
      case "uninstall_unblock":
        return "Unblock";
      default:
        return "Save";
    }
  }, [softwareActionDialogKind]);

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
          alignItems: { xs: "stretch", md: "center" },
          justifyContent: "space-between",
          gap: 1.5,
          flexWrap: "wrap",
        }}
      >
        <CountSliderGroup
          options={SOFTWARE_FILTER_OPTIONS}
          activeKey={softwareFilterMode}
          counts={softwareFilterCounts}
          onChange={setSoftwareFilterMode}
        />
        <Button
          onClick={() => {
            void handleQuerySoftwareUpdates();
          }}
          disabled={!normalizedHostname || softwareRefreshBusy}
          startIcon={<RefreshRoundedIcon />}
          sx={PRIMARY_REFRESH_BUTTON_SX}
        >
          {softwareRefreshBusy ? "Querying..." : "Query Software Changes"}
        </Button>
      </Box>
      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 360,
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
          "& .ag-row-hover": {
            backgroundColor: "rgba(73,156,196,0.2) !important",
          },
          "& .ag-row-selected": {
            backgroundColor: "rgba(125,211,252,0.2) !important",
            boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
          },
        }}
      >
        <AgGridReact
          ref={softwareGridRef}
          rowData={filteredSoftwareRows}
          columnDefs={softwareColumnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={{
            mode: "singleRow",
            checkboxes: false,
            headerCheckbox: false,
            enableClickSelection: true,
          }}
          suppressCellFocus
          pagination
          paginationPageSize={100}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          getRowId={getSoftwareRowId}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>
      <Menu
        open={Boolean(softwareActionMenu?.open)}
        onClose={handleCloseSoftwareActionMenu}
        anchorReference="anchorPosition"
        anchorPosition={
          softwareActionMenu?.open
            ? {
                top: Number(softwareActionMenu?.top || 0),
                left: Number(softwareActionMenu?.left || 0),
              }
            : undefined
        }
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            backdropFilter: "blur(14px)",
            borderRadius: 2,
            minWidth: 260,
          },
        }}
      >
        <MenuItem
          onClick={() => {
            void handleOpenSoftwareActionDialog("icon_override", softwareActionMenu?.row || null);
          }}
          sx={{
            fontSize: 13,
            color: MAGIC_UI.textBright,
          }}
        >
          Create Global Icon Override
        </MenuItem>
        <MenuItem
          onClick={() => {
            void handleOpenSoftwareActionDialog("uninstall_override", softwareActionMenu?.row || null);
          }}
          sx={{
            fontSize: 13,
            color: MAGIC_UI.textBright,
          }}
        >
          Create Global Uninstall Override
        </MenuItem>
        <MenuItem
          onClick={() => {
            void handleOpenSoftwareActionDialog("uninstall_block", softwareActionMenu?.row || null);
          }}
          sx={{
            fontSize: 13,
            color: MAGIC_UI.textBright,
          }}
        >
          Block Uninstallation
        </MenuItem>
        <MenuItem
          onClick={() => {
            void handleOpenSoftwareActionDialog("uninstall_unblock", softwareActionMenu?.row || null);
          }}
          sx={{
            fontSize: 13,
            color: MAGIC_UI.textBright,
          }}
        >
          Unblock Uninstallation
        </MenuItem>
      </Menu>
      <Dialog
        open={Boolean(softwareActionDialog?.open)}
        onClose={() => {
          if (softwareActionDialog?.submitting) return;
          handleCloseSoftwareActionDialog();
        }}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={softwareActionDialogTitle} subtitle={softwareActionDialogSubtitle} />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {softwareActionDialogKind === "icon_override" ? (
              <>
                <TextField
                  select
                  fullWidth
                  label="Suggested Icon Candidates"
                  value={softwareActionDialog?.selectedCandidate || ""}
                  disabled={Boolean(softwareActionDialog?.clearIcon)}
                  onChange={(event) => {
                    const nextValue = String(event.target.value || "");
                    setSoftwareActionDialog((current) => ({
                      ...current,
                      selectedCandidate: nextValue,
                      manualPath: nextValue || current.manualPath || "",
                    }));
                  }}
                  helperText="Choose a suggested candidate or leave this blank and enter a manual path below."
                  sx={SOFTWARE_DIALOG_TEXT_FIELD_SX}
                >
                  <MenuItem value="">Choose a suggested candidate</MenuItem>
                  {(softwareActionDialog?.candidatePaths || []).map((candidate) => (
                    <MenuItem key={candidate} value={candidate}>
                      {candidate}
                    </MenuItem>
                  ))}
                </TextField>
                <TextField
                  fullWidth
                  label="Manual Icon Resource Path"
                  value={softwareActionDialog?.manualPath || ""}
                  disabled={Boolean(softwareActionDialog?.clearIcon)}
                  onChange={(event) => {
                    const nextValue = String(event.target.value || "");
                    setSoftwareActionDialog((current) => ({ ...current, manualPath: nextValue }));
                  }}
                  helperText="Use a verified EXE, DLL, or ICO path. If you omit ,0, Borealis defaults to icon index 0."
                  sx={SOFTWARE_DIALOG_TEXT_FIELD_SX}
                />
              </>
            ) : null}
            {softwareActionDialogKind === "uninstall_override" ? (
              <>
                {softwareActionDialog?.commandPreview ? (
                  <Box
                    sx={{
                      border: `1px solid ${MAGIC_UI.panelBorder}`,
                      borderRadius: 2,
                      px: 1.5,
                      py: 1.25,
                      background: "rgba(7,12,26,0.78)",
                    }}
                  >
                    <Typography
                      sx={{
                        fontSize: 11,
                        textTransform: "uppercase",
                        letterSpacing: 0.4,
                        color: "#8fbfff",
                        mb: 0.6,
                      }}
                    >
                      Current Command Preview
                    </Typography>
                    <Typography
                      sx={{
                        fontSize: 12,
                        color: MAGIC_UI.textBright,
                        fontFamily:
                          '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
                        wordBreak: "break-word",
                      }}
                    >
                      {softwareActionDialog.commandPreview}
                    </Typography>
                  </Box>
                ) : null}
                <TextField
                  fullWidth
                  label="Application Path"
                  value={softwareActionDialog?.applicationPath || ""}
                  onChange={(event) => {
                    const nextValue = String(event.target.value || "");
                    setSoftwareActionDialog((current) => ({ ...current, applicationPath: nextValue }));
                  }}
                  helperText="Example: C:\\Program Files\\Vendor\\App\\uninstall.exe"
                  sx={SOFTWARE_DIALOG_TEXT_FIELD_SX}
                />
                <TextField
                  fullWidth
                  label="Arguments"
                  value={softwareActionDialog?.arguments || ""}
                  onChange={(event) => {
                    const nextValue = String(event.target.value || "");
                    setSoftwareActionDialog((current) => ({ ...current, arguments: nextValue }));
                  }}
                  helperText="Optional arguments passed to the application path above."
                  sx={SOFTWARE_DIALOG_TEXT_FIELD_SX}
                />
              </>
            ) : null}
            {softwareActionDialogKind === "uninstall_block" ? (
              <TextField
                fullWidth
                multiline
                minRows={4}
                label="Reason"
                value={softwareActionDialog?.reason || ""}
                onChange={(event) => {
                  const nextValue = String(event.target.value || "");
                  setSoftwareActionDialog((current) => ({ ...current, reason: nextValue }));
                }}
                helperText="This reason is shown to other operators when Borealis blocks the uninstall."
                sx={SOFTWARE_DIALOG_TEXT_FIELD_SX}
              />
            ) : null}
            {softwareActionDialogKind === "uninstall_unblock" ? (
              <Box
                sx={{
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  borderRadius: 2,
                  px: 1.6,
                  py: 1.5,
                  background: "rgba(7,12,26,0.78)",
                }}
              >
                <Typography sx={{ fontSize: 13, color: MAGIC_UI.textBright, lineHeight: 1.55 }}>
                  Borealis will remove the matching uninstall block rule for this software if one exists.
                </Typography>
                {String(softwareActionDialogRow?.uninstall?.reason || "").trim() ? (
                  <Typography
                    sx={{
                      mt: 1.15,
                      fontSize: 12,
                      color: "rgba(191,219,254,0.86)",
                      lineHeight: 1.6,
                    }}
                  >
                    Current reason: {String(softwareActionDialogRow?.uninstall?.reason || "").trim()}
                  </Typography>
                ) : null}
              </Box>
            ) : null}
          </Box>
        </DialogContent>
        <DialogActions sx={{ ...DIALOG_ACTIONS_SX, justifyContent: "space-between", gap: 1 }}>
          {softwareActionDialogKind === "icon_override" ? (
            <FormControlLabel
              sx={{
                mr: "auto",
                color: "rgba(191,219,254,0.9)",
                "& .MuiFormControlLabel-label": {
                  fontSize: "0.88rem",
                },
              }}
              control={
                <Checkbox
                  checked={Boolean(softwareActionDialog?.clearIcon)}
                  disabled={Boolean(softwareActionDialog?.submitting)}
                  onChange={(event) => {
                    const checked = Boolean(event.target.checked);
                    setSoftwareActionDialog((current) => ({
                      ...current,
                      clearIcon: checked,
                      ...(checked
                        ? {
                            selectedCandidate: "",
                            manualPath: "",
                          }
                        : {}),
                    }));
                  }}
                  sx={{
                    color: "rgba(148,163,184,0.72)",
                    "&.Mui-checked": {
                      color: "#7dd3fc",
                    },
                  }}
                />
              }
              label="Remove the icon entirely"
            />
          ) : (
            <Box sx={{ mr: "auto" }} />
          )}
          <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
            <Button
              onClick={handleCloseSoftwareActionDialog}
              disabled={Boolean(softwareActionDialog?.submitting)}
              sx={DIALOG_BUTTON_SX}
            >
              Cancel
            </Button>
            <Button
              onClick={() => {
                void submitSoftwareAction();
              }}
              disabled={Boolean(softwareActionDialog?.submitting)}
              sx={DIALOG_BUTTON_SX}
            >
              {softwareActionDialog?.submitting ? "Saving..." : softwareActionDialogConfirmLabel}
            </Button>
          </Box>
        </DialogActions>
      </Dialog>
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
      <UninstallProgressDialog
        open={Boolean(activeTrackedUninstall)}
        job={activeTrackedUninstall}
        onClose={() => setProgressDialogJobId("")}
      />
    </Box>
  );
}
