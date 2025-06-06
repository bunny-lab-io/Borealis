#!/usr/bin/env bash

#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh

# =================== ASCII Color Codes ===================
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
CYAN="\033[0;36m"
BLUE="\033[1;34m"
GRAY="\033[0;37m"
DARKGRAY="\033[1;30m"
RESET="\033[0m"
# =================== ASCII Icons Only ===================
CHECKMARK="[OK]"
HOURGLASS="[WAIT]"
CROSSMARK="[X]"
INFO="[i]"
# =================== ASCII Banner ===================
ascii_banner() {
cat <<EOF
============================================================
BBBBBBBBBB   OOOO   RRRRRR   EEEEEEE  AAAAAA  L     III  SSSS
BB     BB  O    O  RR   RR  EE      AA    AA L      I  SS   
BBBBBBBBB  O    O  RRRRRR   EEEE    AAAAAAAA L      I   SSS 
BB     BB  O    O  RR  RR   EE      AA    AA L      I     SS
BBBBBBBBB   OOOO   RR   RR  EEEEEEE AA    AA LLLLL III SSSS 
============================================================
EOF
echo -e "${DARKGRAY}Drag-&-Drop Automation Orchestration | Macros | Data Collection & Analysis${RESET}"
}
# =================== Utility/Progress Functions ===================
run_step() {
    local message="\$1"; shift
    echo -ne "\${CYAN}\${HOURGLASS} \$message...\${RESET}"
    if "\$@"; then
        echo -e "\r\${GREEN}\${CHECKMARK} \$message\${RESET}                   "
    else
        echo -e "\r\${RED}\${CROSSMARK} \$message failed!\${RESET}             "
        exit 1
    fi
}
pause_prompt() {
    echo -ne "\nPress ENTER to continue..."; read _
}
detect_distro() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        DISTRO_ID="\$ID"
    else
        DISTRO_ID="unknown"
    fi
}
# =================== Dependency Installation ===================
install_core_dependencies() {
    detect_distro
    case "\$DISTRO_ID" in
        ubuntu|debian)
            sudo apt update -qq
            sudo apt install -y python3 python3-venv python3-pip nodejs npm git curl tesseract-ocr p7zip-full
            ;;
        rhel|centos|fedora|rocky)
            sudo dnf install -y python3 python3-pip python3-virtualenv nodejs npm git curl tesseract p7zip
            ;;
        arch)
            sudo pacman -Sy --noconfirm python python-venv python-pip nodejs npm git curl tesseract p7zip
            ;;
        *)
            echo -e "\${RED}\${CROSSMARK} Unsupported Linux distribution: \${DISTRO_ID}\${RESET}"
            exit 1
            ;;
    esac
}

ensure_tesseract_data() {
    # Make sure tesseract English and OSD traineddata are present (Windows script always downloads these)
    local tessdata_dir="Data/Server/Python_API_Endpoints/Tesseract-OCR/tessdata"
    mkdir -p "\$tessdata_dir"
    [ ! -f "\$tessdata_dir/eng.traineddata" ] && \
        curl -L -o "\$tessdata_dir/eng.traineddata" "https://github.com/tesseract-ocr/tessdata/raw/main/eng.traineddata"
    [ ! -f "\$tessdata_dir/osd.traineddata" ] && \
        curl -L -o "\$tessdata_dir/osd.traineddata" "https://github.com/tesseract-ocr/tessdata/raw/main/osd.traineddata"
}

