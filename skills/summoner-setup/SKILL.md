---
name: summoner-setup
description: Initialize Summoner for this project (one-command setup)
when_to_use:
  - User says "setup summoner" or "initialize summoner"
  - User wants to onboard a new project to Summoner
  - User asks "how to use summoner in this project"
allowed-tools:
  - Bash
  - Read
  - Write
---

# Summoner Setup Skill

Run the setup wizard to initialize Summoner for this project.

## What This Does

1. Creates `summoner.yaml` (if not exists) with phase-to-skill mappings
2. Initializes Memory database for pattern storage
3. Provides clear next steps for activation

## Usage

User can say:
- "setup summoner for this project"
- "initialize summoner"
- "onboard this project to summoner"

Or explicitly invoke: `/summoner:setup`

## Implementation

```bash
PLUGIN_ROOT="$HOME/.claude/plugins/summoner"

# Check if plugin installed
if [ ! -d "$PLUGIN_ROOT" ]; then
    echo "❌ Summoner plugin not found."
    echo ""
    echo "Install via:"
    echo "  /plugin marketplace add johnson-xue/summoner"
    echo "  /plugin install summoner@summoner-marketplace"
    exit 1
fi

# Run setup script
if "$PLUGIN_ROOT/scripts/summoner-setup.sh"; then
    echo ""
    echo "✅ Summoner setup complete!"
    echo ""
    echo "⚠️  **Action Required:** Please restart Claude Code to activate hooks."
    echo ""
    echo "After restart, try:"
    echo "  /summoner:fix \"your bug description\""
    echo "  /summoner:new \"your feature request\""
else
    echo ""
    echo "❌ Setup failed. Check error messages above."
    exit 1
fi
```

## Quick Mode

For CI/automation, use quick mode (all defaults):

```bash
"$PLUGIN_ROOT/scripts/summoner-setup.sh" --quick
```

## Troubleshooting

### Error: "Cannot extract project name"

Check `summoner.yaml` format:
```yaml
project:
  name: "your-project-name"
```

### Error: "Memory database corrupted"

The script will automatically reinitialize. If issues persist:
```bash
rm ~/.claude/plugins/summoner/memory/your-project.db
./scripts/summoner-setup.sh
```

### Hooks not working after restart

Verify hooks are compiled:
```bash
ls ~/.claude/plugins/summoner/hooks/bin/
```

Should see: `session-start`, `pretooluse-skill`, `stop`

If missing:
```bash
cd ~/.claude/plugins/summoner/hooks
make build
```

## Related

- `scripts/summoner-setup.sh` - The actual setup script
- `scripts/summoner-init.sh` - YAML generation (called by setup)
- `scripts/init-memory-db.sh` - Memory DB initialization (called by setup)
