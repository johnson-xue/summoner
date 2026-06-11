#!/bin/bash
# summoner-bp.sh — Interactive champion select for Summoner workflow skills
# Usage: summoner-bp.sh [--quick] [--force]
#
# --quick:  Accept all defaults, zero interaction (CI/automation friendly)
# --force:  Overwrite existing summoner.yaml without asking
#
# Inspired by League of Legends champion select:
#   Phase = Position, Skill = Champion, Default = Meta pick
#
# Compatible with bash 3.2+ (macOS default)

set -eu

# ============================================================
# Configuration
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT_FILE="summoner.yaml"

# Terminal colors (ANSI)
BOLD='\033[1m'
DIM='\033[2m'
GREEN='\033[32m'
BLUE='\033[34m'
PURPLE='\033[35m'
YELLOW='\033[33m'
CYAN='\033[36m'
RED='\033[31m'
NC='\033[0m'

# ============================================================
# Arg parsing
# ============================================================

QUICK_MODE=false
FORCE_MODE=false

for arg in "$@"; do
    case "$arg" in
        --quick|-q) QUICK_MODE=true ;;
        --force|-f) FORCE_MODE=true ;;
        --help|-h)
            echo "Usage: summoner-bp.sh [--quick] [--force]"
            echo ""
            echo "  --quick   Accept all recommended defaults (zero interaction)"
            echo "  --force   Overwrite existing summoner.yaml without confirmation"
            echo ""
            echo "Without flags, enters interactive BP (Ban/Pick) champion select."
            exit 0
            ;;
        *) echo "Unknown flag: $arg. Use --help."; exit 1 ;;
    esac
done

# ============================================================
# Pre-flight checks
# ============================================================

if [ -f "$OUTPUT_FILE" ] && [ "$FORCE_MODE" = false ]; then
    # Read existing manifest for diff preview
    existing_project=""
    existing_phase_count=0
    if [ -f "$OUTPUT_FILE" ]; then
        existing_project=$(grep -A2 '^project:' "$OUTPUT_FILE" 2>/dev/null | grep 'name:' | head -1 | sed -E 's/.*name: *"?([^"]*)"?/\1/')
        existing_phase_count=$(grep -c '^  [a-z][a-z_]*:' "$OUTPUT_FILE" 2>/dev/null || echo "0")
        cp "$OUTPUT_FILE" "${OUTPUT_FILE}.bak"
    fi

    echo ""
    printf "${YELLOW}WARNING: summoner.yaml already exists.${NC}\n"
    if [ -n "$existing_project" ]; then
        printf "  Project: ${GREEN}${existing_project}${NC} (${existing_phase_count} phases configured)\n"
    fi
    echo ""
    echo "  BP will REPLACE all phase-to-skill mappings with your new selections."
    echo "  Project-specific skills (like antia-*, my-*) will be lost."
    echo ""
    printf "  ${BOLD}[y]${NC} Replace all  ${BOLD}[m]${NC} Merge (keep existing phases, add missing)  ${BOLD}[N]${NC} Cancel\n"
    read -p "  Choice [y/m/N]: " confirm
    case "$confirm" in
        y|Y) MERGE_MODE=false ;;
        m|M) MERGE_MODE=true ;;
        *) echo "Cancelled."; exit 0 ;;
    esac
fi

# ============================================================
# Local skill discovery — scan project for existing skills
# ============================================================

# Discovered local skills: phase_name:skill_name pairs
LOCAL_SKILLS=""

discover_local_skills() {
    local dirs=".claude/skills skills"
    for dir in $dirs; do
        if [ -d "$dir" ]; then
            for skill_md in "$dir"/*/SKILL.md; do
                [ -f "$skill_md" ] || continue
                local skill_name=$(grep '^name:' "$skill_md" 2>/dev/null | head -1 | sed 's/name: *//')
                [ -n "$skill_name" ] || continue
                # Try to guess which phase this skill maps to
                local phase=""
                case "$skill_name" in
                    *debug*)   phase="debug" ;;
                    *config*)  phase="config" ;;
                    *test*|*tdd*) phase="test" ;;
                    *ops*|*deploy*) phase="ops" ;;
                    *subsystem*) phase="subsystem" ;;
                    *rpc*)     phase="rpc" ;;
                    *gmt*|*admin*) phase="gmt" ;;
                    *migrate*) phase="migrate" ;;
                    *docs*|*doc*) phase="docs" ;;
                    *worktree*) phase="worktree" ;;
                    *review*)  phase="review" ;;
                    *security*) phase="security" ;;
                    *define*|*brainstorm*) phase="define" ;;
                    *plan*)    phase="plan" ;;
                    *implement*|*build*) phase="implement" ;;
                esac
                if [ -n "$phase" ]; then
                    LOCAL_SKILLS="${LOCAL_SKILLS}${phase}:${skill_name}\n"
                fi
            done
        fi
    done
}

