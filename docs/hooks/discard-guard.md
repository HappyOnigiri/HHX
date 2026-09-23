# discard-guard

English | [日本語](discard-guard.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code and Codex.

Before a Git command discards uncommitted changes, this hook saves tracked and untracked files in the reflog of
`refs/hhx/discard-snapshot`. It covers commands such as `git reset --hard`, forced checkout or switch, `git restore`,
and `git clean -f`. A successful snapshot lets the command proceed; an unresolved target or failed snapshot blocks it.
The working tree and index are not changed by the snapshot.

## Restore a snapshot

Find the entry marked with your worktree's path, then substitute its number for `N`:

```sh
git reflog show --format='%gd %gs' refs/hhx/discard-snapshot
git restore --source='refs/hhx/discard-snapshot@{N}' --worktree -- .
```

This restores the working tree, not the index. Ignored files and uncommitted changes inside submodules are not saved. Snapshots expire with the reflog.
