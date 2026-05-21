import React, { useMemo } from "react";
import { Box, Tooltip, Typography } from "@mui/material";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import RadioButtonUncheckedRoundedIcon from "@mui/icons-material/RadioButtonUncheckedRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import RemoveCircleOutlineRoundedIcon from "@mui/icons-material/RemoveCircleOutlineRounded";
import { MAGIC_UI } from "./Shared.jsx";

const TIMELINE_WIDTH = 620;
const ROLE_BRANCH_WIDTH = 430;

const MAIN_TIMELINE_DEFINITIONS = Object.freeze([
  { id: "process_start", label: "Agent process started", milestoneKeys: ["process_start"] },
  { id: "server_config_loaded", label: "Server configuration loaded", milestoneKeys: ["server_config_loaded"] },
  { id: "identity_loaded", label: "Device identity loaded", milestoneKeys: ["identity_loaded"] },
  { id: "engine_authentication", label: "Engine authentication", milestoneKeys: ["authenticating", "authenticated"] },
  { id: "agent_role_loading", label: "Agent role loading", milestoneKeys: ["roles_loading", "roles_ready"], branch: "roles" },
  { id: "status_channel_online", label: "Status channel online", milestoneKeys: ["status_channel_online"] },
  { id: "engine_socket", label: "Engine socket", milestoneKeys: ["socket_connecting", "socket_connected"] },
  { id: "steady_state_online", label: "Agent steady state online", milestoneKeys: ["steady_state_online"] },
]);

const STARTUP_STATE_META = Object.freeze({
  complete: { label: "Complete", color: MAGIC_UI.accentC, bg: "rgba(52, 211, 153, 0.12)", Icon: CheckCircleRoundedIcon },
  active: { label: "Active", color: MAGIC_UI.accentA, bg: "rgba(125, 211, 252, 0.13)", Icon: AutorenewRoundedIcon },
  failed: { label: "Failed", color: "#ff7b89", bg: "rgba(255, 123, 137, 0.13)", Icon: ErrorOutlineRoundedIcon },
  pending: { label: "Pending", color: "rgba(148, 163, 184, 0.72)", bg: "rgba(148, 163, 184, 0.08)", Icon: RadioButtonUncheckedRoundedIcon },
  skipped: { label: "Skipped", color: "rgba(176, 184, 200, 0.72)", bg: "rgba(176, 184, 200, 0.08)", Icon: RemoveCircleOutlineRoundedIcon },
});

const RUNTIME_STATUS_COLOR_BY_CODE = Object.freeze({
  healthy: MAGIC_UI.accentC,
  loaded: MAGIC_UI.accentC,
  recovering: "#ffb347",
  stale: "#ffb347",
  pending: MAGIC_UI.accentA,
  unhealthy: "#ff7b89",
  failed: "#ff7b89",
  unsupported: "rgba(176, 184, 200, 0.74)",
  not_applicable: "rgba(176, 184, 200, 0.74)",
  unknown: "rgba(176, 184, 200, 0.74)",
});

export function normalizeMilestoneState(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (["complete", "active", "failed", "pending", "skipped"].includes(normalized)) return normalized;
  if (normalized === "healthy") return "complete";
  if (normalized === "recovering") return "active";
  if (normalized === "unhealthy" || normalized === "failed") return "failed";
  return "pending";
}

function buildMilestoneLookup(milestones) {
  return (Array.isArray(milestones) ? milestones : []).reduce((acc, milestone) => {
    const key = String(milestone?.key || "").trim();
    if (key) acc[key] = milestone;
    return acc;
  }, {});
}

function normalizeRuntimeHealthState(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  if (normalized === "healthy" || normalized === "loaded") return "complete";
  if (normalized === "recovering" || normalized === "pending" || normalized === "stale") return "active";
  if (normalized === "unhealthy") return "failed";
  if (normalized === "unsupported" || normalized === "not_applicable" || normalized === "no_desktop_environment_active") return "skipped";
  return "pending";
}

function getRuntimeStatusColor(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  return RUNTIME_STATUS_COLOR_BY_CODE[normalized] || RUNTIME_STATUS_COLOR_BY_CODE.unknown;
}

