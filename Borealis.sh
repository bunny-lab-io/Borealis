#!/usr/bin/env bash
#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh
# Linux parity for Borealis.ps1 (Engine focus). Aims to be OS-agnostic across Ubuntu/Debian, RHEL/Rocky/Fedora, Arch.
# - Installs system deps when needed (python3, venv, pip, curl, unzip, tesseract)
# - Bundles portable NodeJS into Dependencies/NodeJS to keep a known-good version (no root required)
# - Mirrors Windows flow: create Engine venv, stage Data/Engine, stage web-interface, Vite dev or prod build, Flask launch
# - Supports flags: --server/--agent (agent kept for compatibility), --vite/--flask, --quick, --engine-tests,
#                   --EngineProduction, --EngineDev (auto-select server mode), --enrollmentcode, plus interactive menu
# NOTE: This script focuses on ENGINE parity. Agent paths remain but are not the goal here.

set -o errexit
set -o nounset
set -o pipefail

# ---- Colors / Icons ----
BOREALIS_BLUE="\033[38;5;39m"
DARK_GRAY="\033[1;30m"
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
RESET="\033[0m"
CHECKMARK="[OK]"; HOURGLASS="[WAIT]"; CROSSMARK="[X]"; INFO="[i]"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# ---- CLI flags (parity with Borealis.ps1) ----
SERVER_FLAG=0
AGENT_FLAG=0
VITE_FLAG=0
FLASK_FLAG=0
QUICK_FLAG=0
ENGINE_TESTS_FLAG=0
ENGINE_PROD_FLAG=0
ENGINE_DEV_FLAG=0
ENROLLMENT_CODE=""

while (( "$#" )); do
  case "$1" in
    -Server|--server) SERVER_FLAG=1 ;;
    -Agent|--agent) AGENT_FLAG=1 ;;
    -Vite|--vite) VITE_FLAG=1 ;;
    -Flask|--flask) FLASK_FLAG=1 ;;
    -Quick|--quick) QUICK_FLAG=1 ;;
    -EngineTests|--engine-tests) ENGINE_TESTS_FLAG=1 ;;
    -EngineProduction|--engine-production) ENGINE_PROD_FLAG=1 ;;
    -EngineDev|--engine-dev) ENGINE_DEV_FLAG=1 ;;
    # Enrollment: prefer lowercase --enrollmentcode, keep old alias for compatibility
    -EnrollmentCode|--EnrollmentCode|--enrollmentcode|--enrollment-code) shift; ENROLLMENT_CODE="${1:-}" ;;
    *) ;; # ignore unknown for flexibility
  esac
  shift || true
done

# ---- Helpers ----
run_step() {
  local message="$1"; shift
  printf "%s %s... " "${HOURGLASS}" "$message"
  if "$@"; then
    printf "\r%s %s\n" "${CHECKMARK}" "$message"
  else
    printf "\r%s %s - Failed\n" "${CROSSMARK}" "$message" 1>&2
    exit 1
  fi
}

detect_distro() {
  DISTRO_ID="unknown"; DISTRO_LIKE=""
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID=${ID:-unknown}
    DISTRO_LIKE=${ID_LIKE:-}
  fi
}

need_sudo() { [ "${EUID:-$(id -u)}" -ne 0 ]; }

ensure_engine_log_dir() {
  mkdir -p "${SCRIPT_DIR}/Engine/Logs"
  echo "${SCRIPT_DIR}/Engine/Logs"
}

write_vite_log() {
  local msg="$1"; local svc="${2:-vite-dev}"
  local logdir; logdir=$(ensure_engine_log_dir)
  printf "%s-%s-%s\n" "$(date +%FT%T)" "$svc" "$msg" >> "${logdir}/vite.log"
}

