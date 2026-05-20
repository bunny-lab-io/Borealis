import React, { useCallback, useEffect, useMemo, useRef } from "react";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  Typography,
} from "@mui/material";
import { AgGridReact } from "ag-grid-react";

export const DEFAULT_AGENT_BRANCH = "main";
export const BOREALIS_GITHUB_REPO = "bunny-lab-io/Borealis";
export const GITHUB_BRANCHES_API_URL = `https://api.github.com/repos/${BOREALIS_GITHUB_REPO}/branches`;

const TEXT_BRIGHT = "#e2e8f0";
const TEXT_MUTED = "#94a3b8";
const PANEL_BORDER = "rgba(148, 163, 184, 0.35)";
const GRID_FONT_FAMILY = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const ICON_FONT_FAMILY = '"Quartz Regular"';

const DIALOG_PAPER_SX = {
  borderRadius: 3,
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), rgba(8,12,24,0.96)",
  backdropFilter: "blur(18px)",
  border: `1px solid ${PANEL_BORDER}`,
  boxShadow: "0 24px 60px rgba(2,8,23,0.72)",
  color: TEXT_BRIGHT,
  overflow: "hidden",
};

const BUTTON_SX = {
  borderRadius: 999,
  px: 2,
  minHeight: 38,
  textTransform: "none",
  fontWeight: 600,
  fontSize: "0.92rem",
  color: TEXT_BRIGHT,
  border: `1px solid ${PANEL_BORDER}`,
  background: "rgba(5,10,24,0.84)",
  "&:hover": {
    background: "rgba(8,14,30,0.92)",
    borderColor: "rgba(125,211,252,0.46)",
  },
};

export function normalizeAgentReleaseChannel(value) {
  const text = String(value || "").trim().toLowerCase();
  if (["source", "branch", "repo", "repository", "unstable"].includes(text)) return "unstable";
  if (["release", "releases", "stable", ""].includes(text)) return "stable";
  return text;
}

export function normalizeAgentBranch(channel, branch) {
  const normalizedChannel = normalizeAgentReleaseChannel(channel);
  if (normalizedChannel !== "unstable") return DEFAULT_AGENT_BRANCH;
  return String(branch || "").trim() || DEFAULT_AGENT_BRANCH;
}

export async function fetchAgentBranchRows() {
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

  const rows = [];
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
      rows.push({
        name,
        sha: String(branch?.commit?.sha || "").trim(),
        protected: Boolean(branch?.protected),
        default: name === DEFAULT_AGENT_BRANCH,
      });
    });
    if (pageRows.length < 100) break;
  }
  rows.sort((a, b) => {
    if (a.name === DEFAULT_AGENT_BRANCH) return -1;
    if (b.name === DEFAULT_AGENT_BRANCH) return 1;
    return a.name.localeCompare(b.name);
  });
  return rows;
}

