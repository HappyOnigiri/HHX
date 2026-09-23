#!/usr/bin/env python3
"""Python 実装と hhx の差分テスト (irreversible-guard・dangerous-rm-guard・discard-guard)。

実行: HHX_COMPAT_PYTHON_HOOKS=<Python 本体のディレクトリ> make compat-test（compat/README.md を参照）

同じ入力を Python 本体 (load_python_hook で読み込み、プロセス内で main を呼ぶ) と hhx (`hhx hook <name>`) の
両方に通し、判定と理由文が一致するかを比べる。理由文には発火したルールのラベルと、抽出したトークン
(秘密ファイルのパス、解決後の削除対象など) が入るので、判定・ラベル・トークンをまとめて比べることになる。

入力は既存のテストがフックに渡している入力 (表・payload) を集めたものと、それを変形した大量の入力にする。
変形では区切り文字・クォート・`>`・`.pub`・`.env.<x>`・Unicode の空白や語の文字などの境界の断片を差し込む。
先読み・後読みの書き換えと Unicode の扱いは、手書きの表だけでは取りこぼしやすいためである。

HHX_DIFF_SEED で乱数の種を、HHX_DIFF_CASES で hook ごとの変形の数を変えられる。
"""
import concurrent.futures
import contextlib
import io
import json
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

import helpers
from helpers import hook_command_for_name, load_python_hook

SEED = int(os.environ.get("HHX_DIFF_SEED", "20260923"))
CASES = int(os.environ.get("HHX_DIFF_CASES", "3000"))

SEPARATORS = (" ", "  ", "\t", ";", "|", "&", "&&", "||", "\n", " ; ", " | ", " && ")

# Unicode の空白 (\s)・語の文字 (\w)・数字 (\d) で、Python と Go の既定の範囲が違うもの。
UNICODE_FRAGMENTS = ("\u3000", "\xa0", "\x1c", "\x85", "é", "日本", "٣", "²", "Σ", "İ", "ß")

IRREVERSIBLE_FRAGMENTS = (
    " ", "/", ".", "-", "@", "_", "=", "'", '"', "(", ")", ">", ">>", " > ", ">|", "<", "1", "~/", "/tmp/", "a/b/",
    ".env", ".envrc", ".env.local", ".env.example", ".env.examples", ".env.example.bak", ".env.-x", ".env..x",
    ".env.", ".envrc.", ".envrc.x", "envrc.local", "envrc.localx", ".ssh", ".ssh/", ".ssh/id_rsa", ".ssh/id@x",
    ".ssh/id.pub", ".ssh/.pub", ".ssh/x.pub@y", ".ssh/known_hosts", ".ssh/known_hostsx", ".ssh/config",
    ".ssh/configx", ".ssh/config.d", ".ssh/config@x", ".pub", ".pem", "x.pem", "a.pem@b", "a.pem.bak", ".pem/",
    "example", "sample", "template", "dist", "rc",
    "rm ", "rm -f ", "mv ", "mv -f ", "mv -- ", "tee ", "tee -a ", "tee - ", "sed -i ", "perl -i ", "sed ",
    "unlink ", "shred -u ", "truncate -s 0 ", "srm ", "trash ", "cat ", "cp ", "/bin/", "rtk ", "sudo ",
    "gh ", "git ", "curl ", "wget ", "api ", "auth logout", "auth logoutx", "secret delete", "repo delete",
    "-X DELETE ", "-XDELETE ", "-X delete ", "--method=DELETE ", "--method ", "--dry-run", "-X POST ",
    "https://api.github.com/", "https://x.googleapis.com/", "s3.amazonaws.com", "revoke", "credentials/revoke",
    "delete", "deletex", "delete-x", "gcloud ", "aws ", "s3 rm ", "s3 rb ", "terraform ", "tofu ", "destroy",
    "apply ", "-destroy", "-chdir=x ", "mysqladmin drop", "psql -c 'DROP TABLE t'", "mysql ", "sqlite3 ",
    "TRUNCATE ", "drop table", "DROP  SCHEMA", "npm publish", "pnpm unpublish", "yarn npm publish", "gem push",
    "twine upload", "cargo yank", "security delete-x", "security -q delete-y", "gpg --delete-secret-key",
    "gpg2 ", "op item delete", "npm token revoke", "diskutil eraseDisk", "diskutil zeroDisk", "diskutil apfs delete",
    "tmutil delete", "reflog expire", "gc --prune=now", "gc x --prune=all", "gcx --prune=now", "stash clear",
    "--force-delete-without-recovery", "~/.claude/settings.json", "~/.claude/settings.local.json",
    "~/.codex/hooks.json", "~/.codex/config.toml", "__pycache__", ".DS_Store",
) + UNICODE_FRAGMENTS

