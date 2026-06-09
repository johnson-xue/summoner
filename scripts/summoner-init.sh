#!/bin/bash
# Note: set -e removed — heredoc with conditionals may fail mid-stream

# summoner-init.sh — Interactive wizard to create summoner.yaml for a project
# Usage: ./summoner-init.sh

echo ""
echo "⚡ Summoner Init — 项目接入向导"
echo "================================"
echo ""

# Project name
read -p "项目名称 (如 my-game-server): " PROJECT_NAME
PROJECT_NAME="${PROJECT_NAME:-my-project}"

echo ""
echo "这个项目提供了哪些能力？(回车跳过 = 无此能力)"
echo ""

echo "--- 领域 skill（项目特有的） ---"

read -p "  调试/诊断 skill 名 (默认: my-debug-skill): " input
DEBUG_SKILL="${input:-my-debug-skill}"

read -p "  测试 skill 名 (默认: my-test-skill): " input
TEST_SKILL="${input:-my-test-skill}"

read -p "  运维 skill 名 (默认: my-ops-skill): " input
OPS_SKILL="${input:-my-ops-skill}"

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
  name: ${PROJECT_NAME}

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
