# Engine API Route Inventory

This generated review surface lists routes registered by production Go backend. Route classes separate public operator, device, bootstrap, internal scheduler, operator bridge, health, and fallback contracts.

Regenerate with `python3 Tests/tools/generate_api_route_inventory.py`, then run `python3 Tests/policy/check_api_routes.py`.

| Route pattern | Class | Authentication | Source |
| --- | --- | --- | --- |
| `/` | `fallback` | `shared-public-gate` | `Data/Engine/Containers/api-backend/cmd/api-backend/main.go:175` |
| `/api/admin/device-approvals` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/admin_approvals.go:50` |
| `/api/admin/enrollment-codes` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/admin_approvals.go:48` |
| `/api/agent/hash` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_hash.go:53` |
| `/api/assemblies` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/assemblies.go:152` |
| `/api/assemblies/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/assemblies.go:153` |
| `/api/auth/me` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:328` |
| `/api/auth/mfa/reset` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:329` |
| `/api/auth/passkeys` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:331` |
| `/api/auth/passkeys/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:336` |
| `/api/credentials` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/credentials.go:53` |
| `/api/credentials/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/credentials.go:54` |
| `/api/device/activity/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/activity.go:20` |
| `/api/device/files/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/remote_files.go:21` |
| `/api/device/registry/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/remote_registry.go:13` |
| `/api/device_filters` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters.go:106` |
| `/api/device_filters/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters.go:110` |
| `/api/device_list_views` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_views.go:42` |
| `/api/device_list_views/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_views.go:43` |
| `/api/directory/providers` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/directory.go:83` |
| `/api/directory/providers/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/directory.go:85` |
| `/api/directory/sites` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/directory.go:86` |
| `/api/github/token` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/credentials.go:55` |
| `/api/internal/job-scheduler/credential/` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:21` |
| `/api/internal/job-scheduler/host-service-event` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:26` |
| `/api/internal/job-scheduler/online-hosts` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:23` |
| `/api/internal/job-scheduler/online-sites` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:24` |
| `/api/internal/job-scheduler/public-base-url` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:25` |
| `/api/internal/job-scheduler/service-account/` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:22` |
| `/api/internal/job-scheduler/vpn-prepare` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:28` |
| `/api/internal/job-scheduler/vpn-sessions` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:27` |
| `/api/internal/job-scheduler/work-items` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:29` |
| `/api/internal/job-scheduler/workflow/start` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/internal_scheduler.go:30` |
| `/api/onboarding/jobs/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/scheduled_jobs.go:136` |
| `/api/patches/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go:65` |
| `/api/patches/policies` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go:61` |
| `/api/patches/policies/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go:62` |
| `/api/scheduled_jobs` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/scheduled_jobs.go:134` |
| `/api/scheduled_jobs/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/scheduled_jobs.go:135` |
| `/api/server/ansible-runner-settings` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_settings.go:21` |
| `/api/server/k3s/operator` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go:2154` |
| `/api/server/logs` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_logs.go:24` |
| `/api/server/logs/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_logs.go:25` |
| `/api/server/overview` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_overview.go:53` |
| `/api/server/services/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_actions.go:20` |
| `/api/server/site-worker-settings` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_settings.go:20` |
| `/api/server/time` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_time.go:15` |
| `/api/server/timezones` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_time.go:16` |
| `/api/server/wireguard/recover` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_wireguard.go:9` |
| `/api/server/workers` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_workers.go:89` |
| `/api/sites` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:43` |
| `/api/sites/device_map` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:44` |
| `/api/software/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:35` |
| `/api/system/go-backend/status` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/main.go:125` |
| `/api/user_site_assignments/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/user_site_assignments.go:43` |
| `/api/users` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/users.go:54` |
| `/api/users/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/users.go:55` |
| `/api/watchdogs` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/watchdogs.go:168` |
| `/api/watchdogs/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/watchdogs.go:169` |
| `/api/workflows/` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/workflows.go:105` |
| `/health` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/main.go:121` |
| `/healthz` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go:223` |
| `/live` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go:225` |
| `/live` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/main.go:123` |
| `/ready` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go:226` |
| `/ready` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/main.go:124` |
| `/startup` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go:224` |
| `/startup` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/main.go:122` |
| `/v1/command` | `operator` | `operator-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go:227` |
| `DELETE /api/admin/enrollment-codes/{code_id}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/admin_approvals.go:49` |
| `GET /api/aegis/status` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/aegis.go:19` |
| `GET /api/agent/hash_list` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_hash.go:54` |
| `GET /api/agent/install/download/{platform}` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_update.go:55` |
| `GET /api/agent/install/download/{token}/{platform}` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_update.go:56` |
| `GET /api/agent/metadata/{field_number}` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_reads.go:20` |
| `GET /api/agent/software-management/overrides` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_reads.go:21` |
| `GET /api/agent/update/download/{artifact_id}` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_update.go:54` |
| `GET /api/agent/update/manifest` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_update.go:53` |
| `GET /api/agents` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:83` |
| `GET /api/bootstrap/cluster/join/{id}/events` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:128` |
| `GET /api/bootstrap/state` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:319` |
| `GET /api/device/details/{hostname}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:92` |
| `GET /api/device/patches/{hostname}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go:63` |
| `GET /api/device/processes/{hostname}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_processes.go:36` |
| `GET /api/device/services/{hostname}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:30` |
| `GET /api/device/software/icon/{icon_hash}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:23` |
| `GET /api/device_filters/metadata` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters.go:107` |
| `GET /api/device_filters/search` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters.go:108` |
| `GET /api/devices` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:84` |
| `GET /api/devices/search` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_search.go:35` |
| `GET /api/devices/{device_guid}/agent-updates` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_maintenance.go:50` |
| `GET /api/devices/{device_id}/metadata_fields` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go:86` |
| `GET /api/devices/{device_id}/watchdogs` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:85` |
| `GET /api/devices/{guid}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:87` |
| `GET /api/metadata_fields` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go:84` |
| `GET /api/patches/audit` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go:60` |
| `GET /api/realtime/events` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/operator_realtime.go:38` |
| `GET /api/repo/current_hash` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/repo_hash.go:38` |
| `GET /api/server/backup/export` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go:114` |
| `GET /api/server/cluster` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:110` |
| `GET /api/server/cluster/banner` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:111` |
| `GET /api/server/cluster/events` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:112` |
| `GET /api/server/cluster/releases` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:113` |
| `GET /api/software/audit` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:32` |
| `GET /api/tunnel/active` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go:324` |
| `GET /api/tunnel/connect/status` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go:323` |
| `GET /api/tunnel/status` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go:322` |
| `GET /api/vnc/sessions` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:117` |
| `GET /api/vnc/viewers` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:112` |
| `GET /live` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go:364` |
| `GET /ready` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go:367` |
| `GET /startup` | `health` | `none` | `Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go:361` |
| `POST /api/admin/device-approvals/{approval_id}/approve` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/admin_approvals.go:51` |
| `POST /api/admin/device-approvals/{approval_id}/deny` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/admin_approvals.go:52` |
| `POST /api/aegis/force_reset` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/aegis.go:23` |
| `POST /api/aegis/rotate` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/aegis.go:22` |
| `POST /api/aegis/setup` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/aegis.go:20` |
| `POST /api/aegis/unlock` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/aegis.go:21` |
| `POST /api/agent/details` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_ingest.go:88` |
| `POST /api/agent/enroll/poll` | `agent-enrollment` | `public-enrollment-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_enrollment.go:110` |
| `POST /api/agent/enroll/request` | `agent-enrollment` | `public-enrollment-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_enrollment.go:109` |
| `POST /api/agent/heartbeat` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_ingest.go:86` |
| `POST /api/agent/patches/install-progress` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/patch_progress.go:20` |
| `POST /api/agent/rdp/ensure` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_vpn_runtime.go:23` |
| `POST /api/agent/script/request` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_script.go:6` |
| `POST /api/agent/status` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_ingest.go:87` |
| `POST /api/agent/token/refresh` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_tokens.go:88` |
| `POST /api/agent/update/progress` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_update_progress.go:66` |
| `POST /api/agent/vnc/ensure` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_vpn_runtime.go:22` |
| `POST /api/agent/vpn/ensure` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_vpn_runtime.go:20` |
| `POST /api/agent/vpn/ready` | `agent` | `device-token-dpop` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_vpn_runtime.go:21` |
| `POST /api/auth/login` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:325` |
| `POST /api/auth/logout` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:326` |
| `POST /api/auth/mfa/verify` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:327` |
| `POST /api/auth/passkeys/authenticate/options` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:334` |
| `POST /api/auth/passkeys/authenticate/verify` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:335` |
| `POST /api/auth/passkeys/register/options` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:332` |
| `POST /api/auth/passkeys/register/verify` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:333` |
| `POST /api/auth/password/reset` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:330` |
| `POST /api/bootstrap/admin/mfa/verify` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:324` |
| `POST /api/bootstrap/admin/recover` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:323` |
| `POST /api/bootstrap/admin/setup` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:322` |
| `POST /api/bootstrap/aegis/setup` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:320` |
| `POST /api/bootstrap/aegis/unlock` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/auth.go:321` |
| `POST /api/bootstrap/backup/analyze` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go:117` |
| `POST /api/bootstrap/backup/restore` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go:118` |
| `POST /api/bootstrap/cluster/join` | `public-bootstrap` | `bootstrap-contract` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:127` |
| `POST /api/device/description/{hostname}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:93` |
| `POST /api/device/patches/{hostname}/refresh` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go:64` |
| `POST /api/device/processes/{hostname}/terminate` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_processes.go:37` |
| `POST /api/device/services/{hostname}/action` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:31` |
| `POST /api/device/software/{hostname}/icon-override` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:25` |
| `POST /api/device/software/{hostname}/refresh` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:24` |
| `POST /api/device/software/{hostname}/uninstall` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:29` |
| `POST /api/device/software/{hostname}/uninstall-block` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:27` |
| `POST /api/device/software/{hostname}/uninstall-override` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:26` |
| `POST /api/device/software/{hostname}/uninstall-unblock` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:28` |
| `POST /api/device/update-agent/{hostname}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_maintenance.go:49` |
| `POST /api/device_filters/preview` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters.go:109` |
| `POST /api/devices/agent-maintenance` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/agent_maintenance.go:48` |
| `POST /api/devices/{device_id}/watchdogs/overrides` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:86` |
| `POST /api/devices/{guid}/purge` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:91` |
| `POST /api/devices/{guid}/quarantine` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:88` |
| `POST /api/devices/{guid}/revoke` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:90` |
| `POST /api/devices/{guid}/unquarantine` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go:89` |
| `POST /api/directory/providers/certificate` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/directory.go:84` |
| `POST /api/internal/vnc/session-event` | `internal-scheduler` | `internal-hmac` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:118` |
| `POST /api/notifications/notify` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/notifications.go:16` |
| `POST /api/remote-ops/session` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/remote_ops_sessions.go:85` |
| `POST /api/scripts/quick_run` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/quick_run.go:34` |
| `POST /api/server/backup/analyze` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go:115` |
| `POST /api/server/backup/restore` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go:116` |
| `POST /api/server/cluster/admissions/{id}/approve` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:116` |
| `POST /api/server/cluster/enable` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:114` |
| `POST /api/server/cluster/hmr/exit` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:123` |
| `POST /api/server/cluster/hmr/start` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:122` |
| `POST /api/server/cluster/invitations` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:115` |
| `POST /api/server/cluster/membership/scale` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:117` |
| `POST /api/server/cluster/nodes/{id}/maintenance` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:118` |
| `POST /api/server/cluster/nodes/{id}/remove` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:119` |
| `POST /api/server/cluster/operations/{id}/cancel` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:126` |
| `POST /api/server/cluster/operations/{id}/retry` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:125` |
| `POST /api/server/cluster/postgres/emergency-failover` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:121` |
| `POST /api/server/cluster/postgres/switchover` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:120` |
| `POST /api/server/cluster/updates` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go:124` |
| `POST /api/shell/disconnect` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/remote_shell.go:46` |
| `POST /api/shell/establish` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/remote_shell.go:45` |
| `POST /api/sites/assign` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:46` |
| `POST /api/sites/delete` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:45` |
| `POST /api/sites/rename` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:47` |
| `POST /api/sites/{site_id}/agent-install-links/{platform}/revoke` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:49` |
| `POST /api/sites/{site_id}/auto-approval` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/sites.go:48` |
| `POST /api/software/action/{action}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:33` |
| `POST /api/software/uninstall` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/software.go:34` |
| `POST /api/tunnel/connect` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go:321` |
| `POST /api/users/{username}/directory-cache` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/directory.go:87` |
| `POST /api/vnc/disconnect` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:115` |
| `POST /api/vnc/establish` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:113` |
| `POST /api/vnc/handoff` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:116` |
| `POST /api/vnc/session` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/vnc_runtime.go:114` |
| `POST /internal/cluster/aegis-key` | `internal-cluster` | `mutual-tls` | `Data/Engine/Containers/api-backend/cmd/api-backend/aegis_cluster_fanout.go:50` |
| `PUT /api/devices/{device_id}/metadata_fields/{field_number}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go:87` |
| `PUT /api/metadata_fields/{field_number}` | `operator-api` | `operator-session` | `Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go:85` |

??? example "Detailed Codex Breakdown"

    - Inventory source: `Tests/manifests/api-routes.json`.
    - AST extractor: `Tests/tools/routeinventory/main.go`.
    - Policy: `Tests/policy/check_api_routes.py`.
    - Go route registration remains authoritative; this page must not be edited independently from generated inventory.
