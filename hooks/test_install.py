#!/usr/bin/env python3
"""The installer edits one settings file that also carries what a person set.

Every case here drives the real script against a settings directory of its own,
and asserts both halves: what the installer wrote, and what it left untouched.
"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

INSTALL = Path(__file__).resolve().parent.parent / "install.py"

# A settings file as a person leaves it: a credential, a model, and a hook of
# their own that this repository knows nothing about.
THEIRS = {
    "env": {"SOME_TOKEN": "keep-me"},
    "model": "opus",
    "hooks": {
        "UserPromptSubmit": [
            {"hooks": [{"type": "command", "command": "echo their-own-rule", "timeout": 5}]}
        ]
    },
}


class Install(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.settings = Path(self.dir) / "settings.json"

    def run_install(self, *args):
        env = dict(os.environ, CLAUDE_CONFIG_DIR=self.dir)
        return subprocess.run([sys.executable, str(INSTALL), *args],
                              capture_output=True, text=True, env=env, check=False)

    def written(self):
        return json.loads(self.settings.read_text())

    def commands(self, held=None):
        held = held if held is not None else self.written()
        return [entry["command"]
                for groups in held.get("hooks", {}).values()
                for group in groups
                for entry in group["hooks"]]

    def write_theirs(self):
        self.settings.write_text(json.dumps(THEIRS, indent=2))

    def test_a_settings_file_that_does_not_exist_is_created(self):
        self.assertEqual(self.run_install().returncode, 0)
        self.assertTrue(any("carryforward.py" in c for c in self.commands()))
        self.assertTrue(any("grounding_gate.py" in c for c in self.commands()))

    def test_everything_the_person_set_survives(self):
        self.write_theirs()
        self.assertEqual(self.run_install().returncode, 0)
        held = self.written()
        self.assertEqual(held["env"], {"SOME_TOKEN": "keep-me"})
        self.assertEqual(held["model"], "opus")
        self.assertIn("echo their-own-rule", self.commands(held))

    def test_a_copy_of_the_old_settings_is_kept(self):
        self.write_theirs()
        self.run_install()
        backups = list(Path(self.dir).glob("settings.json.bak-*"))
        self.assertEqual(len(backups), 1)
        self.assertEqual(json.loads(backups[0].read_text()), THEIRS)

    def test_running_it_twice_changes_nothing(self):
        self.run_install()
        once = self.written()
        second = self.run_install()
        self.assertEqual(second.returncode, 0)
        self.assertIn("already says this", second.stdout)
        self.assertEqual(self.written(), once)

    def test_an_entry_from_a_checkout_that_moved_is_replaced(self):
        self.settings.write_text(json.dumps({"hooks": {"Stop": [{"hooks": [
            {"type": "command", "command": 'python3 "/somewhere/else/hooks/carryforward.py" stop'}
        ]}]}}))
        self.run_install()
        stale = [c for c in self.commands() if "/somewhere/else/" in c]
        self.assertEqual(stale, [], "an entry naming the old directory stayed behind")
        self.assertTrue(any("carryforward.py" in c for c in self.commands()))

    def test_remove_takes_only_what_this_repository_owns(self):
        self.write_theirs()
        self.run_install()
        self.assertEqual(self.run_install("--remove").returncode, 0)
        held = self.written()
        self.assertEqual(self.commands(held), ["echo their-own-rule"])
        self.assertEqual(held["env"], {"SOME_TOKEN": "keep-me"})

    def test_remove_drops_an_empty_hooks_key(self):
        self.run_install()
        self.run_install("--remove")
        self.assertNotIn("hooks", self.written())

    def test_print_writes_nothing(self):
        self.write_theirs()
        result = self.run_install("--print")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(self.written(), THEIRS)
        self.assertIn("carryforward.py", result.stdout)

    def test_settings_that_are_not_json_are_refused_rather_than_overwritten(self):
        self.settings.write_text("{ this is not json")
        result = self.run_install()
        self.assertEqual(result.returncode, 1)
        self.assertIn("not readable as JSON", result.stderr)
        self.assertEqual(self.settings.read_text(), "{ this is not json")


if __name__ == "__main__":
    unittest.main(verbosity=2)