RM_FRAGMENTS = (
    " ", "/", "//", ".", "..", "../", "./", "*", "/*", "**", "*/", "?", "[a]", "-", "-rf ", "-p ", "--parents ",
    "-- ", "rm ", "rmdir ", "rm -rf ", "/bin/rm ", "\\rm ", "X=1 ", "A+=b ", "cd ", "cd sub && ", "pushd x; ",
    "$", "$D", "$D/", "${D}", "${D}/", '"$D/', "$HOME", "${HOME}", "$HOMEBREW", "$HOME_x", "$PWD", "${D:?}",
    "~", "~/", "~x/", "'", '"', "`", "`pwd`", "$(", ")", "$(pwd)", "$(true)", "$(rm -rf $D/*)", "(", "{ ", "}",
    "]", ";", "|", "&", "&&", "||", "\n", "\r", "\\\n", "2>", "2>/dev/null ", ">", ">>", "&>", ">&", "<", "<<<",
    "1", "\t", "/private", "/private/tmp", "/PRIVATE/VAR", "/private/etcx", "/tmp", "/usr", "/etc", "/Users",
    "{ROOT}", "{DEEP}", "{HOME}", "{REALHOME}", "{LINK}",
) + UNICODE_FRAGMENTS

# 意図して変えた G 類 (ガードファイル) の保護対象。これを含む入力は Python と hhx で判定が違ってよいので比べない。
CHANGED_GUARD = re.compile(r"\.claude/hooks|\.codex/hooks(?!\.json)|agent-config|hhx")


def parse_output(stdout):
    if not stdout.strip():
        return None, ""
    out = json.loads(stdout)["hookSpecificOutput"]
    return out["permissionDecision"], out["permissionDecisionReason"]


def run_python(module, gate, raw=None, argv=None):
    """Python 本体を、実運用と同じ入力でプロセス内で起動する。本体の例外は無出力 (hook の失敗) として扱う。"""
    text = argv if argv is not None else raw
    if not gate(text):
        return None, ""
    saved = sys.argv, module.RAW
    sys.argv = ["hook"] if argv is None else ["hook", argv]
    module.RAW = text
    out = io.StringIO()
    try:
        with contextlib.redirect_stdout(out):
            module.main()
    except SystemExit:
        pass
    except Exception:  # noqa: BLE001  本体の例外は hook の失敗で、agent には無出力と同じに見える
        return None, ""
    finally:
        sys.argv, module.RAW = saved
    return parse_output(out.getvalue())


def run_hhx(name, raw=None, argv=None, cwd=None):
    command = hook_command_for_name(name) + ([] if argv is None else [argv])
    proc = subprocess.run(command, input="" if raw is None else raw, capture_output=True, text=True,
                          encoding="utf-8", timeout=120, cwd=cwd or os.getcwd())
    if proc.returncode != 0:
        raise AssertionError(f"hhx が 0 以外で終了しました (rc={proc.returncode}): {proc.stderr!r}")
    return parse_output(proc.stdout)


def harvest(module):
    """テストモジュールがフックに渡している入力を集める。フックは起動せず、判定の確認は失敗させて読み捨てる。"""
    records = []

    def record_decision(_self, command, _expected=None, _label=None, cwd=None):
        records.append(("stdin", command, None, cwd))

    def record_run_hook(_script, command, cwd=None, raw=None, env=None, background=None):  # noqa: ARG001
        records.append(("stdin", command, raw, cwd))
        return None, ""

    def record_argv(_script, command):
        records.append(("argv", command, None, None))
        return None, ""

    saved = (helpers.HookTestCase.assert_decision, module.run_hook, getattr(module, "run_hook_argv", None))
    helpers.HookTestCase.assert_decision = record_decision
    module.run_hook = record_run_hook
    module.run_hook_argv = record_argv
    try:
        for value in vars(module).values():
            if not (isinstance(value, type) and issubclass(value, unittest.TestCase)
                    and value.__module__ == module.__name__):
                continue
            with contextlib.suppress(Exception):
                value.setUpClass()
                for attribute in dir(value):
                    if not attribute.startswith("test_"):
                        continue
                    case = value(attribute)
                    with contextlib.suppress(Exception):
                        case.setUp()
                        getattr(case, attribute)()
                    with contextlib.suppress(Exception):
                        case.doCleanups()
                value.tearDownClass()
    finally:
        helpers.HookTestCase.assert_decision, module.run_hook, module.run_hook_argv = saved
    return records


