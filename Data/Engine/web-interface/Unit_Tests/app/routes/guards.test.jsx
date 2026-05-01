import React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { AuthContext } from "@/app/providers/AuthContext.jsx";
import { RequireAdmin, RequireAuth } from "@/app/routes/guards.jsx";

function renderGuardedRouter({ authValue, guardElement, initialEntries, protectedPath }) {
  const guardedPath = String(protectedPath || "").replace(/^\/+/, "");
  const guardedLabel = guardedPath === "admin" ? "Admin" : "Protected";
  const router = createMemoryRouter(
    [
      {
        element: <AuthContext.Provider value={authValue}>{guardElement}</AuthContext.Provider>,
        children: [
          { path: guardedPath, element: <div>{guardedLabel}</div> },
        ],
      },
      { path: "/login", element: <div>Login Page</div> },
      { path: "/sites", element: <div>Sites Page</div> },
    ],
    {
      initialEntries,
      initialIndex: 0,
    }
  );

  render(<RouterProvider router={router} />);
  return router;
}

describe("route guards", () => {
  it("redirects unauthenticated users to login", async () => {
    const router = renderGuardedRouter({
      authValue: {
        ready: true,
        isAuthenticated: false,
        isAdmin: false,
      },
      guardElement: <RequireAuth />,
      initialEntries: ["/protected"],
      protectedPath: "/protected",
    });

    await waitFor(() => {
      expect(screen.getByText("Login Page")).toBeInTheDocument();
    });
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toBe("?next=%2Fprotected");
  });

  it("redirects non-admin users to sites with the not-authorized query flag", async () => {
    const router = renderGuardedRouter({
      authValue: {
        ready: true,
        isAuthenticated: true,
        isAdmin: false,
      },
      guardElement: <RequireAdmin />,
      initialEntries: ["/admin"],
      protectedPath: "/admin",
    });

    await waitFor(() => {
      expect(screen.getByText("Sites Page")).toBeInTheDocument();
    });
    expect(router.state.location.pathname).toBe("/sites");
    expect(router.state.location.search).toBe("?not_authorized=1");
  });
});
