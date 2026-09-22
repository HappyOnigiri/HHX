#!/usr/bin/env python3
"""forbidden-term-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）
"""
import shutil
import tempfile
import unittest
from pathlib import Path

from helpers import HookTestCase, git, make_repo, run_hook

SCRIPT = "forbidden-term-guard.py"
TERM = "acme-internal"


class GuardTestBase(HookTestCase):
    """語リストを持つリポジトリを作り、その中から実行したものとして判定させる。"""

    script = SCRIPT
    terms = (TERM,)

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="forbidden-term-guard-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.repo = Path(make_repo(self.tmp / "repo", dirty=False))
        self.default_cwd = str(self.repo)
        if self.terms:
            (self.repo / ".git" / "forbidden-terms.txt").write_text(
                "\n".join(self.terms) + "\n", encoding="utf-8")


class BlockedTest(GuardTestBase):
    """本文をリモートへ送るコマンドは、禁止語を含むならブロックする。"""

    def test_pr_and_issue_bodies(self):
        self.check_table([
            f'gh pr create --title "fix" --body "closes {TERM}#12"',
            f'gh pr create --body "{TERM}"',
            f'gh pr edit 12 --body "{TERM} の対応"',
            f'gh pr comment 12 --body "{TERM}"',
            f'gh issue create --title "{TERM}" --body "x"',
            f'gh issue comment 3 --body "{TERM}"',
            f'gh api repos/o/r/pulls -f body="{TERM}"',
            f'/opt/homebrew/bin/gh pr create --body "{TERM}"',
            f'cd sub && gh pr create --body "{TERM}"',
            f'gh pr create --body "{TERM.upper()}"',
        ], expected="deny")

    def test_body_file_contents(self):
        body = self.repo / "body.md"
        body.write_text(f"## 概要\n\n{TERM} を直した\n", encoding="utf-8")
        for command in (
            "gh pr create --title x --body-file body.md",
            f"gh pr create --title x --body-file {body}",
            "gh pr edit 12 --body-file=body.md",
            "gh issue comment 3 -F body.md",
        ):
            with self.subTest(command=command):
                decision, reason = run_hook(SCRIPT, command, str(self.repo))
                self.assertEqual(decision, "deny", reason)
                self.assertIn(":3:", reason)  # 何行目かまで出す

    def test_regex_entry(self):
        self.terms = ("re:acme[-_ ]?corp",)
        self.setUp()
        self.assert_decision('gh pr create --body "AcmeCorp のこと"', "deny")

    def test_linked_worktree_uses_common_dir(self):
        """linked worktree からでも共通 git ディレクトリの語リストを読む。"""
        worktree = self.tmp / "wt"
        git(self.repo, "worktree", "add", "-q", "--detach", str(worktree))
        self.assert_decision(f'gh pr create --body "{TERM}"', "deny", cwd=str(worktree))


class PassedTest(GuardTestBase):
    """通過させる側。誤爆がこのフックの実害なので厚めに見る。"""

    def test_clean_bodies(self):
        self.check_table([
            'gh pr create --title "fix" --body "バグを直した"',
            "gh pr edit 12 --body-file body.md",
            "gh pr view 12",
            "gh pr list",
            "gh pr checkout 12",
        ], expected=None)

    def test_other_commands_with_the_term(self):
        """git 側の hook が担当する操作と、送信を伴わない操作は対象外。"""
        self.check_table([
            f'git commit -m "{TERM}"',
            f"git push origin feature/{TERM}",
            f"grep -r {TERM} .",
            f'echo "{TERM}" > note.txt',
        ], expected=None)


class OptInTest(GuardTestBase):
    """語リストの無いリポジトリと、git 管理外では何もしない。"""

    terms = ()

    def test_repo_without_terms_file(self):
        self.assert_decision(f'gh pr create --body "{TERM}"', None)

    def test_outside_a_repository(self):
        outside = self.tmp / "plain"
        outside.mkdir()
        self.assert_decision(f'gh pr create --body "{TERM}"', None, cwd=str(outside))


if __name__ == "__main__":
    unittest.main()
