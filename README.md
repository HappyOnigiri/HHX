# hhx

`hhx` is a single Go binary that provides agent hooks for Claude Code and Codex.
Each hook is registered as its own entry, `hhx hook <name>`, so a slow hook never delays the others.

Hooks inspect commands statically. They catch common mistakes; they are not a security boundary.

## Install

hhx is distributed for macOS on Apple Silicon.

```sh
curl -fsSL https://github.com/HappyOnigiri/HHX/releases/latest/download/install.sh | bash
hhx install         # registers hhx hooks in ~/.claude/settings.json and ~/.codex/hooks.json
```

The installer places the binary at `~/.local/bin/hhx` after verifying its checksum; it never registers hooks by itself.
To build from source instead, run `make install` (it builds `bin/hhx` and copies it to `~/.local/bin/hhx`).

`hhx install` writes only its own hook groups and leaves every other hook untouched.
It is idempotent: running it again without changes does not modify the files.
By default it configures each agent whose config directory (`~/.claude`, `~/.codex`) exists; pass `--agent claude` or `--agent codex` to choose.
hhx always writes Claude's user settings (`~/.claude/settings.json`), even when `~/.claude/settings.local.json` exists.

`hhx uninstall` removes only the entries hhx wrote.

## Update

```sh
hhx update          # checks GitHub Releases for a newer version
hhx update --apply  # installs it with the installer of that release
```

hhx checks for updates only when you run `hhx update`; hooks never access the network for it.
Development builds (`make install`) do not update themselves.

## Uninstall

```sh
curl -fsSL https://github.com/HappyOnigiri/HHX/releases/latest/download/uninstall.sh | bash
```

It runs `hhx uninstall` and then removes `~/.local/bin/hhx`.
Configuration and caches are kept; the script prints their locations.

## Configuration

`~/.config/hhx/config.yaml` (override with `HHX_CONFIG`) turns hooks on or off and holds hook-specific settings:

```yaml
hooks:
  some-hook:
    enabled: false
```

Every known hook is registered by `hhx install`; whether it runs is decided from this file at run time, so changing the file never requires reinstalling.
A missing or broken file never stops a hook: hooks fall back to their defaults.
`hhx install` reports configuration errors instead.

## Hooks

| Hook | Default | What it denies |
| --- | --- | --- |
| `pr-merge-guard` | on | Merging a pull request: `gh pr merge`, the merge REST endpoints, the GraphQL merge mutations, GitHub API updates through `curl` / `wget`, and `git fetch ... pull/N/head` |
| `idle-wait-guard` | on | Commands that do nothing but wait or print literals (`sleep 600`, `echo ok`), which agents use to fill turns while waiting |
| `forbidden-term-guard` | on | Sending a PR or issue body that contains a term from the repository's list (see [docs/forbidden-terms.md](docs/forbidden-terms.md)) |
| `git-hookspath-guard` | off | Changing `core.hooksPath`, and editing Git config files such as `.git/config` directly |

Messages shown to the agent are currently in Japanese.

`git-hookspath-guard` is meant for setups that delegate from global Git hooks to repository hooks. Turn it on with:

```yaml
hooks:
  git-hookspath-guard:
    enabled: true
```

`forbidden-term-guard` does nothing unless the repository has `forbidden-terms.txt` in its common Git directory.

### Allowing merges for one session

Start the agent CLI with `AGENT_ALLOW_PR_MERGE=1` to let `pr-merge-guard` pass everything in that session.
Hooks inherit the environment of the agent CLI process, so only the person who starts the session can set it:
an agent that runs `export AGENT_ALLOW_PR_MERGE=1` in its shell does not change the environment its hooks see.
Any value other than `1` keeps the guard on.

### Limits

The guards read the command text; they do not run it or model the shell.
They stop common mistakes and are not a security boundary.
An agent that deliberately hides a command gets past them, for example with variable expansion (`$CMD`), `eval`,
another shell (`sh -c '...'`), a script file, or another language (`python3 -c ...`).
Quoted text is not treated as a command, so `echo 'gh pr merge'` is allowed on purpose.
What discourages an agent from working around a guard is the reason in the denial,
which tells it to report to the user instead of trying another command.

## License

MIT
