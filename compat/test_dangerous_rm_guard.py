#!/usr/bin/env python3
"""dangerous-rm-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）

判定は Claude Code 組み込み (V7r / lal / g9v) の移植なので、「組み込みが ask を出す形だけを
deny する」ことを両側から確認する。通過ケース (誤爆しないこと) を厚めに置く。

cwd に依存する判定 (作業ディレクトリとその祖先) があるため、実在する一時ディレクトリを
fixture として使う。realpath 解決が絡むので、symlink 越しでも一致することまで見る。
"""
import json
import os
import shutil
import tempfile
import time
import unittest

from helpers import HookTestCase, load_hook, run_hook, run_hook_argv

SCRIPT = "dangerous-rm-guard.py"
SEED = "rm"  # 一次ゲートを通すための種文字列

# ブロック理由に現れるラベル (どのルールが発火したかの判別用)
L_EMPTY_VAR = "空になりうる変数"
L_CMDSUB = "コマンド置換の中"
L_TOO_MANY = "コマンド置換が多すぎる"
L_UNRESOLVABLE = "静的に解決できない"
L_CRITICAL = "システム重要ディレクトリ"
L_CWD = "作業ディレクトリ自身が削除対象"
L_ANCESTOR = "作業ディレクトリの親"

# 調査のきっかけになった実物 (worktree を 5 個作るスクリプト)
REAL_WORLD = """set -e
BASE=/private/tmp/scratchpad

mk_wt() {
  local name="$1"
  rm -rf "$BASE/$name"
  git worktree add -b "test-branch-$name" "$BASE/$name" HEAD --quiet
}

mk_wt test-a-clean-old
"""


class EmptyVariableTest(HookTestCase):
    """空になりうる変数を対象にした削除 (組み込みの lal / A6V)。"""

    script = SCRIPT

    def test_real_world_case(self):
        self.assert_decision(REAL_WORLD, "deny", '"$BASE/$name"')

    def test_variable_followed_by_expansion(self):
        self.check_table([
            ('rm -rf "$BASE/$name"', L_EMPTY_VAR),
            ("rm -rf $BASE/$name", L_EMPTY_VAR),
            ("rm -rf ${BASE}/$name", L_EMPTY_VAR),
            ('rm -rf "${BASE}/${name}"', L_EMPTY_VAR),
        ], expected="deny")

    def test_variable_followed_by_glob(self):
        """`rm -rf $UNSET/*` → `rm -rf /*` に化ける形。"""
        self.check_table([
            "rm -rf $UNSET/*",
            'rm -rf "$UNSET/*"',
            "rm -rf ${UNSET}/*",
            "rm -r $DIR/**",
        ], expected="deny")

    def test_variable_followed_by_separator_or_end(self):
        self.check_table([
            'rm -rf "$DIR/"',
            "rm -rf $DIR/",
            "rm -rf $DIR//sub",
            "rmdir $DIR/",
        ], expected="deny")

    def test_rmdir(self):
        # `rmdir -p` + glob は先に「静的に解決できない」側で捕まる (UnresolvableTest 参照)
        self.check_table([
            ("rmdir $DIR/*", L_EMPTY_VAR),
            ("rmdir -- ${DIR}/*", L_EMPTY_VAR),
        ], expected="deny")

    def test_invocation_forms(self):
        """先行代入・パス指定・エスケープつきの起動。"""
        self.check_table([
            "TMPDIR=/tmp rm -rf $DIR/*",
            "FOO=1 BAR=2 rm -rf $DIR/*",
            "/bin/rm -rf $DIR/*",
            "./rm -rf $DIR/*",
            "\\rm -rf $DIR/*",
        ], expected="deny")

    def test_shell_separators(self):
        self.check_table([
            "echo start && rm -rf $DIR/*",
            "echo start; rm -rf $DIR/*",
            "echo start\nrm -rf $DIR/*",
            "echo start | rm -rf $DIR/*",
            "false || rm -rf $DIR/*",
            "rm -rf $DIR/* &",
            "(rm -rf $DIR/*)",
            "{ rm -rf $DIR/*; }",
        ], expected="deny")

    def test_option_and_redirect_are_skipped(self):
        self.check_table([
            "rm -rf -- $DIR/*",
            "rm -rf 2>/dev/null $DIR/*",
            "rm -rf 2> /dev/null $DIR/*",
            "rm -rf $DIR/* > log.txt",
        ], expected="deny")

    def test_line_continuation(self):
        self.assert_decision("rm -rf \\\n  $DIR/*", "deny", L_EMPTY_VAR)

    def test_reason_bounds_the_guarded_expansion_it_recommends(self):
        """案内する `${BASE:?}` は固定名までしか続けられない。そこまで書いてあること。

        `:?` の `?` が glob 文字として数えられるため、glob を続けると別分岐で止まる。
        それを書かずに勧めると、案内どおりに直しても通らない堂々巡りになる。
        """
        _decision, reason = run_hook(SCRIPT, 'rm -rf "$BASE/$name"')
        self.assertIn("${BASE:?}", reason)
        self.assertIn("glob を続けると", reason)
        self.assert_decision('rm -rf "${BASE:?}"/work', None)
        self.assert_decision('rm -rf "${BASE:?}"/*', "deny", L_UNRESOLVABLE)


