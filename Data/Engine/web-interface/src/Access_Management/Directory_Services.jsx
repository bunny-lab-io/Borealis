import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControlLabel,
  Menu,
  MenuItem,
  Paper,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import DeleteRoundedIcon from "@mui/icons-material/DeleteRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import FileDownloadRoundedIcon from "@mui/icons-material/FileDownloadRounded";
import RefreshIcon from "@mui/icons-material/Refresh";
import SearchRoundedIcon from "@mui/icons-material/SearchRounded";
import SettingsEthernetRoundedIcon from "@mui/icons-material/SettingsEthernetRounded";
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
const PAGE_SUBTITLE = "Connect various credential providers such as LDAP to provide authentication to the Engine.";

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
const NAV_TAB_HEIGHT = 32;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};
const DIALOG_TABS_SX = {
  borderBottom: "1px solid rgba(148,163,184,0.32)",
  minHeight: NAV_TAB_HEIGHT,
  height: NAV_TAB_HEIGHT,
  mb: 2,
  "& .MuiTabs-flexContainer": {
    minHeight: NAV_TAB_HEIGHT,
    height: NAV_TAB_HEIGHT,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    textTransform: "none",
    fontWeight: 400,
    fontFamily: "inherit",
    fontSize: "0.8rem",
    minHeight: NAV_TAB_HEIGHT,
    height: NAV_TAB_HEIGHT,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.icon,
    },
    "&:hover": {
      background: NAV_TAB_COLORS.hover,
    },
    "&:active": {
      transform: "translateY(0.5px)",
    },
  },
  "& .MuiTab-root.Mui-selected": {
    color: NAV_TAB_COLORS.textActive,
    fontWeight: 600,
    background: NAV_TAB_COLORS.activeBg,
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.iconActive,
    },
    "&:hover": {
      background: NAV_TAB_COLORS.activeBg,
    },
  },
};
const DIALOG_TAB_DEFS = [
  { key: "basic", label: "Basic", icon: SettingsEthernetRoundedIcon },
  { key: "mapping", label: "User / Group Mapping", icon: AccountTreeRoundedIcon },
  { key: "diagnostics", label: "Diagnostics", icon: SearchRoundedIcon },
];
const DIALOG_CARD_SX = {
  gridColumn: "1 / -1",
  border: "1px solid rgba(148,163,184,0.25)",
  borderRadius: 2,
  background: "rgba(2,6,23,0.35)",
  p: 1.5,
  display: "grid",
  gridTemplateColumns: { xs: "1fr", md: "repeat(12, 1fr)" },
  gap: 1.5,
};
const CARD_HEADER_SX = {
  gridColumn: "1 / -1",
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: 1.5,
  flexWrap: "wrap",
};
const ACTION_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: "1px solid rgba(148,163,184,0.32)",
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};
const ACTION_MENU_ITEM_SX = {
  minHeight: 42,
  borderRadius: 1.6,
  color: "#e2e8f0",
  alignItems: "center",
  px: 1,
  py: 0.85,
  position: "relative",
  overflow: "hidden",
  "&:hover": {
    backgroundColor: "rgba(88,166,255,0.12)",
  },
  "&::before": {
    content: '""',
    position: "absolute",
    left: 0,
    top: 8,
    bottom: 8,
    width: 3,
    borderRadius: 999,
    background: "transparent",
    transition: "background-color 0.16s ease",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.58)",
  },
};
const ACTION_MENU_DANGER_ITEM_SX = {
  ...ACTION_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};
const ACTION_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};
const ACTION_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};
const ACTION_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};
const ACTION_MENU_HEADER_ICON_SX = {
  width: 32,
  height: 32,
  borderRadius: 1.35,
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  border: "1px solid rgba(148,163,184,0.14)",
  background: "rgba(255,255,255,0.04)",
  color: "#8fd3ff",
};
const ACTION_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};
const ACTION_MENU_LABEL_SX = {
  color: "#e2e8f0",
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};
const ACTION_MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};
const ACTION_MENU_TITLE_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};
const ACTION_MENU_GROUP_LABELS = {
  primary: "Primary",
  organize: "Organize",
  danger: "Danger Zone",
};
const ACTION_MENU_GROUP_ORDER = ["primary", "organize", "danger"];

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
  username_attribute: "sAMAccountName",
  display_name_attribute: "displayName",
  email_attribute: "mail",
  member_of_attribute: "memberOf",
  group_search_base_dn: "",
  nested_groups: true,
  kerberos_realm: "",
  kerberos_kdc: "",
  kerberos_keytab_base64: "",
  admin_group_dns_text: "",
  user_group_dns_text: "",
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

