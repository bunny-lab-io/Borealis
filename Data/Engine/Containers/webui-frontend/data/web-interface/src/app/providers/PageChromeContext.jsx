import React, { createContext, useCallback, useContext, useMemo, useState } from "react";

export const EMPTY_PAGE_CHROME = {
  title: "",
  subtitle: "",
  Icon: null,
  actions: [],
  controls: [],
  breadcrumbLabel: "",
};

function normalizePageChrome(meta) {
  if (!meta) {
    return EMPTY_PAGE_CHROME;
  }

  return {
    title: typeof meta.title === "string" ? meta.title : "",
    subtitle: typeof meta.subtitle === "string" ? meta.subtitle : "",
    Icon: meta.Icon || null,
    actions: Array.isArray(meta.actions) ? meta.actions.filter(Boolean) : [],
    controls: Array.isArray(meta.controls) ? meta.controls.filter(Boolean) : [],
    breadcrumbLabel:
      typeof meta.breadcrumbLabel === "string" ? meta.breadcrumbLabel : typeof meta.title === "string" ? meta.title : "",
  };
}

export const PageChromeContext = createContext(null);

export function PageChromeProvider({ children }) {
  const [pageChrome, setPageChromeState] = useState(EMPTY_PAGE_CHROME);

  const resetPageChrome = useCallback(() => {
    setPageChromeState(EMPTY_PAGE_CHROME);
  }, []);

  const setPageChrome = useCallback((meta) => {
    setPageChromeState(normalizePageChrome(meta));
  }, []);

  const value = useMemo(
    () => ({
      pageChrome,
      setPageChrome,
      resetPageChrome,
    }),
    [pageChrome, resetPageChrome, setPageChrome]
  );

  return <PageChromeContext.Provider value={value}>{children}</PageChromeContext.Provider>;
}

export function usePageChrome() {
  const context = useContext(PageChromeContext);
  if (!context) {
    throw new Error("usePageChrome must be used inside PageChromeProvider.");
  }
  return context;
}
