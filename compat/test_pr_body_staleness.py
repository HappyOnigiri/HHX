#!/usr/bin/env python3
"""pr-body-staleness.py のテスト。

実行: make compat-test（compat/README.md を参照）

観点:
  L1 契約 … gh スタブ + 本物の git リポジトリでフックを起動し、注入の有無を見る。
  L2 単体 … triggers_push / stale_commits / parse_time を直接呼ぶ。
このフックの実害は誤爆 (本文が最新なのに毎回「更新しろ」と出る) なので、無出力ケースを
厚く保つ。gh は fake_gh.py で差し替え、ネットワークにも実 PR にも触らせない。
"""
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from helpers import FAKE_GH, GIT_ISOLATION, git, hook_command, load_hook

SCRIPT = "pr-body-staleness.py"
GATE_SEED = "git push"

WRITTEN = "2026-09-10T07:10:57Z"
BEFORE = "2026-09-10T07:06:57Z"
AFTER = "2026-09-10T07:36:15Z"


def commit(date, headline="feat: 交換のメモを後から入力できるようにする", parents=1):
    return {"commit": {"committedDate": date, "messageHeadline": headline,
                       "parents": {"totalCount": parents}}}


def pull_request(**over):
    data = {
        "number": 10088,
        "url": "https://github.com/o/r/pull/10088",
        "state": "OPEN",
        "lastEditedAt": WRITTEN,
        "createdAt": BEFORE,
        "commits": {"nodes": [commit(BEFORE), commit(AFTER)]},
    }
    data.update(over)
    return data


def graphql(*nodes):
    return {"data": {"repository": {"object": {
        "associatedPullRequests": {"nodes": list(nodes)}}}}}


