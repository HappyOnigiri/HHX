#!/usr/bin/env python3
"""irreversible-guard.py のテスト。

実行: make compat-test（compat/README.md を参照）

このフックは「実行するとユーザーにも戻せない」操作と、秘密ファイルへの直接書き込みを
deny する。カテゴリ A〜G は本体の docstring と対応させてあるので、ルールを足したら
このファイルにも同じ区分で置く。
表記ゆれ (パス起動・区切り文字直結・rtk ラッパー・改行区切り・--method 表記) は
カテゴリごとに 1 本ずつ持つ。誤爆がこのフックの実害なので AllowedTest を厚く保つ。
"""
import json
import os
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

from helpers import (TARGET, HookTestCase, git, hook_command, hook_command_for_name, load_hook, make_repo,
                     run_hook, run_hook_argv)

SCRIPT = "irreversible-guard.py"

# ブロック理由に現れるラベル (どのルールが発火したかの判別用)
L_REVOCATION_API = "GitHub Credential Revocation API"
L_REVOKE_HTTP = "失効 (revoke) エンドポイントへの HTTP リクエスト"
L_GH_API_REVOKE = "gh api の失効 (revoke) エンドポイント"
L_GH_LOGOUT = "gh auth logout"
L_GH_SECRET = "GitHub 上の Secret・鍵の削除"
L_GH_DEPLOY_KEY = "GitHub 上の Deploy Key の削除"
L_KEYCHAIN = "Keychain の削除"
L_GCLOUD_REVOKE = "gcloud の認証失効"
L_NPM_TOKEN = "npm token revoke"
L_GPG_SECRET = "GPG 秘密鍵の削除"
L_OP_ITEM = "1Password アイテムの削除"
L_AWS_SECRET = "AWS Secrets Manager の即時完全削除"

L_NPM_PUBLISH = "npm レジストリへの公開・取り下げ"
L_YARN_PUBLISH = "npm レジストリへの公開 (yarn)"
L_GEM_PUBLISH = "RubyGems への公開・取り下げ"
L_TWINE_PUBLISH = "PyPI への公開 (twine upload)"
L_CARGO_PUBLISH = "crates.io への公開・取り下げ"

L_GH_DELETE = "gh の恒久削除 (repo/release/gist)"
L_GH_API_DELETE = "gh api の DELETE"
L_GCLOUD_DELETE = "gcloud の削除操作"
L_AWS_DELETE = "aws の削除操作"
L_AWS_S3 = "aws s3 のオブジェクト・バケット削除"
L_TF_DESTROY = "terraform destroy"
L_TF_APPLY_DESTROY = "terraform apply -destroy"
L_MYSQLADMIN = "mysqladmin drop"
L_HTTP_DELETE = "HTTP クライアントによるクラウド API の DELETE"
L_DB = "データベースの破壊操作"

L_REFLOG = "git reflog expire"
L_GC = "git gc --prune=now"
L_STASH = "git stash clear"

L_DISKUTIL = "diskutil によるディスク消去"
L_APFS = "diskutil apfs delete*"
L_TMUTIL = "Time Machine バックアップの削除"

L_SECRET_RM = "秘密ファイルの削除・破壊"
L_SECRET_INPLACE = "秘密ファイルの in-place 書き換え"
L_SECRET_MV = "秘密ファイルの移動"
L_SECRET_REDIRECT = "秘密ファイルの上書きリダイレクト"
L_SECRET_TEE = "秘密ファイルへの tee 書き込み"
L_SECRET_EDIT = "秘密ファイルへの直接編集"
L_SECRET_PATCH = "秘密ファイルへのパッチ適用"

L_GUARD = "エージェントガードの削除・移動"


# --- L1 契約テスト: ブロック側 ---------------------------------------------

class CredentialTest(HookTestCase):
    """A. 資格情報の失効・削除。値を読み返せないので復元できない。"""

    script = SCRIPT

    def test_revocation_api(self):
        self.check_table([
            ("curl -sS -X POST -H 'Accept: application/vnd.github+json' "
             "https://api.github.com/credentials/revoke -d '{\"credentials\":[\"x\"]}'",
             L_REVOCATION_API),
            ("wget --post-data='{}' https://api.github.com/credentials/revoke",
             L_REVOCATION_API),
            ("curl -X POST https://slack.com/api/auth.revoke", L_REVOKE_HTTP),
            ("curl -X POST https://oauth2.googleapis.com/revoke -d token=x", L_REVOKE_HTTP),
            ("gh api -X POST /credentials/revoke", L_GH_API_REVOKE),
        ], expected="deny")

    def test_cli_credential_removal(self):
        self.check_table([
            ("gh auth logout", L_GH_LOGOUT),
            ("gh auth logout --hostname github.com", L_GH_LOGOUT),
            ("gh secret delete GH_TOKEN", L_GH_SECRET),
            ("gh variable delete FOO -R o/r", L_GH_SECRET),
            ("gh ssh-key delete 123 --yes", L_GH_SECRET),
            ("gh gpg-key delete 1", L_GH_SECRET),
            ("gh repo deploy-key delete 1", L_GH_DEPLOY_KEY),
            ("security delete-generic-password -a HappyOnigiri -s gh-token", L_KEYCHAIN),
            ("security delete-keychain login.keychain", L_KEYCHAIN),
            ("security delete-internet-password -s example.com", L_KEYCHAIN),
            ("gcloud auth revoke", L_GCLOUD_REVOKE),
            ("gcloud auth application-default revoke", L_GCLOUD_REVOKE),
            ("npm token revoke abcd", L_NPM_TOKEN),
            ("gpg --batch --delete-secret-key ABCD", L_GPG_SECRET),
            ("gpg2 --delete-secret-key ABCD", L_GPG_SECRET),
            ("op item delete 'GitHub PAT'", L_OP_ITEM),
            ("aws secretsmanager delete-secret --secret-id x --force-delete-without-recovery",
             L_AWS_SECRET),
            ("gcloud iam service-accounts keys delete abcdef --iam-account=x@y.iam",
             L_GCLOUD_DELETE),
            ("aws iam delete-access-key --access-key-id AKIAXXXX", L_AWS_DELETE),
        ], expected="deny")

    def test_notation_variants(self):
        """パス起動・区切り文字直結・rtk ラッパー・改行区切り。"""
        self.check_table([
            ("/usr/local/bin/gh auth logout", L_GH_LOGOUT),
            ("./gh auth logout", L_GH_LOGOUT),
            ("/usr/bin/security delete-internet-password -s example.com", L_KEYCHAIN),
            ("/usr/local/bin/gcloud auth revoke", L_GCLOUD_REVOKE),
            ("true;gh auth logout", L_GH_LOGOUT),
            ("(gh auth logout)", L_GH_LOGOUT),
            ("false||gh auth logout", L_GH_LOGOUT),
            ("cd /tmp && gh ssh-key delete 1 --yes", L_GH_SECRET),
            ("rtk gh secret delete GH_TOKEN", L_GH_SECRET),
            ("echo start\ngh auth logout", L_GH_LOGOUT),
            ("gh  auth\tlogout", L_GH_LOGOUT),
        ], expected="deny")


