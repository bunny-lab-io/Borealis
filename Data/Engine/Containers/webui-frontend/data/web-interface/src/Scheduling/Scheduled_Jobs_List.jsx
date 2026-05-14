import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState
} from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Paper,
  Box,
  Typography,
  Button,
  Switch,
  Tooltip,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  CircularProgress,
} from "@mui/material";
import {
  Schedule as HeaderIcon,
  Cached as CachedIcon,
  Add as AddIcon,
  PlayArrow as PlayArrowIcon,
  DeleteOutline as DeleteIcon,
  WarningAmberRounded as WarningAmberRoundedIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { DomainBadge, resolveDomainMeta } from "../Assemblies/Assembly_Badges";
import {
  buildAssemblyIndex,
  parseAssembliesCollectionPayload,
  resolveAssemblyForComponent
} from "../Assemblies/assemblyUtils";
import PageBodyFrame from "../PageBodyFrame.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";

// -----------------------------------------------------------------------------
//  Register AG Grid community modules
// -----------------------------------------------------------------------------
ModuleRegistry.registerModules([AllCommunityModule]);

// -----------------------------------------------------------------------------
//  MagicUI x Quartz Theme (parity with Page_Style_Template)
// -----------------------------------------------------------------------------
const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";

// Typography
const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";

// Aurora gradient shell colors
const AURORA_SHELL = {
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  text: "#e2e8f0",
  subtext: "#9ba3b4",
  accent: "#7dd3fc",
};

const PAGE_TITLE = "Scheduled Jobs";
const PAGE_SUBTITLE = "Monitor scheduled, recurring, and completed Borealis jobs with live status.";
const PAGE_ICON = HeaderIcon;

const FILTER_OPTIONS = [
  { key: "all", label: "All" },
  { key: "immediate", label: "Immediate" },
  { key: "scheduled", label: "Scheduled" },
  { key: "recurring", label: "Recurring" },
  { key: "completed", label: "Completed" },
];
const AUTO_SIZE_COLUMNS = [
  "name",
  "componentsMeta",
  "jobType",
  "target",
  "occurrence",
  "lastRun",
  "result",
  "nextRun",
  "resultsCounts",
];

const STATUS_STYLES = {
  running: { label: "Running", color: "#58a6ff", background: "rgba(88,166,255,0.14)", border: "rgba(88,166,255,0.35)" },
  failed: { label: "Failed", color: "#ff7b72", background: "rgba(255,79,79,0.14)", border: "rgba(255,79,79,0.35)" },
  "timed out": { label: "Timed Out", color: "#d8b4fe", background: "rgba(179,106,226,0.16)", border: "rgba(179,106,226,0.35)" },
  warning: { label: "Warning", color: "#fbbf24", background: "rgba(251,191,36,0.15)", border: "rgba(251,191,36,0.34)" },
  expired: { label: "Expired", color: "#cbd5e1", background: "rgba(119,119,119,0.16)", border: "rgba(148,163,184,0.28)" },
  pending: { label: "Pending", color: "#cbd5e1", background: "rgba(148,163,184,0.12)", border: "rgba(148,163,184,0.24)" },
  scheduled: { label: "Scheduled", color: "#cbd5e1", background: "rgba(148,163,184,0.12)", border: "rgba(148,163,184,0.24)" },
  success: { label: "Success", color: "#00d18c", background: "rgba(0,209,140,0.14)", border: "rgba(0,209,140,0.34)" },
  skipped: { label: "Skipped", color: "#f0c36d", background: "rgba(240,195,109,0.14)", border: "rgba(240,195,109,0.34)" },
  "no eligible targets": { label: "No Eligible Targets", color: "#f0c36d", background: "rgba(240,195,109,0.14)", border: "rgba(240,195,109,0.34)" },
  "no devices targeted": { label: "No Devices Targeted", color: "#f0c36d", background: "rgba(240,195,109,0.14)", border: "rgba(240,195,109,0.34)" },
};

function ResultsBar({ counts }) {
  const total = Math.max(1, Number(counts?.total_targets || 0));
  const sections = [
    { key: "success", color: "#00d18c" },
    { key: "warning", color: "#fbbf24" },
    { key: "running", color: "#58a6ff" },
    { key: "failed", color: "#ff4f4f" },
    { key: "timed_out", color: "#b36ae2" },
    { key: "expired", color: "#777777" },
    { key: "skipped", color: "#f0c36d" },
    { key: "pending", color: "#999999" }
  ];
  const labelFor = (key) =>
    key === "pending"
      ? "Scheduled"
      : key
          .replace(/_/g, " ")
          .replace(/^./, (c) => c.toUpperCase());

  const hasNonPending = sections
    .filter((section) => section.key !== "pending")
    .some((section) => Number(counts?.[section.key] || 0) > 0);

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 0.25, lineHeight: 1.7, fontFamily: gridFontFamily }}>
      <Box sx={{ display: "flex", borderRadius: 1, overflow: "hidden", width: 220, height: 6 }}>
        {sections.map((section) => {
          const value = Number(counts?.[section.key] || 0);
          if (!value) return null;
          const width = `${Math.round((value / total) * 100)}%`;
          return (
            <Box
              key={section.key}
              component="span"
              sx={{ display: "block", height: "100%", width, backgroundColor: section.color }}
            />
          );
        })}
      </Box>
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          columnGap: 0.75,
          rowGap: 0.25,
          color: "#aaa",
          fontSize: 11,
          fontFamily: gridFontFamily
        }}
      >
        {(() => {
          if (!hasNonPending && Number(counts?.pending || 0) > 0) {
            return <Box component="span">Scheduled</Box>;
          }
          return sections
            .filter((section) => Number(counts?.[section.key] || 0) > 0)
            .map((section) => (
              <Box key={section.key} component="span" sx={{ display: "inline-flex", alignItems: "center", gap: 0.5 }}>
                <Box component="span" sx={{ width: 6, height: 6, borderRadius: 1, backgroundColor: section.color }} />
                {counts?.[section.key]} {labelFor(section.key)}
              </Box>
            ));
        })()}
      </Box>
    </Box>
  );
}