class SubstitutionTest(HookTestCase):
    """コマンド置換まわり (組み込みの g9v)。"""

    script = SCRIPT

    def test_dangerous_removal_inside_substitution(self):
        self.check_table([
            ("echo $(rm -rf $D/*)", L_CMDSUB),
            ("echo `rm -rf $D/*`", L_CMDSUB),
            ('echo "$(rm -rf ${D}/$name)"', L_CMDSUB),
        ], expected="deny")

    def test_too_many_substitutions(self):
        command = "rm -rf ./build " + "$(true) " * 65
        self.assert_decision(command, "deny", L_TOO_MANY)

    def test_substitution_count_under_limit_passes(self):
        command = "rm -rf ./build " + "$(true) " * 10
        self.assert_decision(command, None)

    def test_substitution_is_not_mistaken_for_a_literal_path(self):
        """`"$(pwd)/build"` を `/build` と読んで「最上位ディレクトリの削除」にしないこと。

        置換を空白で潰すと `"` と `/build"` の 2 引数に割れ、`/build` が最上位ディレクトリに
        見える。組み込みは sentinel トークンへ置き換えるので、実体パス判定もそれに揃える。
        """
        self.check_table([
            'rm -rf "$(pwd)/build"',
            'rm -rf "$(git rev-parse --show-toplevel)/tmp"',
            'rm -rf "`pwd`/build"',
            'rm -rf "$(mktemp -d)/x"',
            "rm -rf $(cat /tmp/path.txt)",
        ], expected=None)

    def test_substitution_with_trailing_glob_is_denied(self):
        """置換 + 末尾 glob は組み込みが bypass 免疫の ask を出す形 (V7r 分岐 B の `e_(p)`)。

        置換の結果は解決できなくても対象 1 個としては扱えるが、glob が付くと当たり先が
        確定しない。ここを通すと組み込み側でダイアログになるので、先回りで deny する。
        """
        self.check_table([
            ('rm -rf "$(pwd)"/*', L_UNRESOLVABLE),
            ("rm -rf $(git rev-parse --show-toplevel)/tmp/*", L_UNRESOLVABLE),
            ('rm -rf "`pwd`"/*', L_UNRESOLVABLE),
        ], expected="deny")

    def test_substitution_glob_message_points_at_the_substitution(self):
        _decision, reason = run_hook(SCRIPT, 'rm -rf "$(pwd)"/*')
        self.assertIn("置換だけを単独で実行", reason)

    def test_internal_token_is_not_shown(self):
        """内部トークンは書いた覚えのない文字列なので、`$(…)` に戻して見せる。"""
        _decision, reason = run_hook(SCRIPT, 'rm -rf "$(pwd)"/*')
        self.assertNotIn("__CMDSUB__", reason)
        self.assertIn("$(…)", reason)

    def test_other_targets_in_the_same_segment_are_still_checked(self):
        """置換を潰しても、同じ断片の別の対象は見落とさない。"""
        self.assert_decision('rm -rf "$(pwd)/build" /usr', "deny", L_CRITICAL)


