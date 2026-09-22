#!/usr/bin/env python3
"""pr-merge-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）
"""
import json
import os
import subprocess
import time
import unittest

from helpers import HookTestCase, hook_command, load_hook, run_hook, run_hook_argv

SCRIPT = "pr-merge-guard.py"

# ブロック理由に現れるラベル (どのルールが発火したかの判別用)
L_PR_MERGE = "gh pr merge"
L_API = "マージエンドポイント"
L_GRAPHQL = "GraphQL"
L_HTTP = "HTTP クライアント"
L_FETCH = "git fetch origin pull"

# 開錠用の環境変数。実行者のシェルで開錠されたままだとブロック系テストが全滅して
# 気付きにくいため、テストプロセスの環境からは必ず外してから起動する。
UNLOCK_ENV_NAME = "AGENT_ALLOW_PR_MERGE"
UNLOCK_ENV = {UNLOCK_ENV_NAME: "1"}
os.environ.pop(UNLOCK_ENV_NAME, None)


class BlockedTest(HookTestCase):
    """ブロックされるべきコマンド。"""

    script = SCRIPT

    def test_gh_pr_merge(self):
        self.check_table([
            ("gh pr merge 123", L_PR_MERGE),
            ("gh pr merge", L_PR_MERGE),
            ("gh pr merge --squash --delete-branch 42", L_PR_MERGE),
            ("gh pr merge --admin --merge 42", L_PR_MERGE),
            ("rtk gh pr merge 5 --squash", L_PR_MERGE),
            ("GH_TOKEN=xxx gh pr merge 1", L_PR_MERGE),
            ("gh  pr\tmerge  7", L_PR_MERGE),               # 空白・タブの詰まり
            ("cd /tmp && gh pr merge 1", L_PR_MERGE),
            ("gh pr merge 1 2>/dev/null", L_PR_MERGE),
            ("echo start\ngh pr merge 1", L_PR_MERGE),      # 改行区切り
        ], expected="deny")

    def test_gh_pr_merge_shell_separator_boundary(self):
        """区切り文字と語がくっついた形。空白境界だけだと取り逃す。"""
        self.check_table([
            ("git status; gh pr merge 1", L_PR_MERGE),
            ("true;gh pr merge 1", L_PR_MERGE),
            ("(gh pr merge 1)", L_PR_MERGE),
            ("false||gh pr merge 1", L_PR_MERGE),
            ("git status&&gh pr merge 1", L_PR_MERGE),
        ], expected="deny")

    def test_path_prefixed_invocation(self):
        """パス指定での起動。"""
        self.check_table([
            ("/usr/local/bin/gh pr merge 1", L_PR_MERGE),
            ("/opt/homebrew/bin/gh api -X PUT repos/o/r/pulls/1/merge", L_API),
            ("./gh pr merge 1", L_PR_MERGE),
            ("/usr/bin/git fetch origin pull/1/head", L_FETCH),
        ], expected="deny")

    def test_gh_api_merge_endpoint(self):
        """メソッド表記ではなくエンドポイントで判定する。"""
        self.check_table([
            ("gh api -X PUT repos/o/r/pulls/1/merge", L_API),
            ("gh api --method=PUT repos/o/r/pulls/1/merge", L_API),
            ("gh api --method PUT repos/o/r/pulls/1/merge", L_API),
            ("gh api -XPUT repos/o/r/pulls/1/merge", L_API),
            ("gh api -X PUT /repos/o/r/pulls/1/merge", L_API),
            ('gh api "repos/o/r/pulls/1/merge" -f merge_method=squash', L_API),
            ("gh api repos/o/r/pulls/1/merge?x=1", L_API),
            ("gh api repos/o/r/pulls/12345/merge", L_API),
            # ブランチマージ (POST は -f だけで省略できるためメソッドを見ない)
            ("gh api repos/o/r/merges -f base=main -f head=topic", L_API),
            ("gh api -X POST repos/o/r/merges", L_API),
            ("gh api repos/my-org/my.repo/merges", L_API),
            ("gh api /repos/o/r/merges", L_API),
        ], expected="deny")

    def test_graphql_mutation(self):
        """文字列が現れれば内容を問わずブロックする (意図的な過検出)。"""
        self.check_table([
            ("gh api graphql -f query='mutation { mergePullRequest(input: {}) { x } }'", L_GRAPHQL),
            ("gh api graphql -f query='mutation { enablePullRequestAutoMerge(input: {}) { x } }'",
             L_GRAPHQL),
            ("gh api graphql -f query='MERGEPULLREQUEST'", L_GRAPHQL),
            ("gh api graphql -f query='EnablePullRequestAutoMerge'", L_GRAPHQL),
            ("curl -d 'mutation { mergePullRequest }' https://api.github.com/graphql", L_GRAPHQL),
        ], expected="deny")

    def test_http_client(self):
        self.check_table([
            ("curl -X PUT https://api.github.com/repos/o/r/pulls/1/merge", L_HTTP),
            ("curl -XPUT https://api.github.com/repos/o/r/pulls/1/merge", L_HTTP),
            ("curl -X PATCH https://api.github.com/repos/o/r/pulls/1", L_HTTP),
            ("curl -X POST https://api.github.com/repos/o/r/merges", L_HTTP),
            ("curl -s https://api.github.com/repos/o/r/pulls/1/merge", L_HTTP),
            ("wget --method=PUT https://api.github.com/repos/o/r/pulls/1/merge", L_HTTP),
            ("curl --method PUT https://api.github.com/x", L_HTTP),
        ], expected="deny")

    def test_pr_head_fetch(self):
        self.check_table([
            ("git fetch origin pull/123/head", L_FETCH),
            ("git fetch origin pull/123/head:pr-123", L_FETCH),
            ("rtk git fetch origin pull/1/head", L_FETCH),
            # refspec 表記
            ("git fetch origin +refs/pull/1/head:refs/remotes/pr/1", L_FETCH),
            ("git fetch upstream refs/pull/9/head", L_FETCH),
            ("git fetch origin +pull/1/head:pr", L_FETCH),
        ], expected="deny")


