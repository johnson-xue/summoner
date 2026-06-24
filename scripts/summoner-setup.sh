#!/bin/bash
set -o pipefail

# summoner-setup.sh — One-command setup for Summoner in your project
# Usage: summoner-setup.sh [--quick|-q]
#
# This script combines init + memory DB setup into a single command.
# Run from your project directory.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"

# Terminal colors
BOLD='\033[1m'
GREEN='\033[32m'
YELLOW='\033[33m'
CYAN='\033[36m'
RED='\033[31m'
NC='\033[0m'

echo ""
printf "${CYAN}${BOLD}🔮 Summoner Setup Wizard${NC}\n"
echo "================================"
echo ""

# Check if already initialized
if [ -f "summoner.yaml" ]; then
    printf "${GREEN}✓${NC} summoner.yaml already exists\n"

    # Extract project name
    PROJECT_NAME=$(grep -A2 '^project:' summoner.yaml 2>/dev/null | grep 'name:' | head -1 | sed -E 's/.*name: *"?([^"]*)"?/\1/' | tr -d '"')

    if [ -z "$PROJECT_NAME" ]; then
        printf "${YELLOW}⚠️${NC}  Cannot extract project name from summoner.yaml\n"
        read -p "Enter project name: " PROJECT_NAME
        PROJECT_NAME="${PROJECT_NAME//[^a-zA-Z0-9_-]/}"
        if [ -z "$PROJECT_NAME" ]; then
            printf "${RED}❌${NC} Invalid project name\n"
            exit 1
        fi
    else
        printf "  Project: ${BOLD}$PROJECT_NAME${NC}\n"
    fi

    # Check Memory DB
    DB_FILE="$PLUGIN_ROOT/memory/${PROJECT_NAME}.db"
    if [ -f "$DB_FILE" ]; then
        # Check DB health
        if sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM patterns;" > /dev/null 2>&1; then
            PATTERN_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM patterns;")
            printf "${GREEN}✓${NC} Memory database healthy (${PATTERN_COUNT} patterns)\n"
            echo ""
            printf "${GREEN}${BOLD}✅ Setup complete!${NC}\n"
            echo ""
            echo "You can start using:"
            printf "  ${CYAN}/summoner:fix${NC} \"bug description\"\n"
            printf "  ${CYAN}/summoner:new${NC} \"feature request\"\n"
            printf "  ${CYAN}/summoner:debug${NC} \"error message\"\n"
            exit 0
        else
            printf "${YELLOW}⚠️${NC}  Memory database corrupted, reinitializing...\n"
            rm -f "$DB_FILE"
        fi
    else
        printf "${YELLOW}⚠️${NC}  Memory database not found, initializing...\n"
    fi

    # Initialize Memory DB
    echo ""
    printf "${CYAN}📝 Step 2/2: Initialize Memory database${NC}\n"
    echo ""
    if "$SCRIPT_DIR/init-memory-db.sh" "$PROJECT_NAME"; then
        printf "${GREEN}✓${NC} Memory database initialized\n"
        echo ""
        printf "${GREEN}${BOLD}✅ Setup complete!${NC}\n"
        echo ""
        printf "${YELLOW}📋 Next step:${NC}\n"
        printf "  ${BOLD}Restart Claude Code${NC} (required for hooks to activate)\n"
        echo ""
        echo "After restart, try:"
        printf "  ${CYAN}/summoner:fix${NC} \"your bug description\"\n"
        exit 0
    else
        printf "${RED}❌${NC} Failed to initialize Memory database\n"
        exit 1
    fi
fi

# First-time setup
printf "${CYAN}📝 Step 1/2: Create summoner.yaml${NC}\n"
echo ""

# Detect quick mode
QUICK_MODE=false
if [[ "$1" == "--quick" ]] || [[ "$1" == "-q" ]]; then
    QUICK_MODE=true
fi

if [ "$QUICK_MODE" = true ]; then
    echo "Using quick mode (all defaults)..."
    if ! "$SCRIPT_DIR/summoner-init.sh" 2; then
        printf "${RED}❌${NC} Failed to create summoner.yaml\n"
        exit 1
    fi
else
    # Interactive mode selection
    echo "Choose initialization mode:"
    printf "  ${BOLD}[1]${NC} Quick defaults ${GREEN}(recommended)${NC} - zero interaction\n"
    printf "  ${BOLD}[2]${NC} BP champion select - choose skill for each phase\n"
    printf "  ${BOLD}[3]${NC} Manual input - advanced users\n"
    echo ""
    read -p "Select [1-3, default=1]: " MODE
    MODE="${MODE:-1}"

    if ! "$SCRIPT_DIR/summoner-init.sh" "$MODE"; then
        printf "${RED}❌${NC} Failed to create summoner.yaml\n"
        exit 1
    fi
fi

# Check if summoner.yaml was created
if [ ! -f "summoner.yaml" ]; then
    printf "${RED}❌${NC} summoner.yaml not found after init\n"
    exit 1
fi

echo ""
printf "${GREEN}✓${NC} summoner.yaml created\n"
echo ""

# Step 2: Extract project name and init Memory DB
printf "${CYAN}📝 Step 2/2: Initialize Memory database${NC}\n"
echo ""

PROJECT_NAME=$(grep -A2 '^project:' summoner.yaml 2>/dev/null | grep 'name:' | head -1 | sed -E 's/.*name: *"?([^"]*)"?/\1/' | tr -d '"')

if [ -z "$PROJECT_NAME" ]; then
    printf "${YELLOW}⚠️${NC}  Cannot extract project name from summoner.yaml\n"
    echo ""
    echo "Expected format in summoner.yaml:"
    echo "  project:"
    echo "    name: \"your-project-name\""
    echo ""
    read -p "Enter project name: " PROJECT_NAME
    PROJECT_NAME="${PROJECT_NAME//[^a-zA-Z0-9_-]/}"
    if [ -z "$PROJECT_NAME" ]; then
        printf "${RED}❌${NC} Invalid project name\n"
        exit 1
    fi
fi

printf "  Project: ${BOLD}$PROJECT_NAME${NC}\n"

if "$SCRIPT_DIR/init-memory-db.sh" "$PROJECT_NAME"; then
    echo ""
    printf "${GREEN}✓${NC} Memory database initialized\n"
else
    printf "${RED}❌${NC} Failed to initialize Memory database\n"
    exit 1
fi

# Final instructions
echo ""
printf "${CYAN}📝 Step 3/3: Activate hooks${NC}\n"
echo ""
printf "${GREEN}${BOLD}✅ Setup complete!${NC}\n"
echo ""
printf "${YELLOW}📋 Next steps:${NC}\n"
printf "  1. ${BOLD}Restart Claude Code${NC} (required for hooks to activate)\n"
echo "  2. Return to this project"
echo "  3. Try your first workflow:"
printf "     • Bug fix:     ${CYAN}/summoner:fix${NC} \"describe the bug\"\n"
printf "     • New feature: ${CYAN}/summoner:new${NC} \"feature description\"\n"
printf "     • Diagnosis:   ${CYAN}/summoner:debug${NC} \"error message\"\n"
echo ""
printf "${CYAN}📚 Documentation:${NC} $PLUGIN_ROOT/docs/\n"
echo ""

exit 0
