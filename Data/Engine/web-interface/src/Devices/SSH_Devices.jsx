import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  IconButton,
  Table,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
  TableSortLabel,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  TextField,
  CircularProgress,
  Stack,
  Tooltip,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import EditIcon from "@mui/icons-material/Edit";
import DeleteIcon from "@mui/icons-material/Delete";
import RefreshIcon from "@mui/icons-material/Refresh";
import LanIcon from "@mui/icons-material/Lan";
import DesktopWindowsIcon from "@mui/icons-material/DesktopWindows";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import AddDevice from "./Add_Device.jsx";

const MAGIC_UI = {
  panelBg: "linear-gradient(160deg, rgba(7,11,24,0.92), rgba(5,9,20,0.94))",
  panelBorder: "rgba(148,163,184,0.32)",
  glass: "rgba(12,18,35,0.8)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentSuccess: "#34d399",
  accentWarn: "#f97316",
  glow: "0 24px 60px rgba(2,6,23,0.7)",
};

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  fontWeight: 700,
  boxShadow: "0 12px 32px rgba(124,58,237,0.32)",
  px: 2.2,
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 16px 40px rgba(124,58,237,0.42)",
  },
};

const tableStyles = {
  minWidth: "100%",
  "& th, & td": {
    color: MAGIC_UI.textBright,
    borderColor: "rgba(148,163,184,0.18)",
    fontSize: 13,
    py: 1,
    px: 1.5,
    backgroundColor: "transparent",
  },
  "& th": {
    fontWeight: 700,
    backgroundColor: "rgba(5,8,18,0.85)",
    letterSpacing: 0.2,
  },
  "& tbody tr": {
    "&:nth-of-type(odd)": {
      backgroundColor: "rgba(255,255,255,0.02)",
    },
    "&:hover": {
      backgroundColor: "rgba(125,183,255,0.08)",
    },
  },
  "& th .MuiTableSortLabel-root": { color: MAGIC_UI.textBright },
  "& th .MuiTableSortLabel-root.Mui-active": { color: MAGIC_UI.textBright },
};

const TYPE_META = {
  ssh: {
    label: "SSH Devices",
    description: "Manage remote endpoints reachable via SSH for playbook execution.",
    icon: LanIcon,
  },
  winrm: {
    label: "WinRM Devices",
    description: "Manage remote endpoints reachable via WinRM for playbook execution.",
    icon: DesktopWindowsIcon,
  },
};

const defaultForm = {
  hostname: "",
  address: "",
  description: "",
  operating_system: ""
};

