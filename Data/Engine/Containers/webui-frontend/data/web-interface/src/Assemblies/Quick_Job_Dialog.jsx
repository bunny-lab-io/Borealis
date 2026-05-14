import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  MenuItem,
  TextField,
  Typography,
} from "@mui/material";
import PlayArrowRoundedIcon from "@mui/icons-material/PlayArrowRounded";
import ArrowBackRoundedIcon from "@mui/icons-material/ArrowBackRounded";
import AssemblyPicker from "./Assembly_Picker";
import {
  buildAssemblyIndex,
  normalizeAssemblyPath,
  parseAssembliesCollectionPayload,
  parseAssemblyExport,
} from "./assemblyUtils";
import {
  DIALOG_BUTTON_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DIALOG_CONTENT_SX,
} from "../DialogStyles.jsx";

const MAGIC_UI = "#8fd3ff";

function normalizeVariableDefinitions(vars) {
  if (!Array.isArray(vars)) return [];
  return vars
    .map((variable, index) => {
      if (!variable || typeof variable !== "object") return null;
      const key = String(variable.key || variable.name || variable.id || "").trim();
      if (!key) return null;
      const label = String(variable.label || variable.display_name || variable.name || key).trim();
      const rawType = String(variable.type || variable.input_type || "string").toLowerCase();
      const type = ["boolean", "bool"].includes(rawType)
        ? "boolean"
        : ["number", "integer", "int", "float"].includes(rawType)
          ? "number"
          : rawType === "select" || rawType === "choice"
            ? "select"
            : rawType === "secret" || rawType === "password"
              ? "secret"
              : "string";
      const options = Array.isArray(variable.options)
        ? variable.options.map((option) => {
            if (option && typeof option === "object") {
              return {
                value: String(option.value ?? option.key ?? option.label ?? ""),
                label: String(option.label ?? option.value ?? option.key ?? ""),
              };
            }
            return { value: String(option ?? ""), label: String(option ?? "") };
          })
        : [];
      const defaultValue = variable.default ?? variable.default_value ?? variable.value ?? "";
      return {
        ...variable,
        key,
        name: key,
        label,
        type,
        options,
        required: Boolean(variable.required),
        description: variable.description || "",
        value: defaultValue,
        sortOrder: Number.isFinite(Number(variable.order)) ? Number(variable.order) : index,
      };
    })
    .filter(Boolean)
    .sort((left, right) => left.sortOrder - right.sortOrder || left.label.localeCompare(right.label));
}

function coerceVariableValue(type, value) {
  if (type === "boolean") return Boolean(value);
  if (type === "number") {
    if (value === "" || value === null || value === undefined) return "";
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : value;
  }
  return value ?? "";
}

function mergeVariableValues(docVars, storedVars = [], storedValueMap = {}) {
  const definitions = normalizeVariableDefinitions(docVars);
  const storedByKey = new Map(
    normalizeVariableDefinitions(storedVars).map((variable) => [variable.key, variable]),
  );
  return definitions.map((definition) => {
    const stored = storedByKey.get(definition.key);
    const hasStoredMapValue = Object.prototype.hasOwnProperty.call(storedValueMap || {}, definition.key);
    const value = hasStoredMapValue
      ? storedValueMap[definition.key]
      : stored && Object.prototype.hasOwnProperty.call(stored, "value")
        ? stored.value
        : definition.value;
    return {
      ...definition,
      ...(stored || {}),
      value: coerceVariableValue(definition.type, value),
    };
  });
}

function inferAssemblyKind(record, parsed) {
  const kind = String(parsed?.type || record?.kind || record?.type || "").toLowerCase();
  if (kind.includes("workflow")) return "workflow";
  if (kind.includes("playbook") || kind.includes("ansible")) return "ansible";
  return "script";
}

function notify(notifyOperator, title, message, variant = "info") {
  if (typeof notifyOperator !== "function") return;
  notifyOperator({
    title,
    message,
    icon: variant === "error" ? "error" : "play_arrow",
    variant,
  });
}

