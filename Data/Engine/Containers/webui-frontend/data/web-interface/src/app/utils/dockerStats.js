function coerceNumber(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export const DOCKER_STATS_HISTORY_LIMIT = 13;

export function formatDockerStatBytes(value) {
  const bytes = coerceNumber(value, 0);
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let unitIndex = 0;
  let remainder = bytes;
  while (remainder >= 1024 && unitIndex < units.length - 1) {
    remainder /= 1024;
    unitIndex += 1;
  }
  const fractionDigits = remainder >= 100 || unitIndex === 0 ? 0 : remainder >= 10 ? 1 : 2;
  return `${remainder.toFixed(fractionDigits)} ${units[unitIndex]}`;
}

export function formatDockerStatPercent(value) {
  const numeric = Math.max(0, coerceNumber(value, 0));
  if (numeric >= 100) return `${numeric.toFixed(0)}%`;
  if (numeric >= 10) return `${numeric.toFixed(1)}%`;
  return `${numeric.toFixed(2)}%`;
}

export function formatDockerStatRate(value) {
  return `${formatDockerStatBytes(value)}/s`;
}

export function hasDockerStats(stats) {
  return Boolean(stats && typeof stats === "object" && Object.keys(stats).length);
}

export function formatDockerStats(stats) {
  if (!hasDockerStats(stats)) {
    return {
      available: false,
      cpu: "N/A",
      memory: "N/A",
      memoryPercent: "N/A",
      network: "N/A",
      block: "N/A",
      pids: "",
    };
  }
  const memoryUsage = coerceNumber(stats.memory_usage_bytes, 0);
  const memoryLimit = coerceNumber(stats.memory_limit_bytes, 0);
  const pids = coerceNumber(stats.pids, 0);
  return {
    available: true,
    cpu: formatDockerStatPercent(stats.cpu_percent),
    memory: `${formatDockerStatBytes(memoryUsage)} / ${formatDockerStatBytes(memoryLimit)}`,
    memoryPercent: formatDockerStatPercent(stats.memory_percent),
    network: `${formatDockerStatBytes(stats.net_input_bytes)} / ${formatDockerStatBytes(stats.net_output_bytes)}`,
    block: `${formatDockerStatBytes(stats.block_input_bytes)} / ${formatDockerStatBytes(stats.block_output_bytes)}`,
    pids: pids > 0 ? `${pids} PID${pids === 1 ? "" : "s"}` : "",
  };
}

export function dockerStatsHistoryKey(row) {
  if (!row || typeof row !== "object") return "";
  const siteId = String(row.site_id ?? row.siteId ?? row.raw_site_worker?.site_id ?? "").trim();
  const workerGuid = String(row.site_worker_guid ?? row.worker_guid ?? row.raw_site_worker?.worker_guid ?? "").trim();
  const containerName = String(row.site_worker_container_name ?? row.container_name ?? row.raw_site_worker?.container_name ?? "").trim();
  if (!workerGuid && !containerName) return "";
  return `${siteId || "0"}:${workerGuid || "unknown"}:${containerName || "unknown"}`;
}

function dockerStatsFromRow(row) {
  return row?.site_worker_docker_stats || row?.docker_stats || null;
}

function nonNegativeRate(current, previous, seconds) {
  const currentValue = coerceNumber(current, 0);
  const previousValue = coerceNumber(previous, 0);
  if (!Number.isFinite(seconds) || seconds <= 0 || currentValue < previousValue) {
    return 0;
  }
  return (currentValue - previousValue) / seconds;
}

export function buildDockerStatsSample(row, previousSample = null, sampledAtMs = Date.now()) {
  const stats = dockerStatsFromRow(row);
  if (!hasDockerStats(stats)) return null;
  const netInputBytes = coerceNumber(stats.net_input_bytes, 0);
  const netOutputBytes = coerceNumber(stats.net_output_bytes, 0);
  const previousMs = coerceNumber(previousSample?.sampledAtMs, 0);
  const seconds = previousMs > 0 ? (sampledAtMs - previousMs) / 1000 : 0;
  const netInputBps = nonNegativeRate(netInputBytes, previousSample?.netInputBytes, seconds);
  const netOutputBps = nonNegativeRate(netOutputBytes, previousSample?.netOutputBytes, seconds);
  return {
    sampledAtMs,
    cpuPercent: Math.max(0, coerceNumber(stats.cpu_percent, 0)),
    memoryUsageBytes: Math.max(0, coerceNumber(stats.memory_usage_bytes, 0)),
    memoryLimitBytes: Math.max(0, coerceNumber(stats.memory_limit_bytes, 0)),
    netInputBytes,
    netOutputBytes,
    netInputBps,
    netOutputBps,
    netTotalBps: netInputBps + netOutputBps,
    diskUsageBytes: Math.max(0, coerceNumber(row?.site_worker_container_size_rootfs_bytes ?? row?.container_size_rootfs_bytes, 0)),
    diskWritableBytes: Math.max(0, coerceNumber(row?.site_worker_container_size_rw_bytes ?? row?.container_size_rw_bytes, 0)),
    diskLimitBytes: Math.max(0, coerceNumber(row?.site_worker_container_storage_limit_bytes ?? row?.container_storage_limit_bytes, 0)),
    diskLimitSource: String(row?.site_worker_container_storage_limit_source ?? row?.container_storage_limit_source ?? "").trim(),
  };
}

export function appendDockerStatsHistory(historyMap, row, sampledAtMs = Date.now()) {
  if (!(historyMap instanceof Map)) return null;
  const key = dockerStatsHistoryKey(row);
  if (!key) return null;
  const existing = Array.isArray(historyMap.get(key)) ? historyMap.get(key) : [];
  const previous = existing.length ? existing[existing.length - 1] : null;
  const sample = buildDockerStatsSample(row, previous, sampledAtMs);
  if (!sample) return null;
  const nextSamples = [...existing, sample].slice(-DOCKER_STATS_HISTORY_LIMIT);
  historyMap.set(key, nextSamples);
  return { key, samples: nextSamples };
}

export function pruneDockerStatsHistory(historyMap, activeKeys) {
  if (!(historyMap instanceof Map) || !(activeKeys instanceof Set)) return;
  Array.from(historyMap.keys()).forEach((key) => {
    if (!activeKeys.has(key)) {
      historyMap.delete(key);
    }
  });
}
