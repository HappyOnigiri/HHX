#!/usr/bin/env python3
"""wait-ci の差分テスト用の gh スタブ (PATH の先頭に置いて本物の gh を隠す)。

argv から呼び出しの種類を決め、シナリオのファイルに種類ごとに並べた応答を、呼ばれた順に返す。
並びを使い切ったら最後の応答を返し続ける。`--jq` の絞り込みは、絞り込んだ後の文字列を応答に書いておく。

環境変数:
  FAKE_GH_DIR … シナリオ (scenario.json)、呼び出し回数、呼び出しログ (calls.log) の置き場 (必須)

呼び出しの種類:
  view      … gh pr view [reference] --json headRefOid,mergeable,statusCheckRollup
  search    … gh pr list --search <sha> ...
  workflows … gh api repos/{owner}/{repo}/actions/workflows ...
  merged    … gh pr list --state merged ...

応答は {"stdout": 文字列, "stderr": 文字列, "code": 終了コード}。どれも省略でき、既定は空と 0。
"""
import json
import os
import sys

DIRECTORY = os.environ["FAKE_GH_DIR"]
ARGV = sys.argv[1:]

with open(os.path.join(DIRECTORY, "calls.log"), "a", encoding="utf-8") as log:
    log.write(json.dumps(ARGV) + "\n")


def kind():
    if ARGV[:2] == ["pr", "view"]:
        return "view"
    if ARGV[:2] == ["pr", "list"] and "--search" in ARGV:
        return "search"
    if ARGV[:2] == ["pr", "list"] and "merged" in ARGV:
        return "merged"
    if ARGV[:1] == ["api"] and ARGV[1:2] == ["repos/{owner}/{repo}/actions/workflows"]:
        return "workflows"
    return None


name = kind()
with open(os.path.join(DIRECTORY, "scenario.json"), encoding="utf-8") as f:
    responses = json.load(f).get(name or "", [])
if not responses:
    sys.stderr.write("fake gh: no response for %r\n" % (ARGV,))
    sys.exit(1)

counter = os.path.join(DIRECTORY, name + ".count")
try:
    with open(counter, encoding="utf-8") as f:
        count = int(f.read())
except FileNotFoundError:
    count = 0
with open(counter, "w", encoding="utf-8") as f:
    f.write(str(count + 1))

response = responses[min(count, len(responses) - 1)]
sys.stdout.write(response.get("stdout", ""))
sys.stderr.write(response.get("stderr", ""))
sys.exit(response.get("code", 0))
