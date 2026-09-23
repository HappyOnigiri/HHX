# Happy Hooks

English | [日本語](README.ja.md)

Happy Hooks helps Claude Code and Codex stay on track during long-running tasks. Its hooks come in a single Go binary called `hhx`.

## Features

- **Keep work moving:** Guard against avoidable confirmation prompts and common mistakes that interrupt an agent.
- **Wait efficiently:** Give the agent a way to wait for CI without repeatedly checking individual jobs.
- **Add context when needed:** Supply relevant PR details and local instructions as the agent works.

Command guards inspect text statically. They catch common mistakes, but are not a security boundary.

## Hooks

| Hook | What it does | Default |
| --- | --- | --- |
| `pr-merge-guard` | Stops agents from merging PRs | On |
| `discard-guard` | Saves a snapshot before Git commands discard changes | On |
| `idle-wait-guard` | Stops commands used only to fill time | On |
| `forbidden-term-guard` | Blocks configured terms in PR and issue bodies | On |
| `irreversible-guard` | Blocks irreversible operations | On |
| `dangerous-rm-guard` | Stops risky `rm` commands before Claude Code asks for confirmation | On (Claude Code) |
| `exit-plan-subagent-guard` | Keeps plan mode open until background agents finish | On (Claude Code) |
| `git-hookspath-guard` | Blocks changes to Git hook settings | Off |
| `pr-context` | Adds context about PR links in prompts | On |
| `push-ci-context` | Explains how to wait for CI after a push | On |
| `pr-body-staleness` | Flags PR descriptions that may be out of date | On |
| `agents-local-context` | Adds applicable `AGENTS.local.md` instructions | On (Codex) |

## Install

Requires macOS on Apple Silicon and Claude Code or Codex.

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/install.sh | bash
hhx install
```

The installer puts `hhx` in `~/.local/bin`; `hhx install` registers its hooks with the agents already configured on your machine. To build from source, run `make install` before `hhx install`.

## Usage

To wait for CI:

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
  idle-wait-guard:
    enabled: false
```

Changes take effect without reinstalling. See [forbidden-term-guard setup](docs/forbidden-terms.md) for its repository-specific list.

## Contributing

Contributions are welcome. Share bugs and ideas in [Issues](https://github.com/HappyOnigiri/HappyHooks/issues), or send a [pull request](https://github.com/HappyOnigiri/HappyHooks/pulls). Documentation and translations are welcome too.

## Uninstall

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/uninstall.sh | bash
```

## License

[MIT](LICENSE)
