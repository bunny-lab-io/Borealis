import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Box, Button, TextField, Typography } from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import { ConfirmDeleteDialog } from "../../Dialogs.jsx";
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
  minWidth: 16,
  color: "#8fbfff",
  fontSize: "0.9rem",
  filter: "drop-shadow(0 0 8px rgba(59,130,246,0.2))",
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

function SoftwareNameCell({ row = {} }) {
  const distribution = getSoftwareDistribution(row);
  const isSteam = distribution.platform === "steam";
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 1,
        minWidth: 0,
      }}
    >
      {isSteam ? (
        <Box
          component="span"
          sx={SOFTWARE_DISTRIBUTION_ICON_SX}
          title={distribution.appId ? `Steam-managed title (AppID ${distribution.appId})` : "Steam-managed title"}
        >
          <i className="fa-brands fa-steam" aria-hidden="true" />
        </Box>
      ) : null}
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
  const [softwareSearch, setSoftwareSearch] = useState("");
  const [busyActionKey, setBusyActionKey] = useState("");
  const [confirmRow, setConfirmRow] = useState(null);
  const [softwareFilterMode, setSoftwareFilterMode] = useState("locally_installed");
  const notifyOperator = useAppNotifications();

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
      setConfirmRow(null);
      await notifyOperator({
        title: "Software Uninstall Queued",
        message: `Queued a silent uninstall for <b>${String(row?.name || "Software")}</b> on <b>${hostname}</b>. Check Activity History for output.`,
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
  }, [busyActionKey, confirmRow, hostname, notifyOperator]);

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
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1.5,
        }}
      >
        <TextField
          size="small"
          placeholder="Search software..."
          value={softwareSearch}
          onChange={(event) => setSoftwareSearch(event.target.value)}
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
          quickFilterText={softwareSearch}
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
    </Box>
  );
}