function linesToDnsList(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function mappingsToRoleText(mappings = [], role) {
  const targetRole = String(role || "").toLowerCase();
  return (mappings || [])
    .filter((item) => String(item?.role || "").toLowerCase() === targetRole)
    .map((item) => String(item?.group_dn || "").trim())
    .filter(Boolean)
    .join("\n");
}

function roleDnsToMappings(adminDnsText, userDnsText) {
  return [
    ...linesToDnsList(adminDnsText).map((group_dn) => ({ role: "Admin", group_dn })),
    ...linesToDnsList(userDnsText).map((group_dn) => ({ role: "User", group_dn })),
  ];
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
    username_attribute: provider.username_attribute || emptyForm.username_attribute,
    display_name_attribute: provider.display_name_attribute || emptyForm.display_name_attribute,
    member_of_attribute: provider.member_of_attribute || emptyForm.member_of_attribute,
    admin_group_dns_text: mappingsToRoleText(provider.group_mappings || [], "Admin"),
    user_group_dns_text: mappingsToRoleText(provider.group_mappings || [], "User"),
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

function FormTooltip({ title, children }) {
  if (!title) return children;
  return (
    <Tooltip title={title} arrow placement="top" describeChild>
      {children}
    </Tooltip>
  );
}

function SectionCard({ title, subtitle, action, children }) {
  return (
    <Box sx={DIALOG_CARD_SX}>
      <Box sx={CARD_HEADER_SX}>
        <Box>
          <Typography sx={{ color: "#e2e8f0", fontSize: 14, fontWeight: 700 }}>{title}</Typography>
          {subtitle ? <Typography sx={{ color: "#94a3b8", fontSize: 13 }}>{subtitle}</Typography> : null}
        </Box>
        {action || null}
      </Box>
      {children}
    </Box>
  );
}

export default function DirectoryServices() {
  const { isAdmin } = useAuth();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [contextMenu, setContextMenu] = useState(null);
  const [menuRow, setMenuRow] = useState(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState(null);
  const [form, setForm] = useState(emptyForm);
  const [dialogTab, setDialogTab] = useState("basic");
  const [certificateExpanded, setCertificateExpanded] = useState(false);
  const [dnsOverridesExpanded, setDnsOverridesExpanded] = useState(false);
  const [basicAdvancedExpanded, setBasicAdvancedExpanded] = useState(false);
  const [ldapAdvancedExpanded, setLdapAdvancedExpanded] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [certificateReview, setCertificateReview] = useState(null);
  const [certificateBusy, setCertificateBusy] = useState(false);
  const [lookupUsername, setLookupUsername] = useState("");
  const [lookupPassword, setLookupPassword] = useState("");
  const [lookupResult, setLookupResult] = useState(null);
  const [lookupBusy, setLookupBusy] = useState(false);
  const [accessPreview, setAccessPreview] = useState(null);
  const [accessPreviewBusyRole, setAccessPreviewBusyRole] = useState("");
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

  const openContextMenu = useCallback((event, row, node = null) => {
    const mouseEvent = event?.event || event;
    mouseEvent?.preventDefault?.();
    mouseEvent?.stopPropagation?.();
    if (!row) return;
    try {
      node?.setSelected?.(true, true);
    } catch {}
    setMenuRow(row);
    setContextMenu({
      top: Number(mouseEvent?.clientY || 0),
      left: Number(mouseEvent?.clientX || 0),
    });
  }, []);

  const closeMenu = useCallback(() => {
    setContextMenu(null);
    setMenuRow(null);
  }, []);

  const openCreate = useCallback(() => {
    setEditingProvider(null);
    setForm({ ...emptyForm });
    setDialogTab("basic");
    setCertificateExpanded(false);
    setDnsOverridesExpanded(false);
    setBasicAdvancedExpanded(false);
    setLdapAdvancedExpanded(false);
    setLookupUsername("");
    setLookupPassword("");
    setLookupResult(null);
    setAccessPreview(null);
    setEditorOpen(true);
  }, []);

  const openEdit = useCallback((provider) => {
    setEditingProvider(provider);
    setForm(formFromProvider(provider));
    setDialogTab("basic");
    setCertificateExpanded(false);
    setDnsOverridesExpanded(Boolean(provider?.host_overrides && Object.keys(provider.host_overrides || {}).length));
    setBasicAdvancedExpanded(false);
    setLdapAdvancedExpanded(false);
    setLookupUsername("");
    setLookupPassword("");
    setLookupResult(null);
    setAccessPreview(null);
    setEditorOpen(true);
    closeMenu();
  }, [closeMenu]);

  const updateForm = useCallback((key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  }, []);

  const updateConnectionSecurity = useCallback((value) => {
    const useLdaps = value === "ldaps";
    setForm((prev) => ({
      ...prev,
      use_ldaps: useLdaps,
      tls_required: useLdaps,
    }));
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
      group_mappings: roleDnsToMappings(form.admin_group_dns_text, form.user_group_dns_text),
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

  const lookupDirectoryUser = useCallback(async () => {
    if (!editingProvider?.id || !lookupUsername.trim()) return;
    setLookupBusy(true);
    try {
      const resp = await fetch(`/api/directory/providers/${editingProvider.id}/lookup-user`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ username: lookupUsername.trim(), password: lookupPassword }),
      });
      const data = await resp.json().catch(() => ({}));
      setLookupResult({ ...data, ok: resp.ok });
      if (!resp.ok) sendNotification(data?.message || data?.error || "Directory user lookup failed");
    } finally {
      setLookupBusy(false);
    }
  }, [editingProvider, lookupPassword, lookupUsername, sendNotification]);

  const previewEffectiveAccess = useCallback(async (role) => {
    if (!editingProvider?.id) {
      sendNotification("Save provider before previewing effective access");
      return;
    }
    const normalizedRole = role === "Admin" ? "Admin" : "User";
    const groupDns = linesToDnsList(normalizedRole === "Admin" ? form.admin_group_dns_text : form.user_group_dns_text);
    if (!groupDns.length) {
      sendNotification(`${normalizedRole} group DN required`);
      return;
    }
    setAccessPreviewBusyRole(normalizedRole);
    try {
      const resp = await fetch(`/api/directory/providers/${editingProvider.id}/effective-access`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ role: normalizedRole, group_dns: groupDns }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        sendNotification(data?.message || data?.error || "Effective access preview failed");
        return;
      }
      setAccessPreview(data);
    } finally {
      setAccessPreviewBusyRole("");
    }
  }, [editingProvider, form.admin_group_dns_text, form.user_group_dns_text, sendNotification]);

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
      headerName: "Credential Provider",
      field: "name",
      minWidth: 220,
      flex: 1,
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
      headerName: "Connection Status",
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
  ], []);

  const contextMenuActions = useMemo(() => {
    const row = menuRow;
    const busy = Boolean(row?.id && busyId === row.id);
    return [
      {
        id: "edit",
        label: "Edit",
        description: "Open provider configuration.",
        icon: <EditRoundedIcon sx={ACTION_MENU_ROW_ICON_SX} />,
        group: "primary",
        disabled: busy,
        disabledReason: "Provider action already running.",
        onClick: () => openEdit(row),
      },
      {
        id: "test",
        label: "Test",
        description: "Verify provider connectivity and bind settings.",
        icon: <CheckCircleRoundedIcon sx={ACTION_MENU_ROW_ICON_SX} />,
        group: "primary",
        disabled: busy,
        disabledReason: "Provider action already running.",
        onClick: () => callProviderAction(row, "test", { success: `${row?.name || "Provider"} test passed` }),
      },
      {
        id: "sync",
        label: "Sync Cached Users",
        description: "Refresh directory-backed user cache state.",
        icon: <SyncIcon sx={ACTION_MENU_ROW_ICON_SX} />,
        group: "primary",
        disabled: busy,
        disabledReason: "Provider action already running.",
        onClick: () => callProviderAction(row, "sync"),
      },
      {
        id: "toggle-enabled",
        label: row?.enabled ? "Disable" : "Enable",
        description: row?.enabled ? "Stop using this provider for authentication." : "Allow this provider to authenticate users.",
        icon: <SettingsEthernetRoundedIcon sx={ACTION_MENU_ROW_ICON_SX} />,
        group: "organize",
        disabled: busy,
        disabledReason: "Provider action already running.",
        onClick: () => toggleEnabled(row),
      },
      {
        id: "delete",
        label: "Delete",
        description: "Remove this directory provider.",
        icon: <DeleteRoundedIcon sx={ACTION_MENU_ROW_ICON_SX} />,
        group: "danger",
        disabled: busy,
        disabledReason: "Provider action already running.",
        onClick: () => {
          const target = row;
          closeMenu();
          setDeleteTarget(target);
        },
      },
    ];
  }, [busyId, callProviderAction, closeMenu, menuRow, openEdit, toggleEnabled]);

  const renderContextMenuItems = useCallback(
    (closeMenuHandler) => {
      const providerName = menuRow?.name || "Directory Provider";
      const providerSubtitle = [providerTypeLabel(menuRow?.provider_type), menuRow?.domain_suffix || "No domain"]
        .filter(Boolean)
        .join(" / ");
      return (
        <>
          <Box sx={ACTION_MENU_HEADER_SX}>
            <Box sx={ACTION_MENU_HEADER_ICON_SX}>
              <AccountTreeRoundedIcon sx={{ fontSize: 18 }} />
            </Box>
            <Box sx={{ minWidth: 0 }}>
              <Tooltip title={providerName} placement="top-start">
                <Typography sx={{ ...ACTION_MENU_LABEL_SX, ...ACTION_MENU_TITLE_TRUNCATE_SX }}>
                  {providerName}
                </Typography>
              </Tooltip>
              <Tooltip title={providerSubtitle} placement="top-start">
                <Typography sx={{ ...ACTION_MENU_DESCRIPTION_SX, ...ACTION_MENU_TITLE_TRUNCATE_SX }}>
                  {providerSubtitle}
                </Typography>
              </Tooltip>
            </Box>
          </Box>
          {ACTION_MENU_GROUP_ORDER.map((groupId, groupIndex) => {
            const actions = contextMenuActions.filter((action) => action.group === groupId && !action.hidden);
            if (!actions.length) return null;
            return (
              <Box key={groupId}>
                {groupIndex > 0 ? <Divider sx={ACTION_MENU_DIVIDER_SX} /> : null}
                <Typography sx={ACTION_MENU_SECTION_LABEL_SX}>
                  {ACTION_MENU_GROUP_LABELS[groupId] || groupId}
                </Typography>
                {actions.map((action) => (
                  <MenuItem
                    key={action.id}
                    disabled={Boolean(action.disabled)}
                    onClick={() => {
                      if (action.disabled) return;
                      closeMenuHandler();
                      action.onClick?.();
                    }}
                    sx={action.group === "danger" ? ACTION_MENU_DANGER_ITEM_SX : ACTION_MENU_ITEM_SX}
                  >
                    {action.icon}
                    <Box sx={{ minWidth: 0 }}>
                      <Typography sx={ACTION_MENU_LABEL_SX}>{action.label}</Typography>
                      {action.description || (action.disabled && action.disabledReason) ? (
                        <Typography sx={ACTION_MENU_DESCRIPTION_SX}>
                          {action.disabled && action.disabledReason ? action.disabledReason : action.description}
                        </Typography>
                      ) : null}
                    </Box>
                  </MenuItem>
                ))}
              </Box>
            );
          })}
        </>
      );
    },
    [contextMenuActions, menuRow]
  );

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

  const activeDialogTabIndex = Math.max(
    0,
    DIALOG_TAB_DEFS.findIndex((tab) => tab.key === dialogTab)
  );
  const serverUrlPlaceholder = form.use_ldaps
    ? "ldaps://lab-dc-01.bunny-lab.io:636\nldaps://lab-dc-02.bunny-lab.io:636"
    : "ldap://lab-dc-01.bunny-lab.io:389\nldap://lab-dc-02.bunny-lab.io:389";
  const certificateState = form.tls_ca_pem || editingProvider?.tls_ca_pem_present
    ? "Pinned certificate stored"
    : form.use_ldaps
      ? "System trust"
      : "No pinned certificate";
  const dnsOverrideCount = Object.keys(textToHostOverrides(form.host_overrides_text)).length;

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
        <PageBodyFrame variant="grid">
          <Box
            className={themeClassName}
            onContextMenu={(event) => event.preventDefault()}
            sx={{
              flex: 1,
              minHeight: 0,
              width: "100%",
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
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
            style={{
              "--ag-background-color": "#070b1a",
              "--ag-foreground-color": "#f4f7ff",
              "--ag-header-background-color": "#0f172a",
              "--ag-header-foreground-color": "#cfe0ff",
              "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
              "--ag-row-hover-color": "rgba(73,156,196,0.2)",
              "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
              "--ag-border-color": "rgba(125,183,255,0.18)",
              "--ag-row-border-color": "rgba(125,183,255,0.14)",
              "--ag-border-radius": "8px",
              "--ag-cell-horizontal-padding": "18px",
            }}
          >
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
              suppressContextMenu
              preventDefaultOnContextMenu
              onCellContextMenu={(params) => openContextMenu(params.event, params.data, params.node)}
              animateRows
              theme={gridTheme}
            />
          </Box>
        </PageBodyFrame>
      </Paper>

      <Menu
        open={Boolean(contextMenu)}
        onClose={closeMenu}
        anchorReference="anchorPosition"
        anchorPosition={
          contextMenu
            ? {
                top: Number(contextMenu.top || 0),
                left: Number(contextMenu.left || 0),
              }
            : undefined
        }
        PaperProps={{ sx: ACTION_MENU_PAPER_SX }}
      >
        {renderContextMenuItems(closeMenu)}
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
          <Tabs
            value={activeDialogTabIndex}
            onChange={(_, value) => setDialogTab(DIALOG_TAB_DEFS[value]?.key || "basic")}
            variant="scrollable"
            scrollButtons="auto"
            TabIndicatorProps={{
              style: {
                height: 3,
                borderRadius: 3,
                background: NAV_TAB_COLORS.iconActive,
              },
            }}
            sx={DIALOG_TABS_SX}
          >
            {DIALOG_TAB_DEFS.map((tab) => (
              <Tab
                key={tab.key}
                label={tab.label}
                icon={<tab.icon sx={{ fontSize: 18 }} />}
                iconPosition="start"
              />
            ))}
          </Tabs>

          {dialogTab === "basic" ? (
            <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(12, 1fr)" }, gap: 2 }}>
              {form.provider_type === "active_directory" ? (
                <Box
                  sx={{
                    gridColumn: "1 / -1",
                    border: "1px solid rgba(251, 191, 36, 0.45)",
                    background: "rgba(251, 191, 36, 0.12)",
                    color: "#fde68a",
                    borderRadius: 1,
                    px: 1.5,
                    py: 1.25,
                    fontSize: 13,
                    fontWeight: 700,
                  }}
                >
                  Active Directory has not been validated as functional or bug tested. You are on your own.
                </Box>
              ) : null}
              <SectionCard
                title="Provider"
                subtitle="Operator-facing identity and domain ownership."
                action={
                  <FormControlLabel
                    sx={{ m: 0 }}
                    control={<Checkbox checked={Boolean(form.enabled)} onChange={(e) => updateForm("enabled", e.target.checked)} />}
                    label="Enabled"
                  />
                }
              >
                <FormTooltip title="A friendly name for operators to identify this credential provider.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Name" placeholder="Friendly Name" value={form.name} onChange={(e) => updateForm("name", e.target.value)} />
                </FormTooltip>
                <FormTooltip title="The FQDN of the domain being used for user authentication.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Domain Name" placeholder="bunny-lab.io" value={form.domain_suffix} onChange={(e) => updateForm("domain_suffix", e.target.value)} />
                </FormTooltip>
              </SectionCard>

              <SectionCard
                title="Connection"
                subtitle={dnsOverrideCount ? `${dnsOverrideCount} DNS override${dnsOverrideCount === 1 ? "" : "s"} configured` : "Domain controllers and transport security."}
                action={
                  <Button sx={DIALOG_BUTTON_SX} onClick={() => setDnsOverridesExpanded((prev) => !prev)}>
                    {dnsOverridesExpanded ? "Hide DNS Overrides" : "DNS Overrides"}
                  </Button>
                }
              >
                <FormTooltip title="LDAPS uses encrypted LDAP with certificate validation. LDAP uses cleartext LDAP and is not recommended outside isolated networks.">
                  <TextField select sx={{ ...DIALOG_SELECT_SX, gridColumn: { md: "span 4" } }} label="Connection Security" value={form.use_ldaps ? "ldaps" : "ldap"} onChange={(e) => updateConnectionSecurity(e.target.value)}>
                    <MenuItem value="ldaps">LDAPS</MenuItem>
                    <MenuItem value="ldap">LDAP</MenuItem>
                  </TextField>
                </FormTooltip>
                <FormTooltip title="Enter LDAP URLs for domain controllers that are part of this domain. One DC per line. Format: <protocol>://<fqdn>:<port>.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 8" } }} label="LDAP Server URLs" placeholder={serverUrlPlaceholder} multiline minRows={2} value={form.server_urls} onChange={(e) => updateForm("server_urls", e.target.value)} />
                </FormTooltip>
                {dnsOverridesExpanded ? (
                  <FormTooltip title="Maps FQDNs to IP addresses. Useful when the Borealis Engine host is not domain joined, or when LDAP authentication reaches a remote domain controller over WireGuard VPN, such as a DC with a Borealis Agent running on it.">
                    <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="Server Host DNS Overrides" placeholder={"lab-dc-01.bunny-lab.io=192.168.3.25\nlab-dc-02.bunny-lab.io=192.168.3.26"} multiline minRows={2} value={form.host_overrides_text} onChange={(e) => updateForm("host_overrides_text", e.target.value)} />
                  </FormTooltip>
                ) : null}
              </SectionCard>

              <SectionCard
                title="LDAPS Certificate Trust"
                subtitle={certificateState}
                action={
                  <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                    <Tooltip title="Reach out to the server to automatically pull down the certificate data, and if you trust it, store the certificate in the Borealis Engine for LDAPS authentication." arrow placement="top">
                      <Box component="span">
                        <Button
                          sx={DIALOG_BUTTON_SX}
                          startIcon={<FileDownloadRoundedIcon />}
                          disabled={certificateBusy || !form.use_ldaps || splitServerUrls(form.server_urls).length === 0}
                          onClick={downloadCertificate}
                        >
                          Download
                        </Button>
                      </Box>
                    </Tooltip>
                    <Button sx={DIALOG_BUTTON_SX} onClick={() => setCertificateExpanded((prev) => !prev)}>
                      {certificateExpanded ? "Hide PEM" : "Manage"}
                    </Button>
                  </Box>
                }
              >
                {certificateExpanded ? (
                  <FormTooltip title="Manually enter certificate data here, or automatically pull the certificate from the primary LDAP server.">
                    <TextField
                      sx={{
                        ...DIALOG_INPUT_SX,
                        gridColumn: { md: "span 12" },
                        "& textarea": {
                          maxHeight: 220,
                          overflowY: "auto !important",
                        },
                      }}
                      label="LDAP Server Certificate"
                      placeholder={editingProvider?.tls_ca_pem_present ? "Leave blank to keep existing LDAP server certificate" : "Manually enter certificate data here, or automatically pull the certificate from the primary LDAP server."}
                      multiline
                      minRows={3}
                      maxRows={8}
                      value={form.tls_ca_pem}
                      onChange={(e) => updateForm("tls_ca_pem", e.target.value)}
                    />
                  </FormTooltip>
                ) : null}
              </SectionCard>

              <SectionCard
                title="Advanced Provider Settings"
                subtitle={`${providerTypeLabel(form.provider_type)} provider, priority ${form.priority || 100}`}
                action={
                  <Button sx={DIALOG_BUTTON_SX} onClick={() => setBasicAdvancedExpanded((prev) => !prev)}>
                    {basicAdvancedExpanded ? "Hide" : "Edit"}
                  </Button>
                }
              >
                {basicAdvancedExpanded ? (
                  <>
                    <FormTooltip title="The connection method used to identify users.">
                      <TextField select sx={{ ...DIALOG_SELECT_SX, gridColumn: { md: "span 6" } }} label="Provider Type" value={form.provider_type} onChange={(e) => updateForm("provider_type", e.target.value)}>
                        <MenuItem value="ldap">LDAP</MenuItem>
                        <MenuItem value="active_directory">Active Directory</MenuItem>
                      </TextField>
                    </FormTooltip>
                    <FormTooltip title="This determines the authentication priority. The lower the number, the higher the priority. Used if there are multiple providers for the same domain.">
                      <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Priority" type="number" value={form.priority} onChange={(e) => updateForm("priority", e.target.value)} />
                    </FormTooltip>
                  </>
                ) : null}
              </SectionCard>
            </Box>
          ) : null}

          {dialogTab === "mapping" ? (
            <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(12, 1fr)" }, gap: 2 }}>
              <SectionCard title="Directory Search" subtitle="Service bind account and user search root.">
                <FormTooltip title="Google Base DN structure if you cannot format your Base DN from the hint example.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="Base DN" placeholder="CN=Users,DC=bunny-lab,DC=io" value={form.base_dn} onChange={(e) => updateForm("base_dn", e.target.value)} />
                </FormTooltip>
                <FormTooltip title="Domain account used to interact with the directory and perform authentication. Prefer a dedicated, low-privilege service account instead of a domain admin.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Bind DN (LDAP Service User)" placeholder="CN=Nicole Rappe,CN=Users,DC=bunny-lab,DC=io" value={form.bind_dn} onChange={(e) => updateForm("bind_dn", e.target.value)} />
                </FormTooltip>
                <FormTooltip title="Domain password used by the LDAP service user. Set it to never expire, or monitor password expiration to prevent LDAP service outages.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label={editingProvider?.bind_password_present ? "Bind Password (Securely Stored in Aegis)" : "Bind Password"} placeholder={editingProvider?.bind_password_present ? "Leave blank to keep existing secure password" : "Domain Password for the LDAP Service User"} type="password" value={form.bind_password} onChange={(e) => updateForm("bind_password", e.target.value)} />
                </FormTooltip>
              </SectionCard>

              <SectionCard
                title="Role Mapping"
                subtitle="Map directory groups to Borealis roles."
                action={
                  <FormControlLabel
                    sx={{ m: 0 }}
                    control={<Checkbox checked={Boolean(form.nested_groups)} onChange={(e) => updateForm("nested_groups", e.target.checked)} />}
                    label="Nested Groups"
                  />
                }
              >
                <FormTooltip title="How high Borealis should search in the directory hierarchy to discover groups. Use the top-level domain DN to search the entire directory for groups.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="Group Search Base DN" placeholder="DC=bunny-lab,DC=io" value={form.group_search_base_dn} onChange={(e) => updateForm("group_search_base_dn", e.target.value)} />
                </FormTooltip>
                <FormTooltip title="Directory group DNs that grant Borealis Admin role. One group DN per line.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="Admin Group DNs" placeholder="CN=Borealis_Admins,CN=Users,DC=bunny-lab,DC=io" multiline minRows={2} value={form.admin_group_dns_text} onChange={(e) => updateForm("admin_group_dns_text", e.target.value)} />
                </FormTooltip>
                <Box sx={{ gridColumn: { md: "span 12" }, display: "flex", justifyContent: "flex-end" }}>
                  <Button
                    sx={DIALOG_BUTTON_SX}
                    startIcon={<SearchRoundedIcon />}
                    disabled={accessPreviewBusyRole === "Admin" || !editingProvider?.id || linesToDnsList(form.admin_group_dns_text).length === 0}
                    onClick={() => previewEffectiveAccess("Admin")}
                  >
                    Display Effective Admin Access
                  </Button>
                </Box>
                <FormTooltip title="Directory group DNs that grant Borealis User role. One group DN per line.">
                  <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="User Group DNs" placeholder="CN=Borealis_Operators,CN=Users,DC=bunny-lab,DC=io" multiline minRows={2} value={form.user_group_dns_text} onChange={(e) => updateForm("user_group_dns_text", e.target.value)} />
                </FormTooltip>
                <Box sx={{ gridColumn: { md: "span 12" }, display: "flex", justifyContent: "flex-end" }}>
                  <Button
                    sx={DIALOG_BUTTON_SX}
                    startIcon={<SearchRoundedIcon />}
                    disabled={accessPreviewBusyRole === "User" || !editingProvider?.id || linesToDnsList(form.user_group_dns_text).length === 0}
                    onClick={() => previewEffectiveAccess("User")}
                  >
                    Display Effective User Access
                  </Button>
                </Box>
              </SectionCard>

              <SectionCard
                title="LDAP Attribute Overrides"
                subtitle={`${form.username_attribute || "sAMAccountName"} / ${form.display_name_attribute || "displayName"} / ${form.member_of_attribute || "memberOf"}`}
                action={
                  <Button sx={DIALOG_BUTTON_SX} onClick={() => setLdapAdvancedExpanded((prev) => !prev)}>
                    {ldapAdvancedExpanded ? "Hide" : "Edit"}
                  </Button>
                }
              >
                {ldapAdvancedExpanded ? (
                  <>
                    <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="Username Attribute" placeholder="sAMAccountName" value={form.username_attribute} onChange={(e) => updateForm("username_attribute", e.target.value)} />
                    <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="Display Attribute" placeholder="displayName" value={form.display_name_attribute} onChange={(e) => updateForm("display_name_attribute", e.target.value)} />
                    <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 4" } }} label="MemberOf Attribute" placeholder="memberOf" value={form.member_of_attribute} onChange={(e) => updateForm("member_of_attribute", e.target.value)} />
                    <FormTooltip title="Optional LDAP filter template used to find one user. Leave blank for the default provider filter. Supported placeholders: {username}, {login}, and {user}.">
                      <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label="User Search Filter" placeholder="(|(sAMAccountName={username})(userPrincipalName={login})(mail={login}))" value={form.user_search_filter} onChange={(e) => updateForm("user_search_filter", e.target.value)} />
                    </FormTooltip>
                    {form.provider_type === "active_directory" ? (
                      <>
                        <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="Kerberos Realm" value={form.kerberos_realm} onChange={(e) => updateForm("kerberos_realm", e.target.value)} />
                        <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 6" } }} label="KDC Host" value={form.kerberos_kdc} onChange={(e) => updateForm("kerberos_kdc", e.target.value)} />
                        <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 12" } }} label={editingProvider?.kerberos_keytab_present ? "Keytab Base64 (leave blank to keep)" : "Keytab Base64"} multiline minRows={2} value={form.kerberos_keytab_base64} onChange={(e) => updateForm("kerberos_keytab_base64", e.target.value)} />
                      </>
                    ) : null}
                  </>
                ) : null}
              </SectionCard>
            </Box>
          ) : null}

          {dialogTab === "diagnostics" ? (
            <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(12, 1fr)" }, gap: 2 }}>
              <FormTooltip title="Runs a diagnostic lookup against this provider. Use it to verify the search filter, DN resolution, group discovery, mapped Borealis role, and optional password check before enabling sign-in.">
                <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 5" } }} label="Lookup Username" placeholder="jane.doe" value={lookupUsername} onChange={(e) => setLookupUsername(e.target.value)} />
              </FormTooltip>
              <FormTooltip title="Optional for lookup diagnostics. If supplied, Borealis also checks whether the directory accepts this password for the found user. If blank, lookup verifies search, groups, and role mapping only.">
                <TextField sx={{ ...DIALOG_INPUT_SX, gridColumn: { md: "span 5" } }} label="Lookup Password" placeholder="Domain Password" type="password" value={lookupPassword} onChange={(e) => setLookupPassword(e.target.value)} />
              </FormTooltip>
              <Button
                sx={{ ...DIALOG_BUTTON_SX, gridColumn: { md: "span 2" }, minHeight: 44 }}
                startIcon={<SearchRoundedIcon />}
                disabled={lookupBusy || !editingProvider?.id || !lookupUsername.trim()}
                onClick={lookupDirectoryUser}
              >
                Lookup
              </Button>
              <Box
                sx={{
                  gridColumn: "1 / -1",
                  border: "1px solid rgba(148,163,184,0.25)",
                  borderRadius: 2,
                  background: "rgba(2,6,23,0.35)",
                  p: 1.5,
                }}
              >
                <Typography sx={{ color: "#e2e8f0", fontSize: 14, fontWeight: 700 }}>Lookup Status</Typography>
                <Typography sx={{ color: "#94a3b8", fontSize: 13, mt: 0.5 }}>
                  {lookupResult
                    ? `${lookupResult?.ok ? "Found" : "Failed"}${lookupResult?.message ? `: ${lookupResult.message}` : ""}`
                    : "Run lookup after saving provider to verify user search, group mapping, and optional password check."}
                </Typography>
              </Box>
            </Box>
          ) : null}
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

      <Dialog
        open={Boolean(accessPreview)}
        onClose={() => setAccessPreview(null)}
        maxWidth="md"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={`Effective ${accessPreview?.role || "Directory"} Access`}
            subtitle={`${accessPreview?.user_count ?? 0} matching user${Number(accessPreview?.user_count || 0) === 1 ? "" : "s"}`}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 1.25 }}>
            {(accessPreview?.group_dns || []).length ? (
              <Box sx={{ border: "1px solid rgba(148,163,184,0.2)", borderRadius: 2, p: 1.25, background: "rgba(2,6,23,0.3)" }}>
                <Typography sx={{ color: "#94a3b8", fontSize: 12, fontWeight: 700, textTransform: "uppercase", letterSpacing: "0.08em", mb: 0.75 }}>
                  Group DNs
                </Typography>
                <Typography sx={{ color: "#f8fafc", fontSize: 13, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                  {(accessPreview?.group_dns || []).join("\n")}
                </Typography>
              </Box>
            ) : null}
            {(accessPreview?.users || []).length ? (
              (accessPreview?.users || []).map((user) => (
                <Box
                  key={user.dn || user.account}
                  sx={{
                    border: "1px solid rgba(148,163,184,0.2)",
                    borderRadius: 2,
                    p: 1.25,
                    background: "rgba(2,6,23,0.3)",
                  }}
                >
                  <Typography sx={{ color: "#e2e8f0", fontSize: 14, fontWeight: 700 }}>{user.display_name || user.account || "-"}</Typography>
                  <Typography sx={{ color: "#7dd3fc", fontSize: 13, overflowWrap: "anywhere" }}>{user.account || "-"}</Typography>
                  <Typography sx={{ color: "#94a3b8", fontSize: 12, mt: 0.5, overflowWrap: "anywhere" }}>{user.dn || "-"}</Typography>
                  {Array.isArray(user.matched_groups) && user.matched_groups.length ? (
                    <Typography sx={{ color: "#cbd5e1", fontSize: 12, mt: 0.5, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                      {user.matched_groups.join("\n")}
                    </Typography>
                  ) : null}
                </Box>
              ))
            ) : (
              <Typography sx={{ color: "#94a3b8", fontSize: 13 }}>
                No users currently match these group DNs.
              </Typography>
            )}
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} onClick={() => setAccessPreview(null)}>Close</Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={Boolean(lookupResult)}
        onClose={() => setLookupResult(null)}
        maxWidth="md"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Directory User Lookup"
            subtitle={lookupResult?.login || lookupUsername}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box sx={{ display: "grid", gridTemplateColumns: "minmax(130px, 0.25fr) minmax(0, 1fr)", gap: 1.25, color: "#dbeafe" }}>
            {[
              ["Status", lookupResult?.ok ? "Found" : (lookupResult?.error || "Failed")],
              ["Message", lookupResult?.message || ""],
              ["Account", lookupResult?.account],
              ["Display Name", lookupResult?.display_name],
              ["DN", lookupResult?.dn],
              ["Subject", lookupResult?.subject],
              ["Mapped Role", lookupResult?.mapped_role || (lookupResult?.allowed === false ? "No matching group" : "")],
              ["Password", lookupResult?.password_checked ? (lookupResult?.password_ok ? "OK" : (lookupResult?.password_message || "Failed")) : "Not checked"],
              ["Groups", Array.isArray(lookupResult?.groups) ? lookupResult.groups.join("\n") : ""],
            ].filter(([, value]) => value !== "").map(([label, value]) => (
              <React.Fragment key={label}>
                <Typography sx={{ color: "#94a3b8", fontSize: 13 }}>{label}</Typography>
                <Typography sx={{ color: "#f8fafc", fontSize: 13, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{value || "-"}</Typography>
              </React.Fragment>
            ))}
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} onClick={() => setLookupResult(null)}>Close</Button>
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
