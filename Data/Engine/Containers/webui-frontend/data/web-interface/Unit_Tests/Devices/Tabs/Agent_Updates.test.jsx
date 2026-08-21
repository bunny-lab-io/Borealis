import { describe, expect, it } from "vitest";
import {
  agentUpdateIsActive,
  buildAgentUpdateGraph,
  formatAgentUpdateDuration,
} from "@/Devices/Tabs/Agent_Updates.jsx";

describe("Agent Updates dashboard helpers", () => {
  it("maps durable progress events onto read-only topology", () => {
    const graph = buildAgentUpdateGraph({
      events: [
        { phase_id: "requesting_agent_update", state: "success", summary: "Agent Received Request" },
        { phase_id: "quiescing_managed_components", state: "running", summary: "Quiescing Managed Components" },
        { phase_id: "role:remote_shell", state: "recovering", summary: "Remote Shell" },
      ],
    });

    expect(graph.nodes.find((node) => node.id === "quiescing_managed_components")?.data).toBeTruthy();
    expect(graph.nodes.find((node) => node.id === "role:remote_shell")?.draggable).toBe(false);
    expect(graph.edges.some((edge) => edge.source === "verifying_post_update_health" && edge.target === "role:remote_shell")).toBe(true);
  });

  it("classifies active operations and live duration", () => {
    expect(agentUpdateIsActive({ status: "awaiting_health" })).toBe(true);
    expect(agentUpdateIsActive({ status: "success" })).toBe(false);
    expect(formatAgentUpdateDuration(100, 5505)).toBe("1h 30m 5s");
  });
});
