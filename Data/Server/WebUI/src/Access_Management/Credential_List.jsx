import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Box,
  Button,
  IconButton,
  Menu,
  MenuItem,
  Paper,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel,
  Typography,
  CircularProgress
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import AddIcon from "@mui/icons-material/Add";
import RefreshIcon from "@mui/icons-material/Refresh";
import LockIcon from "@mui/icons-material/Lock";
import WifiIcon from "@mui/icons-material/Wifi";
import ComputerIcon from "@mui/icons-material/Computer";
import CredentialEditor from "./Credential_Editor.jsx";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";

const tablePaperSx = { m: 2, p: 0, bgcolor: "#1e1e1e", borderRadius: 2 };
const tableSx = {
  minWidth: 840,
  "& th, & td": {
    color: "#ddd",
    borderColor: "#2a2a2a",
    fontSize: 13,
    py: 0.9
  },
  "& th .MuiTableSortLabel-root": { color: "#ddd" },
  "& th .MuiTableSortLabel-root.Mui-active": { color: "#ddd" }
};

const columns = [
  { id: "name", label: "Name" },
  { id: "credential_type", label: "Credential Type" },
  { id: "connection_type", label: "Connection" },
  { id: "site_name", label: "Site" },
  { id: "username", label: "Username" },
  { id: "updated_at", label: "Updated" },
  { id: "actions", label: "" }
];

