# hhx

English | [日本語](README.ja.md)

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

`~/.config/hhx/config.yaml` (override with `HHX_CONFIG`) chooses the display language, turns hooks on or off, and holds hook-specific settings:

```yaml
language: ja   # en (default) or ja
hooks:
  some-hook:
    enabled: false
```

`language` selects the language of everything hhx writes: denial reasons, the context it injects, and the output of the `hhx` commands.
Without it, or with any value other than `en` or `ja`, hhx uses English; `hhx install` reports an invalid value.
Machine-readable parts stay the same in every language: JSON keys, the tags and field names of injected context,
and the `wait-ci:` prefix, the last line, and the exit codes of `hhx wait-ci`.

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
| `exit-plan-subagent-guard` | on (Claude Code only) | Leaving plan mode (`ExitPlanMode`) while an agent started in the background has not returned its result yet. It passes once the agent finishes or is stopped |
| `git-hookspath-guard` | off | Changing `core.hooksPath`, and editing Git config files such as `.git/config` directly |

The context hooks add text to the agent's context and never deny anything:

| Hook | Default | Event | What it adds |
| --- | --- | --- | --- |
| `pr-context` | on | `UserPromptSubmit` | For each GitHub pull request URL in the prompt (up to 3, and up to 2 `#discussion_r` anchors each): its state, head and base, size, and whether a local clone has its head commit. It reminds the agent that the local files are not the pull request |
| `push-ci-context` | on | `PostToolUse` (Bash) | After a successful `git push`, `gh pr create`, or `gh workflow run`: how to wait for CI with `hhx wait-ci --progress` (or `gh run watch` for a dispatched workflow), after finishing the remaining work of the turn |
| `pr-body-staleness` | on | `PostToolUse` (Bash) | After a successful `git push`: the commits made after the pull request body was last edited, so the agent checks whether the body is out of date |
| `agents-local-context` | on (Codex only) | `PreToolUse`, `SessionStart`, `SubagentStart` | The `AGENTS.local.md` files that apply to the paths a tool touches, from the Git root down, once per session. It re-injects them after compaction and for subagents |

`git-hookspath-guard` is meant for setups that delegate from global Git hooks to repository hooks. Turn it on with:

```yaml
hooks:
  git-hookspath-guard:
    enabled: true
```

`pr-body-staleness` tells the agent to update the body with the `update-pr` skill.
Replace that sentence with your own way of updating a pull request body.
hhx uses it as written in every language, adding a closing `.` (or `。` with `language: ja`) when it has no sentence-ending mark:

```yaml
hooks:
  pr-body-staleness:
    update-instruction: "If they disagree, update the body with `gh pr edit --body-file`."
```

`pr-context` and `pr-body-staleness` call `gh` (it must be on `PATH` and signed in) and give up silently when it fails or takes more than 8 seconds.
`pr-context` keeps a 90-second cache of what it fetched and a 24-hour record of what it injected in each session,
and `agents-local-context` keeps a 30-day record of the rules it injected in each session.
Both live under `$XDG_CACHE_HOME/hhx/` (`~/.cache/hhx/` by default); deleting them only makes the hooks inject again.

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
After a `cd` inside `( ... )` or `$( ... )`, it saves both the directory outside the parentheses and the `cd` target,
because it counts parentheses without telling quoted ones or `case` patterns apart.
Several `-C` in one `git` call apply in order, as in Git.

The ref lives in the common Git directory, so all worktrees of a repository share it.
Each entry's message is `wt-snapshot: <rules> @ <worktree>`: the rules that fired (such as `git-reset-hard / git-clean`,
the same in every language) and the top-level directory of the worktree it came from. To restore:

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
- It misses `git checkout <path>` and `git checkout <commit> <path>` written without `--`, `git checkout --ours <path>`, and `git rm -f`.
- It misses a discarding command written after a global option it does not list, such as `git -P reset --hard` or `git --no-optional-locks reset --hard`.
  It lists `-c`, `-C`, `-p`, `--paginate`, `--no-pager`, `--git-dir=`, `--work-tree=`, `--exec-path=`, and `--literal-pathspecs`.
- It does not save uncommitted changes inside submodules, so `git reset --hard --recurse-submodules` still loses them.
  An untracked nested repository is saved only as its commit ID, so `git clean -ffd` still loses its files and history.
- Text that only mentions a discarding command, such as a heredoc, also triggers a snapshot.
- It denies commands whose target it cannot follow: paths with variables, globs, or quotes, `pushd` / `popd`, `sh -c`,
  `GIT_DIR` / `--git-dir` / `--work-tree`, and directories that do not exist yet (created by `mkdir` or `git clone` in the same command).
  Run the step that creates the directory first, in a separate command.
- Codex does not pass the working directory of each command to hooks, only the one the session started in, so it decides the target from that.
- It treats a `cd` in a pipeline or in a background command (`cd dir | ...`, `cd dir & ...`) as if it changed the directory
  for the rest of the command, although the shell may run it in a subshell.

The context hooks match text, too:

- `pr-context` picks up a URL whenever `github.com` follows a separator, so it also reacts to `evil.com/github.com/o/r/pull/1`.
- `pr-body-staleness` compares commit dates, which rebase, amend, and cherry-pick reset,
  so it may ask the agent to check a body that is still correct. It misses old commits brought in later.
- `push-ci-context` decides whether the push went to GitHub from the command output, and otherwise from the origin of the working directory.
  Codex passes only the directory the session started in, so it injects the guidance whenever it cannot tell.
- `agents-local-context` finds paths in commands on a best-effort basis: quoted text, heredocs, and variables can hide a path or look like one.

`exit-plan-subagent-guard` reads only the session transcript:

- It depends on the exact texts Claude Code writes when a background agent starts, resumes, and finishes.
  If those texts change, it silently stops denying.
- Text that looks like a finish notification, for example read from a file, counts as the agent having finished.
- It tracks only background agents started by the main session, not agents run in the foreground or started by subagents.
- It passes when the transcript is missing or cannot be read.

## Waiting for CI

`hhx wait-ci` waits until every check of a pull request has finished and reports the result once,
so an agent can wait for CI after a push without reading a stream of per-job updates.

```sh
hhx wait-ci --progress         # the pull request of the current branch (or of HEAD when it is detached)
hhx wait-ci 123 --all-checks   # a PR number, branch, or URL; list passing checks too
```

The first line of the output is the verdict and the last line is `wait-ci: exit=<code> failed=<n> total=<n>`,
so either end of a truncated output still tells the result. Everything goes to stdout except `--progress` lines, which go to stderr.
By default only failed checks are listed.

| Exit code | Meaning |
| --- | --- |
| 0 | Every check passed, the branch has no pull request, or the repository has no CI |
| 1 | Some checks failed |
| 2 | Invalid arguments |
| 3 | Timed out (the pushed commit never became the PR head, no check appeared, or the checks did not finish) |
| 4 | `gh` kept failing |
| 5 | The pull request conflicts with its base, so no check starts |

It calls `gh` and `git`, so both must be on `PATH` and `gh` must be signed in.
Without a reference it first waits for `HEAD` to become the PR head, so it does not report the checks of the previous push.
It also waits until the set of finished checks stays the same for `--settle` seconds, to catch workflows that start after others.
Run `hhx wait-ci --help` for the time limits.

## License

MIT
