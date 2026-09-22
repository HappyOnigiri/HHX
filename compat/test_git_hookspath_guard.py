#!/usr/bin/env python3
"""git-hookspath-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）
"""
import json
import time
import unittest

from helpers import HookTestCase, load_hook, run_hook, run_hook_argv

SCRIPT = "git-hookspath-guard.py"
KEY = "core.hooksPath"  # 本体は大文字小文字を区別しない


def file_tool_payload(tool_name, file_path):
    """Edit / Write の PreToolUse payload を作る。"""
    return json.dumps({
        "session_id": "test-session",
        "transcript_path": "/dev/null",
        "hook_event_name": "PreToolUse",
        "tool_name": tool_name,
        "tool_input": {"file_path": file_path, "content": "[core]\n\tbare = false\n"},
        "cwd": "/tmp/repo",
    })


class BlockedTest(HookTestCase):
    """書き込みはブロックする。"""

    script = SCRIPT

    def test_config_with_value(self):
        self.check_table([
            f"git config {KEY} .githooks",
            f"git config --local {KEY} .githooks",
            f"git config --global {KEY} ~/.config/git/hooks",
            f"git config --system {KEY} /etc/githooks",
            f"git config -f .git/config {KEY} hooks",
            f"git config --file /tmp/cfg {KEY} hooks",
            f"git config {KEY} `pwd`/hooks",
            f"git config {KEY} $(pwd)/hooks",
            f'git config {KEY} ""',
            f"git config {KEY} ''",
            f"/usr/bin/git config {KEY} hooks",
            f"git -C /other config {KEY} hooks",
            f"cd repo && git config {KEY} ../hooks",
            f"git config {KEY} hooks && npm test",
            f"true; git config {KEY} hooks",
            f"echo x | git config {KEY} hooks",
            f"git config --get {KEY} || git config {KEY} .githooks",
        ], expected="deny")

    def test_key_operations(self):
        self.check_table([
            f"git config --unset {KEY}",
            f"git config --unset-all {KEY}",
            f"git config --add {KEY} hooks",
            f"git config --replace-all {KEY} hooks",
            f"git config --global --unset {KEY}",
        ], expected="deny")

    def test_oneshot_override(self):
        self.check_table([
            f"git -c {KEY}=/dev/null commit -m x",
            f"git -c {KEY}= commit -m x",
            f"git -c {KEY}=.githooks push",
            f"git -c user.name=x -c {KEY}=y commit -m z",
        ], expected="deny")

    def test_case_insensitive(self):
        """git の設定キーは大文字小文字を区別しないので、表記ゆれも塞ぐ。"""
        self.check_table([
            "git config CORE.HOOKSPATH hooks",
            "git config Core.HooksPath hooks",
            "git config core.hookspath hooks",
            "git config cOrE.hOoKsPaTh hooks",
            "git -c CORE.HOOKSPATH=x commit -m y",
        ], expected="deny")

    def test_newline_separated_segments(self):
        """改行もコマンド区切り。読み取りと書き込みが混在しても書き込みを拾う。"""
        self.check_table([
            f"git config --get {KEY}\ngit config {KEY} hooks",
            f"git config {KEY} hooks\ngit config --get {KEY}",   # 書き込みが先
            f"echo start\ngit config --unset {KEY}\necho done",
        ], expected="deny")

    def test_env_var_assignment(self):
        """GIT_CONFIG_KEY_n 経由の設定は git config を通らない。"""
        self.check_table([
            f"GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0={KEY} GIT_CONFIG_VALUE_0=/tmp/h git commit -m x",
            f"GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_1={KEY} GIT_CONFIG_VALUE_1=x git status",
        ], expected="deny")

    def test_direct_git_config_writes_from_bash(self):
        """明白なファイル書き換えは git config を経由しなくても止める。"""
        self.check_table([
            "sed -i '' 's/bare = false/bare = true/' .git/config",
            "perl -pi -e 's/x/y/' /tmp/repo/.git/config",
            "printf '[core]\\n' > .git/config",
            "cat prepared-config >> \"/tmp/repo/.git/config\"",
            "generate-config | tee .git/config",
            "truncate -s 0 .git/config",
            "cp prepared-config .git/config",
            "mv prepared-config /tmp/repo/.git/config",
            "rm .git/config",
            "dd if=prepared-config of=.git/config",
            "sed -i '' 's/x/y/' /tmp/repo/.git/worktrees/task/config.worktree",
            "cp prepared-config /tmp/repository.git/config",
        ], expected="deny")


