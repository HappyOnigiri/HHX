# irreversible-guard

English | [日本語](irreversible-guard.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code and Codex.

Blocks operations that cannot be undone by the user: revoking credentials, publishing to registries, permanently
deleting remote resources, destroying Git objects, and erasing disks or backups. Publishing with `--dry-run` is allowed.

It also blocks writes to secret files such as `.env*`, `~/.ssh/*`, and `*.pem`, and deletion or relocation of `hhx` and
its hook registration files. Secret-file checks cover editing tools as well as shell commands.

The hook denies a matching operation and tells the agent to report it to the user. It does not return an `ask` decision.
Its static check cannot detect every operation hidden behind variables, scripts, or another shell.
