import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { useUrlTabState } from "@/app/hooks/useUrlTabState.js";

function UrlTabHarness() {
  const { activeKey, setActiveKey } = useUrlTabState({
    defaultKey: "summary",
    allowedKeys: ["summary", "history"],
    keyByUrl: {
      device_summary: "summary",
      activity_history: "history",
    },
    urlByKey: {
      summary: "device_summary",
      history: "activity_history",
    },
  });

  return (
    <>
      <div data-testid="active-key">{activeKey}</div>
      <button type="button" onClick={() => setActiveKey("summary")}>
        Summary
      </button>
      <button type="button" onClick={() => setActiveKey("history")}>
        History
      </button>
    </>
  );
}

describe("useUrlTabState", () => {
  it("hydrates from the query string and writes back using the serialized value", async () => {
    const router = createMemoryRouter(
      [{ path: "/devices/:deviceId", element: <UrlTabHarness /> }],
      {
        initialEntries: ["/devices/agent-1?tab=device_summary"],
      }
    );

    render(<RouterProvider router={router} />);

    expect(screen.getByTestId("active-key")).toHaveTextContent("summary");

    fireEvent.click(screen.getByRole("button", { name: "History" }));

    expect(router.state.location.search).toBe("?tab=activity_history");
  });

  it("does not rewrite the query string when the requested tab is already active", async () => {
    const router = createMemoryRouter(
      [{ path: "/devices/:deviceId", element: <UrlTabHarness /> }],
      {
        initialEntries: ["/devices/agent-1?tab=activity_history"],
      }
    );

    render(<RouterProvider router={router} />);

    expect(screen.getByTestId("active-key")).toHaveTextContent("history");

    fireEvent.click(screen.getByRole("button", { name: "History" }));

    expect(router.state.location.search).toBe("?tab=activity_history");
  });
});
