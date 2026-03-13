import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Checkbox,
  Chip,
  FormControlLabel,
  IconButton,
  LinearProgress,
  MenuItem,
  Paper,
  Select,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import {
  AddRounded as AddIcon,
  CloseRounded as CancelIcon,
  DeleteRounded as DeleteIcon,
  FilterAlt as HeaderIcon,
  PreviewRounded as PreviewIcon,
  SaveRounded as SaveIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

import PageBodyFrame from "../../PageBodyFrame.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_ICON = HeaderIcon;
const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";
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

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});

const MAIN_TABS = [
  { value: "name", label: "Name" },
  { value: "scope", label: "Scope" },
  { value: "criteria", label: "Criteria" },
  { value: "results", label: "Results" },
];

const PREVIEW_AUTO_SIZE_COLUMNS = ["hostname", "site_name", "status", "operating_system"];
const DEFAULT_OPERATOR_OPTIONS = {
  text: [
    { value: "contains", label: "Contains" },
    { value: "does_not_contain", label: "Does Not Contain" },
    { value: "equals", label: "Equals" },
    { value: "begins_with", label: "Begins With" },
    { value: "ends_with", label: "Ends With" },
  ],
  number: [
    { value: "equals", label: "Equals" },
    { value: "greater_than", label: "Greater Than" },
    { value: "greater_than_or_equal", label: "Greater Than or Equal" },
    { value: "less_than", label: "Less Than" },
    { value: "less_than_or_equal", label: "Less Than or Equal" },
  ],
  enum: [{ value: "equals", label: "Equals" }],
  software_version: [
    { value: "matches", label: "Matches" },
    { value: "older_than", label: "Older Than" },
    { value: "newer_than", label: "Newer Than" },
  ],
};

const mergeOperatorOptions = (remote = {}, fallback = DEFAULT_OPERATOR_OPTIONS) => {
  const result = {};
  Object.keys(fallback).forEach((key) => {
    const preferred = Array.isArray(fallback[key]) ? fallback[key] : [];
    const incoming = Array.isArray(remote?.[key]) ? remote[key] : [];
    const seen = new Set();
    const merged = [];
    [...preferred, ...incoming].forEach((item) => {
      const value = String(item?.value || "").trim();
      if (!value || seen.has(value)) return;
      seen.add(value);
      merged.push({
        value,
        label: item?.label || preferred.find((entry) => entry.value === value)?.label || value,
      });
    });
    result[key] = merged;
  });
  return result;
};

const buildNavTabsSx = (minHeight = NAV_TAB_HEIGHT) => ({
  borderBottom: "1px solid rgba(148, 163, 184, 0.16)",
  minHeight,
  height: minHeight,
  "& .MuiTabs-flexContainer": {
    minHeight,
    height: minHeight,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    textTransform: "none",
    fontWeight: 400,
    fontFamily: "inherit",
    fontSize: "0.8rem",
    minHeight,
    height: minHeight,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
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
    "&:hover": {
      background: NAV_TAB_COLORS.activeBg,
    },
  },
});

const GRID_WRAPPER_SX = {
  width: "100%",
  flexGrow: 1,
  minHeight: 420,
  height: "100%",
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
    color: "#e2e8f0",
    fontSize: "0.88rem",
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
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
};

const makeId = (prefix) =>
  `${prefix}-${typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2, 10)}`;

const makeBasicCriterion = () => ({
  id: makeId("basic"),
  field: "hostname",
  operator: "contains",
  value: "",
  use_regex: false,
  software_source: "",
  version_operator: "",
  version_value: "",
});

const makeAdvancedCondition = (joinWith = "") => ({
  id: makeId("condition"),
  join_with: joinWith || undefined,
  field: "hostname",
  operator: "contains",
  value: "",
  use_regex: false,
  software_source: "",
  version_operator: "",
  version_value: "",
});

const makeAdvancedGroup = (joinWith = "") => ({
  id: makeId("group"),
  join_with: joinWith || undefined,
  conditions: [makeAdvancedCondition("")],
});

const ensureArray = (value) => (Array.isArray(value) ? value : []);

