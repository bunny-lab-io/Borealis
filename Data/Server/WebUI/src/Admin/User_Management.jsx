import React, { useEffect, useMemo, useState, useCallback } from "react";
import {
  Paper,
  Box,
  Typography,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel,
  IconButton,
  Menu,
  MenuItem,
  Button,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
  TextField,
  Select,
  FormControl,
  InputLabel
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";

function formatTs(tsSec) {
  if (!tsSec) return "-";
  const d = new Date((tsSec || 0) * 1000);
  const date = d.toLocaleDateString("en-US", { month: "2-digit", day: "2-digit", year: "numeric" });
  const time = d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });
  return `${date} @ ${time}`;
}

async function sha512(text) {
  const enc = new TextEncoder();
  const data = enc.encode(text || "");
  const buf = await crypto.subtle.digest("SHA-512", data);
  const arr = Array.from(new Uint8Array(buf));
  return arr.map((b) => b.toString(16).padStart(2, "0")).join("");
}

export default function UserManagement() {
  const [rows, setRows] = useState([]); // {username, display_name, role, last_login}
  const [orderBy, setOrderBy] = useState("username");
  const [order, setOrder] = useState("asc");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuUser, setMenuUser] = useState(null);
  const [resetOpen, setResetOpen] = useState(false);
  const [newPassword, setNewPassword] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [createForm, setCreateForm] = useState({ username: "", display_name: "", password: "", role: "User" });
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [confirmChangeRoleOpen, setConfirmChangeRoleOpen] = useState(false);
  const [changeRoleTarget, setChangeRoleTarget] = useState(null);
  const [changeRoleNext, setChangeRoleNext] = useState(null);
  const [warnOpen, setWarnOpen] = useState(false);
  const [warnMessage, setWarnMessage] = useState("");
  const [me, setMe] = useState(null);

  const fetchUsers = useCallback(async () => {
    try {
      const res = await fetch("/api/users", { credentials: "include" });
      const data = await res.json();
      setRows(Array.isArray(data?.users) ? data.users : []);
    } catch {
      setRows([]);
    }
  }, []);

  useEffect(() => {
    fetchUsers();
    (async () => {
      try {
        const resp = await fetch('/api/auth/me', { credentials: 'include' });
        if (resp.ok) {
          const who = await resp.json();
          setMe(who);
        }
      } catch {}
    })();
  }, [fetchUsers]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else { setOrderBy(col); setOrder("asc"); }
  };

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    const arr = [...rows];
    arr.sort((a, b) => {
      if (orderBy === "last_login") return ((a.last_login || 0) - (b.last_login || 0)) * dir;
      return String(a[orderBy] ?? "").toLowerCase().localeCompare(String(b[orderBy] ?? "").toLowerCase()) * dir;
    });
    return arr;
  }, [rows, orderBy, order]);

  const openMenu = (evt, user) => {
    setMenuAnchor({ mouseX: evt.clientX, mouseY: evt.clientY, anchorEl: evt.currentTarget });
    setMenuUser(user);
  };
  const closeMenu = () => { setMenuAnchor(null); setMenuUser(null); };

  const confirmDelete = (user) => {
    if (!user) return;
    if (me && user.username && String(me.username).toLowerCase() === String(user.username).toLowerCase()) {
      setWarnMessage("You cannot delete the user you are currently logged in as.");
      setWarnOpen(true);
      return;
    }
    setDeleteTarget(user);
    setConfirmDeleteOpen(true);
  };

  const doDelete = async () => {
    const user = deleteTarget;
    setConfirmDeleteOpen(false);
    if (!user) return;
    try {
      const resp = await fetch(`/api/users/${encodeURIComponent(user.username)}`, { method: 'DELETE', credentials: 'include' });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to delete user");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
    } catch (e) {
      console.error(e);
      setWarnMessage("Failed to delete user");
      setWarnOpen(true);
    }
  };

  const openChangeRole = (user) => {
    if (!user) return;
    const nextRole = (String(user.role || 'User').toLowerCase() === 'admin') ? 'User' : 'Admin';
    setChangeRoleTarget(user);
    setChangeRoleNext(nextRole);
    setConfirmChangeRoleOpen(true);
  };

  const doChangeRole = async () => {
    const user = changeRoleTarget;
    const nextRole = changeRoleNext;
    setConfirmChangeRoleOpen(false);
    if (!user || !nextRole) return;
    try {
      const resp = await fetch(`/api/users/${encodeURIComponent(user.username)}/role`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ role: nextRole })
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to change role");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
    } catch (e) {
      console.error(e);
      setWarnMessage("Failed to change role");
      setWarnOpen(true);
    }
  };

  const doResetPassword = async () => {
    const user = menuUser;
    if (!user) return;
    const pw = newPassword || '';
    if (!pw.trim()) return;
    try {
      const hash = await sha512(pw);
      const resp = await fetch(`/api/users/${encodeURIComponent(user.username)}/reset_password`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ password_sha512: hash })
      });
      const data = await resp.json();
      if (!resp.ok) {
        alert(data?.error || "Failed to reset password");
        return;
      }
      setResetOpen(false);
      setNewPassword("");
    } catch (e) {
      console.error(e);
      alert("Failed to reset password");
    }
  };

  const openReset = () => { setResetOpen(true); setNewPassword(""); closeMenu(); };

  const openCreate = () => { setCreateOpen(true); setCreateForm({ username: "", display_name: "", password: "", role: "User" }); };
  const doCreate = async () => {
    const u = (createForm.username || '').trim();
    const dn = (createForm.display_name || u).trim();
    const pw = (createForm.password || '').trim();
    const role = (createForm.role || 'User');
    if (!u || !pw) return;
    try {
      const hash = await sha512(pw);
      const resp = await fetch('/api/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ username: u, display_name: dn, password_sha512: hash, role })
      });
      const data = await resp.json();
      if (!resp.ok) {
        alert(data?.error || 'Failed to create user');
        return;
      }
      setCreateOpen(false);
      await fetchUsers();
    } catch (e) {
      console.error(e);
      alert('Failed to create user');
    }
  };

  return (
    <>
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>User Management</Typography>
          <Typography sx={{ color: '#aaa', fontSize: '0.85rem', mt: 0.5 }}>
            Create, Edit, and Remove Borealis Server Operators
          </Typography>
        </Box>
        <Box>
          <Button
            variant="outlined"
            size="small"
            onClick={openCreate}
            sx={{ color: '#58a6ff', borderColor: '#58a6ff', textTransform: 'none' }}
          >
            Create User
          </Button>
        </Box>
      </Box>

      <Table size="small" sx={{ minWidth: 700 }}>
        <TableHead>
          <TableRow>
            {[
              { id: 'display_name', label: 'Display Name' },
              { id: 'username', label: 'User Name' },
              { id: 'last_login', label: 'Last Login' },
              { id: 'role', label: 'User Role' },
              { id: 'actions', label: '' },
            ].map((col) => (
              <TableCell key={col.id} sortDirection={['actions'].includes(col.id) ? false : (orderBy === col.id ? order : false)}>
                {col.id !== 'actions' ? (
                  <TableSortLabel
                    active={orderBy === col.id}
                    direction={orderBy === col.id ? order : 'asc'}
                    onClick={() => handleSort(col.id)}
                  >
                    {col.label}
                  </TableSortLabel>
                ) : null}
              </TableCell>
            ))}
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((u) => (
            <TableRow key={u.username} hover>
              <TableCell>{u.display_name || u.username}</TableCell>
              <TableCell>{u.username}</TableCell>
              <TableCell>{formatTs(u.last_login)}</TableCell>
              <TableCell>{u.role || 'User'}</TableCell>
              <TableCell align="right" sx={{ width: 1 }}>
                <IconButton size="small" onClick={(e) => openMenu(e, u)} sx={{ color: '#aaa' }}>
                  <MoreVertIcon fontSize="inherit" />
                </IconButton>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <Menu
        anchorEl={menuAnchor?.anchorEl}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        PaperProps={{ sx: { bgcolor: '#1e1e1e', color: '#fff' } }}
      >
        <MenuItem
          disabled={me && menuUser && String(me.username).toLowerCase() === String(menuUser.username).toLowerCase()}
          onClick={() => { const u = menuUser; closeMenu(); confirmDelete(u); }}
        >
          Delete User
        </MenuItem>
        <MenuItem onClick={() => { openReset(); }}>Reset Password</MenuItem>
        <MenuItem onClick={() => { const u = menuUser; closeMenu(); openChangeRole(u); }}>Change Role</MenuItem>
      </Menu>

      <Dialog open={resetOpen} onClose={() => setResetOpen(false)} PaperProps={{ sx: { bgcolor: '#121212', color: '#fff' } }}>
        <DialogTitle>Reset Password</DialogTitle>
        <DialogContent>
          <DialogContentText sx={{ color: '#ccc' }}>
            Enter a new password for {menuUser?.username}.
          </DialogContentText>
          <TextField
            autoFocus
            margin="dense"
            fullWidth
            label="New Password"
            type="password"
            variant="outlined"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#2a2a2a",
                color: "#ccc",
                "& fieldset": { borderColor: "#444" },
                "&:hover fieldset": { borderColor: "#666" }
              },
              label: { color: "#aaa" },
              mt: 1
            }}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setResetOpen(false)} sx={{ color: '#58a6ff' }}>Cancel</Button>
          <Button onClick={doResetPassword} sx={{ color: '#58a6ff' }}>OK</Button>
        </DialogActions>
      </Dialog>

      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} PaperProps={{ sx: { bgcolor: '#121212', color: '#fff' } }}>
        <DialogTitle>Create User</DialogTitle>
        <DialogContent>
          <TextField
            autoFocus
            margin="dense"
            fullWidth
            label="Username"
            variant="outlined"
            value={createForm.username}
            onChange={(e) => setCreateForm((p) => ({ ...p, username: e.target.value }))}
            sx={{
              "& .MuiOutlinedInput-root": { backgroundColor: "#2a2a2a", color: "#ccc", "& fieldset": { borderColor: "#444" }, "&:hover fieldset": { borderColor: "#666" } },
              label: { color: "#aaa" }, mt: 1
            }}
          />
          <TextField
            margin="dense"
            fullWidth
            label="Display Name (optional)"
            variant="outlined"
            value={createForm.display_name}
            onChange={(e) => setCreateForm((p) => ({ ...p, display_name: e.target.value }))}
            sx={{
              "& .MuiOutlinedInput-root": { backgroundColor: "#2a2a2a", color: "#ccc", "& fieldset": { borderColor: "#444" }, "&:hover fieldset": { borderColor: "#666" } },
              label: { color: "#aaa" }, mt: 1
            }}
          />
          <TextField
            margin="dense"
            fullWidth
            label="Password"
            type="password"
            variant="outlined"
            value={createForm.password}
            onChange={(e) => setCreateForm((p) => ({ ...p, password: e.target.value }))}
            sx={{
              "& .MuiOutlinedInput-root": { backgroundColor: "#2a2a2a", color: "#ccc", "& fieldset": { borderColor: "#444" }, "&:hover fieldset": { borderColor: "#666" } },
              label: { color: "#aaa" }, mt: 1
            }}
          />
          <FormControl fullWidth sx={{ mt: 2 }}>
            <InputLabel sx={{ color: '#aaa' }}>Role</InputLabel>
            <Select
              native
              value={createForm.role}
              onChange={(e) => setCreateForm((p) => ({ ...p, role: e.target.value }))}
              sx={{
                backgroundColor: '#2a2a2a',
                color: '#ccc',
                borderColor: '#444'
              }}
            >
              <option value="User">User</option>
              <option value="Admin">Admin</option>
            </Select>
          </FormControl>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCreateOpen(false)} sx={{ color: '#58a6ff' }}>Cancel</Button>
          <Button onClick={doCreate} sx={{ color: '#58a6ff' }}>Create</Button>
        </DialogActions>
      </Dialog>
    </Paper>
    <ConfirmDeleteDialog
      open={confirmDeleteOpen}
      message={`Are you sure you want to delete user '${deleteTarget?.username || ''}'?`}
      onCancel={() => setConfirmDeleteOpen(false)}
      onConfirm={doDelete}
    />
    <ConfirmDeleteDialog
      open={confirmChangeRoleOpen}
      message={changeRoleTarget ? `Change role for '${changeRoleTarget.username}' to ${changeRoleNext}?` : ''}
      onCancel={() => setConfirmChangeRoleOpen(false)}
      onConfirm={doChangeRole}
    />
    <ConfirmDeleteDialog
      open={warnOpen}
      message={warnMessage}
      onCancel={() => setWarnOpen(false)}
      onConfirm={() => setWarnOpen(false)}
    />
    </>
  );
}