class PublishTest(HookTestCase):
    """B. レジストリへの公開・取り下げ。--dry-run 付きだけ通す。"""

    script = SCRIPT

    def test_registry_publish(self):
        self.check_table([
            ("npm publish", L_NPM_PUBLISH),
            ("npm publish --tag next", L_NPM_PUBLISH),
            ("npm publish --otp=123456", L_NPM_PUBLISH),
            ("pnpm publish --access public", L_NPM_PUBLISH),
            ("npm unpublish pkg@1.0.0", L_NPM_PUBLISH),
            ("npm deprecate pkg@1.0.0 'msg'", L_NPM_PUBLISH),
            ("yarn npm publish", L_NPM_PUBLISH),   # npm ルールが先に当たる (判定は同じ)
            ("yarn publish", L_YARN_PUBLISH),
            ("gem push pkg-1.0.0.gem", L_GEM_PUBLISH),
            ("gem yank pkg -v 1.0.0", L_GEM_PUBLISH),
            ("twine upload dist/*", L_TWINE_PUBLISH),
            ("python3 -m twine upload dist/*", L_TWINE_PUBLISH),
            ("cargo publish", L_CARGO_PUBLISH),
            ("cargo yank --vers 1.0.0", L_CARGO_PUBLISH),
        ], expected="deny")

    def test_notation_variants(self):
        self.check_table([
            ("/opt/homebrew/bin/npm publish", L_NPM_PUBLISH),
            ("/usr/local/bin/gem push pkg-1.0.0.gem", L_GEM_PUBLISH),
            ("true;npm publish", L_NPM_PUBLISH),
            ("cd pkg && cargo publish", L_CARGO_PUBLISH),
            ("(npm publish)", L_NPM_PUBLISH),
            ("echo build\nnpm publish", L_NPM_PUBLISH),
        ], expected="deny")

    def test_dry_run_anywhere_allows(self):
        """--dry-run が付いていれば公開されないので通す。"""
        self.check_table([
            "npm publish --dry-run",
            "npm publish --dry-run --tag next",
            "cargo publish --dry-run",
            "gem push --dry-run pkg-1.0.0.gem",
            "twine upload --dry-run dist/*",
        ], expected=None)