# Get local skill for a phase, if discovered
get_local_skill() {
    echo "$LOCAL_SKILLS" | grep "^${1}:" 2>/dev/null | head -1 | cut -d: -f2-
}

discover_local_skills

# ============================================================
# Champion Pool — matching roster.yaml
# ============================================================

# Phase display order in BP
PHASE_ORDER="define plan implement test review debug ops"

# get_phase_label <phase> → "PHASE_NAME (中文说明)"
get_phase_label() {
    case "$1" in
        define)    echo "DEFINE (需求定义)" ;;
        plan)      echo "PLAN (任务拆解)" ;;
        implement) echo "IMPLEMENT (功能实现)" ;;
        test)      echo "TEST (测试)" ;;
        review)    echo "REVIEW (代码审查)" ;;
        debug)     echo "DEBUG (调试诊断)" ;;
        ops)       echo "OPS (运维操作)" ;;
        subsystem) echo "SUBSYSTEM (新增模块)" ;;
        rpc)       echo "RPC (接口)" ;;
        gmt)       echo "GMT (管理工具)" ;;
        *)         echo "$1" ;;
    esac
}

# get_phase_desc <phase> → one-line description
get_phase_desc() {
    case "$1" in
        define)    echo "将模糊想法转化为清晰的设计规格" ;;
        plan)      echo "将设计规格分解为可执行的任务列表" ;;
        implement) echo "按计划执行编码 — /summoner:new 的 Phase 3" ;;
        test)      echo "编写和运行测试" ;;
        review)    echo "审查代码变更的正确性、风格、安全性和架构" ;;
        debug)     echo "排查根因 — /summoner:fix 的 Phase 1" ;;
        ops)       echo "部署、启动、停止、状态检查等运维操作" ;;
        *)         echo "" ;;
    esac
}

# get_default_skill <phase> → skill name
get_default_skill() {
    case "$1" in
        define)    echo "superpowers:brainstorming" ;;
        plan)      echo "superpowers:writing-plans" ;;
        implement) echo "superpowers:subagent-driven-development" ;;
        test)      echo "superpowers:test-driven-development" ;;
        review)    echo "superpowers:requesting-code-review" ;;
        debug)     echo "superpowers:systematic-debugging" ;;
        ops)       echo "superpowers:finishing-a-development-branch" ;;
        *)         echo "none" ;;
    esac
}

# get_champions <phase> → multi-line string, one per champion: name|source|desc|is_default
get_champions() {
    case "$1" in
        define)
            echo "superpowers:brainstorming|superpowers|Socratic 式设计精炼 — 模糊想法→清晰 spec|true"
            echo "ce-ideate|compound-eng|生成和评估 big-picture 方案，编码前批判性思考|false"
            echo "grill-me|mattpocock|无情追问设计中的每个决策分支直到全部明确|false"
            ;;
        plan)
            echo "superpowers:writing-plans|superpowers|分解为 2-5 分钟可执行任务，含文件路径和验证步骤|true"
            echo "planning-with-files|community|Manus 风格持久化 markdown 计划，含里程碑和清单|false"
            echo "ce-plan|compound-eng|将需求转化为详细实现计划和任务列表|false"
            ;;
        implement)
            echo "superpowers:subagent-driven-development|superpowers|每个任务派发独立 subagent，两阶段审查|true"
            echo "superpowers:executing-plans|superpowers|批量执行计划，任务间设人工检查点|false"
            echo "ce-work|compound-eng|使用 git worktree 执行计划，计划视为活文档|false"
            ;;
        test)
            echo "superpowers:test-driven-development|superpowers|严格 RED-GREEN-REFACTOR 循环|true"
            echo "tdd|mattpocock|红-绿-重构，一次一个垂直切片|false"
            ;;
        review)
            echo "superpowers:requesting-code-review|superpowers|审查前检查清单，按严重程度报告|true"
            echo "ce-code-review|compound-eng|12+ 并行审查 agent（安全/性能/架构/简洁性）|false"
            ;;
        debug)
            echo "superpowers:systematic-debugging|superpowers|4 阶段流程：调查→模式分析→假设实验→修复|true"
            echo "/diagnose|mattpocock|严格诊断循环：复现→最小化→假设→插桩→修复|false"
            echo "ce-debug|compound-eng|系统化 bug 复现、根因分析、修复|false"
            ;;
        ops)
            echo "superpowers:finishing-a-development-branch|superpowers|分支收尾：验证→展示选项→清理|true"
            echo "ce-compound|compound-eng|将经验、模式、坑记录为可复用知识文档|false"
            ;;
        *)
            echo "none|custom|(无可用 champion)|true"
            ;;
    esac
}

