#!/usr/bin/env python3
"""exit-plan-subagent-guard.py のテスト。

判定材料は transcript (JSONL) だけなので、実物で確認した行の形をそのまま組み立てて渡す。
確認した形は 4 つ。

  起動の要求   assistant の tool_use ブロック (name=Agent, input.description)
  起動の結果   user の tool_result ブロック。content は **文字列とブロック列の両方がありうる**
  終了通知     queue-operation 行として先に出て、続けて user メッセージにも入る
  再開         SendMessage の tool_result。停止済みのエージェントが起動 marker 無しで走り出す

起動は tool_use と tool_result の対でだけ成立するので、**起動のテストは必ず対で書く**
(理由は本体の docstring)。

実行: make compat-test（compat/README.md を参照）
"""
import json
import os
import shutil
import tempfile
import unittest

from helpers import run_hook

SCRIPT = "exit-plan-subagent-guard.py"

LAUNCH_TEXT = (
    "Async agent launched successfully. (This tool result is internal metadata.)\n"
    "agentId: {agent} (internal ID - do not mention to user.)\n"
    "The agent is working in the background."
)

# 現行の実物。文面が伸びても拾えることを見るために丸ごと置く
REAL_LAUNCH_TEXT = (
    "Async agent launched successfully. (This tool result is internal metadata — never quote"
    " or paste any part of it, including the agentId below, into a user-facing reply.)\n"
    "agentId: {agent} (internal ID - do not mention to user. Use SendMessage with to:"
    " '{agent}', summary: '<5-10 word recap>' to continue this agent.)\n"
    "The agent is working in the background. You will be notified automatically when it"
    " completes.\n"
    "output_file: /private/tmp/claude-501/-Users-alice-dev-X/sess/tasks/{agent}.output\n"
)

# SendMessage で停止済みのエージェントを再開したときの実物
RESUME_TEXT = (
    '{{"success":true,"message":"Agent \\"{agent}\\" had no active task; resumed from'
    ' transcript in the background with your message. You\'ll be notified when it finishes."}}'
)


def tool_use_line(tool_use_id, description, name="Agent", sidechain=False):
    return json.dumps({
        "type": "assistant",
        "isSidechain": sidechain,
        "message": {"role": "assistant", "content": [{
            "type": "tool_use",
            "id": tool_use_id,
            "name": name,
            "input": {"description": description, "subagent_type": "Explore", "prompt": "..."},
        }]},
    })


def launch_line(tool_use_id, agent, sidechain=False, flat=False):
    """起動の tool_result。flat=True で content が文字列の形になる。"""
    text = LAUNCH_TEXT.format(agent=agent)
    return json.dumps({
        "type": "user",
        "isSidechain": sidechain,
        "message": {"role": "user", "content": [{
            "tool_use_id": tool_use_id,
            "type": "tool_result",
            "content": text if flat else [{"type": "text", "text": text}],
            "is_error": False,
        }]},
    })


def launch_pair(tool_use_id, agent, description="調査", **kwargs):
    """起動の最小形。tool_use と tool_result の対でだけ起動として数える。"""
    sidechain = kwargs.get("sidechain", False)
    return [tool_use_line(tool_use_id, description, sidechain=sidechain),
            launch_line(tool_use_id, agent, **kwargs)]


def send_message_line(tool_use_id, agent, sidechain=False):
    """SendMessage の tool_use。再開の tool_result はこれに紐づく。"""
    return json.dumps({
        "type": "assistant",
        "isSidechain": sidechain,
        "message": {"role": "assistant", "content": [{
            "type": "tool_use",
            "id": tool_use_id,
            "name": "SendMessage",
            "input": {"to": agent, "message": "続きをお願い"},
        }]},
    })


def result_line(tool_use_id, text, sidechain=False):
    """任意の tool_result 行。再開など launch 以外の本文を流し込む。"""
    return json.dumps({
        "type": "user",
        "isSidechain": sidechain,
        "message": {"role": "user", "content": [{
            "tool_use_id": tool_use_id,
            "type": "tool_result",
            "content": [{"type": "text", "text": text}],
            "is_error": False,
        }]},
    })


def notification_line(agent, status="completed", summary="done"):
    return json.dumps({
        "type": "user",
        "isSidechain": False,
        "message": {"role": "user", "content": (
            f"<task-notification>\n<task-id>{agent}</task-id>\n"
            f"<status>{status}</status>\n<summary>{summary}</summary>\n</task-notification>"
        )},
    })


