# Summoner Init 流程优化方案

## 当前问题诊断

### 问题 1: 步骤过多，体验割裂
**现状:**
```bash
# 步骤 1: 安装插件
/plugin install summoner@summoner-marketplace

# 步骤 2: 进入项目目录
cd your-project

# 步骤 3: 运行 init 脚本
~/.claude/plugins/summoner/scripts/summoner-init.sh

# 步骤 4: 提取项目名并初始化 Memory DB（复杂！）
~/.claude/plugins/summoner/scripts/init-memory-db.sh $(grep -A2 '^project:' summoner.yaml | grep 'name:' | head -1 | awk '{print $2}')

# 步骤 5: 重启 Claude Code
```

**问题:**
- 5 个步骤，其中步骤 4 需要复杂的命令行管道
- 用户容易忘记某个步骤
- 错误提示不友好

### 问题 2: 命令行语法复杂
步骤 4 的命令对普通用户不友好：
```bash
$(grep -A2 '^project:' summoner.yaml | grep 'name:' | head -1 | awk '{print $2}')
```

### 问题 3: 缺少自动化检查
- 没有检查 summoner.yaml 是否存在
- 没有检查 Memory DB 是否已初始化
- 没有提示用户重启 Claude Code

---

## 优化方案

### 方案 A: 一键初始化脚本（推荐）

创建 `scripts/summoner-setup.sh` 作为统一入口：

```bash
#!/bin/bash
set -o pipefail

# summoner-setup.sh — One-command setup for Summoner in your project
# Usage: ~/.claude/plugins/summoner/scripts/summoner-setup.sh [--quick]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_ROOT="$(dirname "$SCRIPT_DIR")"

echo ""
echo "🔮 Summoner Setup Wizard"
echo "================================"
echo ""

# Check if already initialized
if [ -f "summoner.yaml" ]; then
    echo "✓ summoner.yaml already exists"
    
    # Extract project name
    PROJECT_NAME=$(grep -A2 '^project:' summoner.yaml | grep 'name:' | head -1 | awk '{print $2}' | tr -d '"')
    
    if [ -z "$PROJECT_NAME" ]; then
        echo "⚠️  Cannot extract project name from summoner.yaml"
        read -p "Enter project name: " PROJECT_NAME
    else
        echo "  Project: $PROJECT_NAME"
    fi
    
    # Check Memory DB
    DB_FILE="$PLUGIN_ROOT/memory/${PROJECT_NAME}.db"
    if [ -f "$DB_FILE" ]; then
        echo "✓ Memory database already initialized"
        echo ""
        echo "✅ Setup complete! You can start using /summoner:fix or /summoner:new"
        exit 0
    else
        echo "⚠️  Memory database not found, initializing..."
        "$SCRIPT_DIR/init-memory-db.sh" "$PROJECT_NAME"
        echo ""
        echo "✅ Setup complete!"
        echo ""
        echo "📋 Next steps:"
        echo "  1. Restart Claude Code (hooks need activation)"
        echo "  2. Try: /summoner:fix \"your bug description\""
        exit 0
    fi
fi

# First-time setup
echo "📝 Step 1/3: Create summoner.yaml"
echo ""

# Detect quick mode
if [[ "$1" == "--quick" ]] || [[ "$1" == "-q" ]]; then
    echo "Using quick mode (all defaults)..."
    "$SCRIPT_DIR/summoner-init.sh" 2
else
    # Interactive mode selection
    echo "Choose initialization mode:"
    echo "  [1] Quick defaults (recommended) - zero interaction"
    echo "  [2] BP champion select - choose skill for each phase"
    echo "  [3] Manual input - advanced users"
    echo ""
    read -p "Select [1-3, default=1]: " MODE
    MODE="${MODE:-1}"
    
    "$SCRIPT_DIR/summoner-init.sh" "$MODE"
fi

# Check if summoner.yaml was created
if [ ! -f "summoner.yaml" ]; then
    echo "❌ Failed to create summoner.yaml"
    exit 1
fi

echo ""
echo "✓ summoner.yaml created"
echo ""

# Step 2: Extract project name and init Memory DB
echo "📝 Step 2/3: Initialize Memory database"
echo ""

PROJECT_NAME=$(grep -A2 '^project:' summoner.yaml | grep 'name:' | head -1 | awk '{print $2}' | tr -d '"')

if [ -z "$PROJECT_NAME" ]; then
    echo "⚠️  Cannot extract project name from summoner.yaml"
    read -p "Enter project name: " PROJECT_NAME
fi

echo "  Project: $PROJECT_NAME"
"$SCRIPT_DIR/init-memory-db.sh" "$PROJECT_NAME"

echo ""
echo "✓ Memory database initialized"
echo ""

# Step 3: Final instructions
echo "📝 Step 3/3: Activate hooks"
echo ""
echo "✅ Setup complete!"
echo ""
echo "📋 Next steps:"
echo "  1. **Restart Claude Code** (required for hooks to activate)"
echo "  2. Return to this project"
echo "  3. Try your first workflow:"
echo "     • Bug fix:     /summoner:fix \"描述问题\""
echo "     • New feature: /summoner:new \"需求描述\""
echo "     • Diagnosis:   /summoner:debug \"错误信息\""
echo ""
echo "📚 Documentation: $PLUGIN_ROOT/docs/"
echo ""
```

