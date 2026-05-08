import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import { useLoaderData, useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  Menu,
  MenuItem,
  Paper,
  Typography,
  IconButton,
  Tooltip,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import AssessmentRoundedIcon from "@mui/icons-material/AssessmentRounded";
import DeleteRoundedIcon from "@mui/icons-material/DeleteRounded";
import DevicesRoundedIcon from "@mui/icons-material/DevicesRounded";
import DriveFileRenameOutlineRoundedIcon from "@mui/icons-material/DriveFileRenameOutlineRounded";
import LocationCityIcon from "@mui/icons-material/LocationCity";
import DownloadRoundedIcon from "@mui/icons-material/DownloadRounded";
import ContentCopyIcon from "@mui/icons-material/ContentCopy";
import AltRouteRoundedIcon from "@mui/icons-material/AltRouteRounded";
import DevicesIcon from "@mui/icons-material/Devices";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { CreateSiteDialog, RenameSiteDialog } from "../Dialogs.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import {
  createRouteRequestPlan,
  fetchRouteJson,
  getRouteErrorMessage,
  requireAuthenticatedRequest,
  rethrowIfRouteRedirect,
} from "../app/routes/routeData.js";
import { APP_PATHS } from "../app/routes/paths.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const AUTO_SIZE_COLUMNS = ["device_count", "enrollment_code"];
const DEFAULT_INSTALL_BRANCH = "main";
const BOREALIS_GITHUB_REPO = "bunny-lab-io/Borealis";
const GITHUB_BRANCHES_API_URL = `https://api.github.com/repos/${BOREALIS_GITHUB_REPO}/branches`;
const RAW_BOREALIS_BASE_URL = "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads";
const INSTALL_OS_OPTIONS = [
  { id: "windows", label: "Windows" },
  { id: "linux", label: "Linux" },
  { id: "macos", label: "MacOS" },
];

const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBg:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.94) 60%, rgba(15, 6, 26, 0.96) 100%)",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  success: "#34d399",
};

const SITE_DIALOG_PAPER_SX = {
  borderRadius: 3,
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), rgba(8,12,24,0.96)",
  backdropFilter: "blur(18px)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: "0 24px 60px rgba(2,8,23,0.72)",
  color: MAGIC_UI.textBright,
  overflow: "hidden",
};

const SITE_DIALOG_TITLE_SX = {
  px: 3,
  pt: 3,
  pb: 0.75,
  background: "transparent",
};

const SITE_DIALOG_CONTENT_SX = {
  px: 3,
  pt: 1,
  pb: 2.5,
  background: "transparent",
};

const SITE_DIALOG_ACTIONS_SX = {
  px: 3,
  pt: 0.5,
  pb: 2.5,
  gap: 1,
  background: "transparent",
};

const SITE_DIALOG_BUTTON_SX = {
  borderRadius: 999,
  px: 2,
  minHeight: 38,
  textTransform: "none",
  fontWeight: 600,
  fontSize: "0.92rem",
  color: MAGIC_UI.textBright,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(5,10,24,0.84)",
  transition: "background 160ms ease, border-color 160ms ease, color 160ms ease, transform 120ms ease",
  "&:hover": {
    background: "rgba(8,14,30,0.92)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&:active": {
    transform: "translateY(0.5px)",
  },
};

const SITE_DIALOG_DANGER_BUTTON_SX = {
  ...SITE_DIALOG_BUTTON_SX,
  color: "#ff9aa5",
  borderColor: "rgba(244,63,94,0.38)",
  background: "rgba(44,8,22,0.6)",
  "&:hover": {
    background: "rgba(58,10,28,0.76)",
    borderColor: "rgba(251,113,133,0.58)",
  },
  "&.Mui-disabled": {
    color: "rgba(255,154,165,0.48)",
    borderColor: "rgba(244,63,94,0.16)",
    background: "rgba(44,8,22,0.24)",
  },
};

const INSTALL_MENU_PAPER_SX = {
  mt: 0.8,
  minWidth: 180,
  borderRadius: 2.5,
  overflow: "hidden",
  background:
    "radial-gradient(140% 140% at 0% 0%, rgba(76,186,255,0.18), transparent 52%), " +
    "radial-gradient(140% 140% at 100% 0%, rgba(214,130,255,0.22), transparent 62%), rgba(8,12,24,0.96)",
  backdropFilter: "blur(18px)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: "0 24px 60px rgba(2,8,23,0.72)",
  color: MAGIC_UI.textBright,
  "& .MuiList-root": {
    py: 0.8,
  },
};

const INSTALL_MENU_ITEM_SX = {
  mx: 0.8,
  my: 0.3,
  borderRadius: 2,
  minHeight: 40,
  fontSize: "0.92rem",
  fontWeight: 600,
  color: MAGIC_UI.textBright,
  transition: "background 160ms ease, color 160ms ease, transform 120ms ease",
  "&:hover": {
    background:
      "linear-gradient(90deg, rgba(125,211,252,0.16) 0%, rgba(192,132,252,0.14) 100%)",
  },
  "&.Mui-focusVisible": {
    background:
      "linear-gradient(90deg, rgba(125,211,252,0.16) 0%, rgba(192,132,252,0.14) 100%)",
  },
};

const SITE_CONTEXT_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};