class AllowedTest(HookTestCase):
    """通過すべきコマンド。誤爆はこのフックの実害なので厚めに置く。"""

    script = SCRIPT

    def test_unrelated_commands(self):
        self.check_table([
            "ls -la",
            "npm run build",
            "echo legit",              # "legit" に git が含まれる (二次ゲートの語境界)
            "digit=1",
            "echo 'merge the branch'",
            "make merge",
            "python3 -c 'print(1)'",
        ], expected=None)

    def test_read_only_gh(self):
        self.check_table([
            "gh pr view 123",
            "gh pr list --state merged",
            "gh pr diff 123",
            "gh pr checkout 123",
            "gh pr comment 1 --body 'merge later'",
            "gh pr edit 1 --add-label merge",
            "gh pr status",
            "gh auth status",
            "gh workflow run merge.yml",
            "gh release create v1 --notes merged",
            "gh pr list | grep merge",
        ], expected=None)

    def test_gh_pr_merge_lookalikes(self):
        """merge が語の一部にすぎない形。"""
        self.check_table([
            "gh pr mergequeue foo",
            "gh pr merge-queue status",
            "foogh pr merge 1",        # gh が語の途中 (パス起動でもない)
        ], expected=None)

    def test_gh_api_non_merge_endpoints(self):
        self.check_table([
            "gh api repos/o/r/pulls/1",
            "gh api -X PATCH repos/o/r/pulls/comments/12345 -f body=x",
            "gh api -X PATCH repos/o/r/issues/1 -f state=closed",
            "gh api -X PUT repos/o/r/issues/1/labels",
            # merge を語の一部として含むだけのパス
            "gh api repos/o/r/branches/feature/merge-fix/protection",
            "gh api repos/o/r/pulls/1/merge-info",
            "gh api repos/o/r/mergesomething",
            "gh api repos/o/r/merges-report",
            "gh api repos/o/r/pulls/1/mergeable",
            "gh api repos/o/r/commits/abc/pulls",
        ], expected=None)

    def test_git_operations_allowed_by_policy(self):
        """ユーザー判断で日常的に使うもの・ローカル操作は塞がない。"""
        self.check_table([
            "git status",
            "git log --oneline -5",
            "git push --force-with-lease origin HEAD",
            "git push origin :old-branch",
            "git push --force origin feature",
            "git branch -D old",
            "git merge --ff-only origin/main",   # ローカルの取り込みは対象外
            "git merge origin/main",
            "git rebase origin/main",
            "git fetch origin main",
            "git fetch --all --prune",
            "git fetch origin pull-request-123",
            "git fetch origin refs/heads/pull",
        ], expected=None)

    def test_http_client_without_merge(self):
        self.check_table([
            "curl -s https://api.github.com/repos/o/r/pulls/1",
            "curl -s https://api.github.com/repos/o/r/issues",
            "curl -s https://example.com/api",
            "curl -X POST https://example.com/webhook",
            "curl -X PUT https://example.com/upload",
            "wget https://example.com/file.tar.gz",
        ], expected=None)

    def test_primary_gate_limits_scope(self):
        """一次ゲートは git / gh / curl / wget を含まないコマンドを見ない。

        GraphQL の mutation 名だけを含む grep などは、判定に入る前に抜ける。
        """
        self.check_table([
            "grep -rn mergePullRequest .",
            "rg enablePullRequestAutoMerge",
            "echo mergePullRequest",
        ], expected=None)

    def test_documented_out_of_scope(self):
        """明示的な回避は対象外 (docstring に明記)。クォート内は境界に含めない。"""
        self.check_table([
            "echo 'gh pr merge'",
            "echo 'gh pr merge' > note.txt",
            'gh pr create --body "gh pr merge するときは..."',
            "sh -c 'gh pr merge 1'",
            "bash -lc 'gh pr merge 1'",
        ], expected=None)