export default function ScheduledJobsList({ refreshToken }) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [jobFilterMode, setJobFilterMode] = useState("all");
  const [assembliesPayload, setAssembliesPayload] = useState({ items: [], queue: [] });
  const [assembliesLoading, setAssembliesLoading] = useState(false);
  const [assembliesError, setAssembliesError] = useState("");
  const [rerunningId, setRerunningId] = useState(null);
  const gridApiRef = useRef(null);
  const sendNotification = useAppNotifications({
    title: PAGE_TITLE,
    icon: "schedule",
    variant: "info",
  });
  const selectedSiteId = useMemo(
    () => String(searchParams.get("site") || "").trim(),
    [searchParams]
  );

  const autoSizeTrackedColumns = useCallback(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const run = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* ignore auto-size errors triggered during async refresh */
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, []);

  const deriveRowKey = useCallback((row, index = "") => {
    if (row && row.id != null && row.id !== "") {
      return String(row.id);
    }
    if (row && row.name) {
      return String(row.name);
    }
    if (index !== undefined && index !== null && index !== "") {
      return `__row_${index}`;
    }
    return "";
  }, []);

  const assembliesCellRenderer = useCallback((params) => {
    const list = params?.data?.componentsMeta || [];
    if (!list.length) {
      return <Typography variant="body2" sx={{ color: "#888" }}>No assemblies</Typography>;
    }
    return (
      <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1 }}>
        {list.map((item) => (
          <Box key={item.key} sx={{ display: "flex", alignItems: "center", gap: 0.75 }}>
            <DomainBadge domain={item.domain} size="small" />
            <Typography variant="body2" sx={{ color: "#f5f7fa" }}>{item.label}</Typography>
          </Box>
        ))}
      </Box>
    );
  }, []);

  const loadAssemblies = useCallback(async () => {
    setAssembliesLoading(true);
    setAssembliesError("");
    try {
      const resp = await fetch("/api/assemblies");
      if (!resp.ok) {
        const detail = await resp.text();
        throw new Error(detail || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      const normalized = parseAssembliesCollectionPayload(data);
      setAssembliesPayload({
        items: normalized.items,
        queue: normalized.queue
      });
    } catch (err) {
      console.error("Failed to load assemblies:", err);
      setAssembliesPayload({ items: [], queue: [] });
      setAssembliesError(err?.message || "Failed to load assemblies");
    } finally {
      setAssembliesLoading(false);
    }
  }, []);

  const assemblyIndex = useMemo(
    () => buildAssemblyIndex(assembliesPayload.items, assembliesPayload.queue),
    [assembliesPayload.items, assembliesPayload.queue]
  );

  const loadJobs = useCallback(
    async ({ showLoading = false } = {}) => {
      if (showLoading) {
        setLoading(true);
        setError("");
      }
      try {
        const siteQuery = selectedSiteId ? `?site=${encodeURIComponent(selectedSiteId)}` : "";
        const resp = await fetch(`/api/scheduled_jobs${siteQuery}`);
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.error || `HTTP ${resp.status}`);
        }
        const pretty = (st) => {
          const s = String(st || "").toLowerCase();
          const map = {
            immediately: "Immediately",
            once: "Once",
            every_5_minutes: "Every 5 Minutes",
            every_10_minutes: "Every 10 Minutes",
            every_15_minutes: "Every 15 Minutes",
            every_30_minutes: "Every 30 Minutes",
            every_hour: "Every Hour",
            daily: "Daily",
            weekly: "Weekly",
            monthly: "Monthly",
            yearly: "Yearly"
          };
          if (map[s]) return map[s];
          try {
            return s.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());
          } catch {
            return String(st || "");
          }
        };
        const fmt = (ts) => {
          if (!ts) return "";
          try {
            const d = new Date(Number(ts) * 1000);
            if (Number.isNaN(d?.getTime())) return "";
            return d.toLocaleString(undefined, {
              year: "numeric",
              month: "2-digit",
              day: "2-digit",
              hour: "numeric",
              minute: "2-digit"
            });
          } catch {
            return "";
          }
        };
        const mappedRows = (data?.jobs || []).map((j) => {
          const jobKind = String(j.job_kind || "automation").toLowerCase();
          const isOnboardingJob = jobKind === "onboarding";
          const components = Array.isArray(j.components) ? j.components : [];
          const normalizedComponents = components.map((component) => {
            const record = resolveAssemblyForComponent(assemblyIndex, component);
            const displayName =
              record?.displayName ||
              component.name ||
              component.component_name ||
              component.script_name ||
              component.script_path ||
              component.path ||
              "Assembly";
            const domainValue = (record?.domain || component.domain || "user").toLowerCase();
            const domainMeta = resolveDomainMeta(domainValue);
            const assemblyGuid =
              component.assembly_guid ||
              component.assemblyGuid ||
              record?.assemblyGuid ||
              null;
            return {
              ...component,
              assembly_guid: assemblyGuid,
              name: displayName,
              domain: domainValue,
              domainLabel: domainMeta.label,
              path: record?.path || component.path || component.script_path || component.playbook_path || ""
            };
          });
          const componentSummaries = normalizedComponents.map((comp, idx) => ({
            key: `${comp.assembly_guid || comp.path || idx}-${idx}`,
            label: comp.name,
            domain: comp.domain
          }));
          const displayComponentSummaries = isOnboardingJob
            ? [{ key: `onboarding-${j.id || j.name}`, label: "Device Onboarding", domain: "system" }]
            : componentSummaries;
          const hasWorkflowComponent = normalizedComponents.some((comp) => {
            const typeRaw = String(comp?.type || comp?.assembly_type || "").trim().toLowerCase();
            const subtypeRaw = String(comp?.assembly_subtype || "").trim().toLowerCase();
            return typeRaw === "workflow" || subtypeRaw === "workflow";
          });
          const compName =
            displayComponentSummaries.length === 1
              ? displayComponentSummaries[0].label
              : displayComponentSummaries.length > 1
                ? `${displayComponentSummaries.length} Assemblies`
                : "No Assemblies";
          const onboardingScopeCount = Array.isArray(j.targets)
            ? j.targets
                .filter((target) => target?.kind === "onboarding_scope")
                .reduce((total, target) => total + (Array.isArray(target.entries) ? target.entries.length : 0), 0)
            : 0;
          const targetText = Array.isArray(j.targets)
            ? isOnboardingJob
              ? `${Math.max(Number(j?.result_counts?.total_targets || 0), onboardingScopeCount)} discovery target${Math.max(Number(j?.result_counts?.total_targets || 0), onboardingScopeCount) !== 1 ? "s" : ""}`
              : hasWorkflowComponent
              ? "Workflow-defined"
              : `${j.targets.length} device${j.targets.length !== 1 ? "s" : ""}`
            : "";
          const occurrence = pretty(j.schedule_type || "immediately");
          const fallbackTargetCount = isOnboardingJob
            ? onboardingScopeCount
            : hasWorkflowComponent
            ? Math.max(1, Array.isArray(j.targets) ? j.targets.length : 0)
            : Array.isArray(j.targets) ? j.targets.length : 0;
          const resultsCounts = {
            total_targets: fallbackTargetCount,
            pending: fallbackTargetCount,
            ...(j.result_counts || {})
          };
          if (resultsCounts.total_targets == null || Number.isNaN(Number(resultsCounts.total_targets))) {
            resultsCounts.total_targets = fallbackTargetCount;
          }
          const normalizeCount = (value) => {
            const num = Number(value);
            return Number.isFinite(num) ? num : 0;
          };
          const totalTargets = normalizeCount(resultsCounts.total_targets);
          const pendingCount = normalizeCount(resultsCounts.pending);
          const runningCount = normalizeCount(resultsCounts.running);
          const successCount = normalizeCount(resultsCounts.success);
          const warningCount = normalizeCount(resultsCounts.warning);
          const failedCount = normalizeCount(resultsCounts.failed);
          const expiredCount = normalizeCount(resultsCounts.expired);
          const timedOutCount = normalizeCount(resultsCounts.timed_out || resultsCounts.timedOut);
          const skippedCount = normalizeCount(resultsCounts.skipped);
          const totalFinished = successCount + warningCount + failedCount + expiredCount + timedOutCount + skippedCount;
          const allTargetsEvaluated =
            totalTargets > 0
              ? totalFinished >= totalTargets && pendingCount === 0 && runningCount === 0
              : pendingCount === 0 && runningCount === 0;
          const jobExpiredFlag =
            expiredCount > 0 || String(j.last_status || "").toLowerCase() === "expired";
          const scheduleRaw = String(j.schedule_type || "").toLowerCase();
          const isImmediateType = scheduleRaw === "immediately";
          const isScheduledType = scheduleRaw === "once";
          const showImmediate = isImmediateType && !allTargetsEvaluated;
          const showScheduled = isScheduledType && !allTargetsEvaluated;
          const canComplete = isImmediateType || isScheduledType;
          const showCompleted = canComplete && (jobExpiredFlag || allTargetsEvaluated);
          const categoryFlags = {
            immediate: showImmediate,
            scheduled: showScheduled,
            recurring: !isImmediateType && !isScheduledType,
            completed: showCompleted
          };
          return {
            id: j.id,
            name: j.name,
            scriptWorkflow: compName,
            componentsMeta: displayComponentSummaries,
            jobType: isOnboardingJob ? "Device Onboarding" : "Automation",
            target: targetText,
            occurrence,
            lastRun: fmt(j.last_run_ts),
            nextRun: fmt(j.next_run_ts || j.start_ts),
            result: j.last_status || (j.next_run_ts ? "Scheduled" : ""),
            resultsCounts,
            enabled: Boolean(j.enabled),
            warningCode: j.warning_code || "",
            warningMessage: j.warning_message || "",
            categoryFlags,
            jobKind,
            raw: { ...j, components: normalizedComponents }
          };
        });
        setRows(mappedRows);
        setError("");
        setSelectedIds((prev) => {
          if (!prev.size) return prev;
          const valid = new Set(
            mappedRows.map((row, index) => deriveRowKey(row, index))
          );
          let changed = false;
          const next = new Set();
          prev.forEach((value) => {
            if (valid.has(value)) {
              next.add(value);
            } else {
              changed = true;
            }
          });
          return changed ? next : prev;
        });
      } catch (err) {
        setRows([]);
        setSelectedIds(() => new Set());
        setError(String(err?.message || err || "Failed to load scheduled jobs"));
      } finally {
        if (showLoading) {
          setLoading(false);
        }
      }
    },
    [assemblyIndex, deriveRowKey, selectedSiteId]
  );

  useEffect(() => {
    loadAssemblies();
  }, [loadAssemblies, refreshToken]);

  useEffect(() => {
    let timer;
    let isMounted = true;
    (async () => {
      if (!isMounted) return;
      await loadJobs({ showLoading: true });
    })();
    timer = setInterval(() => {
      loadJobs();
    }, 5000);
    return () => {
      isMounted = false;
      if (timer) clearInterval(timer);
    };
  }, [loadJobs, refreshToken]);

  const handleGridReady = useCallback(
    (params) => {
      gridApiRef.current = params.api;
      autoSizeTrackedColumns();
    },
    [autoSizeTrackedColumns]
  );

  const filterCounts = useMemo(() => {
    const totals = { all: rows.length, immediate: 0, scheduled: 0, recurring: 0, completed: 0 };
    rows.forEach((row) => {
      if (row?.categoryFlags?.immediate) totals.immediate += 1;
      if (row?.categoryFlags?.scheduled) totals.scheduled += 1;
      if (row?.categoryFlags?.recurring) totals.recurring += 1;
      if (row?.categoryFlags?.completed) totals.completed += 1;
    });
    return totals;
  }, [rows]);

  const filteredRows = useMemo(() => {
    if (jobFilterMode === "all") return rows;
    return rows.filter((row) => row?.categoryFlags?.[jobFilterMode]);
  }, [jobFilterMode, rows]);
  const activeFilterLabel = useMemo(() => {
    const match = FILTER_OPTIONS.find((option) => option.key === jobFilterMode);
    return match ? match.label : jobFilterMode;
  }, [jobFilterMode]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api || loading || api.isDestroyed?.()) return;
    if (!filteredRows.length) {
      api.showNoRowsOverlay();
    } else {
      api.hideOverlay();
    }
  }, [loading, filteredRows.length]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    api.forEachNode((node) => {
      const nodeKey = deriveRowKey(node?.data, node?.rowIndex);
      const shouldSelect = nodeKey && selectedIds.has(nodeKey);
      if (node.isSelected() !== shouldSelect) {
        node.setSelected(shouldSelect);
      }
    });
  }, [deriveRowKey, filteredRows, selectedIds]);

  const anySelected = selectedIds.size > 0;
  const selectedJobRows = useMemo(() => {
    if (!selectedIds.size) return [];
    return rows.filter((row, index) => selectedIds.has(deriveRowKey(row, index)));
  }, [deriveRowKey, rows, selectedIds]);
  const singleSelectedJob = selectedJobRows.length === 1 ? selectedJobRows[0] : null;
  const credentialResetWarningCount = useMemo(
    () => rows.filter((row) => String(row.warningCode || "").toLowerCase() === "credential_reset_required").length,
    [rows]
  );
  useEffect(() => {
    if (!filteredRows.length || loading) return;
    autoSizeTrackedColumns();
  }, [filteredRows, loading, autoSizeTrackedColumns]);

  const handleSelectionChanged = useCallback(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const selectedNodes = api.getSelectedNodes();
    const next = new Set();
    selectedNodes.forEach((node) => {
      const nodeKey = deriveRowKey(node?.data, node?.rowIndex);
      if (nodeKey) {
        next.add(nodeKey);
      }
    });
    setSelectedIds(next);
  }, [deriveRowKey]);

  const getRowId = useCallback(
    (params) => deriveRowKey(params?.data, params?.rowIndex),
    [deriveRowKey]
  );

  const formatComponentsMetaValue = useCallback((value) => {
    const list = Array.isArray(value) ? value : [];
    const labels = list
      .map((item) => String(item?.label || "").trim())
      .filter(Boolean);
    return labels.length ? labels.join(", ") : "No assemblies";
  }, []);

  const formatResultsCountsValue = useCallback((value) => {
    const counts = value && typeof value === "object" ? value : {};
    const labels = [
      ["success", "Success"],
      ["warning", "Warning"],
      ["running", "Running"],
      ["failed", "Failed"],
      ["timed_out", "Timed Out"],
      ["expired", "Expired"],
      ["skipped", "Skipped"],
      ["pending", "Scheduled"],
    ];
    const parts = labels
      .map(([key, label]) => {
        const count = Number(counts?.[key] || 0);
        return count > 0 ? `${label}: ${count}` : "";
      })
      .filter(Boolean);
    if (parts.length) {
      return parts.join(" | ");
    }
    const totalTargets = Number(counts?.total_targets || 0);
    if (totalTargets > 0) {
      return `${totalTargets} target${totalTargets === 1 ? "" : "s"}`;
    }
    return "No targets";
  }, []);

  const nameCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const resolvedJobId = Number(row?.raw?.id ?? row?.id);
      const isOnboarding = String(row?.raw?.job_kind || row?.jobKind || "").toLowerCase() === "onboarding";
      const editorHref =
        Number.isInteger(resolvedJobId) && resolvedJobId > 0
          ? isOnboarding
            ? APP_PATHS.jobOnboarding(resolvedJobId)
            : `${APP_PATHS.job(resolvedJobId)}?tab=job_name`
          : isOnboarding
            ? APP_PATHS.jobOnboardingNew
            : `${APP_PATHS.jobNew}?tab=job_name`;
      let pointerNavigationHandled = false;
      const dispatchEdit = (event) => {
        event.preventDefault();
        event.stopPropagation();
        navigate(isOnboarding ? APP_PATHS.jobOnboarding(resolvedJobId) : APP_PATHS.job(resolvedJobId), {
          state: row.raw ? { initialJob: row.raw } : undefined,
        });
      };
      const handlePointerDown = (event) => {
        const isPrimaryPointer = event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey;
        if (!isPrimaryPointer) return;
        pointerNavigationHandled = true;
        dispatchEdit(event);
      };
      const handleClick = (event) => {
        if (pointerNavigationHandled) {
          pointerNavigationHandled = false;
          event.preventDefault();
          event.stopPropagation();
          return;
        }
        dispatchEdit(event);
      };
      return (
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.8 }}>
          <a
            href={editorHref}
            onPointerDown={handlePointerDown}
            onClick={handleClick}
            title={row.name || ""}
            style={{
              color: "#58a6ff",
              textDecoration: "none",
              fontWeight: 500,
              cursor: "pointer",
              fontFamily: gridFontFamily
            }}
          >
            {row.name || "-"}
          </a>
          {row.warningMessage ? (
            <Tooltip title={row.warningMessage} arrow>
              <WarningAmberRoundedIcon fontSize="small" sx={{ color: "#f0c36d" }} />
            </Tooltip>
          ) : null}
        </Box>
      );
    },
    [navigate]
  );

  const resultsCellRenderer = useCallback((params) => {
    return <ResultsBar counts={params?.data?.resultsCounts} />;
  }, []);

  const statusCellRenderer = useCallback((params) => {
    const rawValue = String(params?.value || "").trim();
    const normalized = rawValue.toLowerCase();
    const style = STATUS_STYLES[normalized] || {
      label: rawValue || "-",
      color: "#cbd5e1",
      background: "rgba(148,163,184,0.12)",
      border: "rgba(148,163,184,0.24)",
    };
    return (
      <Tooltip title={style.label} arrow>
        <Box
          component="span"
          sx={{
            display: "inline-flex",
            alignItems: "center",
            maxWidth: "100%",
            minHeight: 24,
            px: 1,
            borderRadius: 1,
            border: `1px solid ${style.border}`,
            backgroundColor: style.background,
            color: style.color,
            fontSize: 12,
            fontWeight: 700,
            lineHeight: 1.2,
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
            fontFamily: gridFontFamily,
          }}
        >
          {style.label}
        </Box>
      </Tooltip>
    );
  }, []);

  const enabledCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleToggle = async (event) => {
        event.stopPropagation();
        const nextEnabled = event.target.checked;
        try {
          await fetch(`/api/scheduled_jobs/${row.id}/toggle`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ enabled: nextEnabled })
          });
        } catch {
          // ignore network errors for toggle
        }
        setRows((prev) =>
          prev.map((job) => {
            if ((job.id ?? job.name) === (row.id ?? row.name)) {
              const updatedRaw = { ...(job.raw || {}), enabled: nextEnabled };
              return { ...job, enabled: nextEnabled, raw: updatedRaw };
            }
            return job;
          })
        );
      };
      return (
        <Switch
          size="small"
          checked={Boolean(row.enabled)}
          onChange={handleToggle}
          onClick={(event) => event.stopPropagation()}
          sx={{
            "& .MuiSwitch-switchBase.Mui-checked": { color: "#58a6ff" },
            "& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track": { bgcolor: "#58a6ff" }
          }}
        />
      );
    },
    []
  );

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableSelectionWithoutKeys: true,
      enableClickSelection: false,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      width: 52,
      minWidth: 52,
      maxWidth: 52,
      resizable: false,
      sortable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      filter: false,
      pinned: "left",
      lockPosition: true,
      suppressMovable: true,
    }),
    []
  );

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Name",
        field: "name",
        cellRenderer: nameCellRenderer,
        sort: "asc",
        minWidth: 150,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Assembly(s)",
        colId: "componentsMeta",
        valueGetter: (params) => formatComponentsMetaValue(params?.data?.componentsMeta),
        minWidth: 180,
        cellRenderer: assembliesCellRenderer,
        cellClass: "auto-col-tight",
      },
      { headerName: "Type", field: "jobType", minWidth: 150, cellClass: "auto-col-tight" },
      { headerName: "Target", field: "target", minWidth: 140, cellClass: "auto-col-tight" },
      { headerName: "Recurrence", field: "occurrence", minWidth: 150, cellClass: "auto-col-tight" },
      { headerName: "Last Run", field: "lastRun", minWidth: 150, cellClass: "auto-col-tight" },
      {
        headerName: "Status",
        field: "result",
        minWidth: 150,
        cellRenderer: statusCellRenderer,
        cellClass: "auto-col-tight",
      },
      { headerName: "Next Run", field: "nextRun", minWidth: 150, cellClass: "auto-col-tight" },
      {
        headerName: "Results",
        colId: "resultsCounts",
        valueGetter: (params) => formatResultsCountsValue(params?.data?.resultsCounts),
        minWidth: 280,
        cellRenderer: resultsCellRenderer,
        cellClass: "auto-col-tight",
        sortable: false,
        filter: false
      },
      {
        headerName: "Enabled",
        field: "enabled",
        minWidth: 140,
        flex: 1,
        cellRenderer: enabledCellRenderer,
        cellClass: "auto-col-tight",
        sortable: false,
        filter: false,
        resizable: false,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true
      }
    ],
    [
      assembliesCellRenderer,
      enabledCellRenderer,
      formatComponentsMetaValue,
      formatResultsCountsValue,
      nameCellRenderer,
      resultsCellRenderer,
      statusCellRenderer,
    ]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      minWidth: 140,
      cellStyle: {
        display: "flex",
        alignItems: "center",
        justifyContent: "flex-start",
        color: "#f5f7fa",
        fontFamily: gridFontFamily,
        fontSize: "13px",
        textAlign: "left",
        paddingLeft: "8px",
        paddingRight: "6px",
      },
    }),
    []
  );

  const handleRefreshClick = useCallback(async () => {
    await loadJobs({ showLoading: true });
  }, [loadJobs]);

  const handleRerunSelectedJob = useCallback(async () => {
    const job = singleSelectedJob;
    if (!job?.id || rerunningId) return;
    setRerunningId(job.id);
    try {
      const resp = await fetch(`/api/scheduled_jobs/${encodeURIComponent(job.id)}/rerun`, {
        method: "POST",
        credentials: "include",
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(body?.message || body?.error || `HTTP ${resp.status}`);
      }
      sendNotification(`Job ${job.name || job.id} Re-Run Queued`);
      await loadJobs({ showLoading: true });
    } catch (err) {
      setError(String(err?.message || err || "Unable to re-run selected job"));
    } finally {
      setRerunningId(null);
    }
  }, [loadJobs, rerunningId, sendNotification, singleSelectedJob]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "scheduled-jobs-refresh",
        label: "Refresh",
        icon: <CachedIcon />,
        tone: "secondary",
        loading,
        onClick: handleRefreshClick,
      },
      {
        id: "scheduled-jobs-delete",
        label: "Delete Job",
        icon: <DeleteIcon />,
        tone: "secondary",
        disabled: !anySelected,
        onClick: () => setBulkDeleteOpen(true),
      },
      {
        id: "scheduled-jobs-rerun",
        label: "Re-Run Job",
        icon: <PlayArrowIcon />,
        tone: "primary",
        disabled: !singleSelectedJob || selectedJobRows.length !== 1 || !singleSelectedJob.enabled || Boolean(rerunningId),
        loading: Boolean(rerunningId),
        onClick: handleRerunSelectedJob,
      },
      {
        id: "scheduled-jobs-create",
        label: "Create Job",
        icon: <AddIcon />,
        tone: "primary",
        onClick: () => navigate(APP_PATHS.jobNew),
      },
    ],
    [anySelected, handleRefreshClick, handleRerunSelectedJob, loading, navigate, rerunningId, selectedJobRows.length, singleSelectedJob]
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
        background: "transparent",
        border: "none",
        boxShadow: "none",
        borderRadius: 0,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        minHeight: 420,
        height: "100%",
        color: AURORA_SHELL.text,
        fontFamily: gridFontFamily,
      }}
      elevation={0}
    >
      <PageBodyFrame
        variant="grid_with_stack"
        stack={(
          <>
            <Box sx={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 1.5 }}>
              <Box
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 0.75,
                  background: "linear-gradient(120deg, rgba(8,12,24,0.92), rgba(4,7,17,0.85))",
                  borderRadius: 999,
                  border: "1px solid rgba(148,163,184,0.35)",
                  boxShadow: "0 18px 48px rgba(2,8,23,0.45)",
                  padding: "4px"
                }}
              >
                {FILTER_OPTIONS.map((option) => {
                  const active = jobFilterMode === option.key;
                  return (
                    <Box
                      key={option.key}
                      component="button"
                      type="button"
                      onClick={() => setJobFilterMode(option.key)}
                      sx={{
                        border: "none",
                        outline: "none",
                        background: active ? "linear-gradient(135deg,#7dd3fc,#c084fc)" : "transparent",
                        color: active ? "#041224" : "#cbd5e1",
                        fontWeight: 600,
                        fontSize: 13,
                        px: 2,
                        py: 0.5,
                        borderRadius: 999,
                        cursor: "pointer",
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 0.6,
                        boxShadow: active ? "0 0 18px rgba(125,211,252,0.35)" : "none",
                        transition: "all 0.2s ease",
                      }}
                    >
                      <Box component="span" sx={{ userSelect: "none" }}>{option.label}</Box>
                      <Box
                        component="span"
                        sx={{
                          minWidth: 28,
                          textAlign: "center",
                          borderRadius: 999,
                          fontSize: 12,
                          fontWeight: 600,
                          px: 0.75,
                          py: 0.1,
                          color: active ? "#041224" : "#94a3b8",
                          backgroundColor: active ? "rgba(4,18,36,0.2)" : "rgba(15,23,42,0.65)",
                          border: active ? "1px solid rgba(4,18,36,0.3)" : "1px solid rgba(148,163,184,0.3)"
                        }}
                      >
                        {filterCounts[option.key] ?? 0}
                      </Box>
                    </Box>
                  );
                })}
              </Box>
              <Typography variant="body2" sx={{ color: AURORA_SHELL.subtext }}>
                {jobFilterMode === "all"
                  ? `Showing ${filterCounts.all || 0} Jobs`
                  : `Showing ${filterCounts[jobFilterMode] || 0} ${activeFilterLabel} job${(filterCounts[jobFilterMode] || 0) === 1 ? "" : "s"}`}
              </Typography>
            </Box>
            {credentialResetWarningCount ? (
              <Box
                sx={{
                  mt: 0.2,
                  px: 1.2,
                  py: 0.9,
                  borderRadius: 2,
                  border: "1px solid rgba(240,195,109,0.3)",
                  background: "rgba(240,195,109,0.08)",
                }}
              >
                <Typography variant="body2" sx={{ color: "#f0c36d" }}>
                  {credentialResetWarningCount} scheduled job{credentialResetWarningCount === 1 ? "" : "s"} are disabled because their associated credential lost protected secret material during an Aegis Cipher force reset.
                </Typography>
              </Box>
            ) : null}
          </>
        )}
      >
        <Box
          className={themeClassName}
          sx={{
            background: "transparent",
            border: "none",
            p: 0,
            width: "100%",
            flexGrow: 1,
            minHeight: 0,
            height: "100%",
            position: "relative",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              minHeight: "100%",
              border: "none",
              borderRadius: 0,
              background: "transparent",
            },

            "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
              paddingTop: "8px",
              paddingBottom: "8px",
              paddingLeft: "18px",
              paddingRight: "12px",
              gap: 0,
            },
            "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              gap: 0,
              paddingTop: 0,
              paddingBottom: 0,
            },
            "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
            },

            "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
              paddingLeft: "12px",
              paddingRight: "9px",
              justifyContent: "flex-start !important",
              textAlign: "left !important",
            },
            "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper": {
              justifyContent: "flex-start !important",
            },
            "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-value": {
              textAlign: "left !important",
              justifyContent: "flex-start !important",
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
            rowData={filteredRows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            animateRows
            rowHeight={46}
            headerHeight={44}
            suppressCellFocus
            rowSelection={rowSelection}
            selectionColumnDef={selectionColumnDef}
            getRowId={getRowId}
            loading={loading}
            overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No scheduled jobs found.</span>"
            onGridReady={handleGridReady}
            onSelectionChanged={handleSelectionChanged}
            theme={gridTheme}
          />
        </Box>
      </PageBodyFrame>

      <Dialog
        open={bulkDeleteOpen}
        onClose={() => setBulkDeleteOpen(false)}
        maxWidth="xs"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Delete Job(s)"
            subtitle="Permanently remove the selected scheduled jobs from Borealis."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Delete the currently selected scheduled job records.
          </Typography>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setBulkDeleteOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button
            onClick={async () => {
              try {
                const ids = Array.from(selectedIds);
                const idSet = new Set(ids);
                const selectedJobs = rows.filter((job, index) => {
                  const key = getRowId({ data: job, rowIndex: index });
                  return idSet.has(key);
                });
                await Promise.allSettled(
                  ids.map((id) => fetch(`/api/scheduled_jobs/${id}`, { method: "DELETE" }))
                );
                setRows((prev) =>
                  prev.filter((job, index) => {
                    const key = getRowId({ data: job, rowIndex: index });
                    return !idSet.has(key);
                  })
                );
                setSelectedIds(() => new Set());
                selectedJobs
                  .map((job) => job?.name)
                  .filter(Boolean)
                  .forEach((name) => sendNotification(`Job ${name} Deleted Successfully`));
              } catch {
                // ignore delete errors here; a fresh load will surface them
              }
              setBulkDeleteOpen(false);
              await loadJobs({ showLoading: true });
            }}
            sx={DIALOG_DANGER_BUTTON_SX}
          >
            Delete Job(s)
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
