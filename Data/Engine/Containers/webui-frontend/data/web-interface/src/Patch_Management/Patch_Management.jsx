import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  FormControlLabel,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import LockRoundedIcon from "@mui/icons-material/LockRounded";
import LockOpenRoundedIcon from "@mui/icons-material/LockOpenRounded";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import { AgGridReact } from "ag-grid-react";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { DEFAULT_GRID_COL_DEF, DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI } from "../Devices/Tabs/Shared.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";

const PAGE_ICON = SystemUpdateAltRoundedIcon;

const CLASS_KEYS = [
  "security",
  "critical",
  "cumulative",
  "definition",
  "driver",
  "feature",
  "optional",
  "service_pack",
  "update_rollup",
  "updates",
];

const TAB_BY_INDEX = ["catalog", "devices", "policies", "history"];

const ACTION_BUTTON_SX = {
  minHeight: 34,
  borderRadius: 999,
  px: 1.6,
  textTransform: "none",
  fontWeight: 650,
  color: MAGIC_UI.textBright,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
  },
};

const PRIMARY_BUTTON_SX = {
  ...ACTION_BUTTON_SX,
  border: "1px solid rgba(125,211,252,0.42)",
  background: "linear-gradient(90deg, rgba(14,165,233,0.78), rgba(147,51,234,0.72))",
};

const FIELD_SX = {
  "& .MuiOutlinedInput-root": {
    borderRadius: 2,
    background: "rgba(7, 12, 26, 0.82)",
    color: MAGIC_UI.textBright,
    "& fieldset": { borderColor: "rgba(148,163,184,0.28)" },
    "&:hover fieldset": { borderColor: "rgba(125,211,252,0.4)" },
    "&.Mui-focused fieldset": { borderColor: "rgba(125,211,252,0.62)" },
  },
  "& .MuiInputLabel-root": { color: "rgba(191,219,254,0.88)" },
  "& .MuiInputLabel-root.Mui-focused": { color: "#9bd7ff" },
};

function ts(value) {
  const parsed = Number(value || 0);
  if (!parsed) return "";
  return new Date(parsed * 1000).toLocaleString();
}

function classLabel(value) {
  return String(value || "")
    .replace(/_/g, " ")
    .replace(/\b\w/g, (ch) => ch.toUpperCase());
}

async function apiJson(path, options = {}) {
  const response = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data?.message || data?.error || `Request failed (${response.status})`);
  }
  return data;
}

