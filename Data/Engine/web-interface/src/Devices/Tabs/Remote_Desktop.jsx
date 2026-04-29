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
  PowerSettingsNewRounded as PowerIcon,
  ExpandMore as ExpandMoreIcon,
  FitScreenRounded as FitScreenIcon,
  CropFreeRounded as CropFreeIcon,
  SwapHorizRounded as SwapHorizIcon,
  RestartAltRounded as RestartAltIcon,
  ReplayRounded as ReplayIcon,
  KeyboardCommandKeyRounded as KeyboardCommandKeyIcon,
  ChevronLeft as ChevronLeftIcon,
  CheckCircleRounded as StageCompleteIcon,
  AutorenewRounded as StageActiveIcon,
  ErrorOutlineRounded as StageErrorIcon,
  RadioButtonUncheckedRounded as StagePendingIcon,
} from "@mui/icons-material";
import { APP_PATHS } from "../../app/routes/paths.js";
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
const VNC_OPEN_TIMEOUT_MS = 30000;
const VNC_READY_STABILIZE_MS = 1800;
const VNC_UNSTABLE_THRESHOLD_MS = 5 * 60 * 1000;
const VNC_RECEIVE_TIMEOUT_MS = 6 * 60 * 1000;
const ALL_DISPLAYS_ID = "__all_displays__";
const CONNECTION_FLOW_STEPS = Object.freeze([
  {
    id: "tunnel",
    label: "Securing Tunnel",
    detail: "Requesting the WireGuard route to the remote device.",
  },
  {
    id: "service",
    label: "Checking VNC Service",
    detail: "Confirming the remote desktop service is running.",
  },
  {
    id: "socket",
    label: "Opening Desktop Socket",
    detail: "Connecting the browser to the live VNC socket.",
  },
  {
    id: "auth",
    label: "Authenticating Session",
    detail: "Negotiating credentials and display settings.",
  },
  {
    id: "ready",
    label: "Desktop Ready",
    detail: "Finalizing the live desktop stream for control.",
  },
]);

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