const SITE_CONTEXT_MENU_ITEM_SX = {
  minHeight: 42,
  borderRadius: 1.6,
  color: MAGIC_UI.textBright,
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
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const SITE_CONTEXT_MENU_DANGER_ITEM_SX = {
  ...SITE_CONTEXT_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const SITE_CONTEXT_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const SITE_CONTEXT_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const SITE_CONTEXT_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const SITE_CONTEXT_MENU_HEADER_ICON_SX = {
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

const SITE_CONTEXT_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const SITE_CONTEXT_MENU_LABEL_SX = {
  color: MAGIC_UI.textBright,
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const SITE_CONTEXT_MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const SITE_CONTEXT_MENU_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const SITE_CONTEXT_MENU_GROUP_LABELS = {
  primary: "Primary",
  organize: "Organize",
  danger: "Danger Zone",
  view: "View",
};

const SITE_CONTEXT_MENU_GROUP_ORDER = ["primary", "organize", "danger", "view"];

function normalizeInstallServerUrl(value) {
  return String(value || "").trim().replace(/\/+$/, "");
}

function deriveInstallServerUrl(payload) {
  const explicitBaseUrl = normalizeInstallServerUrl(payload?.public_base_url);
  if (explicitBaseUrl) {
    return explicitBaseUrl;
  }

  const explicitHostname = String(payload?.public_hostname || "").trim();
  if (explicitHostname) {
    if (/^https?:\/\//i.test(explicitHostname)) {
      return normalizeInstallServerUrl(explicitHostname);
    }
    return `https://${explicitHostname}`;
  }

  if (typeof window === "undefined") {
    return "";
  }

  try {
    const currentOrigin = String(window.location?.origin || "").trim();
    if (!currentOrigin) return "";
    const currentUrl = new URL(currentOrigin);
    const host = currentUrl.hostname.toLowerCase();
    if (host === "localhost" || host === "127.0.0.1" || host === "0.0.0.0") {
      return "";
    }
    return normalizeInstallServerUrl(currentUrl.origin);
  } catch {
    return "";
  }
}

export async function loadSiteListPageData(request) {
  const progress = createRouteRequestPlan(request, 4);
  try {
    await requireAuthenticatedRequest(request, progress);
    const data = await progress.fetchJson("/api/sites");
    let installServerUrl = deriveInstallServerUrl(data);
    if (!installServerUrl) {
      try {
        const overviewPayload = await progress.fetchJson("/api/server/overview");
        installServerUrl = deriveInstallServerUrl({
          public_base_url:
            overviewPayload?.host?.public_base_url || overviewPayload?.public_edge?.public_base_url || "",
          public_hostname:
            overviewPayload?.host?.public_hostname || overviewPayload?.public_edge?.fqdn || "",
        });
      } catch (error) {
        rethrowIfRouteRedirect(error);
      }
    } else {
      progress.skip(1);
    }

    return {
      rows: Array.isArray(data?.sites) ? data.sites : [],
      installServerUrl,
      initialError: "",
    };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {
      rows: [],
      installServerUrl: "",
      initialError: getRouteErrorMessage(error, "Unable to load sites."),
    };
  } finally {
    progress.finalize();
  }
}

function stopGridRowSelectionEvent(event) {
  if (!event) return;
  if (typeof event.stopPropagation === "function") {
    event.stopPropagation();
  }
}

function escapePowerShellDoubleQuoted(value) {
  return String(value || "").replace(/`/g, "``").replace(/"/g, '`"');
}

function escapeShellDoubleQuoted(value) {
  return String(value || "").replace(/(["\\$`])/g, "\\$1");
}

function normalizeInstallBranch(value) {
  return String(value || DEFAULT_INSTALL_BRANCH).trim() || DEFAULT_INSTALL_BRANCH;
}

function rawBorealisFileUrl(branch, fileName) {
  const encodedBranch = normalizeInstallBranch(branch)
    .split("/")
    .map((part) => encodeURIComponent(part))
    .join("/");
  return `${RAW_BOREALIS_BASE_URL}/${encodedBranch}/${fileName}`;
}

function quoteShellValue(value) {
  return `"${escapeShellDoubleQuoted(value)}"`;
}

function quotePowerShellValue(value) {
  return `"${escapePowerShellDoubleQuoted(value)}"`;
}

export function buildInstallCommand(osId, serverUrl, enrollmentCode, branch = DEFAULT_INSTALL_BRANCH) {
  const normalizedServerUrl = normalizeInstallServerUrl(serverUrl);
  const normalizedEnrollmentCode = String(enrollmentCode || "").trim();
  const normalizedBranch = normalizeInstallBranch(branch);
  const usesDefaultBranch = normalizedBranch === DEFAULT_INSTALL_BRANCH;
  if (!normalizedServerUrl || !normalizedEnrollmentCode) {
    return "";
  }

  if (osId === "windows") {
    const agentUrl = rawBorealisFileUrl(normalizedBranch, "Data/Agent/Bootstrap/Agent.exe");
    return `$borealisAgent = Join-Path $env:TEMP "Borealis-Agent.exe"; ` +
      `Invoke-WebRequest -UseBasicParsing -Uri ${quotePowerShellValue(agentUrl)} -OutFile $borealisAgent; ` +
      `& $borealisAgent --server-url ${quotePowerShellValue(normalizedServerUrl)} ` +
      `--site-enrollment-code ${quotePowerShellValue(normalizedEnrollmentCode)}`;
  }

  const agentUrl = rawBorealisFileUrl(normalizedBranch, "Agent.sh");
  const repoBranchArgs = usesDefaultBranch ? "" : `--repo-branch ${quoteShellValue(normalizedBranch)} `;
  const urlArg = usesDefaultBranch ? agentUrl : quoteShellValue(agentUrl);
  const launchArgs = `${repoBranchArgs}deploy --serverurl ` +
    `"${escapeShellDoubleQuoted(normalizedServerUrl)}" --enrollmentcode ` +
    `"${escapeShellDoubleQuoted(normalizedEnrollmentCode)}"`;
  if (osId === "macos") {
    return `curl -fsSL ${urlArg} | bash -s -- ${launchArgs}`;
  }
  return `curl -fsSL ${urlArg} | { if [ "$(id -u)" -eq 0 ]; then bash -s -- ${launchArgs}; else sudo bash -s -- ${launchArgs}; fi; }`;
}

function SiteDeleteDialog({ open, onCancel, onConfirm, sites }) {
  const siteNames = Array.isArray(sites) ? sites.map((site) => site?.name).filter(Boolean) : [];
  const previewNames = siteNames.slice(0, 4);
  const remainingCount = Math.max(siteNames.length - previewNames.length, 0);
  const deleteLabel = "Delete Site(s)";

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: MAGIC_UI.textBright }}>
            {deleteLabel}
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: MAGIC_UI.textMuted }}>
            Permanently remove the selected site records from Borealis.
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
        {previewNames.length ? (
          <Box
            sx={{
              mt: 0.5,
              borderRadius: 2.5,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(7,12,24,0.82)",
              px: 1.5,
              py: 1.35,
            }}
          >
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", fontWeight: 700, letterSpacing: 0.75, textTransform: "uppercase" }}>
              Selected Sites
            </Typography>
            <Box sx={{ mt: 1.2, display: "flex", flexWrap: "wrap", gap: 0.9 }}>
              {previewNames.map((name) => (
                <Box
                  key={name}
                  sx={{
                    borderRadius: 999,
                    border: "1px solid rgba(148,163,184,0.26)",
                    background: "rgba(15,23,42,0.76)",
                    px: 1.2,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.84rem", fontWeight: 500 }}>
                    {name}
                  </Typography>
                </Box>
              ))}
              {remainingCount > 0 ? (
                <Box
                  sx={{
                    borderRadius: 999,
                    border: "1px solid rgba(244,63,94,0.26)",
                    background: "rgba(44,8,22,0.38)",
                    px: 1.2,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: "#ffb1b9", fontSize: "0.84rem", fontWeight: 600 }}>
                    +{remainingCount} more
                  </Typography>
                </Box>
              ) : null}
            </Box>
          </Box>
        ) : null}
      </DialogContent>
      <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
        <Button onClick={onCancel} sx={SITE_DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onConfirm} disabled={!siteNames.length} sx={SITE_DIALOG_DANGER_BUTTON_SX}>
          {deleteLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

function InstallBranchDialog({
  open,
  rows,
  loading,
  error,
  draftBranch,
  onDraftBranchChange,
  onRefresh,
  onCancel,
  onApply,
}) {
  const branchGridRef = useRef(null);
  const branchGridApiRef = useRef(null);
  const normalizedDraftBranch = normalizeInstallBranch(draftBranch);

  const selectDraftBranch = useCallback(() => {
    const api = branchGridApiRef.current || branchGridRef.current?.api;
    if (!api || typeof api.forEachNode !== "function") return;
    api.forEachNode((node) => {
      const name = String(node?.data?.name || "");
      node.setSelected?.(name === normalizedDraftBranch);
    });
  }, [normalizedDraftBranch]);

  useEffect(() => {
    if (!open) {
      branchGridApiRef.current = null;
      return undefined;
    }
    const handle = setTimeout(selectDraftBranch, 0);
    return () => clearTimeout(handle);
  }, [open, rows, selectDraftBranch]);

  const branchColumnDefs = useMemo(() => [
    {
      headerName: "Branch",
      field: "name",
      minWidth: 260,
      flex: 1,
      cellStyle: {
        display: "flex",
        alignItems: "center",
      },
      cellRenderer: (params) => (
        <Typography sx={{ color: "#58a6ff", fontSize: "0.88rem", fontWeight: 600, lineHeight: 1.2 }}>
          {params.value}
        </Typography>
      ),
    },
    {
      headerName: "Commit",
      field: "sha",
      minWidth: 130,
      maxWidth: 150,
      valueFormatter: (params) => String(params.value || "").slice(0, 12),
    },
  ], []);

  const branchDefaultColDef = useMemo(() => ({
    sortable: true,
    filter: "agTextColumnFilter",
    resizable: true,
  }), []);

  const branchRowSelection = useMemo(
    () => ({
      mode: "singleRow",
      checkboxes: true,
      headerCheckbox: false,
      enableClickSelection: true,
    }),
    []
  );

  const branchSelectionColumnDef = useMemo(
    () => ({
      headerName: "",
      minWidth: 52,
      width: 52,
      maxWidth: 52,
      pinned: "left",
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

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="md" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: MAGIC_UI.textBright }}>
            Switch Branch
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: MAGIC_UI.textMuted }}>
            Selected branch: {normalizedDraftBranch}
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
        {error ? (
          <Alert severity="error" sx={{ mb: 1.5 }}>
            {error}
          </Alert>
        ) : null}
        {loading ? (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, mb: 1.5, color: MAGIC_UI.textMuted }}>
            <CircularProgress size={16} color="inherit" />
            <Typography sx={{ fontSize: "0.84rem", color: "inherit" }}>Loading branches</Typography>
          </Box>
        ) : null}
        <Box
          className={themeClassName}
          sx={{
            height: 440,
            minHeight: 320,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              background: "rgba(7,12,24,0.82)",
            },
            "& .ag-header": {
              backgroundColor: "rgba(15,23,42,0.9)",
              borderBottom: "1px solid rgba(148,163,184,0.25)",
            },
            "& .ag-row-selected": {
              backgroundColor: "rgba(125,211,252,0.2) !important",
              boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
            },
          }}
        >
          <AgGridReact
            ref={branchGridRef}
            rowData={rows}
            columnDefs={branchColumnDefs}
            defaultColDef={branchDefaultColDef}
            rowSelection={branchRowSelection}
            selectionColumnDef={branchSelectionColumnDef}
            suppressCellFocus
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
            animateRows
            getRowId={(params) => String(params.data?.name || "")}
            onGridReady={(params) => {
              branchGridApiRef.current = params.api;
              selectDraftBranch();
            }}
            onFirstDataRendered={selectDraftBranch}
            onRowDataUpdated={selectDraftBranch}
            onSelectionChanged={() => {
              const api = branchGridApiRef.current || branchGridRef.current?.api;
              const selected = api?.getSelectedRows?.()?.[0]?.name;
              if (selected) {
                onDraftBranchChange(selected);
              }
            }}
            theme={myTheme}
          />
        </Box>
      </DialogContent>
      <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
        <Button onClick={onRefresh} disabled={loading} sx={SITE_DIALOG_BUTTON_SX}>Refresh</Button>
        <Button onClick={onCancel} sx={SITE_DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onApply} disabled={!normalizedDraftBranch || loading} sx={SITE_DIALOG_BUTTON_SX}>
          Apply
        </Button>
      </DialogActions>
    </Dialog>
  );
}

const PAGE_TITLE = "Sites";
const PAGE_SUBTITLE = "Manage site enrollment codes and open device inventories by site.";
const PAGE_ICON = LocationCityIcon;

export default function SiteList() {
  const loaderData = useLoaderData();
  const navigate = useNavigate();
  const initialRows = Array.isArray(loaderData?.rows) ? loaderData.rows : [];
  const initialInstallServerUrl = String(loaderData?.installServerUrl || "");
  const [rows, setRows] = useState(() => initialRows);
  const [installServerUrl, setInstallServerUrl] = useState(() => initialInstallServerUrl);
  const [loadError, setLoadError] = useState(() => String(loaderData?.initialError || ""));
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [renameSiteId, setRenameSiteId] = useState(null);
  const [siteContextMenu, setSiteContextMenu] = useState({ open: false, top: 0, left: 0, row: null });
  const [installMenuAnchorEl, setInstallMenuAnchorEl] = useState(null);
  const [installMenuSite, setInstallMenuSite] = useState(null);
  const [selectedInstallBranch, setSelectedInstallBranch] = useState(DEFAULT_INSTALL_BRANCH);
  const [draftInstallBranch, setDraftInstallBranch] = useState(DEFAULT_INSTALL_BRANCH);
  const [branchDialogOpen, setBranchDialogOpen] = useState(false);
  const [branchRows, setBranchRows] = useState([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchLoadError, setBranchLoadError] = useState("");
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);
  const autoSizeHandleRef = useRef(null);
  const notify = useAppNotifications({
    title: PAGE_TITLE,
    icon: "locationcity",
    variant: "info",
  });
  const sendNotification = useCallback(
    async (message) => {
      await notify(message);
    },
    [notify]
  );

  const handleOpenDevicesForSite = useCallback(
    (site) => {
      const siteId = site?.id;
      if (siteId == null || siteId === "") return;
      navigate(`${APP_PATHS.devices}?site=${encodeURIComponent(String(siteId))}`);
    },
    [navigate]
  );

  const handleOpenSoftwareAuditForSite = useCallback(
    (site) => {
      const siteId = site?.id;
      if (siteId == null || siteId === "") return;
      navigate(`${APP_PATHS.software}?site=${encodeURIComponent(String(siteId))}`);
    },
    [navigate]
  );

  const fetchInstallServerUrlFromOverview = useCallback(async () => {
    try {
      const response = await fetch("/api/server/overview", {
        credentials: "include",
        cache: "no-store",
      });
      if (!response.ok) {
        return "";
      }
      const payload = await response.json().catch(() => ({}));
      return deriveInstallServerUrl({
        public_base_url: payload?.host?.public_base_url || payload?.public_edge?.public_base_url || "",
        public_hostname: payload?.host?.public_hostname || payload?.public_edge?.fqdn || "",
      });
    } catch {
      return "";
    }
  }, []);

  const fetchSites = useCallback(async () => {
    try {
      const res = await fetch("/api/sites");
      const data = await res.json();
      const nextInstallServerUrl = deriveInstallServerUrl(data) || await fetchInstallServerUrlFromOverview();
      setRows(Array.isArray(data?.sites) ? data.sites : []);
      setInstallServerUrl(nextInstallServerUrl);
      setLoadError("");
    } catch {
      setRows([]);
      setInstallServerUrl("");
      setLoadError("Unable to load sites.");
    }
  }, [fetchInstallServerUrlFromOverview]);

  const fetchInstallBranches = useCallback(async () => {
    setBranchesLoading(true);
    setBranchLoadError("");
    try {
      const tokenRes = await fetch("/api/github/token", {
        credentials: "include",
        cache: "no-store",
      });
      const tokenData = await tokenRes.json().catch(() => ({}));
      if (!tokenRes.ok) {
        const message = tokenData?.message || tokenData?.error || `GitHub token lookup failed (HTTP ${tokenRes.status}).`;
        throw new Error(message);
      }
      const githubToken = String(tokenData?.token || "").trim();
      if (!githubToken) {
        throw new Error(tokenData?.message || "GitHub API token is unavailable.");
      }

      const nextRows = [];
      for (let page = 1; page <= 10; page += 1) {
        const branchRes = await fetch(`${GITHUB_BRANCHES_API_URL}?per_page=100&page=${page}`, {
          cache: "no-store",
          headers: {
            Accept: "application/vnd.github+json",
            Authorization: `Bearer ${githubToken}`,
          },
        });
        if (!branchRes.ok) {
          const body = await branchRes.text().catch(() => "");
          throw new Error(`GitHub branch lookup failed (HTTP ${branchRes.status})${body ? `: ${body.slice(0, 180)}` : ""}`);
        }
        const pageRows = await branchRes.json().catch(() => []);
        if (!Array.isArray(pageRows)) {
          throw new Error("GitHub branch lookup returned an unexpected payload.");
        }
        pageRows.forEach((branch) => {
          const name = String(branch?.name || "").trim();
          if (!name) return;
          nextRows.push({
            name,
            sha: String(branch?.commit?.sha || "").trim(),
            protected: Boolean(branch?.protected),
            default: name === DEFAULT_INSTALL_BRANCH,
          });
        });
        if (pageRows.length < 100) {
          break;
        }
      }
      nextRows.sort((a, b) => {
        if (a.name === DEFAULT_INSTALL_BRANCH) return -1;
        if (b.name === DEFAULT_INSTALL_BRANCH) return 1;
        return a.name.localeCompare(b.name);
      });
      setBranchRows(nextRows);
      if (!nextRows.length) {
        setBranchLoadError("No GitHub branches returned.");
      }
    } catch (error) {
      setBranchRows([]);
      setBranchLoadError(error instanceof Error ? error.message : "GitHub branch lookup failed.");
    } finally {
      setBranchesLoading(false);
    }
  }, []);

  useEffect(() => {
    setRows(initialRows);
    setInstallServerUrl(initialInstallServerUrl);
    setLoadError(String(loaderData?.initialError || ""));
  }, [initialInstallServerUrl, initialRows, loaderData]);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api || !rows.length) return;
    const doSize = () => {
      autoSizeHandleRef.current = null;
      const liveApi = gridApiRef.current || gridRef.current?.api || api;
      if (!liveApi) return;
      if (typeof liveApi.isDestroyed === "function" && liveApi.isDestroyed()) return;
      try {
        liveApi.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {}
    };
    if (autoSizeHandleRef.current != null) {
      if (typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(autoSizeHandleRef.current);
      } else {
        clearTimeout(autoSizeHandleRef.current);
      }
      autoSizeHandleRef.current = null;
    }
    if (typeof requestAnimationFrame === "function") {
      autoSizeHandleRef.current = requestAnimationFrame(doSize);
    } else {
      autoSizeHandleRef.current = setTimeout(doSize, 0);
    }
  }, [rows.length]);

  useEffect(() => {
    autoSizeColumns();
  }, [rows, autoSizeColumns]);

  useEffect(() => {
    return () => {
      if (autoSizeHandleRef.current != null) {
        if (typeof cancelAnimationFrame === "function") {
          cancelAnimationFrame(autoSizeHandleRef.current);
        } else {
          clearTimeout(autoSizeHandleRef.current);
        }
        autoSizeHandleRef.current = null;
      }
      gridApiRef.current = null;
    };
  }, []);

  const copyTextToClipboard = useCallback(async (value, promptTitle = "Copy text") => {
    const normalizedValue = String(value || "").trim();
    if (!normalizedValue) {
      return false;
    }
    try {
      await navigator.clipboard.writeText(normalizedValue);
      return true;
    } catch {
      window.prompt(promptTitle, normalizedValue);
      return false;
    }
  }, []);

  const handleCopy = useCallback(async (code, siteName = "") => {
    const value = (code || "").trim();
    if (!value) return;
    const normalizedSiteName = String(siteName || "Unknown Site").trim() || "Unknown Site";
    const copied = await copyTextToClipboard(value, "Copy enrollment code");
    if (copied) {
      await sendNotification({
        title: "Enrollment Code Copied",
        message: `Enrollment code for <b>${normalizedSiteName}</b> copied to clipboard.`,
        icon: "done",
        variant: "info",
      });
      return;
    }

    await sendNotification({
      title: "Manual Copy Required",
      message: `Clipboard access was blocked, so Borealis opened a manual copy prompt for the enrollment code at <b>${normalizedSiteName}</b>.`,
      icon: "warning",
      variant: "warning",
    });
  }, [copyTextToClipboard, sendNotification]);

  const handleCloseInstallMenu = useCallback(() => {
    setInstallMenuAnchorEl(null);
    setInstallMenuSite(null);
  }, []);

  const handleOpenBranchDialog = useCallback(() => {
    setDraftInstallBranch(selectedInstallBranch);
    setBranchDialogOpen(true);
    handleCloseInstallMenu();
    void fetchInstallBranches();
  }, [fetchInstallBranches, handleCloseInstallMenu, selectedInstallBranch]);

  const handleCloseBranchDialog = useCallback(() => {
    setBranchDialogOpen(false);
    setDraftInstallBranch(selectedInstallBranch);
  }, [selectedInstallBranch]);

  const handleApplyInstallBranch = useCallback(async () => {
    const nextBranch = normalizeInstallBranch(draftInstallBranch);
    setSelectedInstallBranch(nextBranch);
    setDraftInstallBranch(nextBranch);
    setBranchDialogOpen(false);
    await sendNotification({
      title: "Install Branch Updated",
      message: `Agent install commands now target <b>${nextBranch}</b>.`,
      icon: "done",
      variant: "info",
    });
  }, [draftInstallBranch, sendNotification]);

  const handleCopyInstallCommand = useCallback(async (osId, site) => {
    const siteName = String(site?.name || "Unknown Site").trim() || "Unknown Site";
    const enrollmentCode = String(site?.enrollment_code || "").trim();
    const osLabel = INSTALL_OS_OPTIONS.find((option) => option.id === osId)?.label || "Agent";
    const command = buildInstallCommand(osId, installServerUrl, enrollmentCode, selectedInstallBranch);

    if (!command) {
      await sendNotification({
        title: "Install Command Unavailable",
        message: `Borealis could not build the <b>${osLabel}</b> install command for <b>${siteName}</b> because the public engine URL or enrollment code is unavailable.`,
        icon: "warning",
        variant: "error",
      });
      return;
    }

    const copied = await copyTextToClipboard(command, `Copy ${osLabel} install command`);
    if (copied) {
      await sendNotification({
        title: "Install Command Copied",
        message: `Agent installation command for <b>${osLabel}</b> at <b>${siteName}</b> copied to clipboard.`,
        icon: "done",
        variant: "info",
      });
      return;
    }

    await sendNotification({
      title: "Manual Copy Required",
      message: `Clipboard access was blocked, so Borealis opened a manual copy prompt for the <b>${osLabel}</b> install command at <b>${siteName}</b>.`,
      icon: "warning",
      variant: "warning",
    });
  }, [copyTextToClipboard, installServerUrl, selectedInstallBranch, sendNotification]);

  const handleSelectInstallOs = useCallback(async (osId) => {
    const activeSite = installMenuSite;
    handleCloseInstallMenu();
    if (!activeSite) {
      return;
    }
    await handleCopyInstallCommand(osId, activeSite);
  }, [handleCloseInstallMenu, handleCopyInstallCommand, installMenuSite]);

  const openRenameDialog = useCallback((siteOverride = null) => {
    const selId = siteOverride?.id ?? (selectedIds.size === 1 ? Array.from(selectedIds)[0] : null);
    if (selId == null) return;
    const site = siteOverride || rows.find((row) => row.id === selId);
    setRenameSiteId(selId);
    setRenameValue(site?.name || "");
    setRenameOpen(true);
  }, [rows, selectedIds]);

  const getRowId = useCallback((params) => String(params.data?.id ?? ""), []);

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableClickSelection: true,
      enableSelectionWithoutKeys: true,
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

  const columnDefs = useMemo(() => [
    {
      headerName: "Name",
      field: "name",
      minWidth: 220,
      flex: 1,
      cellRendererParams: {
        suppressMouseEventHandling: () => true,
      },
      cellRenderer: (params) => (
        <span
          style={{ color: "#58a6ff", cursor: "pointer", fontWeight: 500 }}
          onMouseDown={stopGridRowSelectionEvent}
          onClick={(event) => {
            stopGridRowSelectionEvent(event);
            handleOpenDevicesForSite(params.data);
          }}
        >
          {params.value}
        </span>
      ),
    },
    {
      headerName: "Description",
      field: "description",
      minWidth: 220,
      flex: 1,
    },
    {
      headerName: "Devices",
      field: "device_count",
      minWidth: 140,
    },
    {
      headerName: "Agent Enrollment Code",
      field: "enrollment_code",
      minWidth: 260,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      cellRendererParams: {
        suppressMouseEventHandling: () => true,
      },
      cellRenderer: (params) => {
        const code = params.value || "—";
        const siteName = params?.data?.name || "";
        return (
          <Box
            sx={{ display: "flex", alignItems: "center", gap: 1 }}
            onMouseDown={stopGridRowSelectionEvent}
            onClick={stopGridRowSelectionEvent}
            onDoubleClick={stopGridRowSelectionEvent}
            onTouchStart={stopGridRowSelectionEvent}
          >
            <Typography variant="body2" sx={{ fontFamily: "monospace", color: "#9aa3ad" }}>
              {code}
            </Typography>
            <Tooltip title="Copy">
              <span>
                <IconButton
                  size="small"
                  onClick={(event) => {
                    stopGridRowSelectionEvent(event);
                    void handleCopy(code, siteName);
                  }}
                  disabled={!code || code === "—"}
                  sx={{ color: MAGIC_UI.textMuted }}
                >
                  <ContentCopyIcon fontSize="small" />
                </IconButton>
              </span>
            </Tooltip>
          </Box>
        );
      },
    },
  ], [handleCopy, handleOpenDevicesForSite]);

  const defaultColDef = useMemo(() => ({
    sortable: true,
    filter: "agTextColumnFilter",
    resizable: true,
    minWidth: 160,
  }), []);

  const selectedSiteRows = useMemo(
    () => rows.filter((row) => row?.id != null && selectedIds.has(row.id)),
    [rows, selectedIds]
  );

  const singleSelectedSite = useMemo(
    () => (selectedSiteRows.length === 1 ? selectedSiteRows[0] : null),
    [selectedSiteRows]
  );

  const hasSelectedSites = selectedSiteRows.length > 0;
  const canOpenInstallMenu = Boolean(singleSelectedSite && installServerUrl && singleSelectedSite?.enrollment_code);
  const installActionTooltip = selectedIds.size === 0
    ? "Select one site to copy an install command"
    : selectedIds.size > 1
      ? "Select exactly one site to copy an install command"
      : !installServerUrl
        ? "Borealis public engine URL is unavailable"
        : !singleSelectedSite?.enrollment_code
          ? "The selected site is missing an enrollment code"
          : undefined;

  const handleOpenInstallMenu = useCallback((event) => {
    if (!singleSelectedSite) return;
    event?.preventDefault?.();
    event?.stopPropagation?.();
    setInstallMenuAnchorEl(event.currentTarget);
    setInstallMenuSite(singleSelectedSite);
  }, [singleSelectedSite]);

  const handleOpenSiteContextMenu = useCallback((event, row, rowNode = null) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    if (rowNode && !rowNode.isSelected?.()) {
      rowNode.setSelected?.(true, true);
    }
    setSiteContextMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row: row || null,
    });
  }, []);

  const handleCloseSiteContextMenu = useCallback(() => {
    setSiteContextMenu({ open: false, top: 0, left: 0, row: null });
  }, []);

  const handleOpenDeleteDialog = useCallback((siteOverride = null) => {
    handleCloseSiteContextMenu();
    if (siteOverride?.id != null && !selectedIds.has(siteOverride.id)) {
      setSelectedIds(new Set([siteOverride.id]));
      setDeleteOpen(true);
      return;
    }
    if (!hasSelectedSites) return;
    setDeleteOpen(true);
  }, [handleCloseSiteContextMenu, hasSelectedSites, selectedIds]);

  useEffect(() => {
    if (!hasSelectedSites) {
      setInstallMenuAnchorEl(null);
      setInstallMenuSite(null);
      return;
    }

    if (!singleSelectedSite) {
      setInstallMenuAnchorEl(null);
      setInstallMenuSite(null);
      return;
    }

    if (installMenuSite && String(installMenuSite.id) !== String(singleSelectedSite.id)) {
      setInstallMenuAnchorEl(null);
      setInstallMenuSite(null);
    }
  }, [hasSelectedSites, installMenuSite, singleSelectedSite]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "install-site-agent",
        label: "Install Agent(s)",
        icon: <DownloadRoundedIcon />,
        tone: "primary",
        disabled: !canOpenInstallMenu,
        tooltip: installActionTooltip,
        onClick: handleOpenInstallMenu,
      },
      {
        id: "onboard-site-devices",
        label: "Onboard Devices",
        icon: <DevicesIcon />,
        tone: "primary",
        onClick: () => navigate(APP_PATHS.jobOnboardingNew),
      },
      {
        id: "create-site",
        label: "Create Site",
        icon: <AddIcon />,
        tone: "primary",
        onClick: () => setCreateOpen(true),
      },
    ],
    [
      canOpenInstallMenu,
      handleOpenInstallMenu,
      installActionTooltip,
      navigate,
    ]
  );

  const siteContextRow = siteContextMenu.row || null;
  const siteContextSubtitle = siteContextRow
    ? `${Number(siteContextRow.device_count || 0).toLocaleString()} Device${Number(siteContextRow.device_count || 0) === 1 ? "" : "s"}`
    : "Site";
  const siteContextActions = useMemo(() => {
    const row = siteContextMenu.row || null;
    const unavailableReason = row ? "" : "Select a site first.";
    const enrollmentCode = String(row?.enrollment_code || "").trim();
    const deleteTargetCount = row?.id != null && selectedIds.has(row.id) ? selectedSiteRows.length : row ? 1 : 0;
    return [
      {
        id: "display-site-devices",
        group: "primary",
        label: "Display Site Devices",
        icon: DevicesRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Open Devices with this site filter applied.",
        onClick: () => {
          handleCloseSiteContextMenu();
          handleOpenDevicesForSite(row);
        },
      },
      {
        id: "display-software-audit",
        group: "primary",
        label: "Display Software Audit",
        icon: AssessmentRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Open Software Audit filtered to this site.",
        onClick: () => {
          handleCloseSiteContextMenu();
          handleOpenSoftwareAuditForSite(row);
        },
      },
      {
        id: "copy-enrollment-code",
        group: "primary",
        label: "Copy Site Enrollment Code",
        icon: ContentCopyIcon,
        disabled: Boolean(unavailableReason) || !enrollmentCode,
        disabledReason: unavailableReason || (!enrollmentCode ? "This site is missing an enrollment code." : ""),
        description: "Copy the agent enrollment code for this site.",
        onClick: () => {
          handleCloseSiteContextMenu();
          void handleCopy(enrollmentCode, row?.name || "");
        },
      },
      {
        id: "rename-site",
        group: "organize",
        label: "Rename",
        icon: DriveFileRenameOutlineRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Rename this site record.",
        onClick: () => {
          handleCloseSiteContextMenu();
          openRenameDialog(row);
        },
      },
      {
        id: "delete-site",
        group: "danger",
        label: deleteTargetCount > 1 ? "Delete Selected Sites" : "Delete",
        icon: DeleteRoundedIcon,
        intent: "danger",
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description:
          deleteTargetCount > 1
            ? `Delete ${deleteTargetCount} selected site records.`
            : "Delete this site record.",
        onClick: () => handleOpenDeleteDialog(row),
      },
    ];
  }, [
    handleCloseSiteContextMenu,
    handleCopy,
    handleOpenDeleteDialog,
    handleOpenDevicesForSite,
    handleOpenSoftwareAuditForSite,
    openRenameDialog,
    selectedSiteRows.length,
    selectedIds,
    siteContextMenu.row,
  ]);
  const groupedSiteContextActions = useMemo(
    () =>
      SITE_CONTEXT_MENU_GROUP_ORDER.map((groupId) => ({
        id: groupId,
        label: SITE_CONTEXT_MENU_GROUP_LABELS[groupId],
        actions: siteContextActions.filter((action) => action.group === groupId),
      })).filter((group) => group.actions.length),
    [siteContextActions]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: PAGE_ICON,
    actions: pageHeaderActions,
  });

  return (
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
      <Menu
        anchorEl={installMenuAnchorEl}
        open={Boolean(installMenuAnchorEl)}
        onClose={handleCloseInstallMenu}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        PaperProps={{ sx: INSTALL_MENU_PAPER_SX }}
      >
        {INSTALL_OS_OPTIONS.map((option) => (
          <MenuItem
            key={option.id}
            onClick={() => void handleSelectInstallOs(option.id)}
            sx={INSTALL_MENU_ITEM_SX}
          >
            {option.label}
          </MenuItem>
        ))}
        <Divider sx={{ my: 0.55, borderColor: "rgba(148,163,184,0.16)" }} />
        <MenuItem onClick={handleOpenBranchDialog} sx={{ ...INSTALL_MENU_ITEM_SX, alignItems: "flex-start", gap: 1 }}>
          <AltRouteRoundedIcon sx={{ mt: 0.15, fontSize: 18, color: "rgba(226,232,240,0.92)" }} />
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.92rem", fontWeight: 700, lineHeight: 1.2 }}>
              Switch Branch
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.72rem", lineHeight: 1.25, mt: 0.25 }}>
              Current: {selectedInstallBranch}
            </Typography>
          </Box>
        </MenuItem>
      </Menu>
      <Menu
        open={Boolean(siteContextMenu.open)}
        onClose={handleCloseSiteContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={siteContextMenu.open ? { top: siteContextMenu.top, left: siteContextMenu.left } : undefined}
        PaperProps={{ sx: SITE_CONTEXT_MENU_PAPER_SX }}
      >
        <Box component="li" role="presentation" sx={SITE_CONTEXT_MENU_HEADER_SX}>
          <Box sx={SITE_CONTEXT_MENU_HEADER_ICON_SX}>
            <LocationCityIcon sx={{ fontSize: 19, color: "currentColor" }} />
          </Box>
          <Box sx={{ minWidth: 0 }}>
            <Tooltip title={siteContextRow?.name || "Site"} placement="top-start">
              <Typography
                sx={{
                  ...SITE_CONTEXT_MENU_TRUNCATE_SX,
                  color: MAGIC_UI.textBright,
                  fontSize: "0.88rem",
                  fontWeight: 600,
                  lineHeight: 1.2,
                  maxWidth: 240,
                }}
              >
                {siteContextRow?.name || "Site"}
              </Typography>
            </Tooltip>
            <Tooltip title={siteContextSubtitle} placement="top-start">
              <Typography
                sx={{
                  ...SITE_CONTEXT_MENU_TRUNCATE_SX,
                  color: "rgba(148,163,184,0.82)",
                  fontSize: "0.73rem",
                  lineHeight: 1.25,
                  mt: 0.22,
                  maxWidth: 240,
                }}
              >
                {siteContextSubtitle}
              </Typography>
            </Tooltip>
          </Box>
        </Box>
        {groupedSiteContextActions.map((group) => (
          <React.Fragment key={group.id}>
            <Divider component="li" sx={SITE_CONTEXT_MENU_DIVIDER_SX} />
            <Box component="li" role="presentation" sx={SITE_CONTEXT_MENU_SECTION_LABEL_SX}>{group.label}</Box>
            {group.actions.map((action) => {
              const Icon = action.icon;
              const helperText = action.disabledReason || action.description || "";
              return (
                <MenuItem
                  key={action.id}
                  disabled={Boolean(action.disabled)}
                  onClick={() => action.onClick?.()}
                  sx={action.intent === "danger" ? SITE_CONTEXT_MENU_DANGER_ITEM_SX : SITE_CONTEXT_MENU_ITEM_SX}
                >
                  <Icon
                    sx={{
                      ...SITE_CONTEXT_MENU_ROW_ICON_SX,
                      color: action.intent === "danger" ? "rgba(248,113,113,0.92)" : "rgba(226,232,240,0.92)",
                    }}
                  />
                  <Box
                    sx={{
                      flex: 1,
                      minWidth: 0,
                      display: "flex",
                      flexDirection: "column",
                      justifyContent: helperText ? "flex-start" : "center",
                    }}
                  >
                    <Typography sx={SITE_CONTEXT_MENU_LABEL_SX}>{action.label}</Typography>
                    {helperText ? <Typography sx={SITE_CONTEXT_MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
                  </Box>
                </MenuItem>
              );
            })}
          </React.Fragment>
        ))}
      </Menu>
      <PageBodyFrame variant="grid">
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          {loadError ? (
            <Alert severity="error" sx={{ mx: 2, mt: 2, mb: 0 }}>
              {loadError}
            </Alert>
          ) : null}
          <Box
            className={themeClassName}
            sx={{
              flexGrow: 1,
              minHeight: 0,
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
              suppressContextMenu
              preventDefaultOnContextMenu
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              animateRows
              getRowId={getRowId}
              onGridReady={(params) => {
                gridApiRef.current = params.api;
                autoSizeColumns();
              }}
              onGridPreDestroyed={() => {
                gridApiRef.current = null;
                if (autoSizeHandleRef.current != null) {
                  if (typeof cancelAnimationFrame === "function") {
                    cancelAnimationFrame(autoSizeHandleRef.current);
                  } else {
                    clearTimeout(autoSizeHandleRef.current);
                  }
                  autoSizeHandleRef.current = null;
                }
              }}
              onSelectionChanged={() => {
                const api = gridApiRef.current || gridRef.current?.api;
                if (!api) return;
                const selected = api.getSelectedNodes().map((n) => n.data?.id).filter((id) => id != null);
                setSelectedIds(new Set(selected));
              }}
              onCellContextMenu={(params) => handleOpenSiteContextMenu(params.event, params.data, params.node)}
              theme={myTheme}
            />
          </Box>
        </Box>
      </PageBodyFrame>

      <CreateSiteDialog
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onCreate={async (name, description) => {
          try {
            const res = await fetch("/api/sites", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name, description }),
            });
            if (res.ok) {
              setCreateOpen(false);
              if (name) {
                sendNotification(`Site ${name} Created Successfully`);
              }
              fetchSites();
            }
          } catch {}
        }}
      />

      <SiteDeleteDialog
        open={deleteOpen}
        onCancel={() => setDeleteOpen(false)}
        sites={selectedSiteRows}
        onConfirm={async () => {
          try {
            const ids = selectedSiteRows.map((row) => row.id);
            const selectedNames = selectedSiteRows.map((row) => row?.name).filter(Boolean);
            const resp = await fetch("/api/sites/delete", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ ids }),
            });
            if (resp.ok) {
              selectedNames.forEach((name) => sendNotification(`Site ${name} Deleted Successfully`));
            }
          } catch {}
          setDeleteOpen(false);
          setSelectedIds(new Set());
          fetchSites();
        }}
      />

      <RenameSiteDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => {
          setRenameOpen(false);
          setRenameSiteId(null);
        }}
        onSave={async () => {
          const newName = (renameValue || "").trim();
          if (!newName) return;
          const selId = renameSiteId ?? (selectedIds.size === 1 ? Array.from(selectedIds)[0] : null);
          if (!selId) return;
          const oldName = rows.find((r) => r.id === selId)?.name || "Site";
          try {
            const res = await fetch("/api/sites/rename", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ id: selId, new_name: newName }),
            });
            if (res.ok) {
              setRenameOpen(false);
              setRenameSiteId(null);
              sendNotification(`Site ${oldName} Renamed as ${newName} Successfully`);
              fetchSites();
            }
          } catch {}
        }}
      />

      <InstallBranchDialog
        open={branchDialogOpen}
        rows={branchRows}
        loading={branchesLoading}
        error={branchLoadError}
        draftBranch={draftInstallBranch}
        onDraftBranchChange={setDraftInstallBranch}
        onRefresh={() => void fetchInstallBranches()}
        onCancel={handleCloseBranchDialog}
        onApply={() => void handleApplyInstallBranch()}
      />
    </Paper>
  );
}