function formatTs(ts) {
  if (!ts) return "-";
  const date = new Date(Number(ts) * 1000);
  if (Number.isNaN(date?.getTime())) return "-";
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function titleCase(value) {
  if (!value) return "-";
  const lower = String(value).toLowerCase();
  return lower.replace(/(^|\s)\w/g, (c) => c.toUpperCase());
}

function connectionIcon(connection) {
  const val = (connection || "").toLowerCase();
  if (val === "ssh") return <LockIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
  if (val === "winrm") return <WifiIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
  return <ComputerIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
}

export default function CredentialList({ isAdmin = false }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("name");
  const [order, setOrder] = useState("asc");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuRow, setMenuRow] = useState(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorMode, setEditorMode] = useState("create");
  const [editingCredential, setEditingCredential] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const sortedRows = useMemo(() => {
    const sorted = [...rows];
    sorted.sort((a, b) => {
      const aVal = (a?.[orderBy] ?? "").toString().toLowerCase();
      const bVal = (b?.[orderBy] ?? "").toString().toLowerCase();
      if (aVal < bVal) return order === "asc" ? -1 : 1;
      if (aVal > bVal) return order === "asc" ? 1 : -1;
      return 0;
    });
    return sorted;
  }, [rows, order, orderBy]);

  const fetchCredentials = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch("/api/credentials");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const list = Array.isArray(data?.credentials) ? data.credentials : [];
      list.sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || "")));
      setRows(list);
    } catch (err) {
      setRows([]);
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchCredentials();
  }, [fetchCredentials]);

  const handleSort = (columnId) => () => {
    if (orderBy === columnId) {
      setOrder((prev) => (prev === "asc" ? "desc" : "asc"));
    } else {
      setOrderBy(columnId);
      setOrder("asc");
    }
  };

  const openMenu = (event, row) => {
    setMenuAnchor(event.currentTarget);
    setMenuRow(row);
  };

  const closeMenu = () => {
    setMenuAnchor(null);
    setMenuRow(null);
  };

  const handleCreate = () => {
    setEditorMode("create");
    setEditingCredential(null);
    setEditorOpen(true);
  };

  const handleEdit = (row) => {
    closeMenu();
    setEditorMode("edit");
    setEditingCredential(row);
    setEditorOpen(true);
  };

  const handleDelete = (row) => {
    closeMenu();
    setDeleteTarget(row);
  };

  const doDelete = async () => {
    if (!deleteTarget?.id) return;
    setDeleteBusy(true);
    try {
      const resp = await fetch(`/api/credentials/${deleteTarget.id}`, { method: "DELETE" });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      setDeleteTarget(null);
      await fetchCredentials();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setDeleteBusy(false);
    }
  };

  const handleEditorSaved = async () => {
    setEditorOpen(false);
    setEditingCredential(null);
    await fetchCredentials();
  };

  if (!isAdmin) {
    return (
      <Paper sx={{ m: 2, p: 3, bgcolor: "#1e1e1e" }}>
        <Typography variant="h6" sx={{ color: "#ff8080" }}>
          Access denied
        </Typography>
        <Typography variant="body2" sx={{ color: "#bbb" }}>
          You do not have permission to manage credentials.
        </Typography>
      </Paper>
    );
  }

  return (
    <>
      <Paper sx={tablePaperSx} elevation={3}>
        <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", p: 2, borderBottom: "1px solid #2a2a2a" }}>
          <Box>
            <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0.3 }}>
              Credentials
            </Typography>
            <Typography variant="body2" sx={{ color: "#aaa" }}>
              Stored credentials for remote automation tasks and Ansible playbook runs.
            </Typography>
          </Box>
          <Box sx={{ display: "flex", gap: 1 }}>
            <Button
              variant="outlined"
              size="small"
              startIcon={<RefreshIcon />}
              sx={{ borderColor: "#58a6ff", color: "#58a6ff" }}
              onClick={fetchCredentials}
              disabled={loading}
            >
              Refresh
            </Button>
            <Button
              variant="contained"
              size="small"
              startIcon={<AddIcon />}
              sx={{ bgcolor: "#58a6ff", color: "#0b0f19" }}
              onClick={handleCreate}
            >
              New Credential
            </Button>
          </Box>
        </Box>
        {loading && (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: "#7db7ff", px: 2, py: 1.5 }}>
            <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
            <Typography variant="body2">Loading credentials…</Typography>
          </Box>
        )}
        {error && (
          <Box sx={{ px: 2, py: 1.5, color: "#ff8080" }}>
            <Typography variant="body2">{error}</Typography>
          </Box>
        )}

        <Table size="small" sx={tableSx}>
          <TableHead>
            <TableRow>
              {columns.map((col) => (
                <TableCell key={col.id} align={col.id === "actions" ? "right" : "left"}>
                  {col.id === "actions" ? null : (
                    <TableSortLabel
                      active={orderBy === col.id}
                      direction={orderBy === col.id ? order : "asc"}
                      onClick={handleSort(col.id)}
                    >
                      {col.label}
                    </TableSortLabel>
                  )}
                </TableCell>
              ))}
            </TableRow>
          </TableHead>
          <TableBody>
            {!sortedRows.length && !loading ? (
              <TableRow>
                <TableCell colSpan={columns.length} sx={{ color: "#888", textAlign: "center", py: 4 }}>
                  No credentials have been created yet.
                </TableCell>
              </TableRow>
            ) : (
              sortedRows.map((row) => (
                <TableRow key={row.id} hover>
                  <TableCell>{row.name || "-"}</TableCell>
                  <TableCell>{titleCase(row.credential_type)}</TableCell>
                  <TableCell>
                    <Box sx={{ display: "flex", alignItems: "center" }}>
                      {connectionIcon(row.connection_type)}
                      {titleCase(row.connection_type)}
                    </Box>
                  </TableCell>
                  <TableCell>{row.site_name || "-"}</TableCell>
                  <TableCell>{row.username || "-"}</TableCell>
                  <TableCell>{formatTs(row.updated_at || row.created_at)}</TableCell>
                  <TableCell align="right">
                    <IconButton size="small" onClick={(e) => openMenu(e, row)} sx={{ color: "#7db7ff" }}>
                      <MoreVertIcon fontSize="small" />
                    </IconButton>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Paper>

      <Menu anchorEl={menuAnchor} open={Boolean(menuAnchor)} onClose={closeMenu} elevation={2} PaperProps={{ sx: { bgcolor: "#1f1f1f", color: "#f5f5f5" } }}>
        <MenuItem onClick={() => handleEdit(menuRow)}>Edit</MenuItem>
        <MenuItem onClick={() => handleDelete(menuRow)} sx={{ color: "#ff8080" }}>
          Delete
        </MenuItem>
      </Menu>

      <CredentialEditor
        open={editorOpen}
        mode={editorMode}
        credential={editingCredential}
        onClose={() => {
          setEditorOpen(false);
          setEditingCredential(null);
        }}
        onSaved={handleEditorSaved}
      />

      <ConfirmDeleteDialog
        open={Boolean(deleteTarget)}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={doDelete}
        confirmDisabled={deleteBusy}
        message={
          deleteTarget
            ? `Delete credential '${deleteTarget.name || ""}'? Any jobs referencing it will require an update.`
            : ""
        }
      />
    </>
  );
}
