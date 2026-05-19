import React, { useEffect, useMemo, useRef } from "react";
import { Box, Typography } from "@mui/material";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import { MAGIC_UI } from "./Shared.jsx";
import AgentStartupFlow, { normalizeMilestoneState } from "./Agent_Startup_Flow.jsx";

const AGENT_HEALTH_KIND = Object.freeze({
  role: "role",
  service: "service",
});

const AGENT_HEALTH_PRESENTATION_BY_KEY = Object.freeze({
  deviceaudit: { label: "Device Auditor", kind: AGENT_HEALTH_KIND.role },
  deviceauditor: { label: "Device Auditor", kind: AGENT_HEALTH_KIND.role },
  contextsystem: { label: "System Context", kind: AGENT_HEALTH_KIND.role },
  contextcurrentuser: { label: "Current User Context", kind: AGENT_HEALTH_KIND.role },
  remoteshell: { label: "Remote Shell", kind: AGENT_HEALTH_KIND.role },
  remoteshellservice: { label: "Remote Shell", kind: AGENT_HEALTH_KIND.role },
  servicecontrol: { label: "Service Control", kind: AGENT_HEALTH_KIND.role },
  servicemanagement: { label: "Service Management", kind: AGENT_HEALTH_KIND.role },
  softwaremanagement: { label: "Software Management", kind: AGENT_HEALTH_KIND.role },
  scriptexeccurrentuser: { label: "Script Execution - CURRENTUSER", kind: AGENT_HEALTH_KIND.role },
  scriptexecsystem: { label: "Script Execution - SYSTEM", kind: AGENT_HEALTH_KIND.role },
  processmanagement: { label: "Process Management", kind: AGENT_HEALTH_KIND.role },
  filemanagement: { label: "File Management", kind: AGENT_HEALTH_KIND.role },
  vnc: { label: "Borealis Agent - UltraVNC", kind: AGENT_HEALTH_KIND.service },
  ultravnc: { label: "Borealis Agent - UltraVNC", kind: AGENT_HEALTH_KIND.service },
  ultravncservice: { label: "Borealis Agent - UltraVNC", kind: AGENT_HEALTH_KIND.service },
  wireguard: { label: "Borealis Agent - WireGuard", kind: AGENT_HEALTH_KIND.service },
  wireguardtunnel: { label: "Borealis Agent - WireGuard", kind: AGENT_HEALTH_KIND.service },
  wireguardservice: { label: "Borealis Agent - WireGuard", kind: AGENT_HEALTH_KIND.service },
  wireguardvpn: { label: "Borealis Agent - WireGuard", kind: AGENT_HEALTH_KIND.service },
});

const HIDDEN_AGENT_HEALTH_KEYS = Object.freeze(
  new Set(["macro", "macroautomation", "macros", "screenshot", "screenshotcapture", "nodescreenshot", "systemheartbeat", "startuptimeline"])
);

function compactAgentHealthKey(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "");
}

