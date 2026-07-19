import { describe, expect, it } from "vitest";
import {
  SITE_WORKER_SITE_SLUG_MAX,
  siteWorkerSiteSlug,
  validateSiteWorkerSiteName,
} from "@/Sites/Site_List.jsx";

describe("site-worker site name policy", () => {
  it("builds readable Kubernetes slugs from site names", () => {
    expect(siteWorkerSiteSlug("Bunny's Lab")).toBe("bunnys-lab");
    expect(siteWorkerSiteSlug("DRaaS Infrastructure")).toBe("draas-infrastructure");
    expect(siteWorkerSiteSlug("ProxmoxVE Cluster")).toBe("proxmoxve-cluster");
  });

  it("rejects site names without Kubernetes-safe slug content", () => {
    expect(validateSiteWorkerSiteName("!!!")?.title).toBe("Site Name Invalid");
  });

  it("rejects site names whose slug would exceed the pod name limit", () => {
    const problem = validateSiteWorkerSiteName("a".repeat(SITE_WORKER_SITE_SLUG_MAX + 1));

    expect(problem?.title).toBe("Site Name Too Long");
  });

  it("rejects site names that collide after slug normalization", () => {
    const problem = validateSiteWorkerSiteName("Bunnys Lab", [{ id: 7, name: "Bunny's Lab" }]);

    expect(problem?.title).toBe("Site Name Already Used");
  });
});
