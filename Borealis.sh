#!/usr/bin/env bash
#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh
# Linux parity for Borealis.ps1 (Ubuntu/Rocky/Fedora/RHEL). Experimental but feature-complete.

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
AGENT_ACTION=""
VITE_FLAG=0
FLASK_FLAG=0
QUICK_FLAG=0

while (( "$#" )); do
  case "$1" in
    -Server|--server) SERVER_FLAG=1 ;;
    -Agent|--agent) AGENT_FLAG=1 ;;
    -AgentAction|--agent-action) shift; AGENT_ACTION="${1:-}" ;;
    -Vite|--vite) VITE_FLAG=1 ;;
    -Flask|--flask) FLASK_FLAG=1 ;;
    -Quick|--quick) QUICK_FLAG=1 ;;
    *) ;; # ignore unknown for flexibility
  esac
  shift || true
done

# ---- Banner ----
clear || true
echo -e "${BOREALIS_BLUE}"
cat << 'EOF'
�����������                                        ����   ���         
����۰�������                                      �����  ���          
 ����    ����  ������  ��������   ������   ������   ����  ����   ����� 
 �����������  ��۰���۰���۰���� ��۰���� ��������  ���� �����  ��۰�  
 ���۰������۰��� ���� ���� ��� ��������   �������  ����  ���� ������� 
 ����    ���۰��� ���� ����     ���۰��   ��۰����  ����  ����  �������
 ����������� ��������  �����    �������� ���������� ����� ����� ������ 
�����������   ������  �����      ������   �������� ����� ����� ������  
EOF
echo -e "${RESET}"
echo -e "${DARK_GRAY}Automation Platform${RESET}"

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

ensure_log_dir() { mkdir -p "${SCRIPT_DIR}/Logs/Agent"; }
log_agent() { ensure_log_dir; printf "[%s] %s\n" "$(date +%F\ %T)" "$1" >> "${SCRIPT_DIR}/Logs/Agent/$2"; }

need_sudo() { [ "${EUID:-$(id -u)}" -ne 0 ]; }

# ---- Dependency Installation (Linux) ----
install_shared_dependencies() {
  detect_distro
  if command -v python3 >/dev/null 2>&1 && command -v pip3 >/dev/null 2>&1; then :; else
    case "$DISTRO_ID" in
      ubuntu|debian)
        sudo apt update -qq && sudo apt install -y python3 python3-venv python3-pip curl unzip ;;
      rhel|centos|fedora|rocky)
        if command -v dnf >/dev/null 2>&1; then sudo dnf install -y python3 python3-pip curl unzip ; else sudo yum install -y python3 python3-pip curl unzip ; fi ;;
      arch)
        sudo pacman -Sy --noconfirm python python-pip curl unzip ;; 
      *) : ;; 
    esac
  fi
}

NODE_VERSION="v23.11.0"
NODE_DIR="${SCRIPT_DIR}/Dependencies/NodeJS"
NODE_BIN="${NODE_DIR}/bin/node"
NPM_BIN="${NODE_DIR}/bin/npm"
NPX_BIN="${NODE_DIR}/bin/npx"