class FileToolBlockedTest(unittest.TestCase):
    """Edit / Write は Git 設定ファイルへの直接編集をブロックする。"""

    def test_repository_config_paths(self):
        paths = (
            ".git/config",
            "/tmp/repo/.git/config",
            "/tmp/repo/.git/config.worktree",
            "/tmp/repo/.git/worktrees/task/config.worktree",
            "/tmp/repo/.git/modules/child/config",
            "/tmp/repository.git/config",
            r"C:\repo\.git\config",
        )
        for tool_name in ("Edit", "Write"):
            for path in paths:
                with self.subTest(tool_name=tool_name, path=path):
                    decision, reason = run_hook(
                        SCRIPT, None, raw=file_tool_payload(tool_name, path))
                    self.assertEqual(decision, "deny", reason)
                    self.assertIn("git config <key> <value>", reason)


class AllowedTest(HookTestCase):
    """読み取りはすべて許可する。"""

    script = SCRIPT

    def test_reads(self):
        self.check_table([
            f"git config {KEY}",
            f"git config --get {KEY}",
            f"git config --get-all {KEY}",
            f"git config --local --get {KEY}",
            f"git config --get {KEY} 2>/dev/null",
            f"git config --get {KEY} > /tmp/out",
            f"git config --get {KEY} >&2",
            f"git config --get {KEY} && echo ok",
            f"git config --get {KEY}; echo done",
            f"git config --get {KEY} | tr -d '\\n'",
            f"git config --list | grep {KEY}",
            f"git config --list --show-origin | grep {KEY}",
            f"git config --get {KEY} --show-origin",
            f'test -n "$(git config {KEY})" && echo set',
            f"[ -z \"$(git config --get {KEY})\" ] || echo set",
        ], expected=None)

    def test_no_git_involved(self):
        self.check_table([
            f"echo {KEY}",
            f"grep -r {KEY} .",
            f"rg '{KEY}' ~/.claude",
            f"cat .git/config | grep -i {KEY.lower()}",
            f"python3 -c 'print(\"{KEY}\")'",
            f"echo 'set {KEY} manually'",
            f"sed -i '' 's/{KEY}//' notes.md",
        ], expected=None)

    def test_git_without_config_write(self):
        self.check_table([
            f"git log --grep {KEY}",
            f"git grep {KEY}",
            "git config --get-regexp '^core\\.'",
            "git status",
            "git config --list",
            "ls -la",
        ], expected=None)

    def test_repo_local_hooks_are_fine(self):
        """リポジトリ固有の hook を .git/hooks に置く運用は塞がない。"""
        self.check_table([
            "cp scripts/pre-commit .git/hooks/pre-commit",
            "chmod +x .git/hooks/pre-push",
            "ls .git/hooks",
        ], expected=None)

    def test_direct_git_config_reads_are_fine(self):
        self.check_table([
            "cat .git/config",
            "cat .git/config > /tmp/config-copy",
            "grep hooksPath /tmp/repo/.git/config",
            "sed -n '1,20p' .git/config",
            "git diff -- .git/config",
            "cp .git/config /tmp/config-backup",
        ], expected=None)


class FileToolAllowedTest(unittest.TestCase):
    """対象外のツールと似た名前のファイルは通す。"""

    def test_non_config_paths_and_non_writing_tools(self):
        cases = (
            ("Edit", "/tmp/repo/git-config-notes.md"),
            ("Write", "/tmp/repo/.git/config.example"),
            ("Write", "/tmp/repo/config"),
            ("Read", "/tmp/repo/.git/config"),
            ("apply_patch", "/tmp/repo/.git/config"),
        )
        for tool_name, path in cases:
            with self.subTest(tool_name=tool_name, path=path):
                self.assertEqual(
                    run_hook(SCRIPT, None, raw=file_tool_payload(tool_name, path))[0], None)


