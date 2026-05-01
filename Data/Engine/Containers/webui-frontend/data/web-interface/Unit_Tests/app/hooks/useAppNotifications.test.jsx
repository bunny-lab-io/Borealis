import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useAppNotifications } from "@/app/hooks/useAppNotifications.js";

const postAppNotificationMock = vi.fn();

vi.mock("@/app/utils/notifications.js", () => ({
  postAppNotification: (...args) => postAppNotificationMock(...args),
}));

function NotificationHarness() {
  const notify = useAppNotifications({
    title: "Sites",
    icon: "locationcity",
  });

  return (
    <button
      type="button"
      onClick={() => {
        void notify("Sites refreshed");
      }}
    >
      Notify
    </button>
  );
}

describe("useAppNotifications", () => {
  it("merges defaults with string messages", async () => {
    postAppNotificationMock.mockResolvedValue(undefined);

    render(<NotificationHarness />);
    fireEvent.click(screen.getByRole("button", { name: "Notify" }));

    expect(postAppNotificationMock).toHaveBeenCalledWith({
      title: "Sites",
      icon: "locationcity",
      variant: "info",
      message: "Sites refreshed",
    });
  });
});
