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
  CircularProgress
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import EditIcon from "@mui/icons-material/Edit";
import DeleteIcon from "@mui/icons-material/Delete";
import RefreshIcon from "@mui/icons-material/Refresh";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";
import AddDevice from "./Add_Device.jsx";

const tableStyles = {
  "& th, & td": {
    color: "#ddd",
    borderColor: "#2a2a2a",
    fontSize: 13,
    py: 0.75
  },
  "& th": {
    fontWeight: 600
  },
  "& th .MuiTableSortLabel-root": { color: "#ddd" },
  "& th .MuiTableSortLabel-root.Mui-active": { color: "#ddd" }
};

const defaultForm = {
  hostname: "",
  address: "",
  description: "",
  operating_system: ""
};

export default function SSHDevices({ type = "ssh" }) {
  const typeLabel = type === "winrm" ? "WinRM" : "SSH";
  const apiBase = type === "winrm" ? "/api/winrm_devices" : "/api/ssh_devices";
  const pageTitle = `${typeLabel} Devices`;
  const addButtonLabel = `Add ${typeLabel} Device`;
  const addressLabel = `${typeLabel} Address`;
  const loadingLabel = `Loading ${typeLabel} devices…`;
  const emptyLabel = `No ${typeLabel} devices have been added yet.`;
  const descriptionText = type === "winrm"
    ? "Manage remote endpoints reachable via WinRM for playbook execution."
    : "Manage remote endpoints reachable via SSH for playbook execution.";
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

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "transparent" }} elevation={2}>
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          p: 2,
          borderBottom: "1px solid #2a2a2a"
        }}
      >
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            {pageTitle}
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            {descriptionText}
          </Typography>
        </Box>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          <Button
            size="small"
            variant="outlined"
            startIcon={<RefreshIcon />}
            sx={{ color: "#58a6ff", borderColor: "#58a6ff" }}
            onClick={loadDevices}
            disabled={loading}
          >
            Refresh
          </Button>
          <Button
            size="small"
            variant="contained"
            startIcon={<AddIcon />}
            sx={{ bgcolor: "#58a6ff", color: "#0b0f19" }}
            onClick={openCreate}
          >
            {addButtonLabel}
          </Button>
        </Box>
      </Box>

      {error && (
        <Box sx={{ px: 2, py: 1.5, color: "#ff8080" }}>
          <Typography variant="body2">{error}</Typography>
        </Box>
      )}
      {loading && (
        <Box sx={{ px: 2, py: 1.5, display: "flex", alignItems: "center", gap: 1, color: "#7db7ff" }}>
          <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
          <Typography variant="body2">{loadingLabel}</Typography>
        </Box>
      )}

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
              : (row.summary?.created || "");
            return (
              <TableRow key={row.hostname}>
                <TableCell>{row.hostname}</TableCell>
                <TableCell>{row.connection_endpoint || ""}</TableCell>
                <TableCell>{row.description || ""}</TableCell>
                <TableCell>{createdDisplay}</TableCell>
                <TableCell align="right">
                  <IconButton size="small" sx={{ color: "#7db7ff" }} onClick={() => openEdit(row)}>
                    <EditIcon fontSize="small" />
                  </IconButton>
                  <IconButton size="small" sx={{ color: "#ff8080" }} onClick={() => setDeleteTarget(row)}>
                    <DeleteIcon fontSize="small" />
                  </IconButton>
                </TableCell>
              </TableRow>
            );
          })}
          {!sortedRows.length && !loading && (
            <TableRow>
              <TableCell colSpan={5} sx={{ textAlign: "center", color: "#888" }}>
                {emptyLabel}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      <Dialog
        open={dialogOpen}
        onClose={handleDialogClose}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>{isEdit ? editDialogTitle : newDialogTitle}</DialogTitle>
        <DialogContent sx={{ display: "flex", flexDirection: "column", gap: 2, mt: 1 }}>
          <TextField
            label="Hostname"
            value={form.hostname}
            disabled={isEdit}
            onChange={(e) => setForm((prev) => ({ ...prev, hostname: e.target.value }))}
            fullWidth
            size="small"
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#1f1f1f",
                color: "#fff",
                "& fieldset": { borderColor: "#555" },
                "&:hover fieldset": { borderColor: "#888" }
              },
              "& .MuiInputLabel-root": { color: "#aaa" }
            }}
            helperText="Hostname used within Borealis (unique)."
          />
          <TextField
            label={addressLabel}
            value={form.address}
            onChange={(e) => setForm((prev) => ({ ...prev, address: e.target.value }))}
            fullWidth
            size="small"
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#1f1f1f",
                color: "#fff",
                "& fieldset": { borderColor: "#555" },
                "&:hover fieldset": { borderColor: "#888" }
              },
              "& .MuiInputLabel-root": { color: "#aaa" }
            }}
            helperText={`IP or FQDN Borealis can reach over ${typeLabel}.`}
          />
          <TextField
            label="Description"
            value={form.description}
            onChange={(e) => setForm((prev) => ({ ...prev, description: e.target.value }))}
            fullWidth
            size="small"
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#1f1f1f",
                color: "#fff",
                "& fieldset": { borderColor: "#555" },
                "&:hover fieldset": { borderColor: "#888" }
              },
              "& .MuiInputLabel-root": { color: "#aaa" }
            }}
          />
          <TextField
            label="Operating System"
            value={form.operating_system}
            onChange={(e) => setForm((prev) => ({ ...prev, operating_system: e.target.value }))}
            fullWidth
            size="small"
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#1f1f1f",
                color: "#fff",
                "& fieldset": { borderColor: "#555" },
                "&:hover fieldset": { borderColor: "#888" }
              },
              "& .MuiInputLabel-root": { color: "#aaa" }
            }}
          />
          {error && (
            <Typography variant="body2" sx={{ color: "#ff8080" }}>
              {error}
            </Typography>
          )}
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button onClick={handleDialogClose} sx={{ color: "#58a6ff" }} disabled={submitting}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            variant="outlined"
            sx={{ color: "#58a6ff", borderColor: "#58a6ff" }}
            disabled={submitting}
          >
            {submitting ? "Saving..." : "Save"}
          </Button>
        </DialogActions>
      </Dialog>

      <ConfirmDeleteDialog
        open={Boolean(deleteTarget)}
        message={
          deleteTarget
            ? `Remove ${typeLabel} device '${deleteTarget.hostname}' from inventory?`
            : ""
        }
        onCancel={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        confirmDisabled={deleteBusy}
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
