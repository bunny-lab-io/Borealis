import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Checkbox,
  Chip,
  Divider,
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
  const advanced = ensureArray(record?.advanced_criteria?.groups).map((group, index) => ({
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

  return {
    id: record?.id || null,
    name: record?.name || "",
    description: record?.description || "",
    criteria_mode: String(record?.criteria_mode || "basic").toLowerCase() === "advanced" ? "advanced" : "basic",
    site_mode: String(record?.site_mode || "global").toLowerCase() || "global",
    site_ids: ensureArray(record?.site_ids).map((value) => Number(value)).filter((value) => Number.isFinite(value)),
    basic_criteria: basic.length ? basic : [makeBasicCriterion()],
    advanced_criteria: advanced.length ? advanced : [makeAdvancedGroup("")],
    archived: Boolean(record?.archived),
    last_edited_by: record?.last_edited_by || "",
    updated_at: record?.updated_at || 0,
  };
};

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

  const fieldOptions = useMemo(() => ensureArray(metadata?.fields), [metadata]);
  const fieldById = useMemo(() => {
    const next = {};
    fieldOptions.forEach((field) => {
      next[field.value] = field;
    });
    return next;
  }, [fieldOptions]);
  const operatorOptions = useMemo(() => metadata?.operators || { text: [], number: [], enum: [] }, [metadata]);
  const softwareSources = useMemo(() => ensureArray(metadata?.software_sources), [metadata]);
  const softwareVersionOperators = useMemo(
    () => ensureArray(metadata?.operators?.software_version),
    [metadata]
  );

  useEffect(() => {
    const pageTitle = formState?.id ? `Edit Filter: ${formState.name || "Unnamed Filter"}` : "Create Device Filter";
    onPageMetaChange?.({
      title: pageTitle,
      subtitle:
        "Define scope, choose a criteria mode, and preview matching devices using the backend matcher before saving.",
      Icon: PAGE_ICON,
    });
  }, [formState?.id, formState?.name, onPageMetaChange]);

  const hydrateFromRecord = useCallback((record) => {
    const normalized = normalizeFilterRecord(record);
    setFormState(normalized);
    setActiveTab("name");
    setValidationErrors([]);
    setError("");
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

  const selectedSites = useMemo(() => {
    const selectedIds = new Set(ensureArray(formState.site_ids).map((value) => Number(value)));
    return ensureArray(sites).filter((site) => selectedIds.has(Number(site.id)));
  }, [formState.site_ids, sites]);

  const buildPayload = useCallback(() => {
    const basicCriteria = ensureArray(formState.basic_criteria).map((criterion) => ({
      field: criterion.field,
      operator: criterion.operator,
      value: criterion.value,
      use_regex: Boolean(criterion.use_regex),
      software_source: criterion.software_source || "",
      version_operator: criterion.version_operator || "",
      version_value: criterion.version_value || "",
    }));
    const advancedCriteria = ensureArray(formState.advanced_criteria).map((group, groupIndex) => ({
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

    return {
      name: formState.name,
      description: formState.description,
      criteria_mode: formState.criteria_mode,
      site_mode: formState.site_mode,
      site_ids: ensureArray(formState.site_ids),
      basic_criteria: { criteria: basicCriteria },
      advanced_criteria: { groups: advancedCriteria },
    };
  }, [formState]);

  const runPreview = useCallback(async () => {
    setPreviewing(true);
    setError("");
    setValidationErrors([]);
    try {
      const response = await fetch("/api/device_filters/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(buildPayload()),
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
      setActiveTab("results");
      requestAnimationFrame(() => {
        try {
          previewGridRef.current?.autoSizeColumns(["hostname", "site_name", "status", "operating_system"], true);
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
      onSaved?.(data?.filter || null);
    } catch (err) {
      setError(err?.message || "Unable to save filter.");
    } finally {
      setSaving(false);
    }
  }, [buildPayload, formState.id, onSaved]);

  const updateBasicCriterion = useCallback((id, patch) => {
    setFormState((prev) => ({
      ...prev,
      basic_criteria: prev.basic_criteria.map((criterion) =>
        criterion.id === id ? { ...criterion, ...patch } : criterion
      ),
    }));
  }, []);

  const removeBasicCriterion = useCallback((id) => {
    setFormState((prev) => {
      const next = prev.basic_criteria.filter((criterion) => criterion.id !== id);
      return { ...prev, basic_criteria: next.length ? next : [makeBasicCriterion()] };
    });
  }, []);

  const addBasicCriterion = useCallback(() => {
    setFormState((prev) => ({
      ...prev,
      basic_criteria: [...prev.basic_criteria, makeBasicCriterion()],
    }));
  }, []);

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
        previewGridRef.current.autoSizeColumns(["hostname", "site_name", "status", "operating_system"], true);
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
      cellStyle: {
        display: "flex",
        alignItems: "center",
      },
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
      <Tabs
        value={formState.criteria_mode}
        onChange={(_, value) => setFormState((prev) => ({ ...prev, criteria_mode: value }))}
        sx={{ "& .MuiTabs-indicator": { backgroundColor: "#7dd3fc" } }}
      >
        <Tab value="basic" label="Basic" sx={{ textTransform: "none" }} />
        <Tab value="advanced" label="Advanced" sx={{ textTransform: "none" }} />
      </Tabs>
      {formState.criteria_mode === "basic" ? (
        <Stack spacing={1.1}>
          {formState.basic_criteria.map((criterion) => (
            <FilterCriterionRow
              key={criterion.id}
              criterion={criterion}
              onChange={(patch) => updateBasicCriterion(criterion.id, patch)}
              onRemove={() => removeBasicCriterion(criterion.id)}
              fieldOptions={fieldOptions.filter((field) => field.basic !== false)}
              fieldById={fieldById}
              operatorOptions={operatorOptions}
              softwareVersionOperators={softwareVersionOperators}
              softwareSources={softwareSources}
            />
          ))}
          <Button
            variant="outlined"
            startIcon={<AddIcon />}
            onClick={addBasicCriterion}
            sx={{ width: "fit-content", textTransform: "none" }}
          >
            Add Criterion
          </Button>
        </Stack>
      ) : (
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
                    fieldOptions={fieldOptions.filter((field) => field.advanced !== false)}
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
      )}
    </Stack>
  );

  const topStack = (
    <Stack spacing={1.5}>
      <Box sx={{ display: "flex", alignItems: { xs: "stretch", md: "center" }, gap: 1.1, flexWrap: "wrap" }}>
        <Button
          variant="contained"
          startIcon={<SaveIcon />}
          onClick={saveFilter}
          disabled={saving || loading}
          sx={{ textTransform: "none" }}
        >
          {saving ? "Saving..." : "Save Filter"}
        </Button>
        <Button
          variant="outlined"
          startIcon={<PreviewIcon />}
          onClick={runPreview}
          disabled={previewing || loading}
          sx={{ textTransform: "none" }}
        >
          {previewing ? "Previewing..." : "Preview / Apply"}
        </Button>
        <Button variant="text" onClick={() => onCancel?.()} sx={{ textTransform: "none", ml: { md: "auto" } }}>
          Cancel
        </Button>
      </Box>
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
        <Chip label={`Mode: ${formState.criteria_mode === "advanced" ? "Advanced" : "Basic"}`} size="small" />
      </Box>
    </Stack>
  );

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={topStack}
      main={
        <Box sx={{ p: 3, display: "flex", flexDirection: "column", gap: 2, minHeight: 0 }}>
          <Tabs
            value={activeTab}
            onChange={(_, value) => setActiveTab(value)}
            sx={{ "& .MuiTabs-indicator": { backgroundColor: "#7dd3fc" } }}
          >
            {MAIN_TABS.map((tab) => (
              <Tab key={tab.value} value={tab.value} label={tab.label} sx={{ textTransform: "none" }} />
            ))}
          </Tabs>
          <Divider />
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
            <Stack spacing={1.5} sx={{ minHeight: 0, flexGrow: 1 }}>
              <Typography sx={{ color: "#e2e8f0", fontWeight: 700 }}>
                {previewCount == null ? "No preview has been run yet." : `${previewCount.toLocaleString()} device(s) matched.`}
              </Typography>
              <Box sx={{ minHeight: 420, flexGrow: 1 }}>
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
      }
    />
  );
}