function topologyMatchesFramebuffer(topologyBounds, framebufferSize) {
  if (!topologyBounds || !framebufferSize?.width || !framebufferSize?.height) return true;
  const widthTolerance = Math.max(96, Math.round(framebufferSize.width * 0.18));
  const heightTolerance = Math.max(96, Math.round(framebufferSize.height * 0.18));
  return (
    Math.abs(topologyBounds.width - framebufferSize.width) <= widthTolerance &&
    Math.abs(topologyBounds.height - framebufferSize.height) <= heightTolerance
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
  const aspectRatio = bounds.width / bounds.height;
  const horizontalUtilization =
    aspectRatio >= 3
      ? 0.78
      : aspectRatio >= 2
        ? 0.84
        : 0.9;
  const verticalUtilization = 0.9;
  const scale = Math.min(
    (innerWidth * horizontalUtilization) / bounds.width,
    (innerHeight * verticalUtilization) / bounds.height
  );
  const layoutWidth = bounds.width * scale;
  const layoutHeight = bounds.height * scale;
  const offsetX = insetPadding + (innerWidth - layoutWidth) / 2;
  const offsetY = insetPadding + (innerHeight - layoutHeight) / 2;
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
      const x = clampNumber(rawX, insetPadding, maxRight);
      const y = clampNumber(rawY, insetPadding, maxBottom);
      return {
        ...item,
        x,
        y,
        widthPx: Math.max(2, Math.min(item.width * scale, maxRight - x)),
        heightPx: Math.max(2, Math.min(item.height * scale, maxBottom - y)),
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

function displayDiagramTopology(topology, framebufferSize, renderedCanvasSize) {
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

export default function RemoteDesktopPage({ device: providedDevice = null }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { deviceId } = useParams();
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
  const [expandedSidebarSections, setExpandedSidebarSections] = useState({
    display: true,
    clipboard: true,
    power: true,
    session: true,
  });
  const [selectedDisplayId, setSelectedDisplayId] = useState(ALL_DISPLAYS_ID);
  const [displaySelectorExpanded, setDisplaySelectorExpanded] = useState(false);
  const [viewportHint, setViewportHint] = useState("");
  const [displayTopology, setDisplayTopology] = useState([]);
  const [framebufferSize, setFramebufferSize] = useState({ width: 0, height: 0 });
  const [renderedCanvasSize, setRenderedCanvasSize] = useState({ width: 0, height: 0 });
  const [viewfinderSize, setViewfinderSize] = useState({ width: 0, height: 126 });
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
  const viewfinderRef = useRef(null);
  const remoteClientRef = useRef(null);
  const viewfinderCanvasRefs = useRef(new Map());
  const agentIdRef = useRef("");
  const sessionIdRef = useRef("");
  const clipboardSyncRef = useRef(clipboardSync);
  const clipboardLastRef = useRef("");
  const connectAttemptRef = useRef(0);
  const manualDisconnectRef = useRef(false);
  const forcedViewportKeyRef = useRef("");
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
  const topologyBounds = useMemo(
    () => displayTopologyBounds(normalizedDisplayTopology),
    [normalizedDisplayTopology]
  );
  const topologyTrusted = useMemo(() => {
    if (!normalizedDisplayTopology.length) return false;
    if (normalizedDisplayTopology.length <= 1) return false;
    return topologyMatchesFramebuffer(topologyBounds, framebufferSize);
  }, [framebufferSize, normalizedDisplayTopology, topologyBounds]);
  const diagramTopology = useMemo(
    () => displayDiagramTopology(normalizedDisplayTopology, framebufferSize, renderedCanvasSize),
    [framebufferSize, normalizedDisplayTopology, renderedCanvasSize]
  );
  const diagramBounds = useMemo(
    () => displayTopologyBounds(diagramTopology),
    [diagramTopology]
  );
  const displaySelectorOptions = useMemo(
    () => [
      { id: ALL_DISPLAYS_ID, label: "All", shortLabel: "All" },
      ...normalizedDisplayTopology.map((item) => ({
        id: monitorSelectionId(item),
        label: `Display ${item.label}`,
        shortLabel: item.label,
      })),
    ],
    [normalizedDisplayTopology]
  );
  const effectiveSelectedMonitorIds = useMemo(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return [];
    const availableIds = new Set(
      normalizedDisplayTopology.map((item) => monitorSelectionId(item)).filter(Boolean)
    );
    return availableIds.has(selectedDisplayId) ? [selectedDisplayId] : [];
  }, [normalizedDisplayTopology, selectedDisplayId]);
  const displaySelectorLabel = useMemo(() => {
    const selectedOption = displaySelectorOptions.find((item) => item.id === selectedDisplayId);
    return `Display: ${selectedOption?.shortLabel || "All"}`;
  }, [displaySelectorOptions, selectedDisplayId]);
  const viewfinderTopology = useMemo(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return diagramTopology;
    const filtered = diagramTopology.filter(
      (item) => monitorSelectionId(item) === selectedDisplayId
    );
    return filtered.length ? filtered : diagramTopology;
  }, [diagramTopology, selectedDisplayId]);
  const displayLayoutGeometry = useMemo(
    () =>
      buildDisplayLayoutGeometry(viewfinderTopology, {
        frameWidth: Math.max(1, Math.round(viewfinderSize.width || 256)),
        frameHeight: Math.max(1, Math.round(viewfinderSize.height || 126)),
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
    setSessionId(nextSessionId);
    setParticipantId(nextParticipantId);
    setDisplayTopology((previous) =>
      equalDisplayTopology(previous, nextDisplayTopology) ? previous : nextDisplayTopology
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
    sessionIdRef.current = sessionId;
  }, [sessionId]);

  const cancelPendingConnect = useCallback(() => {
    connectAttemptRef.current += 1;
  }, []);

  const notifyAgentOnboarding = useCallback(async () => {
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: "Agent Onboarding Underway",
          message:
            "Please wait for the agent to finish onboarding into Borealis. It takes about 1 minute to finish the process.",
          icon: "info",
          variant: "info",
        }),
      });
    } catch {
      /* ignore notification transport errors */
    }
  }, []);

  const handleAgentOnboarding = useCallback(async () => {
    await notifyAgentOnboarding();
    setStatusMessage("Agent Onboarding Underway.");
    setSessionState("idle");
    setVncStage("agent_onboarding");
  }, [notifyAgentOnboarding]);

  const resetDisconnectedViewState = useCallback(() => {
    forcedViewportKeyRef.current = "";
    setDisplayTopology([]);
    setSelectedDisplayId(ALL_DISPLAYS_ID);
    setDisplaySelectorExpanded(false);
    setDisplayMode("fit");
    setViewportHint("");
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
    if (element?.style) {
      element.style.display = "block";
      element.style.margin = "auto";
      element.style.boxShadow = VNC_CANVAS_BOX_SHADOW;
      element.style.transformOrigin = "top left";
    }
    if (display && typeof display.scale === "function") {
      const hostWidth = Math.max(1, Number(host?.clientWidth || 0));
      const hostHeight = Math.max(1, Number(host?.clientHeight || 0));
      let scale = 1;
      if (mode === "fit" && width > 0 && height > 0) {
        scale = Math.min(hostWidth / width, hostHeight / height);
      } else if (mode === "scaled" && height > 0) {
        scale = hostHeight / height;
      }
      display.scale(Number.isFinite(scale) && scale > 0 ? scale : 1);
    }
  }, []);

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
  }, [cancelPendingConnect, disconnectVnc, resetDisconnectedViewState, teardownDisplay]);

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
      teardownDisplay();
      disconnectVnc("component_unmount");
    };
  }, [cancelPendingConnect, disconnectVnc, teardownDisplay]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      throw buildRetryableError("Agent ID is required to establish.", false);
    }
    setStatusMessage("");
    setVncStage("requesting_tunnel");
    setSessionState("connecting");
    try {
      const resp = await fetch("/api/vnc/establish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: agentId, remove_wallpaper: true, viewer: "guacamole" }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        if (data?.error === "agent_socket_missing") {
          await handleAgentOnboarding();
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
  }, [agentId, applySessionBootstrap, handleAgentOnboarding]);

  const handleClipboardSyncAttempt = useCallback(() => {
    setClipboardSync(false);
    clipboardSyncRef.current = false;
    setClipboardNotImplementedOpen(true);
  }, []);

  const openGuacamoleSession = useCallback(
    async (data, options = {}) => {
      const connectToken = Number(options.connectToken || 0);
      const attempt = Number(options.attempt || 1);
      const maxAttempts = Number(options.maxAttempts || VNC_AUTO_RETRY_ATTEMPTS);
      const wsUrl = data?.guacamole_ws_url || buildWsUrl(data?.guacamole_ws_path, "");
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
      displayHost.focus?.();
      setVncStage("connecting_ws");
      setStatusMessage(
        attempt > 1
          ? `Establishing Apache Guacamole... attempt ${attempt}/${maxAttempts}`
          : "Establishing Apache Guacamole..."
      );

      const tunnel = new Guacamole.WebSocketTunnel(wsUrl);
      tunnel.receiveTimeout = Math.max(Number(tunnel.receiveTimeout || 0), VNC_RECEIVE_TIMEOUT_MS);
      tunnel.unstableThreshold = Math.max(Number(tunnel.unstableThreshold || 0), VNC_UNSTABLE_THRESHOLD_MS);
      const client = new Guacamole.Client(tunnel);
      client.__borealisViewer = "guacamole";
      const display = client.getDisplay();
      const displayElement = display.getElement();
      displayElement.style.margin = "auto";
      displayElement.style.boxShadow = VNC_CANVAS_BOX_SHADOW;
      displayHost.appendChild(displayElement);
      const mouse = new Guacamole.Mouse(displayElement);
      mouse.onmousedown = mouse.onmouseup = mouse.onmousemove = (mouseState) => {
        client.sendMouseState(mouseState, true);
      };
      const keyboard = new Guacamole.Keyboard(displayHost);
      keyboard.onkeydown = (keysym) => {
        client.sendKeyEvent(1, keysym);
        return true;
      };
      keyboard.onkeyup = (keysym) => {
        client.sendKeyEvent(0, keysym);
      };
      client.__borealisMouse = mouse;
      client.__borealisKeyboard = keyboard;
      remoteClientRef.current = client;

      return await new Promise((resolve, reject) => {
        let settled = false;
        let tunnelConnected = false;
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
          clearStableTimer();
          syncFramebufferSize(client);
          configureDisplaySurface(client, displayMode);
          syncRenderedCanvasSize(client);
          setSessionState("connected");
          setVncStage("connected");
          setStatusMessage("");
          resolve();
        };
        const finishReject = (error, retryable = true) => {
          if (settled) return;
          settled = true;
          clearTimeout(timeoutId);
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
          setVncStage("handshaking");
          setStatusMessage("Waiting for desktop video...");
          if (!stableTimerId) {
            stableTimerId = window.setTimeout(finishResolve, VNC_READY_STABILIZE_MS);
          }
        };

        client.onerror = (status) => {
          if (isStaleAttempt()) return;
          const message = status?.message || status?.code || "Guacamole connection failed.";
          setSessionState("error");
          setVncStage("error");
          setStatusMessage(String(message));
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
            setVncStage("handshaking");
          }
        };
        display.onresize = () => {
          if (isStaleAttempt()) return;
          syncFramebufferSize(client);
          configureDisplaySurface(client, displayMode);
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
      resetDisconnectedViewState,
      syncFramebufferSize,
      syncRenderedCanvasSize,
    ]
  );

  const openVncSession = useCallback(
    async (data, options = {}) => openGuacamoleSession(data, options),
    [openGuacamoleSession]
  );

  const handleConnect = useCallback(async () => {
    if (sessionState === "connected" || loading) return;
    const connectToken = connectAttemptRef.current + 1;
    connectAttemptRef.current = connectToken;
    manualDisconnectRef.current = false;
    setStatusMessage("");
    setLoading(true);
    setSessionState("connecting");
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
          await openVncSession(sessionData, {
            connectToken,
            attempt,
            maxAttempts: VNC_AUTO_RETRY_ATTEMPTS,
          });
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
      setSessionState("error");
      setStatusMessage(String(err.message || err));
      setVncStage("error");
    } finally {
      if (connectAttemptRef.current === connectToken) {
        setLoading(false);
      }
    }
  }, [loading, openVncSession, requestTunnel, sessionState, teardownDisplay]);

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
    } catch {
      setStatusMessage("Failed to send Ctrl+Alt+Del.");
    }
  }, []);

  const handlePowerAction = useCallback((_action) => {
    setStatusMessage("Power controls are unavailable through Apache Guacamole VNC.");
  }, []);

  const syncViewportSelection = useCallback((_selectionIds, _options = {}) => {
    const client = remoteClientRef.current;
    if (!client) return;
    configureDisplaySurface(client, displayMode);
    syncFramebufferSize(client);
    syncRenderedCanvasSize(client);
    setViewportHint("");
  }, [
    configureDisplaySurface,
    displayMode,
    syncFramebufferSize,
    syncRenderedCanvasSize,
  ]);

  const isConnected = sessionState === "connected";
  const previewNavigationEnabled = false;

  const navigateViewfinderPoint = useCallback(
    (_localX, _localY) => {},
    []
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
      if (typeof event.preventDefault === "function") {
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
      normalizedDisplayTopology.map((item) => monitorSelectionId(item)).filter(Boolean)
    );
    if (availableIds.has(selectedDisplayId)) return;
    setSelectedDisplayId(ALL_DISPLAYS_ID);
  }, [normalizedDisplayTopology, selectedDisplayId]);

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
    if (!host) return undefined;
    let frameId = 0;
    const syncSize = () => {
      const nextWidth = Math.max(0, Math.round(Number(host.clientWidth || 0)));
      const nextHeight = Math.max(0, Math.round(Number(host.clientHeight || 0)));
      setViewfinderSize((previous) => {
        if (previous.width === nextWidth && previous.height === nextHeight) {
          return previous;
        }
        return { width: nextWidth, height: nextHeight || previous.height || 126 };
      });
    };
    syncSize();
    const observer = new ResizeObserver(() => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      frameId = window.requestAnimationFrame(syncSize);
    });
    observer.observe(host);
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

  const shutdownSupported = false;
  const rebootSupported = false;
  const resetSupported = false;
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
  const viewfinderHelperText = previewNavigationEnabled
    ? viewportPreview.interactive
      ? "Drag the viewport or tap anywhere on the preview to recenter."
      : "The full desktop already fits inside the current viewport."
    : "";
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
  const showViewfinderHelper = Boolean(viewfinderHelperText);
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
      case "handshaking":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "complete";
        steps[3].status = "active";
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
  const showPowerButtons = isConnected;
  const displaySettingsEnabled = isConnected;
  const showLaunchButton = !loading;
  const showConnectingStatus =
    !isConnected &&
    (loading ||
      vncStage === "requesting_tunnel" ||
      vncStage === "connecting_ws" ||
      vncStage === "handshaking" ||
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
                        ? viewportPreview.interactive
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
                            boxSizing: "border-box",
                            borderRadius: 1.5,
                            overflow: "hidden",
                            display: "flex",
                            alignItems: "center",
                            justifyContent: "center",
                            fontSize: "1rem",
                            fontWeight: 700,
                            cursor: "default",
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
                              sx={{
                                position: "absolute",
                                boxSizing: "border-box",
                                left: item.x,
                                top: item.y,
                              width: item.widthPx,
                              height: item.heightPx,
                              borderRadius: 1.5,
                              overflow: "hidden",
                              display: "flex",
                              alignItems: "center",
                              justifyContent: "center",
                                fontSize: "1rem",
                                fontWeight: 700,
                                cursor: "default",
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
                {showViewfinderHelper ? (
                  <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                    {viewfinderHelperText}
                  </Typography>
                ) : null}
                {!previewNavigationEnabled && !topologyTrusted && normalizedDisplayTopology.length > 1 ? (
                  <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                    Monitor layout shown from agent telemetry. Targeted monitor selection will unlock once it matches the live desktop geometry.
                  </Typography>
                ) : null}
                {!normalizedDisplayTopology.length && framebufferSize.width > 0 && framebufferSize.height > 0 ? (
                  <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                    Showing the live framebuffer as a single display map.
                  </Typography>
                ) : null}
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
                  icon={<CropFreeIcon fontSize="small" />}
                  label="Actual"
                  active={displaySettingsEnabled && displayMode === "actual"}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplayMode("actual")}
                />
                <SidebarNavRow
                  icon={<SwapHorizIcon fontSize="small" />}
                  label="Scaled"
                  active={displaySettingsEnabled && displayMode === "scaled"}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplayMode("scaled")}
                />
                <SidebarNavRow
                  icon={<DesktopIcon fontSize="small" />}
                  label={displaySelectorLabel}
                  active={displaySettingsEnabled && selectedDisplayId !== ALL_DISPLAYS_ID}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplaySelectorExpanded((previous) => !previous)}
                  trailing={
                    <ExpandMoreIcon
                      sx={{
                        color: displaySettingsEnabled ? NAV_COLORS.cyan : "rgba(143,191,255,0.35)",
                        transform: displaySelectorExpanded ? "rotate(180deg)" : "none",
                        transition: "transform 140ms ease",
                      }}
                    />
                  }
                  ariaLabel="Choose display"
                />
                {displaySelectorExpanded ? (
                  <Box sx={{ pb: 0.5, pl: 1.5 }}>
                    {displaySelectorOptions.map((option) => (
                      <SidebarNavRow
                        key={option.id}
                        icon={<DesktopIcon fontSize="small" />}
                        label={option.label}
                        active={displaySettingsEnabled && selectedDisplayId === option.id}
                        disabled={!displaySettingsEnabled}
                        onClick={() => setSelectedDisplayId(option.id)}
                      />
                    ))}
                  </Box>
                ) : null}
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
              sectionId="power"
              title="Power"
            >
              <>
              <SidebarNavRow
                icon={<PowerIcon fontSize="small" />}
                label="Shutdown"
                disabled={!showPowerButtons || !shutdownSupported}
                onClick={() => handlePowerAction("shutdown")}
              />
              <SidebarNavRow
                icon={<RestartAltIcon fontSize="small" />}
                label="Restart"
                disabled={!showPowerButtons || !rebootSupported}
                onClick={() => handlePowerAction("reboot")}
              />
              <SidebarNavRow
                icon={<ReplayIcon fontSize="small" />}
                label="Reset"
                disabled={!showPowerButtons || !resetSupported}
                onClick={() => handlePowerAction("reset")}
              />
              </>
            </SidebarSection>

            <SidebarSection
              sectionId="session"
              title="Session Control"
            >
              <SidebarNavRow
                icon={<StopIcon fontSize="small" />}
                label="Disconnect"
                disabled={!isConnected}
                onClick={handleDisconnect}
              />
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
                    sx={{
                      width: "100%",
                      height: "100%",
                      display: "block",
                      position: "relative",
                      overflow: "hidden",
                      minWidth: 0,
                      minHeight: 0,
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
