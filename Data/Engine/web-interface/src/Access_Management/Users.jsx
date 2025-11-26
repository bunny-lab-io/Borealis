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
  InputLabel,
  Checkbox,
  Popover
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import FilterListIcon from "@mui/icons-material/FilterList";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";

/* ---------- Formatting helpers to keep this page in lockstep with Device_List ---------- */
const tablePaperSx = { m: 2, p: 0, bgcolor: "transparent" };
const tableSx = {
  minWidth: 820,
  "& th, & td": {
    color: "#ddd",
    borderColor: "#2a2a2a",
    fontSize: 13,
    py: 0.75
  },
  "& th .MuiTableSortLabel-root": { color: "#ddd" },
  "& th .MuiTableSortLabel-root.Mui-active": { color: "#ddd" }
};
const menuPaperSx = { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" };
const filterFieldSx = {
  input: { color: "#fff" },
  minWidth: 220,
  "& .MuiOutlinedInput-root": {
    "& fieldset": { borderColor: "#555" },
    "&:hover fieldset": { borderColor: "#888" }
  }
};
/* -------------------------------------------------------------------- */

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

export default function UserManagement({ isAdmin = false }) {
  const [rows, setRows] = useState([]); // {username, display_name, role, last_login}
  const [orderBy, setOrderBy] = useState("username");
  const [order, setOrder] = useState("asc");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuUser, setMenuUser] = useState(null);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetTarget, setResetTarget] = useState(null);
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
  const [mfaBusyUser, setMfaBusyUser] = useState(null);
  const [resetMfaOpen, setResetMfaOpen] = useState(false);
  const [resetMfaTarget, setResetMfaTarget] = useState(null);

  // Columns and filters
  const columns = useMemo(() => ([
    { id: "display_name", label: "Display Name" },
    { id: "username", label: "User Name" },
    { id: "last_login", label: "Last Login" },
    { id: "role", label: "User Role" },
    { id: "mfa_enabled", label: "MFA" },
    { id: "actions", label: "" }
  ]), []);
  const [filters, setFilters] = useState({}); // id -> string
  const [filterAnchor, setFilterAnchor] = useState(null); // { id, anchorEl }
  const openFilter = (id) => (e) => setFilterAnchor({ id, anchorEl: e.currentTarget });
  const closeFilter = () => setFilterAnchor(null);
  const onFilterChange = (id) => (e) => setFilters((prev) => ({ ...prev, [id]: e.target.value }));

  const fetchUsers = useCallback(async () => {
    try {
      const res = await fetch("/api/users", { credentials: "include" });
      const data = await res.json();
      if (Array.isArray(data?.users)) {
        setRows(
          data.users.map((u) => ({
            ...u,
            mfa_enabled: u && typeof u.mfa_enabled !== "undefined" ? (u.mfa_enabled ? 1 : 0) : 0
          }))
        );
      } else {
        setRows([]);
      }
    } catch {
      setRows([]);
    }
  }, []);

  useEffect(() => {
    if (!isAdmin) return;
    (async () => {
      try {
        const resp = await fetch("/api/auth/me", { credentials: "include" });
        if (resp.ok) {
          const who = await resp.json();
          setMe(who);
        }
      } catch {}
    })();
    fetchUsers();
  }, [fetchUsers, isAdmin]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else { setOrderBy(col); setOrder("asc"); }
  };

  const filteredSorted = useMemo(() => {
    const applyFilters = (r) => {
      for (const [key, val] of Object.entries(filters || {})) {
        if (!val) continue;
        const needle = String(val).toLowerCase();
        let hay = "";
        if (key === "last_login") hay = String(formatTs(r.last_login));
        else hay = String(r[key] ?? "");
        if (!hay.toLowerCase().includes(needle)) return false;
      }
      return true;
    };

    const dir = order === "asc" ? 1 : -1;
    const arr = rows.filter(applyFilters);
    arr.sort((a, b) => {
      if (orderBy === "last_login") return ((a.last_login || 0) - (b.last_login || 0)) * dir;
      if (orderBy === "mfa_enabled") return ((a.mfa_enabled ? 1 : 0) - (b.mfa_enabled ? 1 : 0)) * dir;
      return String(a[orderBy] ?? "").toLowerCase()
        .localeCompare(String(b[orderBy] ?? "").toLowerCase()) * dir;
    });
    return arr;
  }, [rows, filters, orderBy, order]);

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
      const resp = await fetch(`/api/users/${encodeURIComponent(user.username)}`, { method: "DELETE", credentials: "include" });
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
    if (me && user.username && String(me.username).toLowerCase() === String(user.username).toLowerCase()) {
      setWarnMessage("You cannot change your own role.");
      setWarnOpen(true);
      return;
    }
    const nextRole = (String(user.role || "User").toLowerCase() === "admin") ? "User" : "Admin";
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
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
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

  const openResetMfa = (user) => {
    if (!user) return;
    setResetMfaTarget(user);
    setResetMfaOpen(true);
  };

  const doResetMfa = async () => {
    const user = resetMfaTarget;
    setResetMfaOpen(false);
    setResetMfaTarget(null);
    if (!user) return;
    const username = user.username;
    const keepEnabled = Boolean(user.mfa_enabled);
    setMfaBusyUser(username);
    try {
      const resp = await fetch(`/api/users/${encodeURIComponent(username)}/mfa`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ enabled: keepEnabled, reset_secret: true })
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to reset MFA for this user.");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
    } catch (err) {
      console.error(err);
      setWarnMessage("Failed to reset MFA for this user.");
      setWarnOpen(true);
    } finally {
      setMfaBusyUser(null);
    }
  };

  const toggleMfa = async (user, enabled) => {
    if (!user) return;
    const previous = Boolean(user.mfa_enabled);
    const nextFlag = enabled ? 1 : 0;
    setRows((prev) =>
      prev.map((r) =>
        String(r.username).toLowerCase() === String(user.username).toLowerCase()
          ? { ...r, mfa_enabled: nextFlag }
          : r
      )
    );
    setMfaBusyUser(user.username);
    try {
      const resp = await fetch(`/api/users/${encodeURIComponent(user.username)}/mfa`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ enabled })
      });
      const data = await resp.json();
      if (!resp.ok) {
        setRows((prev) =>
          prev.map((r) =>
            String(r.username).toLowerCase() === String(user.username).toLowerCase()
              ? { ...r, mfa_enabled: previous ? 1 : 0 }
              : r
          )
        );
        setWarnMessage(data?.error || "Failed to update MFA settings.");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
    } catch (e) {
      console.error(e);
      setRows((prev) =>
        prev.map((r) =>
          String(r.username).toLowerCase() === String(user.username).toLowerCase()
            ? { ...r, mfa_enabled: previous ? 1 : 0 }
            : r
        )
      );
      setWarnMessage("Failed to update MFA settings.");
      setWarnOpen(true);
    } finally {
      setMfaBusyUser(null);
    }
  };

  const doResetPassword = async () => {
    const user = resetTarget;
    if (!user) return;
    const pw = newPassword || "";
    if (!pw.trim()) return;
    try {
      const hash = await sha512(pw);
      const resp = await fetch(`/api/users/${encodeURIComponent(user.username)}/reset_password`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ password_sha512: hash })
      });
      const data = await resp.json();
      if (!resp.ok) {
        alert(data?.error || "Failed to reset password");
        return;
      }
      setResetOpen(false);
      setResetTarget(null);
      setNewPassword("");
    } catch (e) {
      console.error(e);
      alert("Failed to reset password");
    }
  };

  const openReset = (user) => {
    if (!user) return;
    setResetTarget(user);
    setResetOpen(true);
    setNewPassword("");
  };

  const openCreate = () => { setCreateOpen(true); setCreateForm({ username: "", display_name: "", password: "", role: "User" }); };
  const doCreate = async () => {
    const u = (createForm.username || "").trim();
    const dn = (createForm.display_name || u).trim();
    const pw = (createForm.password || "").trim();
    const role = (createForm.role || "User");
    if (!u || !pw) return;
    try {
      const hash = await sha512(pw);
      const resp = await fetch("/api/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ username: u, display_name: dn, password_sha512: hash, role })
      });
      const data = await resp.json();
      if (!resp.ok) {
        alert(data?.error || "Failed to create user");
        return;
      }
      setCreateOpen(false);
      await fetchUsers();
    } catch (e) {
      console.error(e);
      alert("Failed to create user");
    }
  };

  if (!isAdmin) return null;

  return (
    <>
      <Paper sx={tablePaperSx} elevation={2}>
        <Box sx={{ p: 2, pb: 1, display: "flex", alignItems: "center", justifyContent: "space-between" }}>
          <Box>
            <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
              User Management
            </Typography>
            <Typography variant="body2" sx={{ color: "#aaa" }}>
              Manage authorized users of the Borealis Automation Platform.
            </Typography>
          </Box>
          <Button
            variant="outlined"
            size="small"
            onClick={openCreate}
            sx={{ color: "#58a6ff", borderColor: "#58a6ff", textTransform: "none" }}
          >
            Create User
          </Button>
        </Box>

        <Table size="small" sx={tableSx}>
          <TableHead>
            <TableRow>
              {/* Leading checkbox gutter to match Devices table rhythm */}
              <TableCell padding="checkbox" />
              {columns.map((col) => (
                <TableCell
                  key={col.id}
                  sortDirection={["actions"].includes(col.id) ? false : (orderBy === col.id ? order : false)}
                >
                  {col.id !== "actions" ? (
                    <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                      <TableSortLabel
                        active={orderBy === col.id}
                        direction={orderBy === col.id ? order : "asc"}
                        onClick={() => handleSort(col.id)}
                      >
                        {col.label}
                      </TableSortLabel>
                      <IconButton
                        size="small"
                        onClick={openFilter(col.id)}
                        sx={{ color: filters[col.id] ? "#58a6ff" : "#888" }}
                      >
                        <FilterListIcon fontSize="inherit" />
                      </IconButton>
                    </Box>
                  ) : null}
                </TableCell>
              ))}
            </TableRow>
          </TableHead>

          <TableBody>
            {filteredSorted.map((u) => (
              <TableRow key={u.username} hover>
                {/* Body gutter to stay aligned with header */}
                <TableCell padding="checkbox" />
                <TableCell>{u.display_name || u.username}</TableCell>
                <TableCell>{u.username}</TableCell>
                <TableCell>{formatTs(u.last_login)}</TableCell>
                <TableCell>{u.role || "User"}</TableCell>
                <TableCell align="center">
                  <Checkbox
                    size="small"
                    checked={Boolean(u.mfa_enabled)}
                    disabled={Boolean(mfaBusyUser && String(mfaBusyUser).toLowerCase() === String(u.username).toLowerCase())}
                    onChange={(event) => {
                      event.stopPropagation();
                      toggleMfa(u, event.target.checked);
                    }}
                    onClick={(event) => event.stopPropagation()}
                    sx={{
                      color: "#888",
                      "&.Mui-checked": { color: "#58a6ff" }
                    }}
                    inputProps={{ "aria-label": `Toggle MFA for ${u.username}` }}
                  />
                </TableCell>
                <TableCell align="right">
                  <IconButton size="small" onClick={(e) => openMenu(e, u)} sx={{ color: "#ccc" }}>
                    <MoreVertIcon fontSize="inherit" />
                  </IconButton>
                </TableCell>
              </TableRow>
            ))}
            {filteredSorted.length === 0 && (
              <TableRow>
                <TableCell colSpan={columns.length + 1} sx={{ color: "#888" }}>
                  No users found.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>

        {/* Filter popover (styled to match Device_List) */}
        <Popover
          open={Boolean(filterAnchor)}
          anchorEl={filterAnchor?.anchorEl || null}
          onClose={closeFilter}
          anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
          PaperProps={{ sx: { bgcolor: "#1e1e1e", p: 1 } }}
        >
          {filterAnchor && (
            <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
              <TextField
                autoFocus
                size="small"
                placeholder={`Filter ${columns.find((c) => c.id === filterAnchor.id)?.label || ""}`}
                value={filters[filterAnchor.id] || ""}
                onChange={onFilterChange(filterAnchor.id)}
                onKeyDown={(e) => { if (e.key === "Escape") closeFilter(); }}
                sx={filterFieldSx}
              />
              <Button
                variant="outlined"
                size="small"
                onClick={() => {
                  setFilters((prev) => ({ ...prev, [filterAnchor.id]: "" }));
                  closeFilter();
                }}
                sx={{ textTransform: "none", borderColor: "#555", color: "#bbb" }}
              >
                Clear
              </Button>
            </Box>
          )}
        </Popover>

        <Menu
          anchorEl={menuAnchor?.anchorEl}
          open={Boolean(menuAnchor)}
          onClose={closeMenu}
          anchorOrigin={{ vertical: "bottom", horizontal: "right" }}
          transformOrigin={{ vertical: "top", horizontal: "right" }}
          PaperProps={{ sx: menuPaperSx }}
        >
          <MenuItem
            disabled={me && menuUser && String(me.username).toLowerCase() === String(menuUser.username).toLowerCase()}
            onClick={() => { const u = menuUser; closeMenu(); confirmDelete(u); }}
          >
            Delete User
          </MenuItem>
          <MenuItem onClick={() => { const u = menuUser; closeMenu(); openReset(u); }}>Reset Password</MenuItem>
          <MenuItem
            disabled={me && menuUser && String(me.username).toLowerCase() === String(menuUser.username).toLowerCase()}
            onClick={() => { const u = menuUser; closeMenu(); openChangeRole(u); }}
          >
            Change Role
          </MenuItem>
          <MenuItem onClick={() => { const u = menuUser; closeMenu(); openResetMfa(u); }}>
            Reset MFA
          </MenuItem>
        </Menu>

        <Dialog open={resetOpen} onClose={() => setResetOpen(false)} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
          <DialogTitle>Reset Password</DialogTitle>
          <DialogContent>
            <DialogContentText sx={{ color: "#ccc" }}>
              Enter a new password for {resetTarget?.username}.
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
            <Button onClick={() => { setResetOpen(false); setResetTarget(null); }} sx={{ color: "#58a6ff" }}>Cancel</Button>
            <Button onClick={doResetPassword} sx={{ color: "#58a6ff" }}>OK</Button>
          </DialogActions>
        </Dialog>

        <Dialog open={createOpen} onClose={() => setCreateOpen(false)} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
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
              <InputLabel sx={{ color: "#aaa" }}>Role</InputLabel>
              <Select
                native
                value={createForm.role}
                onChange={(e) => setCreateForm((p) => ({ ...p, role: e.target.value }))}
                sx={{
                  backgroundColor: "#2a2a2a",
                  color: "#ccc",
                  borderColor: "#444"
                }}
              >
                <option value="User">User</option>
                <option value="Admin">Admin</option>
              </Select>
            </FormControl>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setCreateOpen(false)} sx={{ color: "#58a6ff" }}>Cancel</Button>
            <Button onClick={doCreate} sx={{ color: "#58a6ff" }}>Create</Button>
          </DialogActions>
        </Dialog>
      </Paper>

      <ConfirmDeleteDialog
        open={confirmDeleteOpen}
        message={`Are you sure you want to delete user '${deleteTarget?.username || ""}'?`}
        onCancel={() => setConfirmDeleteOpen(false)}
        onConfirm={doDelete}
      />
      <ConfirmDeleteDialog
        open={confirmChangeRoleOpen}
        message={changeRoleTarget ? `Change role for '${changeRoleTarget.username}' to ${changeRoleNext}?` : ""}
        onCancel={() => setConfirmChangeRoleOpen(false)}
        onConfirm={doChangeRole}
      />
      <ConfirmDeleteDialog
        open={resetMfaOpen}
        message={resetMfaTarget ? `Reset MFA enrollment for '${resetMfaTarget.username}'? This clears their existing authenticator.` : ""}
        onCancel={() => { setResetMfaOpen(false); setResetMfaTarget(null); }}
        onConfirm={doResetMfa}
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
