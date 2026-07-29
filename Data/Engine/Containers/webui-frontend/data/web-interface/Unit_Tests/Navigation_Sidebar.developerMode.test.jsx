import React from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router-dom";
import NavigationSidebar from "@/Navigation_Sidebar.jsx";

const DEVELOPER_MODE_STORAGE_KEY = "borealis_sidebar_developer_mode";

function renderSidebar() {
  return render(
    <MemoryRouter initialEntries={["/patch-management/windows"]}>
      <NavigationSidebar activeNavKey="patch-management-windows" isAdmin />
    </MemoryRouter>
  );
}

describe("NavigationSidebar developer mode visibility", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    window.localStorage.clear();
  });

  it("hides Linux and MacOS patch management entries by default", () => {
    renderSidebar();

    expect(screen.getByText("Patch Management")).toBeInTheDocument();
    expect(screen.getByText("Windows")).toBeInTheDocument();
    expect(screen.queryByText("Linux")).not.toBeInTheDocument();
    expect(screen.queryByText("MacOS")).not.toBeInTheDocument();
  });

  it("shows Linux and MacOS patch management entries in developer mode", () => {
    window.localStorage.setItem(DEVELOPER_MODE_STORAGE_KEY, "1");

    renderSidebar();

    expect(screen.getByText("Windows")).toBeInTheDocument();
    expect(screen.getByText("Linux")).toBeInTheDocument();
    expect(screen.getByText("MacOS")).toBeInTheDocument();
  });
});
