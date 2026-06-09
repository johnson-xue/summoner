## Description

<!-- Briefly describe what this PR does. -->

## Type of Change

- [ ] Bug fix
- [ ] New feature
- [ ] Documentation update
- [ ] Refactoring (no behavior change)
- [ ] Performance improvement

## Related Issues

<!-- Link to related issues: Fixes #123 -->

## Checklist

- [ ] Hooks compile: `cd hooks && make build`
- [ ] Shell scripts pass syntax check: `bash -n scripts/*.sh`
- [ ] JSON Schema is valid: `python3 -c "import json; json.load(open('references/summoner.schema.json'))"`
- [ ] No hardcoded project names in framework code (use `my-project`, `my-debug-skill` as placeholders)
- [ ] CHANGELOG.md updated (if user-facing change)
- [ ] README/README_CN.md updated (if behavior changes)
- [ ] Tested with a real project's `summoner.yaml`

## Screenshots / Demo

<!-- If applicable, add screenshots or session transcripts showing the change in action. -->

## Reviewer Notes

<!-- Anything specific the reviewer should focus on? -->
