import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Accordion,
  AccordionDetails,
  AccordionSummary,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  ListItemButton,
  ListItemText,
  Slider,
  Switch,
  LinearProgress,
  Stack,
  Typography,
} from "@mui/material";
import {
  DesktopWindowsRounded as DesktopIcon,
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  KeyboardRounded as KeyboardIcon,
  ContentPasteRounded as ClipboardIcon,
  ExpandMore as ExpandMoreIcon,
  FitScreenRounded as FitScreenIcon,
  SwapHorizRounded as SwapHorizIcon,
  KeyboardCommandKeyRounded as KeyboardCommandKeyIcon,
  ChevronLeft as ChevronLeftIcon,
  CheckCircleRounded as StageCompleteIcon,
  AutorenewRounded as StageActiveIcon,
  ErrorOutlineRounded as StageErrorIcon,
  RadioButtonUncheckedRounded as StagePendingIcon,
} from "@mui/icons-material";
import { APP_PATHS } from "../../app/routes/paths.js";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import { useAuth } from "../../app/providers/AuthContext.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import Guacamole from "../../vendor/guacamole/guacamole-common-js.js";

const MAGIC_UI = {
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
  accentD: "#f472b6",
};

const VNC_STAGE_BACKGROUND = "#0b1325";
const VNC_CANVAS_BOX_SHADOW =
  "0 0 0 1px rgba(125, 183, 255, 0.18), 0 18px 42px rgba(2, 6, 23, 0.4)";
const VNC_OPERATOR_CURSOR = "default";
const VIEWFINDER_DEFAULT_WIDTH = 202;
const VIEWFINDER_DEFAULT_HEIGHT = 106;
const VIEWFINDER_MIN_MEASURED_SIZE = 24;

const SIDEBAR_THEME = {
  panel:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.95) 60%, rgba(15, 6, 26, 0.97) 100%)",
  border: "rgba(148, 163, 184, 0.28)",
  text: "#e2e8f0",
  muted: "#94a3b8",
  accent: "#7db7ff",
};

const NAV_COLORS = {
  cyan: "#7db7ff",
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  line: "rgba(125,183,255,0.14)",
  hover: "rgba(255,255,255,0.05)",
  itemActiveBg:
    "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const simpleButtonSx = {
  textTransform: "none",
  fontWeight: 600,
  borderRadius: 1,
  borderColor: SIDEBAR_THEME.border,
  color: SIDEBAR_THEME.text,
  backgroundColor: "rgba(8,15,32,0.85)",
  "&:hover": {
    borderColor: "rgba(125,183,255,0.5)",
    backgroundColor: "rgba(12,18,36,0.9)",
  },
};

const sidebarSx = {
  minWidth: 260,
  maxWidth: 260,
  width: { xs: "100%", lg: 260 },
  borderRadius: 2.5,
  border: `1px solid ${NAV_COLORS.line}`,
  background:
    "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), #0f141c",
  boxShadow: "none",
  p: 0.25,
  display: "flex",
  flexDirection: "column",
  gap: 0.25,
  overflow: "auto",
};

const sidebarAccordionSx = {
  "&:before": { display: "none" },
  m: 0,
  bgcolor: "transparent",
  border: 0,
  boxShadow: "none",
};

const sidebarAccordionSummarySx = {
  minHeight: 38,
  px: 1.5,
  backgroundColor: "rgba(255,255,255,0.02)",
  borderTopRightRadius: 8,
  borderBottomRightRadius: 8,
  "& .MuiAccordionSummary-content": {
    m: 0,
    py: 0.5,
    display: "flex",
  },
  "&.Mui-expanded": {
    minHeight: 38,
  },
  "& .MuiAccordionSummary-content.Mui-expanded": {
    my: 0,
  },
};

const sidebarAccordionDetailsSx = {
  p: 0,
};

const glassCardSx = {
  borderRadius: 3,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background:
    "linear-gradient(145deg, rgba(8,12,24,0.94), rgba(10,16,30,0.9)), radial-gradient(circle at 20% 20%, rgba(125,211,252,0.08), transparent 35%)",
  boxShadow: "0 25px 80px rgba(2,6,23,0.6)",
};

const pillActionSx = {
  ...simpleButtonSx,
  minHeight: 38,
  px: 1.8,
  borderRadius: 999,
  backgroundColor: "rgba(9,14,25,0.88)",
  backdropFilter: "blur(12px)",
};

const primaryHeroActionSx = {
  ...pillActionSx,
  color: "#08111f",
  "&, & .MuiButton-startIcon": {
    color: "#08111f",
  },
  borderColor: "transparent",
  backgroundImage: "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)",
  boxShadow: "0 10px 30px rgba(91,126,255,0.25)",
  "&:hover": {
    borderColor: "transparent",
    backgroundImage: "linear-gradient(135deg,#95dcff 0%,#c1a9ff 100%)",
    backgroundColor: "transparent",
  },
};

function sidebarNavRowSx(active, disabled = false) {
  return {
    pl: 2,
    pr: 2,
    py: 1,
    color: active ? NAV_COLORS.textActive : NAV_COLORS.text,
    position: "relative",
    background: active ? NAV_COLORS.itemActiveBg : "transparent",
    borderTopRightRadius: 0,
    borderBottomRightRadius: 0,
    justifyContent: "space-between",
    transition:
      "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "&:hover": {
      background: active ? NAV_COLORS.itemActiveBg : NAV_COLORS.hover,
    },
    "&:active": {
      transform: "translateY(0.5px)",
    },
    "&.Mui-disabled": {
      color: "rgba(203,213,225,0.38)",
    },
    ...(disabled
      ? {
          cursor: "not-allowed",
        }
      : null),
  };
}

function sidebarNavIconSx(active, disabled = false) {
  return {
    mr: 1,
    display: "flex",
    alignItems: "center",
    color: disabled ? "rgba(143,191,255,0.35)" : active ? NAV_COLORS.cyan : "#8fbfff",
    transition: "color 160ms ease",
  };
}

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function summarizeStatus(message, limit = 120) {
  if (!message) return "";
  const text = String(message).trim();
  if (text.length <= limit) return text;
  const sliceLimit = Math.max(0, limit - 3);
  return `${text.slice(0, sliceLimit)}...`;
}

function buildWsUrl(wsPath, token) {
  const pathText = normalizeText(wsPath);
  if (!pathText || typeof window === "undefined") return "";
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const parsed = new URL(pathText, `${protocol}//${window.location.host}`);
  if (token && !parsed.searchParams.get("token")) {
    parsed.searchParams.set("token", token);
  }
  return parsed.toString();
}

function buildGuacamoleWsCandidates(data) {
  const candidates = [];
  const addCandidate = (value) => {
    let candidate = "";
    try {
      candidate = buildWsUrl(value, "");
    } catch {
      candidate = "";
    }
    if (candidate && !candidates.includes(candidate)) {
      candidates.push(candidate);
    }
  };

  addCandidate(data?.guacamole_ws_url);
  addCandidate(data?.guacamole_ws_path);

  const routePrefix = normalizeText(data?.remote_ops_session?.worker?.route_path_prefix);
  if (routePrefix) {
    const normalizedPrefix = routePrefix.startsWith("/") ? routePrefix : `/${routePrefix}`;
    addCandidate(`${normalizedPrefix.replace(/\/$/, "")}/remote-desktop/vnc/guacamole`);
  }

  return candidates;
}

function keysymFromChar(char) {
  if (!char) return null;
  if (char === "\n" || char === "\r") return 0xff0d;
  if (char === "\t") return 0xff09;
  if (char === "\b") return 0xff08;
  if (char === "\x1b") return 0xff1b;
  const code = char.codePointAt(0);
  if (!code) return null;
  return code;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function buildRetryableError(message, retryable = true) {
  const error = message instanceof Error ? message : new Error(String(message || "Request failed."));
  error.retryable = retryable;
  return error;
}

const VNC_AUTO_RETRY_ATTEMPTS = 3;
const VNC_AUTO_RETRY_DELAY_MS = 1500;
const VNC_SESSION_RECONNECT_ATTEMPTS = 3;
const VNC_SESSION_RECONNECT_DELAY_MS = 1500;
const VNC_OPEN_TIMEOUT_MS = 90000;
const VNC_WS_OPEN_TIMEOUT_MS = 20000;
const VNC_READY_STABILIZE_MS = 1800;
const VNC_IDLE_WARNING_MS = 5 * 60 * 1000;
const VNC_IDLE_DISCONNECT_MS = 6 * 60 * 1000;
const VNC_TUNNEL_UNSTABLE_THRESHOLD_MS = VNC_IDLE_WARNING_MS;
const VNC_TUNNEL_RECEIVE_TIMEOUT_MS = VNC_IDLE_DISCONNECT_MS;
const ALL_DISPLAYS_ID = "__all_displays__";
const REMOTE_DESKTOP_PERFORMANCE_STORAGE_KEY = "borealis_remote_desktop_performance_preference_v2";
const PERFORMANCE_PREFERENCE_MIN = -2;
const PERFORMANCE_PREFERENCE_MAX = 2;
const PERFORMANCE_PREFERENCE_DEFAULT = PERFORMANCE_PREFERENCE_MIN;
const DISPLAY_INFERENCE_MIN_WIDTH = 640;
const DISPLAY_INFERENCE_WIDE_RATIO = 4.1;
const DISPLAY_INFERENCE_MAX_ASPECT_ERROR = 0.08;
const DISPLAY_INFERENCE_ASPECT_PRIORS = Object.freeze([16 / 9, 16 / 10, 21 / 9, 32 / 9, 4]);
const DISPLAY_INFERENCE_HEIGHT_FRACTIONS = Object.freeze([1, 0.8, 0.75, 2 / 3]);
const CONNECTION_FLOW_STEPS = Object.freeze([
  {
    id: "tunnel",
    label: "Preparing Agent Session",
    detail: "Requesting live VNC credentials and the WireGuard route.",
  },
  {
    id: "service",
    label: "Starting Agent VNC",
    detail: "Waiting for the Agent to finish VNC service readiness.",
  },
  {
    id: "socket",
    label: "Opening Browser Socket",
    detail: "Connecting the browser to the site-worker socket.",
  },
  {
    id: "guacamole",
    label: "Opening Guacamole",
    detail: "Connecting Guacamole to the Agent VNC listener.",
  },
  {
    id: "frame",
    label: "Waiting for Desktop Frame",
    detail: "Waiting for the first desktop framebuffer update.",
  },
  {
    id: "ready",
    label: "Desktop Ready",
    detail: "Finalizing the live desktop stream for control.",
  },
]);

function normalizePerformancePreference(value) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed)) return PERFORMANCE_PREFERENCE_DEFAULT;
  return Math.max(PERFORMANCE_PREFERENCE_MIN, Math.min(PERFORMANCE_PREFERENCE_MAX, parsed));
}

function sendGuacamoleKeysym(client, keysym) {
  if (!client || keysym == null || typeof client.sendKeyEvent !== "function") return;
  client.sendKeyEvent(1, keysym);
  client.sendKeyEvent(0, keysym);
}

async function writeClipboardText(text) {
  if (!navigator?.clipboard?.writeText) return;
  try {
    await navigator.clipboard.writeText(text || "");
  } catch {
    // ignore
  }
}

function capabilityFlag(value) {
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  const text = normalizeText(value).toLowerCase();
  if (!text) return false;
  return ["1", "true", "yes", "on", "supported", "available"].includes(text);
}

function coerceInteger(value, defaultValue = 0) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : defaultValue;
}

function clampNumber(value, min, max) {
  if (!Number.isFinite(value)) return min;
  if (max < min) return min;
  return Math.min(Math.max(value, min), max);
}

function normalizeDisplayTopology(value) {
  if (typeof value === "string") {
    try {
      return normalizeDisplayTopology(JSON.parse(value));
    } catch {
      return [];
    }
  }
  if (!Array.isArray(value)) return [];
  return value
    .filter((item) => item && typeof item === "object")
    .map((item, index) => {
      const left = coerceInteger(item.left, 0);
      const top = coerceInteger(item.top, 0);
      const right = coerceInteger(item.right, left + coerceInteger(item.width, 0));
      const bottom = coerceInteger(item.bottom, top + coerceInteger(item.height, 0));
      const width = Math.max(0, coerceInteger(item.width, right - left));
      const height = Math.max(0, coerceInteger(item.height, bottom - top));
      const displayIndex = Math.max(
        1,
        coerceInteger(item.display_index ?? item.id ?? item.label, index + 1)
      );
      return {
        id: normalizeText(item.id || displayIndex) || String(displayIndex),
        displayIndex,
        label: normalizeText(item.label || displayIndex) || String(displayIndex),
        deviceName: normalizeText(item.device_name),
        left,
        top,
        right: right > left ? right : left + width,
        bottom: bottom > top ? bottom : top + height,
        width,
        height,
        primary: capabilityFlag(item.primary),
      };
    })
    .filter((item) => item.width > 0 && item.height > 0)
    .sort((left, right) => {
      if (left.displayIndex !== right.displayIndex) return left.displayIndex - right.displayIndex;
      if (left.primary !== right.primary) return left.primary ? -1 : 1;
      if (left.top !== right.top) return left.top - right.top;
      return left.left - right.left;
    });
}

function displayTopologyBounds(topology) {
  if (!Array.isArray(topology) || !topology.length) return null;
  const left = Math.min(...topology.map((item) => item.left));
  const top = Math.min(...topology.map((item) => item.top));
  const right = Math.max(...topology.map((item) => item.right));
  const bottom = Math.max(...topology.map((item) => item.bottom));
  return {
    left,
    top,
    right,
    bottom,
    width: Math.max(0, right - left),
    height: Math.max(0, bottom - top),
  };
}

function normalizeDisplayBounds(value) {
  if (typeof value === "string") {
    try {
      return normalizeDisplayBounds(JSON.parse(value));
    } catch {
      return null;
    }
  }
  if (!value || typeof value !== "object") return null;
  const left = coerceInteger(value.left, 0);
  const top = coerceInteger(value.top, 0);
  const width = Math.max(0, coerceInteger(value.width, 0));
  const height = Math.max(0, coerceInteger(value.height, 0));
  const right = coerceInteger(value.right, left + width);
  const bottom = coerceInteger(value.bottom, top + height);
  const resolvedRight = right > left ? right : left + width;
  const resolvedBottom = bottom > top ? bottom : top + height;
  const resolvedWidth = Math.max(width, resolvedRight - left);
  const resolvedHeight = Math.max(height, resolvedBottom - top);
  if (resolvedWidth <= 0 || resolvedHeight <= 0) return null;
  return {
    left,
    top,
    right: left + resolvedWidth,
    bottom: top + resolvedHeight,
    width: resolvedWidth,
    height: resolvedHeight,
  };
}

function topologyMatchesFramebuffer(topologyBounds, framebufferSize) {
  if (!topologyBounds || !framebufferSize?.width || !framebufferSize?.height) return true;
  const widthTolerance = Math.max(96, Math.round(framebufferSize.width * 0.18));
  const heightTolerance = Math.max(96, Math.round(framebufferSize.height * 0.18));
  return (
    Math.abs(topologyBounds.width - framebufferSize.width) <= widthTolerance &&
    Math.abs(topologyBounds.height - framebufferSize.height) <= heightTolerance
  );
}

function equalDisplayBounds(left, right) {
  if (!left || !right) return left === right;
  return (
    left.left === right.left &&
    left.top === right.top &&
    left.right === right.right &&
    left.bottom === right.bottom &&
    left.width === right.width &&
    left.height === right.height
  );
}

function monitorSelectionId(monitor) {
  return normalizeText(monitor?.id || monitor?.displayIndex || "");
}

function equalDisplayTopology(left, right) {
  if (!Array.isArray(left) || !Array.isArray(right)) return false;
  if (left.length !== right.length) return false;
  return left.every((item, index) => {
    const other = right[index];
    if (!other) return false;
    return (
      item.id === other.id &&
      item.displayIndex === other.displayIndex &&
      item.label === other.label &&
      item.deviceName === other.deviceName &&
      item.left === other.left &&
      item.top === other.top &&
      item.right === other.right &&
      item.bottom === other.bottom &&
      item.width === other.width &&
      item.height === other.height &&
      item.primary === other.primary
    );
  });
}

