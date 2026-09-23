#!/usr/bin/env python3
"""worktree-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）

このフックの価値は「破棄しても戻せること」なので、L3 では git をモックせず本物の
リポジトリでスナップショットを作り、復元できるところまで確認する。
"""
import json
import os
import shutil
import tempfile
import threading
import time
import unittest
from pathlib import Path

from helpers import (
    HookTestCase,
    codex_exec_pretooluse_payload,
    git,
    hook_command,
    load_hook,
    make_repo,
    run_hook as _run_hook,
    run_hook_argv,
    snapshot_ref_exists,
)

SCRIPT = "worktree-guard.py"
REF = "refs/claude/wt-snapshot"

UNRESOLVED = "静的に特定できず"   # DENY_UNRESOLVED の目印
FAILED = "作成に失敗"             # DENY_FAILED の目印
LINKED_ATTACH = "linked worktree へのブランチ attach"
POLICY_UNRESOLVED = "fail closed"
MAIN_POLICY_ENV = {"WT_AGENT_WORKTREE_POLICY": "main"}


def run_hook(script, command, cwd=None, raw=None, env=None):
    """Run policy-independent worktree and snapshot guard tests."""
    return _run_hook(
        script,
        command,
        cwd,
        raw,
        MAIN_POLICY_ENV if env is None else env,
    )


class WorktreeGuardHookTestCase(HookTestCase):
    """HookTestCase variant for the guard's policy-independent behavior."""

    def assert_decision(self, command, expected, label=None, cwd=None):
        decision, reason = run_hook(
            self.script,
            command,
            cwd if cwd is not None else self.default_cwd,
        )
        self.assertEqual(
            decision,
            expected,
            f"コマンド: {command!r} / 理由: {reason!r}",
        )
        if label is not None:
            self.assertIn(
                label,
                reason,
                f"コマンド: {command!r} の理由にラベルが出ていない",
            )


class SandboxMixin:
    """使い捨てリポジトリ群を用意する。"""

    @classmethod
    def make_sandbox(cls):
        cls.tmp = tempfile.mkdtemp(prefix="worktree-guard-test-")
        cls.repo_a = make_repo(Path(cls.tmp) / "repoA")
        cls.repo_b = make_repo(Path(cls.tmp) / "repoB")
        cls.nested = make_repo(Path(cls.tmp) / "repoA" / "sub")
        cls.clean = make_repo(Path(cls.tmp) / "clean", dirty=False)
        cls.notarepo = str(Path(cls.tmp) / "notarepo")
        os.makedirs(cls.notarepo, exist_ok=True)

    @classmethod
    def drop_sandbox(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)


