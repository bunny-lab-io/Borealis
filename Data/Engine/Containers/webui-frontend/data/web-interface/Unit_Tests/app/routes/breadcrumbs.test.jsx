import { describe, expect, it } from "vitest";
import {
  APP_DOCUMENT_TITLE,
  buildBreadcrumbDisplayItems,
  buildBreadcrumbItems,
  formatDocumentTitle,
  resolveActiveNavKey,
  resolveCurrentPageKey,
  resolvePageChromeDefaults,
  resolveRememberedBreadcrumbTargetKey,
} from "@/app/routes/breadcrumbs.js";

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
      { id: "assemblies", label: "Assemblies", to: "/assemblies", menuItems: [] },
      { id: "workflow", label: "Workflow: Device Sync", to: null, menuItems: [] },
    ]);
  });

  it("uses remembered collection URLs for parent breadcrumbs", () => {
    const deviceMatches = [
      {
        id: "devices",
        pathname: "/devices",
        handle: {
          title: "Devices",
          breadcrumb: "Devices",
          navKey: "devices",
          pageKey: "devices",
        },
      },
      {
        id: "device",
        pathname: "/devices/agent-1",
        handle: {
          title: "Device Details",
          breadcrumb: "Device",
          navKey: "devices",
          pageKey: "device",
        },
      },
    ];

    expect(
      buildBreadcrumbItems(
        deviceMatches,
        { title: "lab-docker-02", breadcrumbLabel: "lab-docker-02" },
        { rememberedTargets: { devices: "/devices?site=3&view=servers" } }
      )
    ).toEqual([
      { id: "devices", label: "Devices", to: "/devices?site=3&view=servers", menuItems: [] },
      { id: "device", label: "lab-docker-02", to: null, menuItems: [] },
    ]);
  });

  it("appends page-supplied breadcrumbs and makes the route leaf navigable", () => {
    const deviceMatches = [
      {
        id: "devices",
        pathname: "/devices",
        handle: { breadcrumb: "Devices", navKey: "devices", pageKey: "devices" },
      },
      {
        id: "device",
        pathname: "/devices/agent-1",
        handle: { breadcrumb: "Device", navKey: "devices", pageKey: "device" },
      },
    ];

    expect(
      buildBreadcrumbItems(deviceMatches, {
        title: "lab-docker-02",
        breadcrumbLabel: "lab-docker-02",
        breadcrumbs: [
          {
            id: "workspace",
            label: "Backend Tools",
            to: "/devices/agent-1?tab=remote_ops&view=shell",
          },
          {
            id: "view",
            label: "Files",
            menuItems: [{ id: "shell", label: "Shell", to: "/devices/agent-1?tab=remote_ops&view=shell" }],
          },
        ],
      })
    ).toEqual([
      { id: "devices", label: "Devices", to: "/devices", menuItems: [] },
      { id: "device", label: "lab-docker-02", to: "/devices/agent-1", menuItems: [] },
      {
        id: "workspace",
        label: "Backend Tools",
        to: "/devices/agent-1?tab=remote_ops&view=shell",
        menuItems: [],
      },
      {
        id: "view",
        label: "Files",
        to: null,
        menuItems: [
          { id: "shell", label: "Shell", to: "/devices/agent-1?tab=remote_ops&view=shell", onClick: null, disabled: false },
        ],
      },
    ]);
  });

  it("collapses middle breadcrumbs behind an overflow menu", () => {
    expect(
      buildBreadcrumbDisplayItems(
        [
          { id: "one", label: "One", to: "/one" },
          { id: "two", label: "Two", to: "/two" },
          { id: "three", label: "Three", to: "/three" },
          { id: "four", label: "Four", to: "/four" },
          { id: "five", label: "Five" },
        ],
        { maxItems: 3 }
      )
    ).toEqual([
      { id: "one", label: "One", to: "/one", menuItems: [] },
      {
        id: "breadcrumb-overflow",
        label: "More",
        to: null,
        menuItems: [
          { id: "two", label: "Two", to: "/two", disabled: false },
          { id: "three", label: "Three", to: "/three", disabled: false },
          { id: "four", label: "Four", to: "/four", disabled: false },
        ],
        overflow: true,
      },
      { id: "five", label: "Five", to: null, menuItems: [] },
    ]);
  });

  it("identifies collection routes that should be remembered", () => {
    expect(resolveRememberedBreadcrumbTargetKey(matches)).toBe("");
    expect(
      resolveRememberedBreadcrumbTargetKey([
        {
          id: "devices",
          pathname: "/devices",
          handle: {
            breadcrumb: "Devices",
            navKey: "devices",
            pageKey: "devices",
          },
        },
      ])
    ).toBe("devices");
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

  it("formats browser tab titles from the resolved page title", () => {
    expect(formatDocumentTitle("Sites")).toBe("Borealis - Sites");
    expect(formatDocumentTitle("  Device Inventory  ")).toBe("Borealis - Device Inventory");
    expect(formatDocumentTitle("")).toBe(APP_DOCUMENT_TITLE);
    expect(formatDocumentTitle(APP_DOCUMENT_TITLE)).toBe(APP_DOCUMENT_TITLE);
  });
});
