function resolveHandleValue(handle, key, match) {
  const value = handle?.[key];
  return typeof value === "function" ? value(match) : value;
}

export const APP_DOCUMENT_TITLE = "Borealis";

export function resolveActiveNavKey(matches = []) {
  for (let index = matches.length - 1; index >= 0; index -= 1) {
    const navKey = matches[index]?.handle?.navKey;
    if (navKey) {
      return navKey;
    }
  }
  return "";
}

export function resolveCurrentPageKey(matches = []) {
  for (let index = matches.length - 1; index >= 0; index -= 1) {
    const handle = matches[index]?.handle;
    const pageKey = handle?.pageKey || handle?.navKey;
    if (pageKey) {
      return pageKey;
    }
  }
  return "";
}

export function resolvePageChromeDefaults(matches = []) {
  const activeMatch = [...matches].reverse().find((match) => match?.handle);
  const handle = activeMatch?.handle || {};
  return {
    title: resolveHandleValue(handle, "title", activeMatch) || "",
    subtitle: resolveHandleValue(handle, "subtitle", activeMatch) || "",
    Icon: resolveHandleValue(handle, "icon", activeMatch) || null,
    actions: Array.isArray(handle?.actions) ? handle.actions.filter(Boolean) : [],
    controls: Array.isArray(handle?.controls) ? handle.controls.filter(Boolean) : [],
  };
}

export function formatDocumentTitle(pageTitle, appTitle = APP_DOCUMENT_TITLE) {
  const normalizedAppTitle =
    typeof appTitle === "string" && appTitle.trim() ? appTitle.trim() : APP_DOCUMENT_TITLE;
  const normalizedPageTitle =
    typeof pageTitle === "string" ? pageTitle.trim() : pageTitle == null ? "" : String(pageTitle).trim();

  if (!normalizedPageTitle || normalizedPageTitle === normalizedAppTitle) {
    return normalizedAppTitle;
  }

  return `${normalizedAppTitle} - ${normalizedPageTitle}`;
}

export function buildBreadcrumbItems(matches = [], pageChrome = {}) {
  const breadcrumbMatches = matches.filter((match) => Boolean(match?.handle?.breadcrumb));
  if (!breadcrumbMatches.length) {
    return [];
  }

  return breadcrumbMatches.map((match, index) => {
    const isLast = index === breadcrumbMatches.length - 1;
    const defaultLabel = resolveHandleValue(match.handle, "breadcrumb", match) || "";
    const label = isLast
      ? pageChrome?.breadcrumbLabel || pageChrome?.title || defaultLabel
      : defaultLabel;

    return {
      id: match.id || match.pathname || String(index),
      label,
      to: !isLast ? match.pathname : null,
    };
  });
}
