export const FILTER_OPTIONS = [
  { key: "normal", label: "Normal" },
  { key: "immediate", label: "Immediate" },
  { key: "scheduled", label: "Scheduled" },
  { key: "recurring", label: "Recurring" },
  { key: "completed", label: "Completed" },
  { key: "maintenance", label: "Maintenance" },
  { key: "patch_management", label: "Patch Management" },
];

const MAINTENANCE_JOB_KIND_ALIASES = new Set([
  "agent_maintenance",
  "agent_update",
  "agent_channel_switch",
  "maintenance",
  "engine_maintenance",
]);

const PATCH_JOB_KIND_ALIASES = new Set([
  "patch_install",
  "patch_management",
  "patch_deployment",
  "ad_hoc_patch_install",
  "policy_patch_install",
]);

function normalizeJobKind(value) {
  return String(value || "automation").trim().toLowerCase();
}

function jobKindHasToken(jobKind, token) {
  return normalizeJobKind(jobKind).split(/[^a-z0-9]+/).includes(token);
}

export function isMaintenanceJobKind(value) {
  const jobKind = normalizeJobKind(value);
  return MAINTENANCE_JOB_KIND_ALIASES.has(jobKind) || jobKindHasToken(jobKind, "maintenance");
}

export function isPatchManagementJobKind(value) {
  const jobKind = normalizeJobKind(value);
  return PATCH_JOB_KIND_ALIASES.has(jobKind) || jobKindHasToken(jobKind, "patch");
}

export function buildScheduledJobCategoryFlags({
  jobKind,
  scheduleRaw,
  allTargetsEvaluated,
  jobExpiredFlag,
} = {}) {
  const isMaintenance = isMaintenanceJobKind(jobKind);
  const isPatchManagement = isPatchManagementJobKind(jobKind);
  const isNormal = !isMaintenance && !isPatchManagement;
  const normalizedSchedule = String(scheduleRaw || "").toLowerCase();
  const isImmediateType = normalizedSchedule === "immediately";
  const isScheduledType = normalizedSchedule === "once";
  const showCompleted =
    isNormal &&
    (isImmediateType || isScheduledType) &&
    (Boolean(jobExpiredFlag) || Boolean(allTargetsEvaluated));

  return {
    normal: isNormal,
    immediate: isNormal && isImmediateType && !showCompleted,
    scheduled: isNormal && isScheduledType && !showCompleted,
    recurring: isNormal && !isImmediateType && !isScheduledType,
    completed: showCompleted,
    maintenance: isMaintenance,
    patch_management: isPatchManagement,
  };
}

export function buildScheduledJobFilterCounts(rowItems = []) {
  const totals = FILTER_OPTIONS.reduce((acc, option) => {
    acc[option.key] = 0;
    return acc;
  }, {});
  (Array.isArray(rowItems) ? rowItems : []).forEach((row) => {
    FILTER_OPTIONS.forEach((option) => {
      if (row?.categoryFlags?.[option.key]) {
        totals[option.key] += 1;
      }
    });
  });
  return totals;
}

export function filterScheduledJobRows(rowItems = [], mode = "normal") {
  const selectedMode = FILTER_OPTIONS.some((option) => option.key === mode) ? mode : "normal";
  return (Array.isArray(rowItems) ? rowItems : []).filter((row) => row?.categoryFlags?.[selectedMode]);
}
