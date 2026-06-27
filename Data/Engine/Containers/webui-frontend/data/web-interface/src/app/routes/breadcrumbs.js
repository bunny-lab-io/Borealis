function resolveHandleValue(handle, key, match) {
  const value = handle?.[key];
  return typeof value === "function" ? value(match) : value;
}

export const APP_DOCUMENT_TITLE = "Borealis";
const EMPTY_ITEMS = Object.freeze([]);

function normalizeText(value) {
  return String(value || "").trim();
}

function buildTargetFromLocation(location = {}) {
  const pathname = normalizeText(location.pathname);
  const search = normalizeText(location.search);
  if (!pathname) {
    return "";
  }
  return `${pathname}${search}`;
}

export function normalizeBreadcrumbMenuItems(items = []) {
  if (!Array.isArray(items)) {
    return [];
  }

  return items
    .map((item, index) => {
      if (!item) {
        return null;
      }
      if (typeof item === "string") {
        const label = normalizeText(item);
        return label ? { id: `menu-${index}`, label, to: null, disabled: true } : null;
      }

      const label = normalizeText(item.label);
      if (!label) {
        return null;
      }

      return {
        id: normalizeText(item.id) || `menu-${index}`,
        label,
        to: item.to || null,
        onClick: typeof item.onClick === "function" ? item.onClick : null,
        disabled: Boolean(item.disabled),
      };
    })
    .filter(Boolean);
}

export function normalizeBreadcrumbItems(items = []) {
  if (!Array.isArray(items)) {
    return [];
  }

  return items
    .map((item, index) => {
      if (!item) {
        return null;
      }
      if (typeof item === "string") {
        const label = normalizeText(item);
        return label ? { id: `breadcrumb-${index}`, label, to: null, menuItems: EMPTY_ITEMS } : null;
      }

      const label = normalizeText(item.label);
      if (!label) {
        return null;
      }

      return {
        id: normalizeText(item.id) || `breadcrumb-${index}`,
        label,
        to: item.to || null,
        menuItems: normalizeBreadcrumbMenuItems(item.menuItems),
      };
    })
    .filter(Boolean);
}

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

export function resolveRememberedBreadcrumbTargetKey(matches = []) {
  for (let index = matches.length - 1; index >= 0; index -= 1) {
    const handle = matches[index]?.handle;
    const navKey = normalizeText(handle?.navKey);
    const pageKey = normalizeText(handle?.pageKey || handle?.navKey);
    if (navKey && pageKey && navKey === pageKey) {
      return navKey;
    }
  }
  return "";
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

export function buildBreadcrumbItems(matches = [], pageChrome = {}, options = {}) {
  const breadcrumbMatches = matches.filter((match) => Boolean(match?.handle?.breadcrumb));
  if (!breadcrumbMatches.length) {
    return normalizeBreadcrumbItems(pageChrome?.breadcrumbs);
  }

  const rememberedTargets =
    options?.rememberedTargets && typeof options.rememberedTargets === "object"
      ? options.rememberedTargets
      : {};
  const pageBreadcrumbs = normalizeBreadcrumbItems(pageChrome?.breadcrumbs);
  const replaceRouteTrail = Boolean(pageChrome?.breadcrumbsReplace);
  const currentMenuItems = normalizeBreadcrumbMenuItems(pageChrome?.breadcrumbMenuItems);
  const lastRouteMatch = breadcrumbMatches[breadcrumbMatches.length - 1] || null;
  const routeItems = breadcrumbMatches.map((match, index) => {
    const isLast = index === breadcrumbMatches.length - 1;
    const defaultLabel = resolveHandleValue(match.handle, "breadcrumb", match) || "";
    const label = isLast
      ? pageChrome?.breadcrumbLabel || pageChrome?.title || defaultLabel
      : defaultLabel;
    const navKey = normalizeText(match.handle?.navKey);
    const rememberedTarget = !isLast && navKey ? normalizeText(rememberedTargets[navKey]) : "";

    return {
      id: match.id || match.pathname || String(index),
      label,
      to: !isLast ? rememberedTarget || match.pathname : null,
      menuItems: EMPTY_ITEMS,
    };
  });

  let items = replaceRouteTrail ? pageBreadcrumbs : [...routeItems, ...pageBreadcrumbs];
  if (!replaceRouteTrail && pageBreadcrumbs.length && routeItems.length) {
    items = items.map((item, index) => {
      if (index !== routeItems.length - 1 || item.to) {
        return item;
      }
      const routeTarget = buildTargetFromLocation(lastRouteMatch);
      return routeTarget ? { ...item, to: routeTarget } : item;
    });
  }

  if (currentMenuItems.length && items.length) {
    const lastIndex = items.length - 1;
    items = items.map((item, index) =>
      index === lastIndex
        ? { ...item, menuItems: [...(item.menuItems || EMPTY_ITEMS), ...currentMenuItems] }
        : item
    );
  }

  return items;
}

export function buildBreadcrumbDisplayItems(items = [], { maxItems = 6 } = {}) {
  const normalizedItems = normalizeBreadcrumbItems(items);
  const normalizedMaxItems = Math.max(3, Number(maxItems) || 6);
  if (normalizedItems.length <= normalizedMaxItems) {
    return normalizedItems;
  }

  const visibleSlots = normalizedMaxItems - 1;
  const beforeCount = Math.max(1, Math.ceil((visibleSlots - 1) / 2));
  const afterCount = Math.max(1, visibleSlots - beforeCount);
  const beforeItems = normalizedItems.slice(0, beforeCount);
  const afterItems = normalizedItems.slice(normalizedItems.length - afterCount);
  const overflowItems = normalizedItems.slice(beforeCount, normalizedItems.length - afterCount);

  return [
    ...beforeItems,
    {
      id: "breadcrumb-overflow",
      label: "More",
      to: null,
      menuItems: overflowItems.map((item) => ({
        id: item.id,
        label: item.label,
        to: item.to,
        disabled: !item.to && !item.menuItems?.length,
      })),
      overflow: true,
    },
    ...afterItems,
  ];
}
