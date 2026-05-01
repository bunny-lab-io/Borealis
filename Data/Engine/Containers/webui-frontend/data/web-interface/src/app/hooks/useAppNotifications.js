import { useCallback, useMemo } from "react";
import { postAppNotification } from "../utils/notifications.js";

function normalizeDefaults(defaults = {}) {
  return {
    title: typeof defaults.title === "string" ? defaults.title : "",
    icon: typeof defaults.icon === "string" ? defaults.icon : "notification",
    variant: typeof defaults.variant === "string" ? defaults.variant : "info",
    username: typeof defaults.username === "string" ? defaults.username : undefined,
  };
}

function normalizePayload(input, overrides = {}) {
  if (typeof input === "string") {
    return {
      message: input,
      ...(overrides && typeof overrides === "object" ? overrides : {}),
    };
  }

  return {
    ...(input && typeof input === "object" ? input : {}),
    ...(overrides && typeof overrides === "object" ? overrides : {}),
  };
}

export function useAppNotifications(defaults = {}) {
  const title = typeof defaults?.title === "string" ? defaults.title : "";
  const icon = typeof defaults?.icon === "string" ? defaults.icon : "notification";
  const variant = typeof defaults?.variant === "string" ? defaults.variant : "info";
  const username = typeof defaults?.username === "string" ? defaults.username : undefined;
  const normalizedDefaults = useMemo(
    () =>
      normalizeDefaults({
        title,
        icon,
        variant,
        username,
      }),
    [icon, title, username, variant]
  );

  return useCallback(
    async (input, overrides = {}) => {
      const payload = normalizePayload(input, overrides);
      const message = typeof payload.message === "string" ? payload.message.trim() : "";
      if (!message) {
        return;
      }

      await postAppNotification({
        ...normalizedDefaults,
        ...payload,
        message,
      });
    },
    [normalizedDefaults]
  );
}