install_server_dependencies() {
  # Tesseract via system packages; OCR engines code uses system binary on Linux
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian)
      sudo apt update -qq && sudo apt install -y tesseract-ocr ;; 
    rhel|centos|fedora|rocky)
      if command -v dnf >/dev/null 2>&1; then sudo dnf install -y tesseract; else sudo yum install -y tesseract; fi ;;
    arch)
      sudo pacman -Sy --noconfirm tesseract ;;
    *) : ;;
  esac

  # NodeJS (portable into Dependencies/NodeJS)
  if [[ ! -x "$NODE_BIN" ]]; then
    mkdir -p "$NODE_DIR"
    local tarball="node-${NODE_VERSION}-linux-x64.tar.xz"
    local url="https://nodejs.org/dist/${NODE_VERSION}/${tarball}"
    local dl_path="${SCRIPT_DIR}/Dependencies/${tarball}"
    run_step "Dependency: NodeJS (${NODE_VERSION})" bash -c "
      curl -fsSL -o '${dl_path}' '${url}' && \
      rm -rf '${NODE_DIR:?}'/* && \
      mkdir -p '${NODE_DIR}' && \
      tar -xJf '${dl_path}' -C '${NODE_DIR}' --strip-components=1 && \
      rm -f '${dl_path}'
    "
  fi
  export PATH="${NODE_DIR}/bin:${PATH}"
}

ensure_node_bins() {
  if [[ -x "$NPM_BIN" ]]; then return 0; fi
  if command -v npm >/dev/null 2>&1; then
    NPM_BIN="$(command -v npm)"; NPX_BIN="$(command -v npx || echo npx)"; return 0
  fi
  echo -e "${RED}npm not found. Run server dependency install first.${RESET}" >&2
  return 1
}

install_agent_dependencies() {
  # No AHK on Linux; ensure python only
  install_shared_dependencies
}

# ---- Process/task helpers (Linux) ----
kill_agent_processes() {
  # Kill only Python processes under this repo's Agent venv
  if command -v pgrep >/dev/null 2>&1; then
    pgrep -f "${SCRIPT_DIR}/Agent/.*/python.*${SCRIPT_DIR}/Agent/Borealis/agent.py" >/dev/null 2>&1 && \
      pkill -f "${SCRIPT_DIR}/Agent/.*/python.*${SCRIPT_DIR}/Agent/Borealis/agent.py" || true
  else
    pkill -f "Agent/Borealis/agent.py" || true
  fi
}

ensure_cron_entry() {
  # args: who command
  local who="$1"; shift
  local cmd="$*"
  local tmp
  tmp="$(mktemp)"
  if [[ "$who" == "root" ]]; then
    if need_sudo; then SUDO=sudo; else SUDO=""; fi
    $SUDO crontab -l 2>/dev/null | grep -vF -- "$cmd" > "$tmp" || true
    echo "@reboot ${cmd}" >> "$tmp"
    $SUDO crontab "$tmp"
  else
    crontab -l 2>/dev/null | grep -vF -- "$cmd" > "$tmp" || true
    echo "@reboot ${cmd}" >> "$tmp"
    crontab "$tmp"
  fi
  rm -f "$tmp"
}

remove_cron_entries() {
  # Remove entries we added previously
  local sys_cmd user_cmd
  sys_cmd="$1"; shift
  user_cmd="$1"; shift || true
  local tmp
  tmp="$(mktemp)"; if need_sudo; then SUDO=sudo; else SUDO=""; fi
  $SUDO crontab -l 2>/dev/null | grep -vF -- "$sys_cmd" > "$tmp" || true; $SUDO crontab "$tmp" || true
  crontab -l 2>/dev/null | grep -vF -- "$user_cmd" > "$tmp" || true; crontab "$tmp" || true
  rm -f "$tmp"
}

