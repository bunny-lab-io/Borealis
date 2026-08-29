import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext } from "@/app/providers/AuthContext.jsx";
import { PageChromeProvider } from "@/app/providers/PageChromeContext.jsx";
import AppShell from "@/app/shell/AppShell.jsx";

vi.mock("@/Dialogs.jsx", () => ({
  NotAuthorizedDialog: () => null,
}));

vi.mock("@/Navigation_Sidebar.jsx", () => ({
  default: () => <div>Sidebar</div>,
}));

vi.mock("@/GlobalDeviceSearch.jsx", () => ({
  default: () => <div>Search</div>,
}));

vi.mock("@/Notifications.jsx", () => ({
  default: () => null,
}));

vi.mock("@/Page_Header_Actions.jsx", () => ({
  PageHeaderActionRail: () => null,
}));

vi.mock("@/Access_Management/Aegis_Cipher_Dialog.jsx", () => ({
  default: () => null,
}));

vi.mock("@/app/runtime/bootstrapClientRuntime.js", () => ({
  getBorealisSocket: () => null,
}));

function buildAuthValue() {
  return {
    user: "operator",
    displayName: "Operator",
    mfaEnabled: true,
    passkeyCount: 1,
    logout: vi.fn(),
    registerPasskey: vi.fn(),
    listPasskeys: vi.fn(),
    updatePasskeyLabel: vi.fn(),
    deletePasskey: vi.fn(),
    resetPassword: vi.fn(),
    resetMfa: vi.fn(),
    aegisDialog: null,
    closeAegisDialog: vi.fn(),
    completeAegisDialog: vi.fn(),
    ready: true,
    isAdmin: true,
    isAuthenticated: true,
  };
}

describe("AppShell buffered navigation", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("keeps the current page visible while the next route loader is pending", async () => {
    let resolveSlowRoute;
    const router = createMemoryRouter(
      [
        {
          path: "/",
          element: (
            <AuthContext.Provider value={buildAuthValue()}>
              <PageChromeProvider>
                <AppShell />
              </PageChromeProvider>
            </AuthContext.Provider>
          ),
          children: [
            {
              index: true,
              element: <div>Home Page</div>,
              handle: {
                title: "Home",
                breadcrumb: "Home",
                navKey: "home",
                pageKey: "home",
              },
            },
            {
              path: "slow",
              loader: async () =>
                new Promise((resolve) => {
                  resolveSlowRoute = () => resolve(null);
                }),
              element: <div>Slow Page</div>,
              handle: {
                title: "Slow",
                breadcrumb: "Slow",
                navKey: "slow",
                pageKey: "slow",
              },
            },
          ],
        },
      ],
      {
        initialEntries: ["/"],
      }
    );

    render(<RouterProvider router={router} />);

    expect(screen.getByText("Home Page")).toBeInTheDocument();

    act(() => {
      void router.navigate("/slow");
    });

    await waitFor(() => {
      expect(screen.getByText("Loading Data...")).toBeInTheDocument();
    });
    expect(screen.getByText("Home Page")).toBeInTheDocument();

    await act(async () => {
      resolveSlowRoute?.();
    });

    await waitFor(() => {
      expect(screen.getByText("Slow Page")).toBeInTheDocument();
    });
  });

  it("hides the primary navigation sidebar on device shell pages", async () => {
    const router = createMemoryRouter(
      [
        {
          path: "/",
          element: (
            <AuthContext.Provider value={buildAuthValue()}>
              <PageChromeProvider>
                <AppShell />
              </PageChromeProvider>
            </AuthContext.Provider>
          ),
          children: [
            {
              path: "devices/:deviceId",
              element: <div>Device Summary</div>,
              handle: {
                title: "Device Details",
                breadcrumb: "Device",
                navKey: "devices",
                pageKey: "device",
              },
            },
            {
              path: "devices/:deviceId/remote-desktop",
              element: <div>Remote Desktop</div>,
              handle: {
                breadcrumb: "Remote Desktop",
                navKey: "devices",
                pageKey: "device-remote-desktop",
              },
            },
          ],
        },
      ],
      {
        initialEntries: ["/devices/agent-1/remote-desktop"],
      }
    );

    render(<RouterProvider router={router} />);

    expect(screen.getAllByText("Remote Desktop").length).toBeGreaterThan(0);
    expect(screen.queryByText("Sidebar")).not.toBeInTheDocument();

    await act(async () => {
      await router.navigate("/devices/agent-1");
    });

    await waitFor(() => {
      expect(screen.getByText("Device Summary")).toBeInTheDocument();
    });
    expect(screen.queryByText("Sidebar")).not.toBeInTheDocument();
  });

  it("dismisses the current cluster banner until polled banner identity changes", async () => {
    vi.useFakeTimers();
    let banner = {
      enabled: true,
      status: "Healthy",
      hmr_state: "inactive",
      active_operation: {
        kind: "node_maintenance",
        current_step: "wait_endpoint_withdrawal",
        state: "running",
      },
    };
    const fetchMock = vi.fn(async () => ({ ok: true, json: async () => banner }));
    vi.stubGlobal("fetch", fetchMock);
    const router = createMemoryRouter(
      [
        {
          path: "/",
          element: (
            <AuthContext.Provider value={buildAuthValue()}>
              <PageChromeProvider>
                <AppShell />
              </PageChromeProvider>
            </AuthContext.Provider>
          ),
          children: [{ index: true, element: <div>Home Page</div>, handle: { title: "Home", navKey: "home", pageKey: "home" } }],
        },
      ],
      { initialEntries: ["/"] }
    );

    render(<RouterProvider router={router} />);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByText(/Cluster operation node_maintenance/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByText(/Cluster operation node_maintenance/)).not.toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.queryByText(/Cluster operation node_maintenance/)).not.toBeInTheDocument();

    banner = {
      ...banner,
      active_operation: { ...banner.active_operation, current_step: "verify_quorum" },
    };
    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByText(/verify_quorum/)).toBeInTheDocument();
  });
});
