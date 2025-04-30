#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Launch-Borealis.sh

#!/usr/bin/env bash

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
    echo -e "${GREEN}Deploying Borealis - Server Dashboard...${RESET}"
    echo "===================================================================================="

    detect_distro
    run_step "Install System Dependencies" install_core_dependencies

    venvFolder="Server"
    dataSource="Data/Server"
    dataDestination="${venvFolder}/Borealis"
    customUIPath="${dataSource}/WebUI"
    webUIDestination="${venvFolder}/web-interface"
    venvPython="${venvFolder}/bin/python3"

    run_step "Create Virtual Python Environment" bash -c "
        if [ ! -f '${venvFolder}/bin/activate' ]; then
            python3 -m venv '${venvFolder}'
        fi
    "

    run_step "Copy Python Server Components" bash -c "
        rm -rf '${dataDestination}' && mkdir -p '${dataDestination}'
        cp -r '${dataSource}/Python_API_Endpoints' '${dataDestination}/'
        cp -r '${dataSource}/Sounds' '${dataDestination}/'
        cp -r '${dataSource}/Workflows' '${dataDestination}/'
        cp '${dataSource}/server.py' '${dataDestination}/'
    "

    run_step "Create ReactJS App if Missing" bash -c "
        if [ ! -d '${webUIDestination}' ]; then
            npx create-react-app '${webUIDestination}' --use-npm --silent
        fi
    "

    run_step "Overwrite WebUI with Custom Files" bash -c "
        if [ -d '${customUIPath}' ]; then
            cp -r '${customUIPath}/'* '${webUIDestination}/'
        fi
    "

    run_step "Clean Old React Builds" bash -c "rm -rf '${webUIDestination}/build'"

    source "${venvFolder}/bin/activate"

    run_step "Install Python Dependencies" bash -c "
        pip install --disable-pip-version-check -q -r '${dataSource}/server-requirements.txt'
    "

    run_step "Install React Dependencies" bash -c "
        cd '${webUIDestination}'
        npm install --silent --no-fund --audit=false
        cd -
    "

    run_step "Build React App" bash -c "
        cd '${webUIDestination}'
        npm run build --silent
        cd -
    "

    echo -e "\n${GREEN}Launching Borealis Flask Server...${RESET}"
    echo "===================================================================================="
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
    agentDestinationFile="${agentDestinationFolder}/borealis-agent.py"

    run_step "Create Virtual Python Environment for Agent" bash -c "
        if [ ! -f '${venvFolder}/bin/activate' ]; then
            python3 -m venv '${venvFolder}'
        fi

        rm -rf '${agentDestinationFolder}'
        mkdir -p '${agentDestinationFolder}'
        cp '${agentSourcePath}' '${agentDestinationFile}'
    "

    source "${venvFolder}/bin/activate"

    run_step "Install Python Dependencies for Agent" bash -c "
        pip install --disable-pip-version-check -q -r '${agentRequirements}'
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
