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
        self.assertTrue(any("agentic-grounding-gate" in c for c in self.commands()))

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

    def test_a_go_hook_runs_the_installed_binary(self):
        self.settings.write_text(json.dumps({"hooks": {"SessionEnd": [{"hooks": [
            {"type": "command", "command": 'python3 "/somewhere/hooks/scratch.py" end'}
        ]}]}}))
        self.run_install()
        scratch = [c for c in self.commands() if "scratch" in c]
        self.assertEqual(len(scratch), 2, "the old script entry stayed beside the binary")
        for held in scratch:
            self.assertRegex(held, r'^"/[^"]+/bin/agentic-scratch" (start|end)$')

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


class Skills(unittest.TestCase):
    """Skills are linked, so an edit in the checkout reaches every project."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.skills = Path(self.dir) / "skills"

    def run_install(self, *args):
        env = dict(os.environ, CLAUDE_CONFIG_DIR=self.dir)
        return subprocess.run([sys.executable, str(INSTALL), *args],
                              capture_output=True, text=True, env=env, check=False)

    def shipped(self):
        return sorted(p.name for p in (INSTALL.parent / "skills").iterdir() if p.is_dir())

    def test_every_skill_this_repository_ships_is_linked(self):
        self.run_install()
        self.assertEqual(sorted(p.name for p in self.skills.iterdir()), self.shipped())
        for link in self.skills.iterdir():
            self.assertTrue(link.is_symlink(), f"{link} is a copy rather than a link")
            self.assertTrue((link / "SKILL.md").is_file())

    def test_a_link_naming_somewhere_else_is_replaced(self):
        self.skills.mkdir()
        stale = self.skills / self.shipped()[0]
        stale.symlink_to("/somewhere/else/that/moved")
        self.run_install()
        self.assertEqual(os.path.realpath(stale),
                         str(INSTALL.parent / "skills" / stale.name))

    def test_a_directory_somebody_else_put_there_is_left_alone(self):
        self.skills.mkdir()
        theirs = self.skills / self.shipped()[0]
        theirs.mkdir()
        (theirs / "SKILL.md").write_text("theirs")
        result = self.run_install()
        self.assertIn("not a link", result.stderr)
        self.assertEqual((theirs / "SKILL.md").read_text(), "theirs")

    def test_remove_unlinks_them(self):
        self.run_install()
        self.run_install("--remove")
        self.assertEqual(list(self.skills.iterdir()), [])

    def test_running_it_twice_relinks_nothing(self):
        self.run_install()
        second = self.run_install()
        self.assertNotIn("linked", second.stdout)

    def test_print_links_nothing(self):
        result = self.run_install("--print")
        self.assertIn("link ", result.stdout)
        self.assertFalse(self.skills.exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