# ---- Agent (settings-only parity) ----
configure_agent_settings() {
  echo -e "${GREEN}Configuring Borealis Agent settings...${RESET}"
  local settings_dir="${SCRIPT_DIR}/Agent/Borealis/Settings"
  local legacy_settings_dir="${SCRIPT_DIR}/Agent/Settings"
  local server_url_path="${settings_dir}/server_url.txt"
  local config_path="${settings_dir}/agent_settings.json"

  mkdir -p "${settings_dir}"
  if [[ ! -f "${server_url_path}" && -f "${legacy_settings_dir}/server_url.txt" ]]; then
    cp -f "${legacy_settings_dir}/server_url.txt" "${server_url_path}" 2>/dev/null || true
  fi

  local default_url="https://localhost:5000"
  local current_url="${default_url}"
  if [[ -n "${BOREALIS_SERVER_URL:-}" ]]; then
    current_url="${BOREALIS_SERVER_URL}"
  elif [[ -f "${server_url_path}" ]]; then
    current_url="$(head -n 1 "${server_url_path}" || echo "${default_url}")"
  fi

  if [[ -t 0 ]]; then
    read -r -p "Server URL [${current_url}]: " input_url
  else
    input_url=""
  fi
  input_url="${input_url:-${current_url}}"
  input_url="$(echo -n "${input_url}" | tr -d '\r' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  if [[ -z "${input_url}" ]]; then input_url="${default_url}"; fi
  printf "%s" "${input_url}" > "${server_url_path}"

  local provided_code="${ENROLLMENT_CODE:-}"
  if [[ -z "${provided_code}" && -n "${BOREALIS_ENROLLMENT_CODE:-}" ]]; then
    provided_code="${BOREALIS_ENROLLMENT_CODE}"
  fi

  if [[ -z "${provided_code}" ]]; then
    local existing_code=""
    if [[ -f "${config_path}" ]]; then
      if command -v python3 >/dev/null 2>&1 || command -v python >/dev/null 2>&1; then
        existing_code="$(CONFIG_PATH="${config_path}" python3 - <<'PY' 2>/dev/null || CONFIG_PATH="${config_path}" python - <<'PY' 2>/dev/null || true
import json, os
path = os.environ.get("CONFIG_PATH")
try:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    if isinstance(data, dict):
        print(data.get("enrollment_code") or data.get("installer_code") or "")
except Exception:
    pass
PY
        )"
      fi
    fi
    existing_code="${existing_code:-}"
    if [[ -t 0 ]]; then
      read -r -p "Enrollment Code [${existing_code}]: " input_code
    else
      input_code=""
    fi
    if [[ -n "${input_code// }" ]]; then
      provided_code="${input_code}"
    elif [[ -n "${existing_code}" ]]; then
      provided_code="${existing_code}"
    else
      provided_code=""
    fi
  fi

  local py_bin
  py_bin="$(command -v python3 || command -v python || true)"

  if [[ -n "${py_bin}" ]]; then
    CONFIG_PATH="${config_path}" ENROLLMENT_CODE_VALUE="${provided_code}" "${py_bin}" - <<'PY'
import json, os

path = os.environ["CONFIG_PATH"]
code = os.environ.get("ENROLLMENT_CODE_VALUE", "")
defaults = {
    "config_file_watcher_interval": 2,
    "agent_id": "",
    "regions": {},
    "enrollment_code": "",
    "installer_code": "",
}
data = defaults.copy()
if os.path.exists(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            existing = json.load(fh)
        if isinstance(existing, dict):
            data.update(existing)
    except Exception:
        pass
data["enrollment_code"] = code
data["installer_code"] = code
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
  else
    cat > "${config_path}" <<EOF
{
  "config_file_watcher_interval": 2,
  "agent_id": "",
  "regions": {},
  "enrollment_code": "${provided_code}",
  "installer_code": "${provided_code}"
}
EOF
  fi

  if [[ -n "${provided_code}" ]]; then
    echo -e "${GREEN}Enrollment code saved to agent_settings.json.${RESET}"
  else
    echo -e "${YELLOW}Enrollment code cleared in agent_settings.json.${RESET}"
  fi
  echo -e "${INFO} Agent runtime remains Windows-first; Linux flow configures settings only." 
}