class ContractTest(unittest.TestCase):
    """L1: 実運用と同じ経路 (stdin の JSON) での注入判定。"""

    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="pr-body-staleness-")
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.fixtures = os.path.join(self.tmp, "gh")
        os.makedirs(self.fixtures)
        stub = os.path.join(self.fixtures, "gh")
        shutil.copy(FAKE_GH, stub)
        os.chmod(stub, 0o755)
        self.mode = "ok"
        self.github_repo = self.make_repo("github")
        self.other_repo = self.make_repo("gitlab", "git@gitlab.com:owner/repo.git")
        self.not_a_repo = os.path.join(self.tmp, "plain")
        os.makedirs(self.not_a_repo)
        self.set_graphql(graphql(pull_request()))

    # -- フィクスチャ操作 --

    def make_repo(self, name, origin="https://github.com/o/r.git"):
        """commit を 1 つ持つリポジトリ (フックが HEAD の SHA を引くため)。"""

        path = Path(self.tmp, name)
        path.mkdir(parents=True, exist_ok=True)
        git(path, "init", "-q")
        git(path, "remote", "add", "origin", origin)
        (path / "tracked.txt").write_text("v1\n", encoding="utf-8")
        git(path, "add", "tracked.txt")
        git(path, "commit", "-q", "-m", "init")
        return str(path)

    def set_graphql(self, data):
        Path(self.fixtures, "graphql.json").write_text(json.dumps(data),
                                                       encoding="utf-8")

    def calls(self):
        log = Path(self.fixtures, "calls.log")
        if not log.exists():
            return []
        return [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()]

    # -- 起動 --

    def env(self, **over):
        env = {
            "PATH": self.fixtures + os.pathsep + os.environ["PATH"],
            "FAKE_GH_DIR": self.fixtures,
            "FAKE_GH_MODE": self.mode,
            **GIT_ISOLATION,
        }
        env.update(over)
        return env

    def run_hook(self, command="git push origin HEAD", *, response=None, cwd=None,
                 raw=None, event="PostToolUse", **env_over):
        """戻り値: (additionalContext または None, CompletedProcess)"""

        if raw is None:
            payload = {
                "session_id": "test-session",
                "transcript_path": "/dev/null",
                "hook_event_name": event,
                "tool_name": "Bash",
                "tool_input": {"command": command},
                "cwd": self.github_repo if cwd is None else cwd,
            }
            if event == "PostToolUse":
                payload["tool_response"] = {} if response is None else response
            raw = json.dumps(payload)
        proc = subprocess.run(hook_command(SCRIPT), input=raw, capture_output=True,
                              text=True, timeout=120,
                              env={**os.environ, **self.env(**env_over)})
        self.assertEqual(proc.returncode, 0,
                         "フックが %d で終了しました\nstderr: %s"
                         % (proc.returncode, proc.stderr))
        if not proc.stdout.strip():
            return None, proc
        output = json.loads(proc.stdout)["hookSpecificOutput"]
        self.assertEqual(output["hookEventName"], event, output)
        return output["additionalContext"], proc

    # -- 注入する --

    def test_commit_after_the_body_injects_a_warning(self):
        context, _ = self.run_hook()
        self.assertIsNotNone(context)
        self.assertIn("#10088", context)
        self.assertIn("update-pr", context)
        self.assertIn("交換のメモ", context)

    def test_body_never_edited_falls_back_to_created_at(self):
        self.set_graphql(graphql(pull_request(lastEditedAt=None, createdAt=BEFORE)))
        context, _ = self.run_hook()
        self.assertIsNotNone(context)

    def test_only_the_newer_commits_are_listed(self):
        self.set_graphql(graphql(pull_request(commits={"nodes": [
            commit(BEFORE, "fix: 本文に書いてある修正"),
            commit(AFTER, "feat: 本文より後のコミット"),
        ]})))
        context, _ = self.run_hook()
        self.assertIn("本文より後のコミット", context)
        self.assertNotIn("本文に書いてある修正", context)

    # -- 注入しない --

    def test_body_newer_than_every_commit_is_silent(self):
        self.set_graphql(graphql(pull_request(
            commits={"nodes": [commit(BEFORE)]})))
        context, _ = self.run_hook()
        self.assertIsNone(context)

    def test_merge_commit_alone_is_silent(self):
        """base の取り込みは PR の説明を変えないので数えない。"""

        self.set_graphql(graphql(pull_request(commits={"nodes": [
            commit(BEFORE),
            commit(AFTER, "Merge remote-tracking branch 'origin/main'", parents=2),
        ]})))
        context, _ = self.run_hook()
        self.assertIsNone(context)

    def test_no_associated_pull_request_is_silent(self):
        self.set_graphql(graphql())
        context, _ = self.run_hook()
        self.assertIsNone(context)

    def test_closed_pull_request_is_silent(self):
        self.set_graphql(graphql(pull_request(state="MERGED")))
        context, _ = self.run_hook()
        self.assertIsNone(context)

    def test_failed_push_is_silent(self):
        context, _ = self.run_hook(
            response={"stderr": "! [rejected]        main -> main (non-fast-forward)"})
        self.assertIsNone(context)
        self.assertEqual(self.calls(), [], "失敗した push で gh を呼んでいます")

    def test_up_to_date_push_is_silent(self):
        context, _ = self.run_hook(response={"stderr": "Everything up-to-date"})
        self.assertIsNone(context)
        self.assertEqual(self.calls(), [])

    def test_pr_create_is_silent(self):
        """作りたての本文は差分と同時に書かれている。"""

        context, _ = self.run_hook("gh pr create --fill")
        self.assertIsNone(context)

    def test_unrelated_commands_are_silent(self):
        for command in ("ls -la", "npm run push-notifications", "git status",
                        "git push --dry-run", "git push origin :feature"):
            with self.subTest(command=command):
                context, _ = self.run_hook(command)
                self.assertIsNone(context)

    def test_non_github_remote_is_silent(self):
        context, _ = self.run_hook(cwd=self.other_repo)
        self.assertIsNone(context)
        self.assertEqual(self.calls(), [], "GitHub 以外で gh を呼んでいます")

    def test_outside_a_repository_is_silent(self):
        context, _ = self.run_hook(cwd=self.not_a_repo)
        self.assertIsNone(context)

    def test_gh_failure_is_silent(self):
        self.mode = "fail"
        context, _ = self.run_hook()
        self.assertIsNone(context)

    def test_gh_garbage_is_silent(self):
        self.mode = "garbage"
        context, _ = self.run_hook()
        self.assertIsNone(context)

    def test_gh_hang_does_not_block_forever(self):
        """gh が返らなくてもフックは自前の timeout で無出力終了する。"""

        self.mode = "hang"
        context, _ = self.run_hook(FAKE_GH_SLEEP="60")
        self.assertIsNone(context)

    def test_pre_tool_use_is_silent(self):
        """push の前に本文の新旧は判定できない。"""

        context, _ = self.run_hook(event="PreToolUse")
        self.assertIsNone(context)

    def test_broken_payload_does_not_fail(self):
        _, proc = self.run_hook(raw="not json but mentions push")
        self.assertEqual(proc.returncode, 0)


