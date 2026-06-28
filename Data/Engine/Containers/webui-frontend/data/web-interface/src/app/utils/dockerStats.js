function coerceNumber(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

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
