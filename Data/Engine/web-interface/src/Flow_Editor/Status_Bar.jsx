import React, { useMemo } from "react";
import { Box, Chip, Divider, Typography } from "@mui/material";

const STATUS_TONES = {
  Pending: { bg: "rgba(71, 85, 105, 0.26)", border: "rgba(148, 163, 184, 0.35)", text: "#cbd5e1" },
  Running: { bg: "rgba(14, 116, 144, 0.24)", border: "rgba(125, 211, 252, 0.35)", text: "#7dd3fc" },
  Success: { bg: "rgba(6, 78, 59, 0.24)", border: "rgba(52, 211, 153, 0.35)", text: "#34d399" },
  Warning: { bg: "rgba(120, 53, 15, 0.24)", border: "rgba(251, 191, 36, 0.35)", text: "#fbbf24" },
  Failed: { bg: "rgba(127, 29, 29, 0.24)", border: "rgba(252, 165, 165, 0.35)", text: "#fca5a5" },
  "Timed Out": { bg: "rgba(88, 28, 135, 0.24)", border: "rgba(216, 180, 254, 0.35)", text: "#d8b4fe" },
  Skipped: { bg: "rgba(30, 41, 59, 0.26)", border: "rgba(148, 163, 184, 0.3)", text: "#94a3b8" },
};

function formatTs(value) {
  if (!value) return "—";
  try {
    return new Date(Number(value) * 1000).toLocaleString();
  } catch {
    return "—";
  }
}

export default function StatusBar({
  nodeCount = 0,
  mode = "editor",
  workflowRun = null,
}) {
  const statusTone = STATUS_TONES[String(workflowRun?.status || "").trim()] || null;
  const modeLabel = mode === "run" ? "Run Snapshot" : "Authoring";
  const sourceLabel = useMemo(() => {
    const raw = String(workflowRun?.source_type || "").trim();
    if (!raw) return "Manual";
    return raw.replace(/_/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
  }, [workflowRun?.source_type]);

  return (
    <Box
      component="footer"
      sx={{
        bgcolor: "#08101f",
        color: "white",
        px: 2,
        py: 1,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 2,
        borderTop: "1px solid rgba(148,163,184,0.18)",
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
        <Typography sx={{ fontSize: "0.82rem", color: "#cbd5e1" }}>
          <strong style={{ color: "#7dd3fc" }}>Mode</strong>: {modeLabel}
        </Typography>
        <Divider orientation="vertical" flexItem sx={{ borderColor: "rgba(148,163,184,0.2)" }} />
        <Typography sx={{ fontSize: "0.82rem", color: "#cbd5e1" }}>
          <strong style={{ color: "#7dd3fc" }}>Nodes</strong>: {nodeCount}
        </Typography>
        {mode === "run" && workflowRun ? (
          <>
            <Divider orientation="vertical" flexItem sx={{ borderColor: "rgba(148,163,184,0.2)" }} />
            <Typography sx={{ fontSize: "0.82rem", color: "#cbd5e1" }}>
              <strong style={{ color: "#7dd3fc" }}>Source</strong>: {sourceLabel}
            </Typography>
            <Chip
              size="small"
              label={workflowRun.status || "Pending"}
              sx={{
                height: 24,
                fontSize: "0.7rem",
                fontWeight: 700,
                borderRadius: 999,
                bgcolor: statusTone?.bg || "rgba(30,41,59,0.26)",
                border: `1px solid ${statusTone?.border || "rgba(148,163,184,0.24)"}`,
                color: statusTone?.text || "#cbd5e1",
              }}
            />
            <Typography sx={{ fontSize: "0.78rem", color: "#94a3b8" }}>
              Started: {formatTs(workflowRun.started_ts || workflowRun.created_at)}
            </Typography>
            <Typography sx={{ fontSize: "0.78rem", color: "#94a3b8" }}>
              Finished: {formatTs(workflowRun.finished_ts)}
            </Typography>
          </>
        ) : null}
      </Box>
    </Box>
  );
}
