#!/usr/bin/env bash
# --------------------------------------------------------------------
# Deploying Borealis - Workflow Automation Tool
#
# This script deploys the Borealis Workflow Automation Tool by:
#   - Detecting the Linux distro and installing required system dependencies.
#   - Creating a Python virtual environment.
#   - Copying server data.
#   - Setting up a React UI application.
#   - Installing Python and Node dependencies.
#   - Building the React app.
#   - Launching the Flask server.
#
# Usage:
#   chmod +x deploy_borealis.sh
#   ./deploy_borealis.sh
# --------------------------------------------------------------------

# ---------------------- Initialization & Visuals ----------------------
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
RESET="\033[0m"
CHECKMARK="✅"
HOURGLASS="⏳"
CROSSMARK="❌"
INFO="ℹ️"

# Function to run a step with progress visuals and error checking
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

echo -e "${GREEN}Deploying Borealis - Workflow Automation Tool...${RESET}"
echo "===================================================================================="

# ---------------------- Detect Linux Distribution ----------------------
detect_distro() {
    # This function detects the Linux distribution by sourcing /etc/os-release.
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        DISTRO_ID=$ID
    else
        DISTRO_ID="unknown"
    fi
    echo -e "${INFO} Detected OS: ${DISTRO_ID}"
}
detect_distro

# ---------------------- Install System Dependencies ----------------------
install_core_dependencies() {
    # Install required packages based on detected Linux distribution.
    case "$DISTRO_ID" in
        ubuntu|debian)
            sudo apt update -qq
            sudo apt install -y python3 python3-venv python3-pip nodejs npm git curl
            ;;
        rhel|centos|fedora|rocky)
            # For Fedora and similar distributions, the venv module is built-in so we omit python3-venv.
            sudo dnf install -y python3 python3-pip nodejs npm git curl
            ;;
        arch)
            sudo pacman -Sy --noconfirm python python-venv python-pip nodejs npm git curl
            ;;
        *)
            echo -e "${RED}${CROSSMARK} Unsupported Linux distribution: ${DISTRO_ID}${RESET}"
            exit 1
            ;;
    esac
}
run_step "Install System Dependencies" install_core_dependencies

# ---------------------- Path Setup ----------------------
# Variables and path definitions
venvFolder="Borealis-Workflow-Automation-Tool"
dataSource="Data"
dataDestination="${venvFolder}/Borealis"
customUIPath="${dataSource}/WebUI"
webUIDestination="${venvFolder}/web-interface"

# ---------------------- Create Python Virtual Environment ----------------------
run_step "Create Virtual Python Environment" bash -c "
    # Check if virtual environment already exists; if not, create one.
    if [ ! -f '${venvFolder}/bin/activate' ]; then
        python3 -m venv '${venvFolder}'
    fi
"

# ---------------------- Copy Borealis Data ----------------------
run_step "Copy Borealis Server Data into Virtual Python Environment" bash -c "
    # If the Data folder exists, remove any existing server data folder and copy fresh data.
    if [ -d \"$dataSource\" ]; then
        rm -rf \"$dataDestination\"
        mkdir -p \"$dataDestination\"
        cp -r \"$dataSource/\"* \"$dataDestination\"
    else
        echo -e \"\r${INFO} Warning: Data folder not found, skipping copy.${RESET}\"
    fi
    true
"

# ---------------------- React UI Setup ----------------------
run_step "Create a new ReactJS App in ${webUIDestination}" bash -c "
    # Create a React app if the destination folder does not exist.
    if [ ! -d \"$webUIDestination\" ]; then
        # Set CI=true and add --loglevel=error to suppress funding and audit messages
        CI=true npx create-react-app \"$webUIDestination\" --silent --use-npm --loglevel=error
    fi
"

run_step "Overwrite React App with Custom Files" bash -c "
    # If custom UI files exist, copy them into the React app folder.
    if [ -d \"$customUIPath\" ]; then
        cp -r \"$customUIPath/\"* \"$webUIDestination\"
    else
        echo -e \"\r${INFO} No custom UI found, using default React app.${RESET}\"
    fi
    true
"

run_step "Remove Existing React Build (if any)" bash -c "
    # Remove the build folder if it exists to ensure a fresh build.
    if [ -d \"$webUIDestination/build\" ]; then
        rm -rf \"$webUIDestination/build\"
    fi
    true
"

# ---------------------- Activate Python Virtual Environment ----------------------
# Activate the Python virtual environment for subsequent commands.
source "${venvFolder}/bin/activate"

# ---------------------- Install Python Dependencies ----------------------
run_step "Install Python Dependencies into Virtual Python Environment" bash -c "
    # Install Python packages if a requirements.txt file is present.
    if [ -f \"requirements.txt\" ]; then
        pip install -q -r requirements.txt
    else
        echo -e \"\r${INFO} No requirements.txt found, skipping Python packages.${RESET}\"
    fi
    true
"

# ---------------------- Install Node Dependencies & Build React UI ----------------------
run_step "Install React App Dependencies" bash -c "
    # Install npm dependencies if package.json exists.
    if [ -f \"$webUIDestination/package.json\" ]; then
        cd \"$webUIDestination\"
        # Add --loglevel=error to suppress npm's funding and audit messages
        npm install --silent --no-fund --audit=false --loglevel=error
        cd -
    fi
"

run_step "Install React Flow and UI Libraries" bash -c "
    # Install additional React libraries.
    cd \"$webUIDestination\"
    npm install reactflow --silent --no-fund --audit=false --loglevel=error
    npm install --silent @mui/material @mui/icons-material @emotion/react @emotion/styled --no-fund --audit=false --loglevel=error
    cd -
"

run_step "Build React App" bash -c "
    # Build the React app to create production-ready files.
    cd \"$webUIDestination\"
    npm run build --silent --loglevel=error
    cd -
"

# ---------------------- Launch Flask Server ----------------------
cd "${venvFolder}"
echo -e "\n${GREEN}Launching Borealis...${RESET}"
echo "===================================================================================="
echo -ne "${HOURGLASS} Starting Flask server... "
python3 Borealis/server.py
echo -e "\r${CHECKMARK} Borealis Launched Successfully!"