class UnresolvableTest(HookTestCase):
    """静的に解決できない削除対象 (組み込みの V7r 分岐 A / B / D)。"""

    script = SCRIPT

    def test_cd_with_relative_glob(self):
        """分岐 A: cd 後の相対 glob は最終的な作業ディレクトリが確定しない。"""
        self.check_table([
            ("cd sub && rm -rf ./*", L_UNRESOLVABLE),
            ("cd /tmp/work && rm -rf *", L_UNRESOLVABLE),
            ("pushd sub; rm -rf ./*", L_UNRESOLVABLE),
        ], expected="deny")

    def test_upward_and_tilde_forms(self):
        """分岐 B: `..` で上に抜ける形、`~user` 形、`*/` で終わる相対指定。"""
        self.check_table([
            ("rm -rf ./sub/../*", L_UNRESOLVABLE),
            ("rm -rf ~someone/*", L_UNRESOLVABLE),
            ("rm -rf dist/*/", L_UNRESOLVABLE),
        ], expected="deny")

    def test_rmdir_parents_with_glob(self):
        """`rmdir -p` は親をたどって消すので、glob との併用は範囲が確定しない。

        空変数の形もこちらが先に発火する (どちらも書き直しを促す REWRITE 系)。
        """
        self.check_table([
            ("rmdir -p build/*", L_UNRESOLVABLE),
            ("rmdir --parents build/*", L_UNRESOLVABLE),
            ("rmdir -p ${DIR}/*", L_UNRESOLVABLE),
        ], expected="deny")

    def test_glob_traversing_multiple_levels(self):
        """分岐 D: 列挙できない階層を跨ぐ glob。"""
        self.check_table([
            ("rm -rf a/*/b/*", L_UNRESOLVABLE),
            ("rm -rf ./*/*", L_UNRESOLVABLE),
        ], expected="deny")

    def test_single_level_glob_passes(self):
        """1 階層だけの glob は組み込みも ask にしない。"""
        self.check_table([
            "rm -rf node_modules/*",
            "rm -rf ./build/*",
            "rm -rf /tmp/work/foo/*",
            "rm -rf tmp/*/cache",  # glob が 1 つなら階層が深くても通る
        ], expected=None)

    def test_reason_is_specific_per_branch(self):
        """A / B / D で理由と対処を分けること。

        1 本にまとめると、分岐 A の正解である `rm -rf tmp/*` (cd を外した同じ範囲) まで
        「検出だけ外す書き換え」として禁じているように読めてしまう。
        """
        _decision, cd_reason = run_hook(SCRIPT, "cd sub && rm -rf ./*")
        self.assertIn("`cd` を外して", cd_reason)
        self.assertIn("rm -rf tmp/*", cd_reason)

        _decision, shape_reason = run_hook(SCRIPT, "rmdir -p build/*")
        self.assertIn("`-p`", shape_reason)
        self.assertIn("親ディレクトリ", shape_reason)

        _decision, glob_reason = run_hook(SCRIPT, "rm -rf a/*/b/*")
        self.assertIn("1 階層だけなら通る", glob_reason)

    def test_advice_for_the_cd_branch_actually_passes(self):
        """分岐 A の案内どおりに書き直したものが、実際に通ること。"""
        self.assert_decision("rm -rf tmp/*", None)

    def test_cd_branch_does_not_print_a_resolved_path(self):
        """基準の作業ディレクトリが不定なのだから、解決後のパスを断定して見せない。"""
        _decision, reason = run_hook(SCRIPT, "cd sub && rm -rf ./*")
        self.assertNotIn("→", reason.splitlines()[0])


