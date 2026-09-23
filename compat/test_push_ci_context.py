#!/usr/bin/env python3
"""push-ci-context.py のテスト。

実行: make compat-test（compat/README.md を参照）

観点:
  L1 契約 … stdin の JSON で起動し、注入の有無と出力スキーマを見る。
  L2 単体 … triggers_ci / succeeded を直接呼び、誤爆と取りこぼしを表で確認する。
このフックの実害は誤爆 (無関係な Bash で毎回 CI の案内が出る) なので、通過ケースを厚く保つ。
"""
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import git, hook_command, load_hook

SCRIPT = "push-ci-context.py"
GATE_SEED = "git push"


def run_post_tool_use(command, *, response=None, cwd=None, raw=None,
                      event="PostToolUse", agent="claude"):
    """フックを実運用と同じ経路で起動する。

    戻り値: (additionalContext または None, CompletedProcess)
    """
    if raw is None:
        payload = {
            "session_id": "test-session",
            "transcript_path": ("/tmp/agent/sessions/rollout-2026-09-19T00-00-00-test-session.jsonl"
                                if agent == "codex" else "/tmp/agent/projects/test-session.jsonl"),
            "hook_event_name": event,
            "tool_name": "Bash",
            "tool_input": {"command": command},
            "cwd": cwd or "",
        }
        if event == "PostToolUse":
            payload["tool_response"] = {} if response is None else response
        raw = json.dumps(payload)
    proc = subprocess.run(hook_command(SCRIPT), input=raw, capture_output=True,
                          text=True, timeout=120)
    if proc.returncode != 0:
        raise AssertionError(
            f"{SCRIPT} が 0 以外で終了しました (rc={proc.returncode})\n"
            f"stdout: {proc.stdout!r}\nstderr: {proc.stderr!r}")
    if not proc.stdout.strip():
        return None, proc
    payload = json.loads(proc.stdout)
    output = payload["hookSpecificOutput"]
    assert output["hookEventName"] == event, output
    return output["additionalContext"], proc


def make_repo(path, origin="https://github.com/owner/repo.git"):
    path = Path(path)
    path.mkdir(parents=True, exist_ok=True)
    git(path, "init", "-q")
    if origin:
        git(path, "remote", "add", "origin", origin)
    return str(path)