# ---- Agent deployment (Install / Repair / Remove) ----
create_agent_venv_and_files() {
  local venvFolder="Agent"
  local agentDest="${venvFolder}/Borealis"
  python3 -m venv "$venvFolder" 2>/dev/null || true
  mkdir -p "$agentDest"
  # Fresh copy of agent payload
  rm -rf "${agentDest:?}"/*
  cp -f "Data/Agent/agent.py"              "$agentDest/"
  cp -f "Data/Agent/role_manager.py"       "$agentDest/"
  cp -f "Data/Agent/agent_deployment.py"   "$agentDest/" 2>/dev/null || true
  [ -f "Data/Agent/Borealis.ico" ] && cp -f "Data/Agent/Borealis.ico" "$agentDest/"
  [ -d "Data/Agent/Python_API_Endpoints" ] && cp -r "Data/Agent/Python_API_Endpoints" "$agentDest/"
  [ -d "Data/Agent/Roles" ] && cp -r "Data/Agent/Roles" "$agentDest/"
  # Linux wrapper to guarantee working dir and capture logs
  cat > "${agentDest}/launch_service.sh" << 'SH'
#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail
ROOT_DIR="$(cd -- "$(dirname -- "$0")" && pwd)"
cd "$ROOT_DIR"
LOG_DIR="$(cd -- "$ROOT_DIR/../../Logs/Agent" && pwd 2>/dev/null || echo "$ROOT_DIR/../../Logs/Agent")"
mkdir -p "$LOG_DIR"
PY_BIN="${ROOT_DIR}/../bin/python3"
exec "$PY_BIN" "$ROOT_DIR/agent.py" --system-service --config SYSTEM >>"$LOG_DIR/svc.out.log" 2>>"$LOG_DIR/svc.err.log"
SH
  chmod +x "${agentDest}/launch_service.sh"

  # pip deps
  if [[ -f "Data/Agent/agent-requirements.txt" ]]; then
    "${SCRIPT_DIR}/Agent/bin/python3" -m pip install --disable-pip-version-check -q -r "Data/Agent/agent-requirements.txt"
  fi
}

ensure_agent_tasks() {
  # Register @reboot cron entries for system (root) and current user
  local agentDest="${SCRIPT_DIR}/Agent/Borealis"
  local sys_cmd="bash '${agentDest}/launch_service.sh'"
  local user_cmd="bash -lc 'cd "${agentDest}" && "${SCRIPT_DIR}/Agent/bin/python3" ./agent.py --config CURRENTUSER'"

  # Root/system entry
  if need_sudo; then
    echo -e "${YELLOW}Agent SYSTEM cron requires sudo. Prompting...${RESET}"
  fi
  ensure_cron_entry root "$sys_cmd"
  ensure_cron_entry user "$user_cmd"
}

install_or_update_agent() {
  echo -e "${GREEN}Ensuring Agent Dependencies...${RESET}"
  install_shared_dependencies
  install_agent_dependencies
  log_agent "=== Install/Update start ===" install.log
  kill_agent_processes || true
  run_step "Create Agent venv and deploy files" create_agent_venv_and_files
  run_step "Register cron tasks (SYSTEM, User)" ensure_agent_tasks
  log_agent "=== Install/Update end ===" install.log
}

repair_agent() {
  log_agent "=== Repair start ===" Repair.log
  kill_agent_processes || true
  install_or_update_agent
  log_agent "=== Repair end ===" Repair.log
}

remove_agent() {
  log_agent "=== Removal start ===" Removal.log
  kill_agent_processes || true
  local sys_cmd="bash '${SCRIPT_DIR}/Agent/Borealis/launch_service.sh'"
  local user_cmd="bash -lc 'cd "${SCRIPT_DIR}/Agent/Borealis" && "${SCRIPT_DIR}/Agent/bin/python3" ./agent.py --config CURRENTUSER'"
  remove_cron_entries "$sys_cmd" "$user_cmd" || true
  rm -rf "${SCRIPT_DIR}/Agent" || true
  log_agent "=== Removal end ===" Removal.log
}

launch_user_helper_now() {
  local py="${SCRIPT_DIR}/Agent/bin/python3"
  local helper="${SCRIPT_DIR}/Agent/Borealis/agent.py"
  if [[ -x "$py" && -f "$helper" ]]; then
    (cd "${SCRIPT_DIR}/Agent/Borealis" && nohup "$py" -W ignore::SyntaxWarning "$helper" --config CURRENTUSER >/dev/null 2>&1 & )
    echo -e "${GREEN}Launched user-session helper.${RESET}"
  else
    echo -e "${YELLOW}Agent venv or helper missing; run install first.${RESET}"
  fi
}

# ---- Server deployment ----
copy_server_payload() {
  local venvFolder="Server"
  local dataSource="Data/Server"
  local dataDestination="${venvFolder}/Borealis"
  python3 -m venv "$venvFolder" 2>/dev/null || true
  mkdir -p "$dataDestination"
  rm -rf "${dataDestination:?}"/*
  cp -r "${dataSource}/API" "$dataDestination/"
  cp -r "${dataSource}/Sounds"               "$dataDestination/"
  [ -f "${dataSource}/server.py" ] && cp "${dataSource}/server.py" "$dataDestination/"
  [ -f "${dataSource}/job_scheduler.py" ] && cp "${dataSource}/job_scheduler.py" "$dataDestination/"
  # Python deps
  if [[ -f "${dataSource}/server-requirements.txt" ]]; then
    "${SCRIPT_DIR}/Server/bin/python3" -m pip install --disable-pip-version-check -q -r "${dataSource}/server-requirements.txt"
  fi
}

prepare_webui() {
  local customUIPath="Data/Server/WebUI"
  local webUIDestination="Server/web-interface"
  mkdir -p "$webUIDestination"
  rm -rf "${webUIDestination}/public" "${webUIDestination}/src" "${webUIDestination}/build" 2>/dev/null || true
  cp -r "${customUIPath}/"* "$webUIDestination/"
  ensure_node_bins
  ( cd "$webUIDestination" && "$NPM_BIN" install --silent --no-fund --audit=false >/dev/null )
}

vite_start() {
  local mode="$1" # developer|production
  local webUIDestination="Server/web-interface"
  local subcmd="build"; [[ "$mode" == "developer" ]] && subcmd="dev"
  ensure_node_bins
  ( cd "$webUIDestination" && nohup "$NPM_BIN" run "$subcmd" >/dev/null 2>&1 & )
}

flask_run() {
  local py="${SCRIPT_DIR}/Server/bin/python3"
  local server_py="${SCRIPT_DIR}/Server/Borealis/server.py"
  echo -e "\n${GREEN}Launching Borealis Flask Server...${RESET}"
  echo "===================================================================================="
  "$py" "$server_py"
}

# ---- Menus ----
server_menu() {
  echo -e "\nConfigure Borealis Server Mode:"
  echo -e " 1) Build & Launch > Production Flask Server @ http://localhost:5000"
  echo -e " 2) [Skip Build] & Immediately Launch > Production Flask Server @ http://localhost:5000"
  echo -e " 3) Launch > [Hotload-Ready] Vite Dev Server @ http://localhost:5173"
  read -r -p "Enter choice [1/2/3]: " modeChoice

  case "$modeChoice" in
    1) borealis_operation_mode="production" ;;
    2) borealis_operation_mode="production" ;;
    3) borealis_operation_mode="developer" ;;
    *) echo -e "${RED}Invalid mode choice${RESET}"; return 1 ;;
  esac

  # Common deps
  echo -e "${GREEN}Ensuring Server Dependencies...${RESET}"
  install_shared_dependencies
  install_server_dependencies
  export PATH="${NODE_DIR}/bin:${PATH}"

  if [[ "$modeChoice" == "2" ]]; then
    flask_run
    return 0
  fi

  run_step "Create Server venv & deploy files" copy_server_payload
  run_step "Copy WebUI assets" prepare_webui
  run_step "Start Vite (${borealis_operation_mode})" vite_start "$borealis_operation_mode"
  flask_run
}

agent_menu() {
  echo -e "Agent Menu:"
  echo -e " 1) Install/Update Agent"
  echo -e " 2) Repair Borealis Agent"
  echo -e " 3) Remove Agent"
  echo -e " 4) Launch UserSession Helper (current session)"
  echo -e " 5) Back"
  read -r -p "Select an option: " agentChoice
  case "$agentChoice" in
    1) install_or_update_agent ;;
    2) repair_agent ;;
    3) remove_agent ;;
    4) launch_user_helper_now ;;
    5) return 0 ;;
    *) echo -e "${RED}Invalid selection${RESET}" ;;
  esac
}

electron_menu() {
  echo -e "Deploying Borealis Desktop App..."
  echo "===================================================================================="
  local electronSource="Data/Electron"
  local electronDestination="ElectronApp"

  run_step "Prepare ElectronApp folder" bash -c "
    rm -rf '${electronDestination}' && mkdir -p '${electronDestination}'
    [ -d 'Server/Borealis' ] || { echo 'Server/Borealis not found - please run Server build first.' >&2; exit 1; }
    cp -r 'Server/Borealis' '${electronDestination}/Server'
    cp '${electronSource}/package.json' '${electronDestination}'
    cp '${electronSource}/main.js' '${electronDestination}'
    [ -d 'Server/web-interface/build' ] || { echo 'WebUI build not found - run Server build first.' >&2; exit 1; }
    mkdir -p '${electronDestination}/renderer'
    cp -r 'Server/web-interface/build/'* '${electronDestination}/renderer/'
  "

  run_step "ElectronApp: Install Node dependencies" bash -c "
    export PATH='${NODE_DIR}/bin:'"'${PATH}'" && cd '${electronDestination}' && (command -v npm >/dev/null 2>&1 || exit 1) && npm install --silent --no-fund --audit=false
  "

  run_step "ElectronApp: Package with electron-builder" bash -c "
    export PATH='${NODE_DIR}/bin:'"'${PATH}'" && cd '${electronDestination}' && npm run dist
  "

  run_step "ElectronApp: Launch in dev mode" bash -c "
    export PATH='${NODE_DIR}/bin:'"'${PATH}'" && cd '${electronDestination}' && npm run dev
  "
}

update_borealis() {
  echo -e "\nUpdating Borealis..."
  local staging="${SCRIPT_DIR}/Update_Staging"
  local updateZip="${staging}/main.zip"
  local updateDir="${staging}/Borealis-main"
  local preservePath="${SCRIPT_DIR}/Data/Server/API/Tesseract-OCR"
  local preserveBackupPath="${staging}/Tesseract-OCR"
  mkdir -p "$staging"

  run_step "Updating: Move Tesseract-OCR to staging (if present)" bash -c "
    [ -d '${preservePath}' ] && { rm -rf '${preserveBackupPath}'; mkdir -p '${staging}'; mv '${preservePath}' '${preserveBackupPath}'; } || true
  "
  run_step "Updating: Clean folders before update" bash -c "
    rm -rf 'Data' 'Server/web-interface/src' 'Server/web-interface/build' 'Server/web-interface/public' 'Server/Borealis' || true
  "
  run_step "Updating: Download update" bash -c "
    curl -fsSL -o '${updateZip}' 'https://github.com/bunny-lab-io/Borealis/archive/refs/heads/main.zip'
  "
  run_step "Updating: Extract update" bash -c "
    unzip -oq '${updateZip}' -d '${staging}'
  "
  run_step "Updating: Copy update into repo" bash -c "
    cp -r '${updateDir}/'* '${SCRIPT_DIR}/'
  "
  run_step "Updating: Restore Tesseract-OCR" bash -c "
    [ -d '${preserveBackupPath}' ] && { mkdir -p 'Data/Server/API'; mv '${preserveBackupPath}' 'Data/Server/API/'; } || true
  "
  run_step "Updating: Clean staging" bash -c "rm -rf '${staging}'"
  echo -e "\n${GREEN}Update Complete! Re-launch Borealis.${RESET}"
}

# ---- Main entry ----

main_menu() {
  echo -e "\nPlease choose which function you want to launch:"
  echo -e " 1) Borealis Server"
  echo -e " 2) Borealis Agent"
  echo -e " 3) Build Electron App [Experimental]"
  echo -e " 4) Package Self-Contained App [Experimental]"
  echo -e " 5) Update Borealis [Requires Re-Build]"
  read -r -p "Enter a number: " choice
  case "$choice" in
    1) server_menu ;;
    2) agent_menu ;;
    3) electron_menu ;;
    4) echo -e "${YELLOW}Packaging to single-file EXE not supported on Linux yet.${RESET}" ;;
    5) update_borealis ;;
    *) echo -e "${RED}Invalid selection. Exiting...${RESET}"; exit 1 ;;
  esac
}

# Auto-select when flags provided
if [[ $SERVER_FLAG -eq 1 && $AGENT_FLAG -eq 1 ]]; then
  echo -e "${RED}Cannot use --server and --agent together.${RESET}"; exit 1
fi

if [[ $SERVER_FLAG -eq 1 ]]; then
  # Resolve server mode from flags
  if [[ $VITE_FLAG -eq 1 && $FLASK_FLAG -eq 1 ]]; then
    echo -e "${RED}Cannot combine --vite and --flask.${RESET}"; exit 1
  fi
  if [[ $VITE_FLAG -eq 1 ]]; then
    borealis_operation_mode="developer"; server_menu <<< $'3\n'
  elif [[ $FLASK_FLAG -eq 1 && $QUICK_FLAG -eq 1 ]]; then
    borealis_operation_mode="production"; server_menu <<< $'2\n'
  else
    borealis_operation_mode="production"; server_menu <<< $'1\n'
  fi
  exit $?
fi

if [[ $AGENT_FLAG -eq 1 ]]; then
  case "${AGENT_ACTION:-install}" in
    install) install_or_update_agent ;;
    repair)  repair_agent ;;
    remove)  remove_agent ;;
    launch)  launch_user_helper_now ;;
    *) install_or_update_agent ;;
  esac
  exit $?
fi

# Default to interactive menu
main_menu
