import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router-dom";
import NavigationSidebar from "@/Navigation_Sidebar.jsx";

const DEVELOPER_MODE_STORAGE_KEY = "borealis_sidebar_developer_mode";

function renderSidebar() {
  return render(
    <MemoryRouter initialEntries={["/patch-management"]}>
      <NavigationSidebar activeNavKey="patch-management" isAdmin />
    </MemoryRouter>
  );
}

describe("NavigationSidebar patch management visibility", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    window.localStorage.clear();
  });

  it("shows Patch Management as one Automation item by default", () => {
    renderSidebar();

    expect(screen.getByText("Automation")).toBeInTheDocument();
    expect(screen.getByText("Patch Management")).toBeInTheDocument();
    expect(screen.getAllByText("Patch Management")).toHaveLength(1);
    expect(screen.queryByText("Windows")).not.toBeInTheDocument();
    expect(screen.queryByText("Linux")).not.toBeInTheDocument();
    expect(screen.queryByText("MacOS")).not.toBeInTheDocument();
  });

  it("keeps operating systems out of the sidebar in developer mode", () => {
    window.localStorage.setItem(DEVELOPER_MODE_STORAGE_KEY, "1");

    renderSidebar();

    expect(screen.getAllByText("Patch Management")).toHaveLength(1);
    expect(screen.queryByText("Windows")).not.toBeInTheDocument();
    expect(screen.queryByText("Linux")).not.toBeInTheDocument();
    expect(screen.queryByText("MacOS")).not.toBeInTheDocument();
  });

  it("uses in-progress hidden sections copy in the developer mode menu", async () => {
    renderSidebar();

    fireEvent.contextMenu(screen.getByText("Patch Management"));

    expect(await screen.findByText("Show In-Progress / Hidden Sections")).toBeInTheDocument();
    expect(screen.queryByText("Show Dev Tools in the sidebar.")).not.toBeInTheDocument();
  });
});
