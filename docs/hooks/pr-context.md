# pr-context

English | [日本語](pr-context.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code and Codex.

When a user prompt contains a GitHub pull request URL, this hook adds the PR's state, head and base branches, change
size, and whether the local clone has its head commit. It reminds the agent that local files may not represent the PR.
It does not include the PR body.

It handles up to three PR URLs per prompt and up to two linked discussion comments per PR. Repeated context is cached
within the session. It calls `gh`, so `gh` must be installed and authenticated; failures do not block the prompt.
