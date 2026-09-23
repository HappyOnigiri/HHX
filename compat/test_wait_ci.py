#!/usr/bin/env python3
"""wait-ci の差分テスト (Python 実装と `hhx wait-ci`)。

実行: HHX_COMPAT_PYTHON_WAIT_CI=<Python 実装の wait-ci> make compat-test（compat/README.md を参照）

移植元の Python テストはモジュールを import して内部関数を差し替える形なので、hhx に向けてそのまま流せない。
代わりに、偽の gh (fake_gh_scenario.py) と本物の git を使ったシナリオで両方をプロセスとして起動し、
終了コード・stdout・gh の呼び出しの種類を比べる。所要秒は実時間で揺れるので、結論行と `PR head:` 行の秒数は
正規化してから比べる。進捗 (stderr) は poll の回数が揺れないシナリオでだけ比べる。

対象は HHX_COMPAT_TARGET に従う。hhx では各シナリオの期待する終了コードを hhx で確かめ、
HHX_COMPAT_PYTHON_WAIT_CI を渡したときは Python 実装とも突き合わせる。python では Python 実装だけを期待と比べる
(シナリオの期待値そのものが移植元の挙動と合っているかの確認)。
"""
import concurrent.futures
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest

import helpers

PYTHON_WAIT_CI = os.environ.get("HHX_COMPAT_PYTHON_WAIT_CI", "")
FAKE_GH = helpers.COMPAT_DIR / "fake_gh_scenario.py"

HEAD = "{HEAD}"
OLD = "1" * 40
OTHER = "2" * 40
FAST = ["--interval", "1", "--settle", "1"]


def check_run(name, status="COMPLETED", conclusion="SUCCESS", **extra):
    node = {"__typename": "CheckRun", "name": name, "status": status, "conclusion": conclusion,
            "detailsUrl": f"https://example.test/{name}", "workflowName": "CI"}
    node.update(extra)
    return node


def view(head=HEAD, checks=(), mergeable="MERGEABLE"):
    return {"stdout": json.dumps({"headRefOid": head, "mergeable": mergeable, "statusCheckRollup": list(checks)})}


def failure(stderr, code=1):
    return {"stderr": stderr, "code": code}


DONE = [check_run("build", startedAt="2026-09-09T23:20:01Z", completedAt="2026-09-09T23:21:31Z"),
        check_run("lint")]
FAILED = [check_run("tests", conclusion="FAILURE"), check_run("build"),
          {"__typename": "StatusContext", "context": "legacy/ci", "state": "ERROR", "targetUrl": "https://example.test/legacy"},
          check_run("zeta", conclusion="SKIPPED")]
RUNNING = [check_run("build", status="IN_PROGRESS", conclusion="")]

