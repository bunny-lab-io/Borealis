import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Box, Button, Stack, Typography } from "@mui/material";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import RestartAltRoundedIcon from "@mui/icons-material/RestartAltRounded";
import { AgGridReact } from "ag-grid-react";
import { DEFAULT_GRID_COL_DEF, DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI } from "./Shared.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";

const SECONDARY_BUTTON_SX = {
  minHeight: 34,
  borderRadius: 999,
  px: 1.6,
  textTransform: "none",
  fontWeight: 650,
  color: MAGIC_UI.textBright,
  whiteSpace: "nowrap",
  border: "1px solid rgba(148,163,184,0.4)",
  background: "rgba(5,10,24,0.64)",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
  },
};

const PRIMARY_BUTTON_SX = {
  ...SECONDARY_BUTTON_SX,
  border: "1px solid rgba(125,211,252,0.42)",
  background: "linear-gradient(90deg, #7dd3fc, #c084fc)",
  color: "#06111f",
  boxShadow: "none",
  "&:hover": {
    background: "linear-gradient(90deg, #bae6fd, #d8b4fe)",
    borderColor: "rgba(186,230,253,0.72)",
    boxShadow: "none",
  },
};

function statusLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "pending_install") return "Pending Install";
  if (!normalized) return "";
  return normalized.replace(/_/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function ResultCell(params) {
  const result = statusLabel(params.value);
  const hresult = String(params.data?.hresult || "").trim();
  if (!result && !hresult) return "";
  return (
    <Box component="span" sx={{ display: "inline-flex", alignItems: "center", minWidth: 0 }}>
      <Box component="span" sx={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {result || "Unknown"}
      </Box>
      {hresult && (
        <Box component="span" sx={{ ml: 0.55, color: "rgba(100,116,139,0.9)", whiteSpace: "nowrap" }}>
          - {hresult}
        </Box>
      )}
    </Box>
  );
}

async function apiJson(path, options = {}) {
  const response = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data?.message || data?.error || `Request failed (${response.status})`);
  }
  return data;
}

export default function DevicePatchManagementTab({ device }) {
  const hostname = device?.hostname || device?.summary?.hostname || "";
  const notifyOperator = useAppNotifications({ title: "Patch Management", icon: "notification" });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [payload, setPayload] = useState({ device: {}, policy: {}, updates: [] });
  const [selectedRows, setSelectedRows] = useState([]);

  const load = useCallback(async () => {
    if (!hostname) return;
    setLoading(true);
    setError("");
    try {
      const data = await apiJson(`/api/device/patches/${encodeURIComponent(hostname)}`);
      setPayload(data);
    } catch (err) {
      setError(err.message || "Patch state failed to load.");
    } finally {
      setLoading(false);
    }
  }, [hostname]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    const socket = window.BorealisSocket;
    if (!socket?.on) return undefined;
    const handler = (event = {}) => {
      if (!event.hostname || String(event.hostname).toLowerCase() === String(hostname).toLowerCase()) {
        load();
      }
    };
    socket.on("patch_management_changed", handler);
    return () => {
      if (socket.off) socket.off("patch_management_changed", handler);
    };
  }, [hostname, load]);

  const columns = useMemo(
    () => [
      { field: "title", headerName: "Update", minWidth: 420, flex: 1.8 },
      { field: "kb_article_ids", headerName: "KB", valueFormatter: ({ value }) => (value || []).join(", "), width: 120, minWidth: 105, flex: 0 },
      { field: "classifications", headerName: "Class", valueFormatter: ({ value }) => (value || []).join(", "), width: 180, minWidth: 150, flex: 0 },
      { field: "status", headerName: "Status", valueFormatter: ({ value }) => statusLabel(value), width: 150, minWidth: 125, flex: 0 },
      { field: "approved", headerName: "Approved", width: 108, minWidth: 96, flex: 0, cellStyle: { textAlign: "center" } },
      { field: "held", headerName: "Blocked", width: 90, minWidth: 84, flex: 0, cellStyle: { textAlign: "center" } },
      { field: "reboot_required", headerName: "Pending Reboot", width: 140, minWidth: 132, flex: 0, cellStyle: { textAlign: "center" } },
      { field: "result_code", headerName: "Result", width: 170, minWidth: 150, flex: 0, cellRenderer: ResultCell },
    ],
    []
  );

  const runAction = useCallback(
    async (action) => {
      try {
        await apiJson(`/api/device/patches/${encodeURIComponent(hostname)}/action`, {
          method: "POST",
          body: JSON.stringify({
            action,
            update_ids: selectedRows.map((row) => row.update_id).filter(Boolean),
          }),
        });
        notifyOperator({ message: `Patch ${action.replace("_", " ")} dispatched.`, variant: "success" });
        await load();
      } catch (err) {
        notifyOperator({ message: err.message || "Patch action failed.", variant: "error" });
      }
    },
    [hostname, load, notifyOperator, selectedRows]
  );

  const missingCount = (payload.updates || []).filter((row) => ["missing", "pending_install"].includes(row.status)).length;
  const rebootCount = (payload.updates || []).filter((row) => row.reboot_required).length;
  const policy = payload.policy || {};

  return (
    <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
      <Stack spacing={1.4} sx={{ flexGrow: 1, minHeight: 0 }}>
        {error && <Alert severity="error">{error}</Alert>}
        <Stack direction={{ xs: "column", md: "row" }} alignItems={{ xs: "stretch", md: "center" }} justifyContent="space-between" spacing={1}>
          <Stack direction="row" spacing={1.2} flexWrap="wrap">
            <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
              Policy: {policy.policy_name || "Unknown"} ({policy.effective_reason || "unknown"})
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted }}>Missing: {missingCount}</Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted }}>Pending Reboot: {rebootCount}</Typography>
          </Stack>
          <Stack direction="row" spacing={1} flexWrap="wrap">
            <Button startIcon={<RefreshRoundedIcon />} disabled={loading} onClick={() => runAction("scan")} sx={SECONDARY_BUTTON_SX}>
              Scan for Updates
            </Button>
            <Button startIcon={<RestartAltRoundedIcon />} disabled={loading} onClick={() => runAction("reboot")} sx={SECONDARY_BUTTON_SX}>
              Reboot Now
            </Button>
            <Button startIcon={<SystemUpdateAltRoundedIcon />} disabled={loading} onClick={() => runAction("install")} sx={PRIMARY_BUTTON_SX}>
              Install Updates
            </Button>
          </Stack>
        </Stack>
        <GridShell sx={{ minHeight: 520, flexGrow: 1 }}>
          <AgGridReact
            theme={DEVICE_DETAILS_GRID_THEME}
            rowData={payload.updates || []}
            columnDefs={columns}
            defaultColDef={DEFAULT_GRID_COL_DEF}
            rowSelection="multiple"
            onSelectionChanged={(event) => setSelectedRows(event.api.getSelectedRows() || [])}
            pagination
            paginationPageSize={50}
          />
        </GridShell>
      </Stack>
    </Box>
  );
}
