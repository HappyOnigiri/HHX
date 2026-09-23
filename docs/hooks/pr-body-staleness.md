# pr-body-staleness

English | [日本語](pr-body-staleness.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code and Codex.

After a successful `git push`, this hook compares the PR body's last edit time with commit dates. If it finds newer
commits, it asks the agent to check whether the body still describes the changes. A newer commit does not automatically
mean the body is wrong.

The default guidance refers to the `update-pr` skill. To replace it, set `hooks.pr-body-staleness.update-instruction` in `~/.config/hhx/config.yaml`:

```yaml
hooks:
  pr-body-staleness:
    update-instruction: "If the body is stale, update it with gh pr edit."
```

The hook calls authenticated `gh` and gives up silently if it cannot obtain the PR. Rebases and cherry-picks can change
commit dates, so it may ask for a check when the body is still correct.