def mutate(rng, text, fragments, bases):
    """text に境界の断片を差し込む・消す・置き換える・別のコマンドとつなぐ、を 1〜3 回行う。"""
    for _ in range(rng.randint(1, 3)):
        position = rng.randint(0, len(text))
        choice = rng.random()
        if choice < 0.5:
            text = text[:position] + rng.choice(fragments) + text[position:]
        elif choice < 0.65 and text:
            text = text[:position] + text[position + rng.randint(1, 4):]
        elif choice < 0.8 and text:
            text = text[:position] + rng.choice(fragments) + text[position + 1:]
        elif choice < 0.9:
            text = text + rng.choice(SEPARATORS) + rng.choice(bases)
        else:
            words = text.split(" ")
            index = rng.randrange(len(words))
            words.insert(index, words[index])
            text = " ".join(words)
    return text


def random_command(rng, fragments):
    return "".join(rng.choice(fragments) for _ in range(rng.randint(1, 8)))


def compare(test, name, cases, python, hhx, workers=None):
    """cases の各入力を両方に通し、判定と理由文の不一致を集めて失敗にする。"""
    expected = [python(case) for case in cases]
    workers = workers or min(16, (os.cpu_count() or 4) * 2)
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        actual = list(pool.map(hhx, cases))
    mismatches = [(case, want, got) for case, want, got in zip(cases, expected, actual) if want != got]
    denies = sum(1 for want in expected if want[0] == "deny")
    print(f"\n{name}: {len(cases)} 件を比較 (Python で deny {denies} 件、不一致 {len(mismatches)} 件)",
          file=sys.stderr)
    if mismatches:
        lines = [f"{name}: Python と hhx の結果が {len(mismatches)} 件違う (seed={SEED})"]
        for case, want, got in mismatches[:15]:
            lines.append(f"  入力: {case!r}\n    python: {want!r}\n    hhx:    {got!r}")
        test.fail("\n".join(lines))


