import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import { useNavigate } from "react-router-dom";
import {
  Paper,
  Box,
  Typography,
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
  Checkbox,
  Stack,
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import GroupIcon from "@mui/icons-material/Group";
import LocationCityIcon from "@mui/icons-material/LocationCity";
import PersonAddAlt1Icon from "@mui/icons-material/PersonAddAlt1";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_SELECT_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../app/providers/AuthContext.jsx";
import { buildSiteAssignmentPath } from "../app/routes/paths.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "User Management";
const PAGE_SUBTITLE = "Manage Borealis Engine Operators, MFA, and password resets.";
const ADMIN_SELECTION_MESSAGE =
  "An administrator was selected, admins inherantly have access to all managed sites.  Please unselect the admin and try again.";

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';
const AUTO_SIZE_COLUMNS = ["display_name", "username", "last_login", "role", "mfa_enabled"];

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
  const navigate = useNavigate();
  const { isAdmin } = useAuth();
  const [rows, setRows] = useState([]);
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
  const [confirmMfaStateOpen, setConfirmMfaStateOpen] = useState(false);
  const [confirmMfaStateTarget, setConfirmMfaStateTarget] = useState(null);
  const [confirmMfaStateNextEnabled, setConfirmMfaStateNextEnabled] = useState(null);
  const [resetMfaOpen, setResetMfaOpen] = useState(false);
  const [resetMfaTarget, setResetMfaTarget] = useState(null);
  const [selectedUsernames, setSelectedUsernames] = useState(() => new Set());
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);
  const sendNotification = useAppNotifications({
    title: PAGE_TITLE,
    icon: "group",
    variant: "info",
  });

  const fetchUsers = useCallback(async () => {
    try {
      const res = await fetch("/api/users", { credentials: "include" });
      const data = await res.json();
      if (Array.isArray(data?.users)) {
        setRows(
          data.users.map((u) => ({
            ...u,
            mfa_enabled: u && typeof u.mfa_enabled !== "undefined" ? (u.mfa_enabled ? 1 : 0) : 0,
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

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api || !rows.length) return;
    const doSize = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {}
    };
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(doSize);
    } else {
      setTimeout(doSize, 0);
    }
  }, [rows.length]);

  useEffect(() => {
    autoSizeColumns();
  }, [rows, autoSizeColumns]);

  useEffect(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api) return;
    api.forEachNode((node) => {
      const username = String(node.data?.username || "").trim().toLowerCase();
      node.setSelected(Boolean(username && selectedUsernames.has(username)));
    });
  }, [rows, selectedUsernames]);

  const selectedUsers = useMemo(
    () =>
      rows.filter((row) => {
        const key = String(row?.username || "").trim().toLowerCase();
        return Boolean(key && selectedUsernames.has(key));
      }),
    [rows, selectedUsernames]
  );

  const openMenu = (event, user) => {
    setMenuAnchor(event.currentTarget);
    setMenuUser(user);
  };
  const closeMenu = () => {
    setMenuAnchor(null);
    setMenuUser(null);
  };

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
      setSelectedUsernames((prev) => {
        const next = new Set(prev);
        next.delete(String(user.username).toLowerCase());
        return next;
      });
      if (user?.username) {
        sendNotification(`User ${user.username} Deleted Successfully`);
      }
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
    const nextRole = String(user.role || "User").toLowerCase() === "admin" ? "User" : "Admin";
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
        body: JSON.stringify({ role: nextRole }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to change role");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
      if (user?.username) {
        const action = (nextRole || "").toLowerCase() === "admin" ? "Promoted to Admin" : "Demoted to User";
        sendNotification(`User ${user.username} ${action}`);
      }
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

  const openChangeMfaState = (user) => {
    if (!user) return;
    setConfirmMfaStateTarget(user);
    setConfirmMfaStateNextEnabled(!Boolean(user.mfa_enabled));
    setConfirmMfaStateOpen(true);
  };

  const doChangeMfaState = async () => {
    const user = confirmMfaStateTarget;
    const nextEnabled = Boolean(confirmMfaStateNextEnabled);
    setConfirmMfaStateOpen(false);
    setConfirmMfaStateTarget(null);
    setConfirmMfaStateNextEnabled(null);
    if (!user) return;

    const username = user.username;
    setMfaBusyUser(username);
    try {
      const resp = await fetch(`/api/users/${encodeURIComponent(username)}/mfa`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ enabled: nextEnabled }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to update MFA settings.");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
      if (username) {
        sendNotification(`MFA ${nextEnabled ? "Enabled" : "Disabled"} for "${username}"`);
      }
    } catch (err) {
      console.error(err);
      setWarnMessage("Failed to update MFA settings.");
      setWarnOpen(true);
    } finally {
      setMfaBusyUser(null);
    }
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
        body: JSON.stringify({ enabled: keepEnabled, reset_secret: true }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to reset MFA for this user.");
        setWarnOpen(true);
        return;
      }
      await fetchUsers();
      if (username) {
        sendNotification(`MFA Reset for "${username}"`);
      }
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
        body: JSON.stringify({ enabled }),
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
        body: JSON.stringify({ password_sha512: hash }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to reset password");
        setWarnOpen(true);
        return;
      }
      setResetOpen(false);
      setResetTarget(null);
      setNewPassword("");
      if (user?.username) {
        sendNotification(`Password Reset for ${user.username}`);
      }
    } catch (e) {
      console.error(e);
      setWarnMessage("Failed to reset password");
      setWarnOpen(true);
    }
  };

  const openReset = (user) => {
    if (!user) return;
    setResetTarget(user);
    setResetOpen(true);
    setNewPassword("");
  };

  const openCreate = useCallback(() => {
    setCreateOpen(true);
    setCreateForm({ username: "", display_name: "", password: "", role: "User" });
  }, []);

  const handleOpenSiteAssignment = useCallback(() => {
    if (!selectedUsers.length) return;
    const adminSelected = selectedUsers.some((user) => String(user.role || "").toLowerCase() === "admin");
    if (adminSelected) {
      setWarnMessage(ADMIN_SELECTION_MESSAGE);
      setWarnOpen(true);
      return;
    }
    const usernames = selectedUsers
      .map((user) => String(user?.username || "").trim())
      .filter(Boolean);
    navigate(buildSiteAssignmentPath(usernames));
  }, [navigate, selectedUsers]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "users-site-assignment",
        label: "Site Assignment",
        icon: <LocationCityIcon />,
        tone: "secondary",
        disabled: selectedUsers.length === 0,
        onClick: handleOpenSiteAssignment,
      },
      {
        id: "users-create",
        label: "Create User",
        icon: <PersonAddAlt1Icon />,
        tone: "primary",
        onClick: openCreate,
      },
    ],
    [handleOpenSiteAssignment, openCreate, selectedUsers.length]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: GroupIcon,
    actions: pageHeaderActions,
  });

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableSelectionWithoutKeys: true,
      enableClickSelection: false,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      minWidth: 52,
      width: 52,
      maxWidth: 52,
      pinned: "left",
      filter: false,
      sortable: false,
      resizable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  const doCreate = async () => {
    const u = (createForm.username || "").trim();
    const dn = (createForm.display_name || u).trim();
    const pw = (createForm.password || "").trim();
    const role = createForm.role || "User";
    if (!u || !pw) return;
    try {
      const hash = await sha512(pw);
      const resp = await fetch("/api/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ username: u, display_name: dn, password_sha512: hash, role }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        setWarnMessage(data?.error || "Failed to create user");
        setWarnOpen(true);
        return;
      }
      setCreateOpen(false);
      await fetchUsers();
      sendNotification(`User ${u} Created Successfully`);
    } catch (e) {
      console.error(e);
      setWarnMessage("Failed to create user");
      setWarnOpen(true);
    }
  };

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Display Name",
        field: "display_name",
        minWidth: 220,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "User Name",
        field: "username",
        minWidth: 220,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Last Login",
        field: "last_login",
        minWidth: 190,
        valueFormatter: (params) => formatTs(params.value),
        comparator: (a, b) => (Number(a) || 0) - (Number(b) || 0),
        cellClass: "auto-col-tight",
      },
      {
        headerName: "User Role",
        field: "role",
        minWidth: 150,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "MFA",
        field: "mfa_enabled",
        minWidth: 110,
        width: 110,
        maxWidth: 110,
        filter: false,
        valueGetter: (params) => (params.data?.mfa_enabled ? "Enabled" : "Disabled"),
        comparator: (a, b) => String(a || "").localeCompare(String(b || "")),
        cellRenderer: (params) => {
          const user = params.data || {};
          const busy = Boolean(
            mfaBusyUser && String(mfaBusyUser).toLowerCase() === String(user.username || "").toLowerCase()
          );
          const explicitlyDisabled = !Boolean(user.mfa_enabled);
          return (
            <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%" }}>
              <Checkbox
                size="small"
                checked={Boolean(user.mfa_enabled)}
                disabled={busy || explicitlyDisabled}
                onChange={(event) => {
                  event.stopPropagation();
                  toggleMfa(user, event.target.checked);
                }}
                onClick={(event) => event.stopPropagation()}
                sx={{
                  color: explicitlyDisabled ? "rgba(124,138,165,0.42)" : "#7c8aa5",
                  "&.Mui-checked": { color: "#7dd3fc" },
                  "&.Mui-disabled": { color: "rgba(124,138,165,0.42)" },
                }}
                inputProps={{ "aria-label": `Toggle MFA for ${user.username}` }}
              />
            </Box>
          );
        },
      },
      {
        headerName: "Actions",
        field: "actions",
        minWidth: 110,
        width: 110,
        maxWidth: 110,
        sortable: false,
        filter: false,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true,
        cellRenderer: (params) => {
          const user = params.data || {};
          return (
            <Box sx={{ display: "flex", alignItems: "center", justifyContent: "flex-end", width: "100%" }}>
              <IconButton
                size="small"
                onClick={(event) => {
                  event.stopPropagation();
                  openMenu(event, user);
                }}
                sx={{ color: "#cbd5e1" }}
              >
                <MoreVertIcon fontSize="inherit" />
              </IconButton>
            </Box>
          );
        },
      },
    ],
    [mfaBusyUser]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      minWidth: 140,
    }),
    []
  );

  if (!isAdmin) return null;

  return (
    <>
      <Paper
        sx={{
          m: 0,
          p: 0,
          display: "flex",
          flexDirection: "column",
          flexGrow: 1,
          minWidth: 0,
          height: "100%",
          borderRadius: 0,
          border: "none",
          background: "transparent",
          boxShadow: "none",
          overflow: "hidden",
        }}
        elevation={0}
      >
        <PageBodyFrame variant="grid">
          <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
            {selectedUsers.length ? (
              <Box sx={{ display: "flex", justifyContent: "flex-end", mb: 1.25 }}>
                <Typography variant="body2" sx={{ color: "#94a3b8", fontWeight: 600 }}>
                  {selectedUsers.length} selected
                </Typography>
              </Box>
            ) : null}
            <Box
              className={themeClassName}
              sx={{
                flexGrow: 1,
                minHeight: 0,
                "--ag-font-family": gridFontFamily,
                "--ag-icon-font-family": iconFontFamily,
                "--ag-checkbox-border-radius": "3px",
                "& .ag-root-wrapper": {
                  minHeight: "100%",
                  border: "none",
                  borderRadius: 0,
                  background: "transparent",
                },
                "& .ag-header": {
                  backgroundColor: "rgba(15,23,42,0.9)",
                  borderBottom: "1px solid rgba(148,163,184,0.25)",
                },
                "& .ag-header-cell-label": {
                  color: "#e2e8f0",
                  fontWeight: 600,
                  letterSpacing: 0.3,
                },
                "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "flex-start",
                  textAlign: "left",
                  padding: "8px 12px 8px 18px",
                },
                "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
                  width: "100%",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "flex-start",
                  padding: 0,
                },
                "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
                  paddingLeft: "12px",
                  paddingRight: "9px",
                },
                "& .ag-row": {
                  borderColor: "rgba(255,255,255,0.04)",
                  transition: "background 0.2s ease",
                },
                "& .ag-row:nth-of-type(even)": {
                  backgroundColor: "rgba(15,23,42,0.45)",
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
                defaultColDef={defaultColDef}
                rowSelection={rowSelection}
                selectionColumnDef={selectionColumnDef}
                suppressCellFocus
                pagination
                paginationPageSize={20}
                paginationPageSizeSelector={[20, 50, 100]}
                animateRows
                getRowId={(params) => String(params.data?.username || "")}
                onGridReady={(params) => {
                  gridApiRef.current = params.api;
                  autoSizeColumns();
                }}
                onSelectionChanged={() => {
                  const api = gridApiRef.current || gridRef.current?.api;
                  if (!api) return;
                  const selected = api
                    .getSelectedNodes()
                    .map((node) => String(node.data?.username || "").trim().toLowerCase())
                    .filter(Boolean);
                  setSelectedUsernames(new Set(selected));
                }}
                theme={gridTheme}
              />
            </Box>
          </Box>
        </PageBodyFrame>

        <Menu
          anchorEl={menuAnchor}
          open={Boolean(menuAnchor)}
          onClose={closeMenu}
          anchorOrigin={{ vertical: "bottom", horizontal: "right" }}
          transformOrigin={{ vertical: "top", horizontal: "right" }}
          PaperProps={{ sx: { bgcolor: "#111827", color: "#e5eefc", fontSize: "0.9rem" } }}
        >
          <MenuItem
            disabled={me && menuUser && String(me.username).toLowerCase() === String(menuUser.username).toLowerCase()}
            onClick={() => {
              const user = menuUser;
              closeMenu();
              confirmDelete(user);
            }}
          >
            Delete User
          </MenuItem>
          <MenuItem
            onClick={() => {
              const user = menuUser;
              closeMenu();
              openReset(user);
            }}
          >
            Reset Password
          </MenuItem>
          <MenuItem
            disabled={me && menuUser && String(me.username).toLowerCase() === String(menuUser.username).toLowerCase()}
            onClick={() => {
              const user = menuUser;
              closeMenu();
              openChangeRole(user);
            }}
          >
            Change Role
          </MenuItem>
          <MenuItem
            disabled={Boolean(mfaBusyUser && menuUser && String(mfaBusyUser).toLowerCase() === String(menuUser.username || "").toLowerCase())}
            onClick={() => {
              const user = menuUser;
              closeMenu();
              openChangeMfaState(user);
            }}
          >
            {Boolean(menuUser?.mfa_enabled) ? "Disable MFA" : "Enable MFA"}
          </MenuItem>
          <MenuItem
            onClick={() => {
              const user = menuUser;
              closeMenu();
              openResetMfa(user);
            }}
            disabled={!Boolean(menuUser?.mfa_enabled)}
          >
            Reset MFA
          </MenuItem>
        </Menu>

        <Dialog open={resetOpen} onClose={() => setResetOpen(false)} maxWidth="xs" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
          <DialogTitle sx={DIALOG_TITLE_SX}>
            <DialogHeaderBlock
              title="Reset Password"
              subtitle={resetTarget?.username ? `Set a new password for ${resetTarget.username}.` : undefined}
            />
          </DialogTitle>
          <DialogContent sx={DIALOG_CONTENT_SX}>
            <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
              Enter a new password for {resetTarget?.username}.
            </DialogContentText>
            <TextField
              autoFocus
              fullWidth
              label="New Password"
              type="password"
              variant="outlined"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              sx={{ ...DIALOG_INPUT_SX, mt: 1.5 }}
            />
          </DialogContent>
          <DialogActions sx={DIALOG_ACTIONS_SX}>
            <Button onClick={() => { setResetOpen(false); setResetTarget(null); }} sx={DIALOG_BUTTON_SX}>Cancel</Button>
            <Button onClick={doResetPassword} sx={DIALOG_PRIMARY_BUTTON_SX}>Save Password</Button>
          </DialogActions>
        </Dialog>

        <Dialog open={createOpen} onClose={() => setCreateOpen(false)} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
          <DialogTitle sx={DIALOG_TITLE_SX}>
            <DialogHeaderBlock
              title="Create User"
              subtitle="Add a new Borealis operator account and assign an initial role."
            />
          </DialogTitle>
          <DialogContent sx={DIALOG_CONTENT_SX}>
            <TextField
              autoFocus
              fullWidth
              label="Username"
              variant="outlined"
              value={createForm.username}
              onChange={(e) => setCreateForm((p) => ({ ...p, username: e.target.value }))}
              sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
            />
            <TextField
              fullWidth
              label="Display Name (optional)"
              variant="outlined"
              value={createForm.display_name}
              onChange={(e) => setCreateForm((p) => ({ ...p, display_name: e.target.value }))}
              sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
            />
            <TextField
              fullWidth
              label="Password"
              type="password"
              variant="outlined"
              value={createForm.password}
              onChange={(e) => setCreateForm((p) => ({ ...p, password: e.target.value }))}
              sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
            />
            <TextField
              select
              fullWidth
              label="Role"
              value={createForm.role}
              onChange={(e) => setCreateForm((p) => ({ ...p, role: e.target.value }))}
              sx={{ ...DIALOG_SELECT_SX, mt: 2.1 }}
            >
              <MenuItem value="User">User</MenuItem>
              <MenuItem value="Admin">Admin</MenuItem>
            </TextField>
          </DialogContent>
          <DialogActions sx={DIALOG_ACTIONS_SX}>
            <Button onClick={() => setCreateOpen(false)} sx={DIALOG_BUTTON_SX}>Cancel</Button>
            <Button onClick={doCreate} sx={DIALOG_PRIMARY_BUTTON_SX}>Create</Button>
          </DialogActions>
        </Dialog>
      </Paper>

      <ConfirmDeleteDialog
        open={confirmDeleteOpen}
        title="Delete User"
        message={`Are you sure you want to delete user '${deleteTarget?.username || ""}'?`}
        onCancel={() => setConfirmDeleteOpen(false)}
        onConfirm={doDelete}
        confirmLabel="Delete User"
      />
      <ConfirmDeleteDialog
        open={confirmChangeRoleOpen}
        title="Change User Role"
        message={changeRoleTarget ? `Change role for '${changeRoleTarget.username}' to ${changeRoleNext}?` : ""}
        onCancel={() => setConfirmChangeRoleOpen(false)}
        onConfirm={doChangeRole}
        confirmLabel="Change Role"
        confirmTone="primary"
      />
      <ConfirmDeleteDialog
        open={confirmMfaStateOpen}
        title={confirmMfaStateNextEnabled ? "Enable MFA" : "Disable MFA"}
        message={
          confirmMfaStateTarget
            ? confirmMfaStateNextEnabled
              ? `Require MFA for '${confirmMfaStateTarget.username}'? If they do not already have an authenticator app or passkey configured, Borealis will require MFA setup on their next login.`
              : `Disable MFA for '${confirmMfaStateTarget.username}'? They will be able to sign in without MFA until an administrator enables it again.`
            : ""
        }
        onCancel={() => {
          setConfirmMfaStateOpen(false);
          setConfirmMfaStateTarget(null);
          setConfirmMfaStateNextEnabled(null);
        }}
        onConfirm={doChangeMfaState}
        confirmLabel={confirmMfaStateNextEnabled ? "Enable MFA" : "Disable MFA"}
        confirmTone={confirmMfaStateNextEnabled ? "primary" : "danger"}
      />
      <ConfirmDeleteDialog
        open={resetMfaOpen}
        title="Reset MFA"
        message={resetMfaTarget ? `Reset MFA enrollment for '${resetMfaTarget.username}'? This clears their existing authenticator app secret and passkeys.` : ""}
        onCancel={() => { setResetMfaOpen(false); setResetMfaTarget(null); }}
        onConfirm={doResetMfa}
        confirmLabel="Reset MFA"
        confirmTone="primary"
      />
      <ConfirmDeleteDialog
        open={warnOpen}
        title="Attention"
        message={warnMessage}
        onCancel={() => setWarnOpen(false)}
        onConfirm={() => setWarnOpen(false)}
        confirmLabel="OK"
        confirmTone="primary"
      />
    </>
  );
}
