import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  IconButton,
  Menu,
  MenuItem,
  Paper,
  Tooltip,
  Typography,
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import AddIcon from "@mui/icons-material/Add";
import RefreshIcon from "@mui/icons-material/Refresh";
import LockIcon from "@mui/icons-material/Lock";
import LockOpenIcon from "@mui/icons-material/LockOpenRounded";
import VpnKeyIcon from "@mui/icons-material/VpnKeyRounded";
import GitHubIcon from "@mui/icons-material/GitHub";
import WifiIcon from "@mui/icons-material/Wifi";
import ComputerIcon from "@mui/icons-material/Computer";
import WarningAmberRoundedIcon from "@mui/icons-material/WarningAmberRounded";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import CredentialEditor from "./Credential_Editor.jsx";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#1f2836",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor"
  },
  fontFamily: {
    googleFont: "IBM Plex Sans"
  },
  foregroundColor: "#FFF",
  headerFontSize: 14
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';
const MAGIC_UI = {
  textBright: "#e2e8f0",
  textMuted: "#9aa3ad",
};
const RESET_WARNING_COLOR = "#f0c36d";

function normalizeLostSecretFields(row) {
  if (Array.isArray(row?.lost_secret_fields)) {
    return row.lost_secret_fields
      .map((value) => String(value || "").trim().toLowerCase())
      .filter(Boolean);
  }
  const metadataFields = row?.metadata?.aegis_lost_secret_fields;
  if (Array.isArray(metadataFields)) {
    return metadataFields
      .map((value) => String(value || "").trim().toLowerCase())
      .filter(Boolean);
  }
  return [];
}

function rowRequiresSecretReset(row) {
  if (!row) return false;
  if (row.row_kind === "github_token") {
    return Boolean(row.reset_required);
  }
  if (row.row_kind === "aegis") {
    return false;
  }
  if (Boolean(row.secret_reset_required)) {
    return true;
  }
  const state = String(row?.metadata?.aegis_secret_state || "").trim().toLowerCase();
  return state === "reset_required" && normalizeLostSecretFields(row).length > 0;
}

function rowResetWarningMessage(row) {
  if (!rowRequiresSecretReset(row)) return "";
  if (row?.row_kind === "github_token") {
    return (
      row?.github_message ||
      "Aegis Cipher reset removed the stored GitHub token. Re-enter the Personal Access Token to restore GitHub access."
    );
  }
  return "Aegis Cipher reset removed one or more stored secret fields for this credential. Open it to re-enter the highlighted values.";
}

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

function formatConnectionLabel(connectionType) {
  const value = String(connectionType || "").toLowerCase();
  if (!value) return "-";
  if (value === "ssh") return "SSH";
  if (value === "winrm") return "WinRM";
  if (value === "github") return "GitHub";
  return titleCase(value);
}

function formatCredentialConnectionLabel(row) {
  const baseLabel = formatConnectionLabel(row?.connection_type);
  if (String(row?.connection_type || "").toLowerCase() === "ssh" && Boolean(row?.has_private_key)) {
    return "SSH w/ Stored Private Key";
  }
  return baseLabel;
}

function connectionIcon(connection) {
  const val = (connection || "").toLowerCase();
  if (val === "aegis_pending") return <VpnKeyIcon fontSize="small" sx={{ mr: 0.6, color: "#c084fc" }} />;
  if (val === "aegis_locked") return <LockIcon fontSize="small" sx={{ mr: 0.6, color: "#f0c36d" }} />;
  if (val === "aegis_unlocked") return <LockOpenIcon fontSize="small" sx={{ mr: 0.6, color: "#7dffac" }} />;
  if (val === "github_verified") return <GitHubIcon fontSize="small" sx={{ mr: 0.6, color: "#7dffac" }} />;
  if (val === "github_invalid") return <GitHubIcon fontSize="small" sx={{ mr: 0.6, color: "#ff8080" }} />;
  if (val === "ssh") return <LockIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
  if (val === "winrm") return <WifiIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
  return <ComputerIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
}

