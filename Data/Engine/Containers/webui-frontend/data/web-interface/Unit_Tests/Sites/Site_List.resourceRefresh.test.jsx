import { describe, expect, it } from "vitest";
import { siteWorkerContainerRefreshValue, siteWorkerMetricPollingText } from "@/Sites/Site_List.jsx";

describe("site worker resource refresh value", () => {
  it("formats the delayed polling countdown text", () => {
    expect(siteWorkerMetricPollingText(5)).toBe("Polling Site Worker Metrics in 5s");
    expect(siteWorkerMetricPollingText(2.2)).toBe("Polling Site Worker Metrics in 3s");
  });

  it("changes while metrics are still in the polling countdown", () => {
    const baseRow = {
      site_worker_metrics_visible: false,
      site_worker_metrics_polling_remaining_seconds: 5,
      site_worker_container_id: "abcdef123456",
    };
    const nextRow = {
      ...baseRow,
      site_worker_metrics_polling_remaining_seconds: 4,
    };

    expect(siteWorkerContainerRefreshValue(nextRow)).not.toBe(siteWorkerContainerRefreshValue(baseRow));
  });

  it("changes when nested docker stats change even if container id is stable", () => {
    const baseRow = {
      site_worker_metrics_visible: true,
      site_worker_container_id: "abcdef123456",
      site_worker_resource_history_key: "7:worker-1:site-worker-1",
      site_worker_resource_history: [
        {
          sampledAtMs: 1000,
          cpuPercent: 1,
        },
      ],
      site_worker_docker_stats: {
        cpu_percent: 1,
        memory_usage_bytes: 100,
        memory_limit_bytes: 1000,
        net_input_bytes: 200,
        net_output_bytes: 300,
      },
    };
    const nextRow = {
      ...baseRow,
      site_worker_resource_history: [
        ...baseRow.site_worker_resource_history,
        {
          sampledAtMs: 6000,
          cpuPercent: 2,
        },
      ],
      site_worker_docker_stats: {
        ...baseRow.site_worker_docker_stats,
        cpu_percent: 2,
        memory_usage_bytes: 125,
        net_input_bytes: 400,
      },
    };

    expect(siteWorkerContainerRefreshValue(nextRow)).not.toBe(siteWorkerContainerRefreshValue(baseRow));
  });
});
