import { useCallback, useEffect, useMemo } from "react";
import { useLocation, useSearchParams } from "react-router-dom";

function normalizeString(value) {
  return String(value || "").trim().toLowerCase();
}

export function useUrlTabState({
  param = "tab",
  defaultKey,
  allowedKeys = [],
  keyByUrl = {},
  urlByKey = {},
  replace = true,
}) {
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();

  const normalizedAllowedKeys = useMemo(
    () => allowedKeys.map((value) => String(value || "").trim()).filter(Boolean),
    [allowedKeys]
  );

  const activeKey = useMemo(() => {
    const rawValue = normalizeString(searchParams.get(param));
    const decodedValue = keyByUrl[rawValue] || rawValue || defaultKey;
    if (
      normalizedAllowedKeys.length > 0 &&
      !normalizedAllowedKeys.includes(decodedValue)
    ) {
      return defaultKey;
    }
    return decodedValue || defaultKey;
  }, [defaultKey, keyByUrl, normalizedAllowedKeys, param, searchParams]);

  const serializedActiveKey = useMemo(
    () => normalizeString(urlByKey[activeKey] || activeKey),
    [activeKey, urlByKey]
  );

  useEffect(() => {
    const currentValue = normalizeString(searchParams.get(param));
    if (!serializedActiveKey || currentValue === serializedActiveKey) {
      return;
    }

    const nextParams = new URLSearchParams(searchParams);
    nextParams.set(param, serializedActiveKey);
    setSearchParams(nextParams, { replace, state: location.state });
  }, [location.state, param, replace, searchParams, serializedActiveKey, setSearchParams]);

  const setActiveKey = useCallback(
    (nextKey) => {
      const normalizedKey = String(nextKey || "").trim();
      if (!normalizedKey) {
        return;
      }
      if (
        normalizedAllowedKeys.length > 0 &&
        !normalizedAllowedKeys.includes(normalizedKey)
      ) {
        return;
      }

      const serializedKey = normalizeString(urlByKey[normalizedKey] || normalizedKey);
      if (!serializedKey) {
        return;
      }
      const currentValue = normalizeString(searchParams.get(param));
      if (currentValue === serializedKey) {
        return;
      }

      const nextParams = new URLSearchParams(searchParams);
      nextParams.set(param, serializedKey);
      setSearchParams(nextParams, { replace, state: location.state });
    },
    [location.state, normalizedAllowedKeys, param, replace, searchParams, setSearchParams, urlByKey]
  );

  return {
    activeKey,
    searchParams,
    setActiveKey,
    setSearchParams,
  };
}