class ContractTest(unittest.TestCase):
    """実運用と同じ経路での注入判定。"""

    @classmethod
    def setUpClass(cls):
        cls._temporary = tempfile.TemporaryDirectory(prefix="push-ci-context-")
        root = Path(cls._temporary.name)
        cls.github_repo = make_repo(root / "github")
        cls.other_repo = make_repo(root / "gitlab", "git@gitlab.com:owner/repo.git")
        cls.not_a_repo = str(root / "plain")
        Path(cls.not_a_repo).mkdir()

    @classmethod
    def tearDownClass(cls):
        cls._temporary.cleanup()

    def test_push_injects_guidance(self):
        context, _ = run_post_tool_use("git push origin HEAD", cwd=self.github_repo)
        self.assertIsNotNone(context)
        self.assertIn("wait-ci", context)
        self.assertIn("timeout:600000", context)
        self.assertNotIn("functions.exec", context)
        self.assertNotIn("write_stdin", context)

    def test_codex_injects_wait_loop_only_for_its_transcript(self):
        context, _ = run_post_tool_use(
            "git push origin HEAD", cwd=self.github_repo, agent="codex")
        self.assertIn('"yield_time_ms":3600000', context)
        self.assertIn("write_stdin", context)
        self.assertIn("exit_code", context)
        self.assertNotIn("timeout:600000", context)

    def test_claude_does_not_inherit_codex_instructions(self):
        from unittest.mock import patch
        with patch.dict(os.environ, {"CODEX_THREAD_ID": "test-session"}):
            context, _ = run_post_tool_use(
                "git push origin HEAD", cwd=self.github_repo)
        self.assertNotIn("functions.exec", context)
        self.assertNotIn("60秒", context)

    def test_wait_is_ordered_after_remaining_work(self):
        """待機が他の残作業（PR 本文更新・別 PR の改修）を押しのけないよう案内する。"""

        cases = (
            ("git push origin HEAD", {}),
            ("gh workflow run deploy.yml --ref main",
             {"stdout": "https://github.com/owner/repo/actions/runs/123456789"}),
        )
        for command, response in cases:
            with self.subTest(command=command):
                context, _ = run_post_tool_use(
                    command, cwd=self.github_repo, response=response or None)
                self.assertIn("待機はこのターンの最後に回す", context)
                self.assertIn("PR 本文の更新", context)

    def test_pr_create_injects_guidance(self):
        context, _ = run_post_tool_use(
            "gh pr create --fill --draft", cwd=self.github_repo)
        self.assertIsNotNone(context)

    def test_workflow_dispatch_injects_codex_long_wait_guidance(self):
        context, _ = run_post_tool_use(
            "gh workflow run deploy.yml --ref main", cwd=self.github_repo, agent="codex",
            response={"stdout": "https://github.com/owner/repo/actions/runs/123456789"})
        self.assertIn("workflow dispatch が成功", context)
        self.assertIn("gh run watch 123456789 --exit-status", context)
        self.assertIn('"yield_time_ms":3600000', context)
        self.assertIn("write_stdin", context)
        self.assertIn("30秒ごとにモデルへ制御を戻さない", context)
        self.assertNotIn("wait-ci --progress", context)

    def test_workflow_dispatch_with_repo_flag_works_outside_repository(self):
        context, _ = run_post_tool_use(
            "gh workflow run deploy.yml -R owner/repo", cwd=self.not_a_repo, agent="codex",
            response={"stdout": "https://github.com/owner/repo/actions/runs/987654321"})
        self.assertIsNotNone(context)
        self.assertIn("gh run watch 987654321 --exit-status -R owner/repo", context)

    def test_workflow_dispatch_without_url_polls_inside_one_wait_command(self):
        context, _ = run_post_tool_use(
            "gh workflow run deploy.yml --ref main -R owner/repo",
            cwd=self.not_a_repo, agent="codex", response={"stdout": "✓ Created"})
        self.assertIn("gh run list --event workflow_dispatch", context)
        self.assertIn("--workflow deploy.yml", context)
        self.assertIn("--branch main", context)
        self.assertIn("sleep 30", context)
        self.assertIn("候補が複数あり特定できない", context)
        self.assertIn("90分でタイムアウト", context)
        self.assertIn("list_rc", context)
        self.assertIn("gh run watch", context)
        self.assertIn("-R owner/repo", context)
        self.assertEqual(context.count("tools.exec_command"), 1)

    def test_claude_receives_the_same_dispatch_wait_command(self):
        context, _ = run_post_tool_use(
            "gh workflow run deploy.yml --ref main", cwd=self.github_repo,
            response={"stdout": "Created without a URL"})
        self.assertIn("gh run list --event workflow_dispatch", context)
        self.assertIn("sleep 30", context)
        self.assertIn("timeout:600000", context)

    def test_failed_workflow_dispatch_is_silent(self):
        context, _ = run_post_tool_use(
            "gh workflow run deploy.yml",
            response={"exit_code": 1, "stderr": "HTTP 422"},
            cwd=self.github_repo)
        self.assertIsNone(context)

    def test_failed_push_is_silent(self):
        context, _ = run_post_tool_use(
            "git push origin HEAD",
            response={"stderr": "! [rejected]        main -> main (non-fast-forward)"},
            cwd=self.github_repo)
        self.assertIsNone(context)

    def test_nonzero_exit_code_is_silent(self):
        context, _ = run_post_tool_use(
            "git push origin HEAD",
            response={"exit_code": 1, "stdout": ""},
            cwd=self.github_repo)
        self.assertIsNone(context)

    def test_up_to_date_push_is_silent(self):
        context, _ = run_post_tool_use(
            "git push",
            response={"stderr": "Everything up-to-date"},
            cwd=self.github_repo)
        self.assertIsNone(context)

    def test_non_github_remote_is_silent(self):
        context, _ = run_post_tool_use("git push origin HEAD", cwd=self.other_repo)
        self.assertIsNone(context)

    def test_github_push_output_beats_a_non_github_cwd(self):
        """実際の通信先が GitHub なら、cwd の origin より結果を優先する。"""

        context, _ = run_post_tool_use(
            "git push origin HEAD",
            cwd=self.other_repo,
            response={"exit_code": 0, "stderr": "To github.com:owner/repo.git"})
        self.assertIsNotNone(context)
        self.assertIn("wait-ci", context)

    def test_push_from_a_workspace_root_injects(self):
        """Codex は exec_command の workdir を渡さず、cwd はリポジトリ外のままになる。

        `~/dev/<workspace>` のようなワークスペースルートで開始したセッションからの push で、
        以前は cwd だけを見て抑止していた。結果が GitHub を指していれば注入する。
        """

        context, _ = run_post_tool_use(
            "git status --short --branch && git push -u origin fix/x",
            cwd=self.not_a_repo,
            response={"exit_code": 0, "output": (
                "## fix/x\nTo github.com:owner/repo\n"
                " * [new branch]      fix/x -> fix/x\n")})
        self.assertIsNotNone(context)
        self.assertIn("wait-ci", context)

    def test_undeterminable_repository_injects(self):
        """GitHub 以外だと確認できない限り抑止しない。待ち損ねる方が損失が大きい。"""

        context, _ = run_post_tool_use("git push origin HEAD", cwd=self.not_a_repo)
        self.assertIsNotNone(context)
        self.assertIn("wait-ci", context)

    def test_unrelated_command_is_silent(self):
        for command in ("ls -la", "npm run push-notifications", "git status"):
            with self.subTest(command=command):
                context, _ = run_post_tool_use(command, cwd=self.github_repo)
                self.assertIsNone(context)

    def test_pre_tool_use_injects_before_the_push(self):
        """Codex 用の経路。実行前なので tool_response を見ずに案内する。"""

        context, _ = run_post_tool_use(
            "git push origin HEAD", cwd=self.github_repo, event="PreToolUse")
        self.assertIsNotNone(context)
        self.assertIn("wait-ci", context)

    def test_pre_tool_use_still_skips_non_push_commands(self):
        context, _ = run_post_tool_use(
            "git push --dry-run", cwd=self.github_repo, event="PreToolUse")
        self.assertIsNone(context)

    def test_unknown_event_is_silent(self):
        context, _ = run_post_tool_use(
            "git push origin HEAD", cwd=self.github_repo, event="UserPromptSubmit")
        self.assertIsNone(context)

    def test_broken_payload_does_not_fail(self):
        _, proc = run_post_tool_use("", raw="not json but mentions push")
        self.assertEqual(proc.returncode, 0)