class RemoteDeleteTest(HookTestCase):
    """C. リモートリソースの恒久削除。ゴミ箱も履歴も残らない。"""

    script = SCRIPT

    def test_gh_and_cloud(self):
        self.check_table([
            ("gh repo delete o/r --yes", L_GH_DELETE),
            ("gh release delete v1.0 --yes", L_GH_DELETE),
            ("gh gist delete abc123", L_GH_DELETE),
            ("gh api -X DELETE repos/o/r/releases/1", L_GH_API_DELETE),
            ("gcloud compute instances delete my-vm", L_GCLOUD_DELETE),
            ("aws s3 rb s3://bucket --force", L_AWS_S3),
            ("aws s3 rm s3://bucket/key", L_AWS_S3),
            ("terraform destroy", L_TF_DESTROY),
            ("terraform -chdir=infra destroy -auto-approve", L_TF_DESTROY),
            ("tofu destroy", L_TF_DESTROY),
            ("terraform apply -destroy", L_TF_APPLY_DESTROY),
            ("mysqladmin drop mydb", L_MYSQLADMIN),
        ], expected="deny")

    def test_database_statements(self):
        """DB クライアント経由のときだけ見る (大文字小文字は問わない)。"""
        self.check_table([
            ("psql -c 'DROP TABLE users'", L_DB),
            ("psql -c 'drop table users'", L_DB),
            ("psql -c 'DROP DATABASE app'", L_DB),
            ("psql -c 'DROP SCHEMA public CASCADE'", L_DB),
            ("mysql -e 'TRUNCATE sessions'", L_DB),
            ("mariadb -e 'DROP TABLE t'", L_DB),
            ("sqlite3 app.db 'DROP TABLE t'", L_DB),
        ], expected="deny")

    def test_method_notation_is_normalized(self):
        """--method=DELETE / --method DELETE / -XDELETE を -X DELETE に寄せる。"""
        self.check_table([
            ("gh api -X DELETE /repos/o/r", L_GH_API_DELETE),
            ("gh api -XDELETE repos/o/r", L_GH_API_DELETE),
            ("gh api --method DELETE /repos/o/r", L_GH_API_DELETE),
            ("gh api --method=DELETE /repos/o/r/actions/secrets/FOO", L_GH_API_DELETE),
            ("gh api -X delete /repos/o/r", L_GH_API_DELETE),
            ("curl -X DELETE https://api.github.com/user/keys/1", L_HTTP_DELETE),
            ("curl -XDELETE https://api.github.com/repos/o/r", L_HTTP_DELETE),
            ("curl --method=DELETE https://storage.googleapis.com/b/o", L_HTTP_DELETE),
            ("curl https://s3.amazonaws.com/b/o -X DELETE", L_HTTP_DELETE),
            ("wget --method=DELETE https://api.github.com/repos/o/r", L_HTTP_DELETE),
        ], expected="deny")

    def test_notation_variants(self):
        self.check_table([
            ("/opt/homebrew/bin/terraform destroy", L_TF_DESTROY),
            ("/usr/local/bin/aws s3 rb s3://bucket", L_AWS_S3),
            ("/usr/local/bin/psql -c 'DROP DATABASE app'", L_DB),
            ("true;terraform destroy", L_TF_DESTROY),
            ("git status&&gh repo delete o/r --yes", L_GH_DELETE),
            ("rtk gh repo delete o/r --yes", L_GH_DELETE),
            ("echo hi\ngh release delete v1 --yes", L_GH_DELETE),
        ], expected="deny")


class GitObjectTest(HookTestCase):
    """D. Git オブジェクトの物理破壊。reflog / fsck の復元余地ごと消える。"""

    script = SCRIPT

    def test_destructive_git(self):
        self.check_table([
            ("git reflog expire --expire=now --all", L_REFLOG),
            ("git gc --prune=now", L_GC),
            ("git gc --aggressive --prune=all", L_GC),
            ("git stash clear", L_STASH),
        ], expected="deny")

    def test_notation_variants(self):
        self.check_table([
            ("/usr/bin/git reflog expire --all", L_REFLOG),
            ("git -C /repo stash clear", L_STASH),
            ("true;git stash clear", L_STASH),
            ("rtk git gc --prune=now", L_GC),
            ("echo x\ngit reflog expire --expire=now --all", L_REFLOG),
        ], expected="deny")


class MachineEraseTest(HookTestCase):
    """E. マシン上の不可逆消去。"""

    script = SCRIPT

    def test_disk_and_backup(self):
        self.check_table([
            ("diskutil eraseDisk APFS Blank disk2", L_DISKUTIL),
            ("diskutil eraseVolume APFS Blank disk2s1", L_DISKUTIL),
            ("diskutil zeroDisk disk2", L_DISKUTIL),
            ("diskutil apfs deleteContainer disk2", L_APFS),
            ("tmutil delete -p /Volumes/TM/backup", L_TMUTIL),
        ], expected="deny")

    def test_notation_variants(self):
        self.check_table([
            ("/usr/sbin/diskutil eraseVolume APFS Blank disk2s1", L_DISKUTIL),
            ("/usr/bin/tmutil delete -p /Volumes/TM/x", L_TMUTIL),
            ("true;tmutil delete -p /Volumes/TM/x", L_TMUTIL),
            ("echo x\ndiskutil eraseDisk APFS Blank disk2", L_DISKUTIL),
        ], expected="deny")


class SecretFileTest(HookTestCase):
    """F. Git 管理外の秘密ファイルへの書き込み・破壊。読み取りは通す。"""

    script = SCRIPT

    def test_delete(self):
        self.check_table([
            ("rm .envrc", L_SECRET_RM),
            ("rm -f /Users/alice/dotfiles/.envrc", L_SECRET_RM),
            ("rm .env", L_SECRET_RM),
            ("shred -u .env.local", L_SECRET_RM),
            ("truncate -s 0 .envrc", L_SECRET_RM),
            ("unlink ~/.ssh/id_rsa", L_SECRET_RM),
            ("rm ~/.ssh/id_ed25519", L_SECRET_RM),
            ("rm -rf ~/.ssh", L_SECRET_RM),
            ("rm certs/server.pem", L_SECRET_RM),
            ("srm secrets/key.pem", L_SECRET_RM),
            ("rm ~/.config/dotfiles/envrc.local", L_SECRET_RM),
        ], expected="deny")

    def test_overwrite(self):
        self.check_table([
            ("sed -i '' '/GH_TOKEN/d' .envrc", L_SECRET_INPLACE),
            ("sed -i.bak 's/a/b/' .envrc", L_SECRET_INPLACE),
            ("perl -i -pe 's/x/y/' .env.production", L_SECRET_INPLACE),
            ("mv ~/.config/dotfiles/envrc.local /tmp/", L_SECRET_MV),
            ("mv .envrc /tmp/x", L_SECRET_MV),
            ("echo FOO=1 > .env", L_SECRET_REDIRECT),
            ("echo x >.env", L_SECRET_REDIRECT),          # 空白なしのリダイレクト
            ("printf 'x' > /Users/alice/dev/app/.envrc", L_SECRET_REDIRECT),
            ("cat other | tee .envrc", L_SECRET_TEE),
            ("tee .env < input", L_SECRET_TEE),
        ], expected="deny")

    def test_notation_variants(self):
        self.check_table([
            ("/bin/rm .envrc", L_SECRET_RM),              # パス起動
            ("true;rm .envrc", L_SECRET_RM),
            ("(rm .envrc)", L_SECRET_RM),
            ("cat x\nrm .env", L_SECRET_RM),
            ("cd /repo && rm .envrc", L_SECRET_RM),
        ], expected="deny")


