import React from "react";
import fs from "node:fs";
import path from "node:path";
import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import AgentHealthTab from "@/Devices/Tabs/Agent_Health.jsx";

afterEach(() => {
  vi.useRealTimers();
  delete window.BorealisSocket;
});

describe("AgentHealthTab", () => {
  it("renders grouped startup flow telemetry and runtime health nodes", () => {
    const milestones = [
      { key: "process_start", label: "Agent process started", state: "complete" },
      { key: "authenticating", label: "Authenticating with Engine", state: "complete" },
      { key: "authenticated", label: "Engine authentication complete", state: "complete" },
      { key: "wireguard_online", label: "WireGuard tunnel online", state: "complete" },
    ];

    render(
      <AgentHealthTab
        agentRoleHealth={{
          roles: [
            {
              role_id: "system:system_heartbeat",
              role_name: "system_heartbeat",
              role_label: "Startup Timeline",
              context: "system",
              status_code: "healthy",
              details: {
                boot_id: "boot-test",
                message: "WireGuard tunnel is online.",
                milestones_json: JSON.stringify(milestones),
              },
            },
            {
              role_id: "system:wireguard_tunnel",
              role_name: "wireguard_tunnel",
              role_label: "WireGuard VPN",
              context: "system",
              status_code: "healthy",
              last_checked_at: 1_700_000_000,
              details: { wireguard_peer_ip: "10.255.0.2" },
            },
          ],
        }}
        formatTimestamp={(value) => String(value || "")}
      />
    );

    expect(screen.getByText("Startup Timeline")).toBeInTheDocument();
    expect(screen.getByText("Agent process started")).toBeInTheDocument();
    expect(screen.getByText("Engine authentication")).toBeInTheDocument();
    expect(screen.getByText("Agent role loading")).toBeInTheDocument();
    expect(screen.getByText("Runtime role health")).toBeInTheDocument();
    expect(screen.getByText("Borealis Agent - WireGuard")).toBeInTheDocument();
    expect(screen.queryByText("Current Phase")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Startup Timeline" })).not.toBeInTheDocument();
  });

  it("silently refreshes when agent status changes for current device", () => {
    vi.useFakeTimers();
    const handlers = {};
    window.BorealisSocket = {
      on: (event, handler) => {
        handlers[event] = handler;
      },
      off: (event) => {
        delete handlers[event];
      },
    };
    const refresh = vi.fn();

    render(
      <AgentHealthTab
        agentRoleHealth={{ roles: [] }}
        hostname="test-device"
        onRequestRefresh={refresh}
      />
    );

    act(() => {
      handlers.agent_status_changed?.({ hostname: "other-device" });
      vi.advanceTimersByTime(300);
    });
    expect(refresh).not.toHaveBeenCalled();

    act(() => {
      handlers.agent_status_changed?.({ hostname: "test-device" });
      vi.advanceTimersByTime(300);
    });
    expect(refresh).toHaveBeenCalledWith({ silent: true, includeAgents: false });
  });

  it("keeps consolidated health visible at the top of the Device Summary sidebar", () => {
    const source = fs.readFileSync(
      path.resolve(process.cwd(), "src/Devices/Tabs/Device_Summary.jsx"),
      "utf8"
    );

    const healthSectionIndex = source.indexOf('<SidebarSection sectionId="health" title="Agent Health">');
    const inventorySectionIndex = source.indexOf('<SidebarSection sectionId="inventory" title="Inventory">');

    expect(source).toContain("buildAgentHealthRows");
    expect(source).toContain("RuntimeRoleHealthSidebarRows");
    expect(source).toContain("CopyableHealthTooltip");
    expect(source).toContain('label="Agent Connection"');
    expect(source).toContain('label="Roles"');
    expect(source).toContain('label: "API Websocket Connection"');
    expect(source).toContain('label: "Heartbeat"');
    expect(source).toContain('label: "Secure Tunnel"');
    expect(source).toContain('health: true');
    expect(source).toContain('!["enginesocket", "wireguardtunnel"].includes');
    expect(source).toContain("agentConnectionCopyText");
    expect(source).toContain("roleHealthCopyText");
    expect(healthSectionIndex).toBeGreaterThanOrEqual(0);
    expect(inventorySectionIndex).toBeGreaterThan(healthSectionIndex);
    expect(source).not.toContain("DeviceReadinessHeader");
    expect(source).not.toContain('group: "Identifiers"');
    expect(source).toContain('{ label: "Virtual IP", value:');
    expect(source).toContain('{ label: "Tunnel ID", value:');
    expect(source).toContain('{ label: "Agent ID", value:');
    expect(source).toContain("navigationSidebar: deviceNavigationSidebar");
  });
});
