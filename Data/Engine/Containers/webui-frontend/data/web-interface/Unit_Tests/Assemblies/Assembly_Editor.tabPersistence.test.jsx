import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import AssemblyEditor from "@/Assemblies/Assembly_Editor.jsx";
import { AuthContext } from "@/app/providers/AuthContext.jsx";
import { PageChromeProvider, usePageChrome } from "@/app/providers/PageChromeContext.jsx";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function HeaderActionsProbe() {
  const { pageChrome } = usePageChrome();
  return (
    <div>
      {(pageChrome.actions || []).map((action) => (
        <button
          key={action.id}
          type="button"
          disabled={Boolean(action.disabled)}
          onClick={action.onClick}
        >
          {action.label}
        </button>
      ))}
    </div>
  );
}

function buildAuthValue() {
  return {
    role: "Admin",
    isAdmin: true,
    ready: true,
    isAuthenticated: true,
  };
}

function renderAssemblyEditor() {
  const createContext = {
    name: "Original Assembly",
    description: "Original Description",
    defaultType: "powershell",
    type: "powershell",
  };
  const router = createMemoryRouter(
    [
      {
        path: "/assemblies/new/script",
        element: (
          <AuthContext.Provider value={buildAuthValue()}>
            <PageChromeProvider>
              <HeaderActionsProbe />
              <AssemblyEditor />
            </PageChromeProvider>
          </AuthContext.Provider>
        ),
      },
      {
        path: "/assemblies",
        element: <div>Assemblies Page</div>,
      },
    ],
    {
      initialEntries: [
        {
          pathname: "/assemblies/new/script",
          state: {
            initialAssembly: {
              mode: "script",
              createContext,
              row: {
                assemblyGuid: null,
                name: createContext.name,
                domain: "user",
                createContext,
              },
              nonce: 331,
            },
          },
        },
      ],
    }
  );

  render(<RouterProvider router={router} />);
  return router;
}

describe("AssemblyEditor tab persistence", () => {
  it("keeps edited fields while switching tabs and saves complete current state", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        assembly_guid: "A1F1E331-0000-4000-8000-000000000331",
        source: "user",
        name: "Edited Assembly",
      }),
      text: async () => "",
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "alert").mockImplementation(() => {});

    renderAssemblyEditor();

    const nameInput = await screen.findByLabelText("Assembly Name");
    fireEvent.change(nameInput, { target: { value: "Edited Assembly" } });
    fireEvent.change(screen.getByLabelText("Description"), {
      target: { value: "Edited Description" },
    });

    fireEvent.click(screen.getByRole("tab", { name: /Script/i }));
    const scriptEditor = await screen.findByPlaceholderText("Start typing your script...");
    fireEvent.change(scriptEditor, { target: { value: "Write-Output \"persisted\"" } });

    fireEvent.click(screen.getByRole("tab", { name: /Variables/i }));
    await screen.findByText("No variables defined yet");

    fireEvent.click(screen.getByRole("tab", { name: /Summary/i }));
    await waitFor(() => {
      expect(screen.getByLabelText("Assembly Name")).toHaveValue("Edited Assembly");
      expect(screen.getByLabelText("Description")).toHaveValue("Edited Description");
    });

    fireEvent.click(screen.getByRole("tab", { name: /Script/i }));
    await waitFor(() => {
      expect(screen.getByPlaceholderText("Start typing your script...")).toHaveValue(
        "Write-Output \"persisted\""
      );
    });

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Save Assembly" }));
    });

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    const [, saveOptions] = fetchMock.mock.calls[0];
    const saveBody = JSON.parse(saveOptions.body);
    expect(saveBody.document.name).toBe("Edited Assembly");
    expect(saveBody.document.description).toBe("Edited Description");
    expect(window.atob(saveBody.document.script)).toBe("Write-Output \"persisted\"");
  });
});
