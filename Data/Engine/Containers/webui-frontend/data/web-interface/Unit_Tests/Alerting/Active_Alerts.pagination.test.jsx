import React from "react";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import ActiveAlerts from "@/Alerting/Active_Alerts.jsx";

vi.mock("ag-grid-community", () => ({
  AllCommunityModule: {},
  ModuleRegistry: { registerModules: vi.fn() },
}));

vi.mock("ag-grid-react", () => ({
  AgGridReact: ({
    columnDefs = [],
    domLayout,
    pagination,
    paginationPageSize,
    paginationPageSizeSelector,
  }) => {
    const watchdogColumn = columnDefs.find(({ field }) => field === "watchdog_name") || {};
    return (
      <div
        data-testid="alerts-grid"
        data-dom-layout={domLayout || ""}
        data-pagination={String(pagination)}
        data-page-size={String(paginationPageSize)}
        data-page-size-selector={JSON.stringify(paginationPageSizeSelector)}
        data-watchdog-flex={String(watchdogColumn.flex)}
        data-watchdog-min-width={String(watchdogColumn.minWidth)}
        data-watchdog-width={String(watchdogColumn.width || "")}
      />
    );
  },
}));

vi.mock("@/PageBodyFrame.jsx", () => ({
  default: ({ children, main, stack }) => (
    <div>
      {stack}
      {main || children}
    </div>
  ),
}));

vi.mock("@/app/hooks/useRoutePageChrome.js", () => ({
  useRoutePageChrome: vi.fn(),
}));

vi.mock("@/Automation/Watchdogs/shared.jsx", () => ({
  BOREALIS_BLUE: "#58a6ff",
  CountSliderGroup: () => <div data-testid="alert-status-filter" />,
  GRID_WRAPPER_SX: {},
  formatTimestamp: (value) => String(value || ""),
  gridTheme: {},
  promptRequiredSuppressionReason: vi.fn(),
  severityColor: () => "#fcd34d",
}));

describe("Alerts grid layout", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {}))
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps pagination at frame bottom and lets Watchdog fill remaining width", () => {
    render(
      <MemoryRouter>
        <ActiveAlerts />
      </MemoryRouter>
    );

    const grid = screen.getByTestId("alerts-grid");
    expect(grid.getAttribute("data-pagination")).toBe("true");
    expect(grid.getAttribute("data-page-size")).toBe("20");
    expect(grid.getAttribute("data-page-size-selector")).toBe("[20,50,100]");
    expect(grid.getAttribute("data-dom-layout")).toBe("");
    expect(grid.getAttribute("data-watchdog-flex")).toBe("1");
    expect(grid.getAttribute("data-watchdog-min-width")).toBe("220");
    expect(grid.getAttribute("data-watchdog-width")).toBe("");
  });
});
