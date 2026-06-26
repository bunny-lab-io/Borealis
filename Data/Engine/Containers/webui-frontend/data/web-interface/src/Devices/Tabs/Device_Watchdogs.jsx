import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  Divider,
  LinearProgress,
  Paper,
  Stack,
  Typography,
} from "@mui/material";
import {
  AddRounded as AddIcon,
  CachedRounded as RefreshIcon,
  CheckCircleOutlineRounded as AcknowledgeIcon,
  OpenInNewRounded as OpenIcon,
  PauseCircleOutlineRounded as SuppressIcon,
  PlayArrowRounded as ClearIcon,
} from "@mui/icons-material";

import { APP_PATHS } from "../../app/routes/paths.js";
import {
  formatTimestamp,
  promptRequiredSuppressionReason,
  severityColor,
  summarizeRuleResults,
} from "../../Automation/Watchdogs/shared.jsx";

function SectionShell({ title, subtitle, action, children }) {
  return (
    <Paper
      variant="outlined"
      sx={{
        borderColor: "rgba(148,163,184,0.24)",
        background: "linear-gradient(145deg, rgba(7,10,24,0.96), rgba(6,10,28,0.92))",
        p: 2,
      }}
    >
      <Stack spacing={1.5}>
        <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" spacing={1}>
          <Box>
            <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>{title}</Typography>
            {subtitle ? (
              <Typography variant="body2" sx={{ color: "#94a3b8", mt: 0.35 }}>
                {subtitle}
              </Typography>
            ) : null}
          </Box>
          {action || null}
        </Stack>
        {children}
      </Stack>
    </Paper>
  );
}

function ItemShell({ children, tone = "default" }) {
  return (
    <Box
      sx={{
        p: 1.5,
        borderRadius: 2,
        border: "1px solid rgba(148,163,184,0.2)",
        background:
          tone === "danger"
            ? "rgba(127,29,29,0.18)"
            : tone === "warning"
              ? "rgba(120,53,15,0.16)"
              : "rgba(15,23,42,0.58)",
      }}
    >
      {children}
    </Box>
  );
}

