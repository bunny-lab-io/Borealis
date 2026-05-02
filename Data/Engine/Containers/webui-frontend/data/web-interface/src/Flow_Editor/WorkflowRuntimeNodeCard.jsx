import React from "react";
import { Handle, Position } from "reactflow";
import { Box, Chip, Typography } from "@mui/material";
import {
  getWorkflowRuntimePorts,
  getWorkflowRuntimeDisplayLabel,
  normalizeWorkflowStatus,
  WORKFLOW_RUNTIME_PORT_DIRECTIONS,
  WORKFLOW_RUNTIME_PORT_KINDS,
} from "./runtimeV1";

const STATUS_TONES = {
  Pending: { border: "rgba(148, 163, 184, 0.45)", bg: "rgba(71, 85, 105, 0.18)", text: "#cbd5e1" },
  Running: { border: "rgba(125, 211, 252, 0.45)", bg: "rgba(14, 116, 144, 0.2)", text: "#7dd3fc" },
  Success: { border: "rgba(16, 185, 129, 0.45)", bg: "rgba(6, 78, 59, 0.22)", text: "#34d399" },
  Warning: { border: "rgba(245, 158, 11, 0.45)", bg: "rgba(120, 53, 15, 0.22)", text: "#fbbf24" },
  Failed: { border: "rgba(248, 113, 113, 0.45)", bg: "rgba(127, 29, 29, 0.24)", text: "#fca5a5" },
  "Timed Out": { border: "rgba(192, 132, 252, 0.45)", bg: "rgba(88, 28, 135, 0.24)", text: "#d8b4fe" },
  Skipped: { border: "rgba(148, 163, 184, 0.35)", bg: "rgba(30, 41, 59, 0.2)", text: "#94a3b8" },
};

const HEADER_HEIGHT = 30;
const PORT_ROW_HEIGHT = 18;
const PORT_SECTION_PADDING_TOP = 24;
const PORT_SECTION_PADDING_BOTTOM = 6;
const WORKFLOW_PORT_HANDLE_SIZE = 12;
const PORT_HANDLE_VERTICAL_OFFSET = 1;

function portHandleTop(index, portCount, bodyHeight) {
  return (
    HEADER_HEIGHT +
    bodyHeight -
    PORT_SECTION_PADDING_BOTTOM -
    (portCount - index - 0.5) * PORT_ROW_HEIGHT +
    PORT_HANDLE_VERTICAL_OFFSET
  );
}

function portRowTop(index, portCount, bodyHeight) {
  return (
    bodyHeight -
    PORT_SECTION_PADDING_BOTTOM -
    (portCount - index) * PORT_ROW_HEIGHT +
    PORT_HANDLE_VERTICAL_OFFSET
  );
}

function portTone(port) {
  if (port?.kind === WORKFLOW_RUNTIME_PORT_KINDS.action) {
    return {
      text: "#dbeafe",
    };
  }
  return {
    text: "#cbd5e1",
  };
}

function portHandleStyle(direction) {
  const isInput = direction === WORKFLOW_RUNTIME_PORT_DIRECTIONS.input;
  return {
    top: 0,
    left: isInput ? 0 : "auto",
    right: isInput ? "auto" : 0,
    width: WORKFLOW_PORT_HANDLE_SIZE,
    height: WORKFLOW_PORT_HANDLE_SIZE,
    borderRadius: "50%",
    boxSizing: "border-box",
    border: "1.5px solid rgba(125, 211, 252, 0.92)",
    background: "rgba(8, 17, 31, 0.98)",
    boxShadow:
      "0 0 0 2px rgba(8, 17, 31, 0.98), 0 0 0 1px rgba(88, 166, 255, 0.26)",
    zIndex: 8,
    transform: isInput ? "translate(-50%, -50%)" : "translate(50%, -50%)",
  };
}

