#!/usr/bin/env bash
# Borealis Linux compatibility router.

set -o errexit
set -o nounset
set -o pipefail

if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
else
  SCRIPT_DIR="$(pwd)"
fi
cd "${SCRIPT_DIR}"

usage() {
  cat <<'EOF'
Usage:
  Borealis.sh --EngineProduction
  Borealis.sh --EngineDev
  Borealis.sh --Agent [agent options]
  Borealis.sh --service <service> <action> [mode]
EOF
}

main() {
  local command="${1:-}"
  case "${command}" in
    -EngineProduction|--EngineProduction|--engine-production)
      exec "${SCRIPT_DIR}/Engine.sh" deploy prod
      ;;
    -EngineDev|--EngineDev|--engine-dev)
      exec "${SCRIPT_DIR}/Engine.sh" deploy dev
      ;;
    -Agent|--Agent|--agent)
      shift || true
      exec "${SCRIPT_DIR}/Agent.sh" deploy "$@"
      ;;
    --service|-service)
      shift || true
      exec "${SCRIPT_DIR}/Engine.sh" --service "$@"
      ;;
    -Server|--server)
      exec "${SCRIPT_DIR}/Engine.sh" deploy prod
      ;;
    -h|--help|help|"")
      usage
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
