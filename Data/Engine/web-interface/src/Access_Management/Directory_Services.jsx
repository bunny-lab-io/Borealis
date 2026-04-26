import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  Menu,
  MenuItem,
  Paper,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import FileDownloadRoundedIcon from "@mui/icons-material/FileDownloadRounded";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import RefreshIcon from "@mui/icons-material/Refresh";
import SyncIcon from "@mui/icons-material/Sync";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import PageBodyFrame from "../PageBodyFrame.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_SELECT_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../app/providers/AuthContext.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Directory Services";
const PAGE_SUBTITLE = "Manage LDAP, LDAPS, and Active Directory credential providers.";

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

const emptyForm = {
  name: "",
  provider_type: "ldap",
  enabled: false,
  priority: 100,
  domain_suffix: "",
  server_urls: "",
  host_overrides_text: "",
  use_ldaps: true,
  tls_required: true,
  tls_ca_pem: "",
  base_dn: "",
  bind_dn: "",
  bind_password: "",
  user_search_filter: "",
  username_attribute: "",
  display_name_attribute: "displayName",
  email_attribute: "mail",
  member_of_attribute: "memberOf",
  group_search_base_dn: "",
  nested_groups: false,
  kerberos_realm: "",
  kerberos_kdc: "",
  kerberos_keytab_base64: "",
  group_mappings_text: "",
};

function formatTs(ts) {
  if (!ts) return "-";
  const date = new Date(Number(ts) * 1000);
  if (Number.isNaN(date.getTime())) return "-";
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function providerTypeLabel(value) {
  return String(value || "").toLowerCase() === "active_directory" ? "Active Directory" : "LDAP";
}

function mappingsToText(mappings = []) {
  return mappings
    .map((item) => `${item.role || "User"}|${item.group_dn || ""}`)
    .filter((line) => line.trim() !== "User|")
    .join("\n");
}

function textToMappings(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const separator = line.indexOf("|");
      if (separator < 0) return { role: "User", group_dn: line };
      return {
        role: line.slice(0, separator).trim() || "User",
        group_dn: line.slice(separator + 1).trim(),
      };
    })
    .filter((item) => item.group_dn);
}

function splitServerUrls(value) {
  return String(value || "")
    .split(/\r?\n|,/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function hostOverridesToText(overrides = {}) {
  return Object.entries(overrides || {})
    .map(([host, connectHost]) => `${host}=${connectHost}`)
    .join("\n");
}

function textToHostOverrides(value) {
  return String(value || "")
    .split(/\r?\n|,/)
    .map((line) => line.trim())
    .filter(Boolean)
    .reduce((result, line) => {
      let host = "";
      let connectHost = "";
      if (line.includes("=")) {
        [host, connectHost] = line.split("=", 2);
      } else if (line.includes("|")) {
        [host, connectHost] = line.split("|", 2);
      } else {
        const parts = line.split(/\s+/);
        if (parts.length === 2) [host, connectHost] = parts;
      }
      host = String(host || "").trim().toLowerCase();
      connectHost = String(connectHost || "").trim();
      if (host && connectHost) result[host] = connectHost;
      return result;
    }, {});
}

function formFromProvider(provider) {
  if (!provider) return { ...emptyForm };
  return {
    ...emptyForm,
    ...provider,
    server_urls: Array.isArray(provider.server_urls) ? provider.server_urls.join("\n") : "",
    host_overrides_text: hostOverridesToText(provider.host_overrides || {}),
    bind_password: "",
    kerberos_keytab_base64: "",
    tls_ca_pem: provider.tls_ca_pem || "",
    group_mappings_text: mappingsToText(provider.group_mappings || []),
  };
}

function formatCertificateDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function statusChip(status, message) {
  const normalized = String(status || "").toLowerCase();
  const ok = normalized === "ok";
  const color = ok ? "#00d18c" : normalized ? "#ffb347" : "#94a3b8";
  const Icon = ok ? CheckCircleRoundedIcon : ErrorOutlineRoundedIcon;
  const label = ok ? "OK" : normalized ? "Needs Attention" : "Untested";
  return (
    <Box
      title={message || label}
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.5,
        color,
        fontWeight: 700,
        fontFamily: gridFontFamily,
      }}
    >
      <Icon sx={{ fontSize: 16 }} />
      {label}
    </Box>
  );
}

