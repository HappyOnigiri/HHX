# idle-wait-guard

English | [日本語](idle-wait-guard.ja.md) · [Hooks](../../README.md#hooks)

**Default:** On for Claude Code and Codex.

Blocks commands made entirely of waiting or fixed output, such as `sleep 600; echo done` or `echo ok`. These commands can fill agent turns without advancing the task. The denial explains how to wait for the actual operation instead.

If any part of the command may do useful work, the hook lets it pass. It is a text check, so it does not execute or fully interpret shell syntax.
