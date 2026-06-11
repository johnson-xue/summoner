#!/bin/bash
# Note: set -e removed — heredoc with conditionals may fail mid-stream

# summoner-init.sh — Interactive wizard to create summoner.yaml for a project
# Usage: ./summoner-init.sh
#
# Now offers 3 modes:
#   [1] BP 阵容选择 → summoner-bp.sh (逐 phase 选 skill，推荐)
#   [2] 快速默认    → summoner-bp.sh --quick (零交互)
#   [3] 手动输入    → 传统逐个输入 skill 名 (兼容旧版)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BP_SCRIPT="${SCRIPT_DIR}/summoner-bp.sh"

# If args are passed directly, map numeric modes then delegate to bp.sh
if [ $# -gt 0 ]; then
    case "$1" in
        1) exec "$BP_SCRIPT" ;;
        2) exec "$BP_SCRIPT" --quick ;;
        3) ;; # fall through to manual mode below
        --quick|-q) exec "$BP_SCRIPT" --quick ;;
        --force|-f) exec "$BP_SCRIPT" --force ;;
        --help|-h) exec "$BP_SCRIPT" --help ;;
        *)
            echo "Usage: summoner-init.sh [1|2|3|--quick|--force|--help]"
            echo "  1  BP champion select (recommended)"
            echo "  2  Quick defaults (zero interaction except project name)"
            echo "  3  Manual skill input (advanced)"
            exit 1
            ;;
    esac
    # If mode 3, continue to manual mode below
fi

echo ""
echo ">>> Summoner Init — 项目接入向导"
echo "================================"
echo ""

echo "选择初始化方式:"
echo ""
echo "  [1] BP 阵容选择  — 逐 phase 选择 skill（推荐新用户）"
echo "  [2] 快速默认     — 使用所有推荐默认值，零交互"
echo "  [3] 手动输入     — 逐个输入 skill 名（高级用户）"
echo ""

while true; do
    read -p "选择 [1-3，默认=1]: " init_mode
    init_mode="${init_mode:-1}"

    case "$init_mode" in
        1)
            echo ""
            echo "启动 BP 阵容选择..."
            exec "$BP_SCRIPT"
            ;;
        2)
            echo ""
            echo "使用快速默认模式..."
            exec "$BP_SCRIPT" --quick
            ;;
        3)
            echo ""
            echo "进入手动输入模式..."
            break
            ;;
        *)
            echo "无效选择，请输入 1、2 或 3"
            ;;
    esac
done

# ============================================================
# Manual mode — traditional interactive wizard (保留兼容)
# ============================================================

echo ""
echo "--- 手动 Skill 配置 ---"
echo ""

# Project name
read -p "项目名称 (如 my-game-server): " PROJECT_NAME
PROJECT_NAME="${PROJECT_NAME:-my-project}"

echo ""
echo "这个项目提供了哪些能力？(回车跳过 = 无此能力)"
echo ""

echo "--- 领域 skill（项目特有的） ---"

read -p "  调试/诊断 skill 名 (默认: superpowers:systematic-debugging): " input
DEBUG_SKILL="${input:-superpowers:systematic-debugging}"

read -p "  测试 skill 名 (默认: superpowers:test-driven-development): " input
TEST_SKILL="${input:-superpowers:test-driven-development}"

read -p "  运维 skill 名 (默认: superpowers:finishing-a-development-branch): " input
OPS_SKILL="${input:-superpowers:finishing-a-development-branch}"

read -p "  配置检查 skill 名 (如 my-config-skill, 无则回车): " input
CONFIG_SKILL="$input"

read -p "  新增模块 skill 名 (如 my-subsystem-skill, 无则回车): " input
SUBSYSTEM_SKILL="$input"

read -p "  RPC 接口 skill 名 (如 my-rpc-skill, 无则回车): " input
RPC_SKILL="$input"

echo ""
echo "--- 通用 skill（可直接用 superpowers） ---"
echo "  define (需求定义)  → superpowers:brainstorming"
echo "  plan (任务拆解)    → superpowers:writing-plans"
echo "  review (代码审查)  → superpowers:requesting-code-review"
echo ""

# Generate summoner.yaml
cat > summoner.yaml <<YAML
# summoner.yaml — ${PROJECT_NAME} AI 能力声明
# 由 summoner-init.sh 自动生成

version: "1"

project:
  name: "${PROJECT_NAME}"

phases:
  # 诊断
  debug:
    skill: ${DEBUG_SKILL}
YAML

# Config skill (optional)
if [ -n "$CONFIG_SKILL" ]; then
cat >> summoner.yaml <<YAML
    triggers: [config]

  # 配置
  config:
    skill: ${CONFIG_SKILL}
YAML
else
cat >> summoner.yaml <<YAML

  # 配置
  config:
    skill: none
YAML
fi

# Test skill
cat >> summoner.yaml <<YAML

  # 测试
  test:
    skill: ${TEST_SKILL}
YAML

# Ops skill
cat >> summoner.yaml <<YAML

  # 运维
  ops:
    skill: ${OPS_SKILL}
YAML

# Optional skills
if [ -n "$SUBSYSTEM_SKILL" ]; then
cat >> summoner.yaml <<YAML

  # 新增子系统
  subsystem:
    skill: ${SUBSYSTEM_SKILL}
YAML
fi

if [ -n "$RPC_SKILL" ]; then
cat >> summoner.yaml <<YAML

  # RPC 接口
  rpc:
    skill: ${RPC_SKILL}
YAML
fi

# Standard phases + workflows
cat >> summoner.yaml <<YAML

  # 通用阶段
  define:
    skill: superpowers:brainstorming

  plan:
    skill: superpowers:writing-plans

  implement:
    skill: superpowers:subagent-driven-development

  review:
    skill: superpowers:requesting-code-review

  verify:
    skill: ${TEST_SKILL}

  reproduce:
    skill: ${TEST_SKILL}

  # 显式无此能力
  security:
    skill: none

workflows:
  bugfix:
    chain: [debug, reproduce, fix, verify, review]
    checkpoints: after_each

  ship:
    fan_out:
      - persona: code-reviewer
      - persona: test-engineer
    merge: review
    checkpoints: after_merge
YAML

echo ""
echo "✓ summoner.yaml 已生成"
echo ""
echo "下一步:"
echo "  1. 检查生成的 summoner.yaml，按需修改 phase 映射"
echo "  2. 运行验证: ~/.claude/plugins/summoner/scripts/validate-manifest.sh summoner.yaml"
echo "  3. 初始化 Memory: ~/.claude/plugins/summoner/scripts/init-memory-db.sh ${PROJECT_NAME}"
