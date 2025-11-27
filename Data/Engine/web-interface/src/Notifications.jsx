import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, Typography } from "@mui/material";
import {
  NotificationsActive as NotificationsActiveIcon,
  InfoOutlined as InfoOutlinedIcon,
  CheckCircle as CheckCircleIcon,
  WarningAmber as WarningAmberIcon,
  ErrorOutline as ErrorOutlineIcon,
  Schedule as ScheduleIcon,
  FilterAlt as FilterAltIcon,
  CloudDone as CloudDoneIcon,
  Update as UpdateIcon,
  DeviceHub as DeviceHubIcon,
  GitHub as GitHubIcon,
  Group as GroupIcon,
  ReceiptLong as ReceiptLongIcon,
  Apps as AppsIcon,
  Code as CodeIcon,
  PendingActions as PendingActionsIcon,
  LocationCity as LocationCityIcon,
} from "@mui/icons-material";

const ICON_MAP = {
  notification: NotificationsActiveIcon,
  notifications: NotificationsActiveIcon,
  notificationsactive: NotificationsActiveIcon,
  info: InfoOutlinedIcon,
  success: CheckCircleIcon,
  check: CheckCircleIcon,
  warning: WarningAmberIcon,
  error: ErrorOutlineIcon,
  alert: ErrorOutlineIcon,
  schedule: ScheduleIcon,
  clock: ScheduleIcon,
  job: ScheduleIcon,
  filter: FilterAltIcon,
  filters: FilterAltIcon,
  done: CloudDoneIcon,
  synced: CloudDoneIcon,
  update: UpdateIcon,
  device: DeviceHubIcon,
  github: GitHubIcon,
  git: GitHubIcon,
  user: GroupIcon,
  users: GroupIcon,
  group: GroupIcon,
  groups: GroupIcon,
  people: GroupIcon,
  log: ReceiptLongIcon,
  logs: ReceiptLongIcon,
  receipt: ReceiptLongIcon,
  receiptlong: ReceiptLongIcon,
  app: AppsIcon,
  apps: AppsIcon,
  assembly: AppsIcon,
  assemblies: AppsIcon,
  code: CodeIcon,
  dev: CodeIcon,
  developer: CodeIcon,
  pendingactions: PendingActionsIcon,
  queue: PendingActionsIcon,
  locationcity: LocationCityIcon,
  site: LocationCityIcon,
  sites: LocationCityIcon,
};

const THEMES = {
  info: {
    background:
      "linear-gradient(135deg, rgba(19,27,48,0.96) 0%, rgba(25,36,60,0.94) 40%, rgba(32,52,82,0.9) 68%, rgba(44,70,104,0.86) 100%)",
    glow: "0 12px 30px rgba(4, 7, 17, 0.55), 0 0 0 1px rgba(126, 192, 255, 0.14)",
    border: "1px solid rgba(148, 208, 255, 0.18)",
    text: "#dfe8fa",
    icon: "#c5ddff",
    accentA: "rgba(125, 211, 252, 0.08)",
    accentB: "rgba(192, 132, 252, 0.07)",
  },
  warning: {
    background:
      "linear-gradient(135deg, rgba(44,35,10,0.95) 0%, rgba(54,42,14,0.92) 36%, rgba(64,50,18,0.9) 68%, rgba(78,60,20,0.86) 100%)",
    glow: "0 12px 30px rgba(18,12,4,0.6), 0 0 0 1px rgba(255, 210, 125, 0.16)",
    border: "1px solid rgba(255, 219, 128, 0.24)",
    text: "#f6edd8",
    icon: "#ffe7a3",
    accentA: "rgba(255, 214, 140, 0.1)",
    accentB: "rgba(255, 186, 92, 0.08)",
  },
  error: {
    background:
      "linear-gradient(135deg, rgba(48,16,18,0.95) 0%, rgba(62,20,24,0.92) 36%, rgba(78,26,30,0.9) 68%, rgba(94,30,34,0.86) 100%)",
    glow: "0 12px 30px rgba(18,4,4,0.6), 0 0 0 1px rgba(255, 158, 158, 0.16)",
    border: "1px solid rgba(255, 158, 158, 0.22)",
    text: "#f9e2e2",
    icon: "#ffc4c4",
    accentA: "rgba(255, 163, 163, 0.08)",
    accentB: "rgba(255, 116, 116, 0.08)",
  },
};

function themeForVariant(variant) {
  return THEMES[variant] || THEMES.info;
}

const toKey = (value) => (value || "").toString().replace(/[^a-z0-9]/gi, "").toLowerCase();

function resolveIcon(name) {
  const mapped = ICON_MAP[toKey(name)];
  return mapped || NotificationsActiveIcon;
}