# ---- Dependency Installation (Linux) ----
install_shared_dependencies() {
  detect_distro
  if command -v python3 >/dev/null 2>&1 && command -v pip3 >/dev/null 2>&1; then :; else
    case "$DISTRO_ID" in
      ubuntu|debian)
        sudo apt update -qq && sudo apt install -y python3 python3-venv python3-pip curl unzip ca-certificates ;;
      rhel|centos|fedora|rocky)
        if command -v dnf >/dev/null 2>&1; then sudo dnf install -y python3 python3-pip python3-virtualenv curl unzip ca-certificates ; else sudo yum install -y python3 python3-pip python3-virtualenv curl unzip ca-certificates ; fi ;;
      arch)
        sudo pacman -Sy --noconfirm python python-pip python-virtualenv curl unzip ca-certificates ;;
      *) : ;;
    esac
  fi
}

install_tesseract() {
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian)
      sudo apt update -qq && sudo apt install -y tesseract-ocr ;;
    rhel|centos|fedora|rocky)
      if command -v dnf >/dev/null 2>&1; then sudo dnf install -y tesseract ; else sudo yum install -y tesseract ; fi ;;
    arch)
      sudo pacman -Sy --noconfirm tesseract ;;
    *) : ;;
  esac
}

NODE_VERSION="v23.11.0"
NODE_DIR="${SCRIPT_DIR}/Dependencies/NodeJS"
NODE_BIN="${NODE_DIR}/bin/node"
NPM_BIN="${NODE_DIR}/bin/npm"
NPX_BIN="${NODE_DIR}/bin/npx"

