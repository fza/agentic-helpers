#!/usr/bin/env python3
"""Every branch of the grounding gate, including the ones that must refuse.

A guard that cannot fail is worthless, so each case here asserts the refusal as
hard as it asserts the pass: the same call is driven once with the evidence
present and once with it missing, and the two outcomes have to differ.
"""

import json
import os
import subprocess
import sys
import tempfile
import unittest

# The session may point every process at the real ledger; a test never reads or writes it.
os.environ.pop("GRAPH_LEDGER_DIR", None)

HOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "grounding_gate.py")


def run(mode, payload, project_dir, cwd=None):
    # The gate answers nothing for a project carrying no graph, so every fixture
    # carries one: a test of the gate's reasoning is never a test of its opt-in.
    os.makedirs(os.path.join(project_dir, ".sdd"), exist_ok=True)
    env = dict(os.environ, CLAUDE_PROJECT_DIR=project_dir)
    result = subprocess.run([sys.executable, HOOK, mode], input=json.dumps(payload),
                            capture_output=True, text=True, env=env, check=False, cwd=cwd)

    return result


def bash(command, session="s1"):
    return {"session_id": session, "tool_name": "Bash", "tool_input": {"command": command}}


SEARCH = "python3 scripts/graph.py search --terms 'ssh_host_key_changed'"
# A turn opens the gate only when both modes ran: a literal token and a phrase.
TERM = "python3 scripts/graph.py search --term 'ssh_host_key_changed'"
QUERY = "python3 scripts/graph.py search --query 'a host key that changed'"


def ground(run, directory, session="s1"):
    """Every read the gate asks for, so a test can get past it in one call."""
    run("record", bash(TERM, session=session), directory)
    run("record", bash(QUERY, session=session), directory)
    run("record", bash(LISTING, session=session), directory)
LISTING = 'python3 scripts/graph.py view --layout "topic(area-hooks):as-list"'


