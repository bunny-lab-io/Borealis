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

const SOFTWARE_FILTER_OPTIONS = [
  { key: "locally_installed", label: "Locally Installed" },
  { key: "windows_store", label: "Windows Store" },
  { key: "snap_package", label: "Snap Package" },
];

function buildSoftwareActionKey(row = {}) {
  return [
    String(row?.name || "").trim().toLowerCase(),
    String(row?.version || "").trim().toLowerCase(),
    String(row?.source || "").trim().toLowerCase(),
  ].join("::");
}

function getUninstallCapability(row = {}, hostname = "") {
  if (!hostname) {
    return { supported: false, reason: "The selected device is missing a hostname." };
  }
  const uninstall = row?.uninstall && typeof row.uninstall === "object" ? row.uninstall : null;
  if (uninstall) {
    return {
      supported: Boolean(uninstall.supported),
      reason: String(uninstall.reason || "").trim(),
      summary: String(uninstall.summary || "").trim(),
    };
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