class TriggerTest(unittest.TestCase):
    """L2: どのコマンドを CI 起点とみなすか。"""

    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, seed=GATE_SEED)

    def check(self, commands, expected):
        for command in commands:
            with self.subTest(command=command):
                self.assertEqual(self.hook.triggers_ci(command), expected)

    def test_pushes(self):
        self.check([
            "git push",
            "git push origin HEAD",
            "git push --force-with-lease origin feature",
            "git push origin 61c6a6b:refs/heads/feature",
            "git -C /other/worktree push origin HEAD",
            "/usr/bin/git push",
            "make build && git push",
            "git commit -m 'x'; git push",
            "gh pr create --fill",
            "gh pr create --draft --title x --body y",
            "gh workflow run deploy.yml",
            "gh workflow run deploy.yml --ref main -f target=staging",
            "gh workflow run deploy.yml -f 'message=a;b'",
            "/usr/local/bin/gh workflow run deploy.yml -R owner/repo",
            "gh workflow   run deploy.yml",
        ], True)

    def test_not_pushes(self):
        self.check([
            "git push --dry-run origin HEAD",
            "git push -n origin HEAD",
            "git push origin --delete feature",
            "git push origin :feature",
            "git status",
            "git fetch origin",
            "gh pr view 155",
            "gh pr checks 155",
            "gh workflow run --help",
            "gh workflow view deploy.yml",
            "gh run list --workflow deploy.yml",
            "echo 'gh workflow run deploy.yml'",
            "rg 'gh workflow run' .",
            "echo push",
            "npm run push-image",
        ], False)


class AgentDetectionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, GATE_SEED)

    def test_unknown_or_mismatched_transcript_is_not_codex(self):
        for payload in ({}, {"session_id": "id", "transcript_path": None},
                        {"session_id": "id", "transcript_path": "/tmp/rollout-date-other.jsonl"},
                        {"session_id": "", "transcript_path": "/tmp/rollout-date-.jsonl"}):
            with self.subTest(payload=payload):
                self.assertFalse(self.hook.is_codex(payload))


class WorkflowWatchCommandTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, GATE_SEED)

    def run_with_fake_gh(self, fake_body):
        with tempfile.TemporaryDirectory(prefix="workflow-watch-") as temporary:
            fake_gh = Path(temporary) / "gh"
            fake_gh.write_text("#!/bin/sh\n" + fake_body, encoding="utf-8")
            fake_gh.chmod(0o755)
            command = self.hook.workflow_watch_command(
                "gh workflow run deploy.yml --ref main", {"stdout": "Created"})
            env = os.environ.copy()
            env["PATH"] = temporary + os.pathsep + env.get("PATH", "")
            return subprocess.run(
                ["bash", "-c", command], capture_output=True, text=True,
                timeout=5, env=env)

    def test_multiple_candidates_stop_instead_of_watching_the_first(self):
        result = self.run_with_fake_gh("printf '111\\n222\\n'\n")
        self.assertEqual(result.returncode, 2)
        self.assertIn("候補が複数あり特定できない", result.stderr)

    def test_run_list_failure_is_propagated(self):
        result = self.run_with_fake_gh("exit 7\n")
        self.assertEqual(result.returncode, 7)


class SuccessTest(unittest.TestCase):
    """L2: 成功と判断する条件。判断材料が無ければ成功として扱う。"""

    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, seed=GATE_SEED)

    def test_success(self):
        for response in (
            {},
            None,
            {"stdout": "", "stderr": " * [new branch]      feature -> feature"},
            {"exit_code": 0, "stderr": "To github.com:owner/repo.git"},
        ):
            with self.subTest(response=response):
                self.assertTrue(self.hook.succeeded(response))

    def test_failure(self):
        for response in (
            {"exitCode": 128},
            {"success": False},
            {"interrupted": True},
            {"stderr": "fatal: repository not found"},
            {"aggregated_output": "error: failed to push some refs"},
            {"stdout": "Everything up to date"},
        ):
            with self.subTest(response=response):
                self.assertFalse(self.hook.succeeded(response))


if __name__ == "__main__":
    unittest.main()