class GuardFileTest(HookTestCase):
    """G. ガード自体の削除・移動。編集は通常のメンテナンスなので通す。"""

    script = SCRIPT

    def test_guard_removal(self):
        self.check_table([
            ("rm ~/.codex/hooks.json", L_GUARD),
            ("rm ~/.codex/config.toml", L_GUARD),
            ("rm ~/.claude/settings.json", L_GUARD),
            ("rm -f ~/.claude/settings.local.json", L_GUARD),
            ("mv /Users/alice/.codex/hooks.json /Users/alice/.Trash/x/", L_GUARD),
            ("trash ~/.codex/config.toml", L_GUARD),
        ], expected="deny")

    def test_notation_variants(self):
        self.check_table([
            ("/bin/rm ~/.claude/settings.local.json", L_GUARD),
            ("true;rm ~/.claude/settings.json", L_GUARD),
            ("echo x\nrm ~/.codex/hooks.json", L_GUARD),
        ], expected="deny")

    def test_legacy_hook_locations(self):
        """Python 実装の配布先 (~/.claude/hooks・~/.codex/hooks) と dotfiles の正本。

        hhx では hook の実体がもう無いので、保護対象から外した (意図した仕様の変更)。
        Python 実装を対象にしたときは、元の期待値 (deny) で流す。
        """
        self.check_table([
            ("rm -rf ~/.codex/hooks", L_GUARD),
            ("rm ~/.claude/hooks/irreversible-guard.py", L_GUARD),
            ("mv ~/.claude/hooks ~/.Trash/", L_GUARD),
            ("rm -rf /Users/alice/.codex/hooks", L_GUARD),
            ("mv ~/.claude/hooks/pr-merge-guard.py /tmp/", L_GUARD),
            ("rm files/agent-config/claude/hooks/irreversible-guard.py", L_GUARD),
            ("rm -rf ~/dotfiles/files/agent-config/claude/hooks", L_GUARD),
            ("rm files/agent-config/claude/settings.common.json", L_GUARD),
            ("mv files/agent-config/codex/hooks.json /tmp/", L_GUARD),
        ] if TARGET == "python" else [
            "rm -rf ~/.codex/hooks",
            "rm ~/.claude/hooks/irreversible-guard.py",
            "mv ~/.claude/hooks ~/.Trash/",
            "rm -rf /Users/alice/.codex/hooks",
            "mv ~/.claude/hooks/pr-merge-guard.py /tmp/",
            "rm files/agent-config/claude/hooks/irreversible-guard.py",
            "rm -rf ~/dotfiles/files/agent-config/claude/hooks",
            "rm files/agent-config/claude/settings.common.json",
            "mv files/agent-config/codex/hooks.json /tmp/",
        ], expected="deny" if TARGET == "python" else None)

    @unittest.skipUnless(TARGET == "hhx", "hhx の実行ファイルと設定ディレクトリは hhx だけが守る")
    def test_hhx_itself(self):
        """hhx の実行ファイル (起動したもの) と設定ディレクトリ (~/.config/hhx) の削除・移動。"""
        binary = hook_command_for_name("irreversible-guard")[0]
        self.check_table([
            (f"rm {binary}", L_GUARD),
            (f"mv {binary} /tmp/", L_GUARD),
            ("rm -rf ~/.config/hhx", L_GUARD),
            ("rm ~/.config/hhx/config.yaml", L_GUARD),
            ("mv $HOME/.config/hhx /tmp/", L_GUARD),
        ], expected="deny")
        self.check_table([
            f"cp {binary} /tmp/hhx",
            f"rm {binary}.bak",
            "rm ~/.config/hhx.bak",
            "vim ~/.config/hhx/config.yaml",
        ], expected=None)


# --- L1 契約テスト: 通過側 ---------------------------------------------------