class ProtectedPathTest(HookTestCase):
    """システム重要ディレクトリ (組み込みの Kar)。"""

    script = SCRIPT

    def test_root_and_top_level(self):
        self.check_table([
            ("rm -rf /", L_CRITICAL),
            ("rm -rf /*", L_CRITICAL),
            ("rm -rf /usr", L_CRITICAL),
            ("rm -rf /etc/", L_CRITICAL),
            ("rm -rf /opt", L_CRITICAL),
            ("rm -rf /usr/*", L_CRITICAL),
        ], expected="deny")

    def test_home_directory(self):
        self.check_table([
            ("rm -rf ~", L_CRITICAL),
            ("rm -rf ~/", L_CRITICAL),
            ("rm -rf ~/*", L_CRITICAL),
        ], expected="deny")

    def test_known_variable_expands_to_home(self):
        """組み込みが実値へ展開する唯一の変数。`Wr()` の `if(s==="HOME") return Am()` に対応。"""
        self.check_table([
            ("rm -rf $HOME", L_CRITICAL),
            ('rm -rf "$HOME"', L_CRITICAL),
            ("rm -rf $HOME/", L_CRITICAL),
            ("rm -rf $HOME/*", L_CRITICAL),
        ], expected="deny")

    def test_brace_form_is_denied_as_a_deliberate_exception(self):
        """`${HOME}` は組み込みが too-complex に落とす形。ask かは未確認だが deny 側へ寄せる。"""
        self.check_table([
            ("rm -rf ${HOME}", L_CRITICAL),
            ('rm -rf "${HOME}"', L_CRITICAL),
        ], expected="deny")

    def test_known_variable_survives_the_trailing_closer_strip(self):
        """`${HOME}` の `}` を閉じ括弧として落としてから展開すると一致しなくなる。"""
        _decision, reason = run_hook(SCRIPT, "rm -rf ${HOME}")
        self.assertIn(os.path.expanduser("~"), reason)

    def test_reassignment_in_the_same_command_is_not_tracked(self):
        """再代入は追わない。組み込みもこの形は追えず critical path の ask を出す (実測)。

        安全な形まで止まるので、案内先のリテラル指定が通ることまで確認する。
        """
        reassigned = "S=/tmp/sandbox; export HOME=$S/home; rm -rf $HOME"
        self.assert_decision(reassigned, "deny", L_CRITICAL)
        self.assert_decision("S=/tmp/sandbox; rm -rf /tmp/sandbox/home", None)

    def test_other_variables_are_left_literal(self):
        """既知変数だけを展開する。名前が前方一致するだけの変数を巻き込まない。"""
        self.check_table([
            "rm -rf $HOMEBREW_PREFIX/x",
            "rm -rf ${HOME}x/y",
            "rm -rf $HOME/.cache/mytool",
            'rm -rf "$HOME/.cache/mytool/*"',
        ], expected=None)

    def test_reason_offers_alternatives_before_stopping(self):
        """止める前に「消さずに済ます」「範囲を狭める」を順に提示していること。"""
        _decision, reason = run_hook(SCRIPT, "rm -rf /usr")
        self.assertIn("削除せずに済ませられないか", reason)
        self.assertIn("範囲を狭められないか", reason)
        self.assertIn("判断を仰ぐ", reason)
        self.assertIn("echo", reason)          # 変数の展開結果を確認せよ、まで書いてある
        self.assertIn("`/usr/*`", reason)      # 同じ範囲の別記法が禁止だと明示している

    def test_target_shows_the_resolved_path(self):
        """`~` のように生の表記では何が消えるか分からない対象は、解決後まで出す。"""
        _decision, reason = run_hook(SCRIPT, "rm -rf ~")
        self.assertIn(os.path.expanduser("~"), reason)

    def test_deep_paths_pass(self):
        self.check_table([
            "rm -rf /tmp/work/foo",
            "rm -rf /usr/local/share/mything",
            "rm -f ~/.cache/mytool/x.log",
        ], expected=None)


