# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Summoner, please **do not** open a public issue.

Instead, email the maintainer at the address listed in the GitHub profile. Include:

- A description of the vulnerability
- Steps to reproduce
- Potential impact

You will receive a response within 48 hours. The issue will be fixed and disclosed responsibly.

## Scope

Summoner runs as a local Claude Code plugin. Security considerations include:

- **SQLite injection**: Memory database queries use parameterized statements.
- **Shell injection**: Hook scripts do not pass user input directly to shell commands.
- **State file access**: `/tmp/summoner-state/` files are only readable by the current user.

## Supported Versions

| Version | Supported |
|---------|:---------:|
| 0.1.x   | ✅ |
