import React, { useMemo } from "react";
import CheckCircleOutlineRoundedIcon from "@mui/icons-material/CheckCircleOutlineRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, LinearProgress, Typography } from "@mui/material";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { MAGIC_UI } from "./Shared.jsx";

export const UNINSTALL_JOB_TERMINAL_STATUSES = new Set(["success", "failed"]);

const UNINSTALL_STATUS_THEME = {
  queued: {
    label: "Queued",
    color: "#8fbfff",
    accent: "rgba(96,165,250,0.18)",
    border: "rgba(96,165,250,0.34)",
    progress: "linear-gradient(90deg, rgba(125,211,252,0.88) 0%, rgba(96,165,250,0.9) 100%)",
    helper: "Borealis queued the uninstall job and is waiting for the device to start it.",
    Icon: AutorenewRoundedIcon,
  },
  running: {
    label: "Running",
    color: "#7dd3fc",
    accent: "rgba(34,211,238,0.16)",
    border: "rgba(34,211,238,0.32)",
    progress: "linear-gradient(90deg, rgba(125,211,252,0.92) 0%, rgba(192,132,252,0.92) 100%)",
    helper: "The device is actively running the uninstall. Borealis will update this panel as new output arrives.",
    Icon: AutorenewRoundedIcon,
  },
  success: {
    label: "Complete",
    color: "#86efac",
    accent: "rgba(34,197,94,0.16)",
    border: "rgba(74,222,128,0.34)",
    progress: "linear-gradient(90deg, rgba(110,255,187,0.92) 0%, rgba(52,211,153,0.94) 100%)",
    helper: "The uninstall finished successfully. Full output remains available in Activity History.",
    Icon: CheckCircleOutlineRoundedIcon,
  },
  failed: {
    label: "Failed",
    color: "#fda4af",
    accent: "rgba(244,63,94,0.16)",
    border: "rgba(251,113,133,0.34)",
    progress: "linear-gradient(90deg, rgba(251,113,133,0.92) 0%, rgba(248,113,113,0.94) 100%)",
    helper: "The uninstall finished with an error. Review the captured output for the exact command log.",
    Icon: ErrorOutlineRoundedIcon,
  },
};

function formatTimestamp(epochSec) {
  const ts = Number(epochSec || 0);
  if (!ts) return "";
  const date = new Date(ts * 1000);
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  const yyyy = date.getFullYear();
  let hh = date.getHours();
  const ampm = hh >= 12 ? "PM" : "AM";
  hh = hh % 12 || 12;
  const min = String(date.getMinutes()).padStart(2, "0");
  return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
}

export function normalizeTrackedUninstallStatus(rawStatus = "") {
  const normalized = String(rawStatus || "").trim().toLowerCase();
  if (!normalized) return "Queued";
  if (normalized === "queued" || normalized === "pending" || normalized === "created") return "Queued";
  if (normalized === "running" || normalized === "started" || normalized === "in_progress") return "Running";
  if (normalized === "success" || normalized === "completed" || normalized === "complete") return "Success";
  if (normalized === "failed" || normalized === "error" || normalized === "timeout" || normalized === "timed_out") {
    return "Failed";
  }
  return normalized.charAt(0).toUpperCase() + normalized.slice(1);
}

export function isTrackedUninstallTerminal(status = "") {
  return UNINSTALL_JOB_TERMINAL_STATUSES.has(String(status || "").trim().toLowerCase());
}