function colorWithAlpha(color, alpha) {
  const text = String(color || "").trim();
  const boundedAlpha = Math.min(Math.max(Number(alpha), 0), 1);
  if (/^#[0-9a-f]{6}$/i.test(text)) {
    const suffix = Math.round(boundedAlpha * 255).toString(16).padStart(2, "0");
    return `${text}${suffix}`;
  }
  const rgbaMatch = text.match(/^rgba?\(([^)]+)\)$/i);
  if (rgbaMatch) {
    const channels = rgbaMatch[1].split(",").slice(0, 3).map((part) => part.trim()).join(", ");
    return `rgba(${channels}, ${boundedAlpha})`;
  }
  return text;
}

function resolveGroupedMilestones(definition, milestoneByKey) {
  return (definition.milestoneKeys || [definition.id]).map((key) => {
    const milestone = milestoneByKey[key] || {};
    return {
      key,
      label: milestone?.label || key,
      detail: String(milestone?.detail || "").trim(),
      state: normalizeMilestoneState(milestone?.state),
      timestamp: milestone?.completed_at || milestone?.updated_at || milestone?.started_at || null,
    };
  });
}

function resolveGroupedState(groupedMilestones) {
  if (groupedMilestones.some((milestone) => milestone.state === "failed")) return "failed";
  const terminalState = groupedMilestones[groupedMilestones.length - 1]?.state || "pending";
  if (terminalState === "complete" || terminalState === "skipped") return "complete";
  if (groupedMilestones.some((milestone) => milestone.state === "active" || milestone.state === "complete")) return "active";
  return terminalState;
}

function selectGroupedMilestone(groupedMilestones, state) {
  if (state === "failed") return groupedMilestones.find((milestone) => milestone.state === "failed") || groupedMilestones[0];
  if (state === "active") {
    return (
      [...groupedMilestones].reverse().find((milestone) => milestone.state === "active" || milestone.state === "complete") ||
      groupedMilestones[0]
    );
  }
  if (state === "complete") {
    return [...groupedMilestones].reverse().find((milestone) => milestone.state === "complete" || milestone.state === "skipped") || groupedMilestones[0];
  }
  return groupedMilestones[0];
}

function resolveRuntimeGroupState(runtimeRows, milestoneState) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  if (!rows.length && milestoneState === "complete") return "active";
  const runtimeStates = rows.map((entry) => normalizeRuntimeHealthState(entry?.statusCode));
  if (milestoneState === "failed" || runtimeStates.some((state) => state === "failed")) return "failed";
  if (runtimeStates.some((state) => state === "active" || state === "pending")) return "active";
  if (rows.length && runtimeStates.every((state) => state === "complete" || state === "skipped")) return "complete";
  if (milestoneState === "active") return "active";
  return milestoneState || "pending";
}

function buildRuntimeGroupSummary(runtimeRows) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const counts = rows.reduce(
    (acc, entry) => {
      const kind = String(entry?.healthKind || "role").toLowerCase() === "service" ? "service" : "role";
      const state = normalizeRuntimeHealthState(entry?.statusCode);
      if (state === "skipped") {
        acc[kind].skipped += 1;
      } else {
        acc[kind].total += 1;
        if (state === "complete") acc[kind].healthy += 1;
      }
      return acc;
    },
    { role: { total: 0, healthy: 0, skipped: 0 }, service: { total: 0, healthy: 0, skipped: 0 } }
  );
  const pieces = [];
  if (counts.role.total) pieces.push(`${counts.role.healthy}/${counts.role.total} roles healthy`);
  if (counts.service.total) pieces.push(`${counts.service.healthy}/${counts.service.total} services healthy`);
  const skipped = counts.role.skipped + counts.service.skipped;
  if (skipped) pieces.push(`${skipped} not applicable`);
  return pieces.length ? pieces.join(" · ") : "Awaiting role telemetry";
}

