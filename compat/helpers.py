"""hooks のテスト共通ヘルパー。

テストは 3 層で書く:
  L1 契約テスト   … 実運用と同じ経路 (stdin の JSON) でフックを起動し、判定だけを見る。
                    shebang・実行権限・一次ゲート・出力スキーマまで丸ごと守れる。
  L2 単体テスト   … 正規表現やトークナイザを直接呼ぶ。ask になった原因を切り分けられる。
  L3 結合テスト   … 本物の git でスナップショットを作り、復元できることまで確認する。

L2 のためにフック本体を import できる形へ書き換えることはしない。本体はモジュールトップで
stdin を読み一次ゲートで抜ける構造 (速度上の意図的な設計) なので、テスト側で argv に種文字列を
置いてゲートを通す (load_hook)。

互換スイートとしての切り替え (compat/README.md):
  HHX_COMPAT_TARGET=hhx (既定) … `hhx hook <name>` を起動する。PORTED_HOOKS に無い hook の
                                  テストと、Python 本体を import する L2 は skip する。
  HHX_COMPAT_TARGET=python    … HHX_COMPAT_PYTHON_HOOKS のディレクトリにある Python 本体を起動する。
"""
import atexit
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

COMPAT_DIR = Path(__file__).resolve().parent
FAKE_GH = COMPAT_DIR / "fake_gh.py"

TARGET = os.environ.get("HHX_COMPAT_TARGET", "hhx")
if TARGET not in ("hhx", "python"):
    raise RuntimeError(f"HHX_COMPAT_TARGET は hhx か python: {TARGET!r}")

# hhx へ移植済みの hook 名。移植した hook はここへ足し、その L1・L3 を hhx に向けて全件通す。
PORTED_HOOKS = frozenset({
    "dangerous-rm-guard",
    "forbidden-term-guard",
    "git-hookspath-guard",
    "idle-wait-guard",
    "irreversible-guard",
    "pr-merge-guard",
})

# hhx で既定では無効な hook。Python 実装は常に有効なので、互換スイートでは設定ファイルで有効にして流す。
DEFAULT_OFF_HOOKS = ("git-hookspath-guard",)

# Python 本体のファイル名と hhx の hook 名が違うもの。それ以外は拡張子を除いた名前をそのまま使う。
HOOK_NAMES = {
    "worktree-guard.py": "discard-guard",
}


def _python_hooks_dir():
    directory = os.environ.get("HHX_COMPAT_PYTHON_HOOKS", "")
    if not directory:
        raise RuntimeError("HHX_COMPAT_TARGET=python には HHX_COMPAT_PYTHON_HOOKS (Python 本体のディレクトリ) が必要")
    return Path(directory).expanduser()


def _hhx_binary():
    binary = Path(os.environ.get("HHX_BIN") or COMPAT_DIR.parent / "bin" / "hhx")
    if not os.access(binary, os.X_OK):
        raise RuntimeError(f"hhx が見つからない: {binary} (make build するか HHX_BIN を指定する)")
    return str(binary)


def hook_name(script):
    """Python 本体のファイル名に対応する hhx の hook 名を返す。"""
    return HOOK_NAMES.get(script, script.removesuffix(".py"))


def hook_command_for_name(name):
    """hhx の hook を名前で起動する argv を返す。移植済みかは確かめない (ハーネス自体の検査用)。"""
    return [_hhx_binary(), "hook", name]


def hook_command(script):
    """フックを起動する argv を返す。起動対象は HHX_COMPAT_TARGET で切り替える。

    hhx では、未移植の hook を対象にしたテストを skip する。
    """
    if TARGET == "python":
        return [str(_python_hooks_dir() / script)]
    name = hook_name(script)
    if name not in PORTED_HOOKS:
        raise unittest.SkipTest(f"{name} は hhx へ未移植")
    return hook_command_for_name(name)

# 一次ゲート (モジュールトップの部分文字列判定) を通すための種文字列。
# ゲートを持つフックすべての条件を満たす語を並べてある。
GATE_SEED = "git gh curl wget core.hooksPath sleep"

# フックを起動するときの作業ディレクトリ。payload の cwd が空のときだけ本体が参照するため、
# 「git 管理下でない既知のディレクトリ」に固定して結果を決定的にする。
HOOK_RUN_CWD = tempfile.mkdtemp(prefix="claude-hooks-nonrepo-")
atexit.register(shutil.rmtree, HOOK_RUN_CWD, ignore_errors=True)

# hhx は起動のたびに設定ファイルを読む。開発者の ~/.config/hhx/config.yaml で結果が変わらないよう、
# 一時的な設定ファイルを指し、既定で無効な hook だけを有効にする。各テストの env は os.environ から作るので、全起動経路に効く。
if TARGET == "hhx":
    os.environ["HHX_CONFIG"] = os.path.join(HOOK_RUN_CWD, "hhx-config.yaml")
    with open(os.environ["HHX_CONFIG"], "w", encoding="utf-8") as _config:
        _config.write("hooks:\n" + "".join(f"  {name}:\n    enabled: true\n" for name in DEFAULT_OFF_HOOKS))