function buildDisplayLayoutGeometry(
  topology,
  { frameWidth = 256, frameHeight = 126, padding = 6, edgeInset = 0 } = {}
) {
  const bounds = displayTopologyBounds(topology);
  if (!bounds || bounds.width <= 0 || bounds.height <= 0) {
    return {
      bounds: null,
      frameWidth,
      frameHeight,
      offsetX: padding + edgeInset,
      offsetY: padding + edgeInset,
      padding,
      edgeInset,
      scale: 1,
      frames: [],
    };
  }
  const insetPadding = padding + edgeInset;
  const innerWidth = Math.max(1, frameWidth - insetPadding * 2);
  const innerHeight = Math.max(1, frameHeight - insetPadding * 2);
  const fitScale = Math.min(innerWidth / bounds.width, innerHeight / bounds.height);
  const scale = Math.max(0.001, fitScale * 0.88);
  const layoutWidth = bounds.width * scale;
  const layoutHeight = bounds.height * scale;
  const offsetX = (frameWidth - layoutWidth) / 2;
  const offsetY = (frameHeight - layoutHeight) / 2;
  const maxRight = frameWidth - insetPadding;
  const maxBottom = frameHeight - insetPadding;
  return {
    bounds,
    frameWidth,
    frameHeight,
    offsetX,
    offsetY,
    padding,
    edgeInset,
    scale,
    frames: topology.map((item) => {
      const rawX = offsetX + (item.left - bounds.left) * scale;
      const rawY = offsetY + (item.top - bounds.top) * scale;
      const x = clampNumber(rawX, insetPadding, Math.max(insetPadding, maxRight - 2));
      const y = clampNumber(rawY, insetPadding, Math.max(insetPadding, maxBottom - 2));
      const widthPx = Math.max(2, Math.min(item.width * scale, Math.max(2, maxRight - x)));
      const heightPx = Math.max(2, Math.min(item.height * scale, Math.max(2, maxBottom - y)));
      return {
        ...item,
        x,
        y,
        widthPx,
        heightPx,
      };
    }),
  };
}

function buildDisplayLayoutFrames(topology, options) {
  return buildDisplayLayoutGeometry(topology, options).frames;
}

function aspectRatioMatches(leftSize, rightSize, tolerance = 0.12) {
  if (!leftSize?.width || !leftSize?.height || !rightSize?.width || !rightSize?.height) {
    return false;
  }
  const leftRatio = Number(leftSize.width) / Number(leftSize.height);
  const rightRatio = Number(rightSize.width) / Number(rightSize.height);
  if (!Number.isFinite(leftRatio) || !Number.isFinite(rightRatio) || rightRatio <= 0) {
    return false;
  }
  return Math.abs(leftRatio - rightRatio) / rightRatio <= tolerance;
}

function closeDisplaySize(value, target, tolerance) {
  return Math.abs(Number(value || 0) - Number(target || 0)) <= tolerance;
}

function inferredDisplayLabel(displayIndex) {
  return String(Math.max(1, Number(displayIndex || 1)));
}

function buildInferredHorizontalTopology(remoteWidth, remoteHeight, split) {
  const leftWidth = clampNumber(
    Math.round(split.leftWidth),
    DISPLAY_INFERENCE_MIN_WIDTH,
    remoteWidth - DISPLAY_INFERENCE_MIN_WIDTH
  );
  const rightWidth = Math.max(DISPLAY_INFERENCE_MIN_WIDTH, remoteWidth - leftWidth);
  const leftHeight = clampNumber(
    Math.round(split.leftHeight || remoteHeight),
    DISPLAY_INFERENCE_MIN_WIDTH,
    remoteHeight
  );
  const rightHeight = clampNumber(
    Math.round(split.rightHeight || remoteHeight),
    DISPLAY_INFERENCE_MIN_WIDTH,
    remoteHeight
  );
  const leftTop = clampNumber(
    Math.round(split.leftTop ?? 0),
    0,
    Math.max(0, remoteHeight - leftHeight)
  );
  const rightTop = clampNumber(
    Math.round(split.rightTop ?? 0),
    0,
    Math.max(0, remoteHeight - rightHeight)
  );
  const leftDisplayIndex = Math.max(1, Number(split.leftDisplayIndex || 1));
  const rightDisplayIndex = Math.max(1, Number(split.rightDisplayIndex || 2));
  const leftLabel = inferredDisplayLabel(leftDisplayIndex);
  const rightLabel = inferredDisplayLabel(rightDisplayIndex);
  return [
    {
      id: leftLabel,
      displayIndex: leftDisplayIndex,
      label: leftLabel,
      deviceName: split.leftDeviceName || `Inferred Display ${leftLabel}`,
      left: 0,
      top: leftTop,
      right: leftWidth,
      bottom: leftTop + leftHeight,
      width: leftWidth,
      height: leftHeight,
      primary: Boolean(split.leftPrimary),
      synthetic: true,
      inferred: true,
    },
    {
      id: rightLabel,
      displayIndex: rightDisplayIndex,
      label: rightLabel,
      deviceName: split.rightDeviceName || `Inferred Display ${rightLabel}`,
      left: leftWidth,
      top: rightTop,
      right: leftWidth + rightWidth,
      bottom: rightTop + rightHeight,
      width: rightWidth,
      height: rightHeight,
      primary: Boolean(split.rightPrimary),
      synthetic: true,
      inferred: true,
    },
  ];
}

function inferDisplayHeightFromWidth(width, remoteHeight) {
  const displayWidth = Math.max(0, Number(width || 0));
  if (displayWidth < DISPLAY_INFERENCE_MIN_WIDTH || remoteHeight < DISPLAY_INFERENCE_MIN_WIDTH) {
    return remoteHeight;
  }
  let best = null;
  DISPLAY_INFERENCE_ASPECT_PRIORS.forEach((aspectRatio) => {
    const candidateHeight = displayWidth / aspectRatio;
    if (
      !Number.isFinite(candidateHeight) ||
      candidateHeight < DISPLAY_INFERENCE_MIN_WIDTH ||
      candidateHeight > remoteHeight * 1.04
    ) {
      return;
    }
    const fractionError = Math.min(
      ...DISPLAY_INFERENCE_HEIGHT_FRACTIONS.map((fraction) =>
        Math.abs(candidateHeight - remoteHeight * fraction) / remoteHeight
      )
    );
    if (!best || fractionError < best.score) {
      best = { height: candidateHeight, score: fractionError };
    }
  });
  if (!best) return remoteHeight;
  return clampNumber(Math.round(best.height), DISPLAY_INFERENCE_MIN_WIDTH, remoteHeight);
}

function inferHorizontalDisplaysFromAspectPriors(framebufferSize) {
  const remoteWidth = Math.max(0, Number(framebufferSize?.width || 0));
  const remoteHeight = Math.max(0, Number(framebufferSize?.height || 0));
  if (
    remoteWidth < DISPLAY_INFERENCE_MIN_WIDTH * 2 ||
    remoteHeight < DISPLAY_INFERENCE_MIN_WIDTH ||
    remoteWidth / remoteHeight < DISPLAY_INFERENCE_WIDE_RATIO
  ) {
    return [];
  }
  let best = null;
  DISPLAY_INFERENCE_ASPECT_PRIORS.forEach((leftAspect) => {
    DISPLAY_INFERENCE_ASPECT_PRIORS.forEach((rightAspect) => {
      DISPLAY_INFERENCE_HEIGHT_FRACTIONS.forEach((leftFraction) => {
        DISPLAY_INFERENCE_HEIGHT_FRACTIONS.forEach((rightFraction) => {
          const leftHeight = remoteHeight * leftFraction;
          const rightHeight = remoteHeight * rightFraction;
          const leftWidth = leftHeight * leftAspect;
          const rightWidth = rightHeight * rightAspect;
          if (
            leftWidth < DISPLAY_INFERENCE_MIN_WIDTH ||
            rightWidth < DISPLAY_INFERENCE_MIN_WIDTH
          ) {
            return;
          }
          const totalWidth = leftWidth + rightWidth;
          const widthError = Math.abs(totalWidth - remoteWidth) / remoteWidth;
          const score =
            widthError +
            (rightFraction < 1 ? 0.04 : 0) +
            (rightWidth < leftWidth ? 0.02 : 0) +
            Math.abs(leftFraction - 0.75) * 0.01;
          if (!best || score < best.score) {
            best = {
              leftWidth,
              leftHeight,
              rightWidth,
              rightHeight,
              widthError,
              score,
            };
          }
        });
      });
    });
  });
  if (!best || best.widthError > DISPLAY_INFERENCE_MAX_ASPECT_ERROR) {
    return [];
  }
  const rightIsPrimary = best.rightWidth >= best.leftWidth;
  return buildInferredHorizontalTopology(remoteWidth, remoteHeight, {
    leftWidth: best.leftWidth,
    leftHeight: best.leftHeight,
    leftTop: remoteHeight - best.leftHeight,
    leftDisplayIndex: rightIsPrimary ? 2 : 1,
    leftPrimary: !rightIsPrimary,
    leftDeviceName: "Inferred left display",
    rightWidth: best.rightWidth,
    rightHeight: best.rightHeight,
    rightTop: remoteHeight - best.rightHeight,
    rightDisplayIndex: rightIsPrimary ? 1 : 2,
    rightPrimary: rightIsPrimary,
    rightDeviceName: "Inferred right display",
  });
}

function inferHorizontalDisplaysFromVirtualBounds(display, virtualBounds, framebufferSize) {
  const remoteWidth = Math.max(0, Number(framebufferSize?.width || 0));
  const remoteHeight = Math.max(0, Number(framebufferSize?.height || 0));
  if (
    !display ||
    !virtualBounds?.width ||
    !virtualBounds?.height ||
    remoteWidth < DISPLAY_INFERENCE_MIN_WIDTH * 2 ||
    remoteHeight < DISPLAY_INFERENCE_MIN_WIDTH
  ) {
    return [];
  }
  const scaleX = remoteWidth / virtualBounds.width;
  const scaleY = remoteHeight / virtualBounds.height;
  const reportedWidth = Math.max(0, Number(display.width || 0) * scaleX);
  const reportedHeight = Math.max(0, Number(display.height || 0) * scaleY);
  const reportedLeft = (Number(display.left || 0) - virtualBounds.left) * scaleX;
  const reportedTop = (Number(display.top || 0) - virtualBounds.top) * scaleY;
  const leftGap = clampNumber(reportedLeft, 0, remoteWidth);
  const rightGap = clampNumber(remoteWidth - (reportedLeft + reportedWidth), 0, remoteWidth);
  const reportedDisplayIndex = Math.max(
    1,
    Number(display?.displayIndex || display?.display_index || display?.id || 1)
  );
  const reportedIsPrimary = Boolean(display?.primary) || reportedDisplayIndex === 1;
  const gapDisplayIndex = reportedDisplayIndex === 1 ? 2 : 1;
  if (reportedWidth >= DISPLAY_INFERENCE_MIN_WIDTH && leftGap >= DISPLAY_INFERENCE_MIN_WIDTH) {
    const leftHeight = inferDisplayHeightFromWidth(leftGap, remoteHeight);
    return buildInferredHorizontalTopology(remoteWidth, remoteHeight, {
      leftWidth: leftGap,
      leftHeight,
      leftTop: remoteHeight - leftHeight,
      leftDisplayIndex: gapDisplayIndex,
      leftPrimary: false,
      rightWidth: reportedWidth,
      rightHeight: reportedHeight,
      rightTop: reportedTop,
      rightDisplayIndex: reportedDisplayIndex,
      rightPrimary: reportedIsPrimary,
      rightDeviceName: display?.deviceName || display?.device_name || "",
    });
  }
  if (reportedWidth >= DISPLAY_INFERENCE_MIN_WIDTH && rightGap >= DISPLAY_INFERENCE_MIN_WIDTH) {
    const rightHeight = inferDisplayHeightFromWidth(rightGap, remoteHeight);
    return buildInferredHorizontalTopology(remoteWidth, remoteHeight, {
      leftWidth: reportedWidth,
      leftHeight: reportedHeight,
      leftTop: reportedTop,
      leftDisplayIndex: reportedDisplayIndex,
      leftPrimary: reportedIsPrimary,
      leftDeviceName: display?.deviceName || display?.device_name || "",
      rightWidth: rightGap,
      rightHeight,
      rightTop: remoteHeight - rightHeight,
      rightDisplayIndex: gapDisplayIndex,
      rightPrimary: false,
    });
  }
  if (reportedWidth >= DISPLAY_INFERENCE_MIN_WIDTH && !closeDisplaySize(reportedTop, 0, 1)) {
    return [
      {
        ...display,
        left: reportedLeft,
        top: reportedTop,
        right: reportedLeft + reportedWidth,
        bottom: reportedTop + reportedHeight,
        width: reportedWidth,
        height: reportedHeight,
        synthetic: true,
      },
    ];
  }
  return [];
}

function inferHorizontalDisplaysFromFramebuffer(display, framebufferSize) {
  const remoteWidth = Math.max(0, Number(framebufferSize?.width || 0));
  const remoteHeight = Math.max(0, Number(framebufferSize?.height || 0));
  if (
    !display ||
    remoteWidth < DISPLAY_INFERENCE_MIN_WIDTH * 2 ||
    remoteHeight < DISPLAY_INFERENCE_MIN_WIDTH
  ) {
    return [];
  }
  const reportedWidth = Math.max(0, Number(display?.width || 0));
  const reportedHeight = Math.max(0, Number(display?.height || 0));
  const widthGap = remoteWidth - reportedWidth;
  const reportedDisplayIndex = Math.max(
    1,
    Number(display?.displayIndex || display?.display_index || display?.id || 1)
  );
  const reportedIsPrimary = Boolean(display?.primary) || reportedDisplayIndex === 1;
  const heightTolerance = Math.max(120, Math.round(remoteHeight * 0.28));
  const explicitLeft = Number(display?.left);
  const reportedLeft =
    Number.isFinite(explicitLeft) && explicitLeft > 0 && explicitLeft < remoteWidth
      ? explicitLeft
      : null;
  if (
    reportedWidth >= DISPLAY_INFERENCE_MIN_WIDTH &&
    widthGap >= DISPLAY_INFERENCE_MIN_WIDTH &&
    closeDisplaySize(reportedHeight, remoteHeight, heightTolerance)
  ) {
    const gapDisplayIndex = reportedDisplayIndex === 1 ? 2 : 1;
    if (reportedLeft >= DISPLAY_INFERENCE_MIN_WIDTH || reportedIsPrimary) {
      const leftWidth = reportedLeft >= DISPLAY_INFERENCE_MIN_WIDTH ? reportedLeft : widthGap;
      const leftHeight = inferDisplayHeightFromWidth(leftWidth, remoteHeight);
      return buildInferredHorizontalTopology(remoteWidth, remoteHeight, {
        leftWidth,
        leftHeight,
        leftTop: remoteHeight - leftHeight,
        leftDisplayIndex: gapDisplayIndex,
        leftPrimary: false,
        rightWidth: reportedWidth,
        rightHeight: reportedHeight,
        rightDisplayIndex: reportedDisplayIndex,
        rightPrimary: true,
        rightDeviceName: display?.deviceName || display?.device_name || "",
      });
    }
    const rightHeight = inferDisplayHeightFromWidth(widthGap, remoteHeight);
    return buildInferredHorizontalTopology(remoteWidth, remoteHeight, {
      leftWidth: reportedWidth,
      leftHeight: reportedHeight,
      leftDisplayIndex: reportedDisplayIndex,
      leftPrimary: reportedIsPrimary,
      leftDeviceName: display?.deviceName || display?.device_name || "",
      rightWidth: widthGap,
      rightHeight,
      rightTop: remoteHeight - rightHeight,
      rightDisplayIndex: gapDisplayIndex,
      rightPrimary: false,
    });
  }
  return [];
}

