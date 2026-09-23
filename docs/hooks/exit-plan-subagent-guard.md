# exit-plan-subagent-guard

English | [日本語](exit-plan-subagent-guard.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code only.

Stops `ExitPlanMode` while a background agent started by the main session has not returned. It allows the exit once that agent finishes or is stopped, so its result can be included in the plan.

The hook reads Claude Code's session transcript. If the transcript is unavailable or its notification format changes, it may not detect an unfinished agent. It does not track foreground agents or background agents started by subagents.