# fixture の git 呼び出しをユーザーの設定から切り離す。
# ~/.config/git 側で core.hooksPath が設定されているため、これを外さないと
# テスト用リポジトリの commit で実環境の hook が走ってしまう。
GIT_ISOLATION = {
    "GIT_CONFIG_GLOBAL": "/dev/null",
    "GIT_CONFIG_SYSTEM": "/dev/null",
    "GIT_TERMINAL_PROMPT": "0",
}
GIT_IDENTITY = (
    "-c", "user.name=hook-test",
    "-c", "user.email=hook-test@localhost",
    "-c", "commit.gpgsign=false",
    "-c", "init.defaultBranch=main",
)

_LOADED = {}


# --- フックの起動 ----------------------------------------------------------

def _decision(proc, script):
    if proc.returncode != 0:
        raise AssertionError(
            f"{script} が 0 以外で終了しました (rc={proc.returncode})\n"
            f"stdout: {proc.stdout!r}\nstderr: {proc.stderr!r}")
    if not proc.stdout.strip():
        return None, ""
    data = json.loads(proc.stdout)
    out = data["hookSpecificOutput"]
    assert out["hookEventName"] == "PreToolUse", out
    return out["permissionDecision"], out["permissionDecisionReason"]


def run_hook(script, command, cwd=None, raw=None, env=None, background=None):
    """フックを実運用と同じ経路 (stdin の JSON) で起動する。

    env は os.environ に上書きする追加の環境変数 (HOME の差し替えなど)。
    background は Bash の run_in_background。None なら本物の Claude Code と同じく
    キー自体を入れない (省略時にキーが無い経路をテストでも通すため)。
    戻り値: (decision, reason)
      decision … "deny" / "ask" / None (無出力 = 通常の permission フローに委ねる)
    """
    if raw is None:
        tool_input = {"command": command}
        if background is not None:
            tool_input["run_in_background"] = background
        raw = json.dumps({
            "session_id": "test-session",
            "transcript_path": "/dev/null",
            "hook_event_name": "PreToolUse",
            "tool_name": "Bash",
            "tool_input": tool_input,
            "cwd": HOOK_RUN_CWD if cwd is None else cwd,
        })
    proc = subprocess.run(hook_command(script), input=raw, capture_output=True,
                          text=True, timeout=120, cwd=HOOK_RUN_CWD,
                          env=env if env is None else {**os.environ, **env})
    return _decision(proc, script)


def codex_exec_pretooluse_payload(command, session_cwd, exec_workdir):
    """観測済みの Codex exec_command → PreToolUse 変換を再現する。

    Codex が functions.exec 内で発行する exec_command の要求自体は exec_workdir を
    持つ。しかし現状の Bash PreToolUse 入力には command だけが渡り、payload.cwd は
    セッション開始時のディレクトリのままになる。そのためフックからは、実際のコマンド
    実行先が session_cwd と exec_workdir のどちらなのか判別できない。

    このモックは Codex を起動せず、その境界の挙動を今後のフック開発でも再利用するための
    もの。exec_workdir は欠落が偶然ではないことをテストから見える形にするために受け取り、
    観測した Codex と同じく意図的に payload へ含めない。
    """
    assert exec_workdir, "exec_command の workdir を明示してください"
    return json.dumps({
        "session_id": "codex-mock-session",
        "transcript_path": "/dev/null",
        "hook_event_name": "PreToolUse",
        "tool_name": "Bash",
        "tool_input": {"command": command},
        "cwd": session_cwd,
    })


def run_hook_argv(script, command):
    """デバッグ経路 (argv でコマンド文字列を渡す) で起動する。"""
    proc = subprocess.run([*hook_command(script), command], capture_output=True,
                          text=True, timeout=120, cwd=HOOK_RUN_CWD)
    return _decision(proc, script)


def run_prompt_hook(script, prompt, cwd=None, session_id="test-session", env=None,
                    raw=None, timeout=120):
    """UserPromptSubmit フックを実運用と同じ経路 (stdin の JSON) で起動する。

    PreToolUse と違い、判定 JSON ではなく **stdout がそのまま Claude のコンテキストに
    追加される**。そのため戻り値は CompletedProcess とし、テスト側で stdout と
    終了コードの両方を見る (プロンプトを止めないことも契約のうち)。
    """
    if raw is None:
        raw = json.dumps({
            "session_id": session_id,
            "prompt_id": "test-prompt",
            "transcript_path": "/dev/null",
            "hook_event_name": "UserPromptSubmit",
            "prompt": prompt,
            "cwd": HOOK_RUN_CWD if cwd is None else cwd,
        })
    return subprocess.run(hook_command(script), input=raw, capture_output=True,
                          text=True, timeout=timeout, cwd=HOOK_RUN_CWD,
                          env=env if env is None else {**os.environ, **env})


