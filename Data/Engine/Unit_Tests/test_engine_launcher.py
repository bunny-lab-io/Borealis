from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[3]
ENGINE_SCRIPT = PROJECT_ROOT / "Engine.sh"


def _engine_source() -> str:
    return ENGINE_SCRIPT.read_text(encoding="utf-8")


def test_agent_binary_redeploy_command_is_exposed() -> None:
    source = _engine_source()

    assert "Engine.sh --redeploy-agent-binaries" in source
    assert "--redeploy-agent-binaries)" in source
    assert "redeploy_agent_binaries" in source


def test_agent_binary_redeploy_keeps_old_workers_until_health_cutover() -> None:
    source = _engine_source()
    function = source.split("redeploy_agent_binaries() {", 1)[1].split("\nusage() {", 1)[0]

    candidate_ready = function.index('agent_redeploy_probe_pod "${candidate}"')
    service_cutover = function.index('agent_redeploy_patch_service_revision "${service}" "${candidate_revision}"')
    service_ready = function.index('agent_redeploy_probe_service "${candidate}"')
    commit = function.index("AGENT_REDEPLOY_COMMIT_STARTED=1")
    old_delete = function.index('delete "pod/${pod}" --wait=true')
    registration_ready = function.index('agent_redeploy_wait_for_worker_registration "${worker_guid}"')

    assert candidate_ready < service_cutover < service_ready < commit < old_delete < registration_ready
    assert "Traefik route file unchanged" in function
    assert 'scale deployment/job-scheduler --replicas=0' in function


def test_agent_binary_redeploy_has_precommit_rollback() -> None:
    source = _engine_source()
    recovery = source.split("agent_redeploy_exit_trap() {", 1)[1].split("\nredeploy_agent_binaries() {", 1)[0]

    assert 'AGENT_REDEPLOY_COMMIT_STARTED}" -eq 0' in recovery
    assert 'agent_redeploy_patch_service_revision "${service}" "${old_revision}"' in recovery
    assert 'delete "pod/${candidate}"' in recovery
    assert "--ignore-not-found=true --wait=true --timeout=90s" in recovery
    assert 'scale deployment/job-scheduler --replicas=1' in recovery


def test_engine_ip_fallback_is_resolved_for_every_network_mode() -> None:
    source = _engine_source()
    function = source.split("resolve_engine_ip_fallback() {", 1)[1].split("\nvalidate_engine_fqdn() {", 1)[0]
    env_writer = source.split("write_compose_env() {", 1)[1].split("\ncompute_service_hash() {", 1)[0]

    assert 'normalize_engine_deployment_profile "${engine_profile}" >/dev/null' in function
    assert '== "internal-only"' not in function
    assert "detect_engine_ip_fallback" in function
    fallback_resolution = env_writer.index('engine_ip_fallback="$(resolve_engine_ip_fallback "${engine_profile}")"')
    local_ca_branch = env_writer.index('if [[ "${engine_profile}" == "internal-only" ]]')
    assert fallback_resolution < local_ca_branch
    assert "BOREALIS_ENGINE_IP_FALLBACK=${engine_ip_fallback}" in source