function buildRuntimeHealthTooltip(entry) {
  if (!entry) return "";
  const details = entry.detailsMap && typeof entry.detailsMap === "object" ? entry.detailsMap : {};
  const detailRows = Object.entries(details)
    .filter(([, value]) => value !== null && value !== undefined && String(value).trim() !== "")
    .slice(0, 8);
  return (
    <Box sx={{ maxWidth: 360, py: 0.25 }}>
      <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.8rem", fontWeight: 760, lineHeight: 1.25 }}>
        {entry.name || "Runtime health"}
      </Typography>
      <Typography sx={{ mt: 0.55, color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
        Status: {entry.status || "Unknown"}
      </Typography>
      {entry.lastCheckedText ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Last checked: {entry.lastCheckedText}
        </Typography>
      ) : null}
      {entry.lastSuccessText ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Last healthy: {entry.lastSuccessText}
        </Typography>
      ) : null}
      {entry.contextLabel ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Context: {entry.contextLabel}
        </Typography>
      ) : null}
      {entry.desiredState || entry.observedState ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Desired: {entry.desiredState || "Unknown"} · Observed: {entry.observedState || "Unknown"}
        </Typography>
      ) : null}
      {entry.recoveryAttempts ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Recovery attempts: {entry.recoveryAttempts}
        </Typography>
      ) : null}
      {detailRows.length ? (
        <Box sx={{ mt: 0.55 }}>
          {detailRows.map(([key, value]) => (
            <Typography key={key} sx={{ color: MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.35 }}>
              {String(key).replace(/[_-]+/g, " ")}: {String(value)}
            </Typography>
          ))}
        </Box>
      ) : null}
      {entry.detail ? (
        <Typography sx={{ mt: 0.65, color: MAGIC_UI.textBright, fontSize: "0.69rem", lineHeight: 1.35, whiteSpace: "pre-wrap" }}>
          {entry.detail}
        </Typography>
      ) : null}
    </Box>
  );
}

function getConnectorColor(state) {
  if (state === "complete") return "linear-gradient(180deg, rgba(52,211,153,0.9), rgba(52,211,153,0.16))";
  if (state === "active") return "linear-gradient(180deg, rgba(125,211,252,0.95), rgba(125,211,252,0.18))";
  if (state === "failed") return "linear-gradient(180deg, rgba(255,123,137,0.9), rgba(255,123,137,0.18))";
  return "rgba(148,163,184,0.18)";
}

function TimelineIcon({ state }) {
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;
  const Icon = meta.Icon;
  return (
    <Box
      sx={{
        width: 24,
        height: 24,
        borderRadius: "50%",
        color: meta.color,
        background: state === "active" ? meta.bg : "transparent",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        ...(state === "active"
          ? {
              animation: "agentTimelinePulse 1.8s ease-out infinite",
            }
          : null),
      }}
    >
      <Icon
        sx={{
          fontSize: 20,
          animation: state === "active" ? "agentTimelineSpin 1.15s linear infinite" : "none",
        }}
      />
    </Box>
  );
}

