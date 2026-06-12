import React from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthContext, AuthProvider, useAuth } from "@/app/providers/AuthContext.jsx";
import LoginRoute from "@/app/routes/LoginRoute.jsx";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

function renderLoginRoute(authValue) {
  const router = createMemoryRouter(
    [
      {
        path: "/login",
        element: (
          <AuthContext.Provider value={authValue}>
            <LoginRoute />
          </AuthContext.Provider>
        ),
      },
      { path: "/sites", element: <div>Sites Page</div> },
    ],
    { initialEntries: ["/login"] }
  );

  render(<RouterProvider router={router} />);
  return router;
}

function BootstrapProbe() {
  const { ready, bootstrapState } = useAuth();
  return (
    <div data-testid="bootstrap-state">
      {`${ready}:${bootstrapState?.phase}:${bootstrapState?.apiAvailable}`}
    </div>
  );
}

describe("login bootstrap gating", () => {
  it("keeps failed bootstrap checks in engine loading state", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, status: 503 });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <AuthProvider>
        <BootstrapProbe />
      </AuthProvider>
    );

    await waitFor(() => {
      expect(screen.getByTestId("bootstrap-state")).toHaveTextContent("true:loading:false");
    });
    expect(fetchMock).toHaveBeenCalledWith("/api/bootstrap/state", { credentials: "include" });
  });

  it("renders engine loading instead of Aegis setup while bootstrap phase is loading", async () => {
    vi.useFakeTimers();
    const refreshBootstrapState = vi.fn().mockResolvedValue({ phase: "loading", apiAvailable: false });

    renderLoginRoute({
      ready: true,
      isAuthenticated: false,
      bootstrapState: { phase: "loading", apiAvailable: false },
      refreshBootstrapState,
      login: vi.fn(),
    });

    expect(screen.getByRole("status")).toHaveTextContent("Engine Loading...");
    expect(screen.queryByText("Set Up Aegis Cipher")).not.toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(2500);
      await Promise.resolve();
    });

    expect(refreshBootstrapState).toHaveBeenCalledTimes(1);
  });
});
