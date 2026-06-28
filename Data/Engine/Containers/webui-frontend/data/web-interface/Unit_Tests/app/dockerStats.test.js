import { describe, expect, it } from "vitest";
import { formatDockerStats, hasDockerStats } from "@/app/utils/dockerStats.js";

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
});
