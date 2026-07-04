import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  CircularProgress,
  FormControlLabel,
  MenuItem,
  Stack,
  Switch,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import {
  Add as AddIcon,
  Check as CheckIcon,
  Delete as DeleteIcon,
  DevicesRounded as DevicesRoundedIcon,
  DriveFileRenameOutline as DriveFileRenameOutlineIcon,
  GppMaybeRounded as GppMaybeRoundedIcon,
  PolicyRounded as PolicyRoundedIcon,
  ScheduleRounded as ScheduleRoundedIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { buildNavTabsSx } from "../Automation/Watchdogs/shared.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useUrlTabState } from "../app/hooks/useUrlTabState.js";
import { APP_PATHS } from "../app/routes/paths.js";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
  gridFontFamily,
} from "../Devices/Tabs/Shared.jsx";
import {
  POLICY_EXCLUSION_TYPES,
  POLICY_MATCH_TYPES,
  POLICY_ROLE_SCOPES,
  POLICY_RULE_TYPES,
  POLICY_SCHEDULE_TYPES,
  datetimeLocalFromUnix,
  defaultPolicyDraft,
  formatTimestamp,
  policyDraftFromRecord,
  policySavePayload,
  policyTypeLabel,
  text,
  unixFromDatetimeLocal,
  valueArray,
} from "./Patch_Management.jsx";

const TAB_SECTION_SX = {
  width: "100%",
  display: "flex",
  flexDirection: "column",
  gap: 1.5,
  px: { xs: 1.5, md: 2 },
  py: { xs: 1.25, md: 1.75 },
};

const ACTION_BUTTON_BASE_SX = {
  minHeight: 38,
  height: 38,
  px: 1.8,
  borderRadius: 999,
  fontFamily: gridFontFamily,
  fontWeight: 600,
  fontSize: "0.92rem",
  lineHeight: 1,
  textTransform: "none",
  whiteSpace: "nowrap",
  boxSizing: "border-box",
  borderWidth: "1px",
  borderStyle: "solid",
};

const PRIMARY_CTA_FLAT_SX = {
  ...ACTION_BUTTON_BASE_SX,
  color: "#06101d",
  borderColor: "transparent",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "none",
  "&:hover": {
    background: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
    boxShadow: "none",
  },
};

const OUTLINE_BUTTON_SX = {
  ...ACTION_BUTTON_BASE_SX,
  borderColor: "rgba(148,163,184,0.45)",
  color: MAGIC_UI.textBright,
  background: "rgba(5, 10, 24, 0.84)",
  boxShadow: "0 10px 24px rgba(2, 6, 23, 0.3)",
  "&:hover": {
    borderColor: "rgba(125, 211, 252, 0.52)",
    background: "rgba(8, 14, 30, 0.92)",
    boxShadow: "0 14px 32px rgba(2, 6, 23, 0.42)",
  },
};

const INPUT_FIELD_SX = {
  "& .MuiOutlinedInput-root": {
    borderRadius: 2,
    bgcolor: "rgba(5,9,18,0.85)",
    color: MAGIC_UI.textBright,
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.35)",
    },
    "&:hover fieldset": {
      borderColor: MAGIC_UI.accentA,
    },
    "&.Mui-focused fieldset": {
      borderColor: MAGIC_UI.accentB,
      boxShadow: "0 0 0 1px rgba(192,132,252,0.3)",
    },
  },
  "& .MuiInputLabel-root": {
    color: MAGIC_UI.textMuted,
  },
  "& .MuiFormHelperText-root": {
    color: "#fda4af",
  },
};

const SELECT_FIELD_SX = {
  ...INPUT_FIELD_SX,
  "& .MuiOutlinedInput-root": {
    ...INPUT_FIELD_SX["& .MuiOutlinedInput-root"],
    minHeight: 42,
    bgcolor: "rgba(8,13,30,0.94)",
    background: "linear-gradient(160deg, rgba(8,13,30,0.96), rgba(11,18,38,0.94))",
    boxShadow: "inset 0 1px 0 rgba(255,255,255,0.03)",
  },
  "& .MuiSelect-select": {
    display: "flex",
    alignItems: "center",
    minHeight: "20px !important",
    paddingTop: "7px !important",
    paddingBottom: "7px !important",
  },
  "& .MuiSvgIcon-root": {
    color: MAGIC_UI.accentA,
  },
};