export function RuntimeRoleHealthBreakdown({ runtimeRows, sx = {} }) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const groups = [
    { key: "role", label: "Roles", rows: rows.filter((entry) => String(entry?.healthKind || "role").toLowerCase() !== "service") },
    { key: "service", label: "Services", rows: rows.filter((entry) => String(entry?.healthKind || "").toLowerCase() === "service") },
  ].filter((group) => group.rows.length);
  const state = resolveRuntimeGroupState(rows, rows.length ? "complete" : "active");
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;

  return (
    <Box
      sx={{
        borderRadius: 2,
        border: `1px solid ${state === "pending" ? "rgba(148, 163, 184, 0.26)" : meta.color}`,
        background: `linear-gradient(145deg, rgba(7,11,24,0.96), ${meta.bg})`,
        boxShadow:
          state === "active"
            ? `0 0 0 1px ${meta.color}44, 0 18px 44px rgba(2,6,23,0.58), 0 0 28px ${meta.color}28`
            : "0 16px 36px rgba(2,6,23,0.46)",
        p: 1.1,
        "@keyframes agentTimelineSpin": {
          from: { transform: "rotate(0deg)" },
          to: { transform: "rotate(360deg)" },
        },
        ...sx,
      }}
    >
      <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.78rem", fontWeight: 760, lineHeight: 1.2 }}>
        Runtime role health
      </Typography>
      <Typography sx={{ mt: 0.2, color: state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted, fontSize: "0.67rem", lineHeight: 1.25 }}>
        {buildRuntimeGroupSummary(rows)}
      </Typography>
      <Box sx={{ mt: 0.9, display: "flex", flexDirection: "column", gap: 0.85 }}>
        {groups.length ? (
          groups.map((group, groupIndex) => (
            <Box key={group.key} sx={{ minWidth: 0, mt: groupIndex > 0 ? 0.7 : 0 }}>
              <Typography
                sx={{
                  mb: 0.35,
                  color: MAGIC_UI.textMuted,
                  fontSize: "0.58rem",
                  fontWeight: 800,
                  letterSpacing: "0.08em",
                  textTransform: "uppercase",
                }}
              >
                {group.label}
              </Typography>
              <Box sx={{ display: "flex", flexDirection: "column", gap: 0.35 }}>
                {group.rows.map((entry, index) => {
                  const rowState = normalizeRuntimeHealthState(entry?.statusCode);
                  const rowColor = getRuntimeStatusColor(entry?.statusCode);
                  const rowMeta = STARTUP_STATE_META[rowState] || STARTUP_STATE_META.pending;
                  const RowIcon = rowMeta.Icon;
                  return (
                    <Tooltip key={entry?.id || `${group.key}-${index}`} title={buildRuntimeHealthTooltip(entry)} arrow placement="right">
                      <Box
                        sx={{
                          width: "100%",
                          minHeight: 31,
                          px: 0.65,
                          py: 0.4,
                          borderRadius: 1.3,
                          border: `1px solid ${colorWithAlpha(rowColor, 0.28)}`,
                          background: colorWithAlpha(rowColor, 0.06),
                          color: MAGIC_UI.textBright,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "flex-start",
                          gap: 0.65,
                          textAlign: "left",
                          overflow: "hidden",
                          cursor: "help",
                          "&:hover": {
                            borderColor: colorWithAlpha(rowColor, 0.68),
                            background: colorWithAlpha(rowColor, 0.11),
                          },
                        }}
                      >
                        <RowIcon
                          sx={{
                            color: rowColor,
                            fontSize: 15,
                            flexShrink: 0,
                            animation: rowState === "active" ? "agentTimelineSpin 1.15s linear infinite" : "none",
                          }}
                        />
                        <Box sx={{ minWidth: 0, flex: 1 }}>
                          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.66rem", fontWeight: 740, lineHeight: 1.12 }} noWrap>
                            {entry?.name || `Runtime ${index + 1}`}
                          </Typography>
                          <Typography sx={{ mt: 0.1, color: rowColor, fontSize: "0.59rem", fontWeight: 700, lineHeight: 1.1 }} noWrap>
                            {entry?.status || "Unknown"}
                          </Typography>
                        </Box>
                      </Box>
                    </Tooltip>
                  );
                })}
              </Box>
            </Box>
          ))
        ) : (
          <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.35 }}>
            Role telemetry has not reported yet.
          </Typography>
        )}
      </Box>
    </Box>
  );
}

function RuntimeBranch({ runtimeRows }) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const state = resolveRuntimeGroupState(rows, rows.length ? "complete" : "active");
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;

  return (
    <Box
      sx={{
        position: "relative",
        width: { xs: "100%", lg: ROLE_BRANCH_WIDTH },
        ml: { xs: 0, lg: 2.2 },
        mt: { xs: 1.15, lg: 0 },
        flexShrink: 0,
        "&::before": {
          content: '""',
          position: "absolute",
          left: { xs: 14, lg: -28 },
          top: { xs: -12, lg: 26 },
          width: { xs: 2, lg: 28 },
          height: { xs: 12, lg: 2 },
          borderRadius: 999,
          background: meta.color,
          opacity: state === "pending" ? 0.35 : 0.9,
        },
      }}
    >
      <RuntimeRoleHealthBreakdown runtimeRows={rows} />
    </Box>
  );
}