# get_source_display <source_key> → human-readable
get_source_display() {
    case "$1" in
        superpowers)   echo "obra/superpowers (174k ★)" ;;
        compound-eng)  echo "EveryInc/compound-engineering (19k ★)" ;;
        mattpocock)    echo "mattpocock/skills (40k ★)" ;;
        anthropic)     echo "anthropics/skills (37k ★)" ;;
        community)     echo "Community" ;;
        custom)        echo "自定义" ;;
        *)             echo "$1" ;;
    esac
}

# ============================================================
# Quick mode — generate with all defaults
# ============================================================

generate_manifest() {
    local project_name="$1"
    shift
    # Remaining args: phase=skill pairs
    # We build a simple key=value structure from args
    local def_skill="" plan_skill="" impl_skill="" test_skill=""
    local review_skill="" debug_skill="" ops_skill=""

    for pair in "$@"; do
        phase="${pair%%=*}"
        skill="${pair#*=}"
        case "$phase" in
            define)    def_skill="$skill" ;;
            plan)      plan_skill="$skill" ;;
            implement) impl_skill="$skill" ;;
            test)      test_skill="$skill" ;;
            review)    review_skill="$skill" ;;
            debug)     debug_skill="$skill" ;;
            ops)       ops_skill="$skill" ;;
        esac
    done

    cat > "$OUTPUT_FILE" <<YAML
# summoner.yaml — ${project_name} AI 能力声明
# 由 summoner-bp.sh 生成

version: "1"

project:
  name: "${project_name}"

phases:
  # 调试诊断
  debug:
    skill: ${debug_skill}

  # 配置检查
  config:
    skill: none

  # 测试
  test:
    skill: ${test_skill}

  # 运维
  ops:
    skill: ${ops_skill}

  # 需求定义
  define:
    skill: ${def_skill}

  # 任务拆解
  plan:
    skill: ${plan_skill}

  # 功能实现
  implement:
    skill: ${impl_skill}

  # 代码审查
  review:
    skill: ${review_skill}

  # 验证（复用测试 skill）
  verify:
    skill: ${test_skill}

  # 复现（复用测试 skill）
  reproduce:
    skill: ${test_skill}

  # 安全审计
  security:
    skill: none

  # 项目特有 phase — 设为 none，按需修改
  subsystem:
    skill: none

  rpc:
    skill: none

  gmt:
    skill: none

workflows:
  bugfix:
    chain: [debug, reproduce, fix, verify, review]
    checkpoints: after_each

  feature:
    chain: [define, plan, implement, test, review]
    checkpoints: after_each

  ship:
    fan_out:
      - persona: code-reviewer
      - persona: test-engineer
    merge: review
    checkpoints: after_merge
YAML
}

if [ "$QUICK_MODE" = true ]; then
    echo ""
    printf "${GREEN}Quick mode — using all recommended defaults${NC}\n\n"

    existing_name=$(grep -A2 '^project:' "$OUTPUT_FILE" 2>/dev/null | grep 'name:' | head -1 | sed -E 's/.*name: *"?([^"]*)"?/\1/')
    default_name="${existing_name:-my-project}"
    read -p "项目名称 (如 my-game-server) [${default_name}]: " PROJECT_NAME
    PROJECT_NAME="${PROJECT_NAME:-${default_name}}"

    generate_manifest "$PROJECT_NAME" \
        "define=$(get_default_skill define)" \
        "plan=$(get_default_skill plan)" \
        "implement=$(get_default_skill implement)" \
        "test=$(get_default_skill test)" \
        "review=$(get_default_skill review)" \
        "debug=$(get_default_skill debug)" \
        "ops=$(get_default_skill ops)"

    echo ""
    printf "${GREEN}[OK] summoner.yaml 已生成${NC}\n\n"
    echo "下一步:"
    echo "  ${PLUGIN_ROOT}/scripts/init-memory-db.sh ${PROJECT_NAME}"
    exit 0
fi

# ============================================================
# BP Interactive Mode — helper functions
# ============================================================

draw_box_top() {
    printf "${CYAN}┌─────────────────────────────────────────────┐${NC}\n"
}

draw_box_mid() {
    printf "${CYAN}│${NC} %-45s ${CYAN}│${NC}\n" "$1"
}