const SELECT_MENU_PROPS = {
  PaperProps: {
    sx: {
      mt: 1,
      borderRadius: 2.5,
      border: `1px solid ${MAGIC_UI.panelBorder}`,
      background:
        "linear-gradient(180deg, rgba(8, 13, 30, 0.98) 0%, rgba(7, 11, 24, 0.98) 55%, rgba(10, 16, 34, 0.98) 100%)",
      boxShadow: "0 24px 60px rgba(2, 8, 23, 0.7)",
      color: MAGIC_UI.textBright,
      overflow: "hidden",
      "& .MuiMenu-list": {
        p: 0.5,
      },
      "& .MuiMenuItem-root": {
        minHeight: 27,
        px: 1.3,
        py: 0.35,
        borderRadius: 1.25,
        fontSize: "0.88rem",
        color: MAGIC_UI.textBright,
      },
      "& .MuiMenuItem-root:hover": {
        background: "rgba(125, 183, 255, 0.12)",
      },
      "& .MuiMenuItem-root.Mui-selected": {
        background: "rgba(125, 183, 255, 0.2)",
        color: "#f8fbff",
      },
    },
  },
};

const ROLE_MATCH_LABEL_SX = {
  display: "inline-flex",
  alignItems: "center",
  minHeight: 38,
  px: 1.2,
  borderRadius: 999,
  border: "1px solid rgba(125, 211, 252, 0.24)",
  background: "rgba(8, 47, 73, 0.26)",
  color: "rgba(191, 219, 254, 0.95)",
  fontSize: "0.78rem",
  fontWeight: 700,
  whiteSpace: "nowrap",
};

const POLICY_EDITOR_TAB_URL_BY_KEY = Object.freeze({
  details: "policy_name",
  targets: "targets",
  schedule: "schedule",
  rules: "allow_block",
  exclusions: "exclusions",
});

const POLICY_EDITOR_TAB_KEY_BY_URL = Object.freeze({
  policy_name: "details",
  details: "details",
  targets: "targets",
  schedule: "schedule",
  allow_block: "rules",
  rules: "rules",
  exclusions: "exclusions",
});

const POLICY_EDITOR_TABS = [
  { key: "details", label: "Policy Name", icon: DriveFileRenameOutlineIcon },
  { key: "targets", label: "Targets", icon: DevicesRoundedIcon },
  { key: "schedule", label: "Schedule", icon: ScheduleRoundedIcon },
  { key: "rules", label: "Allow / Block", icon: GppMaybeRoundedIcon },
  { key: "exclusions", label: "Exclusions", icon: PolicyRoundedIcon },
];

function policyTypeFromPath(pathname = "") {
  if (pathname.includes("/global/")) return "global";
  return pathname.includes("/device-filter/") ? "device_filter" : "site";
}

function returnPathForPolicyType(policyType) {
  if (policyType === "global") return APP_PATHS.patchManagementGlobalPolicies;
  return policyType === "device_filter" ? APP_PATHS.patchManagementDeviceFilterPolicies : APP_PATHS.patchManagementSitePolicies;
}

function SectionHeader({ title, action, sx }) {
  return (
    <Box
      sx={{
        mb: 1.5,
        display: "flex",
        alignItems: action ? "flex-start" : "center",
        justifyContent: "space-between",
        gap: 2,
        ...sx,
      }}
    >
      <Typography
        variant="subtitle2"
        sx={{
          color: MAGIC_UI.textBright,
          fontWeight: 400,
          letterSpacing: 0.4,
          textTransform: "uppercase",
          fontSize: 13,
        }}
      >
        {title}
      </Typography>
      {action || null}
    </Box>
  );
}

function RoleMatchLabel({ label }) {
  if (!text(label)) return null;
  return (
    <Typography component="span" sx={ROLE_MATCH_LABEL_SX}>
      {label}
    </Typography>
  );
}