class NormalizeTest(unittest.TestCase):
    """L2: 表記ゆれの正規化。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def test_method_flag_forms(self):
        cases = {
            "gh api --method=PUT x": "gh api -X PUT x",
            "gh api --method PUT x": "gh api -X PUT x",
            "gh api --method   PUT x": "gh api -X PUT x",
            "gh api --method=PATCH x": "gh api -X PATCH x",
            "curl -XPUT x": "curl -X PUT x",
            "curl -XPOST -XPUT x": "curl -X POST -X PUT x",
            "curl -X PUT x": "curl -X PUT x",
            "curl -Xfoo x": "curl -X foo x",
        }
        for src, want in cases.items():
            with self.subTest(src=src):
                self.assertEqual(self.mod.normalize(src), want)

    def test_newline_becomes_space(self):
        self.assertEqual(self.mod.normalize("a\nb"), "a b")

    def test_similar_flags_untouched(self):
        """--method で始まる別のフラグは書き換えない。"""
        for src in ("gh api --methodology=PUT x", "cmd --methods PUT", "cmd -x PUT"):
            with self.subTest(src=src):
                self.assertEqual(self.mod.normalize(src), src)

    def test_idempotent(self):
        for src in ("gh api --method=PUT x", "curl -XPUT x", "gh pr merge 1"):
            with self.subTest(src=src):
                once = self.mod.normalize(src)
                self.assertEqual(self.mod.normalize(once), once)


class CommandExtractionTest(unittest.TestCase):
    """L2: 入力 JSON からのコマンド取り出し。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def test_valid_payload(self):
        raw = json.dumps({"tool_input": {"command": "gh pr merge 1"}})
        self.assertEqual(self.mod.command_from(raw), "gh pr merge 1")

    def test_missing_pieces(self):
        for raw in ('{"tool_input": {}}', '{"tool_input": null}', '{}'):
            with self.subTest(raw=raw):
                self.assertEqual(self.mod.command_from(raw), "")

    def test_broken_json_falls_back_to_raw(self):
        """パースできない入力は fail-open させず、生のまま判定に回す。"""
        self.assertEqual(self.mod.command_from("gh pr merge 1"), "gh pr merge 1")

    def test_non_dict_json_falls_back_to_raw(self):
        self.assertEqual(self.mod.command_from("[1, 2]"), "[1, 2]")