check_version_cmd() {
    # $1 = cmd, $2 = min version, e.g. check_version_cmd node 18
    if ! command -v "$1" >/dev/null; then
        return 1
    fi
    local ver=\$("$1" --version 2>/dev/null | grep -Eo '[0-9.]+' | head -n1 | cut -d. -f1)
    [ "\$ver" -ge "\$2" ] 2>/dev/null
}
# =================== Menu Functions ===================
menu_server() {
    clear
    ascii_banner
    echo ""
    echo -e "${CYAN}Configure Borealis Server Mode:${RESET}"
    echo -e " 1) Build & Launch > ${DARKGRAY}Production Flask Server${RESET} ${CYAN}@ http://localhost:5000${RESET}"
    echo -e " 2) [Skip Build] & Immediately Launch > ${DARKGRAY}Production Flask Server${RESET} ${CYAN}@ http://localhost:5000${RESET}"
    echo -e " 3) Launch > ${DARKGRAY}[Hotload-Ready]${RESET} ${GREEN}Vite Dev Server${RESET} ${CYAN}@ http://localhost:5173${RESET}"
    echo -ne "${CYAN}Enter choice [1/2/3]: ${RESET}"
    read mode_choice

    borealis_operation_mode="production"
    if [ "\$mode_choice" = "3" ]; then
        borealis_operation_mode="developer"
    fi

    venvFolder="Server"
    dataSource="Data/Server"
    dataDestination="\${venvFolder}/Borealis"
    customUIPath="\${dataSource}/WebUI"
    webUIDestination="\${venvFolder}/web-interface"
    venvPython="\${venvFolder}/bin/python3"

    # ---- Build mode 1: always build ----
    if [ "\$mode_choice" = "1" ] || [ "\$mode_choice" = "3" ]; then
        run_step "Install System Dependencies" install_core_dependencies
        run_step "Ensure Tesseract Traineddata" ensure_tesseract_data
        run_step "Create Python Virtual Environment" bash -c "if [ ! -f '\${venvFolder}/bin/activate' ]; then python3 -m venv '\${venvFolder}'; fi"
        run_step "Install Python Server Dependencies" bash -c "source '\${venvFolder}/bin/activate'; pip install --upgrade pip > /dev/null; pip install --no-input -r '\${dataSource}/server-requirements.txt'"
        run_step "Copy Python Server Components" bash -c "rm -rf '\${dataDestination}' && mkdir -p '\${dataDestination}'; cp -r '\${dataSource}/Python_API_Endpoints' '\${dataDestination}/'; cp -r '\${dataSource}/Sounds' '\${dataDestination}/'; cp -r '\${dataSource}/Workflows' '\${dataDestination}/'; cp '\${dataSource}/server.py' '\${dataDestination}/'"
        run_step "Setup Vite WebUI assets" bash -c "rm -rf '\${webUIDestination}' && mkdir -p '\${webUIDestination}'; cp -r '\${customUIPath}/'* '\${webUIDestination}/'"
        run_step "Install Vite Web Frontend NPM Packages" bash -c "cd '\${webUIDestination}'; npm install --silent --no-fund --audit=false; cd - > /dev/null"
    fi
    # ---- Build mode 2: skip build ----
    if [ "\$mode_choice" = "2" ]; then
        # No build steps, just activate venv and launch.
        run_step "Install System Dependencies" install_core_dependencies
        run_step "Ensure Tesseract Traineddata" ensure_tesseract_data
        [ ! -f '\${venvFolder}/bin/activate' ] && { echo -e "\${RED}\${CROSSMARK} Server venv not found! Run option 1 first.\${RESET}"; exit 1; }
    fi

    # ---- Start Vite frontend in proper mode ----
    if [ "\$borealis_operation_mode" = "developer" ]; then
        run_step "Start Vite Web Frontend (Dev Mode)" bash -c "cd '\${webUIDestination}'; npm run dev --silent; cd - > /dev/null &"
    else
        run_step "Vite Web Frontend: Build for Production" bash -c "cd '\${webUIDestination}'; npm run build --silent; cd - > /dev/null"
    fi

    # ---- Start Flask server ----
    echo -e "\n${GREEN}Launching Borealis Flask Server...${RESET}"
    echo "===================================================================================="
    source "\${venvFolder}/bin/activate"
    python3 "\${dataDestination}/server.py"
}

menu_agent() {
    clear
    ascii_banner
    echo ""
    echo -e "${CYAN}Deploying Borealis Agent...${RESET}"
    echo "===================================================================================="
    run_step "Install System Dependencies" install_core_dependencies

    venvFolder="Agent"
    agentSourcePath="Data/Agent/borealis-agent.py"
    agentRequirements="Data/Agent/agent-requirements.txt"
    agentDestinationFolder="\${venvFolder}/Borealis"

    run_step "Create Python Virtual Environment for Agent" bash -c "if [ ! -f '\${venvFolder}/bin/activate' ]; then python3 -m venv '\${venvFolder}'; fi"
    run_step "Install Agent Dependencies" bash -c "source '\${venvFolder}/bin/activate'; pip install --upgrade pip > /dev/null; pip install --no-input -r '\${agentRequirements}'"
    run_step "Copy Agent Script" bash -c "mkdir -p '\${agentDestinationFolder}'; cp '\${agentSourcePath}' '\${agentDestinationFolder}/'"
    echo -e "\n${GREEN}Launching Borealis Agent...${RESET}"
    echo "===================================================================================="
    source "\${venvFolder}/bin/activate"
    python3 "\${agentDestinationFolder}/borealis-agent.py"
}