class AllowedTest(HookTestCase):
    """通過すべきコマンド。誤爆 (正当な操作が止まる) がこのフックの実害なので厚めに置く。"""

    script = SCRIPT

    def test_unrelated_commands(self):
        self.check_table([
            "ls -la",
            "npm install",
            "npm run build",
            "npm run publish-docs",
            "npm run env-check",
            "yarn install",
            "cargo build --release",
            "gem install rails",
            "python3 -c 'print(1)'",
            "make merge",
            "direnv allow",
        ], expected=None)

    def test_read_only_and_reversible(self):
        self.check_table([
            "git status",
            "git log --oneline -5",
            "git stash",
            "git stash pop",
            "git stash list",
            "git stash drop stash@{0}",
            "git reflog",
            "git gc",
            "git gc --prune=2.weeks.ago",
            "git fetch --all --prune",
            "git push --force-with-lease origin HEAD",   # 履歴から戻せるので対象外
            "git push origin :old-branch",
            "git branch -D old",
        ], expected=None)

    def test_gh_non_destructive(self):
        self.check_table([
            "gh pr view 12",
            "gh auth status",
            "gh secret set GH_TOKEN",
            "gh secret list",
            "gh variable list",
            "gh release create v1 --notes x",
            "gh release view v1",
            "gh repo view o/r",
            "gh repo clone o/r",
            "gh api repos/o/r",
            "gh api repos/o/r/actions/secrets",
            "gh api -X PATCH repos/o/r/issues/1 -f state=closed",
            "gh api -X PUT repos/o/r/issues/1/labels",
            "gh cache delete --all",          # キャッシュは再生成できる
        ], expected=None)

    def test_cloud_non_destructive(self):
        self.check_table([
            "gcloud auth list",
            "gcloud config set project x",
            "gcloud compute instances list",
            "gcloud storage ls",
            "aws sts get-caller-identity",
            "aws s3 ls s3://bucket",
            "aws s3 cp x s3://bucket/",
            "aws s3 sync ./dist s3://bucket",
            "terraform plan",
            "terraform plan -destroy",          # 計画の表示だけ
            "terraform apply",
            "terraform apply -auto-approve",
        ], expected=None)

    def test_http_without_destructive_intent(self):
        self.check_table([
            "curl -s https://api.github.com/repos/o/r",
            "curl -X POST https://api.github.com/repos/o/r/issues -f title=x",
            "curl -X DELETE https://example.com/api/session",   # クラウド API ではない
            "wget https://example.com/x.tar.gz",
        ], expected=None)

    def test_credential_tools_non_destructive(self):
        self.check_table([
            "security find-generic-password -a HappyOnigiri -s gh-token -w",
            "security add-generic-password -a x -s y -w z",
            "op item get 'GitHub PAT'",
            "op read op://vault/item/field",
            "gpg --list-secret-keys",
            "gpg --delete-key ABCD",           # 公開鍵は再取得できる
            "npm token list",
            "twine check dist/*",
            "gem list",
            "diskutil list",
            "diskutil info /",
            "tmutil status",
            "tmutil listbackups",
        ], expected=None)

    def test_database_read_only(self):
        self.check_table([
            "psql -c 'select * from truncate_log'",   # truncate が語の一部
            "psql -f schema.sql",
            "mysql -e 'select 1'",
            "sqlite3 db.sqlite '.tables'",
            "grep -rn 'DROP TABLE' db/migrate",       # DB クライアントではない
        ], expected=None)

    def test_secret_files_non_destructive(self):
        self.check_table([
            "cat .envrc",
            "source .envrc",
            "grep GH_TOKEN .envrc",
            "direnv allow .envrc",
            "cat .env > /tmp/backup",       # 読む側であって上書き先ではない
            "echo x >> .env",               # 追記は既存の値を壊さない
            "diff .env .env.example",
            "chmod 600 ~/.ssh/id_ed25519",
            "ssh-add ~/.ssh/id_ed25519",
            "ls -la ~/.ssh",
            "cat ~/.ssh/config",
            "vim ~/.ssh/config",
            "ssh-keygen -R example.com",
            "rm ~/.ssh/known_hosts",        # 再生成できる
            "rm ~/.ssh/id_ed25519.pub",     # 秘密鍵から作り直せる
        ], expected=None)

    def test_env_templates_are_not_secrets(self):
        self.check_table([
            "rm .env.example",
            "rm .env.sample",
            "rm .env.template",
            "rm .env.dist",
            "cat .env.example",
            "cp .env.example .env.example.bak",
            "mv .env.example .env",         # 作る側なので通す
            "git add .env.example",
            "rm -rf node_modules && cp .env.example .env",
            "rm src/environment.ts",        # env が語の一部
        ], expected=None)

    def test_guard_files_edit_and_cache(self):
        """ガードは削除・移動だけ塞ぐ。編集・閲覧・キャッシュ掃除は通す。"""
        self.check_table([
            "cat ~/.claude/settings.json",
            "grep -n hooks ~/.claude/settings.json",
            "ls ~/.claude/hooks",
            "ls ~/.codex/hooks",
            "vim ~/.claude/hooks/pr-merge-guard.py",
            "python3 ~/.claude/hooks/tests/test_pr_merge_guard.py",
            "cp -p ~/.claude/hooks/irreversible-guard.py /tmp/x.py",
            "rm -rf ~/.claude/hooks/tests/__pycache__",
            "rm -rf ~/.claude/hooks/.ruff_cache",
            "rm -rf ~/.claude/hooks/tests/.pytest_cache",
            "rm -rf files/agent-config/claude/hooks/tests/__pycache__",
            "git -C ~/dotfiles status",
        ], expected=None)

    def test_quoted_and_search_forms(self):
        """クォート内・grep の対象文字列は境界に含めない。"""
        self.check_table([
            "echo 'npm publish'",
            "echo 'rm .envrc'",
            "grep -rn 'credentials/revoke' logs/",
            "rg 'gh auth logout' docs/",
            "grep -rn 'gh secret delete' docs/",
            "grep -rn 'terraform destroy' notes.md",
        ], expected=None)

    def test_secondary_gate_limits_scope(self):
        """二次ゲートは「動詞語 OR 秘密/ガードパス片」でしか先へ進めない。

        一次ゲートを通す語 (revoke など) を含んでも、動詞語も秘密パスも無ければ抜ける。
        """
        self.check_table([
            "grep -rn revoke .",
            "echo revoke",
            "rg 'credentials/revoke'",
        ], expected=None)


# --- L1 契約テスト: Edit / Write / apply_patch ------------------------------

