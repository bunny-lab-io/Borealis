import { useEffect } from "react";
import { usePageChrome } from "../providers/PageChromeContext.jsx";

const EMPTY_ITEMS = [];

export function useRoutePageChrome(pageChrome) {
  const { resetPageChrome, setPageChrome } = usePageChrome();
  const hasPageChrome = Boolean(pageChrome);
  const title = typeof pageChrome?.title === "string" ? pageChrome.title : "";
  const subtitle = typeof pageChrome?.subtitle === "string" ? pageChrome.subtitle : "";
  const Icon = pageChrome?.Icon || null;
  const breadcrumbLabel =
    typeof pageChrome?.breadcrumbLabel === "string" ? pageChrome.breadcrumbLabel : "";
  const actions = Array.isArray(pageChrome?.actions) ? pageChrome.actions : EMPTY_ITEMS;
  const controls = Array.isArray(pageChrome?.controls) ? pageChrome.controls : EMPTY_ITEMS;
  const breadcrumbs = Array.isArray(pageChrome?.breadcrumbs) ? pageChrome.breadcrumbs : EMPTY_ITEMS;
  const breadcrumbsReplace = Boolean(pageChrome?.breadcrumbsReplace);
  const breadcrumbMenuItems = Array.isArray(pageChrome?.breadcrumbMenuItems)
    ? pageChrome.breadcrumbMenuItems
    : EMPTY_ITEMS;
  const navigationSidebar = pageChrome?.navigationSidebar || null;

  useEffect(() => {
    if (hasPageChrome) {
      setPageChrome({
        title,
        subtitle,
        Icon,
        breadcrumbLabel,
        breadcrumbs,
        breadcrumbsReplace,
        breadcrumbMenuItems,
        actions,
        controls,
        navigationSidebar,
      });
    } else {
      resetPageChrome();
    }

    return () => {
      resetPageChrome();
    };
  }, [
    Icon,
    actions,
    breadcrumbLabel,
    breadcrumbMenuItems,
    breadcrumbs,
    breadcrumbsReplace,
    controls,
    hasPageChrome,
    navigationSidebar,
    resetPageChrome,
    setPageChrome,
    subtitle,
    title,
  ]);
}