class WorkspaceTest(HookTestCase):
    """作業ディレクトリとその祖先 (組み込みの MF)。"""

    script = SCRIPT

    @classmethod
    def setUpClass(cls):
        cls.root = os.path.realpath(tempfile.mkdtemp(prefix="rm-guard-"))
        cls.deep = os.path.join(cls.root, "a", "b", "c")
        os.makedirs(os.path.join(cls.deep, "sub"))

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.root, ignore_errors=True)

    def test_ancestors(self):
        self.check_table([
            ("rm -rf ..", L_ANCESTOR),
            ("rm -rf ../..", L_ANCESTOR),
            (f"rm -rf {self.root}/a", L_ANCESTOR),
            (f"rm -rf {self.root}/a/b", L_ANCESTOR),
        ], expected="deny", cwd=self.deep)

    def test_cwd_itself(self):
        self.check_table([
            ("rm -rf .", L_CWD),
            ("rm -rf *", L_CWD),
            (f"rm -rf {self.deep}", L_CWD),
        ], expected="deny", cwd=self.deep)

    def test_pwd_is_not_expanded(self):
        """組み込みは PWD をセンチネルに置き換えるだけで実値に展開しない (ask にならない)。"""
        self.check_table([
            "rm -rf $PWD",
            "rm -rf ${PWD}",
            "rm -rf $PWD/sub",
        ], expected=None, cwd=self.deep)

    def test_cwd_message_allows_narrowing(self):
        """cwd そのものは「実体パスで列挙して再実行してよい」側にする。"""
        _decision, reason = run_hook(SCRIPT, "rm -rf *", self.deep)
        self.assertIn("実体パスで列挙", reason)

    def test_cwd_message_offers_the_dedicated_command_first(self):
        """cwd ごと消したい最頻ケースは worktree の後始末なので、専用コマンドを先に出す。"""
        _decision, reason = run_hook(SCRIPT, f"rm -rf {self.deep}", self.deep)
        self.assertIn("worktree remove", reason)

    def test_cwd_message_does_not_overstate_the_target(self):
        """`./*` は中身だけなので「作業ディレクトリごと消える」と断定しない。"""
        _decision, reason = run_hook(SCRIPT, "rm -rf ./*", self.deep)
        self.assertIn("中身を指す形", reason)
        self.assertNotIn("ごと消す", reason)

    def test_relative_target_shows_the_resolved_path(self):
        """`..` は解決後のパスまで出す (`..` の数を数え直させない)。"""
        _decision, reason = run_hook(SCRIPT, "rm -rf ..", self.deep)
        self.assertIn(os.path.dirname(self.deep), reason)

    def test_ancestor_message_offers_worktree_command(self):
        _decision, reason = run_hook(SCRIPT, "rm -rf ..", self.deep)
        self.assertIn("worktree remove", reason)
        self.assertIn("cd", reason)  # cd で判定を外す形の禁止まで書いてある

    def test_children_pass(self):
        self.check_table([
            "rm -rf ./sub",
            "rm -rf sub",
            f"rm -rf {self.deep}/sub",
            "rm -f ./sub/x.log",
        ], expected=None, cwd=self.deep)