function formatRoleHealthContext(context) {
  const normalized = String(context || "").trim().toLowerCase();
  if (!normalized) return "";
  if (normalized === "system") return "System";
  if (normalized === "currentuser" || normalized === "current_user" || normalized === "interactive") return "Current User";
  return normalized.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function normalizeRoleHealthStatusText(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) return "Unknown";
  if (normalized === "not_applicable" || normalized === "no_desktop_environment_active") {
    return "No Desktop Environment Active";
  }
  return normalized.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function resolveAgentHealthPresentation(item, index = 0) {
  const rawRoleName = String(item?.role_name || item?.role || "").trim();
  const rawRoleLabel = String(item?.role_label || "").trim();
  const presentation =
    AGENT_HEALTH_PRESENTATION_BY_KEY[compactAgentHealthKey(rawRoleName)] ||
    AGENT_HEALTH_PRESENTATION_BY_KEY[compactAgentHealthKey(rawRoleLabel)] ||
    null;
  return {
    label: presentation?.label || rawRoleLabel || rawRoleName || `Role ${index + 1}`,
    kind: presentation?.kind || AGENT_HEALTH_KIND.role,
  };
}

function normalizeAgentHealthServiceDetails(details) {
  if (!details || typeof details !== "object") return {};
  const normalized = { ...details };
  const serviceName = String(normalized.service_name || "").trim().toLowerCase();
  if (["uvnc_service", "uvnc_service_64", "ultravnc", "winvnc"].includes(serviceName)) {
    normalized.service_name = "BorealisAgentUltraVNC";
  }
  if (serviceName === "wireguardtunnel$borealis" || serviceName === "wireguardtunnel$borealis-wg") {
    normalized.service_name = "Borealis Agent - WireGuard";
  }
  return normalized;
}

function normalizeAgentHealthDetailText(value) {
  let text = String(value || "")
    .trim()
    .replace(/\buvnc_service(?:_64)?\b/gi, "BorealisAgentUltraVNC");
  if (!text.includes("Borealis Agent - UltraVNC")) {
    text = text.replace(/\bUltraVNC\b/g, "Borealis Agent - UltraVNC");
  }
  if (!text.includes("Borealis Agent - WireGuard")) {
    text = text.replace(/\bWireGuard VPN\b/g, "Borealis Agent - WireGuard");
  }
  return text;
}

function parseJsonArray(value) {
  if (Array.isArray(value)) return value;
  const text = String(value || "").trim();
  if (!text) return [];
  try {
    const parsed = JSON.parse(text);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function Island({ title, icon = null, meta = "", children, sx = {} }) {
  return (
    <Box
      sx={{
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: "linear-gradient(180deg, rgba(15,23,42,0.78), rgba(7,11,24,0.92))",
        boxShadow: "0 24px 70px rgba(2,6,23,0.45)",
        p: 1.6,
        minWidth: 0,
        minHeight: 0,
        ...sx,
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1.5, mb: 1.3 }}>
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.8, minWidth: 0 }}>
          {icon ? <Box sx={{ color: MAGIC_UI.accentA, display: "inline-flex", alignItems: "center" }}>{icon}</Box> : null}
          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.95rem", fontWeight: 760 }}>{title}</Typography>
        </Box>
        {meta ? (
          <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", textAlign: "right" }}>{meta}</Typography>
        ) : null}
      </Box>
      {children}
    </Box>
  );
}

export default function AgentHealthTab({
  agentRoleHealth,
  summaryDataReady = true,
  formatTimestamp = (value) => String(value || ""),
  hostname = "",
  onRequestRefresh = null,
}) {
  const refreshTimerRef = useRef(null);
  const payload = agentRoleHealth && typeof agentRoleHealth === "object" ? agentRoleHealth : {};
  const items = Array.isArray(payload?.roles) ? payload.roles : [];

  const agentHealthRows = useMemo(() => {
    const normalizedItems = items.map((item, index) => {
      const rawRoleName = String(item?.role_name || item?.role || "").trim();
      const rawRoleLabel = String(item?.role_label || item?.label || "").trim();
      const presentation = resolveAgentHealthPresentation(item, index);
      const detailsMap = normalizeAgentHealthServiceDetails({
        ...(item?.details && typeof item.details === "object" ? item.details : {}),
        desired_state: item?.desired_state,
        observed_state: item?.observed_state,
        last_error: item?.last_error,
        recovery_attempts: item?.recovery_attempts,
      });
      return {
        id: item?.role_id || `${presentation.kind}-${presentation.label}-${index}`,
        baseLabel: presentation.label,
        presentationKey: compactAgentHealthKey(rawRoleName || rawRoleLabel || presentation.label),
        healthKind: presentation.kind,
        sourceRoleName: rawRoleName,
        sourceRoleLabel: rawRoleLabel,
        contextLabel: formatRoleHealthContext(item?.context),
        status: normalizeRoleHealthStatusText(item?.status || item?.status_code || "Unknown"),
        statusCode: String(item?.status_code || item?.status || "unknown").trim().toLowerCase(),
        lastCheckedAt: item?.last_checked_at ?? null,
        lastCheckedText: formatTimestamp(item?.last_checked_at),
        lastSuccessAt: item?.last_success_at ?? null,
        lastSuccessText: formatTimestamp(item?.last_success_at),
        desiredState: String(item?.desired_state || detailsMap?.desired_state || "").trim(),
        observedState: String(item?.observed_state || detailsMap?.observed_state || "").trim(),
        lastError: String(item?.last_error || "").trim(),
        recoveryAttempts: Number(item?.recovery_attempts || 0),
        detail: normalizeAgentHealthDetailText(item?.detail),
        detailsMap,
      };
    });
    const noDesktopEnvironmentActive = normalizedItems.some((item) => {
      const key = compactAgentHealthKey(item.sourceRoleName || item.sourceRoleLabel || item.baseLabel);
      return (
        key === "contextcurrentuser" &&
        (
          item.statusCode === "not_applicable" ||
          item.statusCode === "no_desktop_environment_active" ||
          /no desktop environment active/i.test(item.detail)
        )
      );
    });
    const visibleItems = normalizedItems.filter(
      (item) => !HIDDEN_AGENT_HEALTH_KEYS.has(String(item.presentationKey || "").trim().toLowerCase())
    );
    const labelCounts = visibleItems.reduce((acc, item) => {
      const key = `${item.healthKind}:${String(item.baseLabel || "").trim().toLowerCase()}`;
      if (key !== `${item.healthKind}:`) {
        acc[key] = (acc[key] || 0) + 1;
      }
      return acc;
    }, {});
    return visibleItems
      .map((item) => {
        const labelKey = `${item.healthKind}:${String(item.baseLabel || "").trim().toLowerCase()}`;
        const name =
          item.contextLabel && labelCounts[labelKey] > 1 ? `${item.baseLabel} (${item.contextLabel})` : item.baseLabel;
        const key = compactAgentHealthKey(item.sourceRoleName || item.sourceRoleLabel || item.baseLabel);
        if (
          noDesktopEnvironmentActive &&
          ["vnc", "ultravnc", "ultravncservice"].includes(key) &&
          (item.statusCode === "unsupported" || item.statusCode === "pending" || item.statusCode === "recovering")
        ) {
          return {
            ...item,
            name,
            status: "No Desktop Environment Active",
            statusCode: "not_applicable",
            detail: "No Desktop Environment Active.",
            detailsMap: {
              ...item.detailsMap,
              running_status: "No Desktop Environment Active.",
              service_state: "Not Applicable",
              listener_state: "Not Applicable",
            },
          };
        }
        return { ...item, name };
      })
      .sort((left, right) => String(left.name || "").localeCompare(String(right.name || "")));
  }, [formatTimestamp, items]);

  const startupRole = useMemo(() => {
    for (const item of items) {
      const roleName = compactAgentHealthKey(item?.role_name || item?.role || "");
      const roleLabel = compactAgentHealthKey(item?.role_label || item?.label || "");
      if (roleName === "systemheartbeat" || roleLabel === "startuptimeline") {
        return {
          detail: String(item?.detail || "").trim(),
          statusCode: String(item?.status_code || item?.status || "unknown").trim().toLowerCase(),
          detailsMap: item?.details && typeof item.details === "object" ? item.details : {},
        };
      }
    }
    return null;
  }, [items]);

  const milestones = useMemo(() => parseJsonArray(startupRole?.detailsMap?.milestones_json), [startupRole]);
  const timelineMeta = useMemo(() => {
    if (!milestones.length) {
      return agentHealthRows.length ? "Startup telemetry pending - role health current" : "Awaiting startup telemetry";
    }
    const active = milestones.find((item) => normalizeMilestoneState(item?.state) === "active");
    if (active) return `Active: ${active.label || active.key}`;
    const failed = milestones.find((item) => normalizeMilestoneState(item?.state) === "failed");
    if (failed) return `Needs attention: ${failed.label || failed.key}`;
    return `${milestones.filter((item) => normalizeMilestoneState(item?.state) === "complete").length}/${milestones.length} complete`;
  }, [agentHealthRows.length, milestones]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    const expectedHost = String(hostname || "").trim().toLowerCase();
    if (!socket || !expectedHost || typeof onRequestRefresh !== "function") return undefined;
    const handler = (eventPayload = {}) => {
      const payloadHost = String(eventPayload?.hostname || "").trim().toLowerCase();
      if (payloadHost && payloadHost !== expectedHost) return;
      if (refreshTimerRef.current) window.clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = window.setTimeout(() => onRequestRefresh({ silent: true, includeAgents: false }), 250);
    };
    socket.on("agent_status_changed", handler);
    return () => {
      if (refreshTimerRef.current) window.clearTimeout(refreshTimerRef.current);
      socket.off("agent_status_changed", handler);
    };
  }, [hostname, onRequestRefresh]);

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.6,
        minWidth: 0,
        minHeight: { xs: 0, xl: "calc(100vh - 272px)" },
        height: { xs: "auto", xl: "calc(100vh - 272px)" },
        flex: "1 1 auto",
        width: "100%",
      }}
    >
      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: "1fr",
          alignItems: "stretch",
          gap: 1.6,
          flex: { xs: "0 0 auto", xl: 1 },
          minHeight: 0,
          minWidth: 0,
        }}
      >
        <Island
          title="Startup Timeline"
          icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />}
          meta={timelineMeta}
          sx={{ height: { xs: "auto", xl: "100%" }, display: "flex", flexDirection: "column" }}
        >
          <Box sx={{ flex: 1, minHeight: 0 }}>
            <AgentStartupFlow
              milestones={milestones}
              runtimeRows={summaryDataReady ? agentHealthRows : []}
              formatTimestamp={formatTimestamp}
            />
          </Box>
        </Island>
      </Box>
    </Box>
  );
}