draw_box_bot() {
    printf "${CYAN}└─────────────────────────────────────────────┘${NC}\n"
}

draw_separator() {
    printf "${DIM}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
}

clear_screen() {
    printf "\033[2J\033[H"
}

# Store user selections in a simple file-based map under /tmp
SEL_DIR="$(mktemp -d /tmp/summoner-bp-XXXXXX)"
cleanup() { rm -rf "$SEL_DIR"; stty sane 2>/dev/null; }
trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM

set_selection() { echo "$2" > "${SEL_DIR}/$1"; }
get_selection() { cat "${SEL_DIR}/$1" 2>/dev/null || echo "__unset__"; }

# ============================================================
# BP Interactive Mode — main flow
# ============================================================

clear_screen
draw_box_top
draw_box_mid "Summoner 阵容选择"
draw_box_mid ""
draw_box_mid "为每个 Phase 选择\"英雄\"（skill）。"
draw_box_mid "推荐默认已高亮，选完锁定生成配置。"
draw_box_bot
echo ""

TOTAL=$(echo "$PHASE_ORDER" | wc -w | tr -d ' ')
CURRENT=0

for phase in $PHASE_ORDER; do
    CURRENT=$((CURRENT + 1))
    label="$(get_phase_label "$phase")"
    desc="$(get_phase_desc "$phase")"
    default_skill="$(get_default_skill "$phase")"

    echo ""
    draw_separator
    printf "${BOLD}━━━ Phase ${CURRENT}/${TOTAL}: ${label} ━━━${NC}\n"
    printf "${DIM}  ${desc}${NC}\n\n"

    # Display champions
    option_num=1
    default_num=1
    champ_names=""
    champ_sources=""
    champ_descs=""

    while IFS='|' read -r name source desc is_default; do
        [ -z "$name" ] && continue

        # Store champion data in dynamically-named variables
        printf -v "champ_name_${option_num}" "%s" "$name"
        printf -v "champ_source_${option_num}" "%s" "$source"

        icon="[ALT]"
        extra=""
        if [ "$is_default" = "true" ]; then
            icon="[DEFAULT]"
            extra="← 推荐"
            default_num=$option_num
        fi

        src_display="$(get_source_display "$source")"
        printf "  ${icon} ${BOLD}[${option_num}]${NC} ${GREEN}${name}${NC} ${extra}\n"
        printf "     ${DIM}${src_display}${NC}\n"
        printf "     ${desc}\n\n"

        option_num=$((option_num + 1))
    done <<CHAMPIONS
$(get_champions "$phase")
CHAMPIONS

    # Local project skill (if discovered) — made default, overrides roster default
    local_skill="$(get_local_skill "$phase")"
    if [ -n "$local_skill" ]; then
        local_num=$option_num
        printf -v "champ_name_${option_num}" "%s" "$local_skill"
        printf -v "champ_source_${option_num}" "custom"
        # Make local skill the default
        default_num=$option_num
        printf "  [LOCAL] ${BOLD}[${option_num}]${NC} ${PURPLE}${local_skill}${NC} ← 推荐 (项目已有)\n"
        printf "     ${DIM}在 ./skills/ 中发现的项目本地 skill${NC}\n\n"
        option_num=$((option_num + 1))
    else
        local_num=0
    fi

    # Custom option
    custom_num=$option_num
    printf "  [CUSTOM] ${BOLD}[${custom_num}]${NC} 输入自定义 skill 名\n"
    printf "     ${DIM}使用你自己或团队的专属 skill${NC}\n\n"

    # Skip option
    skip_num=$((option_num + 1))
    printf "  [SKIP]  ${BOLD}[${skip_num}]${NC} 跳过此 Phase ${DIM}(skill: none)${NC}\n\n"

    # Get choice
    while true; do
        read -p "  选择 [1-${skip_num}，默认=${default_num}]: " choice
        choice="${choice:-$default_num}"

        case "$choice" in
            ''|*[!0-9]*) ;;
            *)
                if [ "$choice" -ge 1 ] 2>/dev/null && [ "$choice" -le "$skip_num" ] 2>/dev/null; then
                    break
                fi
                ;;
        esac
        printf "  ${RED}无效选择，请输入 1-${skip_num}${NC}\n"
    done

    # Resolve
    if [ "$choice" -eq "$custom_num" ]; then
        read -p "  输入 skill 名: " custom_skill
        set_selection "$phase" "$custom_skill"
        printf "  ${PURPLE}[OK] 已选择自定义: ${custom_skill}${NC}\n"
    elif [ "$choice" -eq "$skip_num" ]; then
        set_selection "$phase" "none"
        printf "  ${DIM}[OK] 已跳过${NC}\n"
    else
        picked_var="champ_name_${choice}"
        picked="${!picked_var}"
        set_selection "$phase" "$picked"
        printf "  ${GREEN}[OK] 已选择: ${picked}${NC}\n"
    fi

    sleep 0.3