install_node_portable() {
  if [[ -x "$NPM_BIN" ]]; then return 0; fi
  mkdir -p "$NODE_DIR"
  local tarball="node-${NODE_VERSION}-linux-x64.tar.xz"
  local url="https://nodejs.org/dist/${NODE_VERSION}/${tarball}"
  local dl_path="${SCRIPT_DIR}/Dependencies/${tarball}"
  write_vite_log "Downloading NodeJS ${NODE_VERSION} from ${url}" "bootstrap"
  curl -fsSL -o "$dl_path" "$url"
  rm -rf "${NODE_DIR:?}"/*
  tar -xJf "$dl_path" -C "$NODE_DIR" --strip-components=1
  rm -f "$dl_path"
}

ensure_node_bins() {
  if [[ -x "$NPM_BIN" ]]; then export PATH="${NODE_DIR}/bin:${PATH}"; return 0; fi
  if command -v npm >/dev/null 2>&1; then
    NPM_BIN="$(command -v npm)"; NPX_BIN="$(command -v npx || echo npx)"; return 0
  fi
  echo -e "${YELLOW}npm not found on PATH; installing portable NodeJS...${RESET}"
  install_node_portable
  export PATH="${NODE_DIR}/bin:${PATH}"
}

install_server_dependencies() {
  run_step "Dependency: Python (system)" install_shared_dependencies
  run_step "Dependency: Tesseract-OCR (system)" install_tesseract
  run_step "Dependency: NodeJS (portable)" install_node_portable
}

# Prefer a resilient resolver for the Engine venv interpreter (some venvs only have 'python')
engine_python_bin() {
  if [[ -x "${SCRIPT_DIR}/Engine/bin/python3" ]]; then
    echo "${SCRIPT_DIR}/Engine/bin/python3"
  elif [[ -x "${SCRIPT_DIR}/Engine/bin/python" ]]; then
    echo "${SCRIPT_DIR}/Engine/bin/python"
  else
    echo ""
  fi
}

# ---- Engine TLS material (parity with Ensure-EngineTlsMaterial) ----
ensure_engine_tls_material() {
  local py="$1" # engine venv python
  local cert_root_arg="$2" # optional path with pre-provided certs
  local effective_root=""

  if [[ -x "$py" ]]; then
    local code='from Data.Engine.security import certificates; certificates.ensure_certificate(); print(certificates.engine_certificates_root())'
    set +e
    effective_root="$("$py" -c "$code" 2>/dev/null | tail -n 1 | tr -d '\r' || true)"
    set -e
  fi

  if [[ -z "$effective_root" && -n "${cert_root_arg}" ]]; then
    if [[ -f "${cert_root_arg}/borealis-server-cert.pem" && -f "${cert_root_arg}/borealis-server-key.pem" ]]; then
      effective_root="${cert_root_arg}"
    else
      write_vite_log "Provided certificate root '${cert_root_arg}' missing expected TLS material; using Engine runtime certificates instead." "tls"
    fi
  fi

  if [[ -z "$effective_root" ]]; then
    effective_root="${SCRIPT_DIR}/Engine/Certificates"
    mkdir -p "$effective_root"
  fi

  export BOREALIS_CERT_DIR="$effective_root"
  export BOREALIS_TLS_CERT="${effective_root}/borealis-server-cert.pem"
  export BOREALIS_TLS_KEY="${effective_root}/borealis-server-key.pem"
  export BOREALIS_TLS_BUNDLE="${effective_root}/borealis-server-bundle.pem"
}

# ---- Engine web interface staging (parity with Ensure-EngineWebInterface) ----
ensure_engine_web_interface() {
  local project_root="$1"
  local dest="${project_root}/Engine/web-interface"
  local stage="${project_root}/Data/Engine/web-interface"
  [[ -d "$stage" ]] || { echo -e "${RED}Engine web interface source missing at '$stage'.${RESET}" >&2; return 1; }
  rm -rf "$dest" 2>/dev/null || true
  mkdir -p "$dest"
  cp -a "${stage}/." "$dest/"
  [[ -f "${dest}/package.json" ]] || { echo -e "${RED}Failed to stage Engine web interface into '$dest'.${RESET}" >&2; return 1; }
}

# ---- Engine build+launch flow ----
create_engine_venv_and_stage_data() {
  local venv_dir="${SCRIPT_DIR}/Engine"
  local engine_src="${SCRIPT_DIR}/Data/Engine"
  local data_dest="${venv_dir}/Data/Engine"

  [[ -d "$venv_dir" ]] || python3 -m venv "$venv_dir"
  mkdir -p "${venv_dir}/Data"

  rm -rf "$data_dest" 2>/dev/null || true
  mkdir -p "$data_dest"

  # Copy everything except Assemblies (handled separately)
  shopt -s dotglob nullglob
  for item in "${engine_src}"/*; do
    base="$(basename "$item")"
    if [[ "$base" == "Assemblies" ]]; then continue; fi
    cp -a "$item" "$data_dest/"
  done
  shopt -u dotglob nullglob

  # Assemblies runtime folder
  [[ -d "${SCRIPT_DIR}/Engine/Assemblies" ]] || {
    if [[ -d "${engine_src}/Assemblies" ]]; then
      cp -a "${engine_src}/Assemblies" "${SCRIPT_DIR}/Engine/Assemblies"
    else
      mkdir -p "${SCRIPT_DIR}/Engine/Assemblies"
    fi
  }

  # Auth_Tokens and database
  mkdir -p "${SCRIPT_DIR}/Engine/Auth_Tokens"
  # database.db will be created by the app if not present; ensure dir exists
}

install_engine_python_deps() {
  local venv_py
  venv_py="$(engine_python_bin)"
  if [[ -z "$venv_py" ]]; then
    # Try to create the venv if it doesn't exist yet
    python3 -m venv "${SCRIPT_DIR}/Engine" || true
    venv_py="$(engine_python_bin)"
  fi
  local engine_src="${SCRIPT_DIR}/Data/Engine"
  local reqs=( "${engine_src}/engine-requirements.txt" "${engine_src}/requirements.txt" )
  for r in "${reqs[@]}"; do
    if [[ -f "$r" && -n "$venv_py" ]]; then
      "$venv_py" -m pip install --disable-pip-version-check -q -r "$r"
      return 0
    fi
  done
  return 0
}

vite_web_frontend_install() {
  local engine_ui_dest="${SCRIPT_DIR}/Engine/web-interface"
  ensure_node_bins
  ( cd "$engine_ui_dest" && "$NPM_BIN" install --silent --no-fund --audit=false >/dev/null )
}

vite_web_frontend_start() {
  local mode="$1" # developer|production
  local engine_ui_dest="${SCRIPT_DIR}/Engine/web-interface"
  ensure_node_bins
  ensure_engine_tls_material "$(engine_python_bin)" ""

  if [[ "$mode" == "developer" ]]; then
    local logdir; logdir=$(ensure_engine_log_dir)
    local stdout_log="${logdir}/vite-dev.stdout.log"
    local stderr_log="${logdir}/vite-dev.stderr.log"
    mv -f "$stdout_log" "${stdout_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    mv -f "$stderr_log" "${stderr_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    write_vite_log "Starting Vite dev server using TLS (cert=$BOREALIS_TLS_CERT bundle=$BOREALIS_TLS_BUNDLE)" "vite-dev"
    (
      cd "$engine_ui_dest"
      PATH="${NODE_DIR}/bin:${PATH}" nohup "$NPM_BIN" run dev >"$stdout_log" 2>"$stderr_log" &
    )
  else
    write_vite_log "Executing npm run build for production WebUI assets." "vite-build"
    ( cd "$engine_ui_dest" && "$NPM_BIN" run build )
  fi
}

flask_engine_launch() {
  local mode="$1" # production|developer
  pushd "${SCRIPT_DIR}/Engine" >/dev/null
  local py
  py="$(engine_python_bin)"
  if [[ -z "$py" ]]; then
    python3 -m venv "${SCRIPT_DIR}/Engine" || true
    py="$(engine_python_bin)"
  fi
  local prev_mode="${BOREALIS_ENGINE_MODE:-}"
  local prev_port="${BOREALIS_ENGINE_PORT:-}"
  local prev_root="${BOREALIS_PROJECT_ROOT:-}"
  export BOREALIS_ENGINE_MODE="$mode"
  export BOREALIS_ENGINE_PORT="5000"
  export BOREALIS_PROJECT_ROOT="$SCRIPT_DIR"
  echo -e "\n${GREEN}Launching Borealis Engine...${RESET}"
  echo "===================================================================================="
  echo "${HOURGLASS} Engine Socket Server Started..."
  "$py" -m Data.Engine.bootstrapper || true
  # restore env
  if [[ -n "$prev_mode" ]]; then export BOREALIS_ENGINE_MODE="$prev_mode"; else unset BOREALIS_ENGINE_MODE; fi
  if [[ -n "$prev_port" ]]; then export BOREALIS_ENGINE_PORT="$prev_port"; else unset BOREALIS_ENGINE_PORT; fi
  if [[ -n "$prev_root" ]]; then export BOREALIS_PROJECT_ROOT="$prev_root"; else unset BOREALIS_PROJECT_ROOT; fi
  popd >/dev/null
}

# ---- Tests parity ----
if (( ENGINE_TESTS_FLAG )); then
  export BOREALIS_PROJECT_ROOT="${SCRIPT_DIR}"
  PYTHON_BIN="$(command -v python3 || command -v python || true)"
  if [[ -z "${PYTHON_BIN}" ]]; then
    echo -e "${RED}Python interpreter not found. Install Python 3 to run Engine tests.${RESET}" >&2
    exit 1
  fi
  "${PYTHON_BIN}" -m pytest 'Data/Engine/Unit_Tests'
  exit $?
fi

# ---- Banner ----
clear || true
printf "%b" "${BOREALIS_BLUE}"
cat << 'EOF'
:::::::::   ::::::::  :::::::::  ::::::::::     :::     :::        ::::::::::: :::::::: 
:+:    :+: :+:    :+: :+:    :+: :+:          :+: :+:   :+:            :+:    :+:    :+:
+:+    +:+ +:+    +:+ +:+    +:+ +:+         +:+   +:+  +:+            +:+    +:+       
+#++:++#+  +#+    +:+ +#++:++#:  +#++:++#   +#++:++#++: +#+            +#+    +#++:++#++
+#+    +#+ +#+    +#+ +#+    +#+ +#+        +#+     +#+ +#+            +#+           +#+
#+#    #+# #+#    #+# #+#    #+# #+#        #+#     #+# #+#            #+#    #+#    #+#
#########   ########  ###    ### ########## ###     ### ########## ########### ######## 
EOF
printf "%b" "${RESET}"
printf "%b\n" "${DARK_GRAY}Automation Platform${RESET}"

# ---- Menus ----
server_menu() {
  echo -e "\nConfigure Borealis Engine Mode:"
  echo -e " 1) Build & Launch > Production Flask Server @ https://localhost:5000"
  echo -e " 2) [Skip Build] & Immediately Launch > Production Flask Server @ https://localhost:5000"
  echo -e " 3) Launch > [Hotload-Ready] Vite Dev Server @ https://localhost:5173"
  read -r -p "Enter choice [1/2/3]: " modeChoice

  case "$modeChoice" in
    1) borealis_operation_mode="production" ;;
    2) borealis_operation_mode="production" ;;
    3) borealis_operation_mode="developer" ;;
    *) echo -e "${RED}Invalid mode choice${RESET}"; return 1 ;;
  esac

  echo -e "${GREEN}Ensuring Engine Dependencies Exist...${RESET}"
  install_server_dependencies
  export PATH="${NODE_DIR}/bin:${PATH}"

  if [[ "$modeChoice" == "2" ]]; then
    # Immediate launch of Flask without rebuild
    flask_engine_launch "$borealis_operation_mode"
    return 0
  fi

  run_step "Create Borealis Engine Virtual Python Environment & Stage Data" create_engine_venv_and_stage_data
  run_step "Install Engine Python Dependencies" install_engine_python_deps
  run_step "Copy Engine WebUI Files" ensure_engine_web_interface "$SCRIPT_DIR"
  run_step "Vite Web Frontend: Install NPM Packages" vite_web_frontend_install
  run_step "Vite Web Frontend: Start (${borealis_operation_mode})" vite_web_frontend_start "$borealis_operation_mode"
  flask_engine_launch "$borealis_operation_mode"
}

main_menu() {
  echo -e "\nPlease choose which function you want to launch:"
  echo -e " 1) Borealis Engine"
  echo -e " 2) Borealis Agent (not the focus on Linux)"
  echo -e " 3) Exit"
  read -r -p "Enter a number: " choice
  case "$choice" in
    1) server_menu ;;
    2) configure_agent_settings ;;
    3) exit 0 ;;
    *) echo -e "${RED}Invalid selection. Exiting...${RESET}"; exit 1 ;;
  esac
}

# ---- Flag-driven auto-select (parity logic) ----
if [[ $SERVER_FLAG -eq 1 && $AGENT_FLAG -eq 1 ]]; then
  echo -e "${RED}Cannot use --server and --agent together.${RESET}"; exit 1
fi

if [[ $AGENT_FLAG -eq 1 ]]; then
  configure_agent_settings
  exit $?
fi

# Auto-select main menu option for Server when EngineProduction/EngineDev provided
if [[ $ENGINE_PROD_FLAG -eq 1 || $ENGINE_DEV_FLAG -eq 1 ]]; then
  SERVER_FLAG=1
fi

if [[ $SERVER_FLAG -eq 1 ]]; then
  if [[ $VITE_FLAG -eq 1 && $FLASK_FLAG -eq 1 ]]; then
    echo -e "${RED}Cannot combine --vite and --flask.${RESET}"; exit 1
  fi
  if [[ $ENGINE_PROD_FLAG -eq 1 && $ENGINE_DEV_FLAG -eq 1 ]]; then
    echo -e "${RED}Cannot combine --EngineProduction and --EngineDev.${RESET}"; exit 1
  fi

  if [[ $ENGINE_PROD_FLAG -eq 1 || $ENGINE_DEV_FLAG -eq 1 ]]; then
    # Map to menu choice automatically
    if [[ $ENGINE_PROD_FLAG -eq 1 ]]; then
      if [[ $QUICK_FLAG -eq 1 ]]; then
        server_menu <<< $'2\n' # skip build, immediate launch
      else
        server_menu <<< $'1\n' # build & launch
      fi
      exit $?
    fi
    if [[ $ENGINE_DEV_FLAG -eq 1 ]]; then
      server_menu <<< $'3\n' # Vite dev
      exit $?
    fi
  fi

  # Resolve server mode from flags (legacy compatibility)
  if [[ $VITE_FLAG -eq 1 ]]; then
    server_menu <<< $'3\n'
  elif [[ $FLASK_FLAG -eq 1 && $QUICK_FLAG -eq 1 ]]; then
    server_menu <<< $'2\n'
  else
    server_menu <<< $'1\n'
  fi
  exit $?
fi

# Default to interactive menu
main_menu
