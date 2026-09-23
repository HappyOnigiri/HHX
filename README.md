# Happy Hooks

English | [日本語](README.ja.md)

Happy Hooks helps Claude Code and Codex stay on track during long-running tasks. Its hooks come in a single Go binary called `hhx`.

## Features

- **Keep work moving:** Guard against avoidable confirmation prompts and common mistakes that interrupt an agent.
- **Wait efficiently:** Give the agent a way to wait for CI without repeatedly checking individual jobs.
- **Add context when needed:** Supply relevant PR details and local instructions as the agent works.
- **Protect uncommitted work:** Save a Git snapshot before commands that discard changes.

Command guards inspect text statically. They catch common mistakes, but are not a security boundary.

## Install

Requires macOS on Apple Silicon and Claude Code or Codex.

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/install.sh | bash
hhx install
```

The installer puts `hhx` in `~/.local/bin`; `hhx install` registers its hooks with the agents already configured on your machine. To build from source, run `make install` before `hhx install`.

## Usage

Hooks run automatically after installation. For CI, the agent can use:

```sh
hhx wait-ci --progress       # Wait for the current pull request
hhx wait-ci 123 --progress   # Wait for a specific pull request
```

To check for or apply a new release, run `hhx update` or `hhx update --apply`. Hooks do not check for updates while they run.

## Configuration

Use `~/.config/hhx/config.yaml` to choose a display language or disable a hook:

```yaml
language: ja  # en (default) or ja
hooks:
  some-hook:
    enabled: false
```

Changes take effect without reinstalling. See `hhx --help` for commands and options, and [forbidden-term-guard setup](docs/forbidden-terms.md) for its repository-specific list.

## Recovering discarded changes

Before a Git command that discards uncommitted changes, `discard-guard` saves tracked and untracked files to `refs/hhx/discard-snapshot`. To find and restore a snapshot:

```sh
git reflog show --format='%gd %gs' refs/hhx/discard-snapshot
git restore --source='refs/hhx/discard-snapshot@{N}' --worktree -- .
```

Replace `N` with the entry for your worktree. The index is left alone. Ignored files and uncommitted changes inside submodules are not saved.

## Contributing

Contributions are welcome. Share bugs and ideas in [Issues](https://github.com/HappyOnigiri/HappyHooks/issues), or send a [pull request](https://github.com/HappyOnigiri/HappyHooks/pulls). Documentation and translations are welcome too.

## Uninstall

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/uninstall.sh | bash
```

This removes the hook entries and binary. Configuration and caches remain; the script prints their locations.

## License

[MIT](LICENSE)