const normalizeCriterion = (criterion, factory) => {
  const normalized = {
    ...factory(),
    ...(criterion || {}),
  };
  if (!normalized.id) normalized.id = makeId("criterion");
  if (!normalized.field) normalized.field = "hostname";
  if (!normalized.operator) normalized.operator = "contains";
  normalized.use_regex = Boolean(normalized.use_regex || normalized.useRegex);
  normalized.software_source = String(
    normalized.software_source || normalized.softwareSource || normalized.source || ""
  ).trim();
  normalized.version_operator = String(
    normalized.version_operator || normalized.versionOperator || ""
  ).trim();
  normalized.version_value = String(normalized.version_value || normalized.versionValue || "").trim();
  normalized.value = normalized.value ?? "";
  return normalized;
};

const normalizeFilterRecord = (record) => {
  const basic = ensureArray(record?.basic_criteria?.criteria).map((criterion) =>
    normalizeCriterion(criterion, makeBasicCriterion)
  );
  const advancedSource = record?.criteria?.groups || record?.advanced_criteria?.groups;
  let advanced = ensureArray(advancedSource).map((group, index) => ({
    id: group?.id || makeId("group"),
    join_with: index === 0 ? "" : String(group?.join_with || group?.joinWith || "OR").toUpperCase(),
    conditions: ensureArray(group?.conditions).length
      ? ensureArray(group?.conditions).map((condition, conditionIndex) =>
          normalizeCriterion(
            {
              ...condition,
              join_with:
                conditionIndex === 0
                  ? ""
                  : String(condition?.join_with || condition?.joinWith || "AND").toUpperCase(),
            },
            makeAdvancedCondition
          )
        )
      : [makeAdvancedCondition("")],
  }));
  if (!advanced.length && basic.length) {
    advanced = [
      {
        id: makeId("group"),
        join_with: "",
        conditions: basic.map((criterion, index) => ({
          ...normalizeCriterion(criterion, makeAdvancedCondition),
          join_with: index === 0 ? "" : "AND",
        })),
      },
    ];
  }

  return {
    id: record?.id || null,
    name: record?.name || "",
    description: record?.description || "",
    site_mode: String(record?.site_mode || "global").toLowerCase() || "global",
    site_ids: ensureArray(record?.site_ids).map((value) => Number(value)).filter((value) => Number.isFinite(value)),
    advanced_criteria: advanced.length ? advanced : [makeAdvancedGroup("")],
    archived: Boolean(record?.archived),
    last_edited_by: record?.last_edited_by || "",
    updated_at: record?.updated_at || 0,
  };
};

const buildCriteriaGroupsPayload = (advancedCriteria) =>
  ensureArray(advancedCriteria).map((group, groupIndex) => ({
    join_with: groupIndex === 0 ? "" : group.join_with || "OR",
    conditions: ensureArray(group.conditions).map((condition, conditionIndex) => ({
      field: condition.field,
      operator: condition.operator,
      value: condition.value,
      use_regex: Boolean(condition.use_regex),
      join_with: conditionIndex === 0 ? "" : condition.join_with || "AND",
      software_source: condition.software_source || "",
      version_operator: condition.version_operator || "",
      version_value: condition.version_value || "",
    })),
  }));

const buildFilterPayload = (state) => {
  const advancedCriteria = buildCriteriaGroupsPayload(state?.advanced_criteria);
  return {
    name: state?.name || "",
    description: state?.description || "",
    criteria_mode: "advanced",
    site_mode: state?.site_mode || "global",
    site_ids: ensureArray(state?.site_ids),
    basic_criteria: { criteria: [] },
    advanced_criteria: { groups: advancedCriteria },
    criteria: { groups: advancedCriteria },
  };
};

const buildPreviewSignature = (state) =>
  JSON.stringify({
    site_mode: state?.site_mode || "global",
    site_ids: ensureArray(state?.site_ids)
      .map((value) => Number(value))
      .filter((value) => Number.isFinite(value))
      .sort((left, right) => left - right),
    criteria: { groups: buildCriteriaGroupsPayload(state?.advanced_criteria) },
  });

const STATUS_OPTIONS = ["Online", "Offline"];

const formatTimestamp = (value) => {
  if (!value) return "";
  const numeric = Number(value);
  const date = Number.isFinite(numeric) && numeric > 0 ? new Date(numeric * 1000) : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
};

