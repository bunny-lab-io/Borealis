import { describe, expect, it } from "vitest";
import {
  DOCKER_STATS_HISTORY_LIMIT,
  appendDockerStatsHistory,
  dockerStatsHistoryKey,
  formatDockerStatRate,
  formatDockerStats,
  hasDockerStats,
  pruneDockerStatsHistory,
} from "@/app/utils/dockerStats.js";

describe("docker stats formatting", () => {
  it("formats cpu memory network and disk stats", () => {
    const formatted = formatDockerStats({
      cpu_percent: 101.09,
      memory_usage_bytes: 86476062,
      memory_limit_bytes: 34660450304,
      memory_percent: 0.25,
      net_input_bytes: 1710000,
      net_output_bytes: 31400000,
      block_input_bytes: 143000000,
      block_output_bytes: 871000000,
      pids: 23,
    });

    expect(formatted.available).toBe(true);
    expect(formatted.cpu).toBe("101%");
    expect(formatted.memoryPercent).toBe("0.25%");
    expect(formatted.memory).toBe("82.5 MB / 32.3 GB");
    expect(formatted.network).toBe("1.63 MB / 29.9 MB");
    expect(formatted.block).toBe("136 MB / 831 MB");
    expect(formatted.pids).toBe("23 PIDs");
  });

  it("marks missing stats unavailable", () => {
    expect(hasDockerStats(null)).toBe(false);
    expect(formatDockerStats(null).available).toBe(false);
  });

  it("formats network throughput rates", () => {
    expect(formatDockerStatRate(1536)).toBe("1.50 KB/s");
  });

  it("keeps bounded browser-only history and computes throughput deltas", () => {
    const history = new Map();
    const row = {
      site_id: 7,
      site_worker_guid: "worker-1",
      site_worker_container_name: "site-worker-1",
      site_worker_container_size_rootfs_bytes: 576716800,
      site_worker_container_storage_limit_bytes: 128849018880,
      site_worker_docker_stats: {
        cpu_percent: 1,
        memory_usage_bytes: 104857600,
        memory_limit_bytes: 1073741824,
        net_input_bytes: 1000,
        net_output_bytes: 500,
      },
    };

    appendDockerStatsHistory(history, row, 1000);
    const key = dockerStatsHistoryKey(row);
    const nextRow = {
      ...row,
      site_worker_docker_stats: {
        ...row.site_worker_docker_stats,
        cpu_percent: 2,
        net_input_bytes: 2500,
        net_output_bytes: 1500,
      },
    };
    appendDockerStatsHistory(history, nextRow, 6000);

    expect(history.get(key).at(-1).netInputBps).toBe(300);
    expect(history.get(key).at(-1).netOutputBps).toBe(200);
    expect(history.get(key).at(-1).netTotalBps).toBe(500);
    expect(history.get(key).at(-1).diskUsageBytes).toBe(576716800);
    expect(history.get(key).at(-1).diskLimitBytes).toBe(128849018880);

    for (let index = 0; index < DOCKER_STATS_HISTORY_LIMIT + 4; index += 1) {
      appendDockerStatsHistory(history, {
        ...row,
        site_worker_docker_stats: {
          ...row.site_worker_docker_stats,
          net_input_bytes: 3000 + index,
          net_output_bytes: 2000 + index,
        },
      }, 11000 + index * 5000);
    }
    expect(history.get(key)).toHaveLength(DOCKER_STATS_HISTORY_LIMIT);

    pruneDockerStatsHistory(history, new Set());
    expect(history.has(key)).toBe(false);
  });

  it("treats network counter resets as zero throughput", () => {
    const history = new Map();
    const row = {
      site_id: 7,
      site_worker_guid: "worker-1",
      site_worker_container_name: "site-worker-1",
      site_worker_docker_stats: {
        net_input_bytes: 1000,
        net_output_bytes: 1000,
      },
    };
    appendDockerStatsHistory(history, row, 1000);
    appendDockerStatsHistory(history, {
      ...row,
      site_worker_docker_stats: {
        net_input_bytes: 10,
        net_output_bytes: 20,
      },
    }, 6000);

    expect(history.get(dockerStatsHistoryKey(row)).at(-1).netTotalBps).toBe(0);
  });
});