def queue_notification_line(agent, status="completed", operation="enqueue"):
    """実物の通知はまず queue-operation として記録される。message を持たない行。"""
    return json.dumps({
        "type": "queue-operation",
        "operation": operation,
        "sessionId": "test-session",
        "content": (
            f"<task-notification>\n<task-id>{agent}</task-id>\n"
            f"<status>{status}</status>\n<summary>done</summary>\n</task-notification>"
        ),
    })


def bash_result_line(text):
    """Bash の出力。agentId の文字列が混ざっても起動として拾ってはいけない。"""
    return json.dumps({
        "type": "user",
        "isSidechain": False,
        "message": {"role": "user", "content": [{
            "tool_use_id": "toolu_bash",
            "type": "tool_result",
            "content": text,
            "is_error": False,
        }]},
    })


class GuardTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="exit-plan-guard-")
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)

    def decide(self, lines, transcript_path=None):
        if transcript_path is None:
            transcript_path = os.path.join(self.dir, "transcript.jsonl")
            with open(transcript_path, "w", encoding="utf-8") as handle:
                handle.write("".join(line + "\n" for line in lines))
        raw = json.dumps({
            "session_id": "test-session",
            "transcript_path": transcript_path,
            "hook_event_name": "PreToolUse",
            "tool_name": "ExitPlanMode",
            "tool_input": {},
            "cwd": self.dir,
        })
        return run_hook(SCRIPT, None, raw=raw)


class BlockedTest(GuardTest):
    """結果が返っていないエージェントが残っていれば止める。"""

    def test_launched_without_notification(self):
        decision, reason = self.decide([
            tool_use_line("toolu_1", "Design the sharding"),
            launch_line("toolu_1", "a0b7129b0d13559d4"),
        ])
        self.assertEqual(decision, "deny", reason)
        self.assertIn("a0b7129b0d13559d4", reason)
        self.assertIn("Design the sharding", reason)

    def test_flat_tool_result_content(self):
        """content が文字列で来る形でも拾う。"""
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222", flat=True))
        self.assertEqual(decision, "deny", reason)
        self.assertIn("aaa111bbb222", reason)

    def test_only_the_pending_agent_is_listed(self):
        decision, reason = self.decide([
            tool_use_line("toolu_1", "Explore the tools"),
            launch_line("toolu_1", "aaa111bbb222"),
            tool_use_line("toolu_2", "Design the sharding"),
            launch_line("toolu_2", "ccc333ddd444"),
            notification_line("aaa111bbb222"),
        ])
        self.assertEqual(decision, "deny", reason)
        self.assertIn("ccc333ddd444", reason)
        self.assertNotIn("aaa111bbb222", reason)

    def test_non_terminal_status_is_not_a_finish(self):
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222") + [
            notification_line("aaa111bbb222", status="running"),
        ])
        self.assertEqual(decision, "deny", reason)

    def test_broken_line_does_not_hide_a_pending_agent(self):
        decision, reason = self.decide(
            ["{ not json"] + launch_pair("toolu_1", "aaa111bbb222"))
        self.assertEqual(decision, "deny", reason)

    def test_description_is_optional(self):
        """description が無い起動でも id だけで止める。"""
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222", description=""))
        self.assertEqual(decision, "deny", reason)
        self.assertIn("aaa111bbb222", reason)

    def test_real_launch_text(self):
        """実物の起動文面 (文面は伸び続ける) でも拾う。"""
        decision, reason = self.decide([
            tool_use_line("toolu_1", "設計の穴を洗う"),
            result_line("toolu_1", REAL_LAUNCH_TEXT.format(agent="a0a21ead91274b7c3")),
        ])
        self.assertEqual(decision, "deny", reason)
        self.assertIn("a0a21ead91274b7c3", reason)
        self.assertIn("設計の穴を洗う", reason)

    def test_old_task_tool_name_keeps_the_description(self):
        """旧 Task 名で記録された起動でも、理由文に説明が出る。"""
        decision, reason = self.decide([
            tool_use_line("toolu_1", "Explore the tools", name="Task"),
            launch_line("toolu_1", "aaa111bbb222"),
        ])
        self.assertEqual(decision, "deny", reason)
        self.assertIn("Explore the tools", reason)

    def test_resume_via_send_message_restarts_the_wait(self):
        """完了後に SendMessage で再開したエージェントは、また待つ対象に戻る。"""
        decision, reason = self.decide([
            tool_use_line("toolu_1", "Design the sharding"),
            launch_line("toolu_1", "aaa111bbb222"),
            notification_line("aaa111bbb222"),
            send_message_line("toolu_2", "aaa111bbb222"),
            result_line("toolu_2", RESUME_TEXT.format(agent="aaa111bbb222")),
        ])
        self.assertEqual(decision, "deny", reason)
        self.assertIn("aaa111bbb222", reason)