class IsWriteTest(unittest.TestCase):
    """L2: セグメント単位の判定。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def check(self, segment, expected):
        self.assertEqual(self.mod.is_write(segment), expected, f"segment={segment!r}")

    def test_write_forms(self):
        for segment in (f"git config {KEY} hooks",
                        f"git config --unset {KEY}",
                        f"git -c {KEY}=x commit",
                        f'git config {KEY} ""',
                        f"GIT_CONFIG_KEY_0={KEY} git commit",
                        f"GIT_CONFIG_KEY_10={KEY} git commit"):
            with self.subTest(segment=segment):
                self.check(segment, True)

    def test_read_forms(self):
        for segment in (f"git config {KEY}",
                        f"git config --get {KEY}",
                        f"git config {KEY} 2>/dev/null",
                        f"git config {KEY} >out",
                        f"git config --get {KEY} --show-origin",
                        f"echo {KEY} hooks",              # git なし
                        f"cat {KEY}",
                        "git status",                      # キーなし (早期 false)
                        f"GIT_CONFIG_KEY_0={KEY}x git commit"):
            with self.subTest(segment=segment):
                self.check(segment, False)

    def test_redirect_stripping(self):
        """リダイレクトは対象ごと除去される (値と誤認しない)。"""
        for segment in (f"git config {KEY} 2>/dev/null",
                        f"git config {KEY} >/tmp/a",
                        f"git config {KEY} >>/tmp/a",
                        f"git config {KEY} 1>&2"):
            with self.subTest(segment=segment):
                self.check(segment, False)

    def test_git_config_path_detection(self):
        for path in (".git/config", "/repo/.git/config.worktree",
                     "/repo/.git/worktrees/wt/config.worktree",
                     "/repo/.git/modules/sub/config", "/repo.git/config"):
            with self.subTest(path=path):
                self.assertTrue(self.mod.is_git_config_path(path))
        for path in (".git/config.example", "/tmp/config", "/repo/git/config"):
            with self.subTest(path=path):
                self.assertFalse(self.mod.is_git_config_path(path))

    def test_direct_write_detection(self):
        for segment in ("echo x > .git/config", "sed -i s/x/y/ .git/config",
                        "tee /repo/.git/config", "cp x repo.git/config"):
            with self.subTest(segment=segment):
                self.assertTrue(self.mod.is_direct_config_write(segment))
        for segment in ("cat .git/config", "cat .git/config > /tmp/out",
                        "sed -n 1p .git/config", "cp .git/config /tmp/out"):
            with self.subTest(segment=segment):
                self.assertFalse(self.mod.is_direct_config_write(segment))


class InterfaceTest(unittest.TestCase):
    def test_output_schema(self):
        decision, reason = run_hook(SCRIPT, f"git config {KEY} hooks")
        self.assertEqual(decision, "deny")
        self.assertIn(".git/hooks/", reason)
        self.assertIn("git config <key> <value>", reason)
        self.assertIn("禁止", reason)

    def test_argv_debug_path(self):
        self.assertEqual(run_hook_argv(SCRIPT, f"git config {KEY} hooks")[0], "deny")
        self.assertEqual(run_hook_argv(SCRIPT, f"git config --get {KEY}")[0], None)

    def test_odd_inputs_do_not_crash(self):
        for raw in ("", "   ", "not json at all", "[]", "null",
                    '{"tool_input": {}}', '{"tool_input": {"command": null}}',
                    json.dumps({"tool_input": {"command": KEY}})):
            with self.subTest(raw=raw):
                run_hook(SCRIPT, None, raw=raw)

    def test_long_command_is_handled(self):
        padding = "x" * 20000
        self.assertEqual(run_hook(SCRIPT, f"echo {padding}")[0], None)
        self.assertEqual(run_hook(SCRIPT, f"echo {padding}; git config {KEY} h")[0], "deny")

    def test_pathological_input_is_fast(self):
        """病的に長い入力でも正規表現が破綻しない。"""
        cases = [
            f"git config {KEY} " + "2>/dev/null " * 2000,
            f"git config {KEY} " + '"" ' * 2000,
            "git config " + "x " * 5000 + f"{KEY} v",
            f"git config {KEY} " + "()" * 2000,
        ]
        start = time.perf_counter()
        for command in cases:
            run_hook(SCRIPT, command)
        self.assertLess(time.perf_counter() - start, 10.0)


if __name__ == "__main__":
    unittest.main()