**使用体验:**
```bash
# 原来：5 个步骤
cd your-project
~/.claude/plugins/summoner/scripts/summoner-init.sh
~/.claude/plugins/summoner/scripts/init-memory-db.sh $(grep ...)
# 重启 Claude Code

# 现在：1 个命令
cd your-project
~/.claude/plugins/summoner/scripts/summoner-setup.sh
# 重启 Claude Code（脚本会提示）
```

---

### 方案 B: Claude Code Skill 包装

创建 `/summoner:setup` skill，让用户在 Claude Code 内直接运行：

```markdown
---
name: summoner-setup
description: Initialize Summoner for this project (one-command setup)
when_to_use:
  - User says "setup summoner" or "initialize summoner"
  - User wants to onboard a new project to Summoner
---

# Summoner Setup Skill

Run the setup wizard to initialize Summoner for this project.

## Steps

1. Check if summoner.yaml exists
   - If yes: check Memory DB, init if missing
   - If no: run summoner-init.sh

2. Auto-extract project name from summoner.yaml

3. Initialize Memory DB with project name

4. Provide clear next steps

## Implementation

```bash
#!/bin/bash
PLUGIN_ROOT="$HOME/.claude/plugins/summoner"

# Run setup script
"$PLUGIN_ROOT/scripts/summoner-setup.sh" --quick

# Report status
if [ $? -eq 0 ]; then
    echo ""
    echo "✅ Summoner setup complete!"
    echo ""
    echo "⚠️  **Action Required:** Please restart Claude Code to activate hooks."
    echo ""
    echo "After restart, try: /summoner:fix \"your issue\""
fi
```

## Usage

```
/summoner:setup
```

Or let user say:
- "setup summoner for this project"
- "initialize summoner"
- "onboard this project to summoner"
```

**使用体验:**
```
用户: setup summoner for this project

AI: 运行 /summoner:setup...

🔮 Summoner Setup Wizard
================================

📝 Step 1/3: Create summoner.yaml
✓ summoner.yaml created

📝 Step 2/3: Initialize Memory database
  Project: my-game-server
✓ Memory database initialized

📝 Step 3/3: Activate hooks
✅ Setup complete!

📋 Next steps:
  1. **Restart Claude Code** (required)
  2. Try: /summoner:fix "your bug"
```

---

### 方案 C: 增强错误提示和自愈

修改现有脚本，增加智能检测和自动修复：

**增强 summoner-init.sh:**
```bash
# 在脚本开头添加
if [ -f "summoner.yaml" ]; then
    echo "⚠️  summoner.yaml already exists"
    echo ""
    read -p "Overwrite? [y/N] " CONFIRM
    if [[ ! $CONFIRM =~ ^[Yy]$ ]]; then
        echo "Aborted. Existing summoner.yaml kept."
        exit 0
    fi
fi
```

**增强 init-memory-db.sh:**
```bash
# 在脚本开头添加
if [ -f "$DB_FILE" ]; then
    echo "✓ Memory database already exists: $DB_FILE"
    
    # Check table integrity
    if sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM patterns;" > /dev/null 2>&1; then
        PATTERN_COUNT=$(sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM patterns;")
        echo "  Patterns: $PATTERN_COUNT"
        echo ""
        echo "Database is healthy. No action needed."
        exit 0
    else
        echo "⚠️  Database corrupted, reinitializing..."
    fi
fi
```

---

## 推荐实施方案（组合）

### Phase 1: 立即改进（本 PR）
1. ✅ 创建 `summoner-setup.sh` 统一入口
2. ✅ 增强错误提示和自愈能力
3. ✅ 更新 README 快速开始部分

### Phase 2: 后续优化
4. ⏳ 添加 `/summoner:setup` skill
5. ⏳ 实现自动检测和提示（Hook）
6. ⏳ 添加 `summoner doctor` 诊断命令

---

## 对比表

| 方面 | 当前流程 | 方案 A (统一脚本) | 方案 B (Skill) | 方案 C (增强) |
|------|---------|------------------|----------------|--------------|
| 步骤数 | 5 步 | 2 步 | 1 步 | 4 步 |
| 复杂度 | 高（管道命令） | 低 | 极低 | 中 |
| 错误提示 | 无 | 详细 | 详细 | 详细 |
| 幂等性 | 否 | 是 | 是 | 是 |
| 自愈能力 | 无 | 有 | 有 | 有 |
| 实施难度 | - | 低 | 中 | 低 |

---

## 最终推荐

**立即实施方案 A + C:**
1. 创建 `summoner-setup.sh` 统一脚本（降低步骤数）
2. 增强现有脚本的幂等性和错误提示（增强健壮性）
3. 更新 README（改善文档）

**未来增加方案 B:**
4. 添加 `/summoner:setup` skill（最佳用户体验）

这样用户体验从 **5 步** 减少到 **2 步**（运行脚本 + 重启），未来可以减少到 **1 步**（只需在 Claude Code 中说一句话）。