class InterfaceTest(unittest.TestCase):
    """フックとしての入出力契約。"""

    def test_exit_code_is_zero_even_when_denying(self):
        # run_hook は 0 以外の終了で AssertionError を投げるので、呼べること自体が確認になる
        decision, _ = run_hook(SCRIPT, "gh pr merge 1")
        self.assertEqual(decision, "deny")

    def test_output_schema(self):
        raw = json.dumps({"tool_input": {"command": "gh pr merge 1"}, "cwd": "/tmp"})
        proc = subprocess.run(hook_command(SCRIPT), input=raw, capture_output=True,
                              text=True, timeout=60)
        data = json.loads(proc.stdout)
        self.assertEqual(list(data.keys()), ["hookSpecificOutput"])
        out = data["hookSpecificOutput"]
        self.assertEqual(out["hookEventName"], "PreToolUse")
        self.assertEqual(out["permissionDecision"], "deny")
        self.assertIn("理由:", out["permissionDecisionReason"])
        self.assertIn("対応:", out["permissionDecisionReason"])

    def test_argv_debug_path(self):
        self.assertEqual(run_hook_argv(SCRIPT, "gh pr merge 1")[0], "deny")
        self.assertEqual(run_hook_argv(SCRIPT, "git status")[0], None)

    def test_odd_inputs_do_not_crash(self):
        for raw in ("", "   ", "not json at all", "[]", "null", "0",
                    '{"tool_input": {"command": null}}',
                    '{"tool_input": {"command": 123}}',
                    '{"tool_input": "gh pr merge"}',
                    '{"cwd": "/tmp"}'):
            with self.subTest(raw=raw):
                run_hook(SCRIPT, None, raw=raw)  # 例外なく終了すれば合格

    def test_long_command_is_handled(self):
        padding = "x" * 20000
        self.assertEqual(run_hook(SCRIPT, f"echo {padding}")[0], None)
        self.assertEqual(run_hook(SCRIPT, f"echo {padding} && gh pr merge 1")[0], "deny")

    def test_pathological_input_is_fast(self):
        """病的に長い入力でも正規表現が破綻しない。"""
        cases = [
            "gh api " + "x" * 20000 + " repos/o/r/pulls/1/mergeX",
            "curl " + "-XPUT " * 2000 + "https://example.com/x",
            "gh api " + "--method=PUT " * 2000 + "repos/o/r/pulls/1/x",
            "gh" + " " * 5000 + "pr view 1",
        ]
        start = time.perf_counter()
        for command in cases:
            run_hook(SCRIPT, command)
        self.assertLess(time.perf_counter() - start, 10.0)

    def test_other_tool_payload(self):
        """Bash 以外の payload が来ても落ちない (matcher の設定漏れへの保険)。"""
        raw = json.dumps({"tool_name": "Read", "tool_input": {"file_path": "/tmp/x"}})
        self.assertEqual(run_hook(SCRIPT, None, raw=raw), (None, ""))


class UnlockTest(HookTestCase):
    """AGENT_ALLOW_PR_MERGE=1 で起動したセッションは全ルールを素通りする。"""

    script = SCRIPT

    # 各ルールの代表例。開錠でここが全部通ることを見る。
    BLOCKED = (
        "gh pr merge 123",
        "gh api -X PUT repos/o/r/pulls/1/merge",
        "gh api graphql -f query='mutation { mergePullRequest(input: {}) { clientMutationId } }'",
        "curl -X PUT https://api.github.com/repos/o/r/pulls/1/merge",
        "git fetch origin pull/1/head",
    )

    def test_blocked_by_default(self):
        """開錠なしでは従来どおりブロックする (開錠テストの対照)。"""
        for command in self.BLOCKED:
            with self.subTest(command=command):
                self.assertEqual(run_hook(SCRIPT, command)[0], "deny")

    def test_unlocked_session_passes_everything(self):
        for command in self.BLOCKED:
            with self.subTest(command=command):
                self.assertEqual(run_hook(SCRIPT, command, env=UNLOCK_ENV), (None, ""))

    def test_other_values_do_not_unlock(self):
        """"1" 以外は開錠しない (空文字や 0 の取り違えで開いてしまわないこと)。"""
        for value in ("", "0", "2", "true", "yes", " 1"):
            with self.subTest(value=value):
                decision = run_hook(SCRIPT, "gh pr merge 1",
                                    env={UNLOCK_ENV_NAME: value})[0]
                self.assertEqual(decision, "deny")


if __name__ == "__main__":
    unittest.main()
