#!/usr/bin/env python3
"""テスト用の gh スタブ (PATH の先頭に置いて本物の gh を隠す)。

フックは gh の応答をそのまま整形して出すだけなので、ここを差し替えれば
ネットワークも認証も無しに実運用と同じ経路を通せる。

環境変数:
  FAKE_GH_DIR   … フィクスチャと呼び出しログの置き場 (必須)
  FAKE_GH_MODE  … ok (既定) / fail / garbage / empty / hang
  FAKE_GH_SLEEP … hang のときに眠る秒数

フィクスチャのファイル名:
  gh pr view <n> --repo <owner>/<repo>  → pr_<owner>_<repo>_<n>.json
  gh api repos/<owner>/<repo>/pulls/comments/<id> → comment_<id>.json
  gh api graphql -f query=... → graphql.json (クエリの中身では振り分けない)
呼び出しは calls.log に 1 行 1 JSON で追記する (cwd も記録する。認証の都合で
「どのリポジトリの中で実行したか」が意味を持つため)。
"""
import json
import os
import sys
import time

FIXTURES = os.environ["FAKE_GH_DIR"]
ARGV = sys.argv[1:]

with open(os.path.join(FIXTURES, "calls.log"), "a", encoding="utf-8") as log:
    log.write(json.dumps({"argv": ARGV, "cwd": os.getcwd()}) + "\n")

MODE = os.environ.get("FAKE_GH_MODE", "ok")
if MODE == "fail":
    sys.stderr.write("could not resolve to a PullRequest\n")
    sys.exit(1)
if MODE == "garbage":
    sys.stdout.write("{ this is not json")
    sys.exit(0)
if MODE == "empty":
    sys.exit(0)
if MODE == "hang":
    time.sleep(float(os.environ.get("FAKE_GH_SLEEP", "30")))
    sys.exit(0)

name = None
if ARGV[:2] == ["pr", "view"] and "--repo" in ARGV:
    name = "pr_%s_%s.json" % (ARGV[ARGV.index("--repo") + 1].replace("/", "_"), ARGV[2])
elif ARGV[:2] == ["api", "graphql"]:
    name = "graphql.json"
elif ARGV[:1] == ["api"] and "/pulls/comments/" in ARGV[1]:
    name = "comment_%s.json" % ARGV[1].rsplit("/", 1)[-1]

path = os.path.join(FIXTURES, name) if name else None
if not path or not os.path.exists(path):
    sys.stderr.write("fake gh: no fixture for %r\n" % (ARGV,))
    sys.exit(1)
with open(path, encoding="utf-8") as f:
    sys.stdout.write(f.read())
