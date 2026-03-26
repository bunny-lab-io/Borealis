import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Typography } from "@mui/material";
import { Add as AddIcon, DeleteOutline as DeleteOutlineIcon } from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 12,
});

const themeClassName = gridTheme.themeName || "ag-theme-quartz";

function formatTs(value) {
  if (!value) return "—";
  try {
    return new Date(Number(value) * 1000).toLocaleString();
  } catch {
    return "—";
  }
}

export default function WorkflowWebhookDialog({
  open,
  onClose,
  workflowGuid,
  workflowName,
}) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [selectedId, setSelectedId] = useState(null);
  const gridRef = useRef(null);

  const canManage = Boolean(String(workflowGuid || "").trim());

  const loadWebhooks = useCallback(async () => {
    if (!canManage) {
      setRows([]);
      setError("");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const resp = await fetch(`/api/workflows/${encodeURIComponent(String(workflowGuid).trim())}/webhooks`);
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      const list = Array.isArray(data?.webhooks) ? data.webhooks : [];
      setRows(
        list.map((entry) => ({
          id: entry.id,
          webhookUrl: entry.webhook_url || "",
          created: formatTs(entry.created || entry.created_at),
          creator: entry.creator || entry.creator_username || "Unknown",
        }))
      );
    } catch (err) {
      setRows([]);
      setError(String(err?.message || err || "Failed to load workflow webhooks"));
    } finally {
      setLoading(false);
    }
  }, [canManage, workflowGuid]);

  useEffect(() => {
    if (open) {
      loadWebhooks();
    }
  }, [open, loadWebhooks]);

  const createWebhook = useCallback(async () => {
    if (!canManage) return;
    setBusy(true);
    setError("");
    try {
      const resp = await fetch(`/api/workflows/${encodeURIComponent(String(workflowGuid).trim())}/webhooks`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      await loadWebhooks();
    } catch (err) {
      setError(String(err?.message || err || "Failed to create webhook"));
    } finally {
      setBusy(false);
    }
  }, [canManage, loadWebhooks, workflowGuid]);

  const deleteWebhook = useCallback(async () => {
    if (!canManage || !selectedId) return;
    setBusy(true);
    setError("");
    try {
      const resp = await fetch(
        `/api/workflows/${encodeURIComponent(String(workflowGuid).trim())}/webhooks/${encodeURIComponent(String(selectedId))}`,
        { method: "DELETE" }
      );
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      setSelectedId(null);
      await loadWebhooks();
    } catch (err) {
      setError(String(err?.message || err || "Failed to delete webhook"));
    } finally {
      setBusy(false);
    }
  }, [canManage, loadWebhooks, selectedId, workflowGuid]);

  const columnDefs = useMemo(
    () => [
      {
        field: "webhookUrl",
        headerName: "Webhook URL",
        flex: 1.8,
        minWidth: 320,
        cellStyle: { color: "#7dd3fc", fontWeight: 600 },
      },
      { field: "created", headerName: "Created", flex: 1, minWidth: 180 },
      { field: "creator", headerName: "Creator", flex: 1, minWidth: 160 },
    ],
    []
  );

  return (
    <Dialog
      open={open}
      onClose={busy ? undefined : onClose}
      fullWidth
      maxWidth="lg"
      PaperProps={{
        sx: {
          ...DIALOG_PAPER_SX,
          minHeight: 460,
        },
      }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock
          title="Webhook Management"
          subtitle={
            canManage
              ? `Define webhooks to trigger ${workflowName || "this workflow"}.`
              : "Save this workflow first so Borealis can create opaque webhook URLs for it."
          }
        />
      </DialogTitle>
      <DialogContent sx={DIALOG_CONTENT_SX}>
        {error ? (
          <Box
            sx={{
              mb: 1.5,
              px: 1.5,
              py: 1,
              borderRadius: "14px",
              border: "1px solid rgba(248,113,113,0.34)",
              background: "rgba(127,29,29,0.18)",
            }}
          >
            <Typography sx={{ color: "#fecaca", fontSize: "0.82rem" }}>{error}</Typography>
          </Box>
        ) : null}
        <Box
          className={themeClassName}
          sx={{
            height: 320,
            borderRadius: "18px",
            overflow: "hidden",
            border: "1px solid rgba(148,163,184,0.18)",
            background:
              "radial-gradient(120% 120% at 0% 0%, rgba(125,211,252,0.08), transparent 55%), " +
              "rgba(6,10,20,0.92)",
            "& .ag-root-wrapper": {
              border: "none",
              background: "transparent",
            },
            "& .ag-header": {
              background: "rgba(15,23,42,0.92)",
            },
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
            ref={gridRef}
            rowData={rows}
            columnDefs={columnDefs}
            rowSelection="single"
            suppressCellFocus
            animateRows
            loading={loading}
            onSelectionChanged={() => {
              const selected = gridRef.current?.api?.getSelectedRows?.() || [];
              setSelectedId(selected[0]?.id || null);
            }}
          />
        </Box>
      </DialogContent>
      <DialogActions sx={{ ...DIALOG_ACTIONS_SX, justifyContent: "space-between" }}>
        <Box sx={{ display: "flex", gap: 1 }}>
          <Button
            startIcon={<AddIcon />}
            sx={DIALOG_PRIMARY_BUTTON_SX}
            disabled={!canManage || busy}
            onClick={createWebhook}
          >
            Create Webhook
          </Button>
          <Button
            startIcon={<DeleteOutlineIcon />}
            sx={DIALOG_DANGER_BUTTON_SX}
            disabled={!canManage || !selectedId || busy}
            onClick={deleteWebhook}
          >
            Delete Selected
          </Button>
        </Box>
        <Button sx={DIALOG_BUTTON_SX} onClick={onClose} disabled={busy}>
          Close
        </Button>
      </DialogActions>
    </Dialog>
  );
}