class AllowedTest(HookTestCase):
    """通過させるべきコマンド。誤爆がこのフックの実害なので厚く見る。"""

    script = SCRIPT

    def test_literal_paths(self):
        self.check_table([
            "rm -rf /tmp/work/foo",
            "rm -rf ./build",
            "rm -rf node_modules",
            "rm -f /tmp/$name.log",
        ], expected=None)

    def test_variable_with_concrete_child(self):
        """`$VAR/` の直後が普通の文字。組み込みも ask にしない形。"""
        self.check_table([
            'rm -rf "$BASE/blob.txt"',
            "rm -rf $BASE/build",
            "rm -f ${BASE}/tmp.log",
        ], expected=None)

    def test_guarded_expansion_passes(self):
        """理由文で案内する書き直し先が、実際に通ること。"""
        self.check_table([
            'rm -rf "${BASE:?}/$name"',
            'rm -rf "${BASE:?BASE unset}/$name"',
            "rm -rf ${BASE:-/tmp/fallback}/*",
        ], expected=None)

    def test_bare_variable_without_slash(self):
        self.check_table([
            "rm -f $file",
            'rm -rf "$DIR"',
            "rm -rf $DIR*",
        ], expected=None)

    def test_no_removal_command(self):
        self.check_table([
            'echo "$BASE/$name"',
            'ls "$BASE/"*',
            "git worktree remove --force $BASE/$name",
            "terraform destroy -target=$MOD/*",  # "terraform" が一次ゲートの "rm" を含む
            'cat "$DIR/"*.log',
            "rm --help",
        ], expected=None)

    def test_removal_word_inside_other_token(self):
        self.check_table([
            "npm run build:$TARGET/*",
            "confirm_rm $DIR/*",
            "echo rm -rf $DIR/*",
        ], expected=None)

    def test_no_gate_keyword(self):
        self.check_table([
            "ls -la",
            "git status",
        ], expected=None)


class UnitTest(unittest.TestCase):
    """L2: 判定関数を直接呼ぶ。"""

    @classmethod
    def setUpClass(cls):
        cls.hook = load_hook(SCRIPT, seed=SEED)

    def test_returns_command_and_target(self):
        self.assertEqual(self.hook.find_dangerous_removal('rm -rf "$BASE/$name"'),
                         ("rm", '"$BASE/$name"'))
        self.assertEqual(self.hook.find_dangerous_removal("rmdir ${D}/*"),
                         ("rmdir", "${D}/*"))
        self.assertIsNone(self.hook.find_dangerous_removal("rm -rf $BASE/build"))

    def test_a6v_boundary(self):
        """A6V が受ける「`$VAR/` の直後」の文字集合。"""
        for tail, expected in (("*", True), ("$", True), ("/", True),
                               ('"', True), ("'", True), ("", True),
                               ("a", False), (".", False), ("-", False)):
            with self.subTest(tail=tail):
                self.assertEqual(bool(self.hook.A6V.match(f"$V/{tail}")), expected)

    def test_l6v_accepts_assignment_prefix(self):
        self.assertIsNotNone(self.hook.L6V.match("A=1 B=2 rm -rf x"))
        self.assertIsNotNone(self.hook.L6V.match("/usr/bin/rmdir x"))
        self.assertIsNone(self.hook.L6V.match("confirm_rm x"))
        self.assertIsNone(self.hook.L6V.match("echo rm x"))

    def test_command_substitution_is_stripped_from_outer_scan(self):
        """外側の走査では置換の中身を見ない (中身は find_in_substitution の担当)。"""
        self.assertIsNone(self.hook.find_dangerous_removal("echo $(rm -rf $D/*)"))
        self.assertIsNotNone(self.hook.find_in_substitution("echo $(rm -rf $D/*)"))

    def test_is_critical_path(self):
        home = os.path.expanduser("~")
        for path, expected in (("/", True), ("/usr", True), ("/etc/", True),
                               (home, True), ("/*", True),
                               ("/usr/local/share/x", False),
                               (f"{home}/.cache", False)):
            with self.subTest(path=path):
                self.assertEqual(self.hook.is_critical_path(path), expected)

    def test_removes_workspace(self):
        base = "/tmp/work/a/b"
        self.assertTrue(self.hook.removes_workspace(base, "/tmp/work/a/b"))   # 一致
        self.assertTrue(self.hook.removes_workspace(base, "/tmp/work/a"))     # 祖先
        self.assertTrue(self.hook.removes_workspace(base, "/tmp"))            # 祖先
        self.assertFalse(self.hook.removes_workspace(base, "/tmp/work/a/b/c"))  # 子
        self.assertFalse(self.hook.removes_workspace(base, "/other"))

    def test_escapes_upward(self):
        self.assertTrue(self.hook.escapes_upward("sub/../*"))
        self.assertTrue(self.hook.escapes_upward("./a/.."))
        self.assertFalse(self.hook.escapes_upward("../../x"))  # 先頭の .. だけなら false
        self.assertFalse(self.hook.escapes_upward("a/b"))

    def test_strip_glob_tail(self):
        self.assertEqual(self.hook.strip_glob_tail("/a/b/*"), "/a/b")
        self.assertEqual(self.hook.strip_glob_tail("/a/b/*/"), "/a/b")
        self.assertEqual(self.hook.strip_glob_tail("/a/b/**"), "/a/b")
        self.assertEqual(self.hook.strip_glob_tail("/*"), "/")
        self.assertEqual(self.hook.strip_glob_tail("/a/b"), "/a/b")

    def test_positional_args(self):
        self.assertEqual(self.hook.positional_args(["-rf", "a", "b"]), ["a", "b"])
        self.assertEqual(self.hook.positional_args(["-rf", "--", "-x"]), ["-x"])
        self.assertEqual(self.hook.positional_args(["-rf"]), [])

    def test_has_directory_change(self):
        self.assertTrue(self.hook.has_directory_change("cd sub && rm -rf ./*"))
        self.assertTrue(self.hook.has_directory_change("pushd sub; rm -rf ./*"))
        self.assertFalse(self.hook.has_directory_change("rm -rf ./*"))
        self.assertFalse(self.hook.has_directory_change("echo cd sub"))