done

# ============================================================
# Review screen
# ============================================================

clear_screen
draw_box_top
draw_box_mid "[OK] 阵容已锁定"
draw_box_mid ""

for phase in $PHASE_ORDER; do
    skill="$(get_selection "$phase")"
    default_skill="$(get_default_skill "$phase")"
    short_label="$(echo "$(get_phase_label "$phase")" | cut -d' ' -f1)"

    if [ "$skill" = "none" ]; then
        draw_box_mid "  ${short_label} → (跳过)"
    elif [ "$skill" = "$default_skill" ]; then
        draw_box_mid "  ${short_label} → ${skill}"
    else
        draw_box_mid "  ${short_label} → ${skill} (自定义)"
    fi
done

draw_box_bot
echo ""

# Confirm
while true; do
    read -p $'[Enter] 生成 summoner.yaml  [b] 重新选择  [q] 退出\n> ' confirm
    case "$confirm" in
        "")
            break
            ;;
        b|B)
            cleanup
            exec "$0" --force
            ;;
        q|Q)
            echo "已取消。"
            exit 0
            ;;
        *)
            printf "${RED}请按 Enter 确认，或 b 重选，或 q 退出${NC}\n"
            ;;
    esac
done

# --- Project name ---
echo ""
existing_name=$(grep -A2 '^project:' "$OUTPUT_FILE" 2>/dev/null | grep 'name:' | head -1 | sed -E 's/.*name: *"?([^"]*)"?/\1/')
default_name="${existing_name:-my-project}"
read -p "项目名称 (如 my-game-server) [${default_name}]: " PROJECT_NAME
PROJECT_NAME="${PROJECT_NAME:-${default_name}}"

# ============================================================
# Generate summoner.yaml
# ============================================================

generate_manifest "$PROJECT_NAME" \
    "define=$(get_selection define)" \
    "plan=$(get_selection plan)" \
    "implement=$(get_selection implement)" \
    "test=$(get_selection test)" \
    "review=$(get_selection review)" \
    "debug=$(get_selection debug)" \
    "ops=$(get_selection ops)"

# Merge mode: preserve extra phases from existing manifest
if [ "${MERGE_MODE:-false}" = "true" ] && [ -f "${OUTPUT_FILE}.bak" ]; then
    # Phases NOT covered by BP flow (keep them as-is)
    EXTRA_PHASES="migrate docs worktree"
    for phase in $EXTRA_PHASES; do
        existing_skill=$(grep -A1 "^  ${phase}:" "${OUTPUT_FILE}.bak" 2>/dev/null | grep 'skill:' | head -1 | sed 's/.*skill: *//')
        if [ -n "$existing_skill" ] && [ "$existing_skill" != "none" ]; then
            # Append this phase before workflows section
            sed -i '' "/^workflows:/i\\
  # ${phase} (preserved from existing)\\
  ${phase}:\\
    skill: ${existing_skill}\\
" "$OUTPUT_FILE" 2>/dev/null || true
        fi
    done
    # Cleanup backup
    rm -f "${OUTPUT_FILE}.bak"
fi

# ============================================================
# Done
# ============================================================

echo ""
printf "${GREEN}[OK] summoner.yaml 已生成${NC}\n\n"

echo "阵容一览:"
for phase in $PHASE_ORDER; do
    skill="$(get_selection "$phase")"
    default_skill="$(get_default_skill "$phase")"
    if [ "$skill" != "$default_skill" ] && [ "$skill" != "none" ]; then
        printf "  ${PURPLE}${phase} → ${skill} (自定义)${NC}\n"
    elif [ "$skill" = "none" ]; then
        printf "  ${DIM}${phase} → (跳过)${NC}\n"
    fi
done

echo ""
echo "下一步:"
echo "  1. 检查生成的 summoner.yaml: cat summoner.yaml"
echo "  2. 验证配置: ${PLUGIN_ROOT}/scripts/validate-manifest.sh summoner.yaml"
echo "  3. 初始化 Memory: ${PLUGIN_ROOT}/scripts/init-memory-db.sh ${PROJECT_NAME}"
echo ""
printf "  ${DIM}TIP: 想换阵容？重新运行 summoner-bp.sh 即可。${NC}\n"