class TriggerTest(unittest.TestCase):
    """L2: どのコマンドを対象にするか。"""

    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, seed=GATE_SEED)

    def check(self, commands, expected):
        for command in commands:
            with self.subTest(command=command):
                self.assertEqual(self.hook.triggers_push(command), expected)

    def test_pushes(self):
        self.check([
            "git push",
            "git push origin HEAD",
            "git push --force-with-lease origin feature",
            "git push origin HEAD:refs/heads/feature",
            "git -C /other/worktree push origin HEAD",
            "/usr/bin/git push",
            "make build && git push",
            "git commit -m 'x'; git push",
        ], True)

    def test_not_pushes(self):
        self.check([
            "git push --dry-run origin HEAD",
            "git push -n origin HEAD",
            "git push origin --delete feature",
            "git push origin :feature",
            "gh pr create --fill",
            "git status",
            "echo push",
            "npm run push-image",
        ], False)


class StalenessTest(unittest.TestCase):
    """L2: 本文より後の commit の数え方。"""

    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, seed=GATE_SEED)

    def test_newest_first(self):
        newer = self.hook.stale_commits(pull_request(commits={"nodes": [
            commit(BEFORE, "古い"),
            commit("2026-09-10T07:20:00Z", "中間"),
            commit(AFTER, "最新"),
        ]}))
        self.assertEqual(newer, ["最新", "中間"])

    def test_ignores_merge_commits(self):
        newer = self.hook.stale_commits(pull_request(commits={"nodes": [
            commit(AFTER, "マージ", parents=2),
        ]}))
        self.assertEqual(newer, [])

    def test_unreadable_timestamps_are_ignored(self):
        self.assertEqual(self.hook.stale_commits(pull_request(
            lastEditedAt="", createdAt="")), [])
        self.assertEqual(self.hook.stale_commits(pull_request(commits={"nodes": [
            commit("", "壊れた日付")]})), [])

    def test_parse_time(self):
        self.assertIsNotNone(self.hook.parse_time("2026-09-10T07:10:57Z"))
        self.assertIsNotNone(self.hook.parse_time("2026-09-10T07:10:57.500Z"))
        for value in (None, "", "yesterday", 17):
            with self.subTest(value=value):
                self.assertIsNone(self.hook.parse_time(value))


class OriginTest(unittest.TestCase):
    """L2: origin URL から owner/repo を取り出す。"""

    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, seed=GATE_SEED)

    def test_matches(self):
        for url in ("https://github.com/o/r.git", "https://github.com/o/r",
                    "git@github.com:o/r.git", "ssh://git@github.com/o/r.git",
                    "https://github.com/o/r/"):
            with self.subTest(url=url):
                matched = self.hook.ORIGIN_PATTERN.search(url)
                self.assertIsNotNone(matched, url)
                self.assertEqual(matched.group("owner", "repo"), ("o", "r"))

    def test_non_matches(self):
        for url in ("git@gitlab.com:o/r.git", "https://notgithub.com/o/r.git",
                    "/local/path/repo"):
            with self.subTest(url=url):
                self.assertIsNone(self.hook.ORIGIN_PATTERN.search(url))


if __name__ == "__main__":
    unittest.main()