# (名前, 引数, git の状態, gh の応答, 期待する終了コード, stderr も比べるか)
# git の状態: branch (branch 上のリポジトリ)、detached (detached HEAD)、none (git 管理下でない)
SCENARIOS = [
    ("all pass", FAST, "branch", {"view": [view(checks=DONE)]}, 0, True),
    ("all pass with details", FAST + ["--all-checks", "-v"], "branch", {"view": [view(checks=DONE)]}, 0, True),
    ("failures", FAST, "branch", {"view": [view(checks=FAILED)]}, 1, True),
    ("failures with all checks", FAST + ["--all-checks", "--progress"], "branch", {"view": [view(checks=FAILED)]}, 1,
     True),
    ("head wait then pass", FAST + ["-v"], "branch",
     {"view": [view(OLD, DONE), view(OLD, DONE), view(checks=RUNNING), view(checks=DONE)]}, 0, True),
    ("late check during settle", FAST + ["--settle", "2", "-v"], "branch",
     {"view": [view(checks=FAILED[:1]), view(checks=FAILED[:1] + RUNNING),
               view(checks=FAILED[:1] + [check_run("build", conclusion="FAILURE")])]}, 1, True),
    ("conflict without checks", FAST + ["-v"], "branch", {"view": [view(mergeable="CONFLICTING")]}, 5, True),
    ("conflict with checks", FAST + ["-v"], "branch",
     {"view": [view(checks=RUNNING, mergeable="CONFLICTING"), view(checks=DONE, mergeable="CONFLICTING")]}, 0, True),
    ("unknown mergeable", FAST, "branch", {"view": [view(mergeable="UNKNOWN"), view(checks=DONE, mergeable="UNKNOWN")]},
     0, True),
    ("no ci", FAST + ["--no-ci-timeout", "1", "--start-timeout", "5", "-v"], "branch",
     {"view": [view()], "workflows": [{"stdout": "0\n"}], "merged": [{"stdout": "0\n"}]}, 0, False),
    ("external status counts as ci", FAST + ["--no-ci-timeout", "1", "--start-timeout", "3"], "branch",
     {"view": [view()], "workflows": [{"stdout": "0\n"}], "merged": [{"stdout": "4\n"}]}, 3, False),
    ("undecidable ci evidence", FAST + ["--no-ci-timeout", "0", "--start-timeout", "2", "-v"], "branch",
     {"view": [view()], "workflows": [{"stdout": "null\n"}]}, 3, False),
    ("empty timeout", FAST + ["--no-ci-timeout", "1", "--start-timeout", "3"], "branch",
     {"view": [view()], "workflows": [{"stdout": "2\n"}]}, 3, False),
    ("overall timeout", FAST + ["--timeout", "2"], "branch", {"view": [view(checks=RUNNING + DONE[1:])]}, 3, False),
    ("head timeout", FAST + ["--start-timeout", "2"], "branch", {"view": [view(OLD, DONE)]}, 3, False),
    ("new push switches the target", ["7"] + FAST + ["-v"], "branch",
     {"view": [view(OLD, RUNNING), view(OTHER, RUNNING), view(OTHER, DONE)]}, 0, True),
    ("explicit sha", ["7", "--sha", OTHER] + FAST, "branch", {"view": [view(OLD, DONE), view(OTHER, DONE)]}, 0, True),
    ("sha HEAD", ["7", "--sha", "HEAD"] + FAST, "branch", {"view": [view(OLD, DONE), view(checks=DONE)]}, 0, True),
    ("any sha", ["--any-sha"] + FAST, "branch", {"view": [view(OLD, DONE)]}, 0, True),
    ("no pull request", FAST, "branch", {"view": [failure('no pull requests found for branch "main"\n')]}, 0, True),
    ("repeated gh failures", FAST + ["-v"], "branch", {"view": [failure("HTTP 502: Bad Gateway\n")]}, 4, True),
    ("fatal gh failure", FAST + ["-v"], "branch",
     {"view": [failure("GraphQL: Could not resolve to a PullRequest with the number of 7.\n")]}, 4, True),
    ("transient gh failure", FAST + ["-v"], "branch",
     {"view": [failure("HTTP 502\n"), failure("HTTP 502\n"), view(checks=DONE)]}, 0, True),
    ("unreadable json", FAST, "branch", {"view": [{"stdout": "{ this is not json"}]}, 4, True),
    ("detached head resolves the pr", FAST + ["-v"], "detached",
     {"search": [{"stdout": ""}, {"stdout": "7\n"}], "view": [view(checks=DONE)]}, 0, True),
    ("detached head without a pr", FAST + ["--pr-lookup-timeout", "2", "-v"], "detached", {"search": [{"stdout": ""}]},
     0, False),
    ("detached head with zero lookup timeout", FAST + ["--pr-lookup-timeout", "0"], "detached",
     {"search": [{"stdout": ""}]}, 0, True),
    ("detached head lookup failure", FAST, "detached", {"search": [failure("could not resolve to a Repository\n")]}, 4,
     True),
    ("detached head without a pr on view", FAST, "detached",
     {"search": [{"stdout": "7\n"}], "view": [failure("no open pull requests\n")]}, 0, True),
    ("outside a repository", ["7"] + FAST, "none", {"view": [view(OLD, DONE)]}, 0, True),
    ("outside a repository without a reference", FAST, "none", {"view": [view(OLD, DONE)]}, 0, True),
    ("interval below one", ["--interval", "0"], "branch", {}, 2, True),
    ("argument error", ["--interval", "abc"], "branch", {}, 2, False),
    ("extra positional", ["1", "2"], "branch", {}, 2, False),
    ("missing sha value", ["--sha", "--progress"], "branch", {}, 2, False),
]

# 秒数が実時間で揺れる行。結論行 (wait-ci: ...) と PR head: 行の「<数字>s」を正規化する。
SECONDS = re.compile(r"(?<![\w.])\d+s(?![\w])")
# JSON の構文エラーの文言は Python と Go で違う (Go は encoding/json のエラーを載せる)。
JSON_ERROR = re.compile(r"(JSON として読めない: ).*")


def normalize(text):
    lines = []
    for line in text.splitlines():
        if line.startswith(("wait-ci:", "PR head:")):
            line = SECONDS.sub("<n>s", line)
            line = JSON_ERROR.sub(r"\1<error>", line)
        lines.append(line)
    return lines