function aegisRowFromStatus(aegisStatus) {
  const configured = Boolean(aegisStatus?.configured);
  const locked = Boolean(aegisStatus?.locked);
  return {
    id: "__aegis_cipher__",
    row_kind: "aegis",
    name: "Aegis Cipher",
    credential_type: "protected secrets",
    connection_type: !configured ? "aegis_pending" : locked ? "aegis_locked" : "aegis_unlocked",
    site_name: "Credentials + GitHub Token",
    username: "",
    updated_at: aegisStatus?.updated_at || 0,
    aegis_status_label: !configured ? "Not configured" : locked ? "Locked" : "Unlocked",
  };
}

function githubTokenRowFromStatus(githubTokenState, aegisStatus) {
  const verified = Boolean(githubTokenState?.valid) && String(githubTokenState?.status || "").toLowerCase() === "ok";
  const rateLimit = verified ? 5000 : 60;
  return {
    id: "__github_api_token__",
    row_kind: "github_token",
    name: "GitHub API Token",
    description:
      githubTokenState?.message ||
      "Significantly increases GitHub API rate limits for both the Borealis Repository and the Aurora Assembly Repository.",
    credential_type: "token",
    connection_type: verified ? "github_verified" : "github_invalid",
    github_rate_label: `Hourly Request Rate Limit: ${rateLimit}`,
    github_connection_color: verified ? "#7dffac" : "#ff8080",
    site_name: "GitHub",
    username: "",
    updated_at: githubTokenState?.checked_at || aegisStatus?.updated_at || 0,
    token: typeof githubTokenState?.token === "string" ? githubTokenState.token : "",
    github_status: githubTokenState?.status || "",
    github_valid: verified,
    github_message: githubTokenState?.message || "",
    github_rate_limit: rateLimit,
    reset_required: Boolean(githubTokenState?.reset_required),
    reset_at: Number(githubTokenState?.reset_at) || 0,
  };
}