function displayDiagramTopology(topology, framebufferSize, renderedCanvasSize, virtualBounds) {
  const authoritativeFramebufferSize =
    framebufferSize?.width && framebufferSize?.height
      ? framebufferSize
      : renderedCanvasSize?.width && renderedCanvasSize?.height
        ? renderedCanvasSize
        : null;

  if (Array.isArray(topology) && topology.length > 1) {
    return topology;
  }
  if (Array.isArray(topology) && topology.length === 1) {
    const [display] = topology;
    if (authoritativeFramebufferSize?.width && authoritativeFramebufferSize?.height) {
      const bounds = displayTopologyBounds(topology);
      const shouldUsePreferredSize =
        !topologyMatchesFramebuffer(bounds, framebufferSize) ||
        !aspectRatioMatches(display, authoritativeFramebufferSize);
      if (shouldUsePreferredSize) {
        const boundsInferredTopology = inferHorizontalDisplaysFromVirtualBounds(
          display,
          virtualBounds,
          authoritativeFramebufferSize
        );
        if (boundsInferredTopology.length > 1) {
          return boundsInferredTopology;
        }
        const framebufferInferredTopology = inferHorizontalDisplaysFromFramebuffer(
          display,
          authoritativeFramebufferSize
        );
        if (framebufferInferredTopology.length > 1) {
          return framebufferInferredTopology;
        }
        const aspectInferredTopology = inferHorizontalDisplaysFromAspectPriors(
          authoritativeFramebufferSize
        );
        if (aspectInferredTopology.length > 1) {
          return aspectInferredTopology;
        }
        return [
          {
            ...display,
            left: 0,
            top: 0,
            right: authoritativeFramebufferSize.width,
            bottom: authoritativeFramebufferSize.height,
            width: authoritativeFramebufferSize.width,
            height: authoritativeFramebufferSize.height,
            primary: true,
            synthetic: true,
          },
        ];
      }
    }
    const aspectInferredTopology = inferHorizontalDisplaysFromAspectPriors(
      authoritativeFramebufferSize
    );
    if (aspectInferredTopology.length > 1) {
      return aspectInferredTopology;
    }
    return topology;
  }
  const fallbackSize =
    framebufferSize?.width && framebufferSize?.height
      ? framebufferSize
      : renderedCanvasSize?.width && renderedCanvasSize?.height
        ? renderedCanvasSize
        : null;
  if (!fallbackSize?.width || !fallbackSize?.height) {
    return [];
  }
  return [
    {
      id: "1",
      displayIndex: 1,
      label: "1",
      deviceName: "Framebuffer",
      left: 0,
      top: 0,
      right: fallbackSize.width,
      bottom: fallbackSize.height,
      width: fallbackSize.width,
      height: fallbackSize.height,
      primary: true,
      synthetic: true,
    },
  ];
}

function buildDisplayViewportTarget(selectedDisplayId, topology, bounds, framebufferSize) {
  const remoteWidth = Math.max(0, Number(framebufferSize?.width || bounds?.width || 0));
  const remoteHeight = Math.max(0, Number(framebufferSize?.height || bounds?.height || 0));
  if (!remoteWidth || !remoteHeight) return null;
  const allTarget = {
    id: ALL_DISPLAYS_ID,
    label: "All",
    left: 0,
    top: 0,
    width: remoteWidth,
    height: remoteHeight,
    remoteWidth,
    remoteHeight,
    focused: false,
  };
  if (
    selectedDisplayId === ALL_DISPLAYS_ID ||
    !Array.isArray(topology) ||
    !topology.length ||
    !bounds?.width ||
    !bounds?.height
  ) {
    return allTarget;
  }
  const monitor = topology.find((item) => monitorSelectionId(item) === selectedDisplayId);
  if (!monitor) return allTarget;
  const scaleX = remoteWidth / bounds.width;
  const scaleY = remoteHeight / bounds.height;
  const left = clampNumber((monitor.left - bounds.left) * scaleX, 0, remoteWidth);
  const top = clampNumber((monitor.top - bounds.top) * scaleY, 0, remoteHeight);
  const right = clampNumber(left + monitor.width * scaleX, left, remoteWidth);
  const bottom = clampNumber(top + monitor.height * scaleY, top, remoteHeight);
  const width = Math.max(1, right - left);
  const height = Math.max(1, bottom - top);
  return {
    id: monitorSelectionId(monitor),
    label: `Display ${monitor.label}`,
    left,
    top,
    width,
    height,
    remoteWidth,
    remoteHeight,
    focused: true,
  };
}

function sameViewportPreview(left, right) {
  if (!left || !right) return false;
  return (
    Math.abs(Number(left.left || 0) - Number(right.left || 0)) < 0.5 &&
    Math.abs(Number(left.top || 0) - Number(right.top || 0)) < 0.5 &&
    Math.abs(Number(left.width || 0) - Number(right.width || 0)) < 0.5 &&
    Math.abs(Number(left.height || 0) - Number(right.height || 0)) < 0.5 &&
    Math.abs(Number(left.targetLeft || 0) - Number(right.targetLeft || 0)) < 0.5 &&
    Math.abs(Number(left.targetTop || 0) - Number(right.targetTop || 0)) < 0.5 &&
    Math.abs(Number(left.targetWidth || 0) - Number(right.targetWidth || 0)) < 0.5 &&
    Math.abs(Number(left.targetHeight || 0) - Number(right.targetHeight || 0)) < 0.5 &&
    Boolean(left.interactive) === Boolean(right.interactive) &&
    left.mode === right.mode
  );
}