class MainWorkspacePassThroughTest(SandboxMixin, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.make_sandbox()
        Path(cls.clean, ".gitignore").write_text("ignored/\n", encoding="utf-8")
        git(cls.clean, "add", ".gitignore")
        git(cls.clean, "commit", "-q", "-m", "ignore generated files")
        Path(cls.clean, "ignored").mkdir()
        cls.detached = str(Path(cls.tmp) / "detached")
        git(cls.clean, "worktree", "add", "-q", "--detach", cls.detached, "HEAD")

    @classmethod
    def tearDownClass(cls):
        cls.drop_sandbox()

    def run_policy(self, command, cwd=None, **extra_env):
        env = {"WT_AGENT_WORKTREE_POLICY": "on-demand", **extra_env}
        return run_hook(SCRIPT, command, cwd or self.clean, env=env)

    def run_codex_exec_policy(self, command, workdir):
        """main で始めた Codex セッションから workdir 指定で実行する。"""
        return run_hook(
            SCRIPT,
            None,
            raw=codex_exec_pretooluse_payload(command, self.clean, workdir),
            env={"WT_AGENT_WORKTREE_POLICY": "on-demand"},
        )

    def test_on_demand_main_git_state_changes_pass_through(self):
        for command in (
            "git checkout --detach HEAD",
            "git switch --detach HEAD",
            "git add tracked.txt",
            "git commit -m test",
            "git reset HEAD -- tracked.txt",
            "git stash push",
        ):
            with self.subTest(command=command):
                self.assertEqual((None, ""), self.run_policy(command))

    def test_on_demand_main_script_writes_pass_through(self):
        for command in (
            "printf x > tracked.txt",
            "touch new.txt",
            "rm untracked.txt",
        ):
            with self.subTest(command=command):
                self.assertEqual((None, ""), self.run_policy(command))

    # Codex は exec_command.workdir を PreToolUse へ渡さず、フックにはセッション開始時の
    # main worktree が cwd として届く。main のハードガードを持たない方針にしたため、
    # その情報欠落があっても、detached worktree で実行される commit / checkout --detach を
    # 誤ってブロックしない。実際の Codex は起動せず、観測済みの入力変換をモックで再現する。
    def test_codex_commit_in_detached_worktree_should_not_be_blocked(self):
        """main で開始後に作った worktree の commit は本来許可されるべき。"""
        self.assertEqual(
            (None, ""),
            self.run_codex_exec_policy("git commit -m test", self.detached),
        )

    def test_codex_checkout_detach_in_detached_worktree_should_not_be_blocked(self):
        """main で開始後に作った worktree の detached checkout も許可されるべき。"""
        self.assertEqual(
            (None, ""),
            self.run_codex_exec_policy("git checkout --detach HEAD", self.detached),
        )

    def test_missing_policy_also_passes_main_git_changes_through(self):
        self.assertEqual(
            (None, ""),
            run_hook(SCRIPT, "git add tracked.txt", self.clean, env={}),
        )

    def test_read_only_git_and_detached_worktree_add_pass_through(self):
        for command in (
            "git status --short",
            "git diff -- tracked.txt",
            "git fetch origin",
            "git add --dry-run tracked.txt",
            "git commit --dry-run",
            f"git worktree add --detach {self.tmp}/new-wt HEAD",
        ):
            with self.subTest(command=command):
                self.assertEqual((None, ""), self.run_policy(command))


class PassThroughTest(SandboxMixin, WorktreeGuardHookTestCase):
    """スナップショットを作って通す (無出力 = 通常の permission フローへ)。

    判定だけでは「保存して通した」と「ルールに当たらず素通しした」が区別できないので、
    ルールの発火有無まで一緒に確認する。
    """

    script = SCRIPT

    @classmethod
    def setUpClass(cls):
        cls.make_sandbox()
        cls.default_cwd = cls.repo_a
        cls.mod = load_hook(SCRIPT)

    @classmethod
    def tearDownClass(cls):
        cls.drop_sandbox()

    def labels(self, command):
        return [label for label, rule in self.mod.DISCARD_RULES
                if self.mod.matches(rule, command)]

    def check_protected(self, commands):
        """通しつつスナップショットを作る (= ルールが発火している)。"""
        for command in commands:
            with self.subTest(command=command):
                self.assert_decision(command, None)
                self.assertTrue(self.labels(command),
                                f"{command!r} がどのルールにも当たらない (保存されない)")

    def check_untouched(self, commands):
        """破棄操作ではないので何もしない。"""
        for command in commands:
            with self.subTest(command=command):
                self.assert_decision(command, None)
                self.assertFalse(self.labels(command), f"{command!r} は破棄操作ではない")

    def test_reset_hard(self):
        self.check_protected([
            "git reset --hard",
            "git reset --hard HEAD~1",
            "git reset --hard origin/main",
            "git reset --hard origin/main && npm ci",
            "git reset --hard; echo done",
            "(git reset --hard)",
            "git reset -q --hard",
            "rtk git reset --hard",
            "/usr/bin/git reset --hard",
            "git --no-pager reset --hard",
            "git -c user.name=x reset --hard",
            "git -c user.name='a b' reset --hard",
            "git --literal-pathspecs reset --hard",
            "git reset --hard --",
            "git reset -q --hard HEAD",
            # 引用の中だけでも拾う (多めに拾って resolve 側で絞る方針)
            "echo 'git reset --hard' > note.txt",
        ])

    def test_checkout_discard_forms(self):
        self.check_protected([
            "git checkout -B feature",
            "git checkout -B feature origin/main",
            "git checkout .",
            "git checkout -- .",
            "git checkout -- src/a.ts",
            "git checkout HEAD -- src/a.ts",
            "git checkout HEAD~1 -- .",
            "git checkout -f main",
            "git checkout --force main",
            "git checkout --ours -- conflicted.txt",
            "git checkout :/",
            "git checkout -f",
            "git checkout --",
        ])

    def test_switch_force_forms(self):
        self.check_protected([
            "git switch -f main",
            "git switch --force main",
            "git switch --discard-changes main",
            "git switch -C feature",
            "git switch -f -",
        ])

    def test_restore_worktree_forms(self):
        self.check_protected([
            "git restore .",
            "git restore :/",
            "git restore src/",
            "git restore src/a.ts",
            "git restore --source=main src/a.ts",
            "git restore -s HEAD~1 .",
            "git restore --staged --worktree .",
            "git restore --worktree .",
            "git restore -SW .",
        ])

    def test_clean_forms(self):
        self.check_protected([
            "git clean -f",
            "git clean -fd",
            "git clean -fdx",
            "git clean -xdf",
            "git clean -ffd",
            "git clean -d -f",
            "git clean --force",
            "git clean --force -d",
        ])

    def test_apply_discard_forms(self):
        self.check_protected([
            "git apply -R fix.patch",
            "git apply --reverse fix.patch",
            "git apply -R --index fix.patch",
            "git apply --3way fix.patch",
            "git apply -3 fix.patch",
            "git apply --reject fix.patch",
            "git apply -R3 fix.patch",
            "git apply -3R fix.patch",
            "git apply -p1 -R fix.patch",
            f"git -C {self.repo_b} apply -R fix.patch",
            "git apply --check fix.patch && git apply -R fix.patch",  # 区切りの先は別扱い
        ])

    def test_target_from_cd_or_dash_c(self):
        self.check_protected([
            f"git -C {self.repo_b} reset --hard",
            f"git -C{self.repo_b} reset --hard",
            f"cd {self.repo_b} && git reset --hard",
            f"cd {self.repo_b}\ngit reset --hard",          # 改行区切り
            "cd sub && git clean -fdx",
            "(cd sub && git clean -fdx)",
            f"make -C {self.repo_b} build && git reset --hard",   # git 以外の -C は無視
            f"git -C {self.repo_b} fetch && git reset --hard",    # 破棄系以外の -C は無視
            f"git reset --hard && cd {self.repo_b} && echo ok",   # 破棄より後ろの cd は無関係
            # cd が複数あっても順に辿る。破棄系が複数あれば全部保存する
            "cd sub && cd .. && git reset --hard",
            f"cd {self.repo_b} && git reset --hard && cd {self.repo_a}",
            f"git reset --hard && cd {self.repo_b} && git reset --hard",
            f"git -C {self.repo_a} reset --hard && git -C {self.repo_b} clean -fd",
        ])

    def test_not_a_discard_operation(self):
        """破棄しない操作は素通し (git stash も stash list/apply で戻せる)。"""
        self.check_untouched([
            "git status",
            "git log --oneline",
            "git diff",
            "git stash",
            "git stash pop",
            "git stash list",
            "git checkout main",
            "git checkout -b feature",
            "git checkout --track origin/feature",
            "git checkout --detach",
            "git switch main",
            "git switch -c feature",
            "git switch -",
            "git restore --staged foo.txt",
            "git restore",
            "git reset --soft HEAD~1",
            "git reset HEAD~1",
            "git reset",
            "git clean -n",
            "git clean --dry-run",
            "git clean -i",
            "git branch -D old",
            "git rm -r --cached .",
            "git checkout -p",
            "git restore --patch",
            "git worktree list",
            "git fetch --prune",
            # 既定の apply は文脈が一致しなければ何も書かずに失敗する
            "git apply fix.patch",
            "git apply --index fix.patch",
            "git apply -p1 fix.patch",
            "git apply --check -R fix.patch",
            "git apply --stat -R fix.patch",
            "git apply --numstat --3way fix.patch",
            "git apply --summary -R fix.patch",
            "git apply --cached -R fix.patch",     # index だけを触る
            "git apply -C3 fix.patch",             # -p3 / -C3 の 3 を -3 と誤検出しない
            "git apply -p3 fix.patch",
            "npm run build",
        ])


class DenyTest(SandboxMixin, WorktreeGuardHookTestCase):
    """スナップショットを作れないときは deny に落とす。"""

    script = SCRIPT

    @classmethod
    def setUpClass(cls):
        cls.make_sandbox()
        cls.default_cwd = cls.repo_a

    @classmethod
    def tearDownClass(cls):
        cls.drop_sandbox()

    def test_target_not_statically_resolvable(self):
        self.check_table([
            ("pushd /tmp && git reset --hard", UNRESOLVED),
            ("popd && git reset --hard", UNRESOLVED),
            ("sh -c 'git reset --hard'", UNRESOLVED),
            ("bash -lc 'git reset --hard'", UNRESOLVED),
            ("/bin/sh -c 'git clean -fd'", UNRESOLVED),
            ("GIT_DIR=/tmp/x git reset --hard", UNRESOLVED),
            ("GIT_WORK_TREE=/tmp git clean -fd", UNRESOLVED),
            ("GIT_INDEX_FILE=/tmp/i git reset --hard", UNRESOLVED),
            ('git -C "$OTHER" reset --hard', UNRESOLVED),
            ("git -C $HOME reset --hard", UNRESOLVED),
            ("git -C ~nobody/dev reset --hard", UNRESOLVED),   # ~user 形は追わない
            ('cd "~/dev" && git reset --hard', UNRESOLVED),    # クォート付きの ~ はシェルも展開しない
            ("git -C 'my repo' reset --hard", UNRESOLVED),
            ('git -C "my repo" clean -fd', UNRESOLVED),
            ("git -C * reset --hard", UNRESOLVED),
            ("git --git-dir=/tmp/x reset --hard", UNRESOLVED),
            ("git --work-tree=/tmp reset --hard", UNRESOLVED),
            ("cd && git reset --hard", UNRESOLVED),          # 引数なしの cd (= ホーム)
            ("cd - && git reset --hard", UNRESOLVED),
            ('cd sub && cd "$DIR" && git reset --hard', UNRESOLVED),  # 途中の cd が変数
        ], expected="deny")

    def test_snapshot_cannot_be_created(self):
        """対象は特定できるが git 管理下ではない。"""
        self.check_table([
            (f"cd {self.notarepo} && git reset --hard", FAILED),
            (f"git -C {self.notarepo} clean -fd", FAILED),
            ("cd .. && git reset --hard", FAILED),           # サンドボックス直下は非リポジトリ
        ], expected="deny")

    def test_missing_directory(self):
        """存在しない行き先は追わない (実行時に失敗した cd の後の相対パスを取り違えるため)。"""
        for cmd in (f"cd {self.tmp}/does-not-exist && git reset --hard",
                    "cd sub && cd does-not-exist && cd .. && git reset --hard"):
            with self.subTest(cmd=cmd):
                self.assert_decision(cmd, "deny", UNRESOLVED)

    def test_empty_cwd_falls_back_to_process_cwd(self):
        """payload の cwd が空・欠落のときはプロセスの cwd (= 非リポジトリ) を見る。"""
        self.assert_decision("git reset --hard", "deny", FAILED, cwd="")
        raw = json.dumps({"tool_input": {"command": "git reset --hard"}})
        self.assertEqual(run_hook(SCRIPT, None, raw=raw)[0], "deny")

    def test_label_appears_in_reason(self):
        decision, reason = run_hook(SCRIPT, "sh -c 'git clean -f && git reset --hard'",
                                    self.repo_a)
        self.assertEqual(decision, "deny")
        # DISCARD_RULES の並び順どおりに連結される
        self.assertIn("git reset --hard / git clean -f", reason)


class DetachedWorktreePolicyTest(WorktreeGuardHookTestCase):
    """linked は常に detached、ブランチを attach できるのは main だけ。"""

    script = SCRIPT

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp(prefix="worktree-policy-test-")
        cls.main = make_repo(Path(cls.tmp) / "main", dirty=False)
        cls.detached = str(Path(cls.tmp) / "detached")
        cls.attached = str(Path(cls.tmp) / "attached")
        git(cls.main, "branch", "other")
        git(cls.main, "worktree", "add", "-q", "--detach", cls.detached, "HEAD")
        git(cls.main, "worktree", "add", "-q", "-b", "legacy-attached", cls.attached)
        git(cls.main, "update-ref", "refs/remotes/origin/remote-only", "HEAD")
        cls.default_cwd = cls.main

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def test_worktree_add_is_outside_this_policy(self):
        for command in (
            "git worktree add /tmp/new-wt HEAD",
            "git worktree add -b feature /tmp/new-wt HEAD",
            "git --no-pager worktree add /tmp/new-wt HEAD",
            "git worktree add --detach /tmp/new-wt HEAD",
            "git worktree add -d /tmp/new-wt HEAD",
        ):
            with self.subTest(command=command):
                self.assert_decision(command, None, cwd=self.main)

    def test_linked_worktree_rejects_branch_attach(self):
        self.check_table([
            ("git checkout other", LINKED_ATTACH),
            ("git checkout -b new-branch", LINKED_ATTACH),
            ("git checkout -b new-branch --detach", LINKED_ATTACH),
            ("git checkout --track origin/remote-only", LINKED_ATTACH),
            ("git checkout remote-only", LINKED_ATTACH),
            ("git checkout -", LINKED_ATTACH),
            ("git switch other", LINKED_ATTACH),
            ("git switch -c new-branch", LINKED_ATTACH),
            ("git switch -c new-branch --detach", LINKED_ATTACH),
            ("git symbolic-ref HEAD refs/heads/other", LINKED_ATTACH),
        ], expected="deny", cwd=self.detached)

    def test_linked_worktree_allows_detached_and_path_operations(self):
        for command in (
            "git checkout --detach HEAD",
            "git switch --detach HEAD",
            "git checkout -- tracked.txt",
            "git checkout .",
            "git symbolic-ref --short HEAD",
        ):
            with self.subTest(command=command):
                self.assert_decision(command, None, cwd=self.detached)

    def test_main_worktree_allows_branch_attach(self):
        for command in (
            "git checkout other",
            "git checkout -b new-branch",
            "git switch other",
            "git switch -c new-branch",
            "git symbolic-ref HEAD refs/heads/other",
        ):
            with self.subTest(command=command):
                self.assert_decision(command, None, cwd=self.main)

    def test_cd_and_git_dash_c_select_the_linked_worktree(self):
        self.assert_decision(
            f"cd {self.detached} && git switch other", "deny", LINKED_ATTACH, cwd=self.main)
        self.assert_decision(
            f"git -C {self.detached} checkout other", "deny", LINKED_ATTACH, cwd=self.main)

    def test_unresolved_shell_wrapper_is_denied(self):
        self.assert_decision(
            "sh -c 'git switch other'", "deny", POLICY_UNRESOLVED, cwd=self.detached)

    def test_policy_words_in_non_policy_arguments_are_allowed(self):
        for command in (
            'git commit -m "see git switch docs"',
            'git log --oneline --grep "git checkout"',
            'echo "git checkout other" >> notes.txt',
            f'git -C {self.tmp}/not-created status --porcelain "git checkout"',
        ):
            with self.subTest(command=command):
                self.assert_decision(command, None, cwd=self.main)

    def test_non_repository_checkout_is_left_to_git(self):
        plain = str(Path(self.tmp) / "plain")
        os.makedirs(plain)
        self.assert_decision("git checkout main", None, cwd=plain)

    def test_safe_worktree_add_can_be_followed_by_non_policy_git_command(self):
        target = str(Path(self.tmp) / "not-created-yet")
        self.assert_decision(
            f"git worktree add --detach {target} HEAD && git -C {target} status",
            None,
            cwd=self.main,
        )

    def test_checkout_dynamic_ref_is_denied_in_linked_worktree(self):
        for command in (
            "git checkout $BRANCH",
            "git checkout `cat ref.txt`",
            "git checkout $(cat ref.txt)",
        ):
            with self.subTest(command=command):
                self.assert_decision(command, "deny", cwd=self.detached)

    def test_argument_injecting_wrappers_are_denied_in_linked_worktree(self):
        for command in (
            "printf feature | xargs git checkout",
            "printf feature | parallel git checkout",
        ):
            with self.subTest(command=command):
                self.assert_decision(command, "deny", cwd=self.detached)
        self.assert_decision("printf other | xargs git checkout", None, cwd=self.main)

    def test_find_exec_placeholder_does_not_make_policy_unresolved(self):
        self.assert_decision(
            "git checkout other && find . -name '*.py' -exec wc -l {} \\;",
            None,
            cwd=self.main,
        )

    def test_new_path_policy_operation_explains_command_split(self):
        target = str(Path(self.tmp) / "future-clone")
        decision, reason = run_hook(
            SCRIPT,
            f"git clone https://example.invalid/repo {target} && "
            f"cd {target} && git checkout -b feature",
            self.main,
        )
        self.assertEqual("deny", decision)
        self.assertIn("2 回の Bash 呼び出し", reason)

    def test_legacy_attached_linked_worktree_can_detach(self):
        self.assert_decision("git switch --detach", None, cwd=self.attached)


class GateTest(WorktreeGuardHookTestCase):
    """一次ゲート・二次ゲートで抜けるもの。"""

    script = SCRIPT

    def test_non_git_commands(self):
        self.check_table([
            "ls -la",
            "npm ci",
            "echo legit",
            "rm -rf build",
            "make clean",
            "docker system prune -f",
        ], expected=None)


class DiscardRuleTest(unittest.TestCase):
    """L2: どのルールが発火するか。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def labels(self, cmd):
        return [label for label, rule in self.mod.DISCARD_RULES
                if self.mod.matches(rule, cmd)]

    def test_single_rules(self):
        cases = {
            "git reset --hard": ["git reset --hard"],
            "git checkout .": ["git checkout (破棄形)"],
            "git switch -f main": ["git switch (強制切り替え)"],
            "git restore src/": ["git restore (作業ツリー)"],
            "git clean -fd": ["git clean -f"],
        }
        for cmd, want in cases.items():
            with self.subTest(cmd=cmd):
                self.assertEqual(self.labels(cmd), want)

    def test_multiple_rules(self):
        self.assertEqual(self.labels("git clean -f && git reset --hard"),
                         ["git reset --hard", "git clean -f"])

    def test_no_rules(self):
        for cmd in ("git status", "git stash", "git checkout main", "git switch main",
                    "git restore --staged foo", "git clean -n", "git reset --soft HEAD~1",
                    "git checkout -b topic", "git switch -c topic", "ls -la"):
            with self.subTest(cmd=cmd):
                self.assertEqual(self.labels(cmd), [])

    def test_every_rule_subcommand_is_resolvable(self):
        """DISCARD_RULES で拾うサブコマンドは DISCARD_SUBCOMMANDS に入っていること。

        漏らすと git -C <別リポジトリ> を採用せず、無関係なリポジトリを保存してしまう。
        """
        samples = {
            "reset": "git reset --hard",
            "checkout": "git checkout .",
            "switch": "git switch -f main",
            "restore": "git restore .",
            "clean": "git clean -fd",
        }
        for sub, cmd in samples.items():
            with self.subTest(sub=sub):
                self.assertTrue(self.labels(cmd), f"{cmd} がどのルールにも当たらない")
                self.assertIn(sub, self.mod.DISCARD_SUBCOMMANDS)

    def test_restore_staged_only_is_not_a_discard(self):
        for cmd in ("git restore --staged foo.txt", "git restore --staged .",
                    "git restore --staged=x ."):
            with self.subTest(cmd=cmd):
                self.assertFalse(self.mod.is_worktree_restore(cmd))
        for cmd in ("git restore --staged --worktree .", "git restore --worktree foo",
                    "git restore ."):
            with self.subTest(cmd=cmd):
                self.assertTrue(self.mod.is_worktree_restore(cmd))

    def test_restore_needs_a_pathspec(self):
        self.assertFalse(self.mod.is_worktree_restore("git restore"))
        self.assertFalse(self.mod.is_worktree_restore("git restore --patch"))

    def test_restore_multiple_occurrences(self):
        """1 コマンド内に複数あるとき、破棄側があれば拾う。"""
        self.assertTrue(self.mod.is_worktree_restore(
            "git restore --staged foo && git restore bar"))


class ResolveTargetDirTest(unittest.TestCase):
    """L2: スナップショット対象ディレクトリの決定。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)
        cls.tmp = tempfile.mkdtemp(prefix="resolve-test-")
        cls.a = os.path.join(cls.tmp, "a")
        cls.b = os.path.join(cls.tmp, "b")
        for path in (cls.a, cls.b, os.path.join(cls.a, "sub")):
            os.makedirs(path, exist_ok=True)

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def resolve(self, cmd, cwd=None):
        return self.mod.resolve_target_dirs(cmd, cwd if cwd is not None else self.a)

    def assert_target(self, cmd, expected, cwd=None):
        """expected は 1 つのパス、または出現順のパスのリスト。"""
        got = self.resolve(cmd, cwd)
        self.assertIsNotNone(got, f"{cmd!r} が特定失敗になった")
        expected = [expected] if isinstance(expected, str) else expected
        self.assertEqual([os.path.normpath(p) for p in got],
                         [os.path.normpath(p) for p in expected], f"cmd={cmd!r}")

    def test_uses_cwd_by_default(self):
        for cmd in ("git reset --hard", "git clean -fd", "git checkout .",
                    "echo git reset --hard",
                    # クォートの中は git トークンとして拾えないが、多めに拾う方針で cwd を保存する
                    "echo 'git reset --hard' > note.txt"):
            with self.subTest(cmd=cmd):
                self.assert_target(cmd, self.a)

    def test_dash_c_on_discard_call(self):
        self.assert_target(f"git -C {self.b} reset --hard", self.b)
        self.assert_target(f"git -C{self.b} clean -fd", self.b)
        self.assert_target(f"git -C {self.b} switch -f main", self.b)
        self.assert_target(f"git -C {self.b} restore .", self.b)

    def test_quoted_option_values(self):
        """クォートで空白を含む値。値の途中で切れてサブコマンドを取り違えないこと。"""
        # -c の値に空白があっても対象は cwd
        self.assert_target("git -c user.name='a b' reset --hard", self.a)
        self.assert_target('git -c user.name="a b" -c x=y clean -fd', self.a)
        # -C の値がクォート込みなら静的に解決できないので特定失敗
        for cmd in ("git -C 'a b' reset --hard", 'git -C "a b" reset --hard',
                    "git -C'a b' reset --hard"):
            with self.subTest(cmd=cmd):
                self.assertIsNone(self.resolve(cmd))

    def test_dash_c_elsewhere_is_ignored(self):
        self.assert_target(f"make -C {self.b} build && git reset --hard", self.a)
        self.assert_target(f"git -C {self.b} fetch && git reset --hard", self.a)
        self.assert_target(f"git -C {self.b} status; git clean -fd", self.a)

    def test_cd_before_discard(self):
        self.assert_target(f"cd {self.b} && git reset --hard", self.b)
        self.assert_target("cd sub && git reset --hard", os.path.join(self.a, "sub"))
        self.assert_target("cd ./sub && git clean -fd", os.path.join(self.a, "sub"))
        self.assert_target(f"cd {self.b}\ngit reset --hard", self.b)
        self.assert_target(f"(cd {self.b} && git reset --hard)", self.b)
        self.assert_target(f"true;cd {self.b};git reset --hard", self.b)

    def test_cd_after_discard_is_ignored(self):
        self.assert_target(f"git reset --hard && cd {self.b}", self.a)
        self.assert_target(f"git reset --hard; cd {self.b}; echo done", self.a)

    def test_dash_c_applies_after_cd(self):
        self.assert_target("cd sub && git -C . reset --hard", os.path.join(self.a, "sub"))
        self.assert_target(f"cd sub && git -C {self.b} reset --hard", self.b)
        self.assert_target("cd sub; git -C .. reset --hard", self.a)
        self.assert_target("git -C sub reset --hard", os.path.join(self.a, "sub"))
        self.assert_target("git -C ./sub clean -fd", os.path.join(self.a, "sub"))

    def test_cd_chain(self):
        """cd が複数あっても順に辿り、破棄時点の作業ディレクトリを対象にする。"""
        self.assert_target("cd sub && cd .. && git reset --hard", self.a)
        self.assert_target(f"cd {self.b} && cd {self.a} && git reset --hard", self.a)
        self.assert_target("cd sub; cd ..; cd sub; git clean -fd", os.path.join(self.a, "sub"))
        self.assert_target("cd sub && cd ../../b && git reset --hard", self.b)
        self.assert_target(f"cd {self.b}\ncd {self.a}/sub\ngit checkout -- x",
                           os.path.join(self.a, "sub"))

    def test_tilde_prefix_is_expanded(self):
        """先頭の ~ / ~/ だけは展開する (シェルと同じ HOME なので決定的)。"""
        home = os.path.expanduser("~")
        self.assert_target("cd ~ && git reset --hard", home)
        self.assert_target("cd ~/ && git reset --hard", home)
        self.assert_target("git -C ~ reset --hard", home)
        self.assert_target("git -C~ clean -fd", home)
        # 展開後の相対 cd も追える
        self.assert_target(f"cd ~ && cd {self.a} && git reset --hard", self.a)

    def test_pathological_input_is_fast(self):
        """病的に長い入力でも正規表現が破綻しない。"""
        cases = [
            "git " + "-c a=b " * 200 + "reset --hard",
            "git -C " + "'" * 300 + " reset --hard",
            "git reset " + "x" * 20000 + " --hard",
            "git reset --hard" + " && cd x" * 500,
        ]
        start = time.perf_counter()
        for cmd in cases:
            self.mod.resolve_target_dirs(cmd, self.a)
            [r for _, r in self.mod.DISCARD_RULES if self.mod.matches(r, cmd)]
        self.assertLess(time.perf_counter() - start, 2.0)

    def test_unresolvable(self):
        cases = [
            "pushd /tmp && git reset --hard",
            "popd; git reset --hard",
            "sh -c 'git reset --hard'",
            "bash -lc 'git reset --hard'",
            "/bin/zsh -c 'git reset --hard'",
            "GIT_DIR=/tmp git reset --hard",
            "GIT_WORK_TREE=/tmp git reset --hard",
            "GIT_INDEX_FILE=/tmp/i git reset --hard",
            "git --git-dir=/tmp/x reset --hard",
            "git --work-tree=/tmp reset --hard",
            "git -C",
            "cd && git reset --hard",
            "cd -- && git reset --hard",
            "cd sub && cd $X && git reset --hard",          # 途中の cd が変数
            "cd sub && cd nope && cd .. && git reset --hard",  # 途中の cd 先が存在しない
            'git -C "$X" reset --hard',
            "git -C $X reset --hard",
            "git -C `pwd` reset --hard",
            "git -C 'a b' reset --hard",
            "git -C ~nobody/x reset --hard",
            'cd "~/x" && git reset --hard',
            "cd ~/does-not-exist-worktree-guard-test && git reset --hard",
            "git -C */repo reset --hard",
            "cd /does/not/exist && git reset --hard",
            "git -C /does/not/exist reset --hard",
        ]
        for cmd in cases:
            with self.subTest(cmd=cmd):
                self.assertIsNone(self.resolve(cmd), f"{cmd!r} は特定失敗になるべき")

    def test_multiple_discards_collect_every_target(self):
        """破棄系が複数あれば、それぞれの時点のディレクトリを出現順に全部候補にする。"""
        self.assert_target(f"git -C {self.a} reset --hard && git -C {self.b} clean -fd",
                           [self.a, self.b])
        self.assert_target(f"cd {self.b} && git reset --hard && cd {self.a}", self.b)
        self.assert_target(f"git reset --hard && cd {self.b} && git clean -fd",
                           [self.a, self.b])
        self.assert_target(f"cd sub && git clean -fd && cd {self.b} && git reset --hard",
                           [os.path.join(self.a, "sub"), self.b])

    def test_same_target_twice_is_listed_once(self):
        self.assert_target(f"git -C {self.b} reset --hard && git -C {self.b} clean -fd",
                           self.b)
        self.assert_target(f"git reset --hard && cd {self.a} && git clean -fd", self.a)

    def test_helpers(self):
        self.assertTrue(self.mod.is_static_path("/tmp/a-b_c.d"))
        for path in ("$HOME", "`pwd`", "a*", "a?", "~/x", "'a'", '"a"', "a&b", "a;b",
                     "a|b", "a(b", "a)b"):
            with self.subTest(path=path):
                self.assertFalse(self.mod.is_static_path(path))
        for token in ("sh", "bash", "zsh", "dash", "ksh", "/bin/sh", "/usr/local/bin/bash"):
            with self.subTest(token=token):
                self.assertTrue(self.mod.is_shell(token))
        for token in ("ssh", "shell", "bashrc", "git", "/bin/ls"):
            with self.subTest(token=token):
                self.assertFalse(self.mod.is_shell(token))