export default function PatchManagementPage() {
  const notifyOperator = useAppNotifications({ title: "Patch Management", icon: "notification" });
  const [tab, setTab] = useState("catalog");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [catalog, setCatalog] = useState([]);
  const [devices, setDevices] = useState([]);
  const [policies, setPolicies] = useState([]);
  const [history, setHistory] = useState([]);
  const [selectedCatalog, setSelectedCatalog] = useState(null);
  const [policyDraft, setPolicyDraft] = useState(() => ({
    name: "",
    description: "",
    class_toggles: Object.fromEntries(CLASS_KEYS.map((key) => [key, true])),
  }));

  useRoutePageChrome({
    title: "Patch Management",
    subtitle: "Windows patch catalog, policy rings, compliance, and operator actions.",
    icon: PAGE_ICON,
  });

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [catalogData, deviceData, policyData, historyData] = await Promise.all([
        apiJson("/api/patch-management/catalog"),
        apiJson("/api/patch-management/devices"),
        apiJson("/api/patch-management/policies"),
        apiJson("/api/patch-management/history"),
      ]);
      setCatalog(catalogData.updates || []);
      setDevices(deviceData.devices || []);
      setPolicies(policyData.policies || []);
      setHistory(historyData.history || []);
    } catch (err) {
      setError(err.message || "Patch data failed to load.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    const socket = window.BorealisSocket;
    if (!socket?.on) return undefined;
    const handler = () => load();
    socket.on("patch_management_changed", handler);
    return () => {
      if (socket.off) socket.off("patch_management_changed", handler);
    };
  }, [load]);

  const catalogColumns = useMemo(
    () => [
      { field: "title", headerName: "Update", minWidth: 320, flex: 2 },
      { field: "kb_article_ids", headerName: "KBs", valueFormatter: ({ value }) => (value || []).join(", "), minWidth: 150 },
      { field: "classifications", headerName: "Class", valueFormatter: ({ value }) => (value || []).join(", "), minWidth: 180 },
      { field: "affected_devices", headerName: "Affected", width: 120, filter: "agNumberColumnFilter" },
      { field: "missing_count", headerName: "Missing", width: 120, filter: "agNumberColumnFilter" },
      { field: "installed_count", headerName: "Installed", width: 120, filter: "agNumberColumnFilter" },
      { field: "failed_count", headerName: "Failed", width: 110, filter: "agNumberColumnFilter" },
      { field: "pending_reboot_count", headerName: "Reboots", width: 120, filter: "agNumberColumnFilter" },
      { field: "held", headerName: "Held", width: 100 },
      { field: "last_state_at", headerName: "Last Seen", valueFormatter: ({ value }) => ts(value), minWidth: 170 },
    ],
    []
  );

  const deviceColumns = useMemo(
    () => [
      { field: "hostname", headerName: "Hostname", minWidth: 210 },
      { field: "site_name", headerName: "Site", minWidth: 160 },
      { field: "operating_system", headerName: "OS", minWidth: 220 },
      { field: "missing_count", headerName: "Missing", width: 120, filter: "agNumberColumnFilter" },
      { field: "installed_count", headerName: "Installed", width: 120, filter: "agNumberColumnFilter" },
      { field: "failed_count", headerName: "Failed", width: 110, filter: "agNumberColumnFilter" },
      { field: "pending_reboot_count", headerName: "Reboots", width: 120, filter: "agNumberColumnFilter" },
      { field: "last_scan_at", headerName: "Last Scan", valueFormatter: ({ value }) => ts(value), minWidth: 170 },
    ],
    []
  );

  const policyColumns = useMemo(
    () => [
      { field: "policy_name", headerName: "Policy", minWidth: 220 },
      { field: "scope_type", headerName: "Scope", width: 130 },
      { field: "site_id", headerName: "Site ID", width: 110 },
      { field: "device_guid", headerName: "Device GUID", minWidth: 240 },
      { field: "enabled", headerName: "Enabled", width: 110 },
      { field: "version", headerName: "Version", width: 120 },
    ],
    []
  );

  const historyColumns = useMemo(
    () => [
      { field: "hostname", headerName: "Hostname", minWidth: 190 },
      { field: "action", headerName: "Action", width: 130 },
      { field: "status", headerName: "Status", width: 150 },
      { field: "requested_by", headerName: "Operator", minWidth: 160 },
      { field: "requested_at", headerName: "Requested", valueFormatter: ({ value }) => ts(value), minWidth: 170 },
      { field: "detail", headerName: "Detail", minWidth: 260, flex: 2 },
    ],
    []
  );

  const holdSelected = useCallback(
    async (release = false) => {
      if (!selectedCatalog) return;
      try {
        await apiJson(`/api/patch-management/catalog/${release ? "release" : "hold"}`, {
          method: "POST",
          body: JSON.stringify({
            scope: "global",
            update_id: selectedCatalog.update_id,
            revision_number: selectedCatalog.revision_number,
            title: selectedCatalog.title,
            reason: "Operator catalog action.",
          }),
        });
        notifyOperator({ message: release ? "Patch hold released." : "Patch held.", variant: "success" });
        await load();
      } catch (err) {
        notifyOperator({ message: err.message || "Patch hold action failed.", variant: "error" });
      }
    },
    [load, notifyOperator, selectedCatalog]
  );

  const createPolicy = useCallback(async () => {
    try {
      await apiJson("/api/patch-management/policies", {
        method: "POST",
        body: JSON.stringify({
          ...policyDraft,
          scope_type: "global",
          reboot: {
            mode: "maintenance_window",
            maintenance_window_start: "22:00",
            maintenance_window_end: "05:00",
            deferral_deadline_hours: 72,
            user_prompt: true,
          },
        }),
      });
      notifyOperator({ message: "Patch policy created.", variant: "success" });
      setPolicyDraft({
        name: "",
        description: "",
        class_toggles: Object.fromEntries(CLASS_KEYS.map((key) => [key, true])),
      });
      await load();
    } catch (err) {
      notifyOperator({ message: err.message || "Patch policy create failed.", variant: "error" });
    }
  }, [load, notifyOperator, policyDraft]);

  return (
    <PageBodyFrame variant="grid_with_stack">
      <Stack spacing={1.5} sx={{ height: "100%", minHeight: 0 }}>
        {error && <Alert severity="error">{error}</Alert>}
        <Stack direction="row" alignItems="center" justifyContent="space-between" spacing={1}>
          <Tabs
            value={tab}
            onChange={(_event, value) => setTab(value)}
            textColor="inherit"
            TabIndicatorProps={{ sx: { bgcolor: "#7dd3fc" } }}
            sx={{ minHeight: 36, "& .MuiTab-root": { minHeight: 36, color: MAGIC_UI.textMuted, textTransform: "none" }, "& .Mui-selected": { color: MAGIC_UI.textBright } }}
          >
            <Tab value="catalog" label="Patch Catalog" />
            <Tab value="devices" label="Device Compliance" />
            <Tab value="policies" label="Policies" />
            <Tab value="history" label="Run History" />
          </Tabs>
          <Stack direction="row" spacing={1}>
            {tab === "catalog" && (
              <>
                <Button startIcon={<LockRoundedIcon />} disabled={!selectedCatalog} onClick={() => holdSelected(false)} sx={ACTION_BUTTON_SX}>
                  Hold
                </Button>
                <Button startIcon={<LockOpenRoundedIcon />} disabled={!selectedCatalog} onClick={() => holdSelected(true)} sx={ACTION_BUTTON_SX}>
                  Release
                </Button>
              </>
            )}
            <Button startIcon={<RefreshRoundedIcon />} disabled={loading} onClick={load} sx={PRIMARY_BUTTON_SX}>
              Refresh
            </Button>
          </Stack>
        </Stack>

        {tab === "catalog" && (
          <GridShell sx={{ height: "calc(100vh - 230px)", minHeight: 520 }}>
            <AgGridReact
              theme={DEVICE_DETAILS_GRID_THEME}
              rowData={catalog}
              columnDefs={catalogColumns}
              defaultColDef={DEFAULT_GRID_COL_DEF}
              rowSelection="single"
              onSelectionChanged={(event) => setSelectedCatalog(event.api.getSelectedRows()[0] || null)}
              pagination
              paginationPageSize={50}
            />
          </GridShell>
        )}

        {tab === "devices" && (
          <GridShell sx={{ height: "calc(100vh - 230px)", minHeight: 520 }}>
            <AgGridReact theme={DEVICE_DETAILS_GRID_THEME} rowData={devices} columnDefs={deviceColumns} defaultColDef={DEFAULT_GRID_COL_DEF} pagination paginationPageSize={50} />
          </GridShell>
        )}

        {tab === "policies" && (
          <Stack spacing={1.5} sx={{ minHeight: 0 }}>
            <Stack direction={{ xs: "column", lg: "row" }} spacing={1.2} alignItems={{ xs: "stretch", lg: "center" }}>
              <TextField
                label="Policy Name"
                size="small"
                value={policyDraft.name}
                onChange={(event) => setPolicyDraft((prev) => ({ ...prev, name: event.target.value }))}
                sx={{ ...FIELD_SX, minWidth: 240 }}
              />
              <TextField
                label="Description"
                size="small"
                value={policyDraft.description}
                onChange={(event) => setPolicyDraft((prev) => ({ ...prev, description: event.target.value }))}
                sx={{ ...FIELD_SX, minWidth: 320, flex: 1 }}
              />
              <Button disabled={!policyDraft.name.trim()} onClick={createPolicy} sx={PRIMARY_BUTTON_SX}>
                Create Policy
              </Button>
            </Stack>
            <Box sx={{ display: "flex", flexWrap: "wrap", gap: 0.8 }}>
              {CLASS_KEYS.map((key) => (
                <FormControlLabel
                  key={key}
                  control={
                    <Checkbox
                      checked={Boolean(policyDraft.class_toggles[key])}
                      onChange={(event) =>
                        setPolicyDraft((prev) => ({
                          ...prev,
                          class_toggles: { ...prev.class_toggles, [key]: event.target.checked },
                        }))
                      }
                      sx={{ color: "#7dd3fc", "&.Mui-checked": { color: "#7dd3fc" } }}
                    />
                  }
                  label={<Typography sx={{ color: MAGIC_UI.textBright, fontSize: 13 }}>{classLabel(key)}</Typography>}
                />
              ))}
            </Box>
            <GridShell sx={{ height: "calc(100vh - 330px)", minHeight: 420 }}>
              <AgGridReact theme={DEVICE_DETAILS_GRID_THEME} rowData={policies} columnDefs={policyColumns} defaultColDef={DEFAULT_GRID_COL_DEF} pagination paginationPageSize={50} />
            </GridShell>
          </Stack>
        )}

        {tab === "history" && (
          <GridShell sx={{ height: "calc(100vh - 230px)", minHeight: 520 }}>
            <AgGridReact theme={DEVICE_DETAILS_GRID_THEME} rowData={history} columnDefs={historyColumns} defaultColDef={DEFAULT_GRID_COL_DEF} pagination paginationPageSize={50} />
          </GridShell>
        )}
      </Stack>
    </PageBodyFrame>
  );
}