class ContractTest(HookTestCase):
    """入力の形が壊れていてもフックが止まらないこと。"""

    script = SCRIPT

    def test_argv_path_matches_stdin_path(self):
        self.assertEqual(run_hook_argv(SCRIPT, 'rm -rf "$BASE/$name"')[0], "deny")
        self.assertEqual(run_hook_argv(SCRIPT, "rm -rf $BASE/build")[0], None)

    def test_malformed_payload(self):
        for raw in ("", "not json rm", "[]", "null",
                    json.dumps({"tool_input": None}),
                    json.dumps({"tool_input": {}}),
                    json.dumps({"tool_input": {"command": None}}),
                    json.dumps({"tool_input": {"command": "rm -rf ."}, "cwd": None}),
                    json.dumps({"tool_input": {"command": "rm -rf ."}, "cwd": ""})):
            with self.subTest(raw=raw):
                run_hook(SCRIPT, None, raw=raw)

    def test_long_command_is_handled(self):
        padding = "x" * 20000
        self.assertEqual(run_hook(SCRIPT, f"echo {padding}")[0], None)
        self.assertEqual(run_hook(SCRIPT, f"echo {padding}; rm -rf $D/*")[0], "deny")

    def test_pathological_input_is_fast(self):
        """病的に長い入力でも括弧の潰し込みが破綻しない。"""
        cases = [
            "rm -rf $D/* " + "2>/dev/null " * 2000,
            "rm -rf " + "$D/x " * 2000 + "$D/*",
            "echo " + "()" * 2000 + "; rm -rf $D/*",
            "echo " + "(" * 2000 + ")" * 2000 + "; rm -rf x",
            "echo " + "`x` " * 2000 + "; rm -rf x",
            "rm -rf " + "a/" * 2000 + "*",
        ]
        start = time.perf_counter()
        for command in cases:
            run_hook(SCRIPT, command)
        self.assertLess(time.perf_counter() - start, 20.0)


if __name__ == "__main__":
    unittest.main()
