import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Box, Button, Stack, Typography } from "@mui/material";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import RestartAltRoundedIcon from "@mui/icons-material/RestartAltRounded";
import { AgGridReact } from "ag-grid-react";
import { DEFAULT_GRID_COL_DEF, DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI } from "./Shared.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";

const ACTION_BUTTON_SX = {
  minHeight: 34,
  borderRadius: 999,
  px: 1.6,
  textTransform: "none",
  fontWeight: 650,
  color: MAGIC_UI.textBright,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
  },
};

const PRIMARY_BUTTON_SX = {
  ...ACTION_BUTTON_SX,
  border: "1px solid rgba(125,211,252,0.42)",
  background: "linear-gradient(90deg, rgba(14,165,233,0.78), rgba(147,51,234,0.72))",
};

function ts(value) {
  const parsed = Number(value || 0);
  if (!parsed) return "";
  return new Date(parsed * 1000).toLocaleString();
}

function statusLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "pending_install") return "Pending Install";
  if (!normalized) return "";
  return normalized.replace(/_/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
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
      { field: "title", headerName: "Update", minWidth: 320, flex: 2 },
      { field: "kb_article_ids", headerName: "KBs", valueFormatter: ({ value }) => (value || []).join(", "), minWidth: 150 },
      { field: "classifications", headerName: "Class", valueFormatter: ({ value }) => (value || []).join(", "), minWidth: 180 },
      { field: "status", headerName: "Status", valueFormatter: ({ value }) => statusLabel(value), width: 150 },
      { field: "approved", headerName: "Approved", width: 120 },
      { field: "held", headerName: "Held", width: 100 },
      { field: "reboot_required", headerName: "Reboot", width: 110 },
      { field: "result_code", headerName: "Result", width: 160 },
      { field: "hresult", headerName: "HRESULT", width: 130 },
      { field: "last_seen_at", headerName: "Last Seen", valueFormatter: ({ value }) => ts(value), minWidth: 170 },
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
            <Button startIcon={<RefreshRoundedIcon />} disabled={loading} onClick={() => runAction("scan")} sx={ACTION_BUTTON_SX}>
              Scan
            </Button>
            <Button startIcon={<SystemUpdateAltRoundedIcon />} disabled={loading} onClick={() => runAction("install")} sx={PRIMARY_BUTTON_SX}>
              Install
            </Button>
            <Button startIcon={<RestartAltRoundedIcon />} disabled={loading} onClick={() => runAction("reboot")} sx={ACTION_BUTTON_SX}>
              Reboot
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