export default function QuickJobDialog({
  open,
  onClose,
  hostnames = [],
  targetRecords = [],
  deviceLabel = "",
  notifyOperator,
  onQueued,
}) {
  const [assembliesLoading, setAssembliesLoading] = useState(false);
  const [assembliesError, setAssembliesError] = useState("");
  const [assemblyIndex, setAssemblyIndex] = useState(() => buildAssemblyIndex([], []));
  const [selectedRecord, setSelectedRecord] = useState(null);
  const [step, setStep] = useState("picker");
  const [exportState, setExportState] = useState({ loading: false, error: "", parsed: null });
  const [variables, setVariables] = useState([]);
  const [submitting, setSubmitting] = useState(false);

  const validHostnames = useMemo(
    () => Array.from(new Set((hostnames || []).map((hostname) => String(hostname || "").trim()).filter(Boolean))),
    [hostnames],
  );

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setStep("picker");
    setSelectedRecord(null);
    setVariables([]);
    setExportState({ loading: false, error: "", parsed: null });
    setAssembliesLoading(true);
    setAssembliesError("");
    fetch("/api/assemblies", { credentials: "include" })
      .then(async (response) => {
        const body = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(body?.error || "Unable to load assemblies");
        return body;
      })
      .then((body) => {
        if (cancelled) return;
        const parsed = parseAssembliesCollectionPayload(body);
        setAssemblyIndex(buildAssemblyIndex(parsed.items, parsed.queue));
      })
      .catch((error) => {
        if (cancelled) return;
        setAssembliesError(error?.message || "Unable to load assemblies");
      })
      .finally(() => {
        if (!cancelled) setAssembliesLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  const selectedGuid = String(selectedRecord?.assemblyGuid || "").toLowerCase();
  const selectedName = selectedRecord?.displayName || selectedRecord?.name || "Selected Assembly";

  const loadSelectedExport = useCallback(async () => {
    if (!selectedGuid) throw new Error("Choose an assembly");
    setExportState({ loading: true, error: "", parsed: null });
    const response = await fetch(`/api/assemblies/${encodeURIComponent(selectedGuid)}/export`, {
      credentials: "include",
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const message = body?.error || "Unable to load assembly";
      setExportState({ loading: false, error: message, parsed: null });
      throw new Error(message);
    }
    const parsed = parseAssemblyExport(body);
    const nextVariables = mergeVariableValues(
      parsed.rawVariables || parsed.variables || [],
      selectedRecord?.variables || [],
      selectedRecord?.variable_values || {},
    );
    setVariables(nextVariables);
    setExportState({ loading: false, error: "", parsed });
    return { parsed, variables: nextVariables };
  }, [selectedGuid, selectedRecord]);

  const buildTargets = useCallback(() => {
    const recordByHostname = new Map(
      (targetRecords || [])
        .map((record) => [String(record?.hostname || record?.device_hostname || "").trim().toLowerCase(), record])
        .filter(([hostname]) => Boolean(hostname)),
    );
    return validHostnames.map((hostname, index) => {
      const record = recordByHostname.get(hostname.toLowerCase()) || targetRecords?.[index] || {};
      return {
        kind: "device",
        hostname,
        device_guid: record.device_guid || record.deviceGuid || record.id || null,
        site_id: record.site_id || record.siteId || null,
        site_name: record.site_name || record.siteName || null,
      };
    });
  }, [targetRecords, validHostnames]);

  const queueJob = useCallback(
    async (parsedOverride = null, variablesOverride = null) => {
      if (!selectedRecord) return;
      if (!validHostnames.length) {
        setExportState((current) => ({ ...current, error: "No target devices selected" }));
        return;
      }
      const parsed = parsedOverride || exportState.parsed || (await loadSelectedExport()).parsed;
      const activeVariables = variablesOverride || variables;
      const valueMap = activeVariables.reduce((acc, variable) => {
        acc[variable.key] = variable.value;
        return acc;
      }, {});
      const kind = inferAssemblyKind(selectedRecord, parsed);
      const executionContext = kind === "ansible" ? "ssh_individual" : "system";
      const componentPath = normalizeAssemblyPath(
        kind,
        selectedRecord.path || selectedRecord.folder_path || parsed.sourcePath || "",
        selectedName,
      );
      const payload = {
        name: `Quick Job - ${selectedName} - ${deviceLabel || validHostnames.join(", ")}`,
        description: "",
        enabled: true,
        execution_context: executionContext,
        credential_id: null,
        use_service_account: false,
        schedule: {
          type: "immediately",
          start: null,
          interval_value: null,
          interval_unit: "minutes",
          cron_expression: "",
          timezone: "UTC",
        },
        duration: {
          stopAfterEnabled: false,
          stopAfterMinutes: 0,
          expiration: "no_expire",
          expirationValue: null,
          expirationUnit: "minutes",
        },
        targets: buildTargets(),
        components: [
          {
            id: `${kind}-${selectedGuid || Date.now()}`,
            type: kind,
            assembly_type: kind,
            assembly_subtype: parsed.type || selectedRecord.kind || kind,
            assembly_guid: selectedGuid,
            name: selectedName,
            display_name: selectedName,
            path: componentPath,
            description: selectedRecord.summary || selectedRecord.description || parsed.metadata?.summary || "",
            variables: activeVariables,
            variable_values: valueMap,
          },
        ],
      };

      setSubmitting(true);
      try {
        const response = await fetch("/api/scheduled_jobs", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "include",
          body: JSON.stringify(payload),
        });
        const body = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(body?.error || "Unable to queue quick job");
        notify(notifyOperator, "Quick Job Queued", `${selectedName} queued for ${validHostnames.length} device(s).`, "info");
        onQueued?.(body);
        onClose?.();
      } catch (error) {
        const message = error?.message || "Unable to queue quick job";
        setExportState((current) => ({ ...current, error: message }));
        notify(notifyOperator, "Quick Job Failed", message, "error");
      } finally {
        setSubmitting(false);
      }
    },
    [
      buildTargets,
      deviceLabel,
      exportState.parsed,
      loadSelectedExport,
      notifyOperator,
      onClose,
      onQueued,
      selectedGuid,
      selectedName,
      selectedRecord,
      validHostnames,
      variables,
    ],
  );

  const handleNext = useCallback(async () => {
    if (!selectedRecord || submitting || exportState.loading) return;
    try {
      const loaded = await loadSelectedExport();
      if (loaded.variables.length) {
        setStep("variables");
        return;
      }
      await queueJob(loaded.parsed, loaded.variables);
    } catch {
      // Error state already set for dialog rendering.
    }
  }, [exportState.loading, loadSelectedExport, queueJob, selectedRecord, submitting]);

  const updateVariable = useCallback((key, value) => {
    setVariables((current) =>
      current.map((variable) =>
        variable.key === key ? { ...variable, value: coerceVariableValue(variable.type, value) } : variable,
      ),
    );
  }, []);

  const renderVariableField = (variable) => {
    if (variable.type === "boolean") {
      return (
        <FormControlLabel
          key={variable.key}
          control={
            <Checkbox
              checked={Boolean(variable.value)}
              onChange={(event) => updateVariable(variable.key, event.target.checked)}
              sx={{
                color: "rgba(143, 211, 255, 0.65)",
                "&.Mui-checked": { color: MAGIC_UI },
              }}
            />
          }
          label={variable.label}
          sx={{
            m: 0,
            p: 1.25,
            borderRadius: 1,
            border: "1px solid rgba(143, 211, 255, 0.14)",
            background: "rgba(4, 16, 28, 0.62)",
            color: "#f5fbff",
          }}
        />
      );
    }
    const commonSx = {
      "& .MuiOutlinedInput-root": {
        color: "#f5fbff",
        background: "rgba(4, 16, 28, 0.72)",
        "& fieldset": { borderColor: "rgba(143, 211, 255, 0.18)" },
        "&:hover fieldset": { borderColor: "rgba(143, 211, 255, 0.38)" },
        "&.Mui-focused fieldset": { borderColor: MAGIC_UI },
      },
      "& .MuiInputLabel-root": { color: "rgba(216, 236, 255, 0.72)" },
      "& .MuiFormHelperText-root": { color: "rgba(216, 236, 255, 0.55)" },
    };
    return (
      <TextField
        key={variable.key}
        label={variable.label}
        value={variable.value ?? ""}
        onChange={(event) => updateVariable(variable.key, event.target.value)}
        required={variable.required}
        select={variable.type === "select"}
        type={variable.type === "secret" ? "password" : variable.type === "number" ? "number" : "text"}
        helperText={variable.description || " "}
        fullWidth
        size="small"
        sx={commonSx}
      >
        {variable.type === "select"
          ? variable.options.map((option) => (
              <MenuItem key={option.value} value={option.value}>
                {option.label}
              </MenuItem>
            ))
          : null}
      </TextField>
    );
  };

  return (
    <Dialog
      open={open}
      onClose={submitting || exportState.loading ? undefined : onClose}
      maxWidth={false}
      fullWidth
      PaperProps={{
        sx: {
          ...DIALOG_PAPER_SX,
          display: "flex",
          flexDirection: "column",
          width: "95vw",
          maxWidth: "95vw",
          height: "95vh",
          maxHeight: "95vh",
        },
      }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <Box
          sx={{
            display: "flex",
            alignItems: "flex-start",
            justifyContent: "space-between",
            gap: 2,
            flexWrap: "wrap",
          }}
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="h6" sx={{ color: "#f5fbff", fontWeight: 800, lineHeight: 1.2 }}>
              {step === "variables" ? "Assembly Variables" : "Select Quick Job Assembly"}
            </Typography>
            <Typography variant="caption" sx={{ color: "rgba(216, 236, 255, 0.62)" }}>
              {step === "variables" ? selectedName : "Choose an assembly to run against selected devices."}
            </Typography>
          </Box>
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, ml: "auto", flexWrap: "wrap" }}>
            {step === "variables" ? (
              <Button
                onClick={() => setStep("picker")}
                startIcon={<ArrowBackRoundedIcon />}
                disabled={submitting || exportState.loading}
                sx={DIALOG_BUTTON_SX}
              >
                Back
              </Button>
            ) : null}
            <Button onClick={onClose} disabled={submitting || exportState.loading} sx={DIALOG_BUTTON_SX}>
              Cancel
            </Button>
            <Button
              onClick={step === "variables" ? () => queueJob() : handleNext}
              variant="contained"
              startIcon={submitting || exportState.loading ? <CircularProgress size={16} color="inherit" /> : <PlayArrowRoundedIcon />}
              disabled={!selectedRecord || !validHostnames.length || submitting || exportState.loading}
              sx={DIALOG_PRIMARY_BUTTON_SX}
            >
              {step === "variables" ? "Run Now" : "Next"}
            </Button>
          </Box>
        </Box>
      </DialogTitle>
      <DialogContent
        sx={{
          ...DIALOG_CONTENT_SX,
          display: "flex",
          flexDirection: "column",
          gap: 2,
          flex: 1,
          minHeight: 0,
          pb: 3,
          pt: 3.5,
          overflow: "hidden",
        }}
      >
        {validHostnames.length ? (
          <Box sx={{ color: "rgba(216, 236, 255, 0.72)", fontSize: "0.84rem", fontWeight: 700 }}>
            Targets: {validHostnames.join(", ")}
          </Box>
        ) : (
          <Alert severity="warning">Select at least one device before launching quick job.</Alert>
        )}
        {exportState.error ? <Alert severity="error">{exportState.error}</Alert> : null}
        {step === "picker" ? (
          <Box sx={{ flex: 1, minHeight: 0, height: "100%" }}>
            <AssemblyPicker
              records={assemblyIndex.records}
              loading={assembliesLoading}
              error={assembliesError}
              allowedKinds={["script", "ansible"]}
              selectedAssemblyGuid={selectedGuid}
              onSelectionChange={setSelectedRecord}
              onChoose={setSelectedRecord}
              height="100%"
            />
          </Box>
        ) : (
          <Box sx={{ display: "grid", gap: 1.5, overflow: "auto", minHeight: 0 }}>
            <Box>
              <Typography variant="subtitle1" sx={{ color: "#f5fbff", fontWeight: 800 }}>
                {selectedName}
              </Typography>
              <Typography variant="body2" sx={{ color: "rgba(216, 236, 255, 0.64)" }}>
                {variables.length} custom field{variables.length === 1 ? "" : "s"}
              </Typography>
            </Box>
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", md: "repeat(2, minmax(0, 1fr))" },
                gap: 1.5,
              }}
            >
              {variables.map(renderVariableField)}
            </Box>
          </Box>
        )}
      </DialogContent>
    </Dialog>
  );
}
