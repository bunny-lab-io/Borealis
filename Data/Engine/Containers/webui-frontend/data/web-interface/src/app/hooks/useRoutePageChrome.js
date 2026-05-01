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

  useEffect(() => {
    if (hasPageChrome) {
      setPageChrome({
        title,
        subtitle,
        Icon,
        breadcrumbLabel,
        actions,
        controls,
      });
    } else {
      resetPageChrome();
    }

    return () => {
      resetPageChrome();
    };
  }, [Icon, actions, breadcrumbLabel, controls, hasPageChrome, resetPageChrome, setPageChrome, subtitle, title]);
}