export function summarizeUninstallOutput(job = {}) {
  const stdoutLines = String(job?.stdout || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const stderrLines = String(job?.stderr || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const detail =
    String(job?.status || "").trim().toLowerCase() === "failed"
      ? stderrLines[0] || stdoutLines[stdoutLines.length - 1] || ""
      : stdoutLines[stdoutLines.length - 1] || stderrLines[0] || "";
  return detail.length > 220 ? `${detail.slice(0, 217)}...` : detail;
}

export function mergeTrackedUninstall(previousJob = {}, patch = {}) {
  const nextPatch = patch && typeof patch === "object" ? patch : {};
  return {
    ...previousJob,
    ...nextPatch,
    status: normalizeTrackedUninstallStatus(nextPatch.status ?? previousJob.status),
    stdout: nextPatch.stdout == null ? String(previousJob.stdout || "") : String(nextPatch.stdout || ""),
    stderr: nextPatch.stderr == null ? String(previousJob.stderr || "") : String(nextPatch.stderr || ""),
    updatedAt: Date.now(),
  };
}

function getTrackedUninstallTheme(status = "") {
  const normalized = String(status || "").trim().toLowerCase();
  return UNINSTALL_STATUS_THEME[normalized] || UNINSTALL_STATUS_THEME.queued;
}

function getTrackedUninstallProgressVariant(status = "") {
  return isTrackedUninstallTerminal(status) ? "determinate" : "indeterminate";
}

function getTrackedUninstallProgressValue(status = "") {
  if (!isTrackedUninstallTerminal(status)) return undefined;
  return 100;
}

function OutputPanel({ title, content = "", tone = "stdout" }) {
  if (!String(content || "").trim()) return null;
  const accent = tone === "stderr" ? "#fda4af" : "#8fbfff";
  return (
    <Box
      sx={{
        borderRadius: 2,
        border: `1px solid ${tone === "stderr" ? "rgba(251,113,133,0.28)" : MAGIC_UI.panelBorder}`,
        background: "rgba(4,7,17,0.72)",
        minHeight: 120,
        maxHeight: 240,
        overflow: "auto",
      }}
    >
      <Box
        sx={{
          px: 1.4,
          py: 0.9,
          borderBottom: `1px solid ${tone === "stderr" ? "rgba(251,113,133,0.22)" : "rgba(148,163,184,0.18)"}`,
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: 1,
        }}
      >
        <Typography sx={{ color: accent, fontWeight: 700, fontSize: "0.84rem" }}>{title}</Typography>
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.72rem" }}>
          {String(content || "").split(/\r?\n/).filter(Boolean).length} line(s)
        </Typography>
      </Box>
      <Box
        component="pre"
        sx={{
          m: 0,
          p: 1.4,
          color: "#dbeafe",
          fontSize: "0.74rem",
          lineHeight: 1.5,
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
          fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
        }}
      >
        {String(content || "")}
      </Box>
    </Box>
  );
}

export default function UninstallProgressDialog({ open = false, job = null, onClose }) {
  const theme = useMemo(() => getTrackedUninstallTheme(job?.status), [job?.status]);
  const progressVariant = useMemo(() => getTrackedUninstallProgressVariant(job?.status), [job?.status]);
  const progressValue = useMemo(() => getTrackedUninstallProgressValue(job?.status), [job?.status]);
  const commandPreview = String(job?.commandPreview || "").trim();
  const subtitle = job
    ? `${theme.helper}${job?.hostname ? ` Device: ${job.hostname}.` : ""}`
    : "Borealis is tracking the uninstall job.";
  const Icon = theme.Icon;
  const metadataLines = [
    { label: "Queued", value: formatTimestamp(job?.ranAt || job?.queuedAt) },
    { label: "Started", value: formatTimestamp(job?.startedAt) },
    { label: "Updated", value: formatTimestamp(job?.updatedAt) },
    { label: "Finished", value: formatTimestamp(job?.finishedAt) },
    { label: "Lane", value: String(job?.queueLane || "").replace(/_/g, " ") },
  ].filter((item) => String(item.value || "").trim());

  return (
    <Dialog open={Boolean(open && job)} onClose={onClose} fullWidth maxWidth="md" PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock
          title={job ? `Uninstall - ${job.softwareName || "Software"}` : "Software Uninstall"}
          subtitle={subtitle}
        />
      </DialogTitle>
      <DialogContent sx={DIALOG_CONTENT_SX}>
        {job ? (
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
            <Box
              sx={{
                p: 1.8,
                borderRadius: 2.5,
                border: `1px solid ${theme.border}`,
                background: theme.accent,
                display: "flex",
                flexDirection: "column",
                gap: 1.35,
              }}
            >
              <Box sx={{ display: "flex", justifyContent: "space-between", gap: 2, flexWrap: "wrap", alignItems: "center" }}>
                <Box sx={{ display: "flex", alignItems: "center", gap: 1.1, minWidth: 0 }}>
                  <Icon sx={{ color: theme.color }} />
                  <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                    {job.softwareName || "Software"}
                    {job.softwareVersion ? ` ${job.softwareVersion}` : ""}
                  </Typography>
                </Box>
                <Box
                  sx={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 1,
                    px: 1.2,
                    py: 0.55,
                    borderRadius: 999,
                    border: `1px solid ${theme.border}`,
                    color: theme.color,
                    background: "rgba(4,7,17,0.4)",
                    fontWeight: 700,
                    fontSize: "0.8rem",
                  }}
                >
                  {theme.label}
                </Box>
              </Box>
              <LinearProgress
                variant={progressVariant}
                value={progressValue}
                sx={{
                  height: 9,
                  borderRadius: 999,
                  backgroundColor: "rgba(148,163,184,0.16)",
                  overflow: "hidden",
                  "& .MuiLinearProgress-bar": {
                    borderRadius: 999,
                    backgroundImage: theme.progress,
                  },
                }}
              />
              <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1.1 }}>
                <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem" }}>
                  Job #{job.jobId}
                </Typography>
                {metadataLines.map((item) => (
                  <Typography key={item.label} sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem" }}>
                    {item.label}: {item.value}
                  </Typography>
                ))}
              </Box>
            </Box>
            {commandPreview ? (
              <Box
                sx={{
                  borderRadius: 2,
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  background: "rgba(4,7,17,0.7)",
                  p: 1.4,
                }}
              >
                <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.76rem", mb: 0.75 }}>
                  Command Preview
                </Typography>
                <Box
                  component="code"
                  sx={{
                    display: "block",
                    color: "#cfe8ff",
                    fontSize: "0.74rem",
                    lineHeight: 1.45,
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-word",
                    fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
                  }}
                >
                  {commandPreview}
                </Box>
              </Box>
            ) : null}
            <OutputPanel title="StdOut" content={job.stdout} tone="stdout" />
            <OutputPanel title="StdErr" content={job.stderr} tone="stderr" />
            {!String(job.stdout || "").trim() && !String(job.stderr || "").trim() ? (
              <Box
                sx={{
                  borderRadius: 2,
                  border: `1px dashed ${MAGIC_UI.panelBorder}`,
                  p: 1.5,
                  color: MAGIC_UI.textMuted,
                  fontSize: "0.82rem",
                }}
              >
                No output has been captured yet for this uninstall task.
              </Box>
            ) : null}
          </Box>
        ) : null}
      </DialogContent>
      <DialogActions
        sx={{
          ...DIALOG_ACTIONS_SX,
          justifyContent: "space-between",
          alignItems: "center",
          gap: 1.25,
        }}
      >
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", minWidth: 0 }}>
          {job ? summarizeUninstallOutput(job) || theme.helper : ""}
        </Typography>
        <Button sx={DIALOG_BUTTON_SX} onClick={onClose}>
          {isTrackedUninstallTerminal(job?.status) ? "Close" : "Hide"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