export default function AgentBranchChannelDialog({
  open,
  title = "Switch Agent Branch/Channel",
  subtitle = "",
  rows = [],
  loading = false,
  error = "",
  channel = "stable",
  branch = DEFAULT_AGENT_BRANCH,
  mixed = false,
  busy = false,
  applyLabel = "Apply",
  onChannelChange,
  onBranchChange,
  onRefresh,
  onCancel,
  onApply,
  gridTheme = null,
}) {
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);
  const normalizedChannel = mixed ? "mixed" : normalizeAgentReleaseChannel(channel);
  const unstableSelected = normalizedChannel === "unstable";
  const normalizedBranch = normalizeAgentBranch(normalizedChannel, branch);

  const selectDraftBranch = useCallback(() => {
    if (!unstableSelected) return;
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api || typeof api.forEachNode !== "function") return;
    api.forEachNode((node) => {
      const name = String(node?.data?.name || "");
      node.setSelected?.(name === normalizedBranch);
    });
  }, [normalizedBranch, unstableSelected]);

  useEffect(() => {
    if (!open) {
      gridApiRef.current = null;
      return undefined;
    }
    const handle = setTimeout(selectDraftBranch, 0);
    return () => clearTimeout(handle);
  }, [open, rows, selectDraftBranch]);

  const columnDefs = useMemo(() => [
    {
      headerName: "Branch",
      field: "name",
      minWidth: 260,
      flex: 1,
      cellStyle: { display: "flex", alignItems: "center" },
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

  const defaultColDef = useMemo(() => ({
    sortable: true,
    filter: "agTextColumnFilter",
    resizable: true,
  }), []);

  const rowSelection = useMemo(
    () => ({
      mode: "singleRow",
      checkboxes: true,
      headerCheckbox: false,
      enableClickSelection: true,
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

  const handleChannelChange = useCallback((event) => {
    const nextChannel = normalizeAgentReleaseChannel(event.target.value);
    onChannelChange?.(nextChannel);
    if (nextChannel !== "unstable") {
      onBranchChange?.(DEFAULT_AGENT_BRANCH);
    }
  }, [onBranchChange, onChannelChange]);

  const helper = normalizedChannel === "mixed"
    ? "Selected devices have mixed branch/channel settings."
    : normalizedChannel === "stable"
      ? "Stable always deploys main."
      : `Unstable branch: ${normalizedBranch}`;

  return (
    <Dialog open={open} onClose={busy ? undefined : onCancel} maxWidth="md" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={{ px: 3, pt: 3, pb: 0.75 }}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: TEXT_BRIGHT }}>
            {title}
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: TEXT_MUTED }}>
            {subtitle || helper}
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent sx={{ px: 3, pt: 1, pb: 2.5 }}>
        {error ? (
          <Alert severity="error" sx={{ mb: 1.5 }}>
            {error}
          </Alert>
        ) : null}
        <FormControl size="small" fullWidth sx={{ mb: 1.5 }}>
          <InputLabel sx={{ color: TEXT_MUTED }}>Release Channel</InputLabel>
          <Select
            label="Release Channel"
            value={normalizedChannel}
            disabled={busy}
            onChange={handleChannelChange}
            sx={{
              color: TEXT_BRIGHT,
              background: "rgba(5,10,24,0.84)",
              ".MuiOutlinedInput-notchedOutline": { borderColor: PANEL_BORDER },
              ".MuiSvgIcon-root": { color: TEXT_MUTED },
            }}
          >
            {normalizedChannel === "mixed" ? <MenuItem value="mixed" disabled>Mixed</MenuItem> : null}
            <MenuItem value="stable">Stable</MenuItem>
            <MenuItem value="unstable">Unstable</MenuItem>
          </Select>
        </FormControl>
        <Typography sx={{ mb: 1.2, color: TEXT_MUTED, fontSize: "0.82rem", lineHeight: 1.35 }}>
          Branch: {mixed ? "Mixed" : normalizedBranch}
        </Typography>
        {loading ? (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, mb: 1.5, color: TEXT_MUTED }}>
            <CircularProgress size={16} color="inherit" />
            <Typography sx={{ fontSize: "0.84rem", color: "inherit" }}>Loading branches</Typography>
          </Box>
        ) : null}
        <Box
          className={(gridTheme && gridTheme.themeName) || "ag-theme-quartz"}
          sx={{
            height: 420,
            minHeight: 300,
            opacity: unstableSelected ? 1 : 0.48,
            pointerEvents: unstableSelected && !busy ? "auto" : "none",
            "--ag-font-family": GRID_FONT_FAMILY,
            "--ag-icon-font-family": ICON_FONT_FAMILY,
            "& .ag-root-wrapper": {
              border: `1px solid ${PANEL_BORDER}`,
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
            getRowId={(params) => String(params.data?.name || "")}
            onGridReady={(params) => {
              gridApiRef.current = params.api;
              selectDraftBranch();
            }}
            onFirstDataRendered={selectDraftBranch}
            onRowDataUpdated={selectDraftBranch}
            onSelectionChanged={() => {
              const api = gridApiRef.current || gridRef.current?.api;
              const selected = api?.getSelectedRows?.()?.[0]?.name;
              if (selected) {
                onBranchChange?.(selected);
              }
            }}
            theme={gridTheme || undefined}
          />
        </Box>
      </DialogContent>
      <DialogActions sx={{ px: 3, pt: 0.5, pb: 2.5, gap: 1 }}>
        <Button onClick={onRefresh} disabled={loading || busy} sx={BUTTON_SX}>Refresh</Button>
        <Button onClick={onCancel} disabled={busy} sx={BUTTON_SX}>Cancel</Button>
        <Button
          onClick={onApply}
          disabled={busy || loading || !["stable", "unstable"].includes(normalizedChannel) || (unstableSelected && !normalizedBranch)}
          sx={BUTTON_SX}
        >
          {busy ? "Applying..." : applyLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