def payload(tool_name, tool_input, cwd="/tmp"):
    return json.dumps({
        "session_id": "test-session",
        "hook_event_name": "PreToolUse",
        "tool_name": tool_name,
        "tool_input": tool_input,
        "cwd": cwd,
    })


class FileToolPayloadTest(unittest.TestCase):
    """Bash 以外の payload。秘密ファイルは Edit / Write / apply_patch からも守る。"""

    def decide(self, tool_name, tool_input):
        return run_hook(SCRIPT, None, raw=payload(tool_name, tool_input))

    def assert_deny(self, tool_name, tool_input, label):
        decision, reason = self.decide(tool_name, tool_input)
        self.assertEqual(decision, "deny", f"{tool_name} {tool_input} / 理由: {reason!r}")
        self.assertIn(label, reason)

    def assert_pass(self, tool_name, tool_input):
        decision, reason = self.decide(tool_name, tool_input)
        self.assertIsNone(decision, f"{tool_name} {tool_input} / 理由: {reason!r}")

    def test_edit_secret_file(self):
        for path in ("/Users/alice/dev/app/.envrc", "/Users/alice/dev/app/.env",
                     "/Users/alice/dev/app/.env.production", "/Users/alice/.config/dotfiles/envrc.local",
                     "/Users/alice/dev/app/certs/server.pem", "/Users/alice/.ssh/id_ed25519"):
            with self.subTest(path=path):
                self.assert_deny("Edit", {"file_path": path}, L_SECRET_EDIT)

    def test_edit_non_secret_file(self):
        for path in ("/x/.env.example", "/x/.env.sample", "/x/.env.template", "/x/.env.dist",
                     "/x/src/environment.ts", "/Users/alice/.ssh/config",
                     "/Users/alice/.ssh/known_hosts", "/Users/alice/.ssh/id_ed25519.pub",
                     "/Users/alice/.claude/settings.json"):
            with self.subTest(path=path):
                self.assert_pass("Edit", {"file_path": path})

    def test_multi_edit_and_notebook_paths(self):
        self.assert_deny("MultiEdit", {"file_path": "/x/.env"}, L_SECRET_EDIT)
        self.assert_deny("NotebookEdit", {"notebook_path": "/x/.envrc"}, L_SECRET_EDIT)

    def test_write_to_missing_secret_path(self):
        """存在しない秘密ファイルでも Write による新規作成を塞ぐ。"""
        with tempfile.TemporaryDirectory() as tmp:
            self.assert_deny(
                "Write", {"file_path": os.path.join(tmp, "nope", ".env")}, L_SECRET_EDIT)
            self.assert_deny(
                "Write", {"file_path": os.path.join(tmp, ".envrc")}, L_SECRET_EDIT)

    def test_write_over_existing_secret_file(self):
        """既存の秘密ファイルへの Write は上書き = 復元不能。"""
        with tempfile.TemporaryDirectory() as tmp:
            for name in (".env", ".envrc"):
                path = os.path.join(tmp, name)
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write("FOO=1\n")
                with self.subTest(name=name):
                    self.assert_deny("Write", {"file_path": path}, L_SECRET_EDIT)

    def test_write_over_existing_template_is_allowed(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, ".env.example")
            with open(path, "w", encoding="utf-8") as fh:
                fh.write("FOO=\n")
            self.assert_pass("Write", {"file_path": path})

    def test_edit_missing_secret_file_still_denied(self):
        """Edit は対象の存在にかかわらず秘密ファイルなら塞ぐ。"""
        with tempfile.TemporaryDirectory() as tmp:
            self.assert_deny("Edit", {"file_path": os.path.join(tmp, ".envrc")}, L_SECRET_EDIT)

    def test_apply_patch_update_and_delete(self):
        for header in ("*** Update File: .envrc", "*** Delete File: .envrc",
                       "*** Update File: config/.env.production",
                       "*** Delete File: config/production.pem"):
            patch = f"*** Begin Patch\n{header}\n@@\n-a\n+b\n*** End Patch"
            with self.subTest(header=header):
                self.assert_deny("apply_patch", {"input": patch}, L_SECRET_PATCH)

    def test_apply_patch_add_file(self):
        for header in ("*** Add File: .env", "*** Add File: .envrc",
                       "*** Add File: config/secret.pem",
                       "*** Add File: /Users/alice/no-such-secret-hook-test.pem"):
            patch = f"*** Begin Patch\n{header}\n+FOO=1\n*** End Patch"
            with self.subTest(header=header):
                self.assert_deny("apply_patch", {"input": patch}, L_SECRET_PATCH)

    def test_apply_patch_non_secret(self):
        for header in ("*** Update File: .env.example", "*** Update File: src/app.ts",
                       "*** Delete File: .env.sample"):
            patch = f"*** Begin Patch\n{header}\n@@\n-a\n+b\n*** End Patch"
            with self.subTest(header=header):
                self.assert_pass("apply_patch", {"input": patch})

    def test_read_tool_is_not_blocked(self):
        self.assert_pass("Read", {"file_path": "/Users/alice/dev/app/.envrc"})
        self.assert_pass("Grep", {"pattern": "TOKEN", "path": "/Users/alice/dev/app/.envrc"})


@unittest.skipIf(TARGET == "hhx", "WT_AGENT_WORKTREE_POLICY による切り替えは hhx に入れない "
                                   "(秘密ファイルを Write でも塞ぐ観点は FileToolPayloadTest にある)")
class OnDemandFileToolPassThroughTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="main-file-guard-")
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.repo = make_repo(Path(self.tmp) / "repo", dirty=False)

    def decide(self, tool_name, tool_input, cwd=None, policy="on-demand"):
        env = {} if policy is None else {"WT_AGENT_WORKTREE_POLICY": policy}
        return run_hook(
            SCRIPT,
            None,
            raw=payload(tool_name, tool_input, cwd or self.repo),
            env=env,
        )

    def test_edit_write_and_apply_patch_do_not_enforce_main_read_only_guidance(self):
        for tool_name, path in (
            ("Edit", "tracked.txt"),
            ("Write", "new.txt"),
            ("Write", os.path.join(self.repo, "new-absolute.txt")),
        ):
            with self.subTest(tool_name=tool_name, path=path):
                self.assertEqual(
                    (None, ""), self.decide(tool_name, {"file_path": path})
                )
        patch = "*** Begin Patch\n*** Update File: tracked.txt\n@@\n-a\n+b\n*** End Patch"
        self.assertEqual(
            (None, ""),
            self.decide("apply_patch", {"input": patch}),
        )

    def test_secret_file_guard_still_applies_on_demand(self):
        decision, reason = self.decide("Write", {"file_path": ".env"})
        self.assertEqual("deny", decision)
        self.assertIn("秘密ファイル", reason)


# --- L2 単体テスト -----------------------------------------------------------

class NormalizeTest(unittest.TestCase):
    """L2: 表記ゆれの正規化。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def test_method_flag_forms(self):
        cases = {
            "gh api --method=DELETE x": "gh api -X DELETE x",
            "gh api --method DELETE x": "gh api -X DELETE x",
            "gh api --method   DELETE x": "gh api -X DELETE x",
            "curl -XDELETE x": "curl -X DELETE x",
            "curl -XPOST -XDELETE x": "curl -X POST -X DELETE x",
            "curl -X DELETE x": "curl -X DELETE x",
            "curl -Xfoo x": "curl -X foo x",
        }
        for src, want in cases.items():
            with self.subTest(src=src):
                self.assertEqual(self.mod.normalize(src), want)

    def test_newline_becomes_space(self):
        self.assertEqual(self.mod.normalize("a\nb"), "a b")
        self.assertEqual(self.mod.normalize("echo x\nrm .envrc"), "echo x rm .envrc")

    def test_similar_flags_untouched(self):
        for src in ("gh api --methodology=DELETE x", "cmd --methods DELETE", "cmd -x DELETE",
                    "rm .envrc"):
            with self.subTest(src=src):
                self.assertEqual(self.mod.normalize(src), src)

    def test_idempotent(self):
        for src in ("gh api --method=DELETE x", "curl -XDELETE x", "rm .envrc"):
            with self.subTest(src=src):
                once = self.mod.normalize(src)
                self.assertEqual(self.mod.normalize(once), once)


class SecretPathLabelTest(unittest.TestCase):
    """L2: Edit / Write / apply_patch のパス判定。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def test_secret_paths(self):
        cases = {
            "/x/.envrc": ".envrc",
            "/x/.env": ".env",
            "/x/.env/": ".env",
            "/x/.env.local": ".env.local",
            "/x/.env.production": ".env.production",
            "/x/envrc.local": "envrc.local",
            "/Users/alice/.config/dotfiles/envrc.local": "envrc.local",
            "certs/server.pem": "server.pem",
            "~/.ssh/id_ed25519": ".ssh/id_ed25519",
            "/Users/alice/.ssh/id_rsa": ".ssh/id_rsa",
        }
        for path, want in cases.items():
            with self.subTest(path=path):
                self.assertEqual(self.mod.secret_path_label(path), want)

    def test_non_secret_paths(self):
        for path in ("/x/.env.example", "/x/.env.sample", "/x/.env.template", "/x/.env.dist",
                     "/x/.environment", "src/env.ts", "/x/README.md",
                     "~/.ssh/id_ed25519.pub", "~/.ssh/known_hosts", "~/.ssh/known_hosts.old",
                     "~/.ssh/config", "/x/.ssh", "/x/.claude/settings.json", ""):
            with self.subTest(path=path):
                self.assertIsNone(self.mod.secret_path_label(path))

    def test_non_string_inputs(self):
        for value in (None, 123, [".env"], {"file_path": ".env"}):
            with self.subTest(value=value):
                self.assertIsNone(self.mod.secret_path_label(value))


class SecretTokensTest(unittest.TestCase):
    """L2: Bash セグメントからの秘密ファイル抽出。"""

    @classmethod
    def setUpClass(cls):
        cls.mod = load_hook(SCRIPT)

    def test_tokens_found(self):
        cases = {
            " rm .envrc": [".envrc"],
            "rm .env": [".env"],
            "cat .env.local": [".env.local"],
            "mv envrc.local /tmp": ["envrc.local"],
            "rm certs/server.pem": ["certs/server.pem"],
            "rm ~/.ssh/id_ed25519": ["~/.ssh/id_ed25519"],
            "rm -rf ~/.ssh": ["~/.ssh"],
            "cat /Users/alice/.config/dotfiles/envrc.local":
                ["/Users/alice/.config/dotfiles/envrc.local"],
            "rm '.envrc'": [".envrc"],
            "FOO=.env ls": [".env"],
            "echo x > .env": [".env"],
            "echo x >.env": [".env"],      # 空白なしのリダイレクトも拾う
        }
        for seg, want in cases.items():
            with self.subTest(seg=seg):
                self.assertEqual(self.mod.secret_tokens(seg), want)

    def test_tokens_not_found(self):
        for seg in ("cat .env.example", "rm .env.sample", "ls .environment", "npm run env",
                    "rm ~/.ssh/known_hosts", "rm ~/.ssh/id_ed25519.pub", "echo hi",
                    "cat ~/.claude/settings.json"):
            with self.subTest(seg=seg):
                self.assertEqual(self.mod.secret_tokens(seg), [])


