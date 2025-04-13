#!/usr/bin/env bash
# --------------------------------------------------------------------
# Deploy Borealis - Workflow Automation Tool
#
# Menu-driven launcher for:
#   [1] Borealis Web Dashboard (Server)
#   [2] Borealis Client Agent (Agent)
# --------------------------------------------------------------------

GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
RESET="\033[0m"
CHECKMARK="✅"
HOURGLASS="⏳"
CROSSMARK="❌"
INFO="ℹ️"

run_step() {
    local message="$1"
    shift
    echo -ne "${HOURGLASS} ${message}... "
    if "$@"; then
        echo -e "\r${CHECKMARK} ${message}"
    else
        echo -e "\r${CROSSMARK} ${message} - Failed${RESET}"
        exit 1
    fi
}

detect_distro() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        DISTRO_ID=$ID
    else
        DISTRO_ID="unknown"
    fi
    echo -e "${INFO} Detected OS: ${DISTRO_ID}"
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
    echo -e "${GREEN}Deploying Borealis - Workflow Automation Tool...${RESET}"
    echo "===================================================================================="

    detect_distro
    run_step "Install System Dependencies" install_core_dependencies

    venvFolder="Borealis-Workflow-Automation-Tool"
    dataSource="Data"
    dataDestination="${venvFolder}/Borealis"
    customUIPath="${dataSource}/WebUI"
    webUIDestination="${venvFolder}/web-interface"

    run_step "Create Virtual Python Environment" bash -c "
        if [ ! -f '${venvFolder}/bin/activate' ]; then
            python3 -m venv '${venvFolder}'
        fi
    "

    run_step "Copy Borealis Server Data into Virtual Python Environment" bash -c "
        if [ -d \"$dataSource\" ]; then
            rm -rf \"$dataDestination\"
            mkdir -p \"$dataDestination\"
            cp -r \"$dataSource/\"* \"$dataDestination\"
        else
            echo -e \"\r${INFO} Warning: Data folder not found, skipping copy.${RESET}\"
        fi
        true
    "

    run_step "Create a new ReactJS App in ${webUIDestination}" bash -c "
        if [ ! -d \"$webUIDestination\" ]; then
            CI=true npx create-react-app \"$webUIDestination\" --silent --use-npm --loglevel=error
        fi
    "

    run_step "Overwrite React App with Custom Files" bash -c "
        if [ -d \"$customUIPath\" ]; then
            cp -r \"$customUIPath/\"* \"$webUIDestination\"
        else
            echo -e \"\r${INFO} No custom UI found, using default React app.${RESET}\"
        fi
        true
    "

    run_step "Remove Existing React Build (if any)" bash -c "
        if [ -d \"$webUIDestination/build\" ]; then
            rm -rf \"$webUIDestination/build\"
        fi
        true
    "

    source "${venvFolder}/bin/activate"

    run_step "Install Python Dependencies into Virtual Python Environment" bash -c "
        if [ -f \"requirements.txt\" ]; then
            pip install -q -r requirements.txt
        else
            echo -e \"\r${INFO} No requirements.txt found, skipping Python packages.${RESET}\"
        fi
        true
    "

    run_step "Install React App Dependencies" bash -c "
        if [ -f \"$webUIDestination/package.json\" ]; then
            cd \"$webUIDestination\"
            npm install --silent --no-fund --audit=false --loglevel=error
            cd -
        fi
    "

    run_step "Install React Flow and UI Libraries" bash -c "
        cd \"$webUIDestination\"
        npm install reactflow --silent --no-fund --audit=false --loglevel=error
        npm install --silent @mui/material @mui/icons-material @emotion/react @emotion/styled --no-fund --audit=false --loglevel=error
        cd -
    "

    run_step "Build React App" bash -c "
        cd \"$webUIDestination\"
        npm run build --silent --loglevel=error
        cd -
    "

    cd "${venvFolder}"
    echo -e "\n${GREEN}Launching Borealis...${RESET}"
    echo "===================================================================================="
    echo -ne "${HOURGLASS} Starting Flask server... "
    python3 Borealis/server.py
    echo -e "\r${CHECKMARK} Borealis Launched Successfully!"
}

launch_agent() {
    echo -e "${GREEN}Deploying Borealis Agent...${RESET}"
    echo "===================================================================================="

    detect_distro
    run_step "Install System Dependencies" install_core_dependencies

    venvFolder="Agent"
    agentSourcePath="Data/Agent/borealis-agent.py"
    agentRequirements="Data/Agent/requirements.txt"
    agentDestinationFolder="${venvFolder}/Agent"
    agentDestinationFile="${agentDestinationFolder}/borealis-agent.py"

    run_step "Create Virtual Python Environment for Agent" bash -c "
        if [ ! -f '${venvFolder}/bin/activate' ]; then
            python3 -m venv '${venvFolder}'
        fi

        if [ -f '${agentSourcePath}' ]; then
            rm -rf '${agentDestinationFolder}'
            mkdir -p '${agentDestinationFolder}'
            cp '${agentSourcePath}' '${agentDestinationFile}'
        else
            echo -e '\r${INFO} Warning: Agent script not found at ${agentSourcePath}, skipping copy.${RESET}'
        fi
        true
    "

    source "${venvFolder}/bin/activate"

    run_step "Install Python Dependencies for Agent" bash -c "
        if [ -f '${agentRequirements}' ]; then
            pip install -q -r '${agentRequirements}'
        else
            echo -e '\r${INFO} Agent-specific requirements.txt not found at ${agentRequirements}, skipping Python packages.${RESET}'
        fi
        true
    "

    echo -e "\n${GREEN}Launching Borealis Agent...${RESET}"
    echo "===================================================================================="
    python3 "${agentDestinationFile}"
}

echo -e "${GREEN}Deploying Borealis - Workflow Automation Tool...${RESET}"
echo "===================================================================================="
echo "Please choose which module you want to launch / (re)deploy:"
echo "- Server (Web Dashboard) [1]"
echo "- Agent (Local/Remote Client) [2]"

read -p "Enter 1 or 2: " choice

case "$choice" in
    1) launch_server ;;
    2) launch_agent ;;
    *) echo -e "${YELLOW}Invalid selection. Exiting...${RESET}"; exit 1 ;;
esac