export default function PatchPolicyEditor() {
  const navigate = useNavigate();
  const location = useLocation();
  const params = useParams();
  const policyType = useMemo(() => policyTypeFromPath(location.pathname), [location.pathname]);
  const policyId = text(params.policyId);
  const editing = Boolean(policyId);
  const [draft, setDraft] = useState(() => defaultPolicyDraft(policyType));
  const [metadata, setMetadata] = useState({});
  const [loading, setLoading] = useState(Boolean(editing));
  const [loadError, setLoadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [warnings, setWarnings] = useState([]);
  const [preview, setPreview] = useState(null);
  const [previewError, setPreviewError] = useState("");
  const [saving, setSaving] = useState(false);
  const notifyOperator = useAppNotifications({
    title: "Patch Policy",
    icon: "pendingactions",
    variant: "success",
  });

  const tabDefs = POLICY_EDITOR_TABS;
  const tabKeys = useMemo(() => tabDefs.map((tab) => tab.key), [tabDefs]);
  const { activeKey: activeTabKey, setActiveKey: setActiveTabKey } = useUrlTabState({
    param: "tab",
    defaultKey: tabDefs[0]?.key || "details",
    allowedKeys: tabKeys,
    keyByUrl: POLICY_EDITOR_TAB_KEY_BY_URL,
    urlByKey: POLICY_EDITOR_TAB_URL_BY_KEY,
  });
  const tab = Math.max(0, tabDefs.findIndex((tabDef) => tabDef.key === activeTabKey));

  useEffect(() => {
    let active = true;
    async function loadEditorData() {
      setLoading(true);
      setLoadError("");
      try {
        const requests = [
          fetch("/api/patches/policies/metadata", { credentials: "include", cache: "no-store" }),
        ];
        if (editing) {
          requests.push(fetch(`/api/patches/policies/${encodeURIComponent(policyId)}`, { credentials: "include", cache: "no-store" }));
        }
        const responses = await Promise.all(requests);
        const metadataPayload = await responses[0].json().catch(() => ({}));
        if (!responses[0].ok) {
          throw new Error(metadataPayload?.message || metadataPayload?.error || `HTTP ${responses[0].status}`);
        }
        if (!active) return;
        setMetadata(metadataPayload || {});
        if (editing) {
          const policyPayload = await responses[1].json().catch(() => ({}));
          if (!responses[1].ok) {
            throw new Error(policyPayload?.message || policyPayload?.error || `HTTP ${responses[1].status}`);
          }
          const policy = policyPayload?.policy || policyPayload;
          setDraft(policyDraftFromRecord(policy, policyType));
        } else {
          setDraft(defaultPolicyDraft(policyType));
        }
      } catch (error) {
        if (active) setLoadError(String(error?.message || error));
      } finally {
        if (active) setLoading(false);
      }
    }
    void loadEditorData();
    return () => {
      active = false;
    };
  }, [editing, policyId, policyType]);

  const setField = useCallback((field, value) => {
    setDraft((previous) => ({ ...previous, [field]: value }));
  }, []);

  const addRule = useCallback((ruleType) => {
    setDraft((previous) => ({
      ...previous,
      rules: [
        ...(Array.isArray(previous.rules) ? previous.rules : []),
        { rule_type: ruleType, match_type: "kb", match_value: "", notes: "" },
      ],
    }));
  }, []);

  const updateRule = useCallback((index, field, value) => {
    setDraft((previous) => {
      const rules = [...(Array.isArray(previous.rules) ? previous.rules : [])];
      rules[index] = { ...(rules[index] || {}), [field]: value };
      return { ...previous, rules };
    });
  }, []);

  const deleteRule = useCallback((index) => {
    setDraft((previous) => {
      const rules = [...(Array.isArray(previous.rules) ? previous.rules : [])];
      rules.splice(index, 1);
      return { ...previous, rules };
    });
  }, []);

  const addDeviceTarget = useCallback(() => {
    setDraft((previous) => ({
      ...previous,
      targets: [...(Array.isArray(previous.targets) ? previous.targets : []), { target_type: "device", hostname: "" }],
    }));
  }, []);

  const addFilterTarget = useCallback(() => {
    const firstFilter = Array.isArray(metadata?.filters) ? metadata.filters[0] : null;
    setDraft((previous) => ({
      ...previous,
      targets: [
        ...(Array.isArray(previous.targets) ? previous.targets : []),
        { target_type: "filter", filter_id: Number(firstFilter?.id || 0) || "" },
      ],
    }));
  }, [metadata]);

  const updateTarget = useCallback((index, field, value) => {
    setDraft((previous) => {
      const targets = [...(Array.isArray(previous.targets) ? previous.targets : [])];
      targets[index] = { ...(targets[index] || {}), [field]: value };
      return { ...previous, targets };
    });
  }, []);

  const deleteTarget = useCallback((index) => {
    setDraft((previous) => {
      const targets = [...(Array.isArray(previous.targets) ? previous.targets : [])];
      targets.splice(index, 1);
      return { ...previous, targets };
    });
  }, []);

  const addExclusion = useCallback((exclusionType) => {
    setDraft((previous) => ({
      ...previous,
      exclusions: [
        ...(Array.isArray(previous.exclusions) ? previous.exclusions : []),
        { exclusion_type: exclusionType, target_type: "device", hostname: "", site_id: "", reason: "" },
      ],
    }));
  }, []);

  const updateExclusion = useCallback((index, field, value) => {
    setDraft((previous) => {
      const exclusions = [...(Array.isArray(previous.exclusions) ? previous.exclusions : [])];
      exclusions[index] = { ...(exclusions[index] || {}), [field]: value };
      return { ...previous, exclusions };
    });
  }, []);

  const deleteExclusion = useCallback((index) => {
    setDraft((previous) => {
      const exclusions = [...(Array.isArray(previous.exclusions) ? previous.exclusions : [])];
      exclusions.splice(index, 1);
      return { ...previous, exclusions };
    });
  }, []);

  const buildPolicyPayload = useCallback(
    (confirmParentOverrides = false) => {
      const draftType = text(draft.policy_type).toLowerCase();
      const payloadBaseType = draftType === "global" ? "site" : policyType;
      const payload = {
        ...policySavePayload(draft, payloadBaseType),
        policy_type: draftType === "global" ? "global" : policyType,
        confirm_parent_overrides: confirmParentOverrides,
      };
      if (payload.policy_type === "global") {
        delete payload.site_ids;
        delete payload.targets;
      }
      return payload;
    },
    [draft, policyType]
  );

  useEffect(() => {
    if (loading || !text(draft.name)) {
      setPreview(null);
      setPreviewError("");
      return undefined;
    }
    let active = true;
    const timer = window.setTimeout(async () => {
      try {
        const payload = buildPolicyPayload(false);
        const url = editing
          ? `/api/patches/policies/${encodeURIComponent(policyId)}/preview`
          : "/api/patches/policies/conflicts";
        const response = await fetch(url, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        const body = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(body?.message || body?.error || `HTTP ${response.status}`);
        }
        if (!active) return;
        setPreview(body || null);
        setPreviewError("");
      } catch (error) {
        if (!active) return;
        setPreview(null);
        setPreviewError(String(error?.message || error));
      }
    }, 350);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [buildPolicyPayload, draft.name, editing, loading, policyId]);

  const savePolicy = useCallback(
    async ({ confirmParentOverrides = false } = {}) => {
      setSaving(true);
      setSaveError("");
      setWarnings([]);
      try {
        const payload = buildPolicyPayload(confirmParentOverrides);
        const method = editing ? "PUT" : "POST";
        const url = editing ? `/api/patches/policies/${encodeURIComponent(policyId)}` : "/api/patches/policies";
        const response = await fetch(url, {
          method,
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        const body = await response.json().catch(() => ({}));
        if (!response.ok) {
          if (body?.error === "parent_block_override_confirmation_required") {
            setWarnings(Array.isArray(body?.warnings) ? body.warnings : []);
            setSaveError("Parent block override confirmation required.");
            return;
          }
          throw new Error(body?.message || body?.error || `HTTP ${response.status}`);
        }
        await notifyOperator(`${payload.name} saved`);
        navigate(returnPathForPolicyType(policyType));
      } catch (error) {
        setSaveError(String(error?.message || error));
      } finally {
        setSaving(false);
      }
    },
    [buildPolicyPayload, editing, navigate, notifyOperator, policyId, policyType]
  );

  const draftPolicyType = text(draft.policy_type).toLowerCase();
  const displayPolicyTypeLabel = draftPolicyType === "global" ? "Global Patch Policy" : policyTypeLabel(policyType);
  const pageTitle = editing ? `Edit ${displayPolicyTypeLabel}` : `New ${policyTypeLabel(policyType)}`;
  const pageSubtitle =
    policyType === "global"
      ? "Edit locked global patch baselines for Windows servers and workstations."
      : policyType === "device_filter"
      ? "Create deepest patch overrides for explicit devices or dynamic device filters."
      : "Create site-scoped patch maintenance windows and approval rules.";

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "patch-policy-cancel",
        label: "Cancel",
        tone: "secondary",
        onClick: () => navigate(returnPathForPolicyType(policyType)),
      },
      {
        id: "patch-policy-save",
        label: saving ? "Saving" : "Save Policy",
        icon: saving ? <CircularProgress size={15} thickness={5} /> : <CheckIcon />,
        tone: "primary",
        disabled: saving || loading || !text(draft.name),
        onClick: () => {
          if (!saving && !loading && text(draft.name)) {
            void savePolicy();
          }
        },
      },
    ],
    [draft.name, loading, navigate, policyType, savePolicy, saving]
  );

  useRoutePageChrome({
    title: pageTitle,
    subtitle: pageSubtitle,
    Icon: PolicyRoundedIcon,
    actions: pageHeaderActions,
  });

  const siteOptions = Array.isArray(metadata?.sites) ? metadata.sites : [];
  const filterOptions = Array.isArray(metadata?.filters) ? metadata.filters : [];
  const selectedSiteIDSet = useMemo(
    () => new Set((Array.isArray(draft.site_ids) ? draft.site_ids : []).map(Number).filter(Boolean)),
    [draft.site_ids]
  );
  const exclusionSiteOptions = useMemo(() => {
    if (policyType !== "site" || selectedSiteIDSet.size === 0) {
      return siteOptions;
    }
    return siteOptions.filter((site) => selectedSiteIDSet.has(Number(site.id)));
  }, [policyType, selectedSiteIDSet, siteOptions]);
  const ruleRows = Array.isArray(draft.rules) ? draft.rules : [];
  const targetRows = Array.isArray(draft.targets) ? draft.targets : [];
  const exclusionRows = Array.isArray(draft.exclusions) ? draft.exclusions : [];
  const targetPreviewByIndex = useMemo(() => {
    const rows = Array.isArray(preview?.target_rows) ? preview.target_rows : [];
    return new Map(rows.map((row) => [Number(row?.row_index || 0), row]));
  }, [preview]);
  const exclusionPreviewByIndex = useMemo(() => {
    const rows = Array.isArray(preview?.exclusion_rows) ? preview.exclusion_rows : [];
    return new Map(rows.map((row) => [Number(row?.row_index || 0), row]));
  }, [preview]);

  const ruleColumnDefs = useMemo(
    () => [
      {
        field: "match_type",
        headerName: "Type",
        width: 150,
        cellRenderer: (params) => (
          <TextField
            select
            size="small"
            value={params.value || "severity"}
            onChange={(event) => updateRule(params.node.rowIndex, "match_type", event.target.value)}
            fullWidth
            sx={SELECT_FIELD_SX}
            SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
          >
            {POLICY_MATCH_TYPES.map((option) => (
              <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
            ))}
          </TextField>
        ),
      },
      {
        field: "match_value",
        headerName: "KB / Value",
        flex: 1,
        minWidth: 180,
        cellRenderer: (params) => (
          <TextField
            size="small"
            value={params.value || ""}
            onChange={(event) => updateRule(params.node.rowIndex, "match_value", event.target.value)}
            fullWidth
            sx={INPUT_FIELD_SX}
          />
        ),
      },
      {
        field: "classification",
        headerName: "Classification",
        width: 170,
        valueGetter: (params) => (params.data?.match_type === "classification" ? params.data?.match_value : ""),
      },
      {
        field: "severity",
        headerName: "Severity",
        width: 130,
        valueGetter: (params) => (params.data?.match_type === "severity" ? params.data?.match_value : ""),
      },
      {
        field: "rule_type",
        headerName: "Action",
        width: 145,
        cellRenderer: (params) => (
          <TextField
            select
            size="small"
            value={params.value || "approve"}
            onChange={(event) => updateRule(params.node.rowIndex, "rule_type", event.target.value)}
            fullWidth
            sx={SELECT_FIELD_SX}
            SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
          >
            {POLICY_RULE_TYPES.map((option) => (
              <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
            ))}
          </TextField>
        ),
      },
      { field: "created_by", headerName: "Created By", width: 140 },
      { field: "created_at", headerName: "Created At", width: 180, valueFormatter: (params) => formatTimestamp(params.value) },
      {
        field: "override_parent_block",
        headerName: "Parent Override",
        width: 160,
        cellRenderer: (params) => (
          <Checkbox
            size="small"
            checked={Boolean(params.value)}
            onChange={(event) => updateRule(params.node.rowIndex, "override_parent_block", event.target.checked)}
            sx={{ color: MAGIC_UI.accentA, "&.Mui-checked": { color: MAGIC_UI.accentA } }}
          />
        ),
      },
      {
        colId: "delete",
        headerName: "",
        width: 110,
        sortable: false,
        filter: false,
        cellRenderer: (params) => (
          <Button size="small" startIcon={<DeleteIcon />} onClick={() => deleteRule(params.node.rowIndex)} sx={OUTLINE_BUTTON_SX}>
            Remove
          </Button>
        ),
      },
    ],
    [deleteRule, updateRule]
  );

  return (
    <Box
      sx={{
        m: 0,
        p: { xs: 2, md: 3 },
        flexGrow: 1,
        minWidth: 0,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        gap: 3,
        borderRadius: 0,
        background: "transparent",
        border: "none",
        boxShadow: "none",
      }}
    >
      <Tabs
        value={tab}
        onChange={(_, value) => setActiveTabKey(tabDefs[value]?.key || tabDefs[0]?.key || "details")}
        variant="scrollable"
        scrollButtons="auto"
        TabIndicatorProps={{
          style: {
            height: 3,
            borderRadius: 3,
            background: "#7db7ff",
          },
        }}
        sx={buildNavTabsSx()}
      >
        {tabDefs.map((tabDef) => (
          <Tab
            key={tabDef.key}
            label={tabDef.label}
            icon={tabDef.icon ? <tabDef.icon sx={{ fontSize: 18 }} /> : undefined}
            iconPosition="start"
          />
        ))}
      </Tabs>

      <Box sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column", gap: 3 }}>
        {loadError ? <Alert severity="error">{loadError}</Alert> : null}
        {saveError ? (
          <Alert
            severity={warnings.length ? "warning" : "error"}
            action={
              warnings.length ? (
                <Button color="inherit" size="small" onClick={() => savePolicy({ confirmParentOverrides: true })}>
                  Confirm
                </Button>
              ) : null
            }
          >
            {saveError}
          </Alert>
        ) : null}
        {previewError && ["targets", "exclusions"].includes(activeTabKey) ? (
          <Alert severity="warning">
            {previewError}
          </Alert>
        ) : null}

        {loading ? (
          <Box sx={{ ...TAB_SECTION_SX, color: MAGIC_UI.textMuted }}>
            Loading policy editor...
          </Box>
        ) : null}

        {!loading && activeTabKey === "details" ? (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Policy Name" />
            <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
              <TextField
                label="Name"
                value={draft.name || ""}
                onChange={(event) => setField("name", event.target.value)}
                fullWidth
                size="small"
                sx={{ width: { xs: "100%", md: "60%" }, maxWidth: 540, ...INPUT_FIELD_SX }}
                error={!text(draft.name)}
                helperText={!text(draft.name) ? "Policy name is required" : ""}
              />
              <TextField
                label="Deferral Days"
                type="number"
                value={draft.deferral_days || 14}
                onChange={(event) => setField("deferral_days", event.target.value)}
                size="small"
                sx={{ width: { xs: "100%", md: 180 }, ...INPUT_FIELD_SX }}
              />
            </Stack>
            <TextField
              label="Description"
              value={draft.description || ""}
              onChange={(event) => setField("description", event.target.value)}
              fullWidth
              size="small"
              sx={INPUT_FIELD_SX}
            />
          </Box>
        ) : null}

        {!loading && activeTabKey === "targets" ? (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader
              title={
                policyType === "global"
                  ? "Global Scope"
                  : policyType === "site"
                    ? "Site Scope"
                    : "Device / Filter Targets"
              }
            />
            {draftPolicyType === "global" ? (
              <Stack direction={{ xs: "column", md: "row" }} spacing={2} alignItems={{ xs: "stretch", md: "flex-start" }}>
                <TextField
                  label="Policy Type"
                  value={draft.role_scope || "Workstation"}
                  size="small"
                  InputProps={{ readOnly: true }}
                  sx={{ width: { xs: "100%", md: 220 }, ...INPUT_FIELD_SX }}
                />
                <RoleMatchLabel label={preview?.role_match_label} />
                <Alert
                  severity="info"
                  sx={{
                    flex: 1,
                    background: "rgba(8, 47, 73, 0.48)",
                    color: MAGIC_UI.textBright,
                    border: "1px solid rgba(125, 211, 252, 0.28)",
                    "& .MuiAlert-icon": { color: MAGIC_UI.accentA },
                  }}
                >
                  Global Patch Policy applies to every typed Windows device in this policy type unless a deeper site or device/filter policy overrides it.
                </Alert>
              </Stack>
            ) : policyType === "site" ? (
              <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                <TextField
                  label="Policy Type"
                  select
                  value={draft.role_scope || "Workstation"}
                  onChange={(event) => setField("role_scope", event.target.value)}
                  size="small"
                  sx={{ minWidth: 180, ...SELECT_FIELD_SX }}
                  SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                >
                  {POLICY_ROLE_SCOPES.map((option) => (
                    <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                  ))}
                </TextField>
                <TextField
                  label="Sites"
                  select
                  SelectProps={{ multiple: true, MenuProps: SELECT_MENU_PROPS }}
                  value={Array.isArray(draft.site_ids) ? draft.site_ids : []}
                  onChange={(event) => setField("site_ids", valueArray(event.target.value).map(Number).filter(Boolean))}
                  size="small"
                  sx={{ minWidth: 320, flex: 1, ...SELECT_FIELD_SX }}
                >
                  {siteOptions.map((site) => (
                    <MenuItem key={site.id} value={Number(site.id)}>{site.name || `Site ${site.id}`}</MenuItem>
                  ))}
                </TextField>
                <RoleMatchLabel label={preview?.role_match_label} />
              </Stack>
            ) : (
              <Stack spacing={1.4}>
                <Stack direction={{ xs: "column", md: "row" }} spacing={1} alignItems={{ xs: "stretch", md: "center" }}>
                  <TextField
                    label="Policy Type"
                    select
                    value={draft.role_scope || "Workstation"}
                    onChange={(event) => setField("role_scope", event.target.value)}
                    size="small"
                    sx={{ minWidth: 180, ...SELECT_FIELD_SX }}
                    SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                  >
                    {POLICY_ROLE_SCOPES.map((option) => (
                      <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                    ))}
                  </TextField>
                  <Button startIcon={<AddIcon />} onClick={addDeviceTarget} sx={PRIMARY_CTA_FLAT_SX}>Device Target</Button>
                  <Button startIcon={<AddIcon />} onClick={addFilterTarget} sx={OUTLINE_BUTTON_SX}>Filter Target</Button>
                </Stack>
                {targetRows.map((target, index) => (
                  <Stack key={`target-${index}`} direction={{ xs: "column", md: "row" }} spacing={1}>
                    <TextField
                      label="Target Type"
                      select
                      value={target.target_type || "device"}
                      onChange={(event) => updateTarget(index, "target_type", event.target.value)}
                      size="small"
                      sx={{ minWidth: 150, ...SELECT_FIELD_SX }}
                      SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                    >
                      <MenuItem value="device">Device</MenuItem>
                      <MenuItem value="filter">Filter</MenuItem>
                    </TextField>
                    {target.target_type === "filter" ? (
                      <TextField
                        label="Filter"
                        select
                        value={target.filter_id || ""}
                        onChange={(event) => updateTarget(index, "filter_id", event.target.value)}
                        size="small"
                        sx={{ minWidth: 300, flex: 1, ...SELECT_FIELD_SX }}
                        SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                      >
                        {filterOptions.map((filter) => (
                          <MenuItem key={filter.id} value={Number(filter.id)}>{filter.name || `Filter ${filter.id}`}</MenuItem>
                        ))}
                      </TextField>
                    ) : (
                      <TextField
                        label="Hostname"
                        value={target.hostname || ""}
                        onChange={(event) => updateTarget(index, "hostname", event.target.value)}
                        size="small"
                        sx={{ flex: 1, ...INPUT_FIELD_SX }}
                      />
                    )}
                    <Button startIcon={<DeleteIcon />} onClick={() => deleteTarget(index)} sx={OUTLINE_BUTTON_SX}>
                      Remove
                    </Button>
                    <RoleMatchLabel label={targetPreviewByIndex.get(index)?.role_match_label} />
                  </Stack>
                ))}
              </Stack>
            )}
          </Box>
        ) : null}

        {!loading && activeTabKey === "schedule" ? (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Schedule" />
            <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
              <TextField
                label="Install Schedule"
                select
                value={draft.install_schedule_type || "weekly"}
                onChange={(event) => setField("install_schedule_type", event.target.value)}
                size="small"
                sx={{ minWidth: 220, ...SELECT_FIELD_SX }}
                SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
              >
                {POLICY_SCHEDULE_TYPES.map((option) => (
                  <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                ))}
              </TextField>
              <TextField
                label="Install Start"
                type="datetime-local"
                value={datetimeLocalFromUnix(draft.install_start_ts)}
                onChange={(event) => setField("install_start_ts", unixFromDatetimeLocal(event.target.value))}
                size="small"
                InputLabelProps={{ shrink: true }}
                sx={{ minWidth: 260, ...INPUT_FIELD_SX }}
              />
              <FormControlLabel
                control={<Switch checked={Boolean(draft.enabled)} onChange={(event) => setField("enabled", event.target.checked)} />}
                label="Enabled"
                sx={{ color: MAGIC_UI.textBright }}
              />
              <FormControlLabel
                control={<Switch checked={Boolean(draft.managed_update_mode)} onChange={(event) => setField("managed_update_mode", event.target.checked)} />}
                label="Managed Windows Update"
                sx={{ color: MAGIC_UI.textBright }}
              />
            </Stack>
            <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
              <FormControlLabel
                control={<Switch checked={Boolean(draft.reboot_after_install)} onChange={(event) => setField("reboot_after_install", event.target.checked)} />}
                label="Reboot after install"
                sx={{ color: MAGIC_UI.textBright }}
              />
              <FormControlLabel
                control={<Switch checked={Boolean(draft.reboot_schedule_enabled)} onChange={(event) => setField("reboot_schedule_enabled", event.target.checked)} />}
                label="Separate reboot schedule"
                sx={{ color: MAGIC_UI.textBright }}
              />
              <TextField
                label="Reboot Start"
                type="datetime-local"
                value={datetimeLocalFromUnix(draft.reboot_start_ts)}
                onChange={(event) => setField("reboot_start_ts", unixFromDatetimeLocal(event.target.value))}
                size="small"
                InputLabelProps={{ shrink: true }}
                sx={{ minWidth: 260, ...INPUT_FIELD_SX }}
              />
              <FormControlLabel
                control={<Switch checked={Boolean(draft.force_reboot_logged_in)} onChange={(event) => setField("force_reboot_logged_in", event.target.checked)} />}
                label="Force logged-in user"
                sx={{ color: MAGIC_UI.textBright }}
              />
            </Stack>
          </Box>
        ) : null}

        {!loading && activeTabKey === "rules" ? (
          <Box sx={{ ...TAB_SECTION_SX, flexGrow: 1, minHeight: 0 }}>
            <SectionHeader
              title="Allow / Block Rules"
              action={
                <Stack direction="row" spacing={1}>
                  <Button startIcon={<AddIcon />} onClick={() => addRule("approve")} sx={PRIMARY_CTA_FLAT_SX}>Approve</Button>
                  <Button startIcon={<AddIcon />} onClick={() => addRule("block")} sx={OUTLINE_BUTTON_SX}>Block</Button>
                </Stack>
              }
            />
            <GridShell sx={{ flexGrow: 1, minHeight: 360, borderRadius: 0 }}>
              <AgGridReact
                rowData={ruleRows}
                columnDefs={ruleColumnDefs}
                defaultColDef={DEFAULT_GRID_COL_DEF}
                suppressCellFocus
                rowHeight={54}
                getRowId={(params) => String(params.data?.id || `${params.data?.match_type}-${params.data?.match_value}-${params.rowIndex}`)}
                theme={DEVICE_DETAILS_GRID_THEME}
              />
            </GridShell>
          </Box>
        ) : null}

        {!loading && activeTabKey === "exclusions" ? (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader
              title="Exclusions"
              action={
                <Stack direction="row" spacing={1}>
                  <Button startIcon={<AddIcon />} onClick={() => addExclusion("unmanaged")} sx={PRIMARY_CTA_FLAT_SX}>Unmanaged</Button>
                  <Button startIcon={<AddIcon />} onClick={() => addExclusion("frozen")} sx={OUTLINE_BUTTON_SX}>Frozen</Button>
                  <Button startIcon={<AddIcon />} onClick={() => addExclusion("managed_override")} sx={OUTLINE_BUTTON_SX}>Managed Override</Button>
                </Stack>
              }
            />
            <Stack spacing={1.2}>
              {exclusionRows.map((exclusion, index) => (
                <Stack key={`exclusion-${index}`} direction={{ xs: "column", md: "row" }} spacing={1}>
                  <TextField
                    label="Mode"
                    select
                    value={exclusion.exclusion_type || "unmanaged"}
                    onChange={(event) => updateExclusion(index, "exclusion_type", event.target.value)}
                    size="small"
                    sx={{ minWidth: 150, ...SELECT_FIELD_SX }}
                    SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                  >
                    {POLICY_EXCLUSION_TYPES.map((option) => (
                      <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                    ))}
                  </TextField>
                  <TextField
                    label="Target Type"
                    select
                    value={exclusion.target_type || "device"}
                    onChange={(event) => updateExclusion(index, "target_type", event.target.value)}
                    size="small"
                    sx={{ minWidth: 150, ...SELECT_FIELD_SX }}
                    SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                  >
                    <MenuItem value="device">Device</MenuItem>
                    <MenuItem value="filter">Filter</MenuItem>
                  </TextField>
                  {exclusion.target_type === "filter" ? (
                    <TextField
                      label="Filter"
                      select
                      value={exclusion.filter_id || ""}
                      onChange={(event) => updateExclusion(index, "filter_id", event.target.value)}
                      size="small"
                      sx={{ minWidth: 260, flex: 1, ...SELECT_FIELD_SX }}
                      SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                    >
                      {filterOptions.map((filter) => (
                        <MenuItem key={filter.id} value={Number(filter.id)}>{filter.name || `Filter ${filter.id}`}</MenuItem>
                      ))}
                    </TextField>
                  ) : (
                    <Stack direction={{ xs: "column", md: "row" }} spacing={1} sx={{ minWidth: { md: 440 }, flex: 1 }}>
                      <TextField
                        label="Site"
                        select
                        value={Number(exclusion.site_id || 0) || ""}
                        onChange={(event) => updateExclusion(index, "site_id", Number(event.target.value || 0) || "")}
                        size="small"
                        sx={{ minWidth: 200, flex: 0.8, ...SELECT_FIELD_SX }}
                        SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                      >
                        <MenuItem value="">Select site</MenuItem>
                        {exclusionSiteOptions.map((site) => (
                          <MenuItem key={site.id} value={Number(site.id)}>{site.name || `Site ${site.id}`}</MenuItem>
                        ))}
                      </TextField>
                      <TextField
                        label="Hostname"
                        value={exclusion.hostname || ""}
                        onChange={(event) => updateExclusion(index, "hostname", event.target.value)}
                        size="small"
                        sx={{ minWidth: 220, flex: 1, ...INPUT_FIELD_SX }}
                      />
                    </Stack>
                  )}
                  <TextField
                    label="Reason"
                    value={exclusion.reason || ""}
                    onChange={(event) => updateExclusion(index, "reason", event.target.value)}
                    size="small"
                    sx={{ minWidth: 220, flex: 1, ...INPUT_FIELD_SX }}
                  />
                  <RoleMatchLabel label={exclusionPreviewByIndex.get(index)?.role_match_label} />
                  <Button startIcon={<DeleteIcon />} onClick={() => deleteExclusion(index)} sx={OUTLINE_BUTTON_SX}>
                    Remove
                  </Button>
                </Stack>
              ))}
              {!exclusionRows.length ? (
                <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                  No exclusions configured.
                </Typography>
              ) : null}
            </Stack>
          </Box>
        ) : null}
      </Box>
    </Box>
  );
}
