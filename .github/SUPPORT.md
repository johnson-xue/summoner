# Support

## Documentation

- [README.md](../README.md) — Installation and quick start (English)
- [README_CN.md](../README_CN.md) — 安装和快速开始 (中文)
- [docs/index.html](../docs/index.html) — Documentation site
- [CHANGELOG.md](../CHANGELOG.md) — Version history

## Getting Help

1. Check the [README](../README.md) for installation and usage instructions
2. Review the [reference docs](../references/) for protocol specifications
3. [GitHub Discussions](https://github.com/johnson-xue/summoner/discussions) — Q&A, ideas, show your setup
4. Search [existing issues](https://github.com/johnson-xue/summoner/issues) for known bugs
5. Open a [bug report](https://github.com/johnson-xue/summoner/issues/new?template=bug_report.yml) or [feature request](https://github.com/johnson-xue/summoner/issues/new?template=feature_request.yml)

## Common Issues

### Hooks don't seem to work
Run `cd ~/.claude/plugins/summoner/hooks && make build` to recompile the Go binaries. Restart Claude Code.

### Memory DB is empty
Run `~/.claude/plugins/summoner/scripts/init-memory-db.sh <project-name>` to create and seed the database.

### summoner.yaml validation fails
Run `~/.claude/plugins/summoner/scripts/validate-manifest.sh summoner.yaml` to see detailed errors.
