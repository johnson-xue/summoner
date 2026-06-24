---
name: auto-setup-summoner
description: Automatically guide user to initialize Summoner when summoner.yaml is missing
when_to_use:
  - User tries to use /summoner:* command but summoner.yaml not found
  - SessionStart hook detects missing config and user asks about the warning
  - User asks "how to fix summoner warning" or "summoner not initialized"
allowed-tools:
  - Bash
  - Read
---

# Auto Setup Summoner Skill

Automatically detect if Summoner is not initialized and guide user through setup.

## When This Triggers

This skill is invoked automatically when:
1. SessionStart hook detects no summoner.yaml
2. User attempts /summoner:* command in an uninitialized project
3. User asks about Summoner warnings

## Behavior

### Detection
Check if summoner.yaml exists in current directory:
```bash
if [ ! -f "summoner.yaml" ]; then
    # Not initialized
    echo "Summoner is not initialized for this project."
else
    # Initialized
    echo "Summoner is already initialized."
fi
```

### Auto-Guide Flow

**Step 1: Detect State**
```bash
PLUGIN_ROOT="$HOME/.claude/plugins/summoner"

if [ ! -f "summoner.yaml" ]; then
    STATE="not_initialized"
elif [ ! -f "$PLUGIN_ROOT/memory/$(grep 'name:' summoner.yaml | head -1 | awk '{print $2}' | tr -d '"').db" ]; then
    STATE="partial"
else
    STATE="ready"
fi
```

**Step 2: Present Options Based on State**

**Case A: Not Initialized (no summoner.yaml)**
```
🔮 Summoner Setup Required

I notice Summoner isn't set up for this project yet. Would you like me to initialize it now?

This will:
  ✓ Create summoner.yaml with skill mappings
  ✓ Initialize Memory database
  ✓ Take ~10 seconds

[Options]
  1. Yes, use quick defaults (recommended)
  2. Yes, let me choose skills for each phase
  3. No, I'll do it manually later

Say "1", "2", or "3"
```

**Case B: Partial Setup (summoner.yaml exists but Memory DB missing)**
```
🔮 Summoner Setup Incomplete

I found summoner.yaml but the Memory database isn't initialized.

Would you like me to complete the setup? (takes ~3 seconds)

Say "yes" to proceed.
```

**Case C: Already Setup**
```
✅ Summoner is ready!

You can use:
  /summoner:fix "bug description"
  /summoner:new "feature request"
  /summoner:debug "error message"
```

**Step 3: Execute Based on User Choice**

**User says "1" or "yes" (quick mode):**
```bash
"$PLUGIN_ROOT/scripts/summoner-setup.sh" --quick

if [ $? -eq 0 ]; then
    echo "✅ Setup complete!"
    echo ""
    echo "⚠️  **Important:** Restart Claude Code to activate hooks."
    echo ""
    echo "After restart, try: /summoner:fix \"your bug\""
else
    echo "❌ Setup failed. Check error messages above."
fi
```

**User says "2" (interactive mode):**
```bash
"$PLUGIN_ROOT/scripts/summoner-setup.sh"
# (Script will ask for mode selection)
```

**User says "3" or "no":**
```
No problem! When you're ready, run:
  summoner-setup.sh

Or just say "setup summoner" anytime.
```

## Integration with Commands

Modify `/summoner:*` commands to check initialization first:

```markdown
# In commands/fix.md (and other commands)

## Pre-flight Check

Before executing workflow, verify Summoner is initialized:

```bash
if [ ! -f "summoner.yaml" ]; then
    echo "⚠️  Summoner is not initialized for this project."
    echo ""
    echo "Would you like to set it up now? (takes ~10 seconds)"
    echo ""
    echo "Say 'yes' to initialize with defaults, or 'no' to skip."
    
    # Wait for user response
    # If yes: run summoner-setup.sh --quick
    # If no: explain manual setup and exit
    exit 1
fi
```

If initialized, proceed with workflow normally.
```

## Error Recovery

If setup fails mid-process:

```bash
# Check what's missing
if [ ! -f "summoner.yaml" ]; then
    echo "❌ summoner.yaml creation failed"
    echo "Try running: summoner-init.sh 2"
elif [ ! -f "$PLUGIN_ROOT/memory/$PROJECT.db" ]; then
    echo "❌ Memory DB initialization failed"
    echo "Try running: init-memory-db.sh $PROJECT"
else
    echo "✅ Setup looks complete"
    echo "If you still see warnings, try restarting Claude Code"
fi
```

## User Experience Flow

### Scenario 1: User tries /summoner:fix before setup

```
User: /summoner:fix "nil pointer at line 123"

AI: 🔮 Summoner Setup Required

    I notice Summoner isn't set up for this project yet.
    Would you like me to initialize it now? (takes ~10 seconds)
    
    Say "yes" to proceed with quick defaults.

User: yes

AI: [Running summoner-setup.sh --quick...]
    
    ✅ Setup complete!
    
    ⚠️  **Important:** Please restart Claude Code to activate hooks.
    
    After restart, I'll be able to run /summoner:fix for you!

User: [restarts Claude Code]

User: /summoner:fix "nil pointer at line 123"

AI: [Executes fix workflow normally...]
```

### Scenario 2: User sees warning at session start

```
[SessionStart hook output:]
⚠️  No summoner.yaml found — Summoner not initialized for this project

🔮 To get started, just say:
   "setup summoner"

User: setup summoner

AI: [Detects request, invokes this skill]
    
    🔮 Summoner Setup
    
    This will create summoner.yaml and initialize Memory database.
    
    Choose mode:
      1. Quick defaults (recommended)
      2. Choose skills per phase
    
    Say "1" or "2"

User: 1

AI: [Runs setup...]
    ✅ Complete! Restart Claude Code to activate.
```

### Scenario 3: Proactive suggestion

```
User: I want to add a new feature

AI: I can help with that!
    
    I notice you have Summoner installed. Would you like to use
    the structured /summoner:new workflow? It guides you through:
      • Requirements definition
      • Task breakdown
      • Implementation
      • Testing
      • Code review
    
    However, Summoner needs to be initialized first (takes ~10 seconds).
    
    Say "yes" to set up Summoner, or "no" to proceed without it.
```

## Related

- `scripts/summoner-setup.sh` - The setup script this skill invokes
- `hooks/session-start/main.go` - Detection and warning
- `skills/summoner/SKILL.md` - Main Summoner skill