class SnapshotTest(unittest.TestCase):
    """L3: 本物の git でスナップショットを作り、復元できることを確認する。"""

    def temp_repo(self, **kwargs):
        base = tempfile.mkdtemp(prefix="snapshot-test-")
        self.addCleanup(shutil.rmtree, base, ignore_errors=True)
        return make_repo(Path(base) / "repo", **kwargs)

    def test_saves_tracked_and_untracked(self):
        repo = self.temp_repo()
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", repo), (None, ""))
        self.assertTrue(snapshot_ref_exists(repo))
        self.assertEqual(git(repo, "show", f"{REF}:tracked.txt"), "v2-uncommitted")
        self.assertEqual(git(repo, "show", f"{REF}:untracked.txt"), "new")

    def test_saves_staged_changes(self):
        repo = self.temp_repo()
        Path(repo, "staged.txt").write_text("staged\n", encoding="utf-8")
        git(repo, "add", "staged.txt")
        run_hook(SCRIPT, "git reset --hard", repo)
        self.assertEqual(git(repo, "show", f"{REF}:staged.txt"), "staged")

    def test_worktree_and_index_are_untouched(self):
        repo = self.temp_repo()
        Path(repo, "staged.txt").write_text("staged\n", encoding="utf-8")
        git(repo, "add", "staged.txt")
        before_status = git(repo, "status", "--porcelain")
        before_index = git(repo, "ls-files", "-s")
        run_hook(SCRIPT, "git reset --hard", repo)
        self.assertEqual(git(repo, "status", "--porcelain"), before_status)
        self.assertEqual(git(repo, "ls-files", "-s"), before_index)

    def test_snapshot_is_restorable(self):
        """破棄したあと、スナップショットから中身が戻せる。"""
        repo = self.temp_repo()
        run_hook(SCRIPT, "git reset --hard && git clean -fd", repo)
        # 実際に破棄する
        git(repo, "reset", "--hard", "-q")
        git(repo, "clean", "-fdq")
        self.assertEqual(Path(repo, "tracked.txt").read_text(encoding="utf-8"), "v1\n")
        self.assertFalse(Path(repo, "untracked.txt").exists())
        # スナップショットから復元する
        git(repo, "checkout", REF, "--", ".")
        self.assertEqual(Path(repo, "tracked.txt").read_text(encoding="utf-8"),
                         "v2-uncommitted\n")
        self.assertEqual(Path(repo, "untracked.txt").read_text(encoding="utf-8"), "new\n")

    def test_parent_is_head_and_author_is_hook(self):
        repo = self.temp_repo()
        head = git(repo, "rev-parse", "HEAD")
        run_hook(SCRIPT, "git reset --hard", repo)
        self.assertEqual(git(repo, "rev-parse", f"{REF}^"), head)
        self.assertEqual(git(repo, "log", "-1", "--format=%an <%ae>", REF),
                         "claude-hook <claude-hook@localhost>")
        self.assertIn("wt-snapshot: git reset --hard",
                      git(repo, "log", "-1", "--format=%s", REF))

    def test_reflog_accumulates(self):
        repo = self.temp_repo()
        run_hook(SCRIPT, "git reset --hard", repo)
        first = git(repo, "rev-parse", REF)
        Path(repo, "tracked.txt").write_text("v3-uncommitted\n", encoding="utf-8")
        run_hook(SCRIPT, "git clean -fd", repo)
        second = git(repo, "rev-parse", REF)
        self.assertNotEqual(first, second)
        reflog = git(repo, "reflog", "show", REF)
        self.assertEqual(len(reflog.splitlines()), 2)
        self.assertIn("git clean -f", reflog.splitlines()[0])
        # 古い方も reflog から辿れる
        self.assertEqual(git(repo, "rev-parse", f"{REF}@{{1}}"), first)

    def test_clean_worktree_creates_no_ref(self):
        repo = self.temp_repo(dirty=False)
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", repo), (None, ""))
        self.assertFalse(snapshot_ref_exists(repo))

    def test_non_discard_command_creates_no_ref(self):
        repo = self.temp_repo()
        self.assertEqual(run_hook(SCRIPT, "git status", repo), (None, ""))
        self.assertFalse(snapshot_ref_exists(repo))

    def test_repo_without_commits(self):
        """HEAD がないリポジトリでも親なしコミットとして保存する。"""
        repo = self.temp_repo(commit=False)
        self.assertEqual(run_hook(SCRIPT, "git clean -fd", repo), (None, ""))
        self.assertTrue(snapshot_ref_exists(repo))
        self.assertEqual(git(repo, "show", f"{REF}:untracked.txt"), "new")
        self.assertEqual(git(repo, "rev-list", "--count", REF), "1")

    def test_gitignored_file_is_not_saved(self):
        """既知の限界: gitignore 対象は保存しない (clean -fdx では救えない)。"""
        repo = self.temp_repo(ignored=True)
        run_hook(SCRIPT, "git clean -fdx", repo)
        self.assertTrue(snapshot_ref_exists(repo))
        proc = git(repo, "cat-file", "-e", f"{REF}:ignored.txt", check=False)
        self.assertEqual(proc, "")
        self.assertEqual(git(repo, "ls-tree", "--name-only", REF).splitlines(),
                         [".gitignore", "tracked.txt", "untracked.txt"])

    def test_unicode_filename(self):
        repo = self.temp_repo()
        Path(repo, "日本語ファイル.txt").write_text("にほんご\n", encoding="utf-8")
        run_hook(SCRIPT, "git reset --hard", repo)
        self.assertEqual(git(repo, "show", f"{REF}:日本語ファイル.txt"), "にほんご")

    def test_spaces_in_filename(self):
        repo = self.temp_repo()
        Path(repo, "a file with spaces.txt").write_text("x\n", encoding="utf-8")
        run_hook(SCRIPT, "git reset --hard", repo)
        self.assertEqual(git(repo, "show", f"{REF}:a file with spaces.txt"), "x")

    def test_nested_repo_targets_inner(self):
        """cd で入れ子リポジトリに入った場合、内側だけを保存する。"""
        outer = self.temp_repo()
        inner = make_repo(Path(outer) / "inner")
        self.assertEqual(run_hook(SCRIPT, "cd inner && git reset --hard", outer), (None, ""))
        self.assertTrue(snapshot_ref_exists(inner))
        self.assertFalse(snapshot_ref_exists(outer))

    def test_cd_target_only(self):
        """cd 先のリポジトリだけを保存し、cwd 側には作らない。"""
        base = tempfile.mkdtemp(prefix="snapshot-test-")
        self.addCleanup(shutil.rmtree, base, ignore_errors=True)
        repo_a = make_repo(Path(base) / "a")
        repo_b = make_repo(Path(base) / "b")
        run_hook(SCRIPT, f"cd {repo_b} && git reset --hard", repo_a)
        self.assertTrue(snapshot_ref_exists(repo_b))
        self.assertFalse(snapshot_ref_exists(repo_a))

    def test_multiple_targets_are_all_saved(self):
        """破棄系が別リポジトリに 2 つあれば、どちらも保存してから通す。"""
        base = tempfile.mkdtemp(prefix="snapshot-test-")
        self.addCleanup(shutil.rmtree, base, ignore_errors=True)
        repo_a = make_repo(Path(base) / "a")
        repo_b = make_repo(Path(base) / "b")
        cmd = f"git reset --hard && cd {repo_b} && git clean -fd"
        self.assertEqual(run_hook(SCRIPT, cmd, repo_a), (None, ""))
        self.assertEqual(git(repo_a, "show", f"{REF}:tracked.txt"), "v2-uncommitted")
        self.assertEqual(git(repo_b, "show", f"{REF}:untracked.txt"), "new")

    def test_tilde_cd_saves_the_target(self):
        """cd ~/repo の形でも対象を保存する (HOME を一時ディレクトリに差し替えて確認)。"""
        home = tempfile.mkdtemp(prefix="snapshot-home-")
        self.addCleanup(shutil.rmtree, home, ignore_errors=True)
        repo = make_repo(Path(home) / "repo")
        notarepo = str(Path(home) / "elsewhere")
        os.makedirs(notarepo)
        self.assertEqual(run_hook(SCRIPT, "cd ~/repo && git reset --hard", notarepo,
                                  env={**MAIN_POLICY_ENV, "HOME": home}), (None, ""))
        self.assertEqual(git(repo, "show", f"{REF}:tracked.txt"), "v2-uncommitted")

    def test_same_worktree_is_saved_once(self):
        """候補が同じ作業ツリーのサブディレクトリでも、保存は 1 回だけ。"""
        repo = self.temp_repo()
        Path(repo, "web").mkdir()
        cmd = ("git checkout -- a.go && cd web && git checkout -- b.ts"
               " && cd .. && git reset --hard")
        self.assertEqual(run_hook(SCRIPT, cmd, repo), (None, ""))
        self.assertEqual(len(git(repo, "reflog", "show", REF).splitlines()), 1)

    def test_temp_index_is_reused(self):
        repo = self.temp_repo()
        run_hook(SCRIPT, "git reset --hard", repo)
        index = Path(git(repo, "rev-parse", "--absolute-git-dir"),
                     "claude-wt-snapshot.index")
        self.assertTrue(index.exists())
        first_mtime = index.stat().st_mtime_ns
        Path(repo, "another.txt").write_text("y\n", encoding="utf-8")
        run_hook(SCRIPT, "git reset --hard", repo)
        self.assertNotEqual(index.stat().st_mtime_ns, first_mtime)
        self.assertEqual(git(repo, "show", f"{REF}:another.txt"), "y")

    def test_symlinked_repo_path(self):
        """symlink 経由のパスでも保存できる (~/dev/dotfiles のような構成)。"""
        repo = self.temp_repo()
        link = str(Path(repo).parent / "link")
        os.symlink(repo, link)
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", link), (None, ""))
        self.assertTrue(snapshot_ref_exists(repo))

    def test_repo_path_with_space(self):
        """cwd に空白を含むパスが来ても扱える (シェルを経由しないため素通しできる)。"""
        base = tempfile.mkdtemp(prefix="snapshot test ")
        self.addCleanup(shutil.rmtree, base, ignore_errors=True)
        repo = make_repo(Path(base) / "my repo")
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", repo), (None, ""))
        self.assertEqual(git(repo, "show", f"{REF}:tracked.txt"), "v2-uncommitted")

    def test_parallel_invocations(self):
        """並行実行でも壊れない (一時 index の競合時は deny に落ちる)。"""
        hook_command(SCRIPT)  # 未移植の skip はスレッドの中では効かないため、先に判定する
        repo = self.temp_repo()
        results = []

        def call():
            results.append(run_hook(SCRIPT, "git reset --hard", repo))

        threads = [threading.Thread(target=call) for _ in range(2)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(len(results), 2, "並行実行で例外が出た")
        for decision, reason in results:
            self.assertIn(decision, (None, "deny"), reason)
        if all(decision is None for decision, _ in results):
            self.assertTrue(snapshot_ref_exists(repo))

    def test_argv_debug_path(self):
        """argv 経路では cwd が渡らないため、プロセスの cwd を対象にする。"""
        decision, reason = run_hook_argv(SCRIPT, "git reset --hard")
        self.assertEqual(decision, "deny")
        self.assertIn(FAILED, reason)


class WorktreeEnvironmentTest(unittest.TestCase):
    """git worktree 環境。「どの作業ツリーを保存したか」を必ず確認する。

    git は cwd / -C / --git-dir / --work-tree / GIT_DIR のどの指定でもベースが変わる。
    取り違えると「無関係な作業ツリーを保存し、本来の対象は無防備なまま破棄される」という
    このフックで最悪の壊れ方になるので、指定方法ごとに中身まで突き合わせる。

    構成: main (メイン作業ツリー) + wt1 / wt2 (リンク作業ツリー)。3 つとも HEAD の内容は
    同じで、未コミットの変更だけが違う。どれを保存したかは中身で判別できる。
    """

    def setUp(self):
        self.base = tempfile.mkdtemp(prefix="wt-env-test-")
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)
        self.main = make_repo(Path(self.base) / "main")
        git(self.main, "add", "-A")
        git(self.main, "commit", "-qm", "base")   # 作業ツリーを一度きれいにする
        self.wt1 = str(Path(self.base) / "wt1")
        self.wt2 = str(Path(self.base) / "wt2")
        git(self.main, "worktree", "add", "-q", "-b", "topic1", self.wt1)
        git(self.main, "worktree", "add", "-q", "-b", "topic2", self.wt2)
        for path, name in ((self.main, "main"), (self.wt1, "wt1"), (self.wt2, "wt2")):
            Path(path, "tracked.txt").write_text(f"{name}-uncommitted\n", encoding="utf-8")
            Path(path, f"only-in-{name}.txt").write_text(name, encoding="utf-8")

    def top(self, path):
        """メッセージに載る作業ツリーのパス。

        フックは git rev-parse --show-toplevel の値を使うので、macOS の
        /var -> /private/var のような symlink は解決済みになる。
        """
        return os.path.realpath(path)

    def assert_snapshot_of(self, name, ref=REF):
        """SNAPSHOT_REF の中身が name の作業ツリーであること。

        ref は worktree 間で共有されるので、読み出しはどの作業ツリーからでも同じ。
        """
        self.assertEqual(git(self.main, "show", f"{ref}:tracked.txt"),
                         f"{name}-uncommitted")
        self.assertEqual(git(self.main, "show", f"{ref}:only-in-{name}.txt"), name)
        self.assertIn(f"@ {self.top(getattr(self, name))}",
                      git(self.main, "log", "-1", "--format=%s", ref))

    def test_cwd_is_a_linked_worktree(self):
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", self.wt1), (None, ""))
        self.assert_snapshot_of("wt1")

    def test_cwd_is_the_main_worktree(self):
        self.assertEqual(run_hook(SCRIPT, "git clean -fd", self.main), (None, ""))
        self.assert_snapshot_of("main")

    def test_dash_c_into_linked_worktree(self):
        """cwd はメインだが -C でリンク作業ツリーを指す。"""
        self.assertEqual(run_hook(SCRIPT, f"git -C {self.wt1} reset --hard", self.main),
                         (None, ""))
        self.assert_snapshot_of("wt1")

    def test_dash_c_into_main_worktree(self):
        """cwd はリンク作業ツリーだが -C でメインを指す。"""
        self.assertEqual(run_hook(SCRIPT, f"git -C {self.main} checkout -- .", self.wt1),
                         (None, ""))
        self.assert_snapshot_of("main")

    def test_relative_dash_c_from_worktree(self):
        self.assertEqual(run_hook(SCRIPT, "git -C ../main reset --hard", self.wt1),
                         (None, ""))
        self.assert_snapshot_of("main")

    def test_cd_to_another_worktree(self):
        self.assertEqual(run_hook(SCRIPT, f"cd {self.wt2} && git restore .", self.wt1),
                         (None, ""))
        self.assert_snapshot_of("wt2")

    def test_subdirectory_inside_worktree(self):
        """作業ツリー内のサブディレクトリからでも、その作業ツリー全体を保存する。"""
        sub = Path(self.wt1) / "sub"
        sub.mkdir()
        (sub / "x.txt").write_text("x", encoding="utf-8")
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", str(sub)), (None, ""))
        self.assert_snapshot_of("wt1")
        self.assertEqual(git(self.wt1, "show", f"{REF}:sub/x.txt"), "x")

    def test_dash_c_into_subdirectory_of_another_worktree(self):
        """-C がサブディレクトリを指しても、その作業ツリーの toplevel まで登る。"""
        sub = Path(self.wt2) / "deep" / "nested"
        sub.mkdir(parents=True)
        (sub / "y.txt").write_text("y", encoding="utf-8")
        self.assertEqual(run_hook(SCRIPT, f"git -C {sub} reset --hard", self.wt1),
                         (None, ""))
        self.assert_snapshot_of("wt2")
        self.assertEqual(git(self.wt2, "show", f"{REF}:deep/nested/y.txt"), "y")

    def test_worktree_inside_the_main_worktree(self):
        """メイン作業ツリーの中に worktree を作った構成でも、メイン側の保存が成立する。"""
        inside = str(Path(self.main) / "inside-wt")
        git(self.main, "worktree", "add", "-q", "-b", "topic-inside", inside)
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", self.main), (None, ""))
        self.assert_snapshot_of("main")

    def test_temp_index_is_per_worktree(self):
        """一時 index は作業ツリーごとの git ディレクトリに置かれる。

        共有されると stat cache が混線し、別の作業ツリーの状態を保存しかねない。
        """
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", self.wt1), (None, ""))
        self.assert_snapshot_of("wt1")
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", self.main), (None, ""))
        self.assert_snapshot_of("main")

        gitdirs = {name: git(path, "rev-parse", "--absolute-git-dir")
                   for name, path in (("main", self.main), ("wt1", self.wt1),
                                      ("wt2", self.wt2))}
        self.assertEqual(len(set(gitdirs.values())), 3, gitdirs)
        for name in ("main", "wt1"):
            self.assertTrue(Path(gitdirs[name], "claude-wt-snapshot.index").exists(),
                            f"{name} の一時 index がない")
        # 触っていない wt2 には作られない
        self.assertFalse(Path(gitdirs["wt2"], "claude-wt-snapshot.index").exists())

    def test_ref_is_shared_but_reflog_identifies_the_worktree(self):
        """既知の挙動: ref は worktree 間で共有される。判別はメッセージの "@ <top>" で行う。"""
        run_hook(SCRIPT, "git reset --hard", self.wt1)
        first = git(self.wt1, "rev-parse", REF)
        run_hook(SCRIPT, "git clean -fd", self.wt2)

        # 先頭は後から破棄した wt2 のもの
        self.assertNotEqual(git(self.wt2, "rev-parse", REF), first)
        self.assert_snapshot_of("wt2")
        # wt1 のものは reflog から辿れる
        self.assertEqual(git(self.main, "rev-parse", f"{REF}@{{1}}"), first)
        self.assert_snapshot_of("wt1", ref=f"{REF}@{{1}}")

        lines = git(self.main, "reflog", "show", REF).splitlines()
        self.assertIn(f"@ {self.top(self.wt2)}", lines[0])
        self.assertIn(f"@ {self.top(self.wt1)}", lines[1])

    def test_parent_is_the_worktree_head(self):
        """親コミットはその作業ツリーの HEAD (別ブランチをチェックアウトしている)。"""
        run_hook(SCRIPT, "git reset --hard", self.wt1)
        self.assertEqual(git(self.main, "rev-parse", f"{REF}^"),
                         git(self.wt1, "rev-parse", "HEAD"))
        self.assertEqual(git(self.wt1, "rev-parse", "--abbrev-ref", "HEAD"), "topic1")

    def test_base_overrides_fall_to_deny(self):
        """--git-dir / --work-tree / GIT_DIR はベースが変わるので特定せず deny にする。"""
        gitdir = git(self.wt1, "rev-parse", "--absolute-git-dir")
        for cmd in (f"git --git-dir={gitdir} --work-tree={self.wt1} reset --hard",
                    f"GIT_DIR={gitdir} GIT_WORK_TREE={self.wt1} git reset --hard",
                    f"GIT_WORK_TREE={self.wt1} git clean -fd"):
            with self.subTest(cmd=cmd):
                decision, reason = run_hook(SCRIPT, cmd, self.main)
                self.assertEqual(decision, "deny")
                self.assertIn(UNRESOLVED, reason)
        self.assertFalse(snapshot_ref_exists(self.main), "deny なのに保存されている")

    def test_two_worktrees_are_both_saved(self):
        """2 つの作業ツリーを同時に破棄する形は、両方を保存してから通す。"""
        cmd = f"git -C {self.wt1} reset --hard && git -C {self.wt2} clean -fd"
        self.assertEqual(run_hook(SCRIPT, cmd, self.main), (None, ""))
        # 出現順に保存するので、ref の先頭は後から保存した wt2、wt1 は 1 つ前
        self.assert_snapshot_of("wt2")
        self.assert_snapshot_of("wt1", ref=f"{REF}@{{1}}")
        self.assertEqual(len(git(self.main, "reflog", "show", REF).splitlines()), 2)

    def test_script_with_several_cds_into_worktree(self):
        """heredoc で書いたスクリプト内に cd が複数あっても、破棄時点の作業ツリーを保存する。

        検証用スクリプト (worktree に入って一時変更 → 計測 → git checkout -- で戻し、
        サブディレクトリに出入りする) がこの形になる。
        """
        Path(self.wt1, "web").mkdir()
        cmd = (f"cat > run.sh <<'EOF'\n#!/bin/zsh\ncd {self.wt1} || exit 1\n"
               'f=internal/doc.go\necho "// touch" >> "$f"\ngo test ./...\n'
               'git checkout -- "$f"\ncd web\npnpm test\ncd ..\nEOF\nchmod +x run.sh && ./run.sh')
        self.assertEqual(run_hook(SCRIPT, cmd, self.main), (None, ""))
        self.assert_snapshot_of("wt1")
        self.assertEqual(len(git(self.main, "reflog", "show", REF).splitlines()), 1)

    def test_worktree_of_bare_repo(self):
        """bare クローンに worktree を足した構成 (メイン作業ツリーがない) でも保存できる。"""
        bare = str(Path(self.base) / "bare.git")
        git(self.base, "clone", "--bare", "-q", self.main, bare)
        wt = str(Path(self.base) / "from-bare")
        git(bare, "worktree", "add", "-q", "-b", "topic-bare", wt)
        Path(wt, "tracked.txt").write_text("bare-uncommitted\n", encoding="utf-8")
        Path(wt, "only-in-bare.txt").write_text("bare", encoding="utf-8")

        self.assertEqual(run_hook(SCRIPT, "git reset --hard", wt), (None, ""))
        self.assertEqual(git(wt, "show", f"{REF}:tracked.txt"), "bare-uncommitted")
        self.assertEqual(git(wt, "show", f"{REF}:only-in-bare.txt"), "bare")
        # ref は共通ディレクトリ (= bare 側) に置かれ、元のリポジトリには作られない
        self.assertTrue(snapshot_ref_exists(bare))
        self.assertFalse(snapshot_ref_exists(self.main))
        self.assertIn(f"@ {self.top(wt)}", git(wt, "log", "-1", "--format=%s", REF))

    def test_worktree_added_then_immediately_discarded(self):
        """作ったばかりの worktree (未コミットが未追跡ファイルだけ) でも保存できる。"""
        fresh = str(Path(self.base) / "fresh")
        git(self.main, "worktree", "add", "-q", "-b", "topic3", fresh)
        Path(fresh, "generated.txt").write_text("generated\n", encoding="utf-8")
        self.assertEqual(run_hook(SCRIPT, "git clean -fd", fresh), (None, ""))
        self.assertEqual(git(fresh, "show", f"{REF}:generated.txt"), "generated")

    def test_clean_worktree_creates_no_ref(self):
        """作業ツリーがきれいなら ref を作らない (他の作業ツリーが汚れていても)。"""
        clean = str(Path(self.base) / "cleanwt")
        git(self.main, "worktree", "add", "-q", "-b", "topic4", clean)
        self.assertEqual(run_hook(SCRIPT, "git reset --hard", clean), (None, ""))
        self.assertFalse(snapshot_ref_exists(self.main))

    def test_worktree_paths_are_resolvable(self):
        """L2: 作業ツリーを指す各種の書き方が対象ディレクトリまで解決できる。"""
        mod = load_hook(SCRIPT)
        cases = [
            ("git reset --hard", self.wt1, [self.wt1]),
            (f"git -C {self.wt1} reset --hard", self.main, [self.wt1]),
            (f"cd {self.wt2} && git reset --hard", self.wt1, [self.wt2]),
            ("git -C ../wt2 reset --hard", self.wt1, [self.wt2]),
            ("cd ../main && git reset --hard", self.wt1, [self.main]),
            ("cd ../main && cd ../wt2 && git reset --hard", self.wt1, [self.wt2]),
            (f"git reset --hard && git -C {self.wt2} clean -fd", self.wt1, [self.wt1, self.wt2]),
        ]
        for cmd, cwd, expected in cases:
            with self.subTest(cmd=cmd):
                got = mod.resolve_target_dirs(cmd, cwd)
                self.assertIsNotNone(got, f"{cmd!r} が特定失敗になった")
                self.assertEqual([os.path.normpath(p) for p in got],
                                 [os.path.normpath(p) for p in expected])


if __name__ == "__main__":
    unittest.main()