function FilterCriterionRow({
  criterion,
  onChange,
  onRemove,
  fieldOptions,
  fieldById,
  operatorOptions,
  softwareVersionOperators,
  softwareSources,
  showJoin = false,
  joinValue = "",
  onJoinChange,
}) {
  const field = fieldById[criterion.field] || fieldOptions[0];
  const kind = field?.kind || "text";
  const operators = kind === "number" ? operatorOptions.number : kind === "enum" ? operatorOptions.enum : operatorOptions.text;
  const supportsRegex = Boolean(field?.supports_regex);
  const isSoftware = field?.value === "installed_software";
  const resolveDefaultOperator = useCallback(
    (fieldId) => {
      const nextField = fieldById[fieldId];
      const nextKind = nextField?.kind || "text";
      const source =
        nextKind === "number" ? operatorOptions.number : nextKind === "enum" ? operatorOptions.enum : operatorOptions.text;
      return source?.[0]?.value || "contains";
    },
    [fieldById, operatorOptions.enum, operatorOptions.number, operatorOptions.text]
  );

  return (
    <Paper
      variant="outlined"
      sx={{
        px: 1.25,
        py: 1.25,
        background: "rgba(15,23,42,0.55)",
        borderColor: "rgba(148,163,184,0.22)",
      }}
    >
      <Stack
        direction={{ xs: "column", lg: "row" }}
        spacing={1}
        sx={{ alignItems: { xs: "stretch", lg: "center" } }}
      >
        {showJoin ? (
          <Select
            size="small"
            value={joinValue || "AND"}
            onChange={(event) => onJoinChange?.(event.target.value)}
            sx={{ minWidth: 96 }}
          >
            <MenuItem value="AND">AND</MenuItem>
            <MenuItem value="OR">OR</MenuItem>
          </Select>
        ) : null}
        <Select
          size="small"
          value={criterion.field}
          onChange={(event) =>
            onChange({
              field: event.target.value,
              operator: resolveDefaultOperator(event.target.value),
              value: "",
              software_source: "",
              version_operator: "",
              version_value: "",
              use_regex: false,
            })
          }
          sx={{ minWidth: 210 }}
        >
          {fieldOptions.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </Select>
        <Select
          size="small"
          value={criterion.operator}
          onChange={(event) => onChange({ operator: event.target.value })}
          sx={{ minWidth: 160 }}
        >
          {operators.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </Select>
        {kind === "enum" && criterion.field === "status" ? (
          <Select
            size="small"
            value={criterion.value || ""}
            onChange={(event) => onChange({ value: event.target.value })}
            sx={{ minWidth: 160, flexGrow: 1 }}
          >
            {STATUS_OPTIONS.map((option) => (
              <MenuItem key={option} value={option}>
                {option}
              </MenuItem>
            ))}
          </Select>
        ) : (
          <TextField
            size="small"
            type={kind === "number" ? "number" : "text"}
            value={criterion.value}
            onChange={(event) => onChange({ value: event.target.value })}
            placeholder={isSoftware ? "Software name" : "Value"}
            sx={{ minWidth: 180, flexGrow: 1 }}
          />
        )}
        {isSoftware ? (
          <>
            <Select
              size="small"
              displayEmpty
              value={criterion.software_source || ""}
              onChange={(event) => onChange({ software_source: event.target.value })}
              sx={{ minWidth: 170 }}
            >
              <MenuItem value="">Any Source</MenuItem>
              {softwareSources.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </Select>
            <Select
              size="small"
              displayEmpty
              value={criterion.version_operator || ""}
              onChange={(event) => onChange({ version_operator: event.target.value })}
              sx={{ minWidth: 155 }}
            >
              <MenuItem value="">No Version Check</MenuItem>
              {softwareVersionOperators.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </Select>
            <TextField
              size="small"
              value={criterion.version_value || ""}
              onChange={(event) => onChange({ version_value: event.target.value })}
              placeholder="Version"
              sx={{ minWidth: 130 }}
            />
          </>
        ) : null}
        {supportsRegex ? (
          <FormControlLabel
            control={
              <Checkbox
                checked={Boolean(criterion.use_regex)}
                onChange={(event) => onChange({ use_regex: event.target.checked })}
              />
            }
            label="Regex"
            sx={{ mr: 0 }}
          />
        ) : null}
        <IconButton size="small" onClick={onRemove} sx={{ color: "#fb7185" }}>
          <DeleteIcon fontSize="small" />
        </IconButton>
      </Stack>
    </Paper>
  );
}

export default function DeviceFilterEditor({ initialFilter, onCancel, onSaved, onPageMetaChange }) {
  const filterId = initialFilter?.id ? Number(initialFilter.id) : null;
  const cancelRef = useRef(onCancel);
  const savedRef = useRef(onSaved);
  const [activeTab, setActiveTab] = useState("name");
  const [metadata, setMetadata] = useState(null);
  const [sites, setSites] = useState([]);
  const [formState, setFormState] = useState(() => normalizeFilterRecord(initialFilter));
  const [loading, setLoading] = useState(Boolean(filterId));
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [error, setError] = useState("");
  const [validationErrors, setValidationErrors] = useState([]);
  const [previewCount, setPreviewCount] = useState(null);
  const [previewRows, setPreviewRows] = useState([]);
  const previewGridRef = useRef(null);
  const formStateRef = useRef(formState);
  const previewSignatureRef = useRef(null);
  const [previewStale, setPreviewStale] = useState(false);

  const fieldOptions = useMemo(() => ensureArray(metadata?.fields), [metadata]);
  const fieldById = useMemo(() => {
    const next = {};
    fieldOptions.forEach((field) => {
      next[field.value] = field;
    });
    return next;
  }, [fieldOptions]);
  const operatorOptions = useMemo(
    () => mergeOperatorOptions(metadata?.operators || {}, DEFAULT_OPERATOR_OPTIONS),
    [metadata]
  );
  const softwareSources = useMemo(() => ensureArray(metadata?.software_sources), [metadata]);
  const softwareVersionOperators = useMemo(
    () => operatorOptions.software_version || [],
    [operatorOptions]
  );

  const hydrateFromRecord = useCallback((record) => {
    const normalized = normalizeFilterRecord(record);
    setFormState(normalized);
    setActiveTab("name");
    setValidationErrors([]);
    setError("");
    setPreviewRows([]);
    setPreviewCount(null);
    setPreviewStale(false);
    previewSignatureRef.current = null;
  }, []);

  const loadInitialData = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [metadataResp, sitesResp, filterResp] = await Promise.all([
        fetch("/api/device_filters/metadata"),
        fetch("/api/sites"),
        filterId ? fetch(`/api/device_filters/${filterId}`) : Promise.resolve(null),
      ]);

      const metadataPayload = await metadataResp.json().catch(() => ({}));
      if (!metadataResp.ok) {
        throw new Error(metadataPayload?.message || metadataPayload?.error || "Unable to load filter metadata.");
      }
      setMetadata(metadataPayload);

      const sitesPayload = await sitesResp.json().catch(() => ({}));
      setSites(Array.isArray(sitesPayload?.sites) ? sitesPayload.sites : []);

      if (filterResp) {
        const filterPayload = await filterResp.json().catch(() => ({}));
        if (!filterResp.ok) {
          throw new Error(filterPayload?.message || filterPayload?.error || "Unable to load filter.");
        }
        hydrateFromRecord(filterPayload?.filter || {});
      } else {
        hydrateFromRecord(initialFilter || {});
      }
    } catch (err) {
      setError(err?.message || "Unable to load filter editor.");
    } finally {
      setLoading(false);
    }
  }, [filterId, hydrateFromRecord, initialFilter]);

  useEffect(() => {
    loadInitialData();
  }, [loadInitialData]);

  useEffect(() => {
    cancelRef.current = onCancel;
  }, [onCancel]);

  useEffect(() => {
    savedRef.current = onSaved;
  }, [onSaved]);

  useEffect(() => {
    formStateRef.current = formState;
  }, [formState]);

  useEffect(() => {
    const lastPreviewSignature = previewSignatureRef.current;
    if (!lastPreviewSignature) return;
    const currentSignature = buildPreviewSignature(formState);
    if (currentSignature === lastPreviewSignature) return;
    previewSignatureRef.current = null;
    setPreviewStale(true);
    setPreviewRows([]);
    setPreviewCount(null);
  }, [formState]);

  const selectedSites = useMemo(() => {
    const selectedIds = new Set(ensureArray(formState.site_ids).map((value) => Number(value)));
    return ensureArray(sites).filter((site) => selectedIds.has(Number(site.id)));
  }, [formState.site_ids, sites]);

  const buildPayload = useCallback((state = formStateRef.current) => buildFilterPayload(state), []);

  const runPreview = useCallback(async () => {
    setPreviewing(true);
    setError("");
    setValidationErrors([]);
    try {
      const payload = buildPayload();
      const response = await fetch("/api/device_filters/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (Array.isArray(data?.validation_errors)) {
          setValidationErrors(data.validation_errors);
        }
        throw new Error(data?.message || data?.error || "Preview failed.");
      }
      setPreviewCount(Number(data?.matched_device_count || 0));
      setPreviewRows(Array.isArray(data?.devices) ? data.devices : []);
      previewSignatureRef.current = buildPreviewSignature(formStateRef.current);
      setPreviewStale(false);
      setActiveTab("results");
      requestAnimationFrame(() => {
        try {
          previewGridRef.current?.autoSizeColumns(PREVIEW_AUTO_SIZE_COLUMNS, true);
        } catch {
          /* ignore */
        }
      });
    } catch (err) {
      setPreviewRows([]);
      setPreviewCount(null);
      setError(err?.message || "Preview failed.");
    } finally {
      setPreviewing(false);
    }
  }, [buildPayload]);

  const saveFilter = useCallback(async () => {
    setSaving(true);
    setError("");
    setValidationErrors([]);
    try {
      const payload = buildPayload();
      const path = formState.id ? `/api/device_filters/${formState.id}` : "/api/device_filters";
      const method = formState.id ? "PUT" : "POST";
      const response = await fetch(path, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (Array.isArray(data?.validation_errors)) {
          setValidationErrors(data.validation_errors);
        }
        throw new Error(data?.message || data?.error || "Unable to save filter.");
      }
      savedRef.current?.(data?.filter || null);
    } catch (err) {
      setError(err?.message || "Unable to save filter.");
    } finally {
      setSaving(false);
    }
  }, [buildPayload, formState.id]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "filter-editor-cancel",
        label: "Cancel",
        icon: <CancelIcon />,
        tone: "secondary",
        onClick: () => cancelRef.current?.(),
      },
      {
        id: "filter-editor-preview",
        label: "Preview / Apply",
        icon: <PreviewIcon />,
        tone: "secondary",
        loading: previewing,
        disabled: loading || saving,
        onClick: runPreview,
      },
      {
        id: "filter-editor-save",
        label: "Save Filter",
        icon: <SaveIcon />,
        tone: "primary",
        loading: saving,
        disabled: loading || previewing,
        onClick: saveFilter,
      },
    ],
    [loading, previewing, runPreview, saveFilter, saving]
  );

  useEffect(() => {
    const pageTitle = formState?.id ? `Edit Filter: ${formState.name || "Unnamed Filter"}` : "Create Device Filter";
    onPageMetaChange?.({
      page_title: pageTitle,
      page_subtitle:
        "Define scope, build grouped criteria, and preview matching devices using the backend matcher before saving.",
      page_icon: PAGE_ICON,
      page_header_actions: pageHeaderActions,
    });
  }, [formState?.id, formState?.name, onPageMetaChange, pageHeaderActions]);

  const updateAdvancedGroup = useCallback((groupId, updater) => {
    setFormState((prev) => ({
      ...prev,
      advanced_criteria: prev.advanced_criteria.map((group) =>
        group.id === groupId ? updater(group) : group
      ),
    }));
  }, []);

  const addAdvancedGroup = useCallback(() => {
    setFormState((prev) => ({
      ...prev,
      advanced_criteria: [...prev.advanced_criteria, makeAdvancedGroup("OR")],
    }));
  }, []);

  const removeAdvancedGroup = useCallback((groupId) => {
    setFormState((prev) => {
      const next = prev.advanced_criteria.filter((group) => group.id !== groupId);
      return { ...prev, advanced_criteria: next.length ? next : [makeAdvancedGroup("")] };
    });
  }, []);

  const previewColumns = useMemo(
    () => [
      { field: "hostname", headerName: "Hostname", minWidth: 220, flex: 1.2 },
      { field: "site_name", headerName: "Site", minWidth: 180, flex: 1 },
      { field: "status", headerName: "Status", minWidth: 120, flex: 0.7 },
      { field: "operating_system", headerName: "Operating System", minWidth: 220, flex: 1.2 },
      { field: "description", headerName: "Description", minWidth: 240, flex: 1.4 },
    ],
    []
  );

  useEffect(() => {
    if (!previewRows.length || !previewGridRef.current) return;
    requestAnimationFrame(() => {
      try {
        previewGridRef.current.autoSizeColumns(PREVIEW_AUTO_SIZE_COLUMNS, true);
      } catch {
        /* ignore */
      }
    });
  }, [previewRows]);

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      flex: 1,
      filter: "agTextColumnFilter",
      cellClass: "auto-col-tight",
    }),
    []
  );

  const scopeCards = (
    <Stack spacing={2}>
      <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
        {ensureArray(metadata?.site_modes).map((mode) => {
          const selected = formState.site_mode === mode.value;
          return (
            <Paper
              key={mode.value}
              variant="outlined"
              onClick={() => setFormState((prev) => ({ ...prev, site_mode: mode.value }))}
              sx={{
                flex: 1,
                px: 2,
                py: 1.5,
                cursor: "pointer",
                borderColor: selected ? "#7dd3fc" : "rgba(148,163,184,0.24)",
                background: selected ? "rgba(125,211,252,0.12)" : "rgba(15,23,42,0.45)",
              }}
            >
              <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>{mode.label}</Typography>
            </Paper>
          );
        })}
      </Stack>
      {formState.site_mode !== "global" ? (
        <Autocomplete
          multiple
          options={sites}
          value={selectedSites}
          getOptionLabel={(option) => option?.name || ""}
          onChange={(_, values) =>
            setFormState((prev) => ({
              ...prev,
              site_ids: values.map((item) => Number(item.id)).filter((value) => Number.isFinite(value)),
            }))
          }
          renderInput={(params) => <TextField {...params} label="Sites" placeholder="Select sites" />}
        />
      ) : (
        <Typography sx={{ color: "#94a3b8" }}>
          Global scope applies to all sites. Use the criteria builder to target device attributes across the full fleet.
        </Typography>
      )}
    </Stack>
  );

  const criteriaTab = (
    <Stack spacing={2}>
      <Stack spacing={1.5}>
        {formState.advanced_criteria.map((group, groupIndex) => (
          <Paper
            key={group.id}
            variant="outlined"
            sx={{
              p: 1.5,
              background: "rgba(15,23,42,0.42)",
              borderColor: "rgba(148,163,184,0.22)",
            }}
          >
            <Stack spacing={1.2}>
              <Box sx={{ display: "flex", alignItems: "center", gap: 1, justifyContent: "space-between" }}>
                <Stack direction="row" spacing={1} alignItems="center">
                  {groupIndex > 0 ? (
                    <Select
                      size="small"
                      value={group.join_with || "OR"}
                      onChange={(event) =>
                        updateAdvancedGroup(group.id, (current) => ({
                          ...current,
                          join_with: event.target.value,
                        }))
                      }
                      sx={{ minWidth: 96 }}
                    >
                      <MenuItem value="AND">AND</MenuItem>
                      <MenuItem value="OR">OR</MenuItem>
                    </Select>
                  ) : (
                    <Chip label="Starting Group" size="small" />
                  )}
                  <Typography sx={{ color: "#e2e8f0", fontWeight: 700 }}>
                    Condition Group {groupIndex + 1}
                  </Typography>
                </Stack>
                <IconButton size="small" onClick={() => removeAdvancedGroup(group.id)} sx={{ color: "#fb7185" }}>
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Box>
              {ensureArray(group.conditions).map((condition, conditionIndex) => (
                <FilterCriterionRow
                  key={condition.id}
                  criterion={condition}
                  showJoin={conditionIndex > 0}
                  joinValue={condition.join_with || "AND"}
                  onJoinChange={(value) =>
                    updateAdvancedGroup(group.id, (current) => ({
                      ...current,
                      conditions: current.conditions.map((item) =>
                        item.id === condition.id ? { ...item, join_with: value } : item
                      ),
                    }))
                  }
                  onChange={(patch) =>
                    updateAdvancedGroup(group.id, (current) => ({
                      ...current,
                      conditions: current.conditions.map((item) =>
                        item.id === condition.id ? { ...item, ...patch } : item
                      ),
                    }))
                  }
                  onRemove={() =>
                    updateAdvancedGroup(group.id, (current) => {
                      const nextConditions = current.conditions.filter((item) => item.id !== condition.id);
                      return {
                        ...current,
                        conditions: nextConditions.length ? nextConditions : [makeAdvancedCondition("")],
                      };
                    })
                  }
                  fieldOptions={fieldOptions}
                  fieldById={fieldById}
                  operatorOptions={operatorOptions}
                  softwareVersionOperators={softwareVersionOperators}
                  softwareSources={softwareSources}
                />
              ))}
              <Button
                variant="outlined"
                startIcon={<AddIcon />}
                onClick={() =>
                  updateAdvancedGroup(group.id, (current) => ({
                    ...current,
                    conditions: [...current.conditions, makeAdvancedCondition("AND")],
                  }))
                }
                sx={{ width: "fit-content", textTransform: "none" }}
              >
                Add Condition
              </Button>
            </Stack>
          </Paper>
        ))}
        <Button
          variant="outlined"
          startIcon={<AddIcon />}
          onClick={addAdvancedGroup}
          sx={{ width: "fit-content", textTransform: "none" }}
        >
          Add Group
        </Button>
      </Stack>
    </Stack>
  );

  const topStack = (
    <Stack spacing={1.5}>
      {loading || saving || previewing ? <LinearProgress /> : null}
      {error ? <Alert severity="error">{error}</Alert> : null}
      {validationErrors.length ? (
        <Alert severity="warning">
          <Stack spacing={0.5}>
            {validationErrors.map((message) => (
              <Typography key={message} variant="body2">
                {message}
              </Typography>
            ))}
          </Stack>
        </Alert>
      ) : null}
      <Box sx={{ display: "flex", alignItems: "center", gap: 1, flexWrap: "wrap" }}>
        {formState.last_edited_by ? <Chip label={`Last Edited By: ${formState.last_edited_by}`} size="small" /> : null}
        {formState.updated_at ? <Chip label={`Updated: ${formatTimestamp(formState.updated_at)}`} size="small" /> : null}
      </Box>
    </Stack>
  );

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={topStack}
      main={
        <Box sx={{ display: "flex", flexDirection: "column", minHeight: 0, height: "100%" }}>
          <Tabs
            value={activeTab}
            onChange={(_, value) => setActiveTab(value)}
            variant="scrollable"
            scrollButtons="auto"
            TabIndicatorProps={{
              style: {
                height: 3,
                borderRadius: 3,
                background: NAV_TAB_COLORS.iconActive,
              },
            }}
            sx={buildNavTabsSx()}
          >
            {MAIN_TABS.map((tab) => (
              <Tab key={tab.value} value={tab.value} label={tab.label} />
            ))}
          </Tabs>
          <Box sx={{ flexGrow: 1, minHeight: 0, px: 3, pt: 2.5, pb: 3, display: "flex", flexDirection: "column" }}>
            {activeTab === "name" ? (
              <Stack spacing={2}>
                <TextField
                  label="Filter Name"
                  value={formState.name}
                  onChange={(event) => setFormState((prev) => ({ ...prev, name: event.target.value }))}
                  fullWidth
                />
                <TextField
                  label="Description"
                  value={formState.description}
                  onChange={(event) => setFormState((prev) => ({ ...prev, description: event.target.value }))}
                  fullWidth
                />
              </Stack>
            ) : null}
            {activeTab === "scope" ? scopeCards : null}
            {activeTab === "criteria" ? criteriaTab : null}
            {activeTab === "results" ? (
              <Stack spacing={1.5} sx={{ minHeight: 0, flexGrow: 1, height: "100%" }}>
                <Typography sx={{ color: "#e2e8f0", fontWeight: 700 }}>
                  {previewCount == null
                    ? previewStale
                      ? "Preview results are out of date."
                      : "No preview has been run yet."
                    : `${previewCount.toLocaleString()} device(s) matched.`}
                </Typography>
                {previewStale ? (
                  <Alert severity="info">
                    The scope or criteria changed after the last preview. Run <strong>Preview / Apply</strong> again to
                    refresh these results.
                  </Alert>
                ) : null}
                <Box sx={GRID_WRAPPER_SX}>
                  <AgGridReact
                    rowData={previewRows}
                    columnDefs={previewColumns}
                    defaultColDef={defaultColDef}
                    suppressCellFocus
                    animateRows
                    pagination
                    paginationPageSize={20}
                    paginationPageSizeSelector={[20, 50, 100]}
                    onGridReady={(params) => {
                      previewGridRef.current = params.api;
                    }}
                    getRowId={(params) => String(params.data?.guid || params.data?.hostname || "")}
                    theme={gridTheme}
                    style={{
                      width: "100%",
                      height: "100%",
                      fontFamily: gridFontFamily,
                      "--ag-icon-font-family": iconFontFamily,
                    }}
                  />
                </Box>
              </Stack>
            ) : null}
          </Box>
        </Box>
      }
    />
  );
}