class WaitCIFixture:
    """git の状態ごとの作業ディレクトリと、偽の gh を置いた PATH。"""

    def __init__(self, root):
        self.root = root
        self.bin = os.path.join(root, "bin")
        os.makedirs(self.bin)
        gh = os.path.join(self.bin, "gh")
        with open(gh, "w", encoding="utf-8") as f:
            f.write(f'#!/bin/sh\nexec "{sys.executable}" "{FAKE_GH}" "$@"\n')
        os.chmod(gh, 0o755)
        self.cwd = {}
        self.head = {}
        for state in ("branch", "detached"):
            repo = helpers.make_repo(os.path.join(root, state), dirty=False)
            if state == "detached":
                helpers.git(repo, "checkout", "-q", "--detach")
            self.cwd[state] = repo
            self.head[state] = helpers.git(repo, "rev-parse", "HEAD")
        self.cwd["none"] = os.path.join(root, "none")
        os.makedirs(self.cwd["none"])
        self.head["none"] = ""
        self.counter = 0
        self.lock = threading.Lock()

    def run(self, command, state, responses):
        """command を state の作業ディレクトリで起動し、(終了コード, stdout, stderr, gh の呼び出し) を返す。"""
        with self.lock:
            self.counter += 1
            directory = os.path.join(self.root, f"gh-{self.counter}")
        os.makedirs(directory)
        text = json.dumps(responses).replace(HEAD, self.head[state] or OLD)
        with open(os.path.join(directory, "scenario.json"), "w", encoding="utf-8") as f:
            f.write(text)
        env = {key: value for key, value in os.environ.items() if not key.startswith(("GH_", "GITHUB_"))}
        env.update(helpers.GIT_ISOLATION)
        env.update({"FAKE_GH_DIR": directory, "PATH": self.bin + os.pathsep + os.environ.get("PATH", "")})
        proc = subprocess.run(command, cwd=self.cwd[state], env=env, capture_output=True, text=True, timeout=120)
        calls = set()
        log = os.path.join(directory, "calls.log")
        if os.path.exists(log):
            with open(log, encoding="utf-8") as f:
                calls = {line.strip() for line in f}
        return proc.returncode, proc.stdout, proc.stderr, calls


def commands():
    """比べる実装の起動コマンド。"""
    targets = {}
    if helpers.TARGET == "hhx":
        targets["hhx"] = [helpers._hhx_binary(), "wait-ci"]
    if PYTHON_WAIT_CI:
        targets["python"] = [sys.executable, os.path.expanduser(PYTHON_WAIT_CI)]
    elif helpers.TARGET == "python":
        raise unittest.SkipTest("HHX_COMPAT_TARGET=python には HHX_COMPAT_PYTHON_WAIT_CI (Python 実装の wait-ci) が必要")
    return targets


class WaitCIScenarioTest(unittest.TestCase):

    @classmethod
    def setUpClass(cls):
        cls.targets = commands()
        cls.root = tempfile.mkdtemp(prefix="hhx-wait-ci-")
        cls.fixture = WaitCIFixture(cls.root)
        # シナリオは秒単位で実時間を待つので、実装とシナリオの組を並行に流す。
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
            futures = {(title, name): pool.submit(cls.fixture.run, [*command, *args], state, responses)
                       for title, args, state, responses, _, _ in SCENARIOS
                       for name, command in cls.targets.items()}
        cls.results = {key: future.result() for key, future in futures.items()}

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.root, ignore_errors=True)

    def test_exit_codes_and_output_contract(self):
        for scenario in SCENARIOS:
            title, args, _, _, code, _ = scenario
            for name in self.targets:
                with self.subTest(scenario=title, target=name):
                    returned, stdout, stderr, _ = self.results[(title, name)]
                    self.assertEqual(code, returned, f"stdout: {stdout!r}\nstderr: {stderr!r}")
                    progress = "-v" in args or "--progress" in args
                    if code == 2 and stdout == "":
                        continue  # 引数の解析の失敗は最終行を出さない (argparse と同じ)
                    lines = stdout.splitlines()
                    self.assertTrue(lines[0].startswith("wait-ci: "), stdout)
                    self.assertRegex(lines[-1], rf"^wait-ci: exit={code} failed=\d+ total=\d+$")
                    if progress:
                        for line in stderr.splitlines():
                            self.assertTrue(line.startswith("wait-ci: [progress] "), stderr)
                    else:
                        self.assertEqual("", stderr)

    def test_hhx_matches_the_python_implementation(self):
        if len(self.targets) < 2:
            self.skipTest("差分テストには HHX_COMPAT_PYTHON_WAIT_CI (Python 実装の wait-ci) が必要")
        for title, _, _, _, _, compare_stderr in SCENARIOS:
            with self.subTest(scenario=title):
                hhx = self.results[(title, "hhx")]
                python = self.results[(title, "python")]
                self.assertEqual(python[0], hhx[0], "終了コード")
                if python[0] == 2 and python[1] == "":
                    # 引数の解析の失敗。stderr の文言は argparse と pflag で違うので、stdout が空であることだけを比べる。
                    self.assertEqual("", hhx[1])
                    continue
                self.assertEqual(normalize(python[1]), normalize(hhx[1]), "stdout")
                self.assertEqual(python[3], hhx[3], "gh の呼び出し")
                if compare_stderr:
                    self.assertEqual(normalize_progress(python[2]), normalize_progress(hhx[2]), "stderr")


def normalize_progress(text):
    return [SECONDS.sub("<n>s", line) for line in text.splitlines()]


if __name__ == "__main__":
    unittest.main()
