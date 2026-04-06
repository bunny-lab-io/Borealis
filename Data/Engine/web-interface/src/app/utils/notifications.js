export async function postAppNotification({
  title,
  message,
  icon = "notification",
  variant = "info",
  username,
}) {
  try {
    await fetch("/api/notifications/notify", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify({
        title,
        message,
        icon,
        variant,
        ...(username ? { username } : {}),
      }),
    });
  } catch {
    /* notifications are best-effort */
  }
}