export default function WorkflowRuntimeNodeCard({
  data,
  title,
  icon,
  nodeType,
}) {
  const status =
    normalizeWorkflowStatus(
      data?.node_execution_status ||
        data?.executionStatus ||
        data?.runtimeStatus ||
        data?.runtime?.status
    ) || "";
  const badgeLabel = String(data?.node_status_badge_label || data?.status_badge_label || status || "").trim();
  const tone = STATUS_TONES[status] || null;
  const accent = String(data?.accentColor || "#7dd3fc");
  const inputPorts = getWorkflowRuntimePorts(nodeType, WORKFLOW_RUNTIME_PORT_DIRECTIONS.input);
  const outputPorts = getWorkflowRuntimePorts(nodeType, WORKFLOW_RUNTIME_PORT_DIRECTIONS.output);
  const rowCount = Math.max(inputPorts.length, outputPorts.length, 1);
  const bodyHeight = PORT_SECTION_PADDING_TOP + PORT_SECTION_PADDING_BOTTOM + rowCount * PORT_ROW_HEIGHT;
  const displayTitle = getWorkflowRuntimeDisplayLabel(nodeType, data?.label || title);

  return (
    <Box
      sx={{
        minWidth: 228,
        maxWidth: 296,
        borderRadius: "10px",
        border: `1px solid ${tone?.border || "rgba(125, 211, 252, 0.22)"}`,
        background:
          "radial-gradient(110% 120% at 0% 0%, rgba(125, 211, 252, 0.14), transparent 52%), " +
          "radial-gradient(110% 120% at 100% 0%, rgba(192, 132, 252, 0.14), transparent 56%), " +
          "rgba(9, 14, 27, 0.96)",
        color: "#e2e8f0",
        boxShadow: tone
          ? `0 0 0 1px ${tone.border} inset, 0 18px 36px rgba(2, 8, 23, 0.34)`
          : "0 18px 36px rgba(2, 8, 23, 0.34)",
        position: "relative",
        overflow: "visible",
      }}
    >
      <Box
        className="borealis-node-header"
        sx={{
          px: 1.45,
          height: HEADER_HEIGHT,
          borderBottom: "1px solid rgba(148, 163, 184, 0.12)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1,
          background:
            `linear-gradient(135deg, ${accent}22 0%, rgba(15, 23, 42, 0.35) 65%, rgba(15, 23, 42, 0.55) 100%)`,
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.85, minWidth: 0, ml: "-5px" }}>
          <Box sx={{ color: accent, display: "inline-flex", alignItems: "center" }}>{icon}</Box>
          <Typography
            sx={{
              fontSize: "0.7rem",
              fontWeight: 400,
              color: "#f8fafc",
              lineHeight: 1.15,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {displayTitle}
          </Typography>
        </Box>
        {badgeLabel ? (
          <Chip
            size="small"
            label={badgeLabel}
            sx={{
              height: 16,
              fontSize: "0.42rem",
              fontWeight: 600,
              borderRadius: 999,
              border: `1px solid ${tone?.border || "rgba(148, 163, 184, 0.35)"}`,
              bgcolor: tone?.bg || "rgba(30, 41, 59, 0.25)",
              color: tone?.text || "#cbd5e1",
              "& .MuiChip-label": {
                px: 0.95,
              },
            }}
          />
        ) : null}
      </Box>

      <Box sx={{ px: 1.25, height: `${bodyHeight}px`, position: "relative" }}>
        {Array.from({ length: Math.max(0, rowCount - 1) }).map((_, index) => (
          <Box
            key={`separator-${index}`}
            sx={{
              position: "absolute",
              left: 0,
              right: 0,
              top: `${PORT_SECTION_PADDING_TOP + (index + 1) * PORT_ROW_HEIGHT}px`,
              borderTop: "1px solid rgba(148, 163, 184, 0.07)",
            }}
          />
        ))}

        <Box
          sx={{
            position: "absolute",
            left: 0,
            top: 0,
            minWidth: 0,
            width: "calc(50% - 14px)",
          }}
        >
          {inputPorts.map((inputPort, index) => {
            const inputTone = portTone(inputPort);
            return (
              <Box
                key={`input-label-${inputPort.id}`}
                sx={{
                  position: "absolute",
                  top: `${portRowTop(index, inputPorts.length, bodyHeight)}px`,
                  left: 0,
                  right: 0,
                  height: PORT_ROW_HEIGHT,
                  display: "flex",
                  alignItems: "center",
                  minWidth: 0,
                  pr: 2.25,
                  pl: 1.35,
                }}
              >
                <Typography
                  sx={{
                    fontSize: "0.58rem",
                    fontWeight: 500,
                    color: inputTone.text,
                    lineHeight: 1.1,
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {inputPort.label}
                </Typography>
              </Box>
            );
          })}
        </Box>

        <Box
          sx={{
            position: "absolute",
            right: 0,
            top: 0,
            minWidth: 0,
            width: "calc(50% - 14px)",
          }}
        >
          {outputPorts.map((outputPort, index) => {
            const outputTone = portTone(outputPort);
            return (
              <Box
                key={`output-label-${outputPort.id}`}
                sx={{
                  position: "absolute",
                  top: `${portRowTop(index, outputPorts.length, bodyHeight)}px`,
                  left: 0,
                  right: 0,
                  height: PORT_ROW_HEIGHT,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "flex-end",
                  minWidth: 0,
                  pl: 2.25,
                  pr: 1.35,
                }}
              >
                <Typography
                  sx={{
                    fontSize: "0.58rem",
                    fontWeight: 500,
                    color: outputTone.text,
                    lineHeight: 1.1,
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {outputPort.label}
                </Typography>
              </Box>
            );
          })}
        </Box>
      </Box>

      {inputPorts.map((port, index) => (
        <Handle
          key={`target-${port.id}`}
          id={port.id}
          type="target"
          position={Position.Left}
          className="borealis-handle"
          style={{
            ...portHandleStyle(WORKFLOW_RUNTIME_PORT_DIRECTIONS.input),
            top: portHandleTop(index, inputPorts.length, bodyHeight),
          }}
        />
      ))}
      {outputPorts.map((port, index) => (
        <Handle
          key={`source-${port.id}`}
          id={port.id}
          type="source"
          position={Position.Right}
          className="borealis-handle"
          style={{
            ...portHandleStyle(WORKFLOW_RUNTIME_PORT_DIRECTIONS.output),
            top: portHandleTop(index, outputPorts.length, bodyHeight),
          }}
        />
      ))}
    </Box>
  );
}
