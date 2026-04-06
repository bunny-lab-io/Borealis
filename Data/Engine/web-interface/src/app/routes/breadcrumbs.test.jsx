import { describe, expect, it } from "vitest";
import {
  buildBreadcrumbItems,
  resolveActiveNavKey,
  resolveCurrentPageKey,
  resolvePageChromeDefaults,
} from "./breadcrumbs.js";

describe("breadcrumb and nav helpers", () => {
  const matches = [
    {
      id: "assemblies",
      pathname: "/assemblies",
      handle: {
        title: "Assemblies",
        breadcrumb: "Assemblies",
        navKey: "assemblies",
        pageKey: "assemblies",
      },
    },
    {
      id: "workflow",
      pathname: "/assemblies/workflows/FLOW-123",
      params: { workflowGuid: "FLOW-123" },
      handle: {
        title: "Workflow",
        breadcrumb: "Workflow",
        navKey: "assemblies",
        pageKey: "workflow",
      },
    },
  ];

  it("derives the active nav item and page key from the deepest handled route", () => {
    expect(resolveActiveNavKey(matches)).toBe("assemblies");
    expect(resolveCurrentPageKey(matches)).toBe("workflow");
  });

  it("allows page chrome to override the final breadcrumb label", () => {
    expect(
      buildBreadcrumbItems(matches, {
        title: "Workflow: Device Sync",
        breadcrumbLabel: "Workflow: Device Sync",
      })
    ).toEqual([
      { id: "assemblies", label: "Assemblies", to: "/assemblies" },
      { id: "workflow", label: "Workflow: Device Sync", to: null },
    ]);
  });

  it("uses route-handle defaults when the page has not published chrome yet", () => {
    expect(resolvePageChromeDefaults(matches)).toEqual({
      title: "Workflow",
      subtitle: "",
      Icon: null,
      actions: [],
      controls: [],
    });
  });
});
