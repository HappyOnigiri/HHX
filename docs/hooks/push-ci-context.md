# push-ci-context

English | [日本語](push-ci-context.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code and Codex.

After a successful `git push` or `gh pr create`, this hook tells the agent to finish its remaining work, then run `hhx wait-ci --progress` and follow CI to completion. This avoids repeated short checks and prevents the agent from ending its turn before CI finishes.

After a successful `gh workflow run`, it instead guides the agent to watch that workflow with `gh run watch`. Failed or dry-run pushes do not trigger the guidance. The hook adds context; it does not make a permission decision.