export default function RemoteDesktopPage({ device: providedDevice = null }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { deviceId } = useParams();
  const { user } = useAuth();
  const notifyOperator = useAppNotifications({
    title: "Remote Desktop",
    icon: "warning",
    variant: "warning",
    username: user || undefined,
  });
  const device = useMemo(() => {
    if (providedDevice) {
      return providedDevice;
    }
    const initialDevice = location.state?.initialDevice;
    if (initialDevice && typeof initialDevice === "object") {
      return initialDevice;
    }
    return {
      agent_guid: deviceId || null,
      hostname: deviceId || null,
      id: deviceId || null,
    };
  }, [deviceId, location.state, providedDevice]);
  const [sessionState, setSessionState] = useState("idle");
  const [vncStage, setVncStage] = useState("idle");
  const [statusMessage, setStatusMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [clipboardSync, setClipboardSync] = useState(false);
  const [clipboardNotImplementedOpen, setClipboardNotImplementedOpen] = useState(false);
  const [guacamoleAvailability, setGuacamoleAvailability] = useState({
    enabled: false,
    available: false,
    reason: "checking",
  });
  const [sessionId, setSessionId] = useState("");
  const [, setParticipantId] = useState("");
  const [displayMode, setDisplayMode] = useState("fit");
  const [performancePreference, setPerformancePreference] = useState(() => {
    if (typeof window === "undefined") return PERFORMANCE_PREFERENCE_DEFAULT;
    try {
      return normalizePerformancePreference(
        window.localStorage.getItem(REMOTE_DESKTOP_PERFORMANCE_STORAGE_KEY)
      );
    } catch {
      return PERFORMANCE_PREFERENCE_DEFAULT;
    }
  });
  const [expandedSidebarSections, setExpandedSidebarSections] = useState({
    display: true,
    clipboard: true,
    session: true,
  });
  const [selectedDisplayId, setSelectedDisplayId] = useState(ALL_DISPLAYS_ID);
  const [displayTopology, setDisplayTopology] = useState([]);
  const [displayVirtualBounds, setDisplayVirtualBounds] = useState(null);
  const [framebufferSize, setFramebufferSize] = useState({ width: 0, height: 0 });
  const [renderedCanvasSize, setRenderedCanvasSize] = useState({ width: 0, height: 0 });
  const [viewfinderSize, setViewfinderSize] = useState({
    width: VIEWFINDER_DEFAULT_WIDTH,
    height: VIEWFINDER_DEFAULT_HEIGHT,
  });
  const [viewportPreview, setViewportPreview] = useState({
    left: 0,
    top: 0,
    width: 0,
    height: 0,
    targetLeft: 0,
    targetTop: 0,
    targetWidth: 0,
    targetHeight: 0,
    interactive: false,
    mode: "fit",
  });

  const containerRef = useRef(null);
  const displayScrollRef = useRef(null);
  const displayRef = useRef(null);
  const viewfinderShellRef = useRef(null);
  const viewfinderRef = useRef(null);
  const remoteClientRef = useRef(null);
  const viewfinderCanvasRefs = useRef(new Map());
  const agentIdRef = useRef("");
  const sessionIdRef = useRef("");
  const sessionStateRef = useRef(sessionState);
  const loadingRef = useRef(loading);
  const clipboardSyncRef = useRef(clipboardSync);
  const clipboardLastRef = useRef("");
  const connectAttemptRef = useRef(0);
  const connectRunnerRef = useRef(null);
  const manualDisconnectRef = useRef(false);
  const sessionReconnectTimerRef = useRef(0);
  const sessionReconnectAttemptRef = useRef(0);
  const idleWarningTimerRef = useRef(0);
  const idleDisconnectTimerRef = useRef(0);
  const lastDesktopActivityAtRef = useRef(0);
  const idleWarningShownRef = useRef(false);
  const idleDisconnectInFlightRef = useRef(false);
  const tunnelWarningShownRef = useRef(false);
  const forcedViewportKeyRef = useRef("");
  const viewportFocusRef = useRef(null);
  const viewfinderDragRef = useRef({
    active: false,
    pointerId: null,
    offsetX: 0,
    offsetY: 0,
    width: 0,
    height: 0,
  });
  const viewfinderNavigateRafRef = useRef(0);
  const pendingViewfinderPointRef = useRef(null);

  const agentId = useMemo(() => {
    return (
      normalizeText(device?.agent_id) ||
      normalizeText(device?.agentId) ||
      normalizeText(device?.agent_guid) ||
      normalizeText(device?.agentGuid) ||
      normalizeText(device?.id) ||
      normalizeText(device?.guid) ||
      normalizeText(device?.summary?.agent_id) ||
      ""
    );
  }, [device]);
  const deviceRouteId = useMemo(
    () =>
      normalizeText(device?.agent_guid) ||
      normalizeText(device?.agentGuid) ||
      normalizeText(device?.id) ||
      normalizeText(deviceId),
    [device, deviceId]
  );
  const deviceHostname = useMemo(
    () =>
      normalizeText(device?.hostname) ||
      normalizeText(device?.summary?.hostname) ||
      deviceRouteId ||
      "device",
    [device, deviceRouteId]
  );
  const guacamoleAvailable = Boolean(guacamoleAvailability.available);
  const normalizedDisplayTopology = useMemo(
    () => normalizeDisplayTopology(displayTopology),
    [displayTopology]
  );
  const diagramTopology = useMemo(
    () =>
      displayDiagramTopology(
        normalizedDisplayTopology,
        framebufferSize,
        renderedCanvasSize,
        displayVirtualBounds
      ),
    [displayVirtualBounds, framebufferSize, normalizedDisplayTopology, renderedCanvasSize]
  );
  const diagramBounds = useMemo(
    () => displayTopologyBounds(diagramTopology),
    [diagramTopology]
  );
  const displaySelectorOptions = useMemo(
    () => [
      { id: ALL_DISPLAYS_ID, label: "All", shortLabel: "All" },
      ...diagramTopology
        .slice()
        .sort((left, right) => {
          if (left.displayIndex !== right.displayIndex) return left.displayIndex - right.displayIndex;
          return monitorSelectionId(left).localeCompare(monitorSelectionId(right));
        })
        .map((item) => ({
          id: monitorSelectionId(item),
          label: `Display ${item.label}`,
          shortLabel: item.label,
        })),
    ],
    [diagramTopology]
  );
  const effectiveSelectedMonitorIds = useMemo(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return [];
    const availableIds = new Set(
      diagramTopology.map((item) => monitorSelectionId(item)).filter(Boolean)
    );
    return availableIds.has(selectedDisplayId) ? [selectedDisplayId] : [];
  }, [diagramTopology, selectedDisplayId]);
  const displaySelectorLabel = useMemo(() => {
    const selectedOption = displaySelectorOptions.find((item) => item.id === selectedDisplayId);
    return `Display: ${selectedOption?.shortLabel || "All"}`;
  }, [displaySelectorOptions, selectedDisplayId]);
  const allDisplaysSelected = selectedDisplayId === ALL_DISPLAYS_ID;
  const singleDisplaySelected = !allDisplaysSelected;
  const viewfinderTopology = useMemo(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return diagramTopology;
    const selected = diagramTopology.filter(
      (item) => monitorSelectionId(item) === selectedDisplayId
    );
    return selected.length ? selected : diagramTopology;
  }, [diagramTopology, selectedDisplayId]);
  const displayLayoutGeometry = useMemo(
    () =>
      buildDisplayLayoutGeometry(viewfinderTopology, {
        frameWidth: Math.max(1, Math.round(viewfinderSize.width || VIEWFINDER_DEFAULT_WIDTH)),
        frameHeight: Math.max(1, Math.round(viewfinderSize.height || VIEWFINDER_DEFAULT_HEIGHT)),
        padding: 8,
      }),
    [viewfinderSize.height, viewfinderSize.width, viewfinderTopology]
  );
  const displayLayoutFrames = useMemo(
    () => displayLayoutGeometry.frames,
    [displayLayoutGeometry]
  );
  const singleViewfinderFrameRect = useMemo(() => {
    if (displayLayoutFrames.length !== 1) return null;
    const [item] = displayLayoutFrames;
    const widthPx = Math.max(2, Math.round(item.widthPx));
    const heightPx = Math.max(2, Math.round(item.heightPx));
    const x = Math.max(0, (viewfinderSize.width - widthPx) / 2);
    const y = Math.max(0, (viewfinderSize.height - heightPx) / 2);
    return {
      ...item,
      widthPx,
      heightPx,
      x,
      y,
    };
  }, [displayLayoutFrames, viewfinderSize.height, viewfinderSize.width]);
  const displayViewportTarget = useMemo(
    () =>
      buildDisplayViewportTarget(
        selectedDisplayId,
        diagramTopology,
        diagramBounds,
        framebufferSize
      ),
    [diagramBounds, diagramTopology, framebufferSize, selectedDisplayId]
  );

  const registerViewfinderCanvas = useCallback((frameId, node) => {
    if (!frameId) return;
    if (node) {
      viewfinderCanvasRefs.current.set(frameId, node);
      return;
    }
    viewfinderCanvasRefs.current.delete(frameId);
  }, []);

  const applySessionBootstrap = useCallback((data) => {
    const nextSession = data?.session && typeof data.session === "object" ? data.session : null;
    const nextSessionId = normalizeText(data?.session_id || nextSession?.session_id);
    const nextParticipantId = normalizeText(data?.participant_id || nextSession?.current_participant_id);
    const nextDisplayTopology = normalizeDisplayTopology(
      data?.display_topology || nextSession?.display_topology
    );
    const nextDisplayVirtualBounds = normalizeDisplayBounds(
      data?.display_virtual_bounds || nextSession?.display_virtual_bounds
    );
    setSessionId(nextSessionId);
    setParticipantId(nextParticipantId);
    setDisplayTopology((previous) =>
      equalDisplayTopology(previous, nextDisplayTopology) ? previous : nextDisplayTopology
    );
    setDisplayVirtualBounds((previous) =>
      equalDisplayBounds(previous, nextDisplayVirtualBounds) ? previous : nextDisplayVirtualBounds
    );
  }, []);

  useEffect(() => {
    agentIdRef.current = agentId;
  }, [agentId]);

  useEffect(() => {
    let cancelled = false;
    const loadViewers = async () => {
      try {
        const resp = await fetch("/api/vnc/viewers", { credentials: "include" });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok || cancelled) return;
        const guacamole =
          data?.guacamole ||
          (Array.isArray(data?.viewers)
            ? data.viewers.find((viewer) => normalizeText(viewer?.id) === "guacamole")
            : null) ||
          {};
        setGuacamoleAvailability({
          enabled: Boolean(guacamole?.enabled),
          available: Boolean(guacamole?.available),
          reason: normalizeText(guacamole?.reason),
        });
      } catch {
        setGuacamoleAvailability({
          enabled: false,
          available: false,
          reason: "unavailable",
        });
      }
    };
    loadViewers();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    clipboardSyncRef.current = clipboardSync;
  }, [clipboardSync]);

  useEffect(() => {
    loadingRef.current = loading;
  }, [loading]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(
        REMOTE_DESKTOP_PERFORMANCE_STORAGE_KEY,
        String(normalizePerformancePreference(performancePreference))
      );
    } catch {
      /* ignore */
    }
  }, [performancePreference]);

  useEffect(() => {
    sessionIdRef.current = sessionId;
  }, [sessionId]);

  useEffect(() => {
    sessionStateRef.current = sessionState;
  }, [sessionState]);

  useEffect(() => {
    if (displayMode === "actual" || (singleDisplaySelected && displayMode !== "fit")) {
      setDisplayMode("fit");
    }
  }, [displayMode, singleDisplaySelected]);

  const cancelPendingConnect = useCallback(() => {
    connectAttemptRef.current += 1;
  }, []);

  const focusDisplaySurface = useCallback(() => {
    const host = displayRef.current;
    if (!host || typeof host.focus !== "function") return;
    host.tabIndex = 0;
    try {
      host.focus({ preventScroll: true });
    } catch {
      host.focus();
    }
  }, []);

  const queueDisplayFocus = useCallback(() => {
    if (typeof window === "undefined") {
      focusDisplaySurface();
      return;
    }
    focusDisplaySurface();
    if (typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(focusDisplaySurface);
    }
    window.setTimeout(focusDisplaySurface, 100);
    window.setTimeout(focusDisplaySurface, 500);
  }, [focusDisplaySurface]);

  const clearSessionReconnect = useCallback(() => {
    if (sessionReconnectTimerRef.current && typeof window !== "undefined") {
      window.clearTimeout(sessionReconnectTimerRef.current);
    }
    sessionReconnectTimerRef.current = 0;
  }, []);

  const scheduleSessionReconnect = useCallback((reason = "desktop_stream_closed") => {
    if (manualDisconnectRef.current || !agentIdRef.current || typeof window === "undefined") {
      return false;
    }
    if (sessionReconnectTimerRef.current) {
      return true;
    }
    if (sessionReconnectAttemptRef.current >= VNC_SESSION_RECONNECT_ATTEMPTS) {
      return false;
    }
    sessionReconnectAttemptRef.current += 1;
    const attempt = sessionReconnectAttemptRef.current;
    setLoading(true);
    setSessionState("connecting");
    setVncStage("retrying");
    setStatusMessage(
      `Desktop stream changed; reconnecting... (${attempt}/${VNC_SESSION_RECONNECT_ATTEMPTS})`
    );
    sessionReconnectTimerRef.current = window.setTimeout(() => {
      sessionReconnectTimerRef.current = 0;
      const runner = connectRunnerRef.current;
      if (typeof runner === "function") {
        void runner({ automaticReconnect: true, reconnectReason: reason });
      }
    }, VNC_SESSION_RECONNECT_DELAY_MS);
    return true;
  }, []);

  const notifyAgentSocketUnavailable = useCallback(async () => {
    await notifyOperator({
      title: "Agent Connection Not Ready",
      message:
        "Engine does not have a live Agent socket for this device yet. Remote desktop will work after the Agent reconnects.",
      icon: "info",
      variant: "info",
    });
  }, [notifyOperator]);

  const handleAgentSocketUnavailable = useCallback(async () => {
    await notifyAgentSocketUnavailable();
    setStatusMessage("Agent connection not ready.");
    setSessionState("idle");
    setVncStage("agent_onboarding");
  }, [notifyAgentSocketUnavailable]);

  const resetDisconnectedViewState = useCallback(() => {
    forcedViewportKeyRef.current = "";
    viewportFocusRef.current = null;
    setDisplayTopology([]);
    setDisplayVirtualBounds(null);
    setSelectedDisplayId(ALL_DISPLAYS_ID);
    setDisplayMode("fit");
    setViewportPreview({
      left: 0,
      top: 0,
      width: 0,
      height: 0,
      targetLeft: 0,
      targetTop: 0,
      targetWidth: 0,
      targetHeight: 0,
      interactive: false,
      mode: "fit",
    });
  }, []);

  const teardownDisplay = useCallback(() => {
    try {
      const client = remoteClientRef.current;
      if (client) {
        if (client.__borealisKeyboard) {
          client.__borealisKeyboard.onkeydown = null;
          client.__borealisKeyboard.onkeyup = null;
        }
        if (client.__borealisMouse) {
          client.__borealisMouse.onmousedown = null;
          client.__borealisMouse.onmouseup = null;
          client.__borealisMouse.onmousemove = null;
        }
        client.disconnect();
      }
    } catch {
      /* ignore */
    }
    remoteClientRef.current = null;
    const host = displayRef.current;
    if (host) {
      host.innerHTML = "";
    }
    forcedViewportKeyRef.current = "";
    viewportFocusRef.current = null;
    setFramebufferSize({ width: 0, height: 0 });
    setRenderedCanvasSize({ width: 0, height: 0 });
    setViewportPreview({
      left: 0,
      top: 0,
      width: 0,
      height: 0,
      targetLeft: 0,
      targetTop: 0,
      targetWidth: 0,
      targetHeight: 0,
      interactive: false,
      mode: "fit",
    });
  }, []);

  const configureDisplaySurface = useCallback((client, mode) => {
    const host = displayRef.current;
    const display = typeof client?.getDisplay === "function" ? client.getDisplay() : null;
    const element = display?.getElement?.() || host?.firstElementChild || null;
    const width = Number(display?.getWidth?.() || 0);
    const height = Number(display?.getHeight?.() || 0);
    const hostWidth = Math.max(1, Number(host?.clientWidth || 0));
    const hostHeight = Math.max(1, Number(host?.clientHeight || 0));
    const targetScaleX =
      displayViewportTarget?.remoteWidth > 0 && width > 0
        ? width / displayViewportTarget.remoteWidth
        : 1;
    const targetScaleY =
      displayViewportTarget?.remoteHeight > 0 && height > 0
        ? height / displayViewportTarget.remoteHeight
        : 1;
    const targetLeft = clampNumber(
      Number(displayViewportTarget?.left || 0) * targetScaleX,
      0,
      Math.max(0, width)
    );
    const targetTop = clampNumber(
      Number(displayViewportTarget?.top || 0) * targetScaleY,
      0,
      Math.max(0, height)
    );
    const targetWidth = Math.max(
      1,
      Math.min(
        Math.max(1, width - targetLeft),
        Number(displayViewportTarget?.width || width || 1) * targetScaleX
      )
    );
    const targetHeight = Math.max(
      1,
      Math.min(
        Math.max(1, height - targetTop),
        Number(displayViewportTarget?.height || height || 1) * targetScaleY
      )
    );
    if (element?.style) {
      element.style.display = "block";
      element.style.margin = "0";
      element.style.position = "absolute";
      element.style.boxShadow = VNC_CANVAS_BOX_SHADOW;
      element.style.transformOrigin = "top left";
    }
    if (display && typeof display.scale === "function") {
      let scale = 1;
      if (mode === "fit" && targetWidth > 0 && targetHeight > 0) {
        scale = Math.min(hostWidth / targetWidth, hostHeight / targetHeight);
      } else if (mode === "scaled" && targetHeight > 0) {
        scale = hostHeight / targetHeight;
      }
      const nextScale = Number.isFinite(scale) && scale > 0 ? scale : 1;
      display.scale(nextScale);
      const visibleWidth = hostWidth / nextScale;
      const visibleHeight = hostHeight / nextScale;
      const targetRight = targetLeft + targetWidth;
      const targetBottom = targetTop + targetHeight;
      const focus = viewportFocusRef.current;
      const focusX =
        focus &&
        focus.x >= targetLeft &&
        focus.x <= targetRight &&
        focus.y >= targetTop &&
        focus.y <= targetBottom
          ? focus.x
          : targetLeft + targetWidth / 2;
      const focusY =
        focus &&
        focus.x >= targetLeft &&
        focus.x <= targetRight &&
        focus.y >= targetTop &&
        focus.y <= targetBottom
          ? focus.y
          : targetTop + targetHeight / 2;
      const visibleLeft =
        visibleWidth >= targetWidth
          ? targetLeft - (visibleWidth - targetWidth) / 2
          : clampNumber(focusX - visibleWidth / 2, targetLeft, targetRight - visibleWidth);
      const visibleTop =
        visibleHeight >= targetHeight
          ? targetTop - (visibleHeight - targetHeight) / 2
          : clampNumber(focusY - visibleHeight / 2, targetTop, targetBottom - visibleHeight);
      if (element?.style) {
        element.style.left = `${Math.round(-visibleLeft * nextScale * 100) / 100}px`;
        element.style.top = `${Math.round(-visibleTop * nextScale * 100) / 100}px`;
      }
      const previewLeft = clampNumber(visibleLeft, targetLeft, targetRight);
      const previewTop = clampNumber(visibleTop, targetTop, targetBottom);
      const previewRight = clampNumber(visibleLeft + visibleWidth, targetLeft, targetRight);
      const previewBottom = clampNumber(visibleTop + visibleHeight, targetTop, targetBottom);
      const nextPreview = {
        left: previewLeft,
        top: previewTop,
        width: Math.max(0, previewRight - previewLeft),
        height: Math.max(0, previewBottom - previewTop),
        targetLeft,
        targetTop,
        targetWidth,
        targetHeight,
        interactive: visibleWidth < targetWidth - 1 || visibleHeight < targetHeight - 1,
        mode,
      };
      setViewportPreview((previous) =>
        sameViewportPreview(previous, nextPreview) ? previous : nextPreview
      );
    } else if (element?.style) {
      element.style.left = "0px";
      element.style.top = "0px";
    }
  }, [displayViewportTarget]);

  const syncFramebufferSize = useCallback((client) => {
    const display = typeof client?.getDisplay === "function" ? client.getDisplay() : null;
    const width = Number(display?.getWidth?.() || 0);
    const height = Number(display?.getHeight?.() || 0);
    setFramebufferSize((previous) => {
      if (previous.width === width && previous.height === height) {
        return previous;
      }
      return { width, height };
    });
    return { width, height };
  }, []);

  const syncRenderedCanvasSize = useCallback((client) => {
    const display = typeof client?.getDisplay === "function" ? client.getDisplay() : null;
    const element = display?.getElement?.() || null;
    const rect = typeof element?.getBoundingClientRect === "function" ? element.getBoundingClientRect() : null;
    const width = Math.max(0, Math.round(Number(rect?.width || element?.clientWidth || 0)));
    const height = Math.max(0, Math.round(Number(rect?.height || element?.clientHeight || 0)));
    setRenderedCanvasSize((previous) => {
      if (previous.width === width && previous.height === height) {
        return previous;
      }
      return { width, height };
    });
    return { width, height };
  }, []);

  const disconnectVnc = useCallback(async (reason = "operator_disconnect") => {
    const currentAgentId = agentIdRef.current;
    if (!currentAgentId) return;
    try {
      await fetch("/api/vnc/disconnect", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          agent_id: currentAgentId,
          session_id: sessionIdRef.current || undefined,
          reason,
        }),
      });
    } catch {
      // best-effort
    }
  }, []);

  const handleDisconnect = useCallback(async () => {
    cancelPendingConnect();
    clearSessionReconnect();
    sessionReconnectAttemptRef.current = 0;
    manualDisconnectRef.current = true;
    setVncStage("disconnecting");
    setLoading(true);
    setStatusMessage("");
    try {
      teardownDisplay();
      await disconnectVnc("operator_disconnect");
    } finally {
      setSessionState("idle");
      setSessionId("");
      setParticipantId("");
      resetDisconnectedViewState();
      clipboardLastRef.current = "";
      setLoading(false);
    }
  }, [
    cancelPendingConnect,
    clearSessionReconnect,
    disconnectVnc,
    resetDisconnectedViewState,
    teardownDisplay,
  ]);

  const clearIdleTimers = useCallback(() => {
    if (idleWarningTimerRef.current) {
      window.clearTimeout(idleWarningTimerRef.current);
      idleWarningTimerRef.current = 0;
    }
    if (idleDisconnectTimerRef.current) {
      window.clearTimeout(idleDisconnectTimerRef.current);
      idleDisconnectTimerRef.current = 0;
    }
  }, []);

  const warnIdleOperator = useCallback(() => {
    if (sessionStateRef.current !== "connected" || idleWarningShownRef.current) return;
    idleWarningShownRef.current = true;
    void notifyOperator({
      title: "Remote Desktop Idle Warning",
      message:
        "No desktop interaction detected for 5 minutes. Interact with the remote desktop within 1 minute or the session will disconnect.",
      icon: "warning",
      variant: "warning",
    });
  }, [notifyOperator]);

  const disconnectIdleSession = useCallback(() => {
    if (sessionStateRef.current !== "connected" || idleDisconnectInFlightRef.current) return;
    idleDisconnectInFlightRef.current = true;
    void notifyOperator({
      title: "Remote Desktop Idle Timeout",
      message: "Remote desktop session disconnected after 6 minutes without desktop interaction.",
      icon: "warning",
      variant: "warning",
    });
    void handleDisconnect();
  }, [handleDisconnect, notifyOperator]);

  const scheduleIdleTimers = useCallback(() => {
    clearIdleTimers();
    if (sessionStateRef.current !== "connected" || typeof window === "undefined") return;
    idleWarningTimerRef.current = window.setTimeout(warnIdleOperator, VNC_IDLE_WARNING_MS);
    idleDisconnectTimerRef.current = window.setTimeout(disconnectIdleSession, VNC_IDLE_DISCONNECT_MS);
  }, [clearIdleTimers, disconnectIdleSession, warnIdleOperator]);

  const registerDesktopActivity = useCallback(() => {
    const now = Date.now();
    if (!idleWarningShownRef.current && now - lastDesktopActivityAtRef.current < 1000) {
      return;
    }
    lastDesktopActivityAtRef.current = now;
    idleWarningShownRef.current = false;
    idleDisconnectInFlightRef.current = false;
    if (sessionStateRef.current === "connected") {
      scheduleIdleTimers();
    }
  }, [scheduleIdleTimers]);

  useEffect(() => {
    if (sessionState !== "connected") {
      clearIdleTimers();
      idleWarningShownRef.current = false;
      idleDisconnectInFlightRef.current = false;
      tunnelWarningShownRef.current = false;
      return undefined;
    }
    idleWarningShownRef.current = false;
    idleDisconnectInFlightRef.current = false;
    tunnelWarningShownRef.current = false;
    scheduleIdleTimers();
    return clearIdleTimers;
  }, [clearIdleTimers, scheduleIdleTimers, sessionState]);

  const handleReturnToDevice = useCallback(() => {
    if (deviceRouteId) {
      navigate(APP_PATHS.device(deviceRouteId), {
        state: device ? { initialDevice: device } : undefined,
      });
      return;
    }
    if (typeof window !== "undefined" && window.history.length > 1) {
      navigate(-1);
      return;
    }
    navigate(APP_PATHS.devices);
  }, [device, deviceRouteId, navigate]);

  useEffect(() => {
    return () => {
      cancelPendingConnect();
      clearSessionReconnect();
      teardownDisplay();
      disconnectVnc("component_unmount");
    };
  }, [cancelPendingConnect, clearSessionReconnect, disconnectVnc, teardownDisplay]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      throw buildRetryableError("Agent ID is required to establish.", false);
    }
    setStatusMessage("");
    setVncStage("starting_agent_vnc");
    setSessionState("connecting");
    setStatusMessage("Starting Agent VNC service...");
    try {
      const resp = await fetch("/api/vnc/establish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          agent_id: agentId,
          remove_wallpaper: true,
          viewer: "guacamole",
          performance_preference: normalizePerformancePreference(performancePreference),
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        if (data?.error === "agent_socket_missing") {
          await handleAgentSocketUnavailable();
          return null;
        }
        const detail = data?.detail ? `: ${data.detail}` : "";
        const status = Number(resp.status || 0);
        throw buildRetryableError(
          `${data?.error || `HTTP ${resp.status}`}${detail}`,
          status >= 500 || status === 429 || status === 408
        );
      }
      applySessionBootstrap(data);
      return data;
    } catch (err) {
      if (err?.retryable !== false) {
        throw buildRetryableError(err?.message || err, true);
      }
      throw err;
    }
  }, [agentId, applySessionBootstrap, handleAgentSocketUnavailable, performancePreference]);

  const handleClipboardSyncAttempt = useCallback(() => {
    setClipboardSync(false);
    clipboardSyncRef.current = false;
    setClipboardNotImplementedOpen(true);
  }, []);

  const handlePerformancePreferenceChange = useCallback((_event, value) => {
    setPerformancePreference(normalizePerformancePreference(Array.isArray(value) ? value[0] : value));
  }, []);

  const handlePerformancePreferenceCommitted = useCallback(
    (_event, value) => {
      const normalized = normalizePerformancePreference(Array.isArray(value) ? value[0] : value);
      setPerformancePreference(normalized);
      if (sessionStateRef.current === "connected") {
        setStatusMessage("Speed/quality preference will apply on reconnect.");
        void notifyOperator({
          title: "Remote Desktop Preference Saved",
          message: "Speed/quality preference will apply on the next remote desktop reconnect.",
          icon: "info",
          variant: "info",
        });
      }
    },
    [notifyOperator]
  );

  const openGuacamoleSession = useCallback(
    async (data, options = {}) => {
      const connectToken = Number(options.connectToken || 0);
      const attempt = Number(options.attempt || 1);
      const maxAttempts = Number(options.maxAttempts || VNC_AUTO_RETRY_ATTEMPTS);
      const wsUrl = normalizeText(options.wsUrl) || buildGuacamoleWsCandidates(data)[0] || "";
      const wsCandidateIndex = Math.max(1, Number(options.wsCandidateIndex || 1));
      const wsCandidateCount = Math.max(1, Number(options.wsCandidateCount || 1));
      const token = data?.token || "";
      const displayHost = displayRef.current;
      if (!wsUrl || !token) {
        throw new Error("Guacamole session unavailable.");
      }
      if (!displayHost) {
        throw new Error("VNC display container missing.");
      }
      displayHost.innerHTML = "";
      displayHost.tabIndex = 0;
      displayHost.setAttribute("role", "application");
      displayHost.setAttribute("aria-label", "Remote desktop input surface");
      queueDisplayFocus();
      setVncStage("connecting_ws");
      setStatusMessage(
        attempt > 1
          ? `Establishing Apache Guacamole... attempt ${attempt}/${maxAttempts}`
          : wsCandidateCount > 1
            ? `Establishing Apache Guacamole... route ${wsCandidateIndex}/${wsCandidateCount}`
          : "Establishing Apache Guacamole..."
      );

      const tunnel = new Guacamole.WebSocketTunnel(wsUrl);
      tunnel.receiveTimeout = Math.max(Number(tunnel.receiveTimeout || 0), VNC_TUNNEL_RECEIVE_TIMEOUT_MS);
      tunnel.unstableThreshold = Math.max(Number(tunnel.unstableThreshold || 0), VNC_TUNNEL_UNSTABLE_THRESHOLD_MS);
      const client = new Guacamole.Client(tunnel);
      client.__borealisViewer = "guacamole";
      const display = client.getDisplay();
      const displayElement = display.getElement();
      displayElement.style.margin = "auto";
      displayElement.style.boxShadow = VNC_CANVAS_BOX_SHADOW;
      displayElement.tabIndex = -1;
      displayHost.appendChild(displayElement);
      queueDisplayFocus();
      const mouse = new Guacamole.Mouse(displayElement);
      mouse.onmousedown = mouse.onmouseup = mouse.onmousemove = (mouseState) => {
        registerDesktopActivity();
        client.sendMouseState(mouseState, true);
      };
      const keyboard = new Guacamole.Keyboard(displayHost);
      keyboard.onkeydown = (keysym) => {
        registerDesktopActivity();
        client.sendKeyEvent(1, keysym);
        return true;
      };
      keyboard.onkeyup = (keysym) => {
        registerDesktopActivity();
        client.sendKeyEvent(0, keysym);
        return true;
      };
      client.__borealisMouse = mouse;
      client.__borealisKeyboard = keyboard;
      remoteClientRef.current = client;

      return await new Promise((resolve, reject) => {
        let settled = false;
        let tunnelConnected = false;
        let tunnelOpened = false;
        let desktopReady = false;
        let sawDesktopSync = false;
        let stableTimerId = 0;
        const isStaleAttempt = () => connectToken && connectAttemptRef.current !== connectToken;
        const clearStableTimer = () => {
          if (stableTimerId) {
            window.clearTimeout(stableTimerId);
            stableTimerId = 0;
          }
        };
        const cleanup = () => {
          clearStableTimer();
          tunnel.onstatechange = null;
          client.onsync = null;
          if (display) {
            display.onresize = null;
          }
          keyboard.onkeydown = null;
          keyboard.onkeyup = null;
          mouse.onmousedown = null;
          mouse.onmouseup = null;
          mouse.onmousemove = null;
        };
        const finishResolve = () => {
          if (settled) return;
          settled = true;
          desktopReady = true;
          clearTimeout(timeoutId);
          clearTimeout(wsOpenTimeoutId);
          clearStableTimer();
          syncFramebufferSize(client);
          configureDisplaySurface(client, displayMode);
          syncRenderedCanvasSize(client);
          setSessionState("connected");
          setVncStage("connected");
          setStatusMessage("");
          sessionReconnectAttemptRef.current = 0;
          queueDisplayFocus();
          resolve();
        };
        const finishReject = (error, retryable = true) => {
          if (settled) return;
          settled = true;
          clearTimeout(timeoutId);
          clearTimeout(wsOpenTimeoutId);
          cleanup();
          const err = error instanceof Error ? error : new Error(String(error || "Guacamole session unavailable."));
          err.retryable = retryable;
          reject(err);
        };
        const armDesktopReady = () => {
          if (settled || isStaleAttempt() || desktopReady || !sawDesktopSync) return;
          const size = syncFramebufferSize(client);
          if (!size.width || !size.height) return;
          configureDisplaySurface(client, displayMode);
          if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
            window.requestAnimationFrame(() => syncRenderedCanvasSize(client));
          } else {
            syncRenderedCanvasSize(client);
          }
          setSessionState("connecting");
          setVncStage("waiting_frame");
          setStatusMessage("Waiting for first desktop frame...");
          if (!stableTimerId) {
            stableTimerId = window.setTimeout(finishResolve, VNC_READY_STABILIZE_MS);
          }
        };

        tunnel.onstatechange = (state) => {
          if (isStaleAttempt()) return;
          if (state === Guacamole.Tunnel.State.OPEN) {
            tunnelOpened = true;
            tunnelWarningShownRef.current = false;
            setVncStage("opening_guacamole");
            setStatusMessage("Opening Guacamole session...");
            return;
          }
          if (state === Guacamole.Tunnel.State.UNSTABLE && !tunnelWarningShownRef.current) {
            tunnelWarningShownRef.current = true;
            void notifyOperator({
              title: "Remote Desktop Connection Unstable",
              message:
                "Remote desktop tunnel appears unstable. Input or video may pause until traffic recovers.",
              icon: "warning",
              variant: "warning",
            });
          }
        };

        client.onerror = (status) => {
          if (isStaleAttempt()) return;
          const message = status?.message || status?.code || "Guacamole connection failed.";
          if (desktopReady && scheduleSessionReconnect(String(message))) {
            return;
          }
          setSessionState("error");
          setVncStage("error");
          setStatusMessage(String(message));
          void notifyOperator({
            title: "Remote Desktop Connection Issue",
            message: `Remote desktop tunnel reported a problem: ${String(message)}`,
            icon: "warning",
            variant: "warning",
          });
          finishReject(String(message), true);
        };
        client.onstatechange = (state) => {
          if (isStaleAttempt()) {
            try {
              client.disconnect();
            } catch {
              /* ignore */
            }
            return;
          }
          if (state === Guacamole.Client.State.CONNECTED) {
            tunnelConnected = true;
            setVncStage("waiting_frame");
            setStatusMessage("Waiting for first desktop frame...");
            syncFramebufferSize(client);
            configureDisplaySurface(client, displayMode);
            if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
              window.requestAnimationFrame(() => syncRenderedCanvasSize(client));
            } else {
              syncRenderedCanvasSize(client);
            }
            armDesktopReady();
          } else if (state === Guacamole.Client.State.DISCONNECTED) {
            cleanup();
            remoteClientRef.current = null;
            if (!desktopReady) {
              setSessionState("connecting");
              setVncStage("retrying");
              finishReject(
                tunnelConnected
                  ? "Guacamole proxy disconnected before desktop video stabilized."
                  : "Guacamole proxy disconnected before the desktop became available.",
                true
              );
              return;
            }
            const wasManual = manualDisconnectRef.current;
            manualDisconnectRef.current = false;
            if (!wasManual && scheduleSessionReconnect("desktop_stream_disconnected")) {
              return;
            }
            setSessionState("idle");
            setSessionId("");
            setParticipantId("");
            resetDisconnectedViewState();
            clipboardLastRef.current = "";
            setVncStage((prev) => (prev === "error" ? prev : "disconnected"));
            if (!wasManual && sessionIdRef.current) {
              setStatusMessage("Desktop stream closed. Reconnect or claim control when the session is ready.");
            }
          } else if (state === Guacamole.Client.State.WAITING) {
            setVncStage("opening_guacamole");
          }
        };
        display.onresize = () => {
          if (isStaleAttempt()) return;
          syncFramebufferSize(client);
          configureDisplaySurface(client, displayMode);
          queueDisplayFocus();
          armDesktopReady();
        };
        client.onsync = () => {
          if (isStaleAttempt()) return;
          sawDesktopSync = true;
          armDesktopReady();
        };
        client.onclipboard = (stream, mimetype) => {
          if (!String(mimetype || "").startsWith("text/")) return;
          const reader = new Guacamole.StringReader(stream);
          let text = "";
          reader.ontext = (chunk) => {
            text += chunk;
          };
          reader.onend = () => {
            clipboardLastRef.current = text;
            if (clipboardSyncRef.current) {
              writeClipboardText(text);
            }
          };
        };
        const timeoutId = setTimeout(() => {
          if (desktopReady) return;
          try {
            client.disconnect();
          } catch {
            /* ignore */
          }
          finishReject("Guacamole connection timed out.", true);
        }, VNC_OPEN_TIMEOUT_MS);
        const wsOpenTimeoutId = setTimeout(() => {
          if (desktopReady || tunnelOpened) return;
          try {
            client.disconnect();
          } catch {
            /* ignore */
          }
          finishReject("Guacamole websocket did not open.", true);
        }, VNC_WS_OPEN_TIMEOUT_MS);
        try {
          client.connect(`token=${encodeURIComponent(token)}`);
        } catch (error) {
          finishReject(error, true);
        }
      });
    },
    [
      configureDisplaySurface,
      displayMode,
      notifyOperator,
      queueDisplayFocus,
      registerDesktopActivity,
      resetDisconnectedViewState,
      scheduleSessionReconnect,
      syncFramebufferSize,
      syncRenderedCanvasSize,
    ]
  );

  const openVncSession = useCallback(
    async (data, options = {}) => openGuacamoleSession(data, options),
    [openGuacamoleSession]
  );

  const runVncConnect = useCallback(async (options = {}) => {
    const automaticReconnect = Boolean(options.automaticReconnect);
    if (!automaticReconnect && (sessionStateRef.current === "connected" || loadingRef.current)) {
      return;
    }
    if (!automaticReconnect) {
      clearSessionReconnect();
      sessionReconnectAttemptRef.current = 0;
    }
    const connectToken = connectAttemptRef.current + 1;
    connectAttemptRef.current = connectToken;
    manualDisconnectRef.current = false;
    setStatusMessage("");
    setLoading(true);
    setSessionState("connecting");
    teardownDisplay();
    let reconnectScheduled = false;
    try {
      for (let attempt = 1; attempt <= VNC_AUTO_RETRY_ATTEMPTS; attempt += 1) {
        if (connectAttemptRef.current !== connectToken) return;
        if (attempt > 1) {
          setSessionState("connecting");
          setVncStage("retrying");
          setStatusMessage(`Retrying VNC... (${attempt}/${VNC_AUTO_RETRY_ATTEMPTS})`);
          await sleep(VNC_AUTO_RETRY_DELAY_MS);
          if (connectAttemptRef.current !== connectToken) return;
        }
        try {
          const sessionData = await requestTunnel();
          if (!sessionData || connectAttemptRef.current !== connectToken) {
            return;
          }
          const wsCandidates = buildGuacamoleWsCandidates(sessionData);
          const candidateList = wsCandidates.length ? wsCandidates : [""];
          let lastCandidateError = null;
          for (let candidateIndex = 0; candidateIndex < candidateList.length; candidateIndex += 1) {
            if (connectAttemptRef.current !== connectToken) return;
            try {
              await openVncSession(sessionData, {
                connectToken,
                attempt,
                maxAttempts: VNC_AUTO_RETRY_ATTEMPTS,
                wsUrl: candidateList[candidateIndex],
                wsCandidateIndex: candidateIndex + 1,
                wsCandidateCount: candidateList.length,
              });
              return;
            } catch (candidateErr) {
              teardownDisplay();
              lastCandidateError = candidateErr;
              const retryableCandidate = candidateErr?.retryable !== false;
              if (!retryableCandidate || candidateIndex >= candidateList.length - 1) {
                throw candidateErr;
              }
              setSessionState("connecting");
              setVncStage("retrying");
              setStatusMessage(`Retrying Guacamole route... (${candidateIndex + 2}/${candidateList.length})`);
              await sleep(250);
            }
          }
          if (lastCandidateError) {
            throw lastCandidateError;
          }
          return;
        } catch (err) {
          teardownDisplay();
          const retryable = err?.retryable !== false;
          if (!retryable || attempt >= VNC_AUTO_RETRY_ATTEMPTS) {
            throw err;
          }
        }
      }
    } catch (err) {
      if (connectAttemptRef.current !== connectToken) return;
      if (automaticReconnect && scheduleSessionReconnect(String(err.message || err))) {
        reconnectScheduled = true;
        return;
      }
      setSessionState("error");
      setStatusMessage(String(err.message || err));
      setVncStage("error");
    } finally {
      if (connectAttemptRef.current === connectToken && !reconnectScheduled) {
        setLoading(false);
      }
    }
  }, [
    clearSessionReconnect,
    openVncSession,
    requestTunnel,
    scheduleSessionReconnect,
    teardownDisplay,
  ]);

  useEffect(() => {
    connectRunnerRef.current = runVncConnect;
  }, [runVncConnect]);

  const handleConnect = useCallback(() => {
    void runVncConnect();
  }, [runVncConnect]);

  const refreshSessionDetails = useCallback(async () => {
    const currentSessionId = sessionIdRef.current;
    if (!currentSessionId) return;
    try {
      const resp = await fetch(`/api/vnc/sessions?session_id=${encodeURIComponent(currentSessionId)}`, {
        credentials: "include",
      });
      if (!resp.ok) return;
      const data = await resp.json().catch(() => ({}));
      const nextSession = Array.isArray(data?.sessions) ? data.sessions[0] || null : null;
      if (!nextSession) {
        teardownDisplay();
        setSessionState("idle");
        setVncStage("disconnected");
        setSessionId("");
        setParticipantId("");
        resetDisconnectedViewState();
        return;
      }
      const nextDisplayTopology = normalizeDisplayTopology(nextSession.display_topology);
      if (nextDisplayTopology.length) {
        setDisplayTopology((previous) =>
          equalDisplayTopology(previous, nextDisplayTopology) ? previous : nextDisplayTopology
        );
      }
      const nextDisplayVirtualBounds = normalizeDisplayBounds(nextSession.display_virtual_bounds);
      setDisplayVirtualBounds((previous) =>
        equalDisplayBounds(previous, nextDisplayVirtualBounds) ? previous : nextDisplayVirtualBounds
      );
      setParticipantId((previous) => normalizeText(nextSession.current_participant_id) || previous);
    } catch {
      // ignore background session refresh failures
    }
  }, [resetDisconnectedViewState, teardownDisplay]);

  useEffect(() => {
    if (!sessionId) return undefined;
    refreshSessionDetails();
    const intervalId = window.setInterval(() => {
      refreshSessionDetails();
    }, 4000);
    return () => {
      window.clearInterval(intervalId);
    };
  }, [refreshSessionDetails, sessionId]);

  const injectClipboardKeystrokes = useCallback(async (prefilledText = "") => {
    const client = remoteClientRef.current;
    if (!client) return;
    let text = prefilledText || "";
    if (!text && navigator?.clipboard?.readText) {
      try {
        text = await navigator.clipboard.readText();
      } catch {
        text = "";
      }
    }
    if (!text) {
      setStatusMessage("Clipboard is empty or unavailable.");
      return;
    }
    try {
      const current = remoteClientRef.current;
      for (const char of text) {
        if (remoteClientRef.current !== current) break;
        const keysym = keysymFromChar(char);
        if (keysym == null) continue;
        sendGuacamoleKeysym(client, keysym);
        // Slow down to improve reliability on large pastes.
        await sleep(20);
      }
      setStatusMessage("Clipboard injected as keystrokes.");
    } catch {
      setStatusMessage("Failed to inject keystrokes.");
    }
  }, []);

  const handleCtrlAltDel = useCallback(() => {
    const client = remoteClientRef.current;
    if (!client) return;
    try {
      client.sendKeyEvent(1, 0xffe3);
      client.sendKeyEvent(1, 0xffe9);
      client.sendKeyEvent(1, 0xffff);
      client.sendKeyEvent(0, 0xffff);
      client.sendKeyEvent(0, 0xffe9);
      client.sendKeyEvent(0, 0xffe3);
      setStatusMessage("Sent Ctrl+Alt+Del.");
      queueDisplayFocus();
    } catch {
      setStatusMessage("Failed to send Ctrl+Alt+Del.");
    }
  }, [queueDisplayFocus]);

  const syncViewportSelection = useCallback((_selectionIds, options = {}) => {
    if (options.forceReset) {
      viewportFocusRef.current = null;
    }
    const client = remoteClientRef.current;
    if (!client) return;
    configureDisplaySurface(client, displayMode);
    syncFramebufferSize(client);
    syncRenderedCanvasSize(client);
  }, [
    configureDisplaySurface,
    displayMode,
    syncFramebufferSize,
    syncRenderedCanvasSize,
  ]);

  const isConnected = sessionState === "connected";
  const previewNavigationEnabled = Boolean(isConnected && displayViewportTarget);

  const navigateViewfinderPoint = useCallback(
    (localX, localY) => {
      if (!displayLayoutGeometry.bounds || !displayLayoutGeometry.scale || !displayViewportTarget) return;
      const sourceBounds = diagramBounds || displayLayoutGeometry.bounds;
      if (!sourceBounds?.width || !sourceBounds?.height) return;
      const remoteWidth = Math.max(1, Number(displayViewportTarget.remoteWidth || 0));
      const remoteHeight = Math.max(1, Number(displayViewportTarget.remoteHeight || 0));
      const diagramX =
        displayLayoutGeometry.bounds.left +
        (localX - displayLayoutGeometry.offsetX) / displayLayoutGeometry.scale;
      const diagramY =
        displayLayoutGeometry.bounds.top +
        (localY - displayLayoutGeometry.offsetY) / displayLayoutGeometry.scale;
      const scaleX = remoteWidth / Math.max(1, sourceBounds.width);
      const scaleY = remoteHeight / Math.max(1, sourceBounds.height);
      const remoteX = clampNumber(
        (diagramX - sourceBounds.left) * scaleX,
        displayViewportTarget.left,
        displayViewportTarget.left + displayViewportTarget.width
      );
      const remoteY = clampNumber(
        (diagramY - sourceBounds.top) * scaleY,
        displayViewportTarget.top,
        displayViewportTarget.top + displayViewportTarget.height
      );
      viewportFocusRef.current = { x: remoteX, y: remoteY };
      const client = remoteClientRef.current;
      if (!client) return;
      configureDisplaySurface(client, displayMode);
      syncFramebufferSize(client);
      syncRenderedCanvasSize(client);
    },
    [
      configureDisplaySurface,
      diagramBounds,
      displayLayoutGeometry,
      displayMode,
      displayViewportTarget,
      syncFramebufferSize,
      syncRenderedCanvasSize,
    ]
  );

  const queueViewfinderNavigate = useCallback(
    (localX, localY) => {
      pendingViewfinderPointRef.current = { localX, localY };
      if (
        typeof window === "undefined" ||
        typeof window.requestAnimationFrame !== "function"
      ) {
        const nextPoint = pendingViewfinderPointRef.current;
        pendingViewfinderPointRef.current = null;
        if (nextPoint) {
          navigateViewfinderPoint(nextPoint.localX, nextPoint.localY);
        }
        return;
      }
      if (viewfinderNavigateRafRef.current) return;
      viewfinderNavigateRafRef.current = window.requestAnimationFrame(() => {
        viewfinderNavigateRafRef.current = 0;
        const nextPoint = pendingViewfinderPointRef.current;
        pendingViewfinderPointRef.current = null;
        if (nextPoint) {
          navigateViewfinderPoint(nextPoint.localX, nextPoint.localY);
        }
      });
    },
    [navigateViewfinderPoint]
  );

  const handlePreviewPointerDown = useCallback(
    (event) => {
      if (!isConnected || displayMode === "fit") return;
      const targetNode =
        event.target && typeof event.target.closest === "function"
          ? event.target.closest("[data-viewfinder-display-button='true']")
          : null;
      if (!targetNode && typeof event.preventDefault === "function") {
        event.preventDefault();
      }
      const rect = event.currentTarget.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      const localX = event.clientX - rect.left;
      const localY = event.clientY - rect.top;
      const dragRect =
        singleViewfinderFrameRect &&
        viewportPreview.targetWidth > 0 &&
        viewportPreview.targetHeight > 0
          ? {
              x:
                singleViewfinderFrameRect.x +
                ((viewportPreview.left - viewportPreview.targetLeft) / viewportPreview.targetWidth) *
                  singleViewfinderFrameRect.widthPx,
              y:
                singleViewfinderFrameRect.y +
                ((viewportPreview.top - viewportPreview.targetTop) / viewportPreview.targetHeight) *
                  singleViewfinderFrameRect.heightPx,
              width:
                (viewportPreview.width / viewportPreview.targetWidth) *
                singleViewfinderFrameRect.widthPx,
              height:
                (viewportPreview.height / viewportPreview.targetHeight) *
                singleViewfinderFrameRect.heightPx,
            }
          : null;
      const canDragViewport =
        Boolean(dragRect) &&
        viewportPreview.interactive &&
        localX >= dragRect.x &&
        localX <= dragRect.x + dragRect.width &&
        localY >= dragRect.y &&
        localY <= dragRect.y + dragRect.height;
      if (canDragViewport) {
        viewfinderDragRef.current = {
          active: true,
          pointerId: event.pointerId,
          offsetX: localX - dragRect.x,
          offsetY: localY - dragRect.y,
          width: dragRect.width,
          height: dragRect.height,
        };
        if (typeof event.currentTarget.setPointerCapture === "function") {
          event.currentTarget.setPointerCapture(event.pointerId);
        }
        return;
      }
      queueViewfinderNavigate(localX, localY);
    },
    [
      displayMode,
      isConnected,
      queueViewfinderNavigate,
      singleViewfinderFrameRect,
      viewportPreview.interactive,
      viewportPreview.height,
      viewportPreview.left,
      viewportPreview.targetHeight,
      viewportPreview.targetLeft,
      viewportPreview.targetTop,
      viewportPreview.targetWidth,
      viewportPreview.top,
      viewportPreview.width,
    ]
  );

  const handlePreviewPointerMove = useCallback(
    (event) => {
      const dragState = viewfinderDragRef.current;
      if (!dragState.active || dragState.pointerId !== event.pointerId) return;
      if (typeof event.preventDefault === "function") {
        event.preventDefault();
      }
      const rect = event.currentTarget.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      const localX =
        event.clientX -
        rect.left -
        dragState.offsetX +
        dragState.width / 2;
      const localY =
        event.clientY -
        rect.top -
        dragState.offsetY +
        dragState.height / 2;
      queueViewfinderNavigate(localX, localY);
    },
    [queueViewfinderNavigate]
  );

  const endPreviewDrag = useCallback((event) => {
    const dragState = viewfinderDragRef.current;
    if (!dragState.active) return;
    if (event && dragState.pointerId !== event.pointerId) return;
    if (
      event &&
      typeof event.currentTarget.releasePointerCapture === "function" &&
      typeof event.currentTarget.hasPointerCapture === "function" &&
      event.currentTarget.hasPointerCapture(event.pointerId)
    ) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    viewfinderDragRef.current = {
      active: false,
      pointerId: null,
      offsetX: 0,
      offsetY: 0,
      width: 0,
      height: 0,
    };
  }, []);

  const selectDisplay = useCallback(
    (displayId) => {
      const nextDisplayId = normalizeText(displayId) || ALL_DISPLAYS_ID;
      if (nextDisplayId !== ALL_DISPLAYS_ID) {
        setDisplayMode("fit");
      }
      if (nextDisplayId !== selectedDisplayId) {
        viewportFocusRef.current = null;
      }
      setSelectedDisplayId(nextDisplayId);
      queueDisplayFocus();
    },
    [queueDisplayFocus, selectedDisplayId]
  );

  const forcedViewportKey = useMemo(
    () =>
      isConnected
        ? `${displayMode}|${effectiveSelectedMonitorIds.join(",") || ALL_DISPLAYS_ID}`
        : "",
    [displayMode, effectiveSelectedMonitorIds, isConnected]
  );

  useEffect(() => {
    if (!isConnected) {
      forcedViewportKeyRef.current = "";
      return;
    }
    if (forcedViewportKeyRef.current === forcedViewportKey) return;
    forcedViewportKeyRef.current = forcedViewportKey;
    syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false, forceReset: true });
  }, [effectiveSelectedMonitorIds, forcedViewportKey, isConnected, syncViewportSelection]);

  useEffect(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return;
    const availableIds = new Set(
      diagramTopology.map((item) => monitorSelectionId(item)).filter(Boolean)
    );
    if (availableIds.has(selectedDisplayId)) return;
    setSelectedDisplayId(ALL_DISPLAYS_ID);
  }, [diagramTopology, selectedDisplayId]);

  useEffect(() => {
    if (!isConnected) return undefined;
    const timers = [
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 250),
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 1200),
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 2500),
    ];
    return () => {
      timers.forEach((timerId) => window.clearTimeout(timerId));
    };
  }, [effectiveSelectedMonitorIds, isConnected, syncViewportSelection]);

  useEffect(() => {
    if (!isConnected) return undefined;
    if (typeof ResizeObserver === "undefined") return undefined;
    const host = displayScrollRef.current || containerRef.current;
    if (!host) return undefined;
    let frameId = 0;
    const observer = new ResizeObserver(() => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      frameId = window.requestAnimationFrame(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      });
    });
    observer.observe(host);
    return () => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      observer.disconnect();
    };
  }, [effectiveSelectedMonitorIds, isConnected, syncViewportSelection]);

  useEffect(() => {
    if (typeof ResizeObserver === "undefined") return undefined;
    const host = viewfinderRef.current;
    const shell = viewfinderShellRef.current;
    if (!host && !shell) return undefined;
    let frameId = 0;
    const syncSize = () => {
      const hostWidth = Number(host?.clientWidth || 0);
      const hostHeight = Number(host?.clientHeight || 0);
      const shellWidth = Number(shell?.clientWidth || 0);
      const shellHeight = Number(shell?.clientHeight || 0);
      const measuredWidth = Math.round(hostWidth || Math.max(0, shellWidth - 20));
      const measuredHeight = Math.round(hostHeight || Math.max(0, shellHeight - 20));
      setViewfinderSize((previous) => {
        if (
          measuredWidth < VIEWFINDER_MIN_MEASURED_SIZE ||
          measuredHeight < VIEWFINDER_MIN_MEASURED_SIZE
        ) {
          return previous;
        }
        const nextWidth = Math.max(1, measuredWidth);
        const nextHeight = Math.max(1, measuredHeight);
        if (previous.width === nextWidth && previous.height === nextHeight) {
          return previous;
        }
        return { width: nextWidth, height: nextHeight };
      });
    };
    syncSize();
    const observer = new ResizeObserver(() => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      frameId = window.requestAnimationFrame(syncSize);
    });
    if (host) observer.observe(host);
    if (shell && shell !== host) observer.observe(shell);
    return () => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      observer.disconnect();
    };
  }, []);

  useEffect(() => {
    const clearViewfinderCanvases = () => {
      viewfinderCanvasRefs.current.forEach((canvas) => {
        const context = canvas?.getContext?.("2d");
        if (!context) return;
        context.clearRect(0, 0, canvas.width || 0, canvas.height || 0);
      });
    };
    if (!isConnected) {
      clearViewfinderCanvases();
      return undefined;
    }
    let frameId = 0;
    const renderMirror = () => {
      const sourceCanvas = displayRef.current?.querySelector("canvas") || null;
      const sourceWidth = Number(sourceCanvas?.width || 0);
      const sourceHeight = Number(sourceCanvas?.height || 0);
      if (!sourceCanvas || sourceWidth <= 0 || sourceHeight <= 0 || !diagramBounds?.width || !diagramBounds?.height) {
        frameId = window.requestAnimationFrame(renderMirror);
        return;
      }
      const sourceScaleX = sourceWidth / diagramBounds.width;
      const sourceScaleY = sourceHeight / diagramBounds.height;
      const topologyById = new Map(diagramTopology.map((item) => [item.id, item]));
      const frameById = new Map(displayLayoutFrames.map((item) => [item.id, item]));
      viewfinderCanvasRefs.current.forEach((targetCanvas, frameIdKey) => {
        const topologyItem = topologyById.get(frameIdKey);
        const frameItem = frameById.get(frameIdKey);
        const context = targetCanvas?.getContext?.("2d");
        if (!topologyItem || !frameItem || !context) return;
        const destWidth = Math.max(1, Math.round(Number(frameItem.widthPx || 0)));
        const destHeight = Math.max(1, Math.round(Number(frameItem.heightPx || 0)));
        if (targetCanvas.width !== destWidth) targetCanvas.width = destWidth;
        if (targetCanvas.height !== destHeight) targetCanvas.height = destHeight;
        context.clearRect(0, 0, destWidth, destHeight);
        const sourceX = clampNumber(
          Math.round((topologyItem.left - diagramBounds.left) * sourceScaleX),
          0,
          Math.max(0, sourceWidth - 1)
        );
        const sourceY = clampNumber(
          Math.round((topologyItem.top - diagramBounds.top) * sourceScaleY),
          0,
          Math.max(0, sourceHeight - 1)
        );
        const sourceWidthClamped = Math.max(
          1,
          Math.min(sourceWidth - sourceX, Math.round(topologyItem.width * sourceScaleX))
        );
        const sourceHeightClamped = Math.max(
          1,
          Math.min(sourceHeight - sourceY, Math.round(topologyItem.height * sourceScaleY))
        );
        context.drawImage(
          sourceCanvas,
          sourceX,
          sourceY,
          sourceWidthClamped,
          sourceHeightClamped,
          0,
          0,
          destWidth,
          destHeight
        );
      });
      frameId = window.requestAnimationFrame(renderMirror);
    };
    frameId = window.requestAnimationFrame(renderMirror);
    return () => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      clearViewfinderCanvases();
    };
  }, [diagramBounds, diagramTopology, displayLayoutFrames, isConnected]);

  useEffect(() => {
    return () => {
      if (
        typeof window !== "undefined" &&
        typeof window.cancelAnimationFrame === "function" &&
        viewfinderNavigateRafRef.current
      ) {
        window.cancelAnimationFrame(viewfinderNavigateRafRef.current);
      }
    };
  }, []);

  const viewfinderViewportRect = useMemo(() => {
    if (!isConnected || !displayLayoutGeometry.bounds || viewportPreview.width <= 0 || viewportPreview.height <= 0) {
      return null;
    }
    if (
      singleViewfinderFrameRect &&
      viewportPreview.targetWidth > 0 &&
      viewportPreview.targetHeight > 0
    ) {
      const x =
        singleViewfinderFrameRect.x +
        ((viewportPreview.left - viewportPreview.targetLeft) / viewportPreview.targetWidth) *
          singleViewfinderFrameRect.widthPx;
      const y =
        singleViewfinderFrameRect.y +
        ((viewportPreview.top - viewportPreview.targetTop) / viewportPreview.targetHeight) *
          singleViewfinderFrameRect.heightPx;
      const width =
        (viewportPreview.width / viewportPreview.targetWidth) * singleViewfinderFrameRect.widthPx;
      const height =
        (viewportPreview.height / viewportPreview.targetHeight) *
        singleViewfinderFrameRect.heightPx;
      const clampedX = clampNumber(
        x,
        singleViewfinderFrameRect.x,
        singleViewfinderFrameRect.x + Math.max(0, singleViewfinderFrameRect.widthPx - 2)
      );
      const clampedY = clampNumber(
        y,
        singleViewfinderFrameRect.y,
        singleViewfinderFrameRect.y + Math.max(0, singleViewfinderFrameRect.heightPx - 2)
      );
      return {
        x: clampedX,
        y: clampedY,
        width: Math.max(
          2,
          Math.min(width, singleViewfinderFrameRect.x + singleViewfinderFrameRect.widthPx - clampedX)
        ),
        height: Math.max(
          2,
          Math.min(height, singleViewfinderFrameRect.y + singleViewfinderFrameRect.heightPx - clampedY)
        ),
        interactive: viewportPreview.interactive,
      };
    }
    const rawX =
      displayLayoutGeometry.offsetX +
      (viewportPreview.left - displayLayoutGeometry.bounds.left) * displayLayoutGeometry.scale;
    const rawY =
      displayLayoutGeometry.offsetY +
      (viewportPreview.top - displayLayoutGeometry.bounds.top) * displayLayoutGeometry.scale;
    const rawTargetX =
      displayLayoutGeometry.offsetX +
      (viewportPreview.targetLeft - displayLayoutGeometry.bounds.left) * displayLayoutGeometry.scale;
    const rawTargetY =
      displayLayoutGeometry.offsetY +
      (viewportPreview.targetTop - displayLayoutGeometry.bounds.top) * displayLayoutGeometry.scale;
    const insetPadding = displayLayoutGeometry.padding + (displayLayoutGeometry.edgeInset || 0);
    const maxRight = displayLayoutGeometry.frameWidth - insetPadding;
    const maxBottom = displayLayoutGeometry.frameHeight - insetPadding;
    const targetX = clampNumber(rawTargetX, insetPadding, maxRight);
    const targetY = clampNumber(rawTargetY, insetPadding, maxBottom);
    const targetRight = Math.max(
      targetX,
      Math.min(maxRight, targetX + viewportPreview.targetWidth * displayLayoutGeometry.scale)
    );
    const targetBottom = Math.max(
      targetY,
      Math.min(maxBottom, targetY + viewportPreview.targetHeight * displayLayoutGeometry.scale)
    );
    const x = clampNumber(rawX, targetX, Math.max(targetX, targetRight - 2));
    const y = clampNumber(rawY, targetY, Math.max(targetY, targetBottom - 2));
    const width = Math.max(
      2,
      Math.min(viewportPreview.width * displayLayoutGeometry.scale, targetRight - x)
    );
    const height = Math.max(
      2,
      Math.min(viewportPreview.height * displayLayoutGeometry.scale, targetBottom - y)
    );
    return {
      x,
      y,
      width,
      height,
      interactive: viewportPreview.interactive,
    };
  }, [
    displayLayoutGeometry,
    isConnected,
    viewportPreview.height,
    viewportPreview.interactive,
    viewportPreview.left,
    viewportPreview.targetHeight,
    viewportPreview.targetLeft,
    viewportPreview.targetTop,
    viewportPreview.targetWidth,
    viewportPreview.top,
    viewportPreview.width,
    singleViewfinderFrameRect,
  ]);
  const highlightedMonitorIds =
    selectedDisplayId === ALL_DISPLAYS_ID ? [] : effectiveSelectedMonitorIds;
  const showViewportIndicator = Boolean(
    viewfinderViewportRect && previewNavigationEnabled && viewportPreview.interactive
  );
  const focusedViewfinderMonitorId = singleViewfinderFrameRect
    ? monitorSelectionId(singleViewfinderFrameRect)
    : "";
  const focusedViewfinderSelected =
    Boolean(focusedViewfinderMonitorId) &&
    highlightedMonitorIds.includes(focusedViewfinderMonitorId);
  const SidebarSection = ({ sectionId, title, children }) => (
    <Accordion
      expanded={expandedSidebarSections[sectionId]}
      onChange={(_, expanded) => {
        setExpandedSidebarSections((previous) => ({ ...previous, [sectionId]: expanded }));
      }}
      square
      disableGutters
      sx={sidebarAccordionSx}
    >
      <AccordionSummary
        expandIcon={<ExpandMoreIcon sx={{ color: NAV_COLORS.cyan }} />}
        sx={sidebarAccordionSummarySx}
      >
        <Typography
          sx={{
            fontSize: "0.85rem",
            color: NAV_COLORS.cyan,
            fontWeight: 700,
            letterSpacing: 0.3,
          }}
        >
          {title}
        </Typography>
      </AccordionSummary>
      <AccordionDetails sx={sidebarAccordionDetailsSx}>{children}</AccordionDetails>
    </Accordion>
  );
  const SidebarNavRow = ({
    icon,
    label,
    onClick,
    active = false,
    disabled = false,
    trailing = null,
    ariaLabel = null,
  }) => (
    <ListItemButton
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      aria-label={ariaLabel || label}
      sx={sidebarNavRowSx(active, disabled)}
    >
      <Box sx={{ display: "flex", alignItems: "center", minWidth: 0, flex: "1 1 auto" }}>
        <Box sx={sidebarNavIconSx(active, disabled)}>{icon}</Box>
        <ListItemText
          primary={label}
          primaryTypographyProps={{
            fontSize: "0.8rem",
            fontWeight: active ? 600 : 400,
            letterSpacing: 0.2,
            noWrap: true,
          }}
        />
      </Box>
      {trailing ? <Box sx={{ ml: 1, flexShrink: 0 }}>{trailing}</Box> : null}
    </ListItemButton>
  );
  const connectionFlowStages = useMemo(() => {
    const steps = CONNECTION_FLOW_STEPS.map((step) => ({ ...step, status: "pending" }));
    if (isConnected || vncStage === "connected") {
      return steps.map((step) => ({ ...step, status: "complete" }));
    }
    switch (vncStage) {
      case "requesting_tunnel":
      case "agent_onboarding":
        steps[0].status = "active";
        break;
      case "starting_agent_vnc":
        steps[0].status = "complete";
        steps[1].status = "active";
        break;
      case "connecting_ws":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "active";
        break;
      case "retrying":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "active";
        break;
      case "opening_guacamole":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "complete";
        steps[3].status = "active";
        break;
      case "handshaking":
      case "waiting_frame":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "complete";
        steps[3].status = "complete";
        steps[4].status = "active";
        break;
      case "auth_failed":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "complete";
        steps[3].status = "failed";
        break;
      case "error":
        if (sessionId) {
          steps[0].status = "complete";
          steps[1].status = "complete";
          steps[2].status = "failed";
        } else {
          steps[0].status = "failed";
        }
        break;
      default:
        break;
    }
    return steps;
  }, [isConnected, sessionId, vncStage]);

  const showClipboardActions = isConnected;
  const displaySettingsEnabled = isConnected;
  const showLaunchButton = !loading;
  const showConnectingStatus =
    !isConnected &&
    (loading ||
      vncStage === "requesting_tunnel" ||
      vncStage === "starting_agent_vnc" ||
      vncStage === "connecting_ws" ||
      vncStage === "opening_guacamole" ||
      vncStage === "handshaking" ||
      vncStage === "waiting_frame" ||
      vncStage === "retrying");
  const showConnectionFlow =
    !isConnected &&
    (showConnectingStatus ||
      vncStage === "agent_onboarding" ||
      vncStage === "auth_failed" ||
      vncStage === "error");

  return (
    <>
    <Box
      className="remote-desktop-shell"
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
        minWidth: 0,
        height: "100%",
        overflow: "hidden",
      }}
    >
      <Box
        sx={{
          display: "flex",
          flexDirection: { xs: "column", lg: "row" },
          flexGrow: 1,
          minHeight: 0,
          minWidth: 0,
          height: "100%",
          overflow: "hidden",
        }}
      >
        <Box
          sx={{
            ...sidebarSx,
            flexShrink: 0,
            width: { xs: "100%", lg: 260 },
            maxWidth: { xs: "100%", lg: 260 },
            borderRadius: { xs: 2.5, lg: 0 },
            borderTop: `1px solid ${NAV_COLORS.line}`,
            borderBottom: `1px solid ${NAV_COLORS.line}`,
            borderLeft: `1px solid ${NAV_COLORS.line}`,
            borderRight: { xs: `1px solid ${NAV_COLORS.line}`, lg: "none" },
            boxShadow: "none",
            height: { xs: "auto", lg: "100%" },
            overflow: "hidden",
          }}
        >
          <Box sx={{ flex: 1, overflowY: "auto" }}>
            <SidebarSection
              sectionId="display"
              title="Display & Focus"
            >
              <>
                <Box sx={{ px: 2, py: 1.25, display: "flex", flexDirection: "column", gap: 1 }}>
                <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                  Viewfinder
                </Typography>
                <Box
                  ref={viewfinderShellRef}
                  sx={{
                    position: "relative",
                    height: 126,
                    borderRadius: 2,
                    border: `1px solid ${SIDEBAR_THEME.border}`,
                    background: "rgba(7,12,23,0.82)",
                    overflow: "hidden",
                    opacity: displaySettingsEnabled ? 1 : 0.6,
                  }}
                >
                  <Box
                    ref={viewfinderRef}
                    onPointerDown={previewNavigationEnabled ? handlePreviewPointerDown : undefined}
                    onPointerMove={previewNavigationEnabled ? handlePreviewPointerMove : undefined}
                    onPointerUp={previewNavigationEnabled ? endPreviewDrag : undefined}
                    onPointerCancel={previewNavigationEnabled ? endPreviewDrag : undefined}
                    onLostPointerCapture={previewNavigationEnabled ? endPreviewDrag : undefined}
                    sx={{
                      position: "absolute",
                      inset: 10,
                      overflow: "hidden",
                      cursor: previewNavigationEnabled
                        ? viewportPreview.interactive && displayMode !== "fit"
                          ? "crosshair"
                          : "default"
                        : "default",
                      touchAction: previewNavigationEnabled ? "none" : "auto",
                    }}
                  >
                    {isConnected && singleViewfinderFrameRect ? (
                      <Box
                        sx={{
                          position: "absolute",
                          left: "50%",
                          top: "50%",
                          width: singleViewfinderFrameRect.widthPx,
                          height: singleViewfinderFrameRect.heightPx,
                          transform: "translate(-50%, -50%)",
                        }}
                      >
                        <Box
                          sx={{
                            position: "absolute",
                            inset: 0,
                            appearance: "none",
                            boxSizing: "border-box",
                            borderRadius: 1.5,
                            overflow: "hidden",
                            display: "flex",
                            alignItems: "center",
                            justifyContent: "center",
                            fontSize: "1rem",
                            fontWeight: 700,
                            cursor: focusedViewfinderMonitorId ? "pointer" : "default",
                            color: focusedViewfinderSelected ? "#08111f" : SIDEBAR_THEME.text,
                            border: focusedViewfinderSelected
                              ? "1px solid rgba(125, 201, 255, 0.42)"
                              : `1px solid ${SIDEBAR_THEME.border}`,
                            background: focusedViewfinderSelected
                              ? "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)"
                              : singleViewfinderFrameRect.primary
                                ? "linear-gradient(180deg, rgba(125,183,255,0.18), rgba(125,183,255,0.18))"
                                : "linear-gradient(180deg, rgba(148,163,184,0.14), rgba(148,163,184,0.14))",
                            boxShadow: focusedViewfinderSelected
                              ? "0 10px 28px rgba(91,126,255,0.25)"
                              : "none",
                            transition: "all 140ms ease",
                            p: 0,
                          }}
                          component="button"
                          type="button"
                          data-viewfinder-display-button="true"
                          onClick={(event) => {
                            event.stopPropagation();
                            if (focusedViewfinderMonitorId) {
                              selectDisplay(focusedViewfinderMonitorId);
                            }
                          }}
                          onDoubleClick={(event) => {
                            event.stopPropagation();
                            selectDisplay(ALL_DISPLAYS_ID);
                          }}
                        >
                          <Box
                            component="canvas"
                            ref={(node) => registerViewfinderCanvas(singleViewfinderFrameRect.id, node)}
                            sx={{
                              position: "absolute",
                              inset: 0,
                              width: "100%",
                              height: "100%",
                              display: "block",
                            }}
                          />
                          <Box
                            sx={{
                              position: "absolute",
                              inset: 0,
                              background:
                                "linear-gradient(135deg, rgba(125,201,255,0.08), rgba(177,149,255,0.05))",
                              pointerEvents: "none",
                            }}
                          />
                          <Box sx={{ position: "relative", zIndex: 1 }}>
                            {singleViewfinderFrameRect.label}
                          </Box>
                        </Box>
                        {showViewportIndicator ? (
                          <Box
                            sx={{
                              position: "absolute",
                              boxSizing: "border-box",
                              left: viewfinderViewportRect.x - singleViewfinderFrameRect.x,
                              top: viewfinderViewportRect.y - singleViewfinderFrameRect.y,
                              width: viewfinderViewportRect.width,
                              height: viewfinderViewportRect.height,
                              borderRadius: 1.5,
                              border: "2px solid rgba(125, 201, 255, 0.98)",
                              background:
                                "linear-gradient(135deg, rgba(125,201,255,0.16), rgba(177,149,255,0.08))",
                              boxShadow:
                                "0 0 0 1px rgba(8,17,31,0.62), inset 0 0 0 1px rgba(255,255,255,0.08)",
                              pointerEvents: "none",
                            }}
                          />
                        ) : null}
                      </Box>
                    ) : isConnected
                      ? displayLayoutFrames.map((item) => {
                          const monitorId = monitorSelectionId(item);
                          const selected = highlightedMonitorIds.includes(monitorId);
                          return (
                            <Box
                              key={item.id}
                              component="button"
                              type="button"
                              data-viewfinder-display-button="true"
                              onClick={(event) => {
                                event.stopPropagation();
                                selectDisplay(monitorId);
                              }}
                              sx={{
                                position: "absolute",
                                appearance: "none",
                                boxSizing: "border-box",
                                left: item.x,
                                top: item.y,
                                width: item.widthPx,
                                height: item.heightPx,
                                maxWidth: "100%",
                                maxHeight: "100%",
                                borderRadius: 1.5,
                                overflow: "hidden",
                                display: "flex",
                                alignItems: "center",
                                justifyContent: "center",
                                fontSize: "1rem",
                                fontWeight: 700,
                                cursor: "pointer",
                                color: selected ? "#08111f" : SIDEBAR_THEME.text,
                                border: selected
                                  ? "1px solid rgba(125, 201, 255, 0.42)"
                                  : `1px solid ${SIDEBAR_THEME.border}`,
                                background: selected
                                  ? "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)"
                                  : item.primary
                                    ? "rgba(125,183,255,0.18)"
                                    : "rgba(148,163,184,0.14)",
                                boxShadow: selected
                                  ? "0 10px 28px rgba(91,126,255,0.25)"
                                  : "none",
                                transition: "all 140ms ease",
                                p: 0,
                              }}
                          >
                            <Box
                              component="canvas"
                              ref={(node) => registerViewfinderCanvas(item.id, node)}
                              sx={{
                                position: "absolute",
                                inset: 0,
                                width: "100%",
                                height: "100%",
                                display: "block",
                              }}
                            />
                            <Box
                              sx={{
                                position: "absolute",
                                inset: 0,
                                background:
                                  "linear-gradient(135deg, rgba(125,201,255,0.08), rgba(177,149,255,0.05))",
                                pointerEvents: "none",
                              }}
                            />
                            <Box sx={{ position: "relative", zIndex: 1 }}>
                              {item.label}
                            </Box>
                          </Box>
                        );
                      })
                      : null}
                    {isConnected && showViewportIndicator && !singleViewfinderFrameRect ? (
                      <Box
                        sx={{
                          position: "absolute",
                          boxSizing: "border-box",
                          left: viewfinderViewportRect.x,
                          top: viewfinderViewportRect.y,
                          width: viewfinderViewportRect.width,
                          height: viewfinderViewportRect.height,
                          borderRadius: 1.5,
                          border: "2px solid rgba(125, 201, 255, 0.98)",
                          background: "linear-gradient(135deg, rgba(125,201,255,0.18), rgba(177,149,255,0.14))",
                          boxShadow:
                            "0 0 0 1px rgba(8,17,31,0.62), inset 0 0 0 1px rgba(255,255,255,0.08)",
                          pointerEvents: "none",
                        }}
                      />
                    ) : null}
                  </Box>
                </Box>
                </Box>
                <Divider sx={{ borderColor: NAV_COLORS.line, mx: 2 }} />
                <SidebarNavRow
                  icon={<FitScreenIcon fontSize="small" />}
                  label="Fit"
                  active={displaySettingsEnabled && displayMode === "fit"}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplayMode("fit")}
                />
                <SidebarNavRow
                  icon={<SwapHorizIcon fontSize="small" />}
                  label="Scaled"
                  active={displaySettingsEnabled && allDisplaysSelected && displayMode === "scaled"}
                  disabled={!displaySettingsEnabled || singleDisplaySelected}
                  onClick={() => {
                    if (allDisplaysSelected) {
                      setDisplayMode("scaled");
                    }
                  }}
                />
                <SidebarNavRow
                  icon={<DesktopIcon fontSize="small" />}
                  label={displaySelectorLabel}
                  active={displaySettingsEnabled && selectedDisplayId !== ALL_DISPLAYS_ID}
                  disabled={!displaySettingsEnabled}
                  onClick={queueDisplayFocus}
                  trailing={
                    <ExpandMoreIcon
                      sx={{
                        color: displaySettingsEnabled ? NAV_COLORS.cyan : "rgba(143,191,255,0.35)",
                        transform: "rotate(180deg)",
                        transition: "transform 140ms ease",
                      }}
                    />
                  }
                  ariaLabel="Choose display"
                />
                <Box sx={{ pb: 0.5, pl: 1.5 }}>
                  {displaySelectorOptions.map((option) => (
                    <SidebarNavRow
                      key={option.id}
                      icon={<DesktopIcon fontSize="small" />}
                      label={option.label}
                      active={displaySettingsEnabled && selectedDisplayId === option.id}
                      disabled={!displaySettingsEnabled}
                      onClick={() => selectDisplay(option.id)}
                    />
                  ))}
                </Box>
              </>
            </SidebarSection>

            <SidebarSection
              sectionId="clipboard"
              title="Clipboard & Keys"
            >
              <>
              <SidebarNavRow
                icon={<ClipboardIcon fontSize="small" />}
                label="Sync Clipboard"
                active={false}
                disabled={!showClipboardActions}
                onClick={handleClipboardSyncAttempt}
                trailing={
                  <Switch
                    checked={false}
                    onClick={(event) => {
                      event.stopPropagation();
                      handleClipboardSyncAttempt();
                    }}
                    onChange={(event) => {
                      event.preventDefault();
                      setClipboardSync(false);
                    }}
                    disabled={!showClipboardActions}
                    size="small"
                    color="info"
                  />
                }
              />
              <SidebarNavRow
                icon={<KeyboardIcon fontSize="small" />}
                label="Inject Keystrokes"
                disabled={!showClipboardActions}
                onClick={() => injectClipboardKeystrokes()}
              />
              <SidebarNavRow
                icon={<KeyboardCommandKeyIcon fontSize="small" />}
                label="Send Ctrl+Alt+Del"
                disabled={!showClipboardActions}
                onClick={handleCtrlAltDel}
              />
              </>
            </SidebarSection>

            <SidebarSection
              sectionId="session"
              title="Session Control"
            >
              <>
              <SidebarNavRow
                icon={<StopIcon fontSize="small" />}
                label="Disconnect"
                disabled={!isConnected}
                onClick={handleDisconnect}
              />
              <Box sx={{ px: 1.5, pt: 1, pb: 1.25 }}>
                <Stack
                  direction="row"
                  alignItems="center"
                  justifyContent="space-between"
                  sx={{ mb: 0.5 }}
                >
                  <Typography
                    variant="caption"
                    sx={{ color: SIDEBAR_THEME.muted, fontWeight: 700 }}
                  >
                    Speed
                  </Typography>
                  <Typography
                    variant="caption"
                    sx={{ color: SIDEBAR_THEME.muted, fontWeight: 700 }}
                  >
                    Quality
                  </Typography>
                </Stack>
                <Slider
                  value={performancePreference}
                  min={PERFORMANCE_PREFERENCE_MIN}
                  max={PERFORMANCE_PREFERENCE_MAX}
                  step={1}
                  size="small"
                  onChange={handlePerformancePreferenceChange}
                  onChangeCommitted={handlePerformancePreferenceCommitted}
                  aria-label="Remote desktop speed quality preference"
                  sx={{
                    color: NAV_COLORS.cyan,
                    height: 4,
                    px: 0.25,
                    "& .MuiSlider-rail": {
                      opacity: 0.28,
                      backgroundColor: "rgba(148,163,184,0.75)",
                    },
                    "& .MuiSlider-track": {
                      border: 0,
                      background:
                        "linear-gradient(90deg, rgba(125,183,255,0.95), rgba(192,132,252,0.9))",
                    },
                    "& .MuiSlider-thumb": {
                      width: 14,
                      height: 14,
                      backgroundColor: "#dbeafe",
                      border: "1px solid rgba(125,183,255,0.72)",
                      boxShadow: "0 0 0 4px rgba(125,183,255,0.12)",
                      "&:hover, &.Mui-focusVisible": {
                        boxShadow: "0 0 0 6px rgba(125,183,255,0.18)",
                      },
                    },
                  }}
                />
              </Box>
              </>
            </SidebarSection>
          </Box>

          <Box sx={{ px: 1, pb: 1, pt: 0.5 }}>
            <Box
              component="button"
              type="button"
              onClick={handleReturnToDevice}
              title={deviceHostname}
              sx={{
                width: "100%",
                height: 28,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                gap: 0.75,
                px: 1,
                background: "rgba(255,255,255,0.04)",
                border: `1px solid ${NAV_COLORS.line}`,
                borderRadius: 6,
                color: NAV_COLORS.cyan,
                cursor: "pointer",
                transition: "background 160ms ease, transform 120ms ease",
                "&:hover": {
                  background: "rgba(255,255,255,0.08)",
                },
                "&:active": {
                  transform: "translateY(1px)",
                },
              }}
            >
              <ChevronLeftIcon fontSize="small" />
              <Typography
                sx={{
                  color: NAV_COLORS.cyan,
                  fontSize: "0.8rem",
                  fontWeight: 600,
                  lineHeight: 1.1,
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                }}
              >
                {deviceHostname}
              </Typography>
            </Box>
          </Box>

        </Box>

        <Box
          sx={{
            display: "flex",
            flexDirection: "column",
            flexGrow: 1,
            minHeight: 0,
            minWidth: 0,
            overflow: "hidden",
          }}
        >
          <Box
            ref={containerRef}
            sx={{
              flexGrow: 1,
              minHeight: { xs: 420, lg: 0 },
              display: "flex",
              flexDirection: "column",
              ...glassCardSx,
              overflow: "hidden",
              position: "relative",
              borderRadius: { xs: 3, lg: 0 },
              borderTop: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRight: `1px solid ${MAGIC_UI.panelBorder}`,
              borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
              borderLeft: { xs: `1px solid ${MAGIC_UI.panelBorder}`, lg: "none" },
              boxShadow: { xs: "0 25px 80px rgba(2,6,23,0.6)", lg: "none" },
            }}
          >
            {loading ? <LinearProgress color="info" sx={{ height: 3 }} /> : null}
            <Box
              sx={{
                flexGrow: 1,
                width: "100%",
                height: "100%",
                position: "relative",
                backgroundColor: "rgba(2,6,20,0.9)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                overflow: "hidden",
              }}
            >
              <Box
                sx={{
                  width: "100%",
                  height: "100%",
                  display: "block",
                  position: "relative",
                  overflow: "hidden",
                  minWidth: 0,
                  minHeight: 0,
                }}
              >
                <Box
                  ref={displayScrollRef}
                  sx={{
                    width: "100%",
                    height: "100%",
                    position: "relative",
                    overflow: "hidden",
                    minWidth: 0,
                    minHeight: 0,
                  }}
                >
                  <Box
                    ref={displayRef}
                    role="application"
                    aria-label="Remote desktop input surface"
                    tabIndex={0}
                    onClick={queueDisplayFocus}
                    onFocus={focusDisplaySurface}
                    onMouseDown={queueDisplayFocus}
                    onPointerDown={queueDisplayFocus}
                    sx={{
                      width: "100%",
                      height: "100%",
                      display: "block",
                      position: "relative",
                      overflow: "hidden",
                      minWidth: 0,
                      minHeight: 0,
                      outline: "none",
                      "& canvas": {
                        display: "block",
                        flex: "0 0 auto",
                        maxWidth: "none",
                        maxHeight: "none",
                        cursor: `${VNC_OPERATOR_CURSOR} !important`,
                        boxShadow: VNC_CANVAS_BOX_SHADOW,
                      },
                    }}
                  />
                </Box>
              </Box>
              {!isConnected ? (
                <Stack
                  spacing={1.5}
                  sx={{
                    position: "absolute",
                    alignItems: "center",
                    textAlign: "center",
                    px: 2,
                    width: "100%",
                    maxWidth: 380,
                    "@keyframes remoteDesktopFlowSpin": {
                      from: { transform: "rotate(0deg)" },
                      to: { transform: "rotate(360deg)" },
                    },
                    "@keyframes remoteDesktopFlowPulse": {
                      "0%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0.28)" },
                      "70%": { boxShadow: "0 0 0 10px rgba(125, 201, 255, 0)" },
                      "100%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0)" },
                    },
                  }}
                >
                  <DesktopIcon sx={{ color: MAGIC_UI.accentA, fontSize: 40 }} />
                  <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                    {showConnectingStatus
                      ? "Connecting..."
                      : guacamoleAvailable
                        ? "Ready to Connect"
                        : "Remote Desktop Unavailable"}
                  </Typography>
                  {!showConnectingStatus && !guacamoleAvailable ? (
                    <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.86rem", maxWidth: 320 }}>
                      Apache Guacamole is not available{guacamoleAvailability.reason ? `: ${guacamoleAvailability.reason}` : "."}
                    </Typography>
                  ) : null}
                  {showConnectionFlow ? (
                    <Box
                      sx={{
                        width: "100%",
                        display: "flex",
                        justifyContent: "center",
                        px: 1,
                      }}
                    >
                      <Box
                        sx={{
                          width: "100%",
                          maxWidth: 340,
                          display: "flex",
                          flexDirection: "column",
                          alignItems: "flex-start",
                        }}
                      >
                        {connectionFlowStages.map((step, index) => {
                          const isLast = index === connectionFlowStages.length - 1;
                          const iconColor =
                            step.status === "complete"
                              ? MAGIC_UI.accentC
                              : step.status === "active"
                                ? MAGIC_UI.accentA
                                : step.status === "failed"
                                  ? "#ff7b89"
                                  : "rgba(148, 163, 184, 0.58)";
                          const labelColor =
                            step.status === "pending" ? MAGIC_UI.textMuted : MAGIC_UI.textBright;
                          const detailColor =
                            step.status === "failed"
                              ? "#ff7b89"
                              : step.status === "active"
                                ? MAGIC_UI.accentA
                                : MAGIC_UI.textMuted;
                          const connectorColor =
                            step.status === "complete"
                              ? "linear-gradient(180deg, rgba(52,211,153,0.9), rgba(52,211,153,0.16))"
                              : step.status === "active"
                                ? "linear-gradient(180deg, rgba(125,201,255,0.95), rgba(125,201,255,0.18))"
                                : step.status === "failed"
                                  ? "linear-gradient(180deg, rgba(255,123,137,0.9), rgba(255,123,137,0.18))"
                                  : "rgba(148,163,184,0.18)";
                          const showStepDetail = step.status === "active" || step.status === "failed";
                          return (
                            <Box
                              key={step.id}
                              sx={{
                                display: "flex",
                                alignItems: "stretch",
                                gap: 1.25,
                                width: "100%",
                              }}
                            >
                              <Box
                                sx={{
                                  width: 22,
                                  display: "flex",
                                  flexDirection: "column",
                                  alignItems: "center",
                                  flexShrink: 0,
                                }}
                              >
                                <Box
                                  sx={{
                                    width: 20,
                                    height: 20,
                                    display: "flex",
                                    alignItems: "center",
                                    justifyContent: "center",
                                    color: iconColor,
                                    borderRadius: "50%",
                                    ...(step.status === "active"
                                      ? {
                                          background: "rgba(125, 201, 255, 0.12)",
                                          animation: "remoteDesktopFlowPulse 1.8s ease-out infinite",
                                        }
                                      : null),
                                  }}
                                >
                                  {step.status === "complete" ? (
                                    <StageCompleteIcon sx={{ fontSize: 18 }} />
                                  ) : step.status === "active" ? (
                                    <StageActiveIcon
                                      sx={{
                                        fontSize: 18,
                                        animation: "remoteDesktopFlowSpin 1.15s linear infinite",
                                      }}
                                    />
                                  ) : step.status === "failed" ? (
                                    <StageErrorIcon sx={{ fontSize: 18 }} />
                                  ) : (
                                    <StagePendingIcon sx={{ fontSize: 18 }} />
                                  )}
                                </Box>
                                {!isLast ? (
                                  <Box
                                    sx={{
                                      mt: 0.4,
                                      mb: 0.2,
                                      width: 2,
                                      flexGrow: 1,
                                      minHeight: showStepDetail ? 22 : 14,
                                      borderRadius: 999,
                                      background: connectorColor,
                                    }}
                                  />
                                ) : null}
                              </Box>
                              <Box
                                sx={{
                                  pt: 0.05,
                                  pb: isLast ? 0 : 0.65,
                                  minWidth: 0,
                                  textAlign: "left",
                                }}
                              >
                                <Typography
                                  sx={{
                                    color: labelColor,
                                    fontSize: "0.85rem",
                                    fontWeight:
                                      step.status === "active" || step.status === "complete" ? 600 : 500,
                                    letterSpacing: 0.2,
                                  }}
                                >
                                  {step.label}
                                </Typography>
                                {showStepDetail ? (
                                  <Typography
                                    sx={{
                                      mt: 0.2,
                                      color: detailColor,
                                      fontSize: "0.74rem",
                                      lineHeight: 1.35,
                                      maxWidth: 280,
                                    }}
                                  >
                                    {step.detail}
                                  </Typography>
                                ) : null}
                              </Box>
                            </Box>
                          );
                        })}
                      </Box>
                    </Box>
                  ) : null}
                  {showLaunchButton ? (
                    <Button
                      size="medium"
                      startIcon={<PlayIcon />}
                      variant="outlined"
                      sx={primaryHeroActionSx}
                      disabled={!agentId || !guacamoleAvailable}
                      onClick={handleConnect}
                    >
                      {sessionId ? "Reconnect Remote Desktop" : "Launch Remote Desktop"}
                    </Button>
                  ) : null}
                </Stack>
              ) : null}
            </Box>
          </Box>
        </Box>
      </Box>
    </Box>
    <Dialog
      open={clipboardNotImplementedOpen}
      onClose={() => setClipboardNotImplementedOpen(false)}
      maxWidth="xs"
      fullWidth
      PaperProps={{ sx: DIALOG_PAPER_SX }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock title="Not Implemented Yet" />
      </DialogTitle>
      <DialogContent sx={DIALOG_CONTENT_SX}>
        <Typography sx={DIALOG_BODY_TEXT_SX}>
          Not Implemented Yet: This feature is not yet functional with the new Guacamole-based remote desktop system.
        </Typography>
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={() => setClipboardNotImplementedOpen(false)} sx={DIALOG_BUTTON_SX}>
          Close
        </Button>
      </DialogActions>
    </Dialog>
    </>
  );
}