def load_hook(script, seed=GATE_SEED):
    """フック本体をモジュールとして読み込む (L2 用)。

    本体を変更せずに読み込むため、argv に種文字列を置いて一次ゲートを通す。
    main() は __main__ ガードの中なので、読み込みだけでは走らない。
    hhx が対象のときは Python 本体が無いので、使われた時点でそのテストを skip する代理を返す。
    """
    if TARGET == "hhx":
        return _SkipOnUse(script)
    if script in _LOADED:
        return _LOADED[script]
    saved_argv = sys.argv
    sys.argv = ["hook-test", seed]
    try:
        name = "hook_" + script.removesuffix(".py").replace("-", "_")
        spec = importlib.util.spec_from_file_location(name, _python_hooks_dir() / script)
        assert spec is not None and spec.loader is not None, f"{script} を読み込めません"
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
    finally:
        sys.argv = saved_argv
    _LOADED[script] = module
    return module


def load_python_hook(script, seed=GATE_SEED):
    """HHX_COMPAT_TARGET によらず、Python 本体をモジュールとして読み込む (差分テスト用)。

    HHX_COMPAT_PYTHON_HOOKS が無ければ、呼んだテストを skip する。
    """
    if not os.environ.get("HHX_COMPAT_PYTHON_HOOKS"):
        raise unittest.SkipTest("差分テストには HHX_COMPAT_PYTHON_HOOKS (Python 本体のディレクトリ) が必要")
    key = ("python", script)
    if key not in _LOADED:
        saved_argv = sys.argv
        sys.argv = ["hook-test", seed]
        try:
            name = "hook_python_" + script.removesuffix(".py").replace("-", "_")
            spec = importlib.util.spec_from_file_location(name, _python_hooks_dir() / script)
            assert spec is not None and spec.loader is not None, f"{script} を読み込めません"
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
        finally:
            sys.argv = saved_argv
        _LOADED[key] = module
    return _LOADED[key]


class _SkipOnUse:
    """L2 が Python 本体の属性に触れた時点で、そのテストを skip する代理。

    モジュールや setUpClass で load_hook しても import 自体は失敗させず、同じファイルの L1 を走らせるため。
    """

    def __init__(self, script):
        self._script = script

    def __getattr__(self, name):
        raise unittest.SkipTest(f"L2 は Python 本体 ({self._script}) を import するため hhx では対象外")


# --- git fixture -----------------------------------------------------------

def git(cwd, *args, check=True):
    """テスト用の git 呼び出し (ユーザーの git 設定から隔離する)。"""
    proc = subprocess.run(["git", "-C", str(cwd), *GIT_IDENTITY, *args],
                          capture_output=True, text=True, timeout=120,
                          env={**os.environ, **GIT_ISOLATION})
    if check and proc.returncode != 0:
        raise AssertionError(f"git {' '.join(args)} が失敗しました: {proc.stderr.strip()}")
    return proc.stdout.strip()


def make_repo(path, dirty=True, ignored=False, commit=True):
    """テスト用リポジトリを作る。

    dirty=True  … tracked.txt に未コミットの変更 + untracked.txt を置く
    ignored=True… gitignore 対象の ignored.txt を置く (保存対象外であることの確認用)
    commit=False… コミットを 1 つも作らない (HEAD なしのリポジトリ)
    """
    path = Path(path)
    path.mkdir(parents=True, exist_ok=True)
    git(path, "init", "-q")
    if commit:
        (path / "tracked.txt").write_text("v1\n", encoding="utf-8")
        git(path, "add", "tracked.txt")
        git(path, "commit", "-q", "-m", "init")
    if ignored:
        (path / ".gitignore").write_text("ignored.txt\n", encoding="utf-8")
        if commit:
            git(path, "add", ".gitignore")
            git(path, "commit", "-q", "-m", "ignore")
        (path / "ignored.txt").write_text("ignored-content\n", encoding="utf-8")
    if dirty:
        if commit:
            (path / "tracked.txt").write_text("v2-uncommitted\n", encoding="utf-8")
        (path / "untracked.txt").write_text("new\n", encoding="utf-8")
    return str(path)


def snapshot_ref_exists(repo, ref="refs/claude/wt-snapshot"):
    proc = subprocess.run(["git", "-C", str(repo), "rev-parse", "--verify", "-q", ref],
                          capture_output=True, text=True, env={**os.environ, **GIT_ISOLATION})
    return proc.returncode == 0


# --- テーブル駆動のための基底クラス ----------------------------------------

class HookTestCase(unittest.TestCase):
    """契約テスト用の共通アサート。script にフックのファイル名を入れて使う。"""

    script = ""
    default_cwd = None

    def assert_decision(self, command, expected, label=None, cwd=None):
        decision, reason = run_hook(self.script, command,
                                    cwd if cwd is not None else self.default_cwd)
        self.assertEqual(decision, expected, f"コマンド: {command!r} / 理由: {reason!r}")
        if label is not None:
            self.assertIn(label, reason, f"コマンド: {command!r} の理由にラベルが出ていない")

    def check_table(self, table, expected=None, cwd=None):
        """(コマンド[, ラベル]) の表をまとめて確認する。expected は共通の期待判定。"""
        for row in table:
            command = row[0] if isinstance(row, tuple) else row
            label = row[1] if isinstance(row, tuple) and len(row) > 1 else None
            with self.subTest(command=command):
                self.assert_decision(command, expected, label, cwd)