class GroundingGate(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def ask(self, session="s1"):
        return {"session_id": session, "tool_name": "AskUserQuestion",
                "tool_input": {"questions": []}}

    def draft(self, path, session="s1"):
        return {"session_id": session, "tool_name": "Edit",
                "tool_input": {"file_path": path}}

    def test_question_refused_with_no_reads_at_all(self):
        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 2)
        self.assertIn("neither read", result.stderr)

    def test_no_refusal_spells_a_flag_the_tool_does_not_take(self):
        """The guard exists to catch `--terms`, so it may not print it either."""
        for payload in (self.ask(), bash("python3 scripts/graph.py search --term x 2>/dev/null")):
            with self.subTest(tool=payload["tool_name"]):
                stderr = run("gate", payload, self.dir).stderr
                self.assertIn("--term ", stderr)
                self.assertNotIn("--terms", stderr)

    def test_question_refused_on_a_search_alone(self):
        run("record", bash(SEARCH), self.dir)
        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 2, "a search alone must not open the gate")
        self.assertIn("No area listing ran this turn.", result.stderr)

    def test_question_refused_on_an_area_listing_alone(self):
        run("record", bash(LISTING), self.dir)
        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 2, "a listing alone must not open the gate")
        self.assertIn("No `--query` search ran this turn.", result.stderr)

    def test_one_search_mode_alone_never_opens_the_gate(self):
        """A phrase matches wording, and a token matches the word nobody guessed."""
        for command, missing in ((QUERY, "--term"), (TERM, "--query")):
            with self.subTest(ran=command):
                directory = tempfile.mkdtemp()
                run("record", bash(command), directory)
                run("record", bash(LISTING), directory)
                result = run("gate", self.ask(), directory)
                self.assertEqual(result.returncode, 2)
                self.assertIn(f"No `{missing}` search ran this turn.", result.stderr)

    def test_question_passes_on_both(self):
        ground(run, self.dir)
        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_show_alone_never_opens_the_gate(self):
        run("record", bash("python3 scripts/graph.py show 20260829-122703-d-tac-kuz"), self.dir)
        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 2)

    def test_draft_edit_is_gated_and_other_files_are_not(self):
        refused = run("gate", self.draft("/x/.tmp/drafts/ssh-access/01-block.md"), self.dir)
        self.assertEqual(refused.returncode, 2)

        allowed = run("gate", self.draft("/x/docs/fdbox/07-cli.md"), self.dir)
        self.assertEqual(allowed.returncode, 0, "only a draft edit is gated")

    def ledger_with(self, draft, history):
        ledger = os.path.join(self.dir, ".sdd", "autopilot", "turns")
        os.makedirs(ledger, exist_ok=True)
        with open(os.path.join(ledger, "20260901-000000-d-tac-aaa.json"), "w", encoding="utf-8") as handle:
            json.dump({"drafts": {draft: {"hash": "x", "history": history}}}, handle)

    def test_a_fix_to_a_verified_draft_owes_no_fresh_reads(self):
        """The verification grounded it; the next delta reading judges the change."""
        draft = os.path.join(self.dir, ".tmp", "drafts", "fix.md")
        self.ledger_with(draft, [{"agent": "verify-1", "verdict": "findings"}])
        self.assertEqual(run("gate", self.draft(draft), self.dir).returncode, 0)

    def test_a_registered_draft_nobody_verified_still_owes_the_reads(self):
        draft = os.path.join(self.dir, ".tmp", "drafts", "new.md")
        self.ledger_with(draft, [])
        self.assertEqual(run("gate", self.draft(draft), self.dir).returncode, 2)

    def test_another_drafts_verification_opens_nothing(self):
        self.ledger_with(os.path.join(self.dir, ".tmp", "drafts", "other.md"),
                         [{"agent": "verify-1", "verdict": "clean"}])
        draft = os.path.join(self.dir, ".tmp", "drafts", "new.md")
        self.assertEqual(run("gate", self.draft(draft), self.dir).returncode, 2)

    def test_a_read_in_another_repository_answers_to_that_one(self):
        """These rules derive from this project's graph, and bind its tree alone."""
        other = os.path.join(self.dir, "..", "other-repo")
        os.makedirs(os.path.join(other, ".git"), exist_ok=True)
        away = run("gate", bash(f"cd {other} && sdd show abc --down 2"), self.dir)
        self.assertEqual(away.returncode, 0)

    def test_a_step_into_nowhere_is_no_exemption(self):
        """A path that merely reads as a repository is this tree's business."""
        held = run("gate", bash("cd /x && sdd show abc --down 2"), self.dir)
        self.assertEqual(held.returncode, 2)

    def test_a_read_in_this_tree_still_needs_the_wrapper(self):
        for command in ("sdd show abc --down 2",
                        f"cd {self.dir} && sdd show abc --down 2",
                        f"cd {self.dir}/source && sdd search --term x"):
            with self.subTest(command=command):
                held = run("gate", bash(command), self.dir)
                self.assertEqual(held.returncode, 2, command)

    def test_a_relative_step_never_counts_as_leaving(self):
        held = run("gate", bash("cd source && sdd show abc --down 2"), self.dir)
        self.assertEqual(held.returncode, 2)

    def test_a_new_turn_discards_the_evidence(self):
        ground(run, self.dir)
        self.assertEqual(run("gate", self.ask(), self.dir).returncode, 0)

        run("turn", {"session_id": "s1"}, self.dir)
        self.assertEqual(run("gate", self.ask(), self.dir).returncode, 2,
                         "evidence from an earlier turn must not carry over")

    def test_one_session_never_answers_for_another(self):
        ground(run, self.dir, session="s1")
        self.assertEqual(run("gate", self.ask(session="s1"), self.dir).returncode, 0)
        self.assertEqual(run("gate", self.ask(session="s2"), self.dir).returncode, 2)

    def test_a_subagents_reads_never_open_this_sessions_gate(self):
        """A subagent carries the parent's session id, and its own agent id."""
        for command in (TERM, QUERY, LISTING):
            run("record", dict(bash(command), agent_id="a1",
                               agent_type="general-purpose"), self.dir)

        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 2,
                         "grounding this session never performed must not open its gate")
        self.assertIn("No `--query` search ran this turn.", result.stderr)

    def test_a_subagents_own_reads_open_its_own_gate(self):
        for command in (TERM, QUERY, LISTING):
            run("record", dict(bash(command), agent_id="a1", agent_type="fdbox-validate"), self.dir)
        self.assertEqual(run("gate", dict(self.ask(), agent_id="a1"), self.dir).returncode, 0)
        self.assertEqual(run("gate", dict(self.ask(), agent_id="a2"), self.dir).returncode, 2,
                         "one agent's reads must not open another's gate")

    def test_this_sessions_own_reads_still_open_the_gate(self):
        """The session's payload carries no agent field, so nothing is dropped."""
        ground(run, self.dir)
        self.assertEqual(run("gate", self.ask(), self.dir).returncode, 0)

    def test_the_listing_records_which_area_it_read(self):
        run("record", bash(LISTING), self.dir)
        run("record", bash('python3 scripts/graph.py view --layout "topic(area-bare-host-deployment):as-counts"'),
            self.dir)
        with open(os.path.join(self.dir, ".sdd", "autopilot", "turns", "s1.json")) as handle:
            state = json.load(handle)
        self.assertEqual(state["areas"], ["area-hooks", "area-bare-host-deployment"])

    def test_a_topic_that_names_no_area_is_not_an_area_listing(self):
        run("record", bash('python3 scripts/graph.py view --layout "topic(deployment):as-list"'), self.dir)
        run("record", bash(SEARCH), self.dir)
        result = run("gate", self.ask(), self.dir)
        self.assertEqual(result.returncode, 2, "a facet listing is not an area listing")

    def test_a_graph_read_may_not_discard_a_stream(self):
        discarding = [
            "python3 scripts/graph.py search --term x 2>/dev/null",
            "python3 scripts/graph.py search --term x 2> /dev/null",
            "python3 scripts/graph.py show abc >/dev/null",
            "python3 scripts/graph.py view --layout y &>/dev/null",
            "python3 scripts/graph.py search --term x >/dev/null 2>&1",
            "python3 scripts/graph.py search --term x 2>&1 >/dev/null",
            "python3 scripts/graph.py info 1>/dev/null",
            "cd /x && python3 scripts/graph.py search --term x 2>/dev/null | grep e",
            "mkdir -p q 2>/dev/null; python3 scripts/graph.py search --term x",
        ]
        for command in discarding:
            with self.subTest(command=command):
                result = run("gate", bash(command), self.dir)
                self.assertEqual(result.returncode, 2, f"must refuse: {command}")
                self.assertIn("discarded stream", result.stderr)

    def test_a_graph_read_with_both_streams_attached_passes(self):
        allowed = [
            "python3 scripts/graph.py search --term x --limit 8 | grep -E '^  [0-9]'",
            "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --up 0 --down 2",
            'python3 scripts/graph.py view --layout "topic(area-hooks):as-list"',
        ]
        for command in allowed:
            with self.subTest(command=command):
                self.assertEqual(run("gate", bash(command), self.dir).returncode, 0)

    def test_a_command_that_is_not_a_graph_read_may_use_the_null_device(self):
        for command in ["grep -r sdd docs/ 2>/dev/null",
                        "ls .sdd/ 2>/dev/null",
                        "python3 x.py 2>/dev/null"]:
            with self.subTest(command=command):
                self.assertEqual(run("gate", bash(command), self.dir).returncode, 0,
                                 f"only an sdd call is covered: {command}")

    def test_a_show_may_not_suppress_its_downstream_chain(self):
        blind = [
            "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --up 0 --down 0",
            "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --down 0",
            "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --down=0",
            "python3 scripts/graph.py show 20260912-152909-d-cpt-ony 20260829-122703-d-tac-kuz --down 0",
        ]
        for command in blind:
            with self.subTest(command=command):
                result = run("gate", bash(command), self.dir)
                self.assertEqual(result.returncode, 2, f"must refuse: {command}")
                self.assertIn("hides the chain", result.stderr)

    def test_a_show_that_reads_its_downstream_passes(self):
        for command in ["python3 scripts/graph.py show 20260907-164635-d-tac-vkm --up 0 --down 2",
                        "python3 scripts/graph.py show 20260907-164635-d-tac-vkm",
                        "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --down 1"]:
            with self.subTest(command=command):
                self.assertEqual(run("gate", bash(command), self.dir).returncode, 0)

    def test_a_process_layer_rules_entry_may_suppress_it(self):
        """Its downstream chain is every entry ever captured under it."""
        allowed = "python3 scripts/graph.py show 20260828-160000-s-prc-rfk --up 0 --down 0"
        self.assertEqual(run("gate", bash(allowed), self.dir).returncode, 0)

        mixed = ("python3 scripts/graph.py show 20260828-160000-s-prc-rfk "
                 "20260907-164635-d-tac-vkm --down 0")
        self.assertEqual(run("gate", bash(mixed), self.dir).returncode, 2,
                         "one non-process entry in the list refuses the whole read")

    def test_the_subject_named_by_entry_never_decides_the_exemption(self):
        """Every agent read names its subject; the rules entry is what it reads."""
        rules = ("python3 scripts/graph.py show 20260828-160000-s-prc-rfk --up 0 --down 0 "
                 "--entry 20260831-185605-d-tac-1oz")
        self.assertEqual(run("gate", bash(rules), self.dir).returncode, 0)
        smuggled = ("python3 scripts/graph.py show 20260831-185605-d-tac-1oz --down 0 "
                    "--entry 20260828-160000-s-prc-rfk")
        self.assertEqual(run("gate", bash(smuggled), self.dir).returncode, 2)

    def test_a_heredoc_body_is_data_rather_than_a_call(self):
        """Writing about the mistake must not be refused as committing it."""
        quoting = (
            "cd /x\npython3 - <<'PY'\n"
            "text = '''never run: sdd search --term x 2>/dev/null\n"
            "and never: sdd show 20260907-164635-d-tac-vkm --down 0'''\n"
            "PY"
        )
        self.assertEqual(run("gate", bash(quoting), self.dir).returncode, 0, "a body is data")

    def test_a_real_call_after_a_heredoc_still_refuses(self):
        both = (
            "cd /x\npython3 - <<'PY'\nprint('hello')\nPY\n"
            "python3 scripts/graph.py search --term x 2>/dev/null"
        )
        result = run("gate", bash(both), self.dir)
        self.assertEqual(result.returncode, 2)
        self.assertIn("discarded stream", result.stderr)

    def test_the_tool_reached_without_the_wrapper_is_refused(self):
        bypassing = [
            "sdd search --term x",
            "cd /x && sdd view --layout \"topic(area-hooks):as-list\"",
            "sdd show 20260907-164635-d-tac-vkm --down 2",
            "sdd info",
        ]
        for command in bypassing:
            with self.subTest(command=command):
                result = run("gate", bash(command), self.dir)
                self.assertEqual(result.returncode, 2, f"must refuse: {command}")
                self.assertIn("without the wrapper", result.stderr)

    def test_the_same_read_through_the_wrapper_passes(self):
        for command in ["python3 scripts/graph.py search --term x",
                        "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --down 2",
                        "python3 scripts/graph.py info"]:
            with self.subTest(command=command):
                self.assertEqual(run("gate", bash(command), self.dir).returncode, 0)

    def test_a_word_that_is_not_a_call_is_not_the_tool(self):
        for command in ["grep -rn sdd docs/", "ls .sdd/graph", "echo 'sdd is the tool'",
                        'grep -n "Bash(sdd new" skill.md',
                        r'grep -rn "captured\|sdd new" file.md',
                        'grep -E "a|sdd show" x',
                        "echo 'allowed-tools: Bash(sdd *)'"]:
            with self.subTest(command=command):
                self.assertEqual(run("gate", bash(command), self.dir).returncode, 0)

    def test_a_pipeline_and_a_subshell_are_still_calls(self):
        """The refusals above only mean something if the real shapes still refuse."""
        for command in ["(sdd search --term x)", "cd /x && (sdd info)",
                        "cat x | sdd search --term y", "cat x |sdd search --term y",
                        "sdd search --query 'a subject' --limit 8"]:
            with self.subTest(command=command):
                result = run("gate", bash(command), self.dir)
                self.assertEqual(result.returncode, 2, f"must refuse: {command}")
                self.assertIn("without the wrapper", result.stderr)

    def test_another_spelling_of_the_same_binary_is_refused(self):
        """A path, a runner, a shell payload and a variable all reach the tool."""
        bypassing = [
            "bash -c 'sdd search --term x'",
            "sh -c \"sdd info\"",
            "env sdd search --term x",
            "env SDD_HOME=/x sdd info",
            "/opt/homebrew/bin/sdd search --term x",
            "S=sdd; $S search --term x",
            "echo 20260907-164635-d-tac-vkm | xargs sdd show",
            "bash -c 'bash -c \"sdd info\"'",
        ]
        for command in bypassing:
            with self.subTest(command=command):
                result = run("gate", bash(command), self.dir)
                self.assertEqual(result.returncode, 2, f"must refuse: {command}")
                self.assertIn("without the wrapper", result.stderr)

    def test_a_mention_of_another_spelling_is_still_a_mention(self):
        """The refusals above only mean something if a line describing one passes."""
        for command in ["grep -c 'sdd show' file.md",
                        "grep -n \"bash -c 'sdd info'\" skill.md",
                        "grep -n 'S=sdd' notes.md",
                        "bash -c 'ls docs/'",
                        "python3 scripts/graph.py search --term x"]:
            with self.subTest(command=command):
                self.assertEqual(run("gate", bash(command), self.dir).returncode, 0, command)

    def test_an_escaped_quote_opens_no_span(self):
        """Pairing one with a later quote hides every call between the two."""
        command = "echo it\\'s ; sdd show 20260907-164635-d-tac-vkm --down 3 ; echo x'"
        result = run("gate", bash(command), self.dir)
        self.assertEqual(result.returncode, 2)
        self.assertIn("without the wrapper", result.stderr)

    def test_a_deeper_zero_is_still_zero(self):
        command = "python3 scripts/graph.py show 20260907-164635-d-tac-vkm --down 00"
        result = run("gate", bash(command), self.dir)
        self.assertEqual(result.returncode, 2)
        self.assertIn("--down 0", result.stderr)

    def test_an_unreadable_ledger_refuses_rather_than_passing(self):
        os.makedirs(os.path.join(self.dir, ".sdd", "autopilot", "turns"))
        with open(os.path.join(self.dir, ".sdd", "autopilot", "turns", "s1.json"), "w") as handle:
            handle.write("{ not json")
        self.assertEqual(run("gate", self.ask(), self.dir).returncode, 2)