function NotificationCard({ item, index }) {
  const Icon = useMemo(() => resolveIcon(item.icon), [item.icon]);
  const delay = Math.min(index * 40, 260);
  const animationName = item.closing ? "fadeOut" : "dropIn";
  const theme = themeForVariant(item.variant);

  return (
    <Box
      sx={{
        width: 360,
        maxWidth: "92vw",
        display: "flex",
        gap: 1.5,
        alignItems: "center",
        backgroundImage: theme.background,
        borderRadius: 2.5,
        boxShadow: theme.glow,
        border: theme.border,
        backdropFilter: "blur(14px)",
        color: theme.text,
        p: 1.6,
        opacity: item.closing ? 1 : 0,
        transform: item.closing ? "translateY(0) scale(1)" : "translateY(-10px) scale(0.98)",
        animation: `${animationName} 0.45s ease forwards`,
        animationDelay: `${delay}ms`,
        position: "relative",
        overflow: "hidden",
        "&::after": {
          content: "\"\"",
          position: "absolute",
          inset: 0,
          background:
            `radial-gradient(120% 120% at 8% 8%, ${theme.accentA}, transparent 52%), ` +
            `radial-gradient(120% 120% at 80% 10%, ${theme.accentB}, transparent 60%)`,
          pointerEvents: "none",
        },
      }}
    >
      <Box
        sx={{
          width: 34,
          height: 34,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: theme.icon,
        }}
      >
        <Icon sx={{ fontSize: 26, filter: "drop-shadow(0 2px 6px rgba(0,0,0,0.35))" }} />
      </Box>
      <Box sx={{ display: "flex", flexDirection: "column", gap: 0.25, minWidth: 0 }}>
        <Typography
          variant="subtitle2"
          sx={{
            color: theme.text,
            fontWeight: 700,
            letterSpacing: 0.2,
            lineHeight: 1.15,
            textShadow: "0 1px 0 rgba(0,0,0,0.2)",
          }}
          noWrap
        >
          {item.title || "Notification"}
        </Typography>
        <Typography
          variant="body2"
          sx={{
            color: theme.text,
            opacity: 0.9,
            lineHeight: 1.35,
            whiteSpace: "pre-line",
          }}
        >
          {item.message}
        </Typography>
      </Box>
    </Box>
  );
}

export default function Notifications({ socket, currentUser }) {
  const [items, setItems] = useState([]);
  const timeouts = useRef({});
  const normalizedUser = toKey(currentUser);

  const clearTimeoutFor = useCallback((id) => {
    const handle = timeouts.current[id];
    if (handle) {
      clearTimeout(handle);
      delete timeouts.current[id];
    }
  }, []);

  const scheduleRemoval = useCallback((id, ttlMs) => {
    clearTimeoutFor(id);
    const safeTtl = Math.max(1000, ttlMs || 5200);
    const exitDuration = 420;
    const fadeAfter = Math.max(600, safeTtl - exitDuration);
    timeouts.current[id] = setTimeout(() => {
      setItems((prev) =>
        prev.map((entry) => (entry.id === id ? { ...entry, closing: true } : entry))
      );
      timeouts.current[id] = setTimeout(() => {
        setItems((prev) => prev.filter((entry) => entry.id !== id));
        clearTimeoutFor(id);
      }, exitDuration);
    }, fadeAfter);
  }, [clearTimeoutFor]);

  const pushNotification = useCallback(
    (raw) => {
      if (!raw) return;
      const owner = toKey(raw.username);
      if (owner && normalizedUser && owner !== normalizedUser) {
        return;
      }

      const id = raw.id || `notif-${Date.now()}`;
      const entry = {
        id,
        title: raw.title || "Notification",
        message: raw.message || "",
        icon: raw.icon || "notification",
        variant: (raw.variant || raw.severity || raw.type || "info").toLowerCase(),
        created_at: raw.created_at || Math.floor(Date.now() / 1000),
        closing: false,
      };
      if (!entry.message) return;

      setItems((prev) => {
        const withoutExisting = prev.filter((item) => item.id !== id);
        return [entry, ...withoutExisting].slice(0, 5);
      });
      scheduleRemoval(id, (raw.ttl_ms && Number(raw.ttl_ms)) || 5200);
    },
    [normalizedUser, scheduleRemoval]
  );

  useEffect(() => {
    if (!socket || typeof socket.on !== "function") return;
    const handler = (data) => pushNotification(data || {});
    socket.on("borealis_notification", handler);
    return () => {
      try {
        socket.off("borealis_notification", handler);
      } catch {
        /* noop */
      }
    };
  }, [socket, pushNotification]);

  useEffect(() => () => Object.keys(timeouts.current).forEach(clearTimeoutFor), [clearTimeoutFor]);

  if (!items.length) {
    return null;
  }

  return (
    <>
      <style>{`
        @keyframes dropIn {
          0% { opacity: 0; transform: translateY(-14px) scale(0.97); }
          50% { opacity: 0.9; transform: translateY(0) scale(1.01); }
          100% { opacity: 1; transform: translateY(0) scale(1); }
        }
        @keyframes fadeOut {
          0% { opacity: 1; transform: translateY(0) scale(1); }
          100% { opacity: 0; transform: translateY(-8px) scale(0.98); }
        }
      `}</style>
      <Box
        sx={{
          position: "fixed",
          top: 18,
          right: 18,
          zIndex: 2400,
          display: "flex",
          flexDirection: "column",
          alignItems: "flex-end",
          gap: 1.25,
          pointerEvents: "none",
        }}
      >
        {items.map((item, index) => (
          <Box key={item.id} sx={{ pointerEvents: "auto" }}>
            <NotificationCard item={item} index={index} />
          </Box>
        ))}
      </Box>
    </>
  );
}