export default function SSHDevices({ type = "ssh", onPageMetaChange, onQuickJobLaunch }) {
  const typeLabel = type === "winrm" ? "WinRM" : "SSH";
  const meta = TYPE_META[type] || TYPE_META.ssh;
  const apiBase = type === "winrm" ? "/api/winrm_devices" : "/api/ssh_devices";
  const pageTitle = meta.label;
  const pageSubtitle = meta.description;
  const addButtonLabel = `Add ${typeLabel} Device`;
  const addressLabel = `${typeLabel} Address`;
  const loadingLabel = `Loading ${typeLabel} devices…`;
  const emptyLabel = `No ${typeLabel} devices have been added yet.`;
  const editDialogTitle = `Edit ${typeLabel} Device`;
  const newDialogTitle = `New ${typeLabel} Device`;
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("hostname");
  const [order, setOrder] = useState("asc");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [form, setForm] = useState(defaultForm);
  const [formError, setFormError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [editTarget, setEditTarget] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [addDeviceOpen, setAddDeviceOpen] = useState(false);

  const isEdit = Boolean(editTarget);

  const loadDevices = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch(apiBase);
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      const list = Array.isArray(data?.devices) ? data.devices : [];
      setRows(list);
    } catch (err) {
      setError(String(err.message || err));
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, [apiBase]);

  useEffect(() => {
    loadDevices();
  }, [loadDevices]);

  useEffect(() => {
    const IconComponent = meta.icon || LanIcon;
    onPageMetaChange?.({
      page_title: pageTitle,
      page_subtitle: pageSubtitle,
      page_icon: IconComponent,
    });
    return () => onPageMetaChange?.(null);
  }, [meta.icon, onPageMetaChange, pageSubtitle, pageTitle]);

  const sortedRows = useMemo(() => {
    const list = [...rows];
    list.sort((a, b) => {
      const getKey = (row) => {
        switch (orderBy) {
          case "created_at":
            return Number(row.created_at || 0);
          case "address":
            return (row.connection_endpoint || "").toLowerCase();
          case "description":
            return (row.description || "").toLowerCase();
          default:
            return (row.hostname || "").toLowerCase();
        }
      };
      const aKey = getKey(a);
      const bKey = getKey(b);
      if (aKey < bKey) return order === "asc" ? -1 : 1;
      if (aKey > bKey) return order === "asc" ? 1 : -1;
      return 0;
    });
    return list;
  }, [rows, order, orderBy]);

  const handleSort = (column) => () => {
    if (orderBy === column) {
      setOrder((prev) => (prev === "asc" ? "desc" : "asc"));
    } else {
      setOrderBy(column);
      setOrder("asc");
    }
  };

  const openCreate = () => {
    setAddDeviceOpen(true);
    setFormError("");
  };

  const openEdit = (row) => {
    setEditTarget(row);
    setForm({
      hostname: row.hostname || "",
      address: row.connection_endpoint || "",
      description: row.description || "",
      operating_system: row.summary?.operating_system || ""
    });
    setDialogOpen(true);
    setFormError("");
  };

  const handleDialogClose = () => {
    if (submitting) return;
    setDialogOpen(false);
    setForm(defaultForm);
    setEditTarget(null);
    setFormError("");
  };

  const handleSubmit = async () => {
    if (submitting) return;
    const payload = {
      hostname: form.hostname.trim(),
      address: form.address.trim(),
      description: form.description.trim(),
      operating_system: form.operating_system.trim()
    };
    if (!payload.hostname) {
      setFormError("Hostname is required.");
      return;
    }
    if (!payload.address) {
      setFormError("Address is required.");
      return;
    }
    setSubmitting(true);
    setFormError("");
    try {
      const endpoint = isEdit
        ? `${apiBase}/${encodeURIComponent(editTarget.hostname)}`
        : apiBase;
      const resp = await fetch(endpoint, {
        method: isEdit ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      setDialogOpen(false);
      setForm(defaultForm);
      setEditTarget(null);
      setFormError("");
      setRows((prev) => {
        const next = [...prev];
        if (data?.device) {
          const idx = next.findIndex((row) => row.hostname === data.device.hostname);
          if (idx >= 0) next[idx] = data.device;
          else next.push(data.device);
          return next;
        }
        return prev;
      });
      // Ensure latest ordering by triggering refresh
      loadDevices();
    } catch (err) {
      setFormError(String(err.message || err));
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteBusy(true);
    try {
      const resp = await fetch(`${apiBase}/${encodeURIComponent(deleteTarget.hostname)}`, {
        method: "DELETE"
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      setRows((prev) => prev.filter((row) => row.hostname !== deleteTarget.hostname));
      setDeleteTarget(null);
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setDeleteBusy(false);
    }
  };

  const accentColor = type === "winrm" ? MAGIC_UI.accentB : MAGIC_UI.accentA;
  const HeaderIconComponent = meta.icon || LanIcon;

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        background: "transparent",
        border: "none",
        boxShadow: "none",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
      }}
      elevation={0}
    >
      <Box
        sx={{
          px: 3,
          pt: 3,
          pb: 1,
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 2,
        }}
      >
        <Box sx={{ flexGrow: 1 }} />
        <Stack direction="row" spacing={1} alignItems="center">
          <Tooltip title="Refresh list">
            <span>
              <IconButton
                size="small"
                onClick={loadDevices}
                disabled={loading}
                sx={{
                  color: MAGIC_UI.textBright,
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  borderRadius: 2,
                  background: "rgba(12,18,35,0.65)",
                  "&:hover": { borderColor: MAGIC_UI.accentA },
                }}
              >
                <RefreshIcon fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
          <Button
            size="small"
            variant="contained"
            startIcon={<AddIcon />}
            sx={gradientButtonSx}
            onClick={openCreate}
          >
            {addButtonLabel}
          </Button>
        </Stack>
      </Box>

      {error && (
        <Box sx={{ px: 3, pb: 1 }}>
          <Box
            sx={{
              px: 2,
              py: 1.5,
              borderRadius: 2,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(255,124,124,0.08)",
              color: "#ffb4b4",
            }}
          >
            <Typography variant="body2">{error}</Typography>
          </Box>
        </Box>
      )}
      {loading && (
        <Box sx={{ px: 3, pb: 1 }}>
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 1,
              px: 2,
              py: 1.25,
              borderRadius: 2,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(12,18,35,0.7)",
              color: MAGIC_UI.textBright,
            }}
          >
            <CircularProgress size={18} sx={{ color: accentColor }} />
            <Typography variant="body2">{loadingLabel}</Typography>
          </Box>
        </Box>
      )}

      <Box sx={{ px: 3, pb: 3, flexGrow: 1, minHeight: 0 }}>
        <Box
          sx={{
            borderRadius: 3,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: MAGIC_UI.panelBg,
            boxShadow: MAGIC_UI.glow,
            overflow: "hidden",
          }}
        >
          <Table size="small" sx={tableStyles}>
            <TableHead>
              <TableRow>
                <TableCell sortDirection={orderBy === "hostname" ? order : false}>
                  <TableSortLabel
                    active={orderBy === "hostname"}
                    direction={orderBy === "hostname" ? order : "asc"}
                    onClick={handleSort("hostname")}
                  >
                    Hostname
                  </TableSortLabel>
                </TableCell>
                <TableCell sortDirection={orderBy === "address" ? order : false}>
                  <TableSortLabel
                    active={orderBy === "address"}
                    direction={orderBy === "address" ? order : "asc"}
                    onClick={handleSort("address")}
                  >
                    {addressLabel}
                  </TableSortLabel>
                </TableCell>
                <TableCell sortDirection={orderBy === "description" ? order : false}>
                  <TableSortLabel
                    active={orderBy === "description"}
                    direction={orderBy === "description" ? order : "asc"}
                    onClick={handleSort("description")}
                  >
                    Description
                  </TableSortLabel>
                </TableCell>
                <TableCell sortDirection={orderBy === "created_at" ? order : false}>
                  <TableSortLabel
                    active={orderBy === "created_at"}
                    direction={orderBy === "created_at" ? order : "asc"}
                    onClick={handleSort("created_at")}
                  >
                    Added
                  </TableSortLabel>
                </TableCell>
                <TableCell align="right">Actions</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {sortedRows.map((row) => {
                const createdTs = Number(row.created_at || 0) * 1000;
                const createdDisplay = createdTs
                  ? new Date(createdTs).toLocaleString()
                  : row.summary?.created || "";
                return (
                  <TableRow key={row.hostname}>
                    <TableCell>{row.hostname}</TableCell>
                    <TableCell>{row.connection_endpoint || ""}</TableCell>
                    <TableCell>{row.description || ""}</TableCell>
                    <TableCell>{createdDisplay}</TableCell>
                    <TableCell align="right">
                      <IconButton
                        size="small"
                        sx={{ color: MAGIC_UI.accentA }}
                        onClick={() => openEdit(row)}
                      >
                        <EditIcon fontSize="small" />
                      </IconButton>
                      <IconButton
                        size="small"
                        sx={{ color: "#ff8a8a" }}
                        onClick={() => setDeleteTarget(row)}
                      >
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </TableCell>
                  </TableRow>
                );
              })}
              {!sortedRows.length && !loading && (
                <TableRow>
                  <TableCell colSpan={5} sx={{ textAlign: "center", color: MAGIC_UI.textMuted }}>
                    {emptyLabel}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Box>
      </Box>

      <Dialog
        open={dialogOpen}
        onClose={handleDialogClose}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={isEdit ? editDialogTitle : newDialogTitle}
            subtitle={`Configure the ${typeLabel} endpoint Borealis should track.`}
          />
        </DialogTitle>
        <DialogContent sx={{ ...DIALOG_CONTENT_SX, display: "flex", flexDirection: "column" }}>
          <TextField
            label="Hostname"
            value={form.hostname}
            disabled={isEdit}
            onChange={(e) => setForm((prev) => ({ ...prev, hostname: e.target.value }))}
            fullWidth
            sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
            helperText="Hostname used within Borealis (unique)."
          />
          <TextField
            label={addressLabel}
            value={form.address}
            onChange={(e) => setForm((prev) => ({ ...prev, address: e.target.value }))}
            fullWidth
            sx={{
              ...DIALOG_INPUT_SX,
              mt: 2.1,
              "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
                ...DIALOG_INPUT_SX["& .MuiOutlinedInput-root, & .MuiInputBase-root"],
                "&:hover fieldset": { borderColor: accentColor },
                "&.Mui-focused fieldset": { borderColor: accentColor },
              },
            }}
            helperText={`IP or FQDN Borealis can reach over ${typeLabel}.`}
          />
          <TextField
            label="Description"
            value={form.description}
            onChange={(e) => setForm((prev) => ({ ...prev, description: e.target.value }))}
            fullWidth
            sx={{
              ...DIALOG_INPUT_SX,
              mt: 2.1,
              "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
                ...DIALOG_INPUT_SX["& .MuiOutlinedInput-root, & .MuiInputBase-root"],
                "&:hover fieldset": { borderColor: accentColor },
                "&.Mui-focused fieldset": { borderColor: accentColor },
              },
            }}
          />
          <TextField
            label="Operating System"
            value={form.operating_system}
            onChange={(e) => setForm((prev) => ({ ...prev, operating_system: e.target.value }))}
            fullWidth
            sx={{
              ...DIALOG_INPUT_SX,
              mt: 2.1,
              "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
                ...DIALOG_INPUT_SX["& .MuiOutlinedInput-root, & .MuiInputBase-root"],
                "&:hover fieldset": { borderColor: accentColor },
                "&.Mui-focused fieldset": { borderColor: accentColor },
              },
            }}
          />
          {error && (
            <Typography variant="body2" sx={{ color: "#ffb4b4", mt: 1.5 }}>
              {error}
            </Typography>
          )}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={handleDialogClose}
            sx={DIALOG_BUTTON_SX}
            disabled={submitting}
          >
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            sx={DIALOG_PRIMARY_BUTTON_SX}
            disabled={submitting}
          >
            {submitting ? "Saving..." : "Save"}
          </Button>
        </DialogActions>
      </Dialog>

      <ConfirmDeleteDialog
        open={Boolean(deleteTarget)}
        title={`Delete ${typeLabel} Device`}
        message={
          deleteTarget
            ? `Remove ${typeLabel} device '${deleteTarget.hostname}' from inventory?`
            : ""
        }
        onCancel={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        confirmDisabled={deleteBusy}
        confirmLabel={`Delete ${typeLabel} Device`}
      />
      <AddDevice
        open={addDeviceOpen}
        defaultType={type}
        onClose={() => setAddDeviceOpen(false)}
        onCreated={() => {
          setAddDeviceOpen(false);
          loadDevices();
        }}
      />
    </Paper>
  );
}
