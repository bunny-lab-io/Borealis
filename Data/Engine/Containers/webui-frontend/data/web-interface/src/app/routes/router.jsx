import React from "react";
import { Navigate, Outlet, createBrowserRouter } from "react-router-dom";
import LoginRoute from "./LoginRoute.jsx";
import { RequireAdmin, RequireAuth } from "./guards.jsx";
import AppShell from "../shell/AppShell.jsx";
import { APP_PATHS } from "./paths.js";

function lazyNamed(importer, exportName) {
  return async () => {
    const module = await importer();
    const loaderExportName = `${exportName}Loader`;
    const errorBoundaryExportName = `${exportName}ErrorBoundary`;
    const shouldRevalidateExportName = `${exportName}ShouldRevalidate`;
    return {
      Component: module[exportName],
      loader: typeof module[loaderExportName] === "function" ? module[loaderExportName] : undefined,
      ErrorBoundary:
        typeof module[errorBoundaryExportName] === "function"
          ? module[errorBoundaryExportName]
          : undefined,
      shouldRevalidate:
        typeof module[shouldRevalidateExportName] === "function"
          ? module[shouldRevalidateExportName]
          : undefined,
    };
  };
}

function RootShell() {
  return <Outlet />;
}

export function buildAppRoutes() {
  return [
    {
      path: APP_PATHS.login,
      element: <LoginRoute />,
      handle: {
        title: "Login",
        breadcrumb: "Login",
        navKey: "login",
        pageKey: "login",
      },
    },
    {
      path: APP_PATHS.bootstrapBackupRestore,
      handle: {
        title: "Backup/Restore",
        breadcrumb: "Backup/Restore",
        navKey: "backup-restore",
        pageKey: "backup-restore",
      },
      lazy: lazyNamed(
        () => import("../route-modules/adminRoutes.jsx"),
        "BootstrapBackupRestoreRoute"
      ),
    },
    {
      path: "/",
      element: <RequireAuth />,
      children: [
        {
          element: <AppShell />,
          children: [
            {
              index: true,
              element: <Navigate to={APP_PATHS.sites} replace />,
            },
            {
              path: "sites",
              handle: {
                title: "Sites",
                breadcrumb: "Sites",
                navKey: "sites",
                pageKey: "sites",
              },
              lazy: lazyNamed(() => import("../route-modules/sitesRoutes.jsx"), "SitesListRoute"),
            },
            {
              path: "devices",
              element: <RootShell />,
              handle: {
                title: "Devices",
                breadcrumb: "Devices",
                navKey: "devices",
                pageKey: "devices",
              },
              children: [
                {
                  index: true,
                  lazy: lazyNamed(
                    () => import("../route-modules/inventoryRoutes.jsx"),
                    "DeviceListRoute"
                  ),
                },
                {
                  path: "approvals",
                  handle: {
                    title: "Device Approvals",
                    breadcrumb: "Device Approvals",
                    navKey: "device-approvals",
                    pageKey: "device-approvals",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/inventoryRoutes.jsx"),
                    "DeviceApprovalsRoute"
                  ),
                },
                {
                  path: ":deviceId",
                  element: <RootShell />,
                  handle: {
                    title: "Device Details",
                    breadcrumb: "Device",
                    navKey: "devices",
                    pageKey: "device",
                  },
                  children: [
                    {
                      index: true,
                      handle: {
                        navKey: "devices",
                        pageKey: "device",
                      },
                      lazy: lazyNamed(
                        () => import("../route-modules/inventoryRoutes.jsx"),
                        "DeviceSummaryRoute"
                      ),
                    },
                    {
                      path: "remote-desktop",
                      handle: {
                        breadcrumb: "Remote Desktop",
                        navKey: "devices",
                        pageKey: "device-remote-desktop",
                      },
                      lazy: lazyNamed(
                        () => import("../route-modules/inventoryRoutes.jsx"),
                        "RemoteDesktopRoute"
                      ),
                    },
                  ],
                },
                {
                  element: <RequireAdmin />,
                  children: [
                    {
                      path: "agents",
                      handle: {
                        title: "Agent Devices",
                        breadcrumb: "Agent Devices",
                        navKey: "devices",
                        pageKey: "agent-devices",
                      },
                      lazy: lazyNamed(
                        () => import("../route-modules/inventoryRoutes.jsx"),
                        "AgentDevicesRoute"
                      ),
                    },
                    {
                      path: "ssh",
                      handle: {
                        title: "SSH Devices",
                        breadcrumb: "SSH Devices",
                        navKey: "devices",
                        pageKey: "ssh-devices",
                      },
                      lazy: lazyNamed(
                        () => import("../route-modules/inventoryRoutes.jsx"),
                        "SSHDevicesRoute"
                      ),
                    },
                    {
                      path: "winrm",
                      handle: {
                        title: "WinRM Devices",
                        breadcrumb: "WinRM Devices",
                        navKey: "devices",
                        pageKey: "winrm-devices",
                      },
                      lazy: lazyNamed(
                        () => import("../route-modules/inventoryRoutes.jsx"),
                        "WinRMDevicesRoute"
                      ),
                    },
                  ],
                },
              ],
            },
            {
              path: "software",
              handle: {
                title: "Software Audit",
                breadcrumb: "Software",
                navKey: "software",
                pageKey: "software",
              },
              lazy: lazyNamed(() => import("../route-modules/softwareRoutes.jsx"), "SoftwareListRoute"),
            },
            {
              path: "patch-management",
              element: <RootShell />,
              handle: {
                title: "Patch Management",
                breadcrumb: "Patch Management",
                navKey: "patch-management-windows",
                pageKey: "patch-management",
              },
              children: [
                {
                  index: true,
                  element: <Navigate to={APP_PATHS.patchManagementWindows} replace />,
                },
                {
                  path: "windows",
                  handle: {
                    title: "Windows Patch Management",
                    breadcrumb: "Windows",
                    navKey: "patch-management-windows",
                    pageKey: "patch-management-windows",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchManagementRoute"),
                },
                {
                  path: "linux",
                  handle: {
                    title: "Linux Patch Management",
                    breadcrumb: "Linux",
                    navKey: "patch-management-linux",
                    pageKey: "patch-management-linux",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchLinuxRoute"),
                },
                {
                  path: "macos",
                  handle: {
                    title: "MacOS Patch Management",
                    breadcrumb: "MacOS",
                    navKey: "patch-management-macos",
                    pageKey: "patch-management-macos",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchMacOSRoute"),
                },
                {
                  path: "policies/site/new",
                  handle: {
                    title: "New Site Patch Policy",
                    breadcrumb: "New Site Policy",
                    navKey: "patch-management-windows",
                    pageKey: "patch-policy-site-new",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchPolicyEditorRoute"),
                },
                {
                  path: "policies/global/:policyId",
                  handle: {
                    title: "Global Patch Policy",
                    breadcrumb: "Global Policy",
                    navKey: "patch-management-windows",
                    pageKey: "patch-policy-global",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchPolicyEditorRoute"),
                },
                {
                  path: "policies/site/:policyId",
                  handle: {
                    title: "Site Patch Policy",
                    breadcrumb: "Site Policy",
                    navKey: "patch-management-windows",
                    pageKey: "patch-policy-site",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchPolicyEditorRoute"),
                },
                {
                  path: "policies/device-filter/new",
                  handle: {
                    title: "New Device / Filter Patch Policy",
                    breadcrumb: "New Device / Filter Policy",
                    navKey: "patch-management-windows",
                    pageKey: "patch-policy-device-filter-new",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchPolicyEditorRoute"),
                },
                {
                  path: "policies/device-filter/:policyId",
                  handle: {
                    title: "Device / Filter Patch Policy",
                    breadcrumb: "Device / Filter Policy",
                    navKey: "patch-management-windows",
                    pageKey: "patch-policy-device-filter",
                  },
                  lazy: lazyNamed(() => import("../route-modules/patchRoutes.jsx"), "PatchPolicyEditorRoute"),
                },
              ],
            },
            {
              path: "filters",
              element: <RootShell />,
              handle: {
                title: "Filters",
                breadcrumb: "Filters",
                navKey: "filters",
                pageKey: "filters",
              },
              children: [
                {
                  index: true,
                  lazy: lazyNamed(
                    () => import("../route-modules/filterRoutes.jsx"),
                    "FilterListRoute"
                  ),
                },
                {
                  path: "new",
                  handle: {
                    title: "New Filter",
                    breadcrumb: "New Filter",
                    navKey: "filters",
                    pageKey: "filter",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/filterRoutes.jsx"),
                    "FilterEditorRoute"
                  ),
                },
                {
                  path: ":filterId",
                  handle: {
                    title: "Filter",
                    breadcrumb: "Filter",
                    navKey: "filters",
                    pageKey: "filter",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/filterRoutes.jsx"),
                    "FilterEditorRoute"
                  ),
                },
              ],
            },
            {
              path: "jobs",
              element: <RootShell />,
              handle: {
                title: "Scheduled Jobs",
                breadcrumb: "Scheduled Jobs",
                navKey: "jobs",
                pageKey: "jobs",
              },
              children: [
                {
                  index: true,
                  lazy: lazyNamed(() => import("../route-modules/jobsRoutes.jsx"), "JobListRoute"),
                },
                {
                  path: "new",
                  handle: {
                    title: "Create Job",
                    breadcrumb: "Create Job",
                    navKey: "jobs",
                    pageKey: "job",
                  },
                  lazy: lazyNamed(() => import("../route-modules/jobsRoutes.jsx"), "JobEditorRoute"),
                },
                {
                  path: "onboarding/new",
                  handle: {
                    title: "Automatic Device Onboarding",
                    breadcrumb: "Device Onboarding",
                    navKey: "jobs",
                    pageKey: "job",
                  },
                  lazy: lazyNamed(() => import("../route-modules/jobsRoutes.jsx"), "OnboardingJobEditorRoute"),
                },
                {
                  path: "onboarding/:jobId",
                  handle: {
                    title: "Automatic Device Onboarding",
                    breadcrumb: "Device Onboarding",
                    navKey: "jobs",
                    pageKey: "job",
                  },
                  lazy: lazyNamed(() => import("../route-modules/jobsRoutes.jsx"), "OnboardingJobEditorRoute"),
                },
                {
                  path: ":jobId",
                  handle: {
                    title: "Scheduled Job",
                    breadcrumb: "Job",
                    navKey: "jobs",
                    pageKey: "job",
                  },
                  lazy: lazyNamed(() => import("../route-modules/jobsRoutes.jsx"), "JobEditorRoute"),
                },
              ],
            },
            {
              path: "automation",
              element: <RootShell />,
              handle: {
                title: "Automation",
                breadcrumb: "Automation",
                navKey: "watchdogs",
                pageKey: "watchdogs",
              },
              children: [
                {
                  path: "watchdogs",
                  handle: {
                    title: "Watchdogs",
                    breadcrumb: "Watchdogs",
                    navKey: "watchdogs",
                    pageKey: "watchdogs",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/watchdogRoutes.jsx"),
                    "WatchdogListRoute"
                  ),
                },
                {
                  path: "watchdogs/new",
                  handle: {
                    title: "New Watchdog",
                    breadcrumb: "New Watchdog",
                    navKey: "watchdogs",
                    pageKey: "watchdog",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/watchdogRoutes.jsx"),
                    "WatchdogEditorRoute"
                  ),
                },
                {
                  path: "watchdogs/:watchdogId",
                  handle: {
                    title: "Watchdog",
                    breadcrumb: "Watchdog",
                    navKey: "watchdogs",
                    pageKey: "watchdog",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/watchdogRoutes.jsx"),
                    "WatchdogEditorRoute"
                  ),
                },
              ],
            },
            {
              path: "assemblies",
              element: <RootShell />,
              handle: {
                title: "Assemblies",
                breadcrumb: "Assemblies",
                navKey: "assemblies",
                pageKey: "assemblies",
              },
              children: [
                {
                  index: true,
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "AssembliesRoute"
                  ),
                },
                {
                  path: "new/script",
                  handle: {
                    title: "New Script",
                    breadcrumb: "New Script",
                    navKey: "assemblies",
                    pageKey: "script-assembly",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "ScriptAssemblyRoute"
                  ),
                },
                {
                  path: "new/ansible_playbook",
                  handle: {
                    title: "New Ansible Playbook",
                    breadcrumb: "New Ansible Playbook",
                    navKey: "assemblies",
                    pageKey: "ansible-playbook",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "AnsibleAssemblyRoute"
                  ),
                },
                {
                  path: "new/workflow",
                  handle: {
                    breadcrumb: "New Workflow",
                    navKey: "assemblies",
                    pageKey: "workflow",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "WorkflowEditorRoute"
                  ),
                },
                {
                  path: "scripts/:assemblyGuid",
                  handle: {
                    title: "Script Assembly",
                    breadcrumb: "Script",
                    navKey: "assemblies",
                    pageKey: "script-assembly",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "ScriptAssemblyRoute"
                  ),
                },
                {
                  path: "ansible_playbooks/:assemblyGuid",
                  handle: {
                    title: "Ansible Playbook",
                    breadcrumb: "Ansible Playbook",
                    navKey: "assemblies",
                    pageKey: "ansible-playbook",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "AnsibleAssemblyRoute"
                  ),
                },
                {
                  path: "workflows/:workflowGuid",
                  handle: {
                    breadcrumb: "Workflow",
                    navKey: "assemblies",
                    pageKey: "workflow",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "WorkflowEditorRoute"
                  ),
                },
                {
                  path: "workflows/runs/:runId",
                  handle: {
                    breadcrumb: "Workflow Run",
                    navKey: "assemblies",
                    pageKey: "workflow",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/assemblyRoutes.jsx"),
                    "WorkflowEditorRoute"
                  ),
                },
              ],
            },
            {
              path: "alerts",
              handle: {
                title: "Alerts",
                breadcrumb: "Alerts",
                navKey: "alerts",
                pageKey: "alerts",
              },
              lazy: lazyNamed(
                () => import("../route-modules/watchdogRoutes.jsx"),
                "ActiveAlertsRoute"
              ),
            },
            {
              element: <RequireAdmin />,
              children: [
                {
                  path: "credentials",
                  handle: {
                    title: "Credentials",
                    breadcrumb: "Credentials",
                    navKey: "credentials",
                    pageKey: "credentials",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/accessRoutes.jsx"),
                    "CredentialsRoute"
                  ),
                },
                {
                  path: "directory-services",
                  handle: {
                    title: "Directory Services",
                    breadcrumb: "Directory Services",
                    navKey: "directory-services",
                    pageKey: "directory-services",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/accessRoutes.jsx"),
                    "DirectoryServicesRoute"
                  ),
                },
                {
                  path: "users",
                  element: <RootShell />,
                  handle: {
                    title: "Users",
                    breadcrumb: "Users",
                    navKey: "users",
                    pageKey: "users",
                  },
                  children: [
                    {
                      index: true,
                      lazy: lazyNamed(
                        () => import("../route-modules/accessRoutes.jsx"),
                        "UsersRoute"
                      ),
                    },
                    {
                      path: "site-assignment",
                      handle: {
                        title: "Site Assignment",
                        breadcrumb: "Site Assignment",
                        navKey: "users",
                        pageKey: "site-assignment",
                      },
                      lazy: lazyNamed(
                        () => import("../route-modules/accessRoutes.jsx"),
                        "SiteAssignmentRoute"
                      ),
                    },
                  ],
                },
                {
                  path: "server",
                  handle: {
                    title: "Server Info",
                    breadcrumb: "Server Info",
                    navKey: "server",
                    pageKey: "server",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/adminRoutes.jsx"),
                    "ServerRoute"
                  ),
                },
                {
                  path: "engine-status",
                  element: <Navigate to={APP_PATHS.server} replace />,
                },
                {
                  path: "backup-restore",
                  handle: {
                    title: "Backup/Restore",
                    breadcrumb: "Backup/Restore",
                    navKey: "backup-restore",
                    pageKey: "backup-restore",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/adminRoutes.jsx"),
                    "BackupRestoreRoute"
                  ),
                },
                {
                  path: "metadata-fields",
                  handle: {
                    title: "Metadata Fields",
                    breadcrumb: "Metadata Fields",
                    navKey: "metadata-fields",
                    pageKey: "metadata-fields",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/adminRoutes.jsx"),
                    "MetadataFieldsRoute"
                  ),
                },
                {
                  path: "logs",
                  handle: {
                    title: "Log Management",
                    breadcrumb: "Log Management",
                    navKey: "logs",
                    pageKey: "logs",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/adminRoutes.jsx"),
                    "LogsRoute"
                  ),
                },
                {
                  path: "dev-tools",
                  handle: {
                    title: "Dev Tools",
                    breadcrumb: "Dev Tools",
                    navKey: "dev-tools",
                    pageKey: "dev-tools",
                  },
                  lazy: lazyNamed(
                    () => import("../route-modules/adminRoutes.jsx"),
                    "DevToolsRoute"
                  ),
                },
                {
                  path: "page-template",
                  element: <Navigate to={APP_PATHS.pageStyleTemplate} replace />,
                },
              ],
            },
          ],
        },
      ],
    },
    {
      path: "*",
      element: <Navigate to={APP_PATHS.sites} replace />,
    },
  ];
}

export function createAppRouter() {
  return createBrowserRouter(buildAppRoutes());
}