menu_electron() {
    clear
    ascii_banner
    echo ""
    echo -e "${CYAN}Deploying Borealis Electron Desktop App...${RESET}"
    echo "===================================================================================="
    electronSource="Data/Electron"
    electronDestination="ElectronApp"
    venvFolder="Server"
    webUIDestination="\${venvFolder}/web-interface"
    staticBuild="\${webUIDestination}/build"

    run_step "Install System Dependencies" install_core_dependencies
    run_step "Prepare ElectronApp folder" bash -c "rm -rf '\${electronDestination}'; mkdir -p '\${electronDestination}'; cp -r '\${venvFolder}/Borealis' '\${electronDestination}/Server'; cp '\${electronSource}/package.json' '\${electronDestination}/'; cp '\${electronSource}/main.js' '\${electronDestination}/'; cp -r '\${staticBuild}/'* '\${electronDestination}/renderer/'"
    run_step "Install Electron dependencies" bash -c "cd '\${electronDestination}'; npm install --silent --no-fund --audit=false; cd - > /dev/null"
    run_step "ElectronApp: Package with electron-builder" bash -c "cd '\${electronDestination}'; npm run dist; cd - > /dev/null"
    run_step "ElectronApp: Launch in dev mode" bash -c "cd '\${electronDestination}'; npm run dev; cd - > /dev/null"
}

menu_packager() {
    clear
    ascii_banner
    echo ""
    echo -e "${CYAN}Packager: Build Standalone EXE/Binary via PyInstaller${RESET}"
    echo "1) Package Borealis Server"
    echo "2) Package Borealis Agent"
    echo -ne "${CYAN}Enter choice [1/2]: ${RESET}"
    read exe_choice
    if [ "\$exe_choice" = "1" ]; then
        (cd Data/Server && bash Package-Borealis-Server.sh)
    elif [ "\$exe_choice" = "2" ]; then
        (cd Data/Agent && bash Package_Borealis-Agent.sh)
    else
        echo -e "\${RED}Invalid Choice. Exiting...\${RESET}"; exit 1
    fi
}

menu_update() {
    clear
    ascii_banner
    echo ""
    echo -e "${CYAN}Updating Borealis...${RESET}"
    scriptDir="\$(dirname "\$0")"
    updateZip="Update_Staging/main.zip"
    updateDir="Update_Staging/borealis"
    preservePath="Data/Server/Python_API_Endpoints/Tesseract-OCR"
    preserveBackupPath="Update_Staging/Tesseract-OCR"

    run_step "Updating: Move Tesseract-OCR Folder Somewhere Safe to Restore Later" bash -c "[ -d '\$preservePath' ] && mkdir -p Update_Staging && mv '\$preservePath' '\$preserveBackupPath'"
    run_step "Updating: Clean Up Folders to Prepare for Update" bash -c "rm -rf Data Server/web-interface/src Server/web-interface/build Server/web-interface/public Server/Borealis"
    run_step "Updating: Create Update Staging Folder" bash -c "mkdir -p Update_Staging"
    run_step "Updating: Download Update" curl -L -o "\$updateZip" "https://git.bunny-lab.io/bunny-lab/Borealis/archive/main.zip"
    run_step "Updating: Extract Update Files" bash -c "unzip -o '\$updateZip' -d Update_Staging"
    run_step "Updating: Copy Update Files into Production Borealis Root Folder" bash -c "cp -r '\$updateDir/'* ."
    run_step "Updating: Restore Tesseract-OCR Folder" bash -c "[ -d '\$preserveBackupPath' ] && mkdir -p Data/Server/Python_API_Endpoints && mv '\$preserveBackupPath' Data/Server/Python_API_Endpoints/"
    run_step "Updating: Clean Up Update Staging Folder" bash -c "rm -rf Update_Staging"
    echo -e "\n${GREEN}Update Complete! Please Re-Launch the Borealis Script.${RESET}"
    pause_prompt
    exec bash "\$0"
}

# =================== MAIN MENU ===================
clear
ascii_banner
echo "Please choose which function you want to launch:"
echo -e " 1) Borealis Server"
echo -e " 2) Borealis Agent"
echo -e " 3) Build Electron App ${DARKGRAY}[Experimental]${RESET}"
echo -e " 4) Package Self-Contained EXE of Server or Agent ${DARKGRAY}[Experimental]${RESET}"
echo -e " 5) Update Borealis ${DARKGRAY}[Requires Re-Build]${RESET}"
echo -e "Type a number and press ${CYAN}<ENTER>${RESET}"
read choice

case "\$choice" in
    1) menu_server ;;
    2) menu_agent ;;
    3) menu_electron ;;
    4) menu_packager ;;
    5) menu_update ;;
    *) echo -e "\${YELLOW}Invalid selection. Exiting...\${RESET}"; exit 1 ;;
esac

