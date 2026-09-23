#!/usr/bin/env python3
"""agents-local-context.py の契約テスト。"""
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import GIT_ISOLATION, TARGET, git, hook_command


SCRIPT = "agents-local-context.py"


class AgentsLocalContextTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(
            os.path.realpath(tempfile.mkdtemp(prefix="agents-local-context-"))
        )
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.repo = self.tmp / "repo"
        self.repo.mkdir()
        git(self.repo, "init", "-q")
        (self.repo / "AGENTS.local.md").write_text("root-local\n", encoding="utf-8")
        self.nested = self.repo / "src/service"
        self.nested.mkdir(parents=True)
        (self.repo / "src/AGENTS.local.md").write_text(
            "src-local\n", encoding="utf-8"
        )
        self.target = self.nested / "target.py"
        self.target.write_text("print('ok')\n", encoding="utf-8")
        self.codex_home = self.tmp / "codex-home"

    def environment(self, codex_home=None):
        # hhx は状態を $XDG_CACHE_HOME/hhx に置くので、CODEX_HOME の代わりに XDG_CACHE_HOME で隔離する。
        state_home = "XDG_CACHE_HOME" if TARGET == "hhx" else "CODEX_HOME"
        return {
            **os.environ,
            **GIT_ISOLATION,
            "HOME": str(self.tmp),
            state_home: str(codex_home or self.codex_home),
        }

    def payload(
        self,
        *,
        event="PreToolUse",
        session="session-1",
        tool_name="Bash",
        tool_input=None,
        source=None,
    ):
        data = {
            "session_id": session,
            "transcript_path": str(self.tmp / "transcript.jsonl"),
            "hook_event_name": event,
            "cwd": str(self.repo),
        }
        if event == "PreToolUse":
            data.update(
                {
                    "turn_id": "turn-1",
                    "tool_name": tool_name,
                    "tool_use_id": "tool-1",
                    "tool_input": tool_input
                    or {"command": f"sed -n 1p {self.target}"},
                }
            )
        if source is not None:
            data["source"] = source
        return data

    def run_hook(self, payload=None, *, raw=None, codex_home=None):
        if raw is None:
            raw = json.dumps(payload or self.payload())
        return subprocess.run(
            hook_command(SCRIPT),
            input=raw,
            capture_output=True,
            text=True,
            timeout=30,
            cwd=self.repo,
            env=self.environment(codex_home),
        )

    def output(self, process):
        self.assertEqual(process.returncode, 0, process.stderr)
        if not process.stdout.strip():
            return {}
        return json.loads(process.stdout)

    def context(self, process):
        data = self.output(process)
        return data.get("hookSpecificOutput", {}).get("additionalContext", "")

    def test_injects_root_to_target_rules_once_per_session(self):
        first = self.run_hook()
        data = self.output(first)
        context = data["hookSpecificOutput"]["additionalContext"]

        self.assertLess(context.index("root-local"), context.index("src-local"))
        self.assertIn(f"source: {self.repo}/AGENTS.local.md", context)
        self.assertIn(f"scope: {self.repo}/src/**", context)
        self.assertNotIn("permissionDecision", json.dumps(data))
        self.assertEqual(self.run_hook().stdout, "")

    def test_content_change_is_reinjected(self):
        self.run_hook()
        (self.repo / "src/AGENTS.local.md").write_text(
            "src-local-v2\n", encoding="utf-8"
        )

        context = self.context(self.run_hook())

        self.assertIn("src-local-v2", context)
        self.assertNotIn("root-local", context)

    def test_different_session_gets_its_own_context(self):
        self.assertTrue(self.context(self.run_hook()))
        second = self.payload(session="session-2")
        self.assertTrue(self.context(self.run_hook(second)))

    def test_apply_patch_adds_context_without_blocking(self):
        patch = """*** Begin Patch
*** Update File: src/service/target.py
@@
-print('ok')
+print('updated')
*** End Patch"""
        payload = self.payload(
            tool_name="apply_patch", tool_input={"command": patch}
        )

        data = self.output(self.run_hook(payload))

        self.assertIn("src-local", data["hookSpecificOutput"]["additionalContext"])
        self.assertNotIn("decision", data)
        self.assertNotIn("permissionDecision", json.dumps(data))

    def test_command_parse_failure_is_silent_without_fallback_tokens(self):
        command = """cat <<'PY'
cd src/service
const n = /data-page="/g;
PY"""
        payload = self.payload(
            session="parse-fallback-session",
            tool_input={"command": command},
        )

        data = self.output(self.run_hook(payload))

        self.assertNotIn("systemMessage", data)
        context = data["hookSpecificOutput"]["additionalContext"]
        self.assertIn("root-local", context)
        self.assertNotIn("src-local", context)

    def test_write_and_edit_add_context_without_blocking(self):
        for tool_name in ("Write", "Edit"):
            with self.subTest(tool_name=tool_name):
                payload = self.payload(
                    session=f"{tool_name.lower()}-session",
                    tool_name=tool_name,
                    tool_input={"file_path": str(self.target), "content": "updated"},
                )

                data = self.output(self.run_hook(payload))

                self.assertIn(
                    "src-local", data["hookSpecificOutput"]["additionalContext"]
                )
                self.assertNotIn("decision", data)
                self.assertNotIn("permissionDecision", json.dumps(data))

    def test_direct_path_tool_input_is_supported(self):
        payload = self.payload(
            tool_name="mcp__filesystem__read_file",
            tool_input={"file_path": str(self.target)},
        )
        self.assertIn("src-local", self.context(self.run_hook(payload)))

    def test_no_rule_is_silent(self):
        outside = self.tmp / "outside"
        outside.mkdir()
        payload = self.payload(tool_input={"command": f"ls {outside}"})
        payload["cwd"] = str(outside)

        process = self.run_hook(payload)

        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertEqual(process.stdout, "")

    def test_state_failure_warns_and_still_injects_without_blocking(self):
        invalid_home = self.tmp / "not-a-directory"
        invalid_home.write_text("file\n", encoding="utf-8")

        data = self.output(self.run_hook(codex_home=invalid_home))

        # hhx は SQLite をやめてファイルに記録するので、警告の語を変えた。
        self.assertIn("状態ファイルを利用できない" if TARGET == "hhx" else "状態DBを利用できない",
                      data["systemMessage"])
        self.assertIn("root-local", data["hookSpecificOutput"]["additionalContext"])
        self.assertNotIn("decision", data)

    def test_rule_read_failure_warns_and_keeps_other_context(self):
        (self.repo / "src/AGENTS.local.md").write_bytes(b"\xff\xfe")

        data = self.output(self.run_hook())

        self.assertIn("ルールを読み込めない", data["systemMessage"])
        self.assertIn("root-local", data["hookSpecificOutput"]["additionalContext"])

    def test_invalid_input_warns_and_exits_zero(self):
        process = self.run_hook(raw="not-json")
        data = self.output(process)

        self.assertIn("hook入力を解析できない", data["systemMessage"])
        self.assertNotIn("hookSpecificOutput", data)

    def test_compact_reinjects_all_loaded_rules(self):
        self.assertTrue(self.context(self.run_hook()))
        compact = self.payload(event="SessionStart", source="compact")

        context = self.context(self.run_hook(compact))

        self.assertIn("root-local", context)
        self.assertIn("src-local", context)
        self.assertEqual(self.run_hook().stdout, "")

    def test_session_start_injects_cwd_rules_without_repeating_them_at_tool_use(self):
        start = self.payload(event="SessionStart", source="startup")

        context = self.context(self.run_hook(start))

        self.assertIn("root-local", context)
        self.assertNotIn("src-local", context)
        self.assertEqual(self.run_hook(start).stdout, "")
        tool_context = self.context(self.run_hook())
        self.assertIn("src-local", tool_context)
        self.assertNotIn("root-local", tool_context)

    def test_subagent_start_reinjects_all_loaded_rules_each_time(self):
        self.assertTrue(self.context(self.run_hook()))
        start = self.payload(event="SubagentStart")

        first = self.context(self.run_hook(start))
        second = self.context(self.run_hook(start))

        self.assertIn("root-local", first)
        self.assertIn("src-local", first)
        self.assertEqual(second, first)

    def test_clear_does_not_delete_session_state(self):
        self.assertTrue(self.context(self.run_hook()))
        clear = self.payload(event="SessionStart", source="clear")
        cleared = self.run_hook(clear)
        self.assertEqual(cleared.returncode, 0, cleared.stderr)
        self.assertEqual(cleared.stdout, "")

        self.assertEqual(self.run_hook().stdout, "")

    def test_session_end_does_not_delete_session_state(self):
        self.assertTrue(self.context(self.run_hook()))
        end = self.payload(event="SessionEnd")
        ended = self.run_hook(end)
        self.assertEqual(ended.returncode, 0, ended.stderr)
        self.assertEqual(ended.stdout, "")

        self.assertEqual(self.run_hook().stdout, "")

    def test_parallel_calls_emit_each_unchanged_rule_once(self):
        raw = json.dumps(self.payload(session="parallel-session"))
        processes = [
            subprocess.Popen(
                hook_command(SCRIPT),
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                cwd=self.repo,
                env=self.environment(),
            )
            for _ in range(6)
        ]
        results = [process.communicate(raw, timeout=30) for process in processes]

        for process, (_, stderr) in zip(processes, results):
            self.assertEqual(process.returncode, 0, stderr)
        outputs = [stdout for stdout, _ in results if stdout.strip()]
        self.assertEqual(len(outputs), 1, outputs)
        self.assertIn("root-local", outputs[0])
        self.assertIn("src-local", outputs[0])


if __name__ == "__main__":
    unittest.main()