export default function DeviceWatchdogsTab({
  deviceId,
  hostname,
  deviceGuid,
  siteId = null,
  siteName = "",
}) {
  const navigate = useNavigate();
  const [payload, setPayload] = useState({ device: null, incidents: [], assignments: [], overrides: [] });
  const [loading, setLoading] = useState(false);
  const [actionBusy, setActionBusy] = useState(false);
  const [error, setError] = useState("");

  const resolvedDeviceKey = useMemo(
    () => String(deviceGuid || hostname || deviceId || "").trim(),
    [deviceGuid, deviceId, hostname]
  );

  const loadData = useCallback(async () => {
    if (!resolvedDeviceKey) return;
    setLoading(true);
    setError("");
    try {
      const response = await fetch(`/api/devices/${encodeURIComponent(resolvedDeviceKey)}/watchdogs`, {
        credentials: "include",
        cache: "no-store",
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.errors?.[0] || data?.error || data?.message || `HTTP ${response.status}`);
      }
      setPayload({
        device: data?.device || null,
        incidents: Array.isArray(data?.incidents) ? data.incidents : [],
        assignments: Array.isArray(data?.assignments) ? data.assignments : [],
        overrides: Array.isArray(data?.overrides) ? data.overrides : [],
      });
    } catch (err) {
      setPayload({ device: null, incidents: [], assignments: [], overrides: [] });
      setError(String(err?.message || err || "Failed to load device watchdogs."));
    } finally {
      setLoading(false);
    }
  }, [resolvedDeviceKey]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || typeof socket.on !== "function") return undefined;
    const expectedHost = String(hostname || "").trim().toLowerCase();
    const handler = (event) => {
      const eventHost = String(event?.hostname || "").trim().toLowerCase();
      if (eventHost && expectedHost && eventHost !== expectedHost) return;
      loadData();
    };
    socket.on("device_watchdogs_changed", handler);
    return () => {
      try {
        socket.off("device_watchdogs_changed", handler);
      } catch {
        /* noop */
      }
    };
  }, [hostname, loadData]);

  const launchDeviceWatchdog = useCallback(() => {
    const targetHostname = String(payload?.device?.hostname || hostname || "").trim();
    if (!targetHostname) return;
    const targetGuid = String(payload?.device?.device_guid || deviceGuid || "").trim();
    const targetSiteId = payload?.device?.site_id ?? siteId ?? null;
    const targetSiteName = payload?.device?.site_name || siteName || "";
    navigate(APP_PATHS.watchdogNew, {
      state: {
        watchdogDraft: {
          name: `${targetHostname} Watchdog`,
          description: `Device-scoped watchdog for ${targetHostname}.`,
          site_mode: targetSiteId ? "specific_sites" : "global",
          site_ids: targetSiteId ? [Number(targetSiteId)] : [],
          targets: [
            {
              kind: "device",
              device_guid: targetGuid,
              hostname: targetHostname,
              site_id: targetSiteId,
              site_name: targetSiteName,
            },
          ],
        },
      },
    });
  }, [deviceGuid, hostname, navigate, payload?.device, siteId, siteName]);

  const acknowledgeIncident = useCallback(
    async (incidentId) => {
      setActionBusy(true);
      setError("");
      try {
        const response = await fetch(`/api/watchdogs/incidents/${encodeURIComponent(incidentId)}/acknowledge`, {
          method: "POST",
          credentials: "include",
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(data?.errors?.[0] || data?.error || data?.message || `HTTP ${response.status}`);
        }
        loadData();
      } catch (err) {
        setError(String(err?.message || err || "Failed to acknowledge incident."));
      } finally {
        setActionBusy(false);
      }
    },
    [loadData]
  );

  const saveOverride = useCallback(
    async ({ watchdogId, clear = false }) => {
      if (!resolvedDeviceKey || !watchdogId) return;
      setError("");
      let reason = "";
      if (!clear) {
        const promptedReason = promptRequiredSuppressionReason("Suppression reason required for this device:");
        if (promptedReason === null) return;
        if (!promptedReason) {
          setError("Enter a suppression reason before suppressing this watchdog for the device.");
          return;
        }
        reason = promptedReason;
      }
      setActionBusy(true);
      try {
        const response = await fetch(`/api/devices/${encodeURIComponent(resolvedDeviceKey)}/watchdogs/overrides`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            clear
              ? { watchdog_id: watchdogId, clear: true }
              : { watchdog_id: watchdogId, state: "suppressed", reason }
          ),
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(data?.errors?.[0] || data?.error || data?.message || `HTTP ${response.status}`);
        }
        loadData();
      } catch (err) {
        setError(String(err?.message || err || "Failed to update device override."));
      } finally {
        setActionBusy(false);
      }
    },
    [loadData, resolvedDeviceKey]
  );

  return (
    <Stack spacing={2}>
      <SectionShell
        title="Watchdogs"
        subtitle="Review active incidents first, then manage the policies currently targeting this device."
        action={
          <Stack direction="row" spacing={1}>
            <Button startIcon={<RefreshIcon />} onClick={loadData} disabled={loading || actionBusy}>
              Refresh
            </Button>
            <Button variant="contained" startIcon={<AddIcon />} onClick={launchDeviceWatchdog}>
              New Watchdog for This Device
            </Button>
          </Stack>
        }
      >
        {loading ? <LinearProgress /> : null}
        {error ? <Alert severity="error">{error}</Alert> : null}
        <Typography variant="body2" sx={{ color: "#94a3b8" }}>
          Use device-specific suppressions here when you need to mute one endpoint without cloning or weakening the shared watchdog policy.
        </Typography>
      </SectionShell>

      <SectionShell
        title={`Active Incidents (${payload.incidents.length})`}
        subtitle="These are the currently open watchdog incidents affecting this device."
      >
        {!payload.incidents.length ? (
          <Typography variant="body2" sx={{ color: "#94a3b8" }}>
            No active watchdog incidents are currently open for this device.
          </Typography>
        ) : (
          <Stack spacing={1.25}>
            {payload.incidents.map((incident) => (
              <ItemShell key={incident.id} tone="danger">
                <Stack spacing={1}>
                  <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" spacing={1}>
                    <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
                      <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>
                        {incident.watchdog_name || incident.title}
                      </Typography>
                      <Chip
                        size="small"
                        label={String(incident.severity || "warning").replace(/^./, (char) => char.toUpperCase())}
                        variant="outlined"
                        sx={{
                          color: severityColor(incident.severity),
                          borderColor: severityColor(incident.severity),
                        }}
                      />
                    </Stack>
                    <Stack direction="row" spacing={1}>
                      <Button
                        size="small"
                        startIcon={<AcknowledgeIcon />}
                        onClick={() => acknowledgeIncident(incident.id)}
                        disabled={actionBusy}
                      >
                        Acknowledge
                      </Button>
                      <Button
                        size="small"
                        startIcon={<OpenIcon />}
                        onClick={() => navigate(APP_PATHS.watchdog(incident.watchdog_id))}
                      >
                        Open Policy
                      </Button>
                    </Stack>
                  </Stack>
                  <Typography sx={{ color: "#e2e8f0" }}>{incident.message || "Active alert"}</Typography>
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    {summarizeRuleResults(incident.sample) || "No sampled rule details available."}
                  </Typography>
                  <Typography variant="caption" sx={{ color: "#7dd3fc" }}>
                    Opened {formatTimestamp(incident.opened_at)} | Updated {formatTimestamp(incident.updated_at)}
                  </Typography>
                </Stack>
              </ItemShell>
            ))}
          </Stack>
        )}
      </SectionShell>

      <SectionShell
        title={`Effective Watchdogs (${payload.assignments.length})`}
        subtitle="These are the watchdog policies currently resolved onto this device after scope and target evaluation."
      >
        {!payload.assignments.length ? (
          <Typography variant="body2" sx={{ color: "#94a3b8" }}>
            No watchdogs are currently assigned to this device.
          </Typography>
        ) : (
          <Stack spacing={1.25}>
            {payload.assignments.map((assignment) => (
              <ItemShell
                key={assignment.watchdog_id}
                tone={assignment.state === "triggered" ? "warning" : assignment.override ? "warning" : "default"}
              >
                <Stack spacing={1}>
                  <Stack direction={{ xs: "column", lg: "row" }} justifyContent="space-between" spacing={1}>
                    <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
                      <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>{assignment.name}</Typography>
                      <Chip
                        size="small"
                        label={String(assignment.state || "normal").replace(/_/g, " ")}
                        variant="outlined"
                        sx={{
                          color:
                            assignment.state === "triggered"
                              ? severityColor(assignment.severity)
                              : assignment.state === "stale_data"
                                ? "#93c5fd"
                                : "#cbd5e1",
                          borderColor:
                            assignment.state === "triggered"
                              ? severityColor(assignment.severity)
                              : assignment.state === "stale_data"
                                ? "#93c5fd"
                                : "rgba(148,163,184,0.35)",
                        }}
                      />
                      {assignment.override ? (
                        <Chip size="small" label={assignment.override.state} variant="outlined" sx={{ color: "#fcd34d", borderColor: "#fcd34d" }} />
                      ) : null}
                    </Stack>
                    <Stack direction="row" spacing={1}>
                      <Button
                        size="small"
                        startIcon={assignment.override ? <ClearIcon /> : <SuppressIcon />}
                        onClick={() =>
                          saveOverride({
                            watchdogId: assignment.watchdog_id,
                            clear: Boolean(assignment.override),
                          })
                        }
                        disabled={actionBusy}
                      >
                        {assignment.override ? "Clear Suppression" : "Suppress"}
                      </Button>
                      <Button
                        size="small"
                        startIcon={<OpenIcon />}
                        onClick={() => navigate(APP_PATHS.watchdog(assignment.watchdog_id))}
                      >
                        Open Policy
                      </Button>
                    </Stack>
                  </Stack>
                  {assignment.description ? (
                    <Typography variant="body2" sx={{ color: "#cbd5e1" }}>
                      {assignment.description}
                    </Typography>
                  ) : null}
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    Rules: {assignment.rule_summaries?.length ? assignment.rule_summaries.join(" | ") : "No rule summary available."}
                  </Typography>
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    Actions: {assignment.action_summaries?.length ? assignment.action_summaries.join(" | ") : "No actions configured."}
                  </Typography>
                  <Typography variant="body2" sx={{ color: "#7dd3fc" }}>
                    {summarizeRuleResults(assignment.sample) || "No sampled evaluation details yet."}
                  </Typography>
                  <Typography variant="caption" sx={{ color: "#94a3b8" }}>
                    Last evaluated {formatTimestamp(assignment.last_evaluated_at)}
                  </Typography>
                </Stack>
              </ItemShell>
            ))}
          </Stack>
        )}
      </SectionShell>

      <SectionShell
        title={`Device Overrides (${payload.overrides.length})`}
        subtitle="Per-device suppressions keep shared watchdog policies intact while you handle exceptions locally."
      >
        {!payload.overrides.length ? (
          <Typography variant="body2" sx={{ color: "#94a3b8" }}>
            No device-specific overrides are active right now.
          </Typography>
        ) : (
          <Stack spacing={1.25}>
            {payload.overrides.map((override, index) => (
              <React.Fragment key={`${override.watchdog_id}-${override.id || index}`}>
                <ItemShell tone="warning">
                  <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" spacing={1}>
                    <Box>
                      <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>
                        {override.watchdog_name || `Watchdog ${override.watchdog_id}`}
                      </Typography>
                      <Typography variant="body2" sx={{ color: "#fcd34d" }}>
                        {override.state} by {override.created_by || "operator"}
                      </Typography>
                      <Typography variant="body2" sx={{ color: "#cbd5e1", mt: 0.5 }}>
                        {override.reason || "No reason provided."}
                      </Typography>
                      <Typography variant="caption" sx={{ color: "#94a3b8" }}>
                        Updated {formatTimestamp(override.updated_at)}
                      </Typography>
                    </Box>
                    <Button
                      size="small"
                      startIcon={<ClearIcon />}
                      onClick={() => saveOverride({ watchdogId: override.watchdog_id, clear: true })}
                      disabled={actionBusy}
                    >
                      Clear Override
                    </Button>
                  </Stack>
                </ItemShell>
                {index < payload.overrides.length - 1 ? <Divider sx={{ borderColor: "rgba(148,163,184,0.12)" }} /> : null}
              </React.Fragment>
            ))}
          </Stack>
        )}
      </SectionShell>
    </Stack>
  );
}
