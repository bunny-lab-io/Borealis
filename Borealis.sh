#!/usr/bin/env bash
#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh

clear

# ---- ASCII ART BANNER ----
BOREALIS_BLUE="\033[38;5;39m"
DARK_GRAY="\033[1;30m"
RESET="\033[0m"

echo -e "${BOREALIS_BLUE}"
cat << "EOF"
███████████                                        ████   ███         
░░███░░░░░███                                      ░░███  ░░░          
 ░███    ░███  ██████  ████████   ██████   ██████   ░███  ████   █████ 
 ░██████████  ███░░███░░███░░███ ███░░███ ░░░░░███  ░███ ░░███  ███░░  
 ░███░░░░░███░███ ░███ ░███ ░░░ ░███████   ███████  ░███  ░███ ░░█████ 
 ░███    ░███░███ ░███ ░███     ░███░░░   ███░░███  ░███  ░███  ░░░░███
 ███████████ ░░██████  █████    ░░██████ ░░████████ █████ █████ ██████ 
░░░░░░░░░░░   ░░░░░░  ░░░░░      ░░░░░░   ░░░░░░░░ ░░░░░ ░░░░░ ░░░░░░  
EOF
echo -e "${RESET}"

echo -e "${DARK_GRAY}Drag-&-Drop Automation Orchestration | Macros | Data Collection & Analysis${RESET}"

# ---- END ASCII ART BANNER ----

# Color codes
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
RESET="\033[0m"

# ASCII-only icons
CHECKMARK="[OK]"
HOURGLASS="[WAIT]"
CROSSMARK="[X]"
INFO="[i]"

run_step() {
    local message="$1"
    shift
    echo -e "${GREEN}${HOURGLASS} ${message}...${RESET}"
    if "$@"; then
        echo -e "${GREEN}${CHECKMARK} ${message} completed.${RESET}"
    else
        echo -e "${RED}${CROSSMARK} ${message} failed!${RESET}"
        exit 1
    fi
}

detect_distro() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        DISTRO_ID="$ID"
    else
        DISTRO_ID="unknown"
    fi
}

install_core_dependencies() {
    case "$DISTRO_ID" in
        ubuntu|debian)
            sudo apt update -qq
            sudo apt install -y python3 python3-venv python3-pip nodejs npm git curl tesseract-ocr
            ;;
        rhel|centos|fedora|rocky)
            sudo dnf install -y python3 python3-pip nodejs npm git curl tesseract
            ;;
        arch)
            sudo pacman -Sy --noconfirm python python-venv python-pip nodejs npm git curl tesseract
            ;;
        *)
            echo -e "${RED}${CROSSMARK} Unsupported Linux distribution: ${DISTRO_ID}${RESET}"
            exit 1
            ;;
    esac
}

launch_server() {
    echo -e "${GREEN}Deploying Borealis - Server Dashboard...${RESET}"
    echo "===================================================================================="

    detect_distro
    run_step "Install System Dependencies" install_core_dependencies

    # Paths
    venvFolder="Server"
    dataSource="Data/Server"
    dataDestination="${venvFolder}/Borealis"
    customUIPath="${dataSource}/WebUI"
    webUIDestination="${venvFolder}/web-interface"
    venvPython="${venvFolder}/bin/python3"

    # Create Python venv
    run_step "Create Virtual Python Environment" bash -c "
        if [ ! -f '${venvFolder}/bin/activate' ]; then
            python3 -m venv '${venvFolder}'
        fi
    "

    # Install Python requirements
    run_step "Install Python Server Dependencies" bash -c "
        source '${venvFolder}/bin/activate'
        pip install --upgrade pip > /dev/null
        pip install --no-input -r '${dataSource}/server-requirements.txt'
    "

    # Copy Python server components
    run_step "Copy Python Server Components" bash -c "
        rm -rf '${dataDestination}' && mkdir -p '${dataDestination}'
        cp -r '${dataSource}/Python_API_Endpoints' '${dataDestination}/'
        cp -r '${dataSource}/Sounds' '${dataDestination}/'
        cp -r '${dataSource}/Workflows' '${dataDestination}/'
        cp     '${dataSource}/server.py' '${dataDestination}/'
    "

    # Setup Vite WebUI assets
    run_step "Setup Vite WebUI assets" bash -c "
        rm -rf '${webUIDestination}' && mkdir -p '${webUIDestination}'
        cp -r '${customUIPath}/'* '${webUIDestination}/'
    "

    # Install NPM packages for Vite
    run_step "Install Vite Web Frontend NPM Packages" bash -c "
        cd '${webUIDestination}'
        npm install --silent --no-fund --audit=false
        cd - > /dev/null
    "

    # Launch Vite Web Frontend in Dev Mode
    run_step "Start Vite Web Frontend (Dev Mode)" bash -c "
        cd '${webUIDestination}'
        npm run dev --silent
        cd - > /dev/null
    "

    # Launch Flask server
    echo -e "\n${GREEN}Launching Borealis Flask Server...${RESET}"
    echo "===================================================================================="
    source '${venvFolder}/bin/activate'
    python3 "${dataDestination}/server.py"
}

launch_agent() {
    echo -e "${GREEN}Deploying Borealis Agent...${RESET}"
    echo "===================================================================================="

    detect_distro
    run_step "Install System Dependencies" install_core_dependencies

    venvFolder="Agent"
    agentSourcePath="Data/Agent/borealis-agent.py"
    agentRequirements="Data/Agent/agent-requirements.txt"
    agentDestinationFolder="${venvFolder}/Borealis"

    run_step "Create Virtual Python Environment for Agent" bash -c "
        if [ ! -f '${venvFolder}/bin/activate' ]; then
            python3 -m venv '${venvFolder}'
        fi
    "

    run_step "Install Agent Dependencies" bash -c "
        source '${venvFolder}/bin/activate'
        pip install --upgrade pip > /dev/null
        pip install --no-input -r '${agentRequirements}'
    "

    run_step "Copy Agent Script" bash -c "
        mkdir -p '${agentDestinationFolder}'
        cp '${agentSourcePath}' '${agentDestinationFolder}/'
    "

    echo -e "\n${GREEN}Launching Borealis Agent...${RESET}"
    echo "===================================================================================="
    source '${venvFolder}/bin/activate'
    python3 "${agentDestinationFolder}/borealis-agent.py"
}

# Main menu

echo -e "${GREEN}Deploying Borealis - Workflow Automation Tool...${RESET}"
echo "===================================================================================="
echo "Please choose which module you want to launch / (re)deploy:"
echo "- Server (Web Dashboard) [1]"
echo "- Agent (Local/Remote Client) [2]"

read -p "Enter 1 or 2: " choice

case "${choice}" in
    1) launch_server ;;
    2) launch_agent ;;
    *) echo -e "${YELLOW}Invalid selection. Exiting...${RESET}"; exit 1 ;;
esac
