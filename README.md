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
| `discard-guard` | on | Nothing by itself. Before a Git command that discards uncommitted changes, it saves a snapshot of the working tree (see below); it denies the command only when it cannot tell which repository is affected or cannot save the snapshot |
| `idle-wait-guard` | on | Commands that do nothing but wait or print literals (`sleep 600`, `echo ok`), which agents use to fill turns while waiting |
| `forbidden-term-guard` | on | Sending a PR or issue body that contains a term from the repository's list (see [docs/forbidden-terms.md](docs/forbidden-terms.md)) |
| `irreversible-guard` | on | Operations nobody can undo: revoking or deleting credentials, publishing to package registries (unless `--dry-run`), deleting remote resources, destroying Git objects, erasing disks or backups, writing to secret files (`.env*`, `~/.ssh`, `*.pem`) from any tool, and deleting or moving hhx itself, `~/.config/hhx`, or the files that register the hooks |
| `dangerous-rm-guard` | on (Claude Code only) | The `rm` / `rmdir` forms that Claude Code's built-in check would stop with a confirmation prompt that no permission setting can skip: paths that start with a possibly empty variable, targets that cannot be resolved statically, critical directories, and the working directory or its ancestors |
| `git-hookspath-guard` | off | Changing `core.hooksPath`, and editing Git config files such as `.git/config` directly |

Messages shown to the agent are currently in Japanese.

`git-hookspath-guard` is meant for setups that delegate from global Git hooks to repository hooks. Turn it on with:

```yaml
hooks:
  git-hookspath-guard:
    enabled: true
```

`forbidden-term-guard` does nothing unless the repository has `forbidden-terms.txt` in its common Git directory.

`dangerous-rm-guard` follows the rules of the built-in check in Claude Code v2.1.239 and denies those commands first,
so the agent gets a reason it can act on instead of a prompt that interrupts the user.
Newer Claude Code versions may have changed the built-in check.

### Discard snapshots

`discard-guard` watches `git reset --hard`, the discarding forms of `git checkout` (`-- <path>`, `.`, `-f`, `-B`),
`git switch -f` / `-C` / `--discard-changes`, `git restore <path>` (except `--staged` alone), `git clean -f`,
and `git apply -R` / `--3way` / `--reject`.
Before such a command runs, it commits the working tree, tracked and untracked files alike, to the reflog of `refs/hhx/discard-snapshot`.
It leaves the working tree and the index untouched, and it saves nothing when the working tree matches `HEAD`.
It follows literal `cd` and `git -C` to find the repository, and saves every repository the command discards in.

The ref lives in the common Git directory, so all worktrees of a repository share it.
Each entry's message ends with `@ <worktree>`, the top-level directory of the worktree it came from. To restore:

```sh
# 1. Find the entry of your worktree
git reflog show --format='%gd %gs' refs/hhx/discard-snapshot
# 2. Put its files back into the working tree (the index is left alone)
git restore --source='refs/hhx/discard-snapshot@{N}' --worktree -- .
# or take a single file
git show 'refs/hhx/discard-snapshot@{N}:path/to/file'
```

Snapshots are reachable only from the reflog, so `git gc` expires old entries like any other reflog entry.

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

`discard-guard` errs on the side of saving:

- Untracked ignored files are not saved, so `git clean -fdx` still loses them. Tracked files that match `.gitignore` are saved.
- It cannot save what a command discards on another machine, for example through `ssh`.
- It misses `--git-dir <path>` written with a space instead of `=`.
- Text that only mentions a discarding command, such as a heredoc, also triggers a snapshot.
- It denies commands whose target it cannot follow: paths with variables, globs, or quotes, `pushd` / `popd`, `sh -c`,
  `GIT_DIR` / `--git-dir` / `--work-tree`, and directories that do not exist yet (created by `mkdir` or `git clone` in the same command).
  Run the step that creates the directory first, in a separate command.
- Codex does not pass the working directory of each command to hooks, only the one the session started in, so it decides the target from that.

## License

MIT
