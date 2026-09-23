#!/usr/bin/env python3
"""Python 実装と hhx の差分テスト。

対象は irreversible-guard・dangerous-rm-guard・discard-guard・exit-plan-subagent-guard・push-ci-context・pr-body-staleness・
pr-context・agents-local-context。

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
#     hhx は括弧を無視した作業ディレクトリ (Python の意味) も候補に残し、両方を保存する。
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


# exit-plan-subagent-guard の transcript の行に差し込む断片。JSON を壊すもの、改行の変種、不正な UTF-8 を含む。
# NaN・Infinity・対の無いサロゲートのエスケープ・深い入れ子は、hhx で再現していない違いなので入れない (hook の package の説明)。
EXIT_PLAN_RAW_FRAGMENTS = (
    b'"', b"\\", b",", b"{", b"}", b"[", b"]", b" ", b"\r", b"\r\n", b"\n", b"\xff", b"\xe3\x81", b"\xf0\x9f\x98",
    b"\xed\xa0\x80", "日本".encode(), b"\\n", b"\\\"", b'"tool_use"', b'"Agent"', b'"Task"', b'"SendMessage"',
    b"<task-notification>", b"<status>killed</status>", b"<task-id>aaa111bbb222</task-id>", b"agentId: ccc333ddd444",
    b"Async agent launched successfully", b"resumed from transcript in the background",
)

EXIT_PLAN_AGENTS = ("aaa111bbb222", "ccc333ddd444", "a0b7129b0d13559d4", "ab12", "eee555")
EXIT_PLAN_TOOL_IDS = ("toolu_1", "toolu_2", "toolu_3", "toolu_bash")
EXIT_PLAN_DESCRIPTIONS = ("調査", "Design the sharding", "", "b", "a", "PR差分を機能整理", "\U0001f600 絵文字", "Zeta", "é")
EXIT_PLAN_STATUSES = ("completed", "stopped", "killed", "failed", "error", "cancelled", "running", "queued", "Completed")

# 型の違う値。Python で例外になる形 (真で dict でない message・input、配列の id) と、偽の値として読み飛ばす形を混ぜる。
EXIT_PLAN_ODD_VALUES = (None, True, False, 0, 1, 1.0, -0.0, 1e300, 123456789012345678901234567890, "", "x", [], ["t"],
                        {}, {"a": 1}, "toolu_1")


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class ExitPlanDifferentialTest(unittest.TestCase):
    """exit-plan-subagent-guard の差分テスト。

    既存のテストが使う行の形 (起動の要求と結果・終了通知・再開・Bash の出力) を組み合わせた transcript と、
    それを JSON の型やバイト列の段階で変形したものを、Python 本体と hhx の両方に読ませて判定と理由文を比べる。
    判定材料が transcript だけなので、手書きの表では値の型 (Python の真偽・== の意味) や UTF-8 の置換、
    改行の扱いの違いを取りこぼしやすい。
    """

    SCRIPT = "exit-plan-subagent-guard.py"
    NAME = "exit-plan-subagent-guard"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT)
        import test_exit_plan_subagent_guard
        cls.fixtures = test_exit_plan_subagent_guard
        cls.root = tempfile.mkdtemp(prefix="hhx-diff-exit-plan-")

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.root, ignore_errors=True)

    def random_line(self, rng):
        f = self.fixtures
        agent, tool_id = rng.choice(EXIT_PLAN_AGENTS), rng.choice(EXIT_PLAN_TOOL_IDS)
        description = rng.choice(EXIT_PLAN_DESCRIPTIONS)
        sidechain = rng.random() < 0.1
        choice = rng.randrange(12)
        if choice == 0:
            return f.tool_use_line(tool_id, description, name=rng.choice(("Agent", "Task", "Bash", "SendMessage")),
                                   sidechain=sidechain)
        if choice == 1:
            return f.launch_line(tool_id, agent, sidechain=sidechain, flat=rng.random() < 0.5)
        if choice == 2:
            return f.send_message_line(tool_id, agent, sidechain=sidechain)
        if choice == 3:
            text = rng.choice((f.RESUME_TEXT, f.REAL_LAUNCH_TEXT, f.LAUNCH_TEXT)).format(agent=agent)
            return f.result_line(tool_id, text, sidechain=sidechain)
        if choice == 4:
            return f.notification_line(agent, status=rng.choice(EXIT_PLAN_STATUSES))
        if choice == 5:
            return f.queue_notification_line(agent, status=rng.choice(EXIT_PLAN_STATUSES))
        if choice == 6:
            return f.bash_result_line(rng.choice((f.LAUNCH_TEXT, f.RESUME_TEXT, "ok", "agentId: {agent}")).format(
                agent=agent))
        if choice == 7:
            # 型の違う値を 1 つ差し込んだ、起動の要求か結果の行。
            entry = json.loads(rng.choice((f.tool_use_line(tool_id, description), f.launch_line(tool_id, agent),
                                           f.send_message_line(tool_id, agent))))
            block = entry["message"]["content"][0]
            target = rng.choice((entry, entry, entry["message"], block, block, block))
            target[rng.choice(("message", "content", "id", "tool_use_id", "input", "name", "type", "isSidechain",
                               "text"))] = rng.choice(EXIT_PLAN_ODD_VALUES)
            return json.dumps(entry, ensure_ascii=rng.random() < 0.5)
        if choice == 8:
            # 数値や真偽の id と、それに対応する tool_use_id。Python の == で一致するものを混ぜる。
            ids = (1, True, 1.0, 0, False, -0.0, None, "1", 10 ** 20, 1e20)
            use = json.loads(f.tool_use_line(tool_id, description, name=rng.choice(("Agent", "SendMessage"))))
            use["message"]["content"][0]["id"] = rng.choice(ids)
            result = json.loads(f.result_line(tool_id, rng.choice((f.LAUNCH_TEXT, f.RESUME_TEXT)).format(agent=agent)))
            result["message"]["content"][0]["tool_use_id"] = rng.choice(ids)
            return json.dumps(use) + "\n" + json.dumps(result)
        if choice == 9:
            # content のブロック列の text に文字列でない値や、ブロックでない要素を混ぜる。
            entry = json.loads(f.launch_line(tool_id, agent))
            parts = [{"type": "text", "text": f.LAUNCH_TEXT.format(agent=agent)},
                     {"type": "text", "text": rng.choice(EXIT_PLAN_ODD_VALUES)}, rng.choice(EXIT_PLAN_ODD_VALUES)]
            rng.shuffle(parts)
            entry["message"]["content"][0]["content"] = parts
            return json.dumps(entry)
        if choice == 10:
            return rng.choice(("", "{ not json", "null", "[]", '"tool_use" "Agent"', "{}"))
        return f.bash_result_line("x" * rng.choice((10, 70000)))

    def mutate_bytes(self, rng, data):
        for _ in range(rng.randint(1, 3)):
            position = rng.randint(0, len(data))
            if rng.random() < 0.7:
                data = data[:position] + rng.choice(EXIT_PLAN_RAW_FRAGMENTS) + data[position:]
            else:
                data = data[:position] + data[position + rng.randint(1, 4):]
        return data

    def transcripts(self, rng, count):
        f = self.fixtures
        # 既存のテストのシナリオ (起動の対と、その後の終了・再開) を土台に、行を足し、変形する。
        scenarios = [
            f.launch_pair("toolu_1", "aaa111bbb222"),
            f.launch_pair("toolu_1", "aaa111bbb222") + [f.notification_line("aaa111bbb222")],
            f.launch_pair("toolu_1", "aaa111bbb222") + [f.notification_line("aaa111bbb222"),
                                                         f.send_message_line("toolu_2", "aaa111bbb222"),
                                                         f.result_line("toolu_2",
                                                                       f.RESUME_TEXT.format(agent="aaa111bbb222"))],
            f.launch_pair("toolu_1", "aaa111bbb222", sidechain=True),
            [f.queue_notification_line("aaa111bbb222", status="killed")] + f.launch_pair("toolu_1", "aaa111bbb222"),
        ]
        results = []
        for _ in range(count):
            lines = list(rng.choice(scenarios))
            for _ in range(rng.randint(0, 6)):
                lines.insert(rng.randint(0, len(lines)), self.random_line(rng))
            separator = rng.choice(("\n", "\n", "\r", "\r\n"))
            data = (separator.join(lines) + rng.choice(("", separator))).encode("utf-8")
            if rng.random() < 0.4:
                data = self.mutate_bytes(rng, data)
            results.append(data)
        return results

    def write(self, index, data):
        path = os.path.join(self.root, f"t{index}.jsonl")
        with open(path, "wb") as file:
            file.write(data)
        return path

    def test_transcripts_via_argv(self):
        rng = random.Random(SEED + 11)
        paths = [self.write(index, data) for index, data in enumerate(self.transcripts(rng, CASES))]
        paths += [os.path.join(self.root, "missing.jsonl"), self.root, ""]
        compare(self, "exit-plan-subagent-guard (argv)", paths,
                lambda path: run_python(self.module, lambda _text: True, argv=path),
                lambda path: run_hhx(self.NAME, argv=path))

    def test_payloads(self):
        rng = random.Random(SEED + 12)
        paths = [self.write(f"p{index}", data) for index, data in enumerate(self.transcripts(rng, CASES // 10))]
        raws = []
        for path in paths:
            payload = {"session_id": "diff", "transcript_path": path, "hook_event_name": "PreToolUse",
                       "tool_name": rng.choice(("ExitPlanMode", "Bash")), "tool_input": {}}
            raws.append(json.dumps(payload))
        path = paths[0]
        raws += ["", " ", "ExitPlanMode", "null", '"ExitPlanMode"', '["ExitPlanMode"]',
                 json.dumps({"tool_name": "ExitPlanMode"}), json.dumps({"tool_name": "ExitPlanMode", "transcript_path": 1}),
                 json.dumps({"tool_name": "ExitPlanMode", "transcript_path": [path]}),
                 json.dumps({"tool_name": "ExitPlanMode", "transcript_path": path}) + " x",
                 json.dumps({"tool_name": "ExitPlanMode", "transcript_path": path}) + "\n",
                 json.dumps({"tool_name": "Bash", "tool_input": {"command": "grep ExitPlanMode x"},
                             "transcript_path": path})]
        compare(self, "exit-plan-subagent-guard (payload)", unique(raws),
                lambda raw: run_python(self.module, lambda text: "ExitPlanMode" in text, raw=raw),
                lambda raw: run_hhx(self.NAME, raw=raw))


def run_python_stdout(module, gate, raw=None, argv=None):
    """Python 本体をプロセス内で起動し、stdout をそのまま返す (注入系の hook 用)。本体の例外は無出力として扱う。"""
    text = argv if argv is not None else raw
    if not gate(text):
        return ""
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
        return ""
    finally:
        sys.argv, module.RAW = saved
    return out.getvalue()


def run_hhx_stdout(name, raw=None, argv=None, cwd=None):
    command = hook_command_for_name(name) + ([] if argv is None else [argv])
    proc = subprocess.run(command, input="" if raw is None else raw, capture_output=True, text=True,
                          encoding="utf-8", timeout=120, cwd=cwd or os.getcwd())
    if proc.returncode != 0:
        raise AssertionError(f"hhx が 0 以外で終了しました (rc={proc.returncode}): {proc.stderr!r}")
    return proc.stdout


def injection(stdout):
    """注入系の出力を比べられる形にする。JSON なら読んだ値、平文ならそのまま。"""
    if not stdout.strip():
        return None
    try:
        return json.loads(stdout)
    except ValueError:
        return stdout


PUSH_BASE_COMMANDS = (
    "git push", "git push origin HEAD", "git push --force-with-lease origin feature",
    "git push origin 61c6a6b:refs/heads/feature", "git -C /other/worktree push origin HEAD", "/usr/bin/git push",
    "make build && git push", "git commit -m 'x'; git push", "gh pr create --fill", "gh pr create --draft --title x --body y",
    "gh workflow run deploy.yml", "gh workflow run deploy.yml --ref main -f target=staging",
    "gh workflow run deploy.yml -f 'message=a;b'", "/usr/local/bin/gh workflow run deploy.yml -R owner/repo",
    "gh workflow   run deploy.yml", "git push --dry-run origin HEAD", "git push -n origin HEAD",
    "git push origin --delete feature", "git push origin :feature", "git status", "git fetch origin", "gh pr view 155",
    "gh workflow run --help", "gh workflow view deploy.yml", "gh run list --workflow deploy.yml",
    "echo 'gh workflow run deploy.yml'", "rg 'gh workflow run' .", "echo push", "npm run push-image",
    "git status --short --branch && git push -u origin fix/x", "gh workflow run deploy.yml --ref main -R owner/repo",
    "gh workflow run 'my flow.yml' -r 'feat/x y' --repo=o/r --json -F a=b",
)

PUSH_FRAGMENTS = (
    " ", "  ", "\t", ";", "|", "&", "&&", "||", "\n", "\r", "'", '"', "\\", "\\\n", "(", ")", ":", ":x", "-", "--",
    "git ", "push", "PUSH", "/usr/bin/git ", "GIT_X=1 ", "gh ", "pr ", "create", "workflow ", "run ", "--help", "-h",
    "-H", "--HELP", "-n", "-d", "--dry-run", "--delete", "-R", "-R o/r", "-Ro/r", "--repo=o/r", "-r main", "--ref=x",
    "-f a=b", "--json", "x.yml", "'a b'", "$(x)", "`x`", "#", "=", "İ", "\u3000", "é",
) + UNICODE_FRAGMENTS

PUSH_RESPONSES = (
    {}, None, "text", [], {"stdout": "https://github.com/o/r/actions/runs/123456789"},
    {"stdout": "https://github.com/o/r/actions/runs/42?x=1"}, {"output": "/actions/runs/7x"},
    {"exit_code": 1}, {"exit_code": True}, {"exitCode": 1.0}, {"code": "1"}, {"success": False}, {"interrupted": True},
    {"stderr": "To github.com:o/r.git"}, {"stderr": "Everything up-to-date"}, {"aggregated_output": "fatal: x"},
    {"stdout": ["github.com"]}, {"stdout": {"a": "! [rejected]"}}, {"stdout": 0, "stderr": "GITHUB.COM"},
)


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class PushCIContextDifferentialTest(unittest.TestCase):
    """push-ci-context の差分テスト。

    コマンドの分割は Python の shlex を Go で書き直したので、区切り・引用・エスケープの境界を変形して比べる。
    待機コマンドの文面は意図して変えた (wait-ci → hhx wait-ci) ので置き換えてから比べ、
    workflow run の一覧を絞る時刻 (実行した時刻の 1 分前) は秒の揺れがあるので伏せる。
    """

    SCRIPT = "push-ci-context.py"
    NAME = "push-ci-context"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT, seed="git push")
        cls.root = tempfile.mkdtemp(prefix="hhx-diff-push-ci-")
        cls.repos = {}
        for name, origin in (("github", "https://github.com/o/r.git"), ("gitlab", "git@gitlab.com:o/r.git"),
                             ("plain", None)):
            path = os.path.join(cls.root, name)
            os.makedirs(path)
            if origin:
                helpers.git(path, "init", "-q")
                helpers.git(path, "remote", "add", "origin", origin)
            cls.repos[name] = path

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.root, ignore_errors=True)

    @staticmethod
    def gate(text):
        lower = text.lower()
        return "push" in lower or ("pr" in lower and "create" in lower) or ("workflow" in lower and "run" in lower)

    @staticmethod
    def normalize(stdout):
        stdout = re.sub(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", "<cutoff>", stdout)
        return injection(stdout.replace("hhx wait-ci", "wait-ci"))

    def commands(self, rng, count):
        bases = list(PUSH_BASE_COMMANDS)
        commands = bases + [mutate(rng, rng.choice(bases), PUSH_FRAGMENTS, bases) for _ in range(count)]
        return unique(command for command in commands if "\x00" not in command)

    def test_payloads(self):
        rng = random.Random(SEED + 21)
        raws = []
        for command in self.commands(rng, CASES // 3):
            event = rng.choice(("PostToolUse", "PostToolUse", "PostToolUse", "PreToolUse", "UserPromptSubmit"))
            payload = {"session_id": "diff", "hook_event_name": event, "tool_name": "Bash",
                       "tool_input": {"command": command},
                       "cwd": rng.choice(list(self.repos.values()) + ["", os.path.join(self.root, "missing")]),
                       "transcript_path": rng.choice(("/x/rollout-2026-diff.jsonl", "/x/diff.jsonl"))}
            if rng.random() < 0.9:
                payload["tool_response"] = rng.choice(PUSH_RESPONSES)
            raws.append(json.dumps(payload, ensure_ascii=rng.random() < 0.5))
        raws += ["", "push", "null", '"git push"', json.dumps({"tool_input": "git push"}),
                 json.dumps({"tool_input": {"command": ["git", "push"]}}),
                 json.dumps({"tool_input": {"command": "git push"}, "hook_event_name": ["x"]}),
                 json.dumps({"tool_input": {"command": "git push"}, "hook_event_name": 1}),
                 json.dumps({"tool_input": {"command": "git push"}, "cwd": 5}),
                 json.dumps({"tool_input": {"command": "git push"}}) + " x"]
        compare_injections(self, "push-ci-context (payload)", unique(raws),
                           lambda raw: self.normalize(run_python_stdout(self.module, self.gate, raw=raw)),
                           lambda raw: self.normalize(run_hhx_stdout(self.NAME, raw=raw)))

    def test_argv(self):
        rng = random.Random(SEED + 22)
        commands = self.commands(rng, CASES // 5)
        cwd = os.getcwd()
        compare_injections(self, "push-ci-context (argv)", commands,
                           lambda argv: self.normalize(run_python_stdout(self.module, self.gate, argv=argv)),
                           lambda argv: self.normalize(run_hhx_stdout(self.NAME, argv=argv, cwd=cwd)))


def compare_injections(test, name, cases, python, hhx, workers=None):
    """cases の各入力を両方に通し、注入の内容の不一致を集めて失敗にする。"""
    expected = [python(case) for case in cases]
    if workers == 1:
        actual = [hhx(case) for case in cases]
    else:
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers or min(16, (os.cpu_count() or 4) * 2)) as pool:
            actual = list(pool.map(hhx, cases))
    mismatches = [(case, want, got) for case, want, got in zip(cases, expected, actual) if want != got]
    injected = sum(1 for want in expected if want is not None)
    print(f"\n{name}: {len(cases)} 件を比較 (Python で注入 {injected} 件、不一致 {len(mismatches)} 件)", file=sys.stderr)
    if mismatches:
        lines = [f"{name}: Python と hhx の結果が {len(mismatches)} 件違う (seed={SEED})"]
        for case, want, got in mismatches[:15]:
            lines.append(f"  入力: {case!r}\n    python: {want!r}\n    hhx:    {got!r}")
        test.fail("\n".join(lines))


ORIGIN_BASES = (
    "https://github.com/o/r.git", "https://github.com/o/r", "git@github.com:o/r.git", "ssh://git@github.com/o/r.git",
    "https://github.com/o/r/", "git@gitlab.com:o/r.git", "https://notgithub.com/o/r.git", "/local/path/repo",
    "https://GitHub.COM/Owner/Repo.GIT", "git@github.com:o/r.git/", "https://x/github.com/o/r",
)

ORIGIN_FRAGMENTS = (
    "github.com", "GITHUB.COM", "gıthub.com", "gİthub.com", "notgithub.com", "-", "_", ".", "a", "Z", "0", "\u212a",
    "\u017f", "\u0131", "\u0130", "é", "日本", ":", "/", "//", ":/", ".git", ".GIT", ".gıt", ".git/", " ", "\t", "o", "r",
    "x/", "git@", "https://", "ssh://git@", "?", "#", "%",
)

STALE_GRAPHQL = {"data": {"repository": {"object": {"associatedPullRequests": {"nodes": [{
    "number": 7, "url": "https://github.com/o/r/pull/7", "state": "OPEN", "lastEditedAt": "2026-09-10T07:10:57Z",
    "createdAt": "2026-09-10T07:06:57Z", "commits": {"nodes": [{"commit": {
        "committedDate": "2026-09-10T07:36:15Z", "messageHeadline": "feat: x", "parents": {"totalCount": 1}}}]}}]}}}}}


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class PrBodyStalenessDifferentialTest(unittest.TestCase):
    """pr-body-staleness の差分テスト。

    origin の URL から owner と repo を取る ORIGIN_PATTERN は、後読み (?<![A-Za-z0-9-]) を手書きの走査に置き換えたので、
    origin を変形して Python 本体と hhx に同じリポジトリを読ませ、注入の内容と gh の呼び出し (owner・repo) を比べる。
    push の判定 (正規表現で区切る別の実装) も、コマンドを変形して比べる。gh は偽の gh (fake_gh.py) で差し替える。
    """

    SCRIPT = "pr-body-staleness.py"
    NAME = "pr-body-staleness"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT, seed="git push")
        cls.root = tempfile.mkdtemp(prefix="hhx-diff-pr-body-")
        cls.fixtures = os.path.join(cls.root, "gh")
        os.makedirs(cls.fixtures)
        shutil.copy(helpers.FAKE_GH, os.path.join(cls.fixtures, "gh"))
        os.chmod(os.path.join(cls.fixtures, "gh"), 0o755)
        with open(os.path.join(cls.fixtures, "graphql.json"), "w", encoding="utf-8") as file:
            json.dump(STALE_GRAPHQL, file)
        cls.saved_environ = dict(os.environ)
        os.environ.update({"PATH": cls.fixtures + os.pathsep + os.environ["PATH"], "FAKE_GH_DIR": cls.fixtures,
                           "FAKE_GH_MODE": "ok", **helpers.GIT_ISOLATION})
        cls.repo = os.path.join(cls.root, "repo")
        os.makedirs(cls.repo)
        helpers.git(cls.repo, "init", "-q")
        helpers.git(cls.repo, "commit", "-q", "--allow-empty", "-m", "init")
        helpers.git(cls.repo, "remote", "add", "origin", "https://github.com/o/r.git")

    @classmethod
    def tearDownClass(cls):
        os.environ.clear()
        os.environ.update(cls.saved_environ)
        shutil.rmtree(cls.root, ignore_errors=True)

    @staticmethod
    def gate(text):
        return "push" in text.lower()

    def calls(self):
        path = os.path.join(self.fixtures, "calls.log")
        if not os.path.exists(path):
            return []
        with open(path, encoding="utf-8") as file:
            calls = [json.loads(line)["argv"] for line in file]
        os.remove(path)
        return calls

    def payload(self, command="git push origin HEAD"):
        return json.dumps({"session_id": "diff", "hook_event_name": "PostToolUse", "tool_name": "Bash",
                           "tool_input": {"command": command}, "tool_response": {}, "cwd": self.repo})

    def run_both(self, raw):
        self.calls()
        python = injection(run_python_stdout(self.module, self.gate, raw=raw)), self.calls()
        hhx = injection(run_hhx_stdout(self.NAME, raw=raw)), self.calls()
        return python, hhx

    def test_origins(self):
        rng = random.Random(SEED + 31)
        bases = list(ORIGIN_BASES)
        urls = bases + [mutate(rng, rng.choice(bases), ORIGIN_FRAGMENTS, bases) for _ in range(CASES // 10)]
        urls = unique(url.strip() for url in urls if "\x00" not in url and "\n" not in url and url.strip())
        results = []
        for url in urls:
            helpers.git(self.repo, "config", "remote.origin.url", url)
            results.append((url,) + self.run_both(self.payload()))
        mismatches = [(url, want, got) for url, want, got in results if want != got]
        matched = sum(1 for _url, want, _got in results if want[1])
        print(f"\npr-body-staleness (origin): {len(urls)} 件を比較 (Python で gh を呼んだもの {matched} 件、"
              f"不一致 {len(mismatches)} 件)", file=sys.stderr)
        if mismatches:
            self.fail("\n".join(f"  origin: {url!r}\n    python: {want!r}\n    hhx:    {got!r}"
                                 for url, want, got in mismatches[:15]))

    def test_commands(self):
        rng = random.Random(SEED + 32)
        helpers.git(self.repo, "config", "remote.origin.url", "https://github.com/o/r.git")
        bases = list(PUSH_BASE_COMMANDS)
        commands = bases + [mutate(rng, rng.choice(bases), PUSH_FRAGMENTS, bases) for _ in range(CASES // 10)]
        commands = unique(command for command in commands if "\x00" not in command)
        results = [(command,) + self.run_both(self.payload(command)) for command in commands]
        mismatches = [(command, want, got) for command, want, got in results if want != got]
        print(f"\npr-body-staleness (command): {len(commands)} 件を比較 (不一致 {len(mismatches)} 件)", file=sys.stderr)
        if mismatches:
            self.fail("\n".join(f"  command: {command!r}\n    python: {want!r}\n    hhx:    {got!r}"
                                 for command, want, got in mismatches[:15]))


PR_PROMPT_BASES = (
    "https://github.com/o/r/pull/1", "http://github.com/o/r/pull/2", "https://www.github.com/o/r/pull/3",
    "github.com/o/r/pull/1", "見て → https://github.com/o/r/pull/1 です", "(https://github.com/o/r/pull/2)",
    "https://github.com/o/r/pull/1#discussion_r11", "https://github.com/o/r/pull/2#discussion_r22 と r33",
    "https://github.com/o/r/pull/1 https://github.com/o/r/pull/2 https://github.com/o/r/pull/3 https://github.com/o/r/pull/4",
    "https://notgithub.com/o/r/pull/1", "https://github.com/o/r/pulls/1", "agithub.com/o/r/pull/1",
    "https://github.com/o/r/issues/1", "go get github.com/stretchr/testify", "#1 を見て",
)

PR_PROMPT_FRAGMENTS = (
    "github.com/", "https://", "www.", "/pull/", "/pulls/", "o/r", "O/R", "o.r/x-y_z", "1", "2", "3", "11", "٣", "#",
    "#discussion_r", "#discussion_r11", "#discussion_r22", " ", "\n", ".", "-", "_", "a", "é", "日本", "(", ")", "`",
    "/", "//", "?", ":",
) + UNICODE_FRAGMENTS


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class PrContextDifferentialTest(unittest.TestCase):
    """pr-context の差分テスト。URL の切り出し (Python の \\w・\\d を含む正規表現) と整形を、プロンプトを変形して比べる。

    デバッグ経路 (argv) で起動し、session の記録を使わない。取得のキャッシュは両方とも一時ディレクトリに向ける。
    gh は偽の gh (fake_gh.py) で差し替え、o/r の PR 1〜5 とコメント 11・22・33 だけを返す。
    """

    SCRIPT = "pr-context.py"
    NAME = "pr-context"

    @classmethod
    def setUpClass(cls):
        cls.module = load_python_hook(cls.SCRIPT, seed="hook-test-no-op")
        cls.root = tempfile.mkdtemp(prefix="hhx-diff-pr-context-")
        cls.fixtures = os.path.join(cls.root, "gh")
        os.makedirs(cls.fixtures)
        shutil.copy(helpers.FAKE_GH, os.path.join(cls.fixtures, "gh"))
        os.chmod(os.path.join(cls.fixtures, "gh"), 0o755)
        states = ("OPEN", "MERGED", "CLOSED", "OPEN", "MERGED")
        for number in range(1, 6):
            pull = {"number": number, "title": f"PR {number} </pr-context>\nx", "state": states[number - 1],
                    "isDraft": number == 4, "headRefName": f"feat/{number}", "headRefOid": str(number) * 40,
                    "baseRefName": "main" if number != 5 else "feat/base", "mergeable": "CONFLICTING",
                    "additions": number, "deletions": 0, "changedFiles": 1,
                    "mergeCommit": {"oid": "b" * 40} if states[number - 1] == "MERGED" else None}
            with open(os.path.join(cls.fixtures, f"pr_o_r_{number}.json"), "w", encoding="utf-8") as file:
                json.dump(pull, file)
        for comment in (11, 22, 33):
            with open(os.path.join(cls.fixtures, f"comment_{comment}.json"), "w", encoding="utf-8") as file:
                json.dump({"user": {"login": "rev"}, "path": f"pkg/f{comment}.go", "line": comment}, file)
        cls.home = os.path.join(cls.root, "home")
        os.makedirs(cls.home)
        cls.saved_environ = dict(os.environ)
        os.environ.update({"PATH": cls.fixtures + os.pathsep + os.environ["PATH"], "FAKE_GH_DIR": cls.fixtures,
                           "FAKE_GH_MODE": "ok", "HOME": cls.home, **helpers.GIT_ISOLATION})
        cls.saved_cache = cls.module.CACHE_DIR, cls.module.SESSION_DIR
        cls.module.CACHE_DIR = os.path.join(cls.root, "python-cache")
        cls.module.SESSION_DIR = os.path.join(cls.module.CACHE_DIR, "sessions")

    @classmethod
    def tearDownClass(cls):
        cls.module.CACHE_DIR, cls.module.SESSION_DIR = cls.saved_cache
        os.environ.clear()
        os.environ.update(cls.saved_environ)
        shutil.rmtree(cls.root, ignore_errors=True)

    def run_python(self, prompt):
        saved = sys.argv
        sys.argv = ["hook", prompt]
        self.module._DIR_CACHE.clear()
        out = io.StringIO()
        try:
            with contextlib.redirect_stdout(out):
                self.module.main()
        except Exception:  # noqa: BLE001  移植元は main の外側で例外を握りつぶす
            return ""
        finally:
            sys.argv = saved
        return out.getvalue()

    def test_prompts(self):
        rng = random.Random(SEED + 41)
        bases = list(PR_PROMPT_BASES)
        prompts = bases + [mutate(rng, rng.choice(bases), PR_PROMPT_FRAGMENTS, bases) for _ in range(CASES // 10)]
        prompts = unique(prompt for prompt in prompts if "\x00" not in prompt)
        cwd = helpers.HOOK_RUN_CWD
        saved = os.getcwd()
        os.chdir(cwd)
        try:
            compare_injections(self, "pr-context (argv)", prompts, lambda prompt: injection(self.run_python(prompt)),
                               lambda prompt: injection(run_hhx_stdout(self.NAME, argv=prompt, cwd=cwd)), workers=1)
        finally:
            os.chdir(saved)


AGENTS_PATH_FRAGMENTS = (
    " ", "/", "./", "../", "src/", "src/service/", "target.py", "missing", "*", "?", "[", ":12", ":1:2", "'", '"',
    "--file=", "-f=", "-", "~/", "$HOME/", "${HOME}/", "$NOPE/", "https://x/", "\n", "\t", "\\", ";", "|", "&&",
    "*** Update File: ", "--- ", "+++ ", "cat ", "ls ", "sed -n 1p ", "cd ", "é", "日本",
) + UNICODE_FRAGMENTS


@unittest.skipUnless(helpers.TARGET == "hhx", "差分テストは hhx を対象にしたときだけ流す")
class AgentsLocalContextDifferentialTest(unittest.TestCase):
    """agents-local-context の差分テスト。対象のパスの集め方 (shlex.split、apply_patch の行、パスの正規化) を比べる。

    1 件ごとに別の session_id を使い、重複の抑止が比較に混ざらないようにする。Python 本体は CODEX_HOME に、
    hhx は XDG_CACHE_HOME に状態を置く。警告の文面は例外の説明の書き方が違うので、警告の種類 (: の前) だけを比べる。
    """

    SCRIPT = "agents-local-context.py"
    NAME = "agents-local-context"

    @classmethod
    def setUpClass(cls):
        load_python_hook("pr-merge-guard.py")  # HHX_COMPAT_PYTHON_HOOKS が無ければここで skip する
        # dataclass はモジュールが sys.modules にあることを前提にするので、登録してから読み込む。
        import importlib.util
        spec = importlib.util.spec_from_file_location(
            "hook_python_agents_local_context", helpers._python_hooks_dir() / cls.SCRIPT)
        cls.module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = cls.module
        spec.loader.exec_module(cls.module)
        cls.root = os.path.realpath(tempfile.mkdtemp(prefix="hhx-diff-agents-local-"))
        cls.repo = os.path.join(cls.root, "repo")
        os.makedirs(os.path.join(cls.repo, "src", "service"))
        helpers.git(cls.repo, "init", "-q")
        for relative, text in (("AGENTS.local.md", "root-local\n"), ("src/AGENTS.local.md", "src-local\n"),
                               ("src/service/AGENTS.local.md", "service-local\n"), ("src/service/target.py", "x\n")):
            with open(os.path.join(cls.repo, relative), "w", encoding="utf-8") as file:
                file.write(text)
        cls.saved_environ = dict(os.environ)
        os.environ.update({"HOME": cls.root, "CODEX_HOME": os.path.join(cls.root, "codex"),
                           "XDG_CACHE_HOME": os.path.join(cls.root, "cache"), **helpers.GIT_ISOLATION})

    @classmethod
    def tearDownClass(cls):
        os.environ.clear()
        os.environ.update(cls.saved_environ)
        shutil.rmtree(cls.root, ignore_errors=True)

    def run_python(self, raw):
        saved = sys.stdin
        sys.stdin = io.StringIO(raw)
        out = io.StringIO()
        try:
            with contextlib.redirect_stdout(out):
                try:
                    self.module.main()
                except Exception as exc:  # noqa: BLE001  移植元の __main__ と同じく警告にする
                    self.module.emit("PreToolUse", errors=[f"予期しないエラー ({type(exc).__name__}): {exc}"])
        finally:
            sys.stdin = saved
        return out.getvalue()

    @staticmethod
    def normalize(stdout):
        data = injection(stdout)
        if isinstance(data, dict) and "systemMessage" in data:
            data["systemMessage"] = [part.split(":")[0].split(" (")[0]
                                     for part in data["systemMessage"].split("; ")]
        return data

    def test_tool_inputs(self):
        rng = random.Random(SEED + 51)
        bases = ["sed -n 1p src/service/target.py", "cat src/service/target.py src/x", "ls src", "ls",
                 "*** Begin Patch\n*** Update File: src/service/target.py\n@@\n*** End Patch",
                 f"cat {self.repo}/src/service/target.py", "cat 'src/service/target.py:12'", "echo 'unterminated"]
        raws = []
        for index in range(CASES // 10):
            command = mutate(rng, rng.choice(bases), AGENTS_PATH_FRAGMENTS, bases)
            path = mutate(rng, rng.choice(("src/service/target.py", "src", "missing/x", self.repo + "/src")),
                          AGENTS_PATH_FRAGMENTS, bases)
            tool_input = rng.choice((
                {"command": command}, {"cmd": command}, command, {"file_path": path, "content": "x"},
                {"command": command, "workdir": rng.choice(("src", "src/service", "missing", "", path))},
                {"paths": [path, {"target": path}]}, {"x": {"File": path}}, {"command": [command]}, None, 5,
            ))
            payload = {"session_id": f"diff-{index}", "hook_event_name": "PreToolUse", "tool_name": "x",
                       "cwd": rng.choice((self.repo, self.repo + "/src", "~", "~/repo", "", "src", "src/missing", self.root)),
                       "tool_input": tool_input}
            if "\x00" not in json.dumps(payload):
                raws.append(json.dumps(payload, ensure_ascii=rng.random() < 0.5))
        raws += ["", "x", "[]", json.dumps({"hook_event_name": "PreToolUse"}),
                 json.dumps({"hook_event_name": "SessionStart", "source": "startup", "cwd": self.repo, "session_id": "s"}),
                 json.dumps({"hook_event_name": "SubagentStart", "cwd": self.repo}),
                 json.dumps({"hook_event_name": "SessionEnd", "cwd": self.repo, "session_id": "e"})]
        # 相対の cwd はプロセスの作業ディレクトリを基準にするので、両方をリポジトリの中で起動する。
        saved = os.getcwd()
        os.chdir(self.repo)
        try:
            compare_injections(self, "agents-local-context (payload)", unique(raws),
                               lambda raw: self.normalize(self.run_python(raw)),
                               lambda raw: self.normalize(run_hhx_stdout(self.NAME, raw=raw, cwd=self.repo)), workers=1)
        finally:
            os.chdir(saved)


if __name__ == "__main__":
    unittest.main()
