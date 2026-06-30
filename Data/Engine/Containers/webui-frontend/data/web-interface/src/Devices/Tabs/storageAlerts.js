export const STORAGE_USAGE_ALERT_THRESHOLD_PCT = 90;
export const STORAGE_USAGE_ALERT_LABEL = `Usage Exceeding ${STORAGE_USAGE_ALERT_THRESHOLD_PCT}%`;
export const STORAGE_USAGE_ALERT_COLOR = "#facc15";

function normalizeDiskType(value) {
  return String(value || "").trim().toLowerCase();
}

export function isCdRomStorageDevice(storageEntry) {
  const diskType = normalizeDiskType(storageEntry?.disk_type || storageEntry?.type);
  if (!diskType) return false;
  return diskType.includes("cd-rom") || diskType.includes("cdrom") || diskType.includes("cd rom");
}

export function isStorageUsageAlert(storageEntry) {
  const usageValue = storageEntry?.usage;
  return (
    !isCdRomStorageDevice(storageEntry) &&
    typeof usageValue === "number" &&
    !Number.isNaN(usageValue) &&
    usageValue > STORAGE_USAGE_ALERT_THRESHOLD_PCT
  );
}
