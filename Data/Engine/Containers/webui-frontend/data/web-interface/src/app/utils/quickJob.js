let quickJobSeed = 0;

function normalizeQuickJobValue(value) {
  return typeof value === "string" ? value.trim() : "";
}

export function normalizeQuickJobTargets(hostnames, options = {}) {
  const list = Array.isArray(hostnames) ? hostnames : [hostnames];
  const excludeValues = Array.isArray(options?.excludeValues) ? options.excludeValues : [];
  const excluded = new Set(
    excludeValues
      .map((value) => normalizeQuickJobValue(value).toLowerCase())
      .filter(Boolean)
  );
  const seen = new Set();

  return list.reduce((accumulator, value) => {
    const normalizedValue = normalizeQuickJobValue(value);
    if (!normalizedValue) {
      return accumulator;
    }

    const comparisonKey = normalizedValue.toLowerCase();
    if (excluded.has(comparisonKey) || seen.has(comparisonKey)) {
      return accumulator;
    }

    seen.add(comparisonKey);
    accumulator.push(normalizedValue);
    return accumulator;
  }, []);
}

export function createQuickJobDraft(hostnames, options = {}) {
  const normalized = normalizeQuickJobTargets(hostnames, options);
  if (!normalized.length) {
    return null;
  }

  quickJobSeed += 1;
  const primary = normalized[0];
  const extraCount = normalized.length - 1;
  const deviceLabel = extraCount > 0 ? `${primary} +${extraCount} more` : primary;
  return {
    id: `${Date.now()}_${quickJobSeed}`,
    hostnames: normalized,
    deviceLabel,
    initialTabKey: "components",
    scheduleType: "immediately",
    placeholderAssemblyLabel: "Choose Assembly",
  };
}