def unique(items):
    return list(dict.fromkeys(items))


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class IrreversibleDifferentialTest(unittest.TestCase):
    """irreversible-guard の差分テスト。G 類の保護対象を変えた入力は比べない。"""

    SCRIPT = "irreversible-guard.py"
    NAME = "irreversible-guard"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT)
        import test_irreversible_guard
        cls.records = harvest(test_irreversible_guard)

    def gate(self, text):
        return any(keyword in text for keyword in self.module._GATE)

    def bash_payload(self, command):
        return json.dumps({"session_id": "diff", "hook_event_name": "PreToolUse", "tool_name": "Bash",
                           "tool_input": {"command": command}, "cwd": "/tmp"}, ensure_ascii=False)

    def base_commands(self):
        return unique(command for kind, command, raw, _cwd in self.records
                      if raw is None and isinstance(command, str) and command)

    def test_bash_commands(self):
        rng = random.Random(SEED)
        bases = self.base_commands()
        commands = bases + [mutate(rng, rng.choice(bases), IRREVERSIBLE_FRAGMENTS, bases) for _ in range(CASES)]
        commands += [random_command(rng, IRREVERSIBLE_FRAGMENTS) for _ in range(CASES // 3)]
        payloads = unique(self.bash_payload(command) for command in commands if not CHANGED_GUARD.search(command))
        compare(self, "irreversible-guard (Bash)", payloads,
                lambda raw: run_python(self.module, self.gate, raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw))

    def test_recorded_payloads_and_argv(self):
        """既存のテストが渡している payload (壊れた JSON・Edit・apply_patch など) と argv の経路。"""
        rng = random.Random(SEED + 1)
        raws = [raw for _kind, _command, raw, _cwd in self.records if raw is not None]
        raws += [raw[:position] for raw in raws for position in (1, len(raw) // 2, len(raw) - 1)]
        raws = unique(raw for raw in raws if not CHANGED_GUARD.search(raw))
        compare(self, "irreversible-guard (payload)", raws,
                lambda raw: run_python(self.module, self.gate, raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw))
        bases = self.base_commands()
        argvs = bases + [mutate(rng, rng.choice(bases), IRREVERSIBLE_FRAGMENTS, bases) for _ in range(CASES // 5)]
        argvs = unique(argv for argv in argvs if "\x00" not in argv and not CHANGED_GUARD.search(argv))
        compare(self, "irreversible-guard (argv)", argvs,
                lambda argv: run_python(self.module, self.gate, argv=argv),
                lambda argv: run_hhx(self.NAME, argv=argv))

    def file_paths(self, rng):
        heads = ("", "/", "/x/", "~/", "a/b/", "C:\\x\\", "/Users/alice/.ssh/", ".ssh/", "x/.ssh/y/", "/x/.ssh")
        names = (".env", ".envrc", ".env.local", ".env.example", ".env.examples", ".envrc.local", "envrc.local",
                 "x.pem", ".pem", "id_rsa", "id_rsa.pub", "known_hosts", "known_hosts.old", "config", "configx",
                 ".environment", "a.ts", "", "/", ".env/", " .env", ".env ", "\t.envrc")
        paths = [head + name for head in heads for name in names]
        paths += [mutate(rng, rng.choice(paths), IRREVERSIBLE_FRAGMENTS, paths) for _ in range(CASES // 5)]
        return unique(paths)

    def test_file_tools(self):
        """Edit / Write / NotebookEdit / apply_patch のパス判定と、tool_input の中の文字列の走査順。"""
        rng = random.Random(SEED + 2)
        paths = self.file_paths(rng)
        payloads = []
        for path in paths:
            tool = rng.choice(("Edit", "Write", "MultiEdit", "NotebookEdit"))
            key = rng.choice(("file_path", "notebook_path"))
            payloads.append({"tool_name": tool, "tool_input": {key: path}})
            payloads.append({"tool_name": tool, "tool_input": {"file_path": rng.choice(("", None, 0, [], path)),
                                                               "notebook_path": path}})
            verb = rng.choice(("Add", "Update", "Delete"))
            other = rng.choice(paths)
            patch = f"*** Begin Patch\n*** {verb} File: {other}\n@@\n*** {verb} File: {path}\n+x\n*** End Patch"
            payloads.append({"tool_name": "apply_patch", "tool_input": {"input": patch}})
            payloads.append({"tool_name": "apply_patch",
                             "tool_input": {"a": [f"*** Add File: {path}"], f"*** Delete File: {other}": 1}})
        raws = [json.dumps({**payload, "cwd": "/tmp"}, ensure_ascii=False) for payload in payloads]
        # object の重複したキーは、最初の位置に最後の値が入る (Python の dict と同じ)。
        raws += ['{"tool_name": "apply_patch", "tool_input": {"a": "*** Add File: a.pem", '
                 '"b": "*** Add File: .env", "a": "*** Add File: .envrc"}}',
                 '{"tool_name": "apply_patch", "tool_input": {"a": "x", "b": "*** Add File: .env", '
                 '"a": "*** Add File: .envrc"}}']
        compare(self, "irreversible-guard (file tools)", unique(raws),
                lambda raw: run_python(self.module, self.gate, raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw))


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class DangerousRmDifferentialTest(unittest.TestCase):
    """dangerous-rm-guard の差分テスト。実在するディレクトリと symlink を cwd・HOME・削除対象に使う。"""

    SCRIPT = "dangerous-rm-guard.py"
    NAME = "dangerous-rm-guard"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT, seed="rm")
        import test_dangerous_rm_guard
        cls.records = harvest(test_dangerous_rm_guard)
        # mkdtemp は macOS では /var/folders/... (実体は /private/var/...) を返すので、alias と realpath の両方を通る。
        cls.root = tempfile.mkdtemp(prefix="hhx-diff-")
        cls.deep = os.path.join(cls.root, "work", "a", "b")
        os.makedirs(os.path.join(cls.deep, "sub"))
        cls.link = os.path.join(cls.root, "link")
        os.symlink(os.path.join(cls.root, "work"), cls.link)
        cls.real_home = os.path.join(cls.root, "real-home")
        os.makedirs(os.path.join(cls.real_home, ".cache"))
        cls.home = os.path.join(cls.root, "home")
        os.symlink(cls.real_home, cls.home)
        os.symlink("loop-b", os.path.join(cls.root, "loop-a"))
        os.symlink("loop-a", os.path.join(cls.root, "loop-b"))
        cls.saved_home = os.environ.get("HOME")
        os.environ["HOME"] = cls.home

    @classmethod
    def tearDownClass(cls):
        if cls.saved_home is None:
            os.environ.pop("HOME", None)
        else:
            os.environ["HOME"] = cls.saved_home
        shutil.rmtree(cls.root, ignore_errors=True)

    def gate(self, text):
        return "rm" in text

    def expand(self, text):
        for placeholder, value in (("{ROOT}", self.root), ("{DEEP}", self.deep), ("{HOME}", self.home),
                                   ("{REALHOME}", self.real_home), ("{LINK}", self.link)):
            text = text.replace(placeholder, value)
        return text

    def cwds(self):
        return (self.deep, os.path.realpath(self.deep), os.path.join(self.link, "a", "b"), self.deep + "/",
                self.root, "/", self.home, self.real_home, os.path.join(self.home, ".cache"), "relative/dir",
                os.path.join(self.root, "loop-a", "x"), "", None, 123, "/private" + self.deep, "//" + self.deep)

    def commands(self, rng):
        bases = unique(self.expand(command) for _kind, command, raw, _cwd in self.records
                       if raw is None and isinstance(command, str) and command)
        commands = bases + [self.expand(mutate(rng, rng.choice(bases), RM_FRAGMENTS, bases)) for _ in range(CASES)]
        commands += [self.expand("rm " + random_command(rng, RM_FRAGMENTS)) for _ in range(CASES // 3)]
        return unique(commands)

    def test_bash_commands(self):
        rng = random.Random(SEED + 3)
        payloads = []
        for command in self.commands(rng):
            cwd = rng.choice(self.cwds())
            payload = {"session_id": "diff", "hook_event_name": "PreToolUse", "tool_name": "Bash",
                       "tool_input": {"command": command}}
            if cwd is not None:
                payload["cwd"] = cwd
            payloads.append(json.dumps(payload, ensure_ascii=False))
        compare(self, "dangerous-rm-guard (Bash)", payloads,
                lambda raw: run_python(self.module, self.gate, raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw))

    def test_recorded_payloads_and_argv(self):
        """既存のテストが渡している payload と、argv の経路 (cwd はプロセスの作業ディレクトリ)。"""
        rng = random.Random(SEED + 4)
        raws = [raw for _kind, _command, raw, _cwd in self.records if raw is not None]
        raws += ['{"tool_input": {"command": "rm -rf ."}, "cwd": "/tmp/\\u0000x"}',
                 '{"tool_input": {"command": "rm -rf ./x\\u0000"}, "cwd": "/tmp"}',
                 '{"tool_input": {"command": ["rm -rf /"]}, "cwd": "/tmp"}', 'rm -rf /', '"rm -rf /"']
        compare(self, "dangerous-rm-guard (payload)", unique(raws),
                lambda raw: run_python(self.module, self.gate, raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw))
        argvs = [command for command in self.commands(rng)[: CASES // 3] if "\x00" not in command]
        saved = os.getcwd()
        os.chdir(self.deep)
        try:
            compare(self, "dangerous-rm-guard (argv)", argvs,
                    lambda argv: run_python(self.module, self.gate, argv=argv),
                    lambda argv: run_hhx(self.NAME, argv=argv, cwd=self.deep))
        finally:
            os.chdir(saved)


DISCARD_FRAGMENTS = (
    " ", "  ", "\t", "\n", ";", "&", "&&", "|", "||", "(", ")", "{ ", " }", "'", '"', "`", "$X", "~", "~/", "*", "?",
    "=", "git ", "/usr/bin/git ", "rtk git ", "git --no-pager ", "git -p ", "git --literal-pathspecs ",
    "git --git-dir=x ", "git --work-tree=x ", "git --exec-path=x ", "git -C ", "git -C{B} ", "git -C {B} ",
    "git -C sub ", "git -C . ", "git -C .. ", "git -C ~ ", "git -C 'a b' ", "git -c a=b ", "git -c 'a b' ",
    "git -c \"a b\" ", "reset ", "--hard", "--soft", "checkout ", "-B ", "-- ", ".", ":/", "-f", "--force",
    "switch ", "-C ", "--discard-changes", "restore ", "--staged", "--staged=x", "--worktree", "-SW", "-s ",
    "clean ", "-fd", "-xdf", "-n", "apply ", "-R", "-3", "-R3", "-3R", "--3way", "--reject", "--check", "--cached",
    "-p3", "-C3", "fix.patch", "cd ", "cd {A} && ", "cd {B}; ", "cd sub && ", "cd .. && ", "cd {MISSING} && ",
    "cd {NOTREPO} && ", "cd {WT1} && ", "cd ~ && ", "cd ~/repo && ", "cd - && ", "cd -- ", "pushd x; ", "popd; ",
    "sh -c ", "bash -lc ", "/bin/zsh ", "GIT_DIR=x ", "GIT_WORK_TREE=x ", "GIT_INDEX_FILE=x ", "echo ", "{A}", "{B}",
    "{SUB}", "{ROOT}/", "sub", "..", "make -C {B} ", "\r",
) + UNICODE_FRAGMENTS

# Python 実装の理由文のうち、hhx で意図して変えた文。Claude Code と Codex の両方に出るので、片方にしか無いツール名を外した。
DISCARD_CHANGED_REASON = ("実行せず文字列として書きたいだけなら Write / Edit ツールを使ってください。",
                          "実行せず文字列として書きたいだけなら、ファイルを編集するツールを使ってください。")

# hhx で意図して Python 実装から変えた、snapshot の対象の特定。どちらも Python 実装は別のリポジトリを保存して通し、
# 本来の対象の変更を失う (データ消失) ので、1 対 1 よりデータを守ることを優先して直した (AGENTS.md の「hook の移植の型」)。
#   - ( ... ) / $( ... ) の中の cd を閉じ括弧で取り消す。Python は括弧を空白として読み、閉じた後も cd を残す。
#   - 1 つの git の複数の -C を順に適用する。Python は最後の -C だけを cwd から解決する。
# これに当たりうる入力 (cd の後に閉じ括弧がある、-C で始まるトークンが 2 つ以上ある) は判定が違ってよいので比べない。
# 実際の判定は Go のテスト (TestResolveCdInsideSubshellDoesNotLeak・TestResolveMultipleDashCAreApplied など) で固定している。
# 条件は広めにとり、違いうる入力を取りこぼさないことを優先する。
DISCARD_SUBSHELL_CD = re.compile(r"(?:^|[\s(){};&|])cd(?=[\s(){};&|]|$)[\s\S]*\)")


def discard_differs_on_purpose(command):
    if not isinstance(command, str):
        return False
    tokens = re.sub(r"[(){};&|]", " ", command).split()
    return bool(DISCARD_SUBSHELL_CD.search(command)) or sum(token.startswith("-C") for token in tokens) >= 2


def discard_payload_differs_on_purpose(raw):
    try:
        payload = json.loads(raw)
    except ValueError:
        return False
    tool_input = payload.get("tool_input") if isinstance(payload, dict) else None
    return isinstance(tool_input, dict) and discard_differs_on_purpose(tool_input.get("command"))


def split_intended(name, cases, differs):
    """意図して Python と変えた入力を cases から外す。外した入力も hhx が 0 で終わることは compare の外で確かめる。"""
    kept = [case for case in cases if not differs(case)]
    excluded = [case for case in cases if differs(case)]
    print(f"\n{name}: 意図して Python と変えた入力 {len(excluded)} 件を比較から外す", file=sys.stderr)
    return kept, excluded


# 既存のテストのサンドボックスの場所。記録した入力のうち、ここを差分テストのサンドボックスに差し替える。
HARVESTED_SANDBOX = re.compile(
    r"/[^\s'\";&|()]*?/(?:worktree-guard-test-|resolve-test-|snapshot-test-|wt-env-test-|worktree-policy-test-|"
    r"snapshot-home-)[^/\s'\";&|()]*")


SPACES = ("\u3000", "\xa0", "\x1c", "\x1f", "\x85", "\u2028", "\u200a", "\t", "\n", "\r", "\x0b", "\u200b")


def respace(rng, text):
    """text の空白のいくつかを、別の空白 (Python の \\s に当たるものと当たらないもの) に置き換え、末尾に空白を足すこともある。"""
    text = "".join(rng.choice(SPACES) if char == " " and rng.random() < 0.4 else char for char in text)
    return text + (rng.choice(SPACES) if rng.random() < 0.3 else "")


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class DiscardDifferentialTest(unittest.TestCase):
    """discard-guard の差分テスト。本物のリポジトリ (汚れたもの・きれいなもの・worktree) を cwd と cd / -C の先に使う。

    Python 本体の worktree-guard.py のうち、hhx へ移さないブランチ attach の判定は無効にして比べる。
    snapshot の作成は両方が本物の git で行う。同じ作業ツリーの一時 index を並行して使うと lock で片方が失敗するので、
    hhx も 1 件ずつ起動する。
    """

    SCRIPT = "worktree-guard.py"
    NAME = "discard-guard"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT)
        cls.module.worktree_policy_violation = lambda _cmd, _cwd: ""
        import test_worktree_guard
        cls.records = harvest(test_worktree_guard)
        # mkdtemp は macOS では /var/folders/... (実体は /private/var/...) を返すので、toplevel の表記の違いも通る。
        cls.root = tempfile.mkdtemp(prefix="hhx-diff-discard-")
        cls.paths = {
            "{ROOT}": cls.root,
            "{A}": helpers.make_repo(os.path.join(cls.root, "repoA")),
            "{B}": helpers.make_repo(os.path.join(cls.root, "repoB")),
            "{CLEAN}": helpers.make_repo(os.path.join(cls.root, "clean"), dirty=False),
            "{NOTREPO}": os.path.join(cls.root, "notarepo"),
            "{MISSING}": os.path.join(cls.root, "missing"),
            "{MAIN}": helpers.make_repo(os.path.join(cls.root, "main"), dirty=False),
            "{WT1}": os.path.join(cls.root, "wt1"),
        }
        cls.paths["{SUB}"] = helpers.make_repo(os.path.join(cls.root, "repoA", "sub"))
        helpers.git(cls.paths["{MAIN}"], "worktree", "add", "-q", "-b", "topic1", cls.paths["{WT1}"])
        with open(os.path.join(cls.paths["{WT1}"], "untracked.txt"), "w", encoding="utf-8") as file:
            file.write("wt1\n")
        for name in ("notarepo", "a", "b", os.path.join("a", "sub"), "home"):
            os.makedirs(os.path.join(cls.root, name), exist_ok=True)
        cls.link = os.path.join(cls.root, "link")
        os.symlink(cls.paths["{A}"], cls.link)
        helpers.make_repo(os.path.join(cls.root, "home", "repo"))
        cls.saved_home, cls.saved_cwd = os.environ.get("HOME"), os.getcwd()
        # ~ の展開と、cwd が空のときのプロセスの作業ディレクトリをサンドボックスの中に閉じる。
        os.environ["HOME"] = os.path.join(cls.root, "home")
        os.chdir(cls.paths["{NOTREPO}"])

    @classmethod
    def tearDownClass(cls):
        os.chdir(cls.saved_cwd)
        if cls.saved_home is None:
            os.environ.pop("HOME", None)
        else:
            os.environ["HOME"] = cls.saved_home
        shutil.rmtree(cls.root, ignore_errors=True)

    def expand(self, text):
        text = HARVESTED_SANDBOX.sub("{ROOT}", text)
        for placeholder, value in self.paths.items():
            text = text.replace(placeholder, value)
        return text

    def cwds(self):
        paths = self.paths
        return (paths["{A}"], paths["{SUB}"], paths["{B}"], paths["{CLEAN}"], paths["{NOTREPO}"], paths["{MISSING}"],
                paths["{WT1}"], paths["{MAIN}"], self.link, os.path.realpath(paths["{A}"]), "relative", "", None, 123)

    def run_python(self, raw=None, argv=None):
        self.module.DEADLINE = time.monotonic() + self.module.TIME_BUDGET
        decision, reason = run_python(self.module, lambda text: "git" in text, raw=raw, argv=argv)
        return decision, reason.replace(*DISCARD_CHANGED_REASON)

    def run_excluded(self, raws):
        """比較から外した入力も、hhx が 0 で終わること (run_hhx が確かめる) だけは見る。"""
        for raw in raws:
            run_hhx(self.NAME, raw=raw, cwd=self.paths["{NOTREPO}"])

    def commands(self, rng, count):
        bases = unique(self.expand(command) for _kind, command, raw, _cwd in self.records
                       if raw is None and isinstance(command, str) and command)
        commands = bases + [self.expand(mutate(rng, rng.choice(bases), DISCARD_FRAGMENTS, bases))
                            for _ in range(count)]
        commands += [self.expand("git " + random_command(rng, DISCARD_FRAGMENTS)) for _ in range(count // 3)]
        # 空白の位置に Python と RE2 で範囲の違う空白を置く。境界の判定 (\s と $) がずれると、ここで差が出る。
        commands += [self.expand(respace(rng, rng.choice(bases))) for _ in range(count // 3)]
        return unique(command for command in commands if "\x00" not in command)

    def test_bash_commands(self):
        rng = random.Random(SEED + 5)
        payloads = []
        for command in self.commands(rng, CASES // 2):
            cwd = rng.choice(self.cwds())
            payload = {"session_id": "diff", "hook_event_name": "PreToolUse", "tool_name": "Bash",
                       "tool_input": {"command": command}}
            if cwd is not None:
                payload["cwd"] = cwd
            payloads.append(json.dumps(payload, ensure_ascii=False))
        payloads, excluded = split_intended("discard-guard (Bash)", payloads, discard_payload_differs_on_purpose)
        self.run_excluded(excluded)
        compare(self, "discard-guard (Bash)", payloads, lambda raw: self.run_python(raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw, cwd=self.paths["{NOTREPO}"]), workers=1)

    def test_detection(self):
        """検出 (発火したルールのラベル) だけを比べる。

        cwd が存在しなければ、ルールに当たったコマンドは必ず特定できないものとして deny になり、理由文にラベルが出る。
        上の比較では、snapshot を作って通したのか、ルールに当たらず素通りしたのかを区別できないため。git を起動しないので並行して流せる。
        """
        rng = random.Random(SEED + 7)
        payloads = [json.dumps({"tool_input": {"command": command}, "cwd": self.paths["{MISSING}"]}, ensure_ascii=False)
                    for command in self.commands(rng, CASES)]
        payloads, excluded = split_intended("discard-guard (detection)", payloads, discard_payload_differs_on_purpose)
        self.run_excluded(excluded)
        compare(self, "discard-guard (detection)", payloads, lambda raw: self.run_python(raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw, cwd=self.paths["{NOTREPO}"]))

    def test_recorded_payloads_and_argv(self):
        """既存のテストが渡している payload と、argv の経路 (cwd はプロセスの作業ディレクトリ)。"""
        rng = random.Random(SEED + 6)
        raws = [self.expand(raw) for _kind, _command, raw, _cwd in self.records if raw is not None]
        raws += ['{"tool_input": {"command": "git reset --hard"}}', '{"tool_input": {"command": ["git reset --hard"]}}',
                 '{"tool_input": {"command": "git reset --hard"}, "cwd": 1}', '"git reset --hard"', "git reset --hard",
                 '{"tool_input": {"command": "git reset --hard"}, "cwd": "/tmp/\\u0000x"}', "null", "[]"]
        raws, excluded = split_intended("discard-guard (payload)", unique(raws), discard_payload_differs_on_purpose)
        self.run_excluded(excluded)
        compare(self, "discard-guard (payload)", raws, lambda raw: self.run_python(raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw, cwd=self.paths["{NOTREPO}"]), workers=1)
        for directory in (self.paths["{NOTREPO}"], self.paths["{A}"]):
            os.chdir(directory)
            argvs, excluded = split_intended(f"discard-guard (argv in {os.path.basename(directory)})",
                                             self.commands(rng, CASES // 10), discard_differs_on_purpose)
            for argv in excluded:
                run_hhx(self.NAME, argv=argv, cwd=directory)
            compare(self, f"discard-guard (argv in {os.path.basename(directory)})", argvs,
                    lambda argv: self.run_python(argv=argv),
                    lambda argv, cwd=directory: run_hhx(self.NAME, argv=argv, cwd=cwd), workers=1)
        os.chdir(self.paths["{NOTREPO}"])


if __name__ == "__main__":
    unittest.main()