class AllowedTest(GuardTest):
    """誤爆がこのフックの実害なので、通過ケースを厚く見る。"""

    def test_no_agents(self):
        decision, reason = self.decide([bash_result_line("ok")])
        self.assertIsNone(decision, reason)

    def test_launch_text_read_from_a_file_is_not_a_launch(self):
        """transcript やフック自体を読んだ出力に起動の文面が載っても起動ではない。

        実際にこれで実在しないエージェントが登録され、ExitPlanMode が恒久的に deny された。
        """
        decision, reason = self.decide([
            tool_use_line("toolu_2", "transcript を読む", name="Bash"),
            bash_result_line(REAL_LAUNCH_TEXT.format(agent="ccc333ddd444")),
        ])
        self.assertIsNone(decision, reason)

    def test_resume_text_read_from_a_file_is_not_a_resume(self):
        """再開の文面も、SendMessage の結果でなければ待つ対象に戻さない。"""
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222") + [
            notification_line("aaa111bbb222"),
            bash_result_line(RESUME_TEXT.format(agent="aaa111bbb222")),
        ])
        self.assertIsNone(decision, reason)

    def test_completed_notification(self):
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222") + [
            notification_line("aaa111bbb222"),
        ])
        self.assertIsNone(decision, reason)

    def test_stopped_notification_is_the_escape_hatch(self):
        """resume 時の合成通知 (stopped) は待つ対象ではない。"""
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222") + [
            notification_line("aaa111bbb222", status="stopped"),
        ])
        self.assertIsNone(decision, reason)

    def test_killed_notification_is_the_escape_hatch(self):
        """TaskStop で止めた実際の状態は killed。理由文が案内する逃げ道そのものなので通す。"""
        decision, reason = self.decide([
            tool_use_line("toolu_1", "PR差分を機能整理"),
            launch_line("toolu_1", "aaa111bbb222"),
            queue_notification_line("aaa111bbb222", status="killed"),
            notification_line("aaa111bbb222", status="killed",
                              summary='Agent "PR差分を機能整理" was stopped by Claude'),
        ])
        self.assertIsNone(decision, reason)

    def test_failed_notification(self):
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222") + [
            notification_line("aaa111bbb222", status="failed"),
        ])
        self.assertIsNone(decision, reason)

    def test_queue_operation_notification_only(self):
        """通知は queue-operation 行として先に記録される。message を持たない形でも終了。"""
        decision, reason = self.decide(launch_pair("toolu_1", "aaa111bbb222") + [
            queue_notification_line("aaa111bbb222"),
        ])
        self.assertIsNone(decision, reason)

    def test_notification_before_launch(self):
        """resume 直後は前セッション分の通知が先に並ぶ。順序で取りこぼさない。"""
        decision, reason = self.decide(
            [notification_line("aaa111bbb222")] + launch_pair("toolu_1", "aaa111bbb222"))
        self.assertIsNone(decision, reason)

    def test_sidechain_launch_is_ignored(self):
        """subagent が起動した孫は親の待つ対象ではない。"""
        decision, reason = self.decide([
            tool_use_line("toolu_1", "Explore", sidechain=True),
            launch_line("toolu_1", "aaa111bbb222", sidechain=True),
        ])
        self.assertIsNone(decision, reason)

    def test_agent_id_in_command_output_is_not_a_launch(self):
        """起動 marker の無いテキストは、agentId の文字列があっても起動ではない。"""
        decision, reason = self.decide([
            bash_result_line("agentId: aaa111bbb222\nagentId: ccc333ddd444"),
        ])
        self.assertIsNone(decision, reason)

    def test_unreadable_transcript_fails_open(self):
        decision, reason = self.decide(
            [], transcript_path=os.path.join(self.dir, "missing.jsonl"))
        self.assertIsNone(decision, reason)

    def test_other_tools_are_untouched(self):
        """matcher を取り違えて登録されても、payload に ExitPlanMode の語が無ければ一次ゲートで抜ける。

        逆に、語を含む payload (このフックを扱う Bash など) なら tool_name に関わらず判定する。
        """
        transcript = os.path.join(self.dir, "transcript.jsonl")
        with open(transcript, "w", encoding="utf-8") as handle:
            handle.write("".join(l + "\n" for l in launch_pair("toolu_1", "aaa111bbb222")))
        raw = json.dumps({
            "session_id": "test-session",
            "transcript_path": transcript,
            "hook_event_name": "PreToolUse",
            "tool_name": "Bash",
            "tool_input": {"command": "ls"},
            "cwd": self.dir,
        })
        decision, reason = run_hook(SCRIPT, None, raw=raw)
        self.assertIsNone(decision, reason)


if __name__ == "__main__":
    unittest.main(verbosity=2)
