#!/usr/bin/env python3
"""The sweep deletes. Every case asserts what it removed and what it did not.

A directory named anything but a session identifier is somebody's deliberate
keep. Each test that proves a removal also proves a keep survives the same run.
"""

import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent / "scratch.py"
SESSION = "c3fb65eb-dbdf-47a8-9e04-e0477053b2b6"
OTHER = "a1b2c3d4-0000-4000-8000-000000000000"


def run(mode, payload, project_dir):
    env = dict(os.environ, CLAUDE_PROJECT_DIR=str(project_dir))
    return subprocess.run([sys.executable, str(HOOK), mode], input=json.dumps(payload),
                          capture_output=True, text=True, env=env, check=False)


class Scratch(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp())
        subprocess.run(["git", "init", "-q", str(self.dir)], check=True)
        self.ours = self.dir / ".tmp" / "claude"

    def make(self, name, age_days=0):
        held = self.ours / name
        held.mkdir(parents=True)
        (held / "probe.txt").write_text("scratch")
        if age_days:
            old = time.time() - age_days * 86400
            os.utime(held, (old, old))
        return held

    def test_a_session_directory_goes_when_the_session_ends(self):
        mine = self.make(SESSION)
        run("end", {"session_id": SESSION}, self.dir)
        self.assertFalse(mine.exists())

    def test_another_session_directory_is_left_alone(self):
        mine, theirs = self.make(SESSION), self.make(OTHER)
        run("end", {"session_id": SESSION}, self.dir)
        self.assertFalse(mine.exists())
        self.assertTrue(theirs.exists(), "a running session lost its scratch")

    def test_a_named_directory_is_never_removed(self):
        keep = self.ours / "keep"
        keep.mkdir(parents=True)
        (keep / "worth-keeping.sh").write_text("#!/bin/sh\n")
        mine = self.make(SESSION)
        run("end", {"session_id": SESSION}, self.dir)
        self.assertFalse(mine.exists())
        self.assertTrue((keep / "worth-keeping.sh").is_file())

    def test_a_directory_beside_ours_is_never_removed(self):
        drafts = self.dir / ".tmp" / "drafts"
        drafts.mkdir(parents=True)
        (drafts / "entry.md").write_text("draft")
        self.make(SESSION)
        run("end", {"session_id": SESSION}, self.dir)
        self.assertTrue((drafts / "entry.md").is_file())

    def test_an_abandoned_directory_is_reaped_at_the_next_start(self):
        stale, fresh, named = self.make(OTHER, age_days=30), self.make(SESSION), self.ours / "keep"
        named.mkdir(parents=True)
        os.utime(named, (time.time() - 99 * 86400,) * 2)
        result = run("start", {"session_id": SESSION}, self.dir)
        self.assertFalse(stale.exists())
        self.assertTrue(fresh.exists(), "a directory inside the window was reaped")
        self.assertTrue(named.exists(), "a named directory was reaped on age alone")
        self.assertIn("reaped", result.stdout)

    def test_a_project_with_no_scratch_directory_is_left_alone(self):
        bare = Path(tempfile.mkdtemp())
        result = run("start", {"session_id": SESSION}, bare)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertFalse((bare / ".tmp").exists(), "the hook created a directory")

    def test_a_payload_naming_no_session_removes_nothing(self):
        mine = self.make(SESSION)
        run("end", {}, self.dir)
        self.assertTrue(mine.exists())


class Reports(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp())
        subprocess.run(["git", "init", "-q", str(self.dir)], check=True)
        (self.dir / ".tmp").mkdir()
        self.memory = self.dir / ".memory"
        (self.memory / "carryforward").mkdir(parents=True)

    def start(self):
        return run("start", {"session_id": SESSION}, self.dir).stdout

    def test_an_oversized_carry_forward_is_reported(self):
        held = self.memory / "carryforward" / "main-memory.md"
        held.write_text("x" * (21 * 1024))
        self.assertIn("main-memory.md is 21 KB", self.start())

    def test_a_carry_forward_within_the_limit_is_not(self):
        (self.memory / "carryforward" / "main-memory.md").write_text("x" * 1024)
        self.assertEqual(self.start(), "")

    def test_a_pointer_at_nothing_is_reported(self):
        (self.memory / "MEMORY.md").write_text("- [Gone](carryforward/gone-memory.md) - hook\n")
        self.assertIn("points at carryforward/gone-memory.md", self.start())

    def test_a_pointer_that_resolves_is_not(self):
        (self.memory / "carryforward" / "main-memory.md").write_text("held")
        (self.memory / "MEMORY.md").write_text("- [Main](carryforward/main-memory.md) - hook\n")
        self.assertEqual(self.start(), "")

    def test_a_link_to_the_web_is_not_a_dead_pointer(self):
        (self.memory / "MEMORY.md").write_text("- [Docs](https://example.com/x) - hook\n")
        self.assertEqual(self.start(), "")


if __name__ == "__main__":
    unittest.main(verbosity=2)