class GapCapture(unittest.TestCase):
    """A draft recording a problem never reaches the graph as an entry."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def draft_file(self, kind: str) -> str:
        path = os.path.join(self.dir, "draft.md")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(f"---\ntype: signal\nlayer: tactical\nkind: {kind}\n---\n\nA body.\n")

        return path

    def capture(self, path: str) -> str:
        return f"python3 scripts/graph.py capture {path} --entry 20260901-000000-d-tac-aaa"

    def test_a_gap_draft_is_refused(self):
        result = run("gate", bash(self.capture(self.draft_file("gap"))), self.dir)
        self.assertEqual(result.returncode, 2)
        self.assertIn("carries `kind: gap`", result.stderr)

    def test_every_other_kind_passes(self):
        for kind in ("fact", "done", "question", "insight"):
            with self.subTest(kind=kind):
                result = run("gate", bash(self.capture(self.draft_file(kind))), self.dir)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_a_gap_draft_named_outside_a_capture_passes(self):
        """Reading a draft is not capturing it."""
        path = self.draft_file("gap")
        self.assertEqual(run("gate", bash(f"cat {path}"), self.dir).returncode, 0)

    def test_another_checkouts_draft_is_not_this_one(self):
        """A relative name means the file here, never one in somebody else's tree."""
        tree = os.path.join(self.dir, ".claude", "worktrees", "probe")
        os.makedirs(tree)
        with open(os.path.join(tree, "draft.md"), "w", encoding="utf-8") as handle:
            handle.write("---\ntype: signal\nlayer: tactical\nkind: gap\n---\n\nA body.\n")

        here = os.path.join(self.dir, "here")
        os.makedirs(here)
        with open(os.path.join(here, "draft.md"), "w", encoding="utf-8") as handle:
            handle.write("---\ntype: signal\nlayer: tactical\nkind: fact\n---\n\nA body.\n")

        result = run("gate", bash(self.capture("draft.md")), self.dir, cwd=here)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_a_gap_draft_in_this_directory_is_still_refused(self):
        """The pass above only means something if the local gap still refuses."""
        here = os.path.join(self.dir, "local")
        os.makedirs(here)
        with open(os.path.join(here, "draft.md"), "w", encoding="utf-8") as handle:
            handle.write("---\ntype: signal\nlayer: tactical\nkind: gap\n---\n\nA body.\n")

        result = run("gate", bash(self.capture("draft.md")), self.dir, cwd=here)
        self.assertEqual(result.returncode, 2)

    def test_a_capture_naming_no_draft_on_disk_passes(self):
        command = self.capture(os.path.join(self.dir, "absent.md"))
        self.assertEqual(run("gate", bash(command), self.dir).returncode, 0)