export default function DirectoryServices() {
  const { isAdmin } = useAuth();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuRow, setMenuRow] = useState(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState(null);
  const [form, setForm] = useState(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [certificateReview, setCertificateReview] = useState(null);
  const [certificateBusy, setCertificateBusy] = useState(false);
  const [busyId, setBusyId] = useState(null);
  const gridRef = useRef(null);
  const sendNotification = useAppNotifications({
    title: PAGE_TITLE,
    icon: "account_tree",
    variant: "info",
  });

  const fetchProviders = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await fetch("/api/directory/providers", { credentials: "include" });
      const data = await resp.json();
      setRows(Array.isArray(data?.providers) ? data.providers : []);
    } catch {
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (isAdmin) fetchProviders();
  }, [fetchProviders, isAdmin]);

  const openMenu = useCallback((event, row) => {
    setMenuAnchor(event.currentTarget);
    setMenuRow(row);
  }, []);

  const closeMenu = useCallback(() => {
    setMenuAnchor(null);
    setMenuRow(null);
  }, []);

  const openCreate = useCallback(() => {
    setEditingProvider(null);
    setForm({ ...emptyForm });
    setEditorOpen(true);
  }, []);

  const openEdit = useCallback((provider) => {
    setEditingProvider(provider);
    setForm(formFromProvider(provider));
    setEditorOpen(true);
    closeMenu();
  }, [closeMenu]);

  const updateForm = useCallback((key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  }, []);

  const providerPayload = useCallback(() => {
    const payload = {
      name: form.name,
      provider_type: form.provider_type,
      enabled: Boolean(form.enabled),
      priority: Number(form.priority || 100),
      domain_suffix: form.domain_suffix,
      server_urls: splitServerUrls(form.server_urls),
      host_overrides: textToHostOverrides(form.host_overrides_text),
      use_ldaps: Boolean(form.use_ldaps),
      tls_required: Boolean(form.tls_required),
      base_dn: form.base_dn,
      bind_dn: form.bind_dn,
      user_search_filter: form.user_search_filter,
      username_attribute: form.username_attribute,
      display_name_attribute: form.display_name_attribute,
      email_attribute: form.email_attribute,
      member_of_attribute: form.member_of_attribute,
      group_search_base_dn: form.group_search_base_dn,
      nested_groups: Boolean(form.nested_groups),
      kerberos_realm: form.kerberos_realm,
      kerberos_kdc: form.kerberos_kdc,
      group_mappings: textToMappings(form.group_mappings_text),
    };
    if (!editingProvider?.tls_ca_pem_present || form.tls_ca_pem) payload.tls_ca_pem = form.tls_ca_pem;
    if (!editingProvider?.bind_password_present || form.bind_password) payload.bind_password = form.bind_password;
    if (!editingProvider?.kerberos_keytab_present || form.kerberos_keytab_base64) payload.kerberos_keytab_base64 = form.kerberos_keytab_base64;
    return payload;
  }, [editingProvider, form]);

  const downloadCertificate = useCallback(async () => {
    const serverUrls = splitServerUrls(form.server_urls);
    if (!serverUrls.length) {
      sendNotification("LDAP server URL required");
      return;
    }
    setCertificateBusy(true);
    try {
      const resp = await fetch("/api/directory/providers/certificate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          server_urls: serverUrls,
          host_overrides: textToHostOverrides(form.host_overrides_text),
          use_ldaps: Boolean(form.use_ldaps),
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        sendNotification(data?.message || data?.error || "Certificate download failed");
        return;
      }
      setCertificateReview(data?.certificate || null);
    } finally {
      setCertificateBusy(false);
    }
  }, [form.host_overrides_text, form.server_urls, form.use_ldaps, sendNotification]);

  const trustCertificate = useCallback(() => {
    if (!certificateReview?.pem) return;
    setForm((prev) => ({
      ...prev,
      use_ldaps: true,
      tls_required: true,
      tls_ca_pem: certificateReview.pem,
    }));
    setCertificateReview(null);
    sendNotification("LDAP certificate trusted");
  }, [certificateReview, sendNotification]);

  const saveProvider = useCallback(async () => {
    const payload = providerPayload();
    const editingId = editingProvider?.id;
    const resp = await fetch(
      editingId ? `/api/directory/providers/${editingId}` : "/api/directory/providers",
      {
        method: editingId ? "PATCH" : "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(payload),
      }
    );
    const data = await resp.json();
    if (!resp.ok) {
      sendNotification(data?.message || data?.error || "Failed to save directory provider");
      return;
    }
    setEditorOpen(false);
    setEditingProvider(null);
    await fetchProviders();
    sendNotification(`Directory provider ${payload.name} saved`);
  }, [editingProvider, fetchProviders, providerPayload, sendNotification]);

  const callProviderAction = useCallback(async (provider, action, options = {}) => {
    if (!provider?.id) return;
    setBusyId(provider.id);
    closeMenu();
    try {
      const resp = await fetch(`/api/directory/providers/${provider.id}/${action}`, {
        method: "POST",
        credentials: "include",
      });
      const data = await resp.json().catch(() => ({}));
      await fetchProviders();
      if (!resp.ok) {
        sendNotification(data?.message || data?.error || `Directory ${action} failed`);
        return;
      }
      sendNotification(options.success || data?.message || `Directory ${action} completed`);
    } finally {
      setBusyId(null);
    }
  }, [closeMenu, fetchProviders, sendNotification]);

  const toggleEnabled = useCallback(async (provider) => {
    if (!provider?.id) return;
    setBusyId(provider.id);
    closeMenu();
    try {
      const resp = await fetch(`/api/directory/providers/${provider.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ enabled: !provider.enabled }),
      });
      const data = await resp.json().catch(() => ({}));
      await fetchProviders();
      if (!resp.ok) {
        sendNotification(data?.message || data?.error || "Failed to update provider state");
        return;
      }
      sendNotification(`${provider.name} ${provider.enabled ? "disabled" : "enabled"}`);
    } finally {
      setBusyId(null);
    }
  }, [closeMenu, fetchProviders, sendNotification]);

  const deleteProvider = useCallback(async () => {
    const provider = deleteTarget;
    setDeleteTarget(null);
    if (!provider?.id) return;
    const resp = await fetch(`/api/directory/providers/${provider.id}`, {
      method: "DELETE",
      credentials: "include",
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      sendNotification(data?.error || "Failed to delete provider");
      return;
    }
    await fetchProviders();
    sendNotification(`Directory provider ${provider.name} deleted`);
  }, [deleteTarget, fetchProviders, sendNotification]);

  const columnDefs = useMemo(() => [
    {
      headerName: "Provider",
      field: "name",
      minWidth: 220,
      cellRenderer: (params) => (
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.75, fontFamily: gridFontFamily }}>
          <AccountTreeRoundedIcon sx={{ fontSize: 17, color: "#7dd3fc" }} />
          <Box component="span">{params.value}</Box>
        </Box>
      ),
    },
    {
      headerName: "Type",
      field: "provider_type",
      minWidth: 160,
      valueFormatter: (params) => providerTypeLabel(params.value),
    },
    {
      headerName: "Enabled",
      field: "enabled",
      minWidth: 120,
      valueGetter: (params) => (params.data?.enabled ? "Enabled" : "Disabled"),
    },
    {
      headerName: "Priority",
      field: "priority",
      minWidth: 110,
      comparator: (a, b) => Number(a || 0) - Number(b || 0),
    },
    {
      headerName: "Domain",
      field: "domain_suffix",
      minWidth: 180,
      valueFormatter: (params) => params.value || "-",
    },
    {
      headerName: "Servers",
      field: "server_urls",
      minWidth: 260,
      valueFormatter: (params) => (params.value || []).join(", ") || "-",
    },
    {
      headerName: "Test",
      field: "last_test_status",
      minWidth: 160,
      cellRenderer: (params) => statusChip(params.data?.last_test_status, params.data?.last_test_message),
    },
    {
      headerName: "Last Sync",
      field: "last_sync_at",
      minWidth: 180,
      valueFormatter: (params) => formatTs(params.value),
    },
    {
      headerName: "Actions",
      field: "actions",
      minWidth: 110,
      flex: 1,
      sortable: false,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      cellRenderer: (params) => (
        <Box sx={{ display: "flex", justifyContent: "flex-end", width: "100%" }}>
          <IconButton
            size="small"
            disabled={busyId === params.data?.id}
            onClick={(event) => {
              event.stopPropagation();
              openMenu(event, params.data);
            }}
            sx={{ color: "#cbd5e1" }}
          >
            <MoreVertIcon fontSize="inherit" />
          </IconButton>
        </Box>
      ),
    },
  ], [busyId, openMenu]);

  const pageHeaderActions = useMemo(() => [
    {
      id: "directory-refresh",
      label: "Refresh",
      icon: <RefreshIcon />,
      tone: "secondary",
      disabled: loading,
      onClick: fetchProviders,
    },
    {
      id: "directory-create",
      label: "New Provider",
      icon: <AddIcon />,
      tone: "primary",
      onClick: openCreate,
    },
  ], [fetchProviders, loading, openCreate]);

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: AccountTreeRoundedIcon,
    actions: pageHeaderActions,
  });

  if (!isAdmin) return null;

  return (
    <>
      <Paper
        elevation={0}
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
      >
        <PageBodyFrame
          variant="grid_with_stack"
          stack={
            <Stack direction={{ xs: "column", md: "row" }} spacing={1.5} alignItems={{ xs: "stretch", md: "center" }}>
              <Button variant="outlined" startIcon={<RefreshIcon />} onClick={fetchProviders} disabled={loading}>
                Refresh
              </Button>
              <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
                New Provider
              </Button>
            </Stack>
          }
        >
          <Box className={themeClassName} sx={{ flex: 1, minHeight: 0, width: "100%" }}>
            <AgGridReact
              ref={gridRef}
              rowData={rows}
              columnDefs={columnDefs}
              defaultColDef={{
                sortable: true,
                filter: "agTextColumnFilter",
                resizable: true,
                minWidth: 120,
              }}
              loading={loading}
              suppressCellFocus
              suppressRowClickSelection
              animateRows
            />
          </Box>
        </PageBodyFrame>
      </Paper>

      <Menu anchorEl={menuAnchor} open={Boolean(menuAnchor)} onClose={closeMenu}>
        <MenuItem onClick={() => openEdit(menuRow)}>Edit</MenuItem>
        <MenuItem onClick={() => callProviderAction(menuRow, "test", { success: `${menuRow?.name || "Provider"} test passed` })}>
          Test
        </MenuItem>
        <MenuItem onClick={() => callProviderAction(menuRow, "sync")}>
          <SyncIcon fontSize="small" sx={{ mr: 1 }} />
          Sync Cached Users
        </MenuItem>
        <MenuItem onClick={() => toggleEnabled(menuRow)}>
          {menuRow?.enabled ? "Disable" : "Enable"}
        </MenuItem>
        <MenuItem
          onClick={() => {
            setDeleteTarget(menuRow);
            closeMenu();
          }}
          sx={{ color: "#ff8080" }}
        >
          Delete
        </MenuItem>
      </Menu>

      <Dialog
        open={editorOpen}
        onClose={() => setEditorOpen(false)}
        maxWidth="md"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={editingProvider ? "Edit Directory Provider" : "New Directory Provider"}
            subtitle="Saved providers must pass test before enablement."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(12, 1fr)" }, gap: 2 }}>
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Name" value={form.name} onChange={(e) => updateForm("name", e.target.value)} />
            <TextField select sx={{ ...DIALOG_SELECT_SX, gridColumn: { md: "span 3" } }} label="Type" value={form.provider_type} onChange={(e) => updateForm("provider_type", e.target.value)}>
              <MenuItem value="ldap">LDAP / LDAPS</MenuItem>
              <MenuItem value="active_directory">Active Directory</MenuItem>
            </TextField>
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 3" } }} label="Priority" type="number" value={form.priority} onChange={(e) => updateForm("priority", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="Domain Suffix" value={form.domain_suffix} onChange={(e) => updateForm("domain_suffix", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 8" } }} label="Server URLs" multiline minRows={2} value={form.server_urls} onChange={(e) => updateForm("server_urls", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="Host Overrides" multiline minRows={2} value={form.host_overrides_text} onChange={(e) => updateForm("host_overrides_text", e.target.value)} />
            <FormControlLabel sx={{ gridColumn: { md: "span 3" } }} control={<Checkbox checked={Boolean(form.use_ldaps)} onChange={(e) => updateForm("use_ldaps", e.target.checked)} />} label="Use LDAPS" />
            <FormControlLabel sx={{ gridColumn: { md: "span 3" } }} control={<Checkbox checked={Boolean(form.tls_required)} onChange={(e) => updateForm("tls_required", e.target.checked)} />} label="Strict TLS" />
            <FormControlLabel sx={{ gridColumn: { md: "span 3" } }} control={<Checkbox checked={Boolean(form.nested_groups)} onChange={(e) => updateForm("nested_groups", e.target.checked)} />} label="Nested Groups" />
            <FormControlLabel sx={{ gridColumn: { md: "span 3" } }} control={<Checkbox checked={Boolean(form.enabled)} onChange={(e) => updateForm("enabled", e.target.checked)} />} label="Enabled" />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Base DN" value={form.base_dn} onChange={(e) => updateForm("base_dn", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Group Search Base DN" value={form.group_search_base_dn} onChange={(e) => updateForm("group_search_base_dn", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Bind DN" value={form.bind_dn} onChange={(e) => updateForm("bind_dn", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label={editingProvider?.bind_password_present ? "Bind Password (leave blank to keep)" : "Bind Password"} type="password" value={form.bind_password} onChange={(e) => updateForm("bind_password", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="Username Attribute" value={form.username_attribute} onChange={(e) => updateForm("username_attribute", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="Display Attribute" value={form.display_name_attribute} onChange={(e) => updateForm("display_name_attribute", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="MemberOf Attribute" value={form.member_of_attribute} onChange={(e) => updateForm("member_of_attribute", e.target.value)} />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="User Search Filter" value={form.user_search_filter} onChange={(e) => updateForm("user_search_filter", e.target.value)} />
            {form.provider_type === "active_directory" ? (
              <>
                <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Kerberos Realm" value={form.kerberos_realm} onChange={(e) => updateForm("kerberos_realm", e.target.value)} />
                <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="KDC Host" value={form.kerberos_kdc} onChange={(e) => updateForm("kerberos_kdc", e.target.value)} />
                <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label={editingProvider?.kerberos_keytab_present ? "Keytab Base64 (leave blank to keep)" : "Keytab Base64"} multiline minRows={2} value={form.kerberos_keytab_base64} onChange={(e) => updateForm("kerberos_keytab_base64", e.target.value)} />
              </>
            ) : null}
            <Button
              sx={{ ...DIALOG_BUTTON_SX, gridColumn: { md: "span 12" }, justifySelf: "flex-start" }}
              startIcon={<FileDownloadRoundedIcon />}
              disabled={certificateBusy || !form.use_ldaps || splitServerUrls(form.server_urls).length === 0}
              onClick={downloadCertificate}
            >
              Download Certificate from LDAP Server
            </Button>
            <TextField
              sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }}
              label={editingProvider?.tls_ca_pem_present ? "CA / Pinned Certificate PEM (leave blank to keep)" : "CA / Pinned Certificate PEM"}
              multiline
              minRows={3}
              value={form.tls_ca_pem}
              onChange={(e) => updateForm("tls_ca_pem", e.target.value)}
            />
            <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="Group Mappings" multiline minRows={4} value={form.group_mappings_text} onChange={(e) => updateForm("group_mappings_text", e.target.value)} />
            <Typography sx={{ gridColumn: "1 / -1", color: "#9aa3ad", fontSize: 13 }}>
              Group mappings use one entry per line: Admin|CN=Borealis Admins,DC=example,DC=com or User|CN=Borealis Users,DC=example,DC=com.
            </Typography>
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setEditorOpen(false)}>Cancel</Button>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} onClick={saveProvider}>Save</Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={Boolean(certificateReview)}
        onClose={() => setCertificateReview(null)}
        maxWidth="sm"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Trust LDAP Certificate"
            subtitle={certificateReview?.server_url || ""}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box sx={{ display: "grid", gridTemplateColumns: "minmax(120px, 0.35fr) minmax(0, 1fr)", gap: 1.25, color: "#dbeafe" }}>
            {[
              ["Subject", certificateReview?.subject],
              ["Issuer", certificateReview?.issuer],
              ["Common Name", certificateReview?.common_name],
              ["Connected To", certificateReview?.connect_host],
              ["DNS Names", (certificateReview?.dns_names || []).join(", ")],
              ["IP Addresses", (certificateReview?.ip_addresses || []).join(", ")],
              ["Serial", certificateReview?.serial_number],
              ["Valid From", formatCertificateDate(certificateReview?.not_before)],
              ["Valid Until", formatCertificateDate(certificateReview?.not_after)],
              ["SHA-256", certificateReview?.sha256_fingerprint],
            ].map(([label, value]) => (
              <React.Fragment key={label}>
                <Typography sx={{ color: "#94a3b8", fontSize: 13 }}>{label}</Typography>
                <Typography sx={{ color: "#f8fafc", fontSize: 13, overflowWrap: "anywhere" }}>{value || "-"}</Typography>
              </React.Fragment>
            ))}
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setCertificateReview(null)}>Deny</Button>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} onClick={trustCertificate}>Trust Certificate</Button>
        </DialogActions>
      </Dialog>

      <ConfirmDeleteDialog
        open={Boolean(deleteTarget)}
        title="Delete Directory Provider"
        message={`Delete ${deleteTarget?.name || "this provider"}? Cached directory users must be disabled or removed first.`}
        confirmLabel="Delete Provider"
        onCancel={() => setDeleteTarget(null)}
        onConfirm={deleteProvider}
      />
    </>
  );
}
