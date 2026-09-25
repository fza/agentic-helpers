#!/usr/bin/env python3
"""Exercise carryforward.py against a temporary repository root."""
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("carryforward.py")


def own_process_name() -> str:
    result = subprocess.run(
        ["ps", "-p", str(os.getpid()), "-o", "comm="],
        capture_output=True, text=True, check=False,
    )
    return Path(result.stdout.strip()).name


OWNER_NAME = own_process_name()


def run(command: list[str], root: Path, stdin: str = "",
        window: int | None = None) -> subprocess.CompletedProcess:
    environment = dict(
        os.environ, CLAUDE_PROJECT_DIR=str(root), CARRYFORWARD_PROCESS_NAME=OWNER_NAME
    )
    environment.pop("CLAUDE_CODE_AUTO_COMPACT_WINDOW", None)
    if window:
        environment["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = str(window)
    return subprocess.run(
        [sys.executable, str(SCRIPT), *command],
        input=stdin, capture_output=True, text=True, env=environment, check=False,
    )


def transcript_line(kind: str, blocks: list[dict]) -> str:
    return json.dumps({"type": kind, "message": {"content": blocks}}) + "\n"


class CarryForwardTest(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        (self.root / ".memory" / "transcripts").mkdir(parents=True)
        self.session = "test-session-0001"
        self.transcript = self.root / "transcript.jsonl"
        self.transcript.write_text("")

    def tearDown(self) -> None:
        self.directory.cleanup()

    def payload(self, **extra) -> str:
        base = {"session_id": self.session, "transcript_path": str(self.transcript)}
        base.update(extra)
        return json.dumps(base)

    def lock(self, role: str = "drive") -> Path:
        return self.root / ".memory" / "roles" / f"{role}.json"

    def state(self) -> dict:
        return json.loads((self.root / ".memory" / "transcripts" / f"{self.session}.state").read_text())

    def log(self) -> Path:
        return self.root / ".memory" / "transcripts" / f"{self.session}.md"

    def set_sensor(self, percent: float, age_seconds: float = 0, size: int = 0) -> None:
        path = self.root / ".memory" / "transcripts" / f"{self.session}.ctx"
        reading = {"used_percentage": percent, "at": time.time() - age_seconds}
        if size:
            reading["size"] = size
        path.write_text(json.dumps(reading))

    def claim(self, role: str = "drive", session: str | None = None) -> None:
        self.assertEqual(
            run(["claim", role, session or self.session], self.root).returncode, 0
        )

    def append_turn(self, asked: str, replied: str, tool: str | None = None) -> None:
        text = transcript_line("user", [{"type": "text", "text": asked}])
        blocks: list[dict] = [{"type": "text", "text": replied}]
        if tool:
            blocks.append({"type": "tool_use", "name": tool, "input": {"file_path": "/x/y.go"}})
        text += transcript_line("assistant", blocks)
        with self.transcript.open("a") as handle:
            handle.write(text)

    def test_a_live_holder_is_named_and_a_free_seat_is_not(self) -> None:
        """`seat-up.sh` opens no second session on a seat somebody holds."""
        self.assertEqual(run(["holder", "drive"], self.root).returncode, 1)
        self.claim()
        held = run(["holder", "drive"], self.root)
        self.assertEqual(held.returncode, 0)
        self.assertIn("drive is held by session", held.stdout)
        record = json.loads(self.lock().read_text())
        record["pid"] = 999_999
        self.lock().write_text(json.dumps(record))
        self.assertEqual(run(["holder", "drive"], self.root).returncode, 1, "a dead holder frees the seat")

    def test_claim_writes_lock_and_refuses_second_live_claim(self) -> None:
        self.claim()
        record = json.loads(self.lock().read_text())
        self.assertEqual(record["session_id"], self.session)
        self.assertGreater(record["pid"], 0)
        self.assertTrue(record["started"])

        second = run(["claim", "drive", "other-session"], self.root)
        self.assertEqual(second.returncode, 1)
        self.assertIn("drive is held by", second.stderr)
        self.assertIn("take drive", second.stderr)
        self.assertEqual(json.loads(self.lock().read_text())["session_id"], self.session)

    def test_claim_seeds_offsets_so_the_first_stop_logs_only_new_turns(self) -> None:
        projects = Path.home() / ".claude" / "projects" / str(self.root).replace("/", "-")
        projects.mkdir(parents=True, exist_ok=True)
        harness = projects / f"{self.session}.jsonl"
        try:
            harness.write_text(transcript_line("user", [{"type": "text", "text": "old history"}]))
            existing = harness.stat().st_size
            self.claim()
            state = self.state()
            self.assertEqual(state["log_offset"], existing)
            self.assertEqual(state["transcript_offset_at_refresh"], existing)

            self.transcript.write_bytes(harness.read_bytes())
            self.append_turn("new ask", "new reply")
            run(["stop"], self.root, self.payload())
            body = self.log().read_text()
            self.assertIn("new ask", body)
            self.assertNotIn("old history", body)
        finally:
            harness.unlink(missing_ok=True)
            projects.rmdir()

    def test_a_seat_in_a_worktree_finds_its_own_harness_transcript(self) -> None:
        worktree_slug = f"{str(self.root).replace('/', '-')}--claude-worktrees-settle"
        projects = Path.home() / ".claude" / "projects" / worktree_slug
        projects.mkdir(parents=True, exist_ok=True)
        harness = projects / f"{self.session}.jsonl"
        harness.write_text("x" * 512)
        try:
            self.claim("settle")
            self.assertEqual(self.state()["log_offset"], 512)
        finally:
            harness.unlink()
            projects.rmdir()

    def test_the_owner_speaking_refills_the_seat_budget(self) -> None:
        """The seat gate's resting line promises it, so every prompt keeps the promise."""
        run(["session-start"], self.root, self.payload(source="startup"))
        run(["claim", "validate"], self.root)
        budget = self.root / ".memory" / "roles" / "validate.autopilot"
        budget.write_text("22")
        prompted = run(["prompt"], self.root, self.payload(prompt="carry on"))
        self.assertEqual(prompted.returncode, 0, prompted.stderr)
        self.assertFalse(budget.exists())

    def test_a_prompt_from_a_session_holding_no_seat_touches_no_budget(self) -> None:
        """The refill above only means something if it reaches the seat alone."""
        budget = self.root / ".memory" / "roles" / "validate.autopilot"
        budget.parent.mkdir(parents=True, exist_ok=True)
        budget.write_text("22")
        prompted = run(["prompt"], self.root, self.payload(prompt="carry on"))
        self.assertEqual(prompted.returncode, 0, prompted.stderr)
        self.assertTrue(budget.exists())

    def test_a_claim_needs_no_session_id_once_the_session_started(self) -> None:
        run(["session-start"], self.root, self.payload(source="startup"))
        claimed = run(["claim", "settle"], self.root)
        self.assertEqual(claimed.returncode, 0, claimed.stderr)
        self.assertEqual(
            json.loads(self.lock("settle").read_text())["session_id"], self.session
        )

    def test_a_claim_naming_another_session_is_refused(self) -> None:
        """An id read out of the sweep or another seat's files is not this session's."""
        run(["session-start"], self.root, self.payload(source="startup"))
        refused = run(["claim", "settle", "another-session-9999"], self.root)
        self.assertEqual(refused.returncode, 1)
        self.assertIn("not another", refused.stderr)
        self.assertFalse(self.lock("settle").exists())

    def test_a_short_form_of_its_own_id_is_the_same_session(self) -> None:
        run(["session-start"], self.root, self.payload(source="startup"))
        claimed = run(["claim", "settle", self.session[:8]], self.root)
        self.assertEqual(claimed.returncode, 0, claimed.stderr)
        self.assertEqual(
            json.loads(self.lock("settle").read_text())["session_id"], self.session
        )

    def test_a_claim_with_no_recorded_session_refuses_rather_than_guessing(self) -> None:
        refused = run(["claim", "settle"], self.root)
        self.assertEqual(refused.returncode, 1)
        self.assertIn("usage:", refused.stderr)
        self.assertFalse(self.lock("settle").exists())

    def test_claim_leaves_an_existing_state_alone(self) -> None:
        self.claim()
        state = self.state()
        state["turns"] = 7
        (self.root / ".memory" / "transcripts" / f"{self.session}.state").write_text(
            json.dumps(state)
        )
        run(["release", "drive"], self.root)
        self.claim()
        self.assertEqual(self.state()["turns"], 7)

    def test_everything_is_inert_without_a_memory_directory(self) -> None:
        bare = Path(tempfile.mkdtemp())
        try:
            for command in (["session-start"], ["prompt"], ["stop"]):
                result = run(command, bare, self.payload(source="startup"))
                self.assertEqual(result.returncode, 0, command)
                self.assertEqual(result.stdout, "", command)
            refused = run(["claim", "drive", "any-session"], bare)
            self.assertEqual(refused.returncode, 1)
            self.assertIn("has not opted in", refused.stderr)
            self.assertEqual(list(bare.iterdir()), [])
        finally:
            subprocess.run(["rm", "-rf", str(bare)], check=False)

    def test_a_refresh_disarms_the_percentage_trigger(self) -> None:
        self.claim()
        self.set_sensor(70)
        self.assertIn("carry-forward", run(["prompt"], self.root, self.payload()).stdout)
        run(["refreshed"], self.root)
        self.assertEqual(self.state()["percent_rung"], 75)
        self.set_sensor(72)
        self.assertEqual(run(["prompt"], self.root, self.payload()).stdout, "")
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 0)

    def test_the_trigger_rearms_once_context_climbs_further(self) -> None:
        self.claim()
        self.set_sensor(70)
        run(["prompt"], self.root, self.payload())
        run(["refreshed"], self.root)
        self.set_sensor(80)
        self.assertIn("carry-forward", run(["prompt"], self.root, self.payload()).stdout)

    def test_a_late_nudge_climbs_past_the_reading_it_answered(self) -> None:
        self.claim()
        self.set_sensor(70)
        run(["prompt"], self.root, self.payload())
        self.set_sensor(87)
        run(["refreshed"], self.root)
        self.assertEqual(self.state()["percent_rung"], 95)
        self.set_sensor(90)
        self.assertEqual(run(["prompt"], self.root, self.payload()).stdout, "")

    def test_a_compaction_resets_the_ladder_to_the_threshold(self) -> None:
        self.claim()
        self.set_sensor(87)
        run(["prompt"], self.root, self.payload())
        run(["refreshed"], self.root)
        self.assertEqual(self.state()["percent_rung"], 95)
        run(["session-start"], self.root, self.payload(source="compact"))
        self.assertIsNone(self.state()["percent_rung"])
        self.set_sensor(70)
        self.assertIn("carry-forward", run(["prompt"], self.root, self.payload()).stdout)

    def test_a_refresh_never_wedges_the_turn(self) -> None:
        self.claim()
        self.set_sensor(70)
        for _ in range(4):
            run(["prompt"], self.root, self.payload())
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 2)
        run(["refreshed"], self.root)
        for _ in range(4):
            run(["prompt"], self.root, self.payload())
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 0)

    def test_take_seizes_and_records_the_previous_holder(self) -> None:
        self.claim()
        seized = run(["take", "drive", "other-session"], self.root)
        self.assertEqual(seized.returncode, 0)
        record = json.loads(self.lock().read_text())
        self.assertEqual(record["session_id"], "other-session")
        self.assertEqual(record["seized_from"], self.session)

    def test_claim_succeeds_when_the_holder_is_dead(self) -> None:
        self.lock().parent.mkdir(parents=True, exist_ok=True)
        self.lock().write_text(json.dumps({
            "session_id": "dead-session", "pid": 999999, "started": "irrelevant",
        }))
        self.assertEqual(run(["claim", "drive", self.session], self.root).returncode, 0)
        self.assertEqual(json.loads(self.lock().read_text())["session_id"], self.session)

    def test_secondary_session_writes_nothing(self) -> None:
        result = run(["prompt"], self.root, self.payload())
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        stopped = run(["stop"], self.root, self.payload())
        self.assertEqual(stopped.returncode, 0)
        self.assertFalse(self.log().exists())
        self.assertFalse((self.root / ".memory" / "transcripts" / f"{self.session}.state").exists())

    def test_stop_appends_one_turn_block_per_call(self) -> None:
        self.claim()
        self.append_turn("first ask", "first reply", tool="Edit")
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 0)
        body = self.log().read_text()
        self.assertIn("## turn 1", body)
        self.assertIn("### asked", body)
        self.assertIn("first ask", body)
        self.assertIn("### replied", body)
        self.assertIn("first reply", body)
        self.assertIn("Edit /x/y.go", body)

        self.append_turn("second ask", "second reply")
        run(["stop"], self.root, self.payload())
        body = self.log().read_text()
        self.assertIn("## turn 2", body)
        self.assertEqual(body.count("## turn "), 2)
        self.assertEqual(self.state()["turns"], 2)

    def test_stop_with_no_new_records_adds_nothing(self) -> None:
        self.claim()
        self.append_turn("only ask", "only reply")
        run(["stop"], self.root, self.payload())
        first = self.log().read_text()
        run(["stop"], self.root, self.payload())
        self.assertEqual(self.log().read_text(), first)

    def test_sensor_above_threshold_nudges(self) -> None:
        self.claim()
        self.set_sensor(70)
        result = run(["prompt"], self.root, self.payload())
        self.assertIn("carry-forward", result.stdout)
        self.assertEqual(self.state()["nudges"], 1)

    def test_every_nudge_repeats_what_a_carry_forward_keeps(self) -> None:
        """A seat reads the nudge, not the rules file, so the rule rides in it."""
        self.claim()
        self.set_sensor(70)
        said = run(["prompt"], self.root, self.payload()).stdout
        self.assertIn("not yet on the board or in the graph", said)
        self.assertIn("leave it untouched where nothing does", said)
        import importlib.util
        spec = importlib.util.spec_from_file_location("carryforward_text", SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.assertIn("not yet on the board or in the graph", module.BACKSTOP_TEXT)

    def test_a_seat_off_the_board_keeps_its_own_work(self) -> None:
        """`main` and `idea` carry work nothing else rebuilds, so their nudge says fold."""
        self.claim("idea")
        self.set_sensor(70)
        said = run(["prompt"], self.root, self.payload()).stdout
        self.assertIn("Uncaptured decisions", said)
        self.assertNotIn("not yet on the board or in the graph", said)

    def test_a_seat_compacting_early_is_nudged_before_its_compaction(self) -> None:
        """14% of a 1M window is 70% of a 200k one, past the nudge rung."""
        self.claim()
        self.set_sensor(14, size=1_000_000)
        said = run(["prompt"], self.root, self.payload(), window=200_000).stdout
        self.assertIn("carry-forward", said)

    def test_the_same_reading_without_an_early_window_stays_quiet(self) -> None:
        self.claim()
        self.set_sensor(14, size=1_000_000)
        self.assertNotIn("carry-forward", run(["prompt"], self.root, self.payload()).stdout)

    def test_sensor_below_threshold_is_quiet(self) -> None:
        self.claim()
        self.set_sensor(40)
        self.assertEqual(run(["prompt"], self.root, self.payload()).stdout, "")

    def test_stale_sensor_falls_back_to_transcript_bytes(self) -> None:
        self.claim()
        self.set_sensor(10, age_seconds=600)
        self.transcript.write_text("x" * (500 * 1024))
        self.assertIn("carry-forward", run(["prompt"], self.root, self.payload()).stdout)

    def test_stale_sensor_below_byte_threshold_is_quiet(self) -> None:
        self.claim()
        self.set_sensor(99, age_seconds=600)
        self.transcript.write_text("x" * 1024)
        self.assertEqual(run(["prompt"], self.root, self.payload()).stdout, "")

    def test_backstop_blocks_only_after_two_nudges(self) -> None:
        self.claim()
        self.set_sensor(70)
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 0)
        run(["prompt"], self.root, self.payload())
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 0)
        run(["prompt"], self.root, self.payload())
        blocked = run(["stop"], self.root, self.payload())
        self.assertEqual(blocked.returncode, 2)
        self.assertIn("overdue", blocked.stderr)

    def test_refreshed_truncates_the_log_and_clears_nudges(self) -> None:
        self.claim()
        self.set_sensor(70)
        self.append_turn("ask", "reply")
        run(["stop"], self.root, self.payload())
        run(["prompt"], self.root, self.payload())
        run(["prompt"], self.root, self.payload())
        self.assertTrue(self.log().exists())
        self.assertEqual(run(["refreshed"], self.root).returncode, 0)
        self.assertFalse(self.log().exists())
        state = self.state()
        self.assertEqual(state["nudges"], 0)
        self.assertEqual(state["transcript_offset_at_refresh"], state["log_offset"])
        self.assertEqual(run(["stop"], self.root, self.payload()).returncode, 0)

    def test_sweep_reports_a_young_orphan_and_keeps_it(self) -> None:
        directory = self.root / ".memory" / "transcripts"
        (directory / "aaaaaaaa-orphan.md").write_text("## turn 1\n")
        result = run(["sweep"], self.root)
        self.assertIn("aaaaaaaa", result.stdout)
        self.assertIn("log holds unfolded turns", result.stdout)
        self.assertTrue((directory / "aaaaaaaa-orphan.md").exists())

    def test_sweep_deletes_an_orphan_past_forty_eight_hours(self) -> None:
        directory = self.root / ".memory" / "transcripts"
        stale = directory / "bbbbbbbb-orphan.md"
        stale.write_text("## turn 1\n")
        old = time.time() - (49 * 3600)
        os.utime(stale, (old, old))
        result = run(["sweep"], self.root)
        self.assertIn("deleted", result.stdout)
        self.assertFalse(stale.exists())

    def test_sweep_never_touches_the_live_holder(self) -> None:
        self.claim()
        self.append_turn("ask", "reply")
        run(["stop"], self.root, self.payload())
        old = time.time() - (72 * 3600)
        for path in (self.root / ".memory" / "transcripts").iterdir():
            os.utime(path, (old, old))
        run(["sweep"], self.root)
        self.assertTrue(self.log().exists())

    def test_sweep_clears_a_lock_whose_owner_is_dead(self) -> None:
        self.lock().parent.mkdir(parents=True, exist_ok=True)
        self.lock().write_text(json.dumps({
            "session_id": "dead", "pid": 999999, "started": "irrelevant",
        }))
        run(["sweep"], self.root)
        self.assertFalse(self.lock().exists())

    def test_session_start_announces_a_session_holding_no_role(self) -> None:
        result = run(["session-start"], self.root, self.payload(source="startup"))
        self.assertIn("holds no role", result.stdout)
        self.assertIn("claim <role>", result.stdout)
        self.assertIn("Seats held: none", result.stdout)

    def test_session_start_names_the_seats_already_held(self) -> None:
        self.claim("finalize", "finalize-session")
        result = run(["session-start"], self.root, self.payload(source="startup"))
        self.assertIn("finalize (finalize)", result.stdout)

    def test_two_roles_are_held_at_once_by_different_sessions(self) -> None:
        self.claim("drive")
        self.claim("settle", "settle-session")
        self.assertEqual(json.loads(self.lock("drive").read_text())["session_id"], self.session)
        self.assertEqual(
            json.loads(self.lock("settle").read_text())["session_id"], "settle-session"
        )

    def test_one_session_cannot_hold_two_seats(self) -> None:
        self.claim("drive")
        second = run(["claim", "settle", self.session], self.root)
        self.assertEqual(second.returncode, 1)
        self.assertIn("already holds drive", second.stderr)
        self.assertFalse(self.lock("settle").exists())

    def test_a_role_nobody_named_before_is_claimable(self) -> None:
        self.claim("chart-sweep")
        self.assertEqual(
            json.loads(self.lock("chart-sweep").read_text())["session_id"], self.session
        )
        seatless = run(
            ["session-start"], self.root,
            json.dumps({"session_id": "no-seat", "source": "startup"}),
        )
        self.assertIn("chart-sweep", seatless.stdout)

    def test_a_role_name_that_is_not_a_path_segment_is_refused(self) -> None:
        for name in ("../escape", "Drive", "with space", "a" * 30, ""):
            refused = run(["claim", name, self.session], self.root)
            self.assertEqual(refused.returncode, 1, name)
            self.assertIn("usage:", refused.stderr)
        self.assertFalse((self.root / ".memory" / "roles").exists())

    def test_the_nudge_names_the_holding_role_carry_forward_file(self) -> None:
        self.claim("settle")
        self.set_sensor(70)
        result = run(["prompt"], self.root, self.payload())
        self.assertIn(".memory/carryforward/settle-memory.md", result.stdout)
        self.assertNotIn("drive-memory.md", result.stdout)

    def test_a_dead_seat_is_cleared_while_a_live_one_stands(self) -> None:
        self.claim("drive")
        self.lock("settle").write_text(json.dumps({
            "session_id": "dead-settle", "pid": 999999, "started": "irrelevant",
        }))
        run(["sweep"], self.root)
        self.assertFalse(self.lock("settle").exists())
        self.assertTrue(self.lock("drive").exists())

    def compacted_log(self) -> list[str]:
        """Every piece the session-log hooks print after a compaction, in order."""
        return [run(["session-log", str(index)], self.root, self.payload(source="compact")).stdout
                for index in range(1, 6)]

    def test_session_start_on_compact_serves_the_prompt_file(self) -> None:
        self.claim()
        prompt = self.root / ".claude"
        prompt.mkdir()
        (prompt / "settings.local.md").write_text("# After a compaction\nRe-read the rules.\n")
        self.append_turn("ask", "reply")
        run(["stop"], self.root, self.payload())
        result = run(["session-start"], self.root, self.payload(source="compact"))
        self.assertIn("After a compaction", result.stdout)
        self.assertIn(f".memory/transcripts/{self.session}.md", "".join(self.compacted_log()))

    def test_session_start_on_compact_injects_the_log_content(self) -> None:
        self.claim()
        self.append_turn("what breaks the deploy", "the tenant role grants ingresses alone")
        run(["stop"], self.root, self.payload())
        pieces = self.compacted_log()
        self.assertIn("the tenant role grants ingresses alone", pieces[0])
        self.assertIn("Turns since the last carry-forward refresh", pieces[0])
        self.assertNotIn("earlier characters are in", pieces[0])
        self.assertEqual(pieces[1:], ["", "", "", ""])

    def test_every_piece_stays_under_what_claude_code_inlines(self) -> None:
        """Past 10,000 characters a hook's output arrives as a path, not content."""
        self.claim()
        for index in range(40):
            self.append_turn(f"question {index}", f"answer {index} " + "y" * 1_500)
        self.append_turn("last", "the newest turn survives the cap")
        run(["stop"], self.root, self.payload())
        pieces = self.compacted_log()
        self.assertTrue(all(len(piece) < 10_000 for piece in pieces), [len(p) for p in pieces])
        self.assertIn("the newest turn survives the cap", pieces[-1])
        self.assertIn("earlier characters are in", pieces[0])

    def test_a_single_long_line_is_cut_rather_than_overflowing(self) -> None:
        self.claim()
        self.append_turn("first", "x" * 200_000)
        self.append_turn("last", "the newest turn survives the cap")
        run(["stop"], self.root, self.payload())
        pieces = self.compacted_log()
        self.assertTrue(all(len(piece) < 10_000 for piece in pieces))
        self.assertIn("the newest turn survives the cap", "".join(pieces))

    def test_the_log_arrives_only_after_a_compaction(self) -> None:
        self.claim()
        self.append_turn("ask", "reply")
        run(["stop"], self.root, self.payload())
        started = run(["session-log", "1"], self.root, self.payload(source="startup"))
        self.assertEqual(started.stdout, "")

    def test_session_start_on_compact_is_quiet_with_an_empty_log(self) -> None:
        self.claim()
        self.log().write_text("")
        self.assertEqual(self.compacted_log(), ["", "", "", "", ""])

    def test_session_start_refreshes_the_recorded_pid(self) -> None:
        self.claim()
        record = json.loads(self.lock().read_text())
        record["pid"] = 999999
        record["started"] = "stale"
        self.lock().write_text(json.dumps(record))
        run(["session-start"], self.root, self.payload(source="resume"))
        refreshed = json.loads(self.lock().read_text())
        self.assertNotEqual(refreshed["pid"], 999999)
        self.assertNotEqual(refreshed["started"], "stale")


if __name__ == "__main__":
    unittest.main(verbosity=2)
