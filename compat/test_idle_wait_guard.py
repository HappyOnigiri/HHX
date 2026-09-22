#!/usr/bin/env python3
"""idle-wait-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）
"""
import unittest

from helpers import HookTestCase, load_hook, run_hook, run_hook_argv

SCRIPT = "idle-wait-guard.py"


class WaitTest(HookTestCase):
    """sleep を含む「時間を潰すだけ」のコマンドは WAIT で deny する。"""

    script = SCRIPT

    def test_background_sleep_loop(self):
        """実測された空ループの前半 (run_in_background の sleep)。"""
        for command in ("sleep 600; echo done", "sleep 600", "sleep 60; echo done",
                        "(sleep 600; echo done)"):
            with self.subTest(command=command):
                decision, reason = run_hook(SCRIPT, command, background=True)
                self.assertEqual(decision, "deny", reason)
                self.assertIn("WAIT", reason)

    def test_foreground_sleep(self):
        """前景でも同じく deny する (待機になっていないのは同じ)。"""
        self.check_table([
            ("sleep 5", "WAIT"),
            ("sleep 600; echo done", "WAIT"),
            ("sleep 1 && sleep 2", "WAIT"),
        ], expected="deny")

    def test_reason_mentions_background_only_when_background(self):
        """理由文はバックグラウンドかどうかで言い回しを変える。"""
        _, bg = run_hook(SCRIPT, "sleep 600", background=True)
        _, fg = run_hook(SCRIPT, "sleep 600", background=False)
        self.assertIn("バックグラウンド実行は即座に戻る", bg)
        self.assertNotIn("バックグラウンド実行は即座に戻る", fg)
        for reason in (bg, fg):
            self.assertIn("ターンを終えて", reason)

    def test_omitted_background_key_behaves_as_foreground(self):
        """run_in_background は省略時にキー自体が無い。落ちずに前景扱いにする。"""
        decision, reason = run_hook(SCRIPT, "sleep 600", background=None)
        self.assertEqual(decision, "deny", reason)
        self.assertIn("WAIT", reason)


class NoopTest(HookTestCase):
    """無効果コマンド単独は NOOP で deny する (前景・背景を問わない)。"""

    script = SCRIPT

    def test_turn_filler(self):
        """実測された空ループの後半 (ターン繋ぎ)。"""
        self.check_table([
            ("echo ok", "NOOP"),
            ("echo waiting", "NOOP"),
            ("echo idle", "NOOP"),
            ("true", "NOOP"),
            ("echo ok; true", "NOOP"),
            ("printf 'ok\\n'", "NOOP"),
        ], expected="deny")

    def test_background_noop_is_denied_too(self):
        decision, reason = run_hook(SCRIPT, "echo ok", background=True)
        self.assertEqual(decision, "deny", reason)
        self.assertIn("NOOP", reason)

    def test_reason_offers_alternatives(self):
        _, reason = run_hook(SCRIPT, "echo ok")
        self.assertIn("ターンを終えて", reason)
        self.assertIn("書き換えて再実行しないでください", reason)

    def test_reason_starts_with_rejected_command(self):
        """UI で理由が省略されても、先頭だけで拒否対象を特定できる。"""
        _, reason = run_hook(SCRIPT, "echo ok; true")
        self.assertTrue(
            reason.startswith('NOOP: 拒否対象コマンド: "echo ok; true"\n'),
            reason,
        )

        _, multiline_reason = run_hook(SCRIPT, "echo first\necho second")
        self.assertTrue(
            multiline_reason.startswith(
                'NOOP: 拒否対象コマンド: "echo first\\necho second"\n'
            ),
            multiline_reason,
        )


class AllowedTest(HookTestCase):
    """効果のあるコマンドは通す。誤爆がこのフックの実害なので範囲を明示する。"""

    script = SCRIPT

    def test_echo_in_compound_command(self):
        """複合コマンドの途中の echo は許容する (実測ログの正当な使い方)。"""
        self.check_table([
            "mkdir -p tmp/scratch && echo created",
            "go build -o bin/x ./cmd/x && echo built",
            "git fetch origin && echo fetched",
            "echo start && make test",
            "npm ci; echo done; npm test",
        ], expected=None)

    def test_echo_carrying_information(self):
        """変数展開・コマンド置換を伴う echo は情報を取るコマンドなので通す。"""
        self.check_table([
            'echo "installPath=$P"',
            "echo $PATH",
            "echo $(git rev-parse HEAD)",
            "echo `date`",
        ], expected=None)

    def test_echo_with_side_effect(self):
        """リダイレクト・パイプ・代入を伴うものは状態を変える。"""
        self.check_table([
            "echo hello > out.txt",
            "echo hello >> out.txt",
            "echo hello | pbcopy",
            "FOO=bar echo hello",
        ], expected=None)

    def test_ordinary_commands(self):
        self.check_table([
            "ls -la",
            "git status",
            "gh pr view 123 --json body",
            "node companion.mjs task --json",
            "sleep_timer --help",
        ], expected=None)

    def test_sleep_combined_with_real_work(self):
        """待機を伴う正当なポーリングは、実処理と連結されていれば通す。"""
        self.check_table([
            "sleep 2 && curl -s https://example.test/health",
            "until gh run view --json status; do sleep 30; done",
        ], expected=None)


class GateTest(unittest.TestCase):
    """一次ゲートの契約 (速度方針) を守る。"""

    def test_gate_lets_unrelated_commands_out_early(self):
        module = load_hook(SCRIPT)
        self.assertTrue(hasattr(module, "classify"))

    def test_known_gap_bare_colon(self):
        """`:` 単独はキーワードゲートを通らず素通りする (意図的な穴)。

        本体の docstring に穴として記載してある。挙動が変わったらここで気づけるようにする。
        """
        self.assertIsNone(run_hook(SCRIPT, ":")[0])


class ArgvTest(unittest.TestCase):
    """デバッグ経路 (argv) でも同じ判定になる。"""

    def test_argv_path(self):
        self.assertEqual(run_hook_argv(SCRIPT, "sleep 600; echo done")[0], "deny")
        self.assertEqual(run_hook_argv(SCRIPT, "echo ok")[0], "deny")
        self.assertIsNone(run_hook_argv(SCRIPT, "mkdir -p x && echo created")[0])


if __name__ == "__main__":
    unittest.main(verbosity=2)