class WhatCountsAsAReadPerformed(unittest.TestCase):
    """The ledger credits a call that ran and answered, and nothing else."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def ask(self):
        return {"session_id": "s1", "tool_name": "AskUserQuestion",
                "tool_input": {"questions": []}}

    def record(self, command, response=None):
        payload = bash(command)
        if response is not None:
            payload["tool_response"] = response
        run("record", payload, self.dir)

    def gate(self):
        return run("gate", self.ask(), self.dir).returncode

    def ground(self, response=None):
        self.record("python3 scripts/graph.py search --query 'a subject' --term x", response)
        self.record('python3 scripts/graph.py view --layout "topic(area-hooks):as-list"',
                    response)

    def test_reads_that_ran_open_the_gate(self):
        self.ground()
        self.assertEqual(self.gate(), 0)

    def test_a_mention_of_a_read_counts_for_nothing(self):
        """An echo naming every read would otherwise satisfy the whole gate."""
        self.record("echo 'sdd search --query foo --term bar'")
        self.record("echo 'sdd view --layout \"topic(area-hooks):as-list\"'")
        self.assertEqual(self.gate(), 2)

    def test_a_read_that_came_back_refused_counts_for_nothing(self):
        """A wrong flag prints usage and exits non-zero, and finds nothing."""
        self.ground({"exit_code": 1})
        self.assertEqual(self.gate(), 2)

    def test_a_read_the_harness_marked_an_error_counts_for_nothing(self):
        self.ground({"is_error": True})
        self.assertEqual(self.gate(), 2)

    def test_a_read_with_no_recorded_answer_still_counts(self):
        """Nothing guarantees a response field, and refusing then would refuse all."""
        self.ground(None)
        self.assertEqual(self.gate(), 0)


class TheTurnHookNeverBlocksAPrompt(unittest.TestCase):
    """A UserPromptSubmit hook exiting non-zero blocks the prompt itself."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def turn(self, payload):
        return run("turn", payload, self.dir).returncode

    def test_a_payload_that_is_not_an_object_is_survived(self):
        result = subprocess.run(
            [sys.executable, HOOK, "turn"], input='"a string"',
            capture_output=True, text=True,
            env=dict(os.environ, CLAUDE_PROJECT_DIR=self.dir), check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_a_session_name_carrying_a_separator_is_survived(self):
        self.assertEqual(self.turn({"session_id": "a/b/c"}), 0)

    def test_a_ledger_directory_that_is_a_file_is_survived(self):
        os.makedirs(os.path.join(self.dir, ".sdd", "autopilot"), exist_ok=True)
        with open(os.path.join(self.dir, ".sdd", "autopilot", "turns"), "w") as handle:
            handle.write("occupied")
        self.assertEqual(self.turn({"session_id": "s1"}), 0)

    def test_a_turn_that_cannot_clear_leaves_no_evidence_standing(self):
        """Carrying the last turn's reads forward opens the gate on nobody's work."""
        ground(run, self.dir)
        self.assertEqual(run("gate", self.ask(), self.dir).returncode, 0)
        self.assertEqual(self.turn({"session_id": "s1"}), 0)
        self.assertEqual(run("gate", self.ask(), self.dir).returncode, 2)

    def ask(self, session="s1"):
        return {"session_id": session, "tool_name": "AskUserQuestion",
                "tool_input": {"questions": []}}


class OptIn(unittest.TestCase):
    """A project carrying no graph never hears from the gate.

    Every refusal the gate makes reasons about what a session read out of a
    decision graph, so a project without one has nothing to answer to. The same
    call is driven twice, once against a project carrying a graph and once
    against a project carrying none, and the two outcomes have to differ.
    """

    def ask(self):
        return {"session_id": "opt-in", "tool_name": "AskUserQuestion",
                "tool_input": {"questions": []}}

    def gate(self, project_dir):
        env = dict(os.environ, CLAUDE_PROJECT_DIR=project_dir)
        return subprocess.run([sys.executable, HOOK, "gate"], input=json.dumps(self.ask()),
                              capture_output=True, text=True, env=env, check=False,
                              cwd=project_dir)

    def test_a_project_with_no_graph_is_left_alone(self):
        bare = tempfile.mkdtemp(dir=tempfile.mkdtemp())
        result = self.gate(bare)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")

    def test_the_same_call_is_refused_where_a_graph_stands(self):
        held = tempfile.mkdtemp(dir=tempfile.mkdtemp())
        os.makedirs(os.path.join(held, ".sdd"))
        result = self.gate(held)
        self.assertEqual(result.returncode, 2)
        self.assertIn("search", result.stderr)

    def test_a_graph_above_the_session_answers_for_it(self):
        root = tempfile.mkdtemp()
        os.makedirs(os.path.join(root, ".sdd"))
        below = os.path.join(root, "source", "internal")
        os.makedirs(below)
        self.assertEqual(self.gate(below).returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
