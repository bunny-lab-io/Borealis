import React from "react";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  EMPTY_PAGE_CHROME,
  PageChromeContext,
} from "@/app/providers/PageChromeContext.jsx";
import { useRoutePageChrome } from "@/app/hooks/useRoutePageChrome.js";

function Harness({ meta, onValue }) {
  useRoutePageChrome(meta);
  return (
    <PageChromeContext.Consumer>
      {(value) => {
        onValue(value.pageChrome);
        return null;
      }}
    </PageChromeContext.Consumer>
  );
}

describe("useRoutePageChrome", () => {
  it("publishes route page chrome and resets it on unmount", () => {
    const observed = [];
    const setPageChrome = (meta) => {
      observed.push(meta);
    };
    const resetPageChrome = () => {
      observed.push(EMPTY_PAGE_CHROME);
    };

    const { unmount } = render(
      <PageChromeContext.Provider
        value={{
          pageChrome: EMPTY_PAGE_CHROME,
          setPageChrome,
          resetPageChrome,
        }}
      >
        <Harness
          meta={{
            title: "Devices",
            subtitle: "Inventory",
            breadcrumbs: [{ id: "inventory", label: "Inventory" }],
            breadcrumbMenuItems: [{ id: "devices", label: "Devices", to: "/devices" }],
          }}
          onValue={() => {}}
        />
      </PageChromeContext.Provider>
    );

    unmount();

    expect(observed).toEqual([
      {
        title: "Devices",
        subtitle: "Inventory",
        Icon: null,
        breadcrumbLabel: "",
        breadcrumbs: [{ id: "inventory", label: "Inventory" }],
        breadcrumbsReplace: false,
        breadcrumbMenuItems: [{ id: "devices", label: "Devices", to: "/devices" }],
        actions: [],
        controls: [],
        navigationSidebar: null,
      },
      EMPTY_PAGE_CHROME,
    ]);
  });
});
