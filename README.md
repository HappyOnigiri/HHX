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

## License

MIT
