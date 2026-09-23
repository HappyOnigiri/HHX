#!/usr/bin/env python3
"""互換スイートの切り替えそのものを確かめる。

hook を移植する前でも、hhx を対象に L1 の経路 (stdin・argv・UserPromptSubmit) で起動できることを見る。
実行: make compat-test（compat/README.md を参照）
"""
import unittest
from unittest import mock

import helpers
from helpers import load_hook, run_hook, run_hook_argv, run_prompt_hook

# 登録表に無い名前。hhx は未知の hook を無出力・終了コード 0 で抜ける。
UNKNOWN = "compat-harness-unknown"


@unittest.skipUnless(helpers.TARGET == "hhx", "hhx を対象にしたときだけ確かめる")
class HHXTargetTest(unittest.TestCase):

    def test_unported_hook_is_skipped(self):
        with self.assertRaises(unittest.SkipTest):
            helpers.hook_command(UNKNOWN + ".py")

    def test_l2_is_skipped_on_use(self):
        module = load_hook("pr-merge-guard.py")
        with self.assertRaises(unittest.SkipTest):
            module.main  # noqa: B018 — 属性に触れた時点で skip になることを見る

    def test_script_names_map_to_hook_names(self):
        self.assertEqual(helpers.hook_name("pr-merge-guard.py"), "pr-merge-guard")
        self.assertEqual(helpers.hook_name("worktree-guard.py"), "discard-guard")

    def test_contract_paths_reach_hhx(self):
        """未知の名前を移植済みとみなして、helpers の起動経路をそのまま通す。"""
        with mock.patch.object(helpers, "PORTED_HOOKS", frozenset({UNKNOWN})):
            self.assertEqual(run_hook(UNKNOWN + ".py", "gh pr merge 1"), (None, ""))
            self.assertEqual(run_hook_argv(UNKNOWN + ".py", "gh pr merge 1"), (None, ""))
            proc = run_prompt_hook(UNKNOWN + ".py", "https://github.com/o/r/pull/1")
            self.assertEqual((proc.returncode, proc.stdout, proc.stderr), (0, "", ""))


if __name__ == "__main__":
    unittest.main()