# --- フックとしての入出力契約 -------------------------------------------------

class InterfaceTest(unittest.TestCase):
    """フックとしての入出力契約。"""

    def test_exit_code_is_zero_even_when_denying(self):
        # run_hook は 0 以外の終了で AssertionError を投げるので、呼べること自体が確認になる
        decision, _ = run_hook(SCRIPT, "gh auth logout")
        self.assertEqual(decision, "deny")

    def test_output_schema(self):
        raw = payload("Bash", {"command": "gh auth logout"})
        proc = subprocess.run(hook_command(SCRIPT), input=raw, capture_output=True,
                              text=True, timeout=60)
        data = json.loads(proc.stdout)
        self.assertEqual(list(data.keys()), ["hookSpecificOutput"])
        out = data["hookSpecificOutput"]
        self.assertEqual(out["hookEventName"], "PreToolUse")
        self.assertEqual(out["permissionDecision"], "deny")
        self.assertIn("❌ ブロック:", out["permissionDecisionReason"])
        self.assertIn("理由:", out["permissionDecisionReason"])
        self.assertIn("対応:", out["permissionDecisionReason"])

    def test_never_uses_ask(self):
        """Codex CLI は ask を解釈しないので、判定は deny だけに揃える。"""
        for command in ("gh auth logout", "npm publish", "rm .envrc", "trash ~/.codex/config.toml"):
            with self.subTest(command=command):
                self.assertEqual(run_hook(SCRIPT, command)[0], "deny")

    def test_argv_debug_path(self):
        self.assertEqual(run_hook_argv(SCRIPT, "gh auth logout")[0], "deny")
        self.assertEqual(run_hook_argv(SCRIPT, "rm .envrc")[0], "deny")
        self.assertEqual(run_hook_argv(SCRIPT, "git status")[0], None)
        self.assertEqual(run_hook_argv(SCRIPT, "cat .envrc")[0], None)

    def test_odd_inputs_do_not_crash(self):
        for raw in ("", "   ", "not json at all", "[]", "null", "0", '"just a string"',
                    '{"tool_input": {"command": null}}',
                    '{"tool_input": {"command": 123}}',
                    '{"tool_input": "rm .envrc"}',
                    '{"tool_name": "Edit", "tool_input": null}',
                    '{"tool_name": "Write", "tool_input": {"file_path": null}}',
                    '{"tool_name": "Write", "tool_input": {"file_path": [".env"]}}',
                    '{"tool_name": "apply_patch", "tool_input": {"input": null}}',
                    '{"tool_name": "apply_patch", "tool_input": {"input": ".envrc"}}',
                    '{"cwd": "/tmp"}'):
            with self.subTest(raw=raw):
                run_hook(SCRIPT, None, raw=raw)  # 例外なく終了すれば合格

    def test_broken_json_is_not_fail_open(self):
        """パースできない入力は生のまま判定に回す (取り逃がしより過検出を選ぶ)。"""
        self.assertEqual(run_hook(SCRIPT, None, raw="gh auth logout")[0], "deny")
        self.assertEqual(run_hook(SCRIPT, None, raw="rm .envrc")[0], "deny")

    def test_missing_tool_name_defaults_to_bash(self):
        raw = json.dumps({"tool_input": {"command": "gh auth logout"}})
        self.assertEqual(run_hook(SCRIPT, None, raw=raw)[0], "deny")

    def test_long_command_is_handled(self):
        padding = "x" * 20000
        self.assertEqual(run_hook(SCRIPT, f"echo {padding}")[0], None)
        self.assertEqual(run_hook(SCRIPT, f"echo {padding} && gh auth logout")[0], "deny")

    def test_pathological_input_is_fast(self):
        """病的に長い入力でも正規表現が破綻しない。"""
        cases = [
            "gh api " + "x" * 20000 + " repos/o/r",
            "curl " + "-XDELETE " * 2000 + "https://example.com/x",
            "gh api " + "--method=DELETE " * 2000 + "repos/o/r",
            "gh" + " " * 5000 + "pr view 1",
            "echo " + ".env.example " * 2000,          # 秘密ファイルの惜しい形の連打
            "cat .env.example " + "a/" * 5000 + "x",   # パス片の連打
            "ls " + "a/" * 3000 + " ~/.ssh/id_rsa",
        ]
        start = time.perf_counter()
        for command in cases:
            run_hook(SCRIPT, command)
        self.assertLess(time.perf_counter() - start, 10.0)

    def test_other_tool_payload(self):
        """Bash 以外の payload が来ても落ちない (matcher の設定漏れへの保険)。"""
        for tool_name, tool_input in (("Read", {"file_path": "/tmp/x"}),
                                      ("Glob", {"pattern": "**/*.py"}),
                                      ("WebFetch", {"url": "https://example.com"}),
                                      ("Task", {"prompt": "rm .envrc してください"})):
            with self.subTest(tool_name=tool_name):
                self.assertEqual(run_hook(SCRIPT, None, raw=payload(tool_name, tool_input)),
                                 (None, ""))


if __name__ == "__main__":
    unittest.main()