function TimelineStep({ step, isLast, runtimeRows }) {
  const meta = STARTUP_STATE_META[step.state] || STARTUP_STATE_META.pending;
  const showDetail = step.state !== "pending" || Boolean(step.detail);
  const detailColor = step.state === "failed" ? "#ff7b89" : step.state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted;
  return (
    <Box sx={{ display: "flex", alignItems: "stretch", width: "100%" }}>
      <Box
        sx={{
          width: 28,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          flexShrink: 0,
        }}
      >
        <TimelineIcon state={step.state} />
        {!isLast ? (
          <Box
            sx={{
              mt: 0.45,
              mb: 0.2,
              width: 2,
              flexGrow: 1,
              minHeight: step.branch ? 135 : showDetail ? 34 : 22,
              borderRadius: 999,
              background: getConnectorColor(step.state),
            }}
          />
        ) : null}
      </Box>
      <Box sx={{ flex: 1, minWidth: 0, pb: isLast ? 0 : 0.9 }}>
        <Box sx={{ display: { xs: "block", lg: step.branch ? "flex" : "block" }, alignItems: "flex-start", minWidth: 0 }}>
          <Tooltip title={step.timestampText || ""} arrow placement="top">
            <Box
              sx={{
                width: "100%",
                maxWidth: TIMELINE_WIDTH,
                borderRadius: 2,
                border: `1px solid ${step.state === "pending" ? "rgba(148, 163, 184, 0.2)" : colorWithAlpha(meta.color, 0.64)}`,
                background: `linear-gradient(145deg, rgba(7,11,24,0.78), ${meta.bg})`,
                px: 1.15,
                py: 0.9,
                boxShadow: step.state === "active" ? `0 0 0 1px ${meta.color}33, 0 0 24px ${meta.color}22` : "none",
              }}
            >
              <Typography
                sx={{
                  color: step.state === "pending" || step.state === "skipped" ? MAGIC_UI.textMuted : MAGIC_UI.textBright,
                  fontSize: "0.84rem",
                  fontWeight: step.state === "active" || step.state === "complete" ? 700 : 600,
                  lineHeight: 1.22,
                }}
              >
                {step.label}
              </Typography>
              {showDetail ? (
                <Typography sx={{ mt: 0.3, color: detailColor, fontSize: "0.72rem", lineHeight: 1.35 }}>
                  {step.detail || meta.label}
                </Typography>
              ) : null}
            </Box>
          </Tooltip>
          {step.branch ? <RuntimeBranch runtimeRows={runtimeRows} /> : null}
        </Box>
      </Box>
    </Box>
  );
}

function useTimelineSteps(milestones, runtimeRows, formatTimestamp) {
  return useMemo(() => {
    const milestoneByKey = buildMilestoneLookup(milestones);
    return MAIN_TIMELINE_DEFINITIONS.map((definition) => {
      const groupedMilestones = resolveGroupedMilestones(definition, milestoneByKey);
      const milestoneState = resolveGroupedState(groupedMilestones);
      const state = definition.id === "agent_role_loading" ? resolveRuntimeGroupState(runtimeRows, milestoneState) : milestoneState;
      const displayMilestone = selectGroupedMilestone(groupedMilestones, state) || {};
      return {
        id: definition.id,
        label: definition.label,
        branch: definition.branch,
        state,
        detail: displayMilestone.detail || STARTUP_STATE_META[state]?.label || "",
        timestampText: displayMilestone.timestamp ? formatTimestamp(displayMilestone.timestamp) : "",
      };
    });
  }, [formatTimestamp, milestones, runtimeRows]);
}

export default function AgentStartupFlow({
  milestones,
  runtimeRows = [],
  formatTimestamp = (value) => String(value || ""),
}) {
  const rows = Array.isArray(milestones) ? milestones : [];
  const runtimeHealthRows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const timelineSteps = useTimelineSteps(rows, runtimeRows, formatTimestamp);

  if (!rows.length && !runtimeHealthRows.length) {
    return (
      <Box
        sx={{
          borderRadius: 2,
          border: `1px dashed ${MAGIC_UI.panelBorder}`,
          px: 1.4,
          py: 1.2,
          color: MAGIC_UI.textMuted,
          fontSize: "0.84rem",
        }}
      >
        Awaiting startup telemetry
      </Box>
    );
  }

  return (
    <Box
      sx={{
        height: "100%",
        minHeight: { xs: 520, md: 620 },
        width: "100%",
        overflow: "auto",
        borderRadius: 2,
        background:
          "linear-gradient(180deg, rgba(3,7,18,0.22), rgba(3,7,18,0.42)), radial-gradient(circle at 25% 25%, rgba(125,211,252,0.08), transparent 34%)",
        p: { xs: 1.1, md: 1.5 },
        "@keyframes agentTimelineSpin": {
          from: { transform: "rotate(0deg)" },
          to: { transform: "rotate(360deg)" },
        },
        "@keyframes agentTimelinePulse": {
          "0%": { boxShadow: "0 0 0 0 rgba(125, 211, 252, 0.26)" },
          "70%": { boxShadow: "0 0 0 10px rgba(125, 211, 252, 0)" },
          "100%": { boxShadow: "0 0 0 0 rgba(125, 211, 252, 0)" },
        },
      }}
    >
      <Box sx={{ width: "100%", maxWidth: 1120, mx: "auto", display: "flex", flexDirection: "column", alignItems: "stretch" }}>
        {timelineSteps.map((step, index) => (
          <TimelineStep
            key={step.id}
            step={step}
            isLast={index === timelineSteps.length - 1}
            runtimeRows={runtimeRows}
          />
        ))}
      </Box>
    </Box>
  );
}