export default function CredentialList({
  isAdmin = false,
  onPageMetaChange,
  aegisStatus,
  onAegisAction,
}) {
  const [rows, setRows] = useState([]);
  const [githubTokenState, setGithubTokenState] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuRow, setMenuRow] = useState(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorMode, setEditorMode] = useState("create");
  const [editingCredential, setEditingCredential] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const gridApiRef = useRef(null);

  const canMutateCredentials = Boolean(aegisStatus?.configured) && !Boolean(aegisStatus?.locked);
  const aegisRow = useMemo(() => aegisRowFromStatus(aegisStatus), [aegisStatus]);
  const githubTokenRow = useMemo(
    () => githubTokenRowFromStatus(githubTokenState, aegisStatus),
    [githubTokenState, aegisStatus]
  );
  const gridRows = useMemo(() => [aegisRow, githubTokenRow, ...rows], [aegisRow, githubTokenRow, rows]);

  const openMenu = useCallback((event, row) => {
    setMenuAnchor(event.currentTarget);
    setMenuRow(row);
  }, []);

  const closeMenu = useCallback(() => {
    setMenuAnchor(null);
    setMenuRow(null);
  }, []);

  const connectionCellRenderer = useCallback((params) => {
    const row = params.data || {};
    const label = row.row_kind === "aegis"
      ? row.aegis_status_label || "Not configured"
      : row.row_kind === "github_token"
      ? row.github_rate_label || "Hourly Request Rate Limit: 60"
      : formatCredentialConnectionLabel(row);
    const hasStoredPrivateKey =
      row.row_kind !== "aegis" &&
      row.row_kind !== "github_token" &&
      String(row.connection_type || "").toLowerCase() === "ssh" &&
      Boolean(row.has_private_key);
    return (
      <Box sx={{ display: "flex", alignItems: "center", fontFamily: gridFontFamily }}>
        {connectionIcon(row.connection_type)}
        {hasStoredPrivateKey ? (
          <Box component="span">
            <Box component="span" sx={{ color: "#f5f7fa" }}>
              SSH
            </Box>
            <Box component="span" sx={{ color: MAGIC_UI.textMuted }}>
              {" "}w/ Stored Private Key
            </Box>
          </Box>
        ) : (
          <Box component="span" sx={{ color: "#f5f7fa" }}>
            {label}
          </Box>
        )}
      </Box>
    );
  }, []);

  const actionCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleClick = (event) => {
        event.preventDefault();
        event.stopPropagation();
        openMenu(event, row);
      };
      return (
        <IconButton size="small" onClick={handleClick} sx={{ color: "#7db7ff" }}>
          <MoreVertIcon fontSize="small" />
        </IconButton>
      );
    },
    [openMenu]
  );

  const nameComparator = useCallback((valueA, valueB, nodeA, nodeB) => {
    const sortWeight = (rowKind) => {
      if (rowKind === "aegis") return 0;
      if (rowKind === "github_token") return 1;
      return 2;
    };
    const aWeight = sortWeight(nodeA?.data?.row_kind);
    const bWeight = sortWeight(nodeB?.data?.row_kind);
    if (aWeight !== bWeight) return aWeight - bWeight;
    return String(valueA || "").localeCompare(String(valueB || ""));
  }, []);

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Name",
        field: "name",
        sort: "asc",
        comparator: nameComparator,
        cellRenderer: (params) => {
          const row = params.data || {};
          const resetRequired = rowRequiresSecretReset(row);
          const resetMessage = rowResetWarningMessage(row);
          const warningGlyph = resetRequired ? (
            <Tooltip title={resetMessage} arrow>
              <WarningAmberRoundedIcon fontSize="small" sx={{ color: RESET_WARNING_COLOR }} />
            </Tooltip>
          ) : null;
          if (row.row_kind === "aegis") {
            return (
              <Box sx={{ display: "flex", alignItems: "center", gap: 0.9, fontWeight: 600 }}>
                <VpnKeyIcon fontSize="small" sx={{ color: "#7db7ff" }} />
                <Box component="span">{row.name || "Aegis Cipher"}</Box>
              </Box>
            );
          }
          if (row.row_kind === "github_token") {
            return (
              <Box sx={{ display: "flex", alignItems: "center", gap: 0.9, fontWeight: 600 }}>
                <GitHubIcon fontSize="small" sx={{ color: "#7db7ff" }} />
                <Box component="span">{row.name || "GitHub API Token"}</Box>
                {warningGlyph}
              </Box>
            );
          }
          if (String(row.credential_type || "").toLowerCase() === "machine") {
            return (
              <Box sx={{ display: "flex", alignItems: "center", gap: 0.9 }}>
                <ComputerIcon fontSize="small" sx={{ color: "#7db7ff" }} />
                <Box component="span">{row.name || "-"}</Box>
                {warningGlyph}
              </Box>
            );
          }
          return (
            <Box sx={{ display: "flex", alignItems: "center", gap: 0.9 }}>
              <Box component="span">{params.value || "-"}</Box>
              {warningGlyph}
            </Box>
          );
        }
      },
      {
        headerName: "Credential Type",
        field: "credential_type",
        valueGetter: (params) =>
          params.data?.row_kind === "aegis"
            ? "Protected Secrets"
            : params.data?.row_kind === "github_token"
            ? "Token"
            : titleCase(params.data?.credential_type)
      },
      {
        headerName: "Connection",
        field: "connection_type",
        cellRenderer: connectionCellRenderer
      },
      {
        headerName: "Site",
        field: "site_name",
        cellRenderer: (params) => {
          if (params.data?.row_kind === "aegis") {
            return params.value || "-";
          }
          return params.value || "Global";
        }
      },
      {
        headerName: "Username",
        field: "username",
        cellRenderer: (params) => {
          if (params.data?.row_kind === "aegis" || params.data?.row_kind === "github_token") {
            return "";
          }
          return params.value || "-";
        }
      },
      {
        headerName: "Updated",
        field: "updated_at",
        valueGetter: (params) => formatTs(params.data?.updated_at || params.data?.created_at)
      },
      {
        headerName: "",
        field: "__actions__",
        minWidth: 70,
        maxWidth: 80,
        sortable: false,
        filter: false,
        resizable: false,
        suppressMenu: true,
        cellRenderer: actionCellRenderer,
        pinned: "right"
      }
    ],
    [actionCellRenderer, connectionCellRenderer, nameComparator]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      flex: 1,
      minWidth: 140,
      cellStyle: {
        display: "flex",
        alignItems: "center",
        color: "#f5f7fa",
        fontFamily: gridFontFamily,
        fontSize: "13px"
      },
      headerClass: "credential-grid-header"
    }),
    []
  );

  const getRowId = useCallback(
    (params) =>
      params.data?.id ||
      params.data?.name ||
      params.data?.username ||
      String(params.rowIndex ?? ""),
    []
  );

  const sortCredentialRows = useCallback(
    (items) => [...items].sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || ""))),
    []
  );

  const fetchCredentials = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [credentialsResp, githubResp] = await Promise.all([
        fetch("/api/credentials", { cache: "no-store", credentials: "include" }),
        fetch("/api/github/token", { cache: "no-store", credentials: "include" }),
      ]);
      const data = await credentialsResp.json().catch(() => ({}));
      if (!credentialsResp.ok) throw new Error(data?.message || data?.error || `HTTP ${credentialsResp.status}`);
      const list = Array.isArray(data?.credentials) ? data.credentials : [];
      const githubData = await githubResp.json().catch(() => ({}));
      setRows(sortCredentialRows(list));
      setGithubTokenState(
        githubResp.ok
          ? githubData
          : {
              valid: false,
              status: "error",
              checked_at: Math.floor(Date.now() / 1000),
              message: githubData?.message || githubData?.error || `HTTP ${githubResp.status}`,
              token: "",
            }
      );
    } catch (err) {
      setRows([]);
      setGithubTokenState(null);
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  }, [sortCredentialRows]);

  useEffect(() => {
    fetchCredentials();
  }, [fetchCredentials, aegisStatus?.configured, aegisStatus?.locked]);

  const handleCreate = useCallback(() => {
    if (!canMutateCredentials) return;
    setEditorMode("create");
    setEditingCredential(null);
    setEditorOpen(true);
  }, [canMutateCredentials]);

  const handleEdit = useCallback((row) => {
    if (!canMutateCredentials || row?.row_kind === "aegis") return;
    closeMenu();
    setEditorMode("edit");
    setEditingCredential(row);
    setEditorOpen(true);
  }, [canMutateCredentials, closeMenu]);

  const handleDelete = useCallback((row) => {
    if (!canMutateCredentials || row?.row_kind === "aegis" || row?.row_kind === "github_token") return;
    closeMenu();
    setDeleteTarget(row);
  }, [canMutateCredentials, closeMenu]);

  const handleAegisMenuAction = useCallback((mode) => {
    closeMenu();
    onAegisAction && onAegisAction(mode);
  }, [closeMenu, onAegisAction]);

  const doDelete = async () => {
    if (!deleteTarget?.id) return;
    setDeleteBusy(true);
    try {
      const resp = await fetch(`/api/credentials/${deleteTarget.id}`, {
        method: "DELETE",
        credentials: "include",
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      setDeleteTarget(null);
      await fetchCredentials();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setDeleteBusy(false);
    }
  };

  const handleEditorSaved = async (savedCredential) => {
    if (savedCredential?.id) {
      setRows((prev) => {
        const next = prev.some((row) => String(row?.id) === String(savedCredential.id))
          ? prev.map((row) => (String(row?.id) === String(savedCredential.id) ? { ...row, ...savedCredential } : row))
          : [...prev, savedCredential];
        return sortCredentialRows(next);
      });
    }
    setEditorOpen(false);
    setEditingCredential(null);
    await fetchCredentials();
  };

  const handleGridReady = useCallback((params) => {
    gridApiRef.current = params.api;
  }, []);

  const banners = useMemo(() => {
    const next = [];
    const resetCredentialCount = rows.filter((row) => rowRequiresSecretReset(row)).length;
    const githubResetRequired = rowRequiresSecretReset(githubTokenRow);
    if (!aegisStatus?.configured) {
      next.push({
        severity: "warning",
        message:
          "Set up the Aegis Cipher before creating or changing credentials. Existing credential-backed workflows remain available until setup is completed.",
      });
    } else if (aegisStatus?.locked) {
      next.push({
        severity: "warning",
        message:
          "Aegis Cipher is locked. Credential metadata remains visible, but credential changes are disabled until the cipher is entered.",
      });
    }
    if (resetCredentialCount || githubResetRequired) {
      const credentialSummary =
        resetCredentialCount > 0
          ? `${resetCredentialCount} credential${resetCredentialCount === 1 ? "" : "s"}`
          : "";
      const githubSummary = githubResetRequired ? "the GitHub API token" : "";
      const subjects = [credentialSummary, githubSummary].filter(Boolean).join(" and ");
      next.push({
        severity: "warning",
        message:
          `Aegis Cipher was force reset. ${subjects} lost stored secret material and must be updated before dependent jobs can be safely re-enabled.`,
      });
    }
    if (!error) return next;
    if (String(error).includes("HTTP 404")) {
      next.push({
        severity: "info",
        message: "Credential management does not yet exist. This page serves as a placeholder.",
      });
      return next;
    }
    next.push({
      severity: "error",
      message: `Unable to load credentials: ${error}`,
    });
    return next;
  }, [aegisStatus, error, githubTokenRow, rows]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    if (loading) {
      api.showLoadingOverlay();
    } else if (!gridRows.length) {
      api.showNoRowsOverlay();
    } else {
      api.hideOverlay();
    }
  }, [gridRows, loading]);

  const pageHeaderActions = useMemo(() => {
    const actions = [
      {
        id: "credentials-refresh",
        label: "Refresh",
        icon: <RefreshIcon />,
        tone: "secondary",
        loading,
        onClick: fetchCredentials,
      },
    ];

    const newCredentialAction = {
      id: "credentials-create",
      label: "New Credential",
      icon: <AddIcon />,
      tone: "primary",
      disabled: !canMutateCredentials,
      tooltip: !aegisStatus?.configured
        ? "Set up Aegis Cipher before adding credentials."
        : aegisStatus?.locked
        ? "Enter the Aegis Cipher before adding credentials."
        : "",
      onClick: handleCreate,
    };

    if (!aegisStatus?.configured) {
      actions.push(newCredentialAction);
      actions.push({
        id: "credentials-aegis-setup",
        label: "Setup Aegis Cipher",
        icon: <VpnKeyIcon />,
        tone: "primary",
        onClick: () => onAegisAction && onAegisAction("setup"),
      });
      return actions;
    }

    if (aegisStatus?.locked) {
      actions.push({
        id: "credentials-aegis-unlock",
        label: "Enter Aegis Cipher",
        icon: <VpnKeyIcon />,
        tone: "secondary",
        onClick: () => onAegisAction && onAegisAction("unlock"),
      });
      actions.push(newCredentialAction);
      return actions;
    }

    actions.push(newCredentialAction);
    return actions;
  }, [aegisStatus, canMutateCredentials, fetchCredentials, handleCreate, loading, onAegisAction]);

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Credentials",
      page_subtitle: "Stored machine & domain credentials and API service tokens for remote automation tasks, Ansible playbook runs, and Borealis GitHub access.",
      page_icon: LockIcon,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

  if (!isAdmin) {
    return (
      <Paper sx={{ m: 2, p: 3, bgcolor: "transparent" }}>
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
      <PageBodyFrame
        variant="grid_with_stack"
        stack={
          banners.length ? (
            <Box sx={{ display: "flex", flexDirection: "column", gap: 1.1 }}>
              {banners.map((banner) => (
                <Alert key={`${banner.severity}-${banner.message}`} severity={banner.severity}>
                  {banner.message}
                </Alert>
              ))}
            </Box>
          ) : null
        }
      >
        <Box
          className={themeClassName}
          sx={{
            flexGrow: 1,
            minHeight: 0,
            width: "100%",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              minHeight: "100%",
              border: "none",
              borderRadius: 0,
              background: "transparent",
            },
            "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
              fontFamily: gridFontFamily,
              background: "transparent",
            },
            "& .ag-header": {
              backgroundColor: "rgba(15,23,42,0.9)",
              borderBottom: "1px solid rgba(148,163,184,0.25)",
            },
            "& .ag-header-cell-label": {
              color: MAGIC_UI.textBright,
              fontWeight: 600,
              letterSpacing: 0.3,
            },
            "& .ag-icon": {
              fontFamily: iconFontFamily,
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
            "& .ag-paging-panel": {
              borderTop: "1px solid rgba(148,163,184,0.2)",
              backgroundColor: "rgba(3,7,18,0.8)",
            },
          }}
          style={{
            "--ag-background-color": "transparent",
            "--ag-foreground-color": "#f5f7fa",
            "--ag-row-hover-color": "rgba(73,156,196,0.2)",
            "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
            "--ag-checkbox-checked-color": "#7dd3fc",
          }}
        >
          <AgGridReact
            rowData={gridRows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            animateRows
            rowHeight={46}
            headerHeight={44}
            getRowId={getRowId}
            overlayNoRowsTemplate={`<span class='ag-overlay-no-rows-center'>${
              !aegisStatus?.configured
                ? "Protected credentials will appear here once Aegis Cipher setup is complete."
                : "No credentials have been created yet."
            }</span>`}
            onGridReady={handleGridReady}
            suppressCellFocus
            theme={myTheme}
            style={{
              width: "100%",
              height: "100%",
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
            }}
          />
        </Box>
      </PageBodyFrame>

      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        elevation={2}
        PaperProps={{ sx: { bgcolor: "#1f1f1f", color: "#f5f5f5" } }}
      >
        {menuRow?.row_kind === "aegis" ? (
          <>
            {!aegisStatus?.configured ? (
              <MenuItem onClick={() => handleAegisMenuAction("setup")}>Setup Aegis Cipher</MenuItem>
            ) : null}
            {aegisStatus?.configured && aegisStatus?.locked ? (
              <MenuItem onClick={() => handleAegisMenuAction("unlock")}>Enter Aegis Cipher</MenuItem>
            ) : null}
            {aegisStatus?.configured ? (
              <MenuItem onClick={() => handleAegisMenuAction("rotate")}>Rotate Aegis Cipher</MenuItem>
            ) : null}
            {aegisStatus?.configured ? (
              <MenuItem onClick={() => handleAegisMenuAction("force_reset")} sx={{ color: "#ff9aa5" }}>
                Force Reset Aegis Cipher
              </MenuItem>
            ) : null}
          </>
        ) : menuRow?.row_kind === "github_token" ? (
          <MenuItem disabled={!canMutateCredentials} onClick={() => handleEdit(menuRow)}>
            Edit Token
          </MenuItem>
        ) : (
          <>
            <MenuItem disabled={!canMutateCredentials} onClick={() => handleEdit(menuRow)}>Edit</MenuItem>
            <MenuItem
              disabled={!canMutateCredentials}
              onClick={() => handleDelete(menuRow)}
              sx={{ color: canMutateCredentials ? "#ff8080" : "inherit" }}
            >
              Delete
            </MenuItem>
          </>
        )}
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
        title="Delete Credential"
        onCancel={() => setDeleteTarget(null)}
        onConfirm={doDelete}
        confirmDisabled={deleteBusy}
        confirmLabel="Delete Credential"
        message={
          deleteTarget
            ? `Delete credential '${deleteTarget.name || ""}'? Any jobs referencing it will require an update.`
            : ""
        }
      />
    </>
  );
}
