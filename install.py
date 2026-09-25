#!/usr/bin/env python3
"""Wire this checkout's hooks into Claude Code, or take them back out.

Claude Code reads one settings file per user. That file also carries credentials
and everything else a person configured, so this edits only the hook entries
naming this checkout and leaves every other key byte for byte as it found it. A
timestamped copy goes beside it before anything is written.

Running it twice changes nothing the second time: every entry running one of
these scripts is removed before the current set goes in, whichever directory it
named, so a moved or renamed checkout leaves nothing stale behind.

A hook built in Go runs from where `go install` put it, so this wires the
binary's path in GOBIN (or GOPATH's bin) and warns when it is not there yet.

Skills are linked rather than copied, so an edit in this checkout reaches every
project at once and no copy drifts from the file it came from.

    install.py            wire the hooks in and link the skills
    install.py --remove   take both back out
    install.py --print    write nothing, show what would land
"""

import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
SETTINGS = Path(os.environ.get("CLAUDE_CONFIG_DIR", Path.home() / ".claude")) / "settings.json"

# What each hook answers, and when. A carry-forward reads the session as it opens
# and writes as it closes; the gate reads a prompt before it lands and records
# what a call covered after it ran.
WIRING = [
    ("SessionStart", None, "carryforward.py", "session-start"),
    ("SessionStart", None, "carryforward.py", "session-log 1"),
    ("SessionStart", None, "carryforward.py", "session-log 2"),
    ("SessionStart", None, "carryforward.py", "session-log 3"),
    ("SessionStart", None, "carryforward.py", "session-log 4"),
    ("SessionStart", None, "carryforward.py", "session-log 5"),
    ("UserPromptSubmit", None, "carryforward.py", "prompt"),
    ("UserPromptSubmit", None, "agentic-grounding-gate", "turn"),
    ("Stop", None, "carryforward.py", "stop"),
    ("PostToolUse", "Bash", "agentic-grounding-gate", "record"),
    ("SessionStart", None, "agentic-scratch", "start"),
    ("SessionEnd", None, "agentic-scratch", "end"),
]

TIMEOUT = 15

SKILLS = Path(os.environ.get("CLAUDE_CONFIG_DIR", Path.home() / ".claude")) / "skills"


def go_bin():
    """Where `go install` puts a binary: GOBIN, else the first GOPATH's bin."""
    done = subprocess.run(["go", "env", "GOBIN", "GOPATH"], capture_output=True, text=True,
                          check=False)
    gobin, gopath = (done.stdout.splitlines() + ["", ""])[:2]
    if gobin:
        return Path(gobin)

    return Path((gopath or str(Path.home() / "go")).split(os.pathsep)[0]) / "bin"


def command(script, mode):
    if script.endswith(".py"):
        return f'python3 "{HERE / "hooks" / script}" {mode}'

    return f'"{go_bin() / script}" {mode}'


# Matching on the script names rather than on this checkout's path is what makes
# a move survivable: an entry written before the checkout moved still names the
# same scripts, so it is found and replaced instead of standing dead.
SCRIPTS = ("carryforward.py", "grounding_gate.py", "scratch.py", "agentic-scratch",
           "agentic-grounding-gate")


def ours(entry):
    """Whether one hook entry runs a script this repository owns."""
    command = entry.get("command") or ""

    return any(script in command for script in SCRIPTS)


def without_ours(settings):
    """Every hook the person configured, minus the ones this checkout owns."""
    hooks = settings.get("hooks") or {}
    kept = {}
    for event, groups in hooks.items():
        standing = []
        for group in groups:
            rest = [held for held in (group.get("hooks") or []) if not ours(held)]
            if rest:
                standing.append({**group, "hooks": rest})
        if standing:
            kept[event] = standing
    return kept


def wired(hooks):
    """The hooks this checkout adds, folded into what is already there."""
    held = {event: [dict(group) for group in groups] for event, groups in hooks.items()}
    for event, matcher, script, mode in WIRING:
        entry = {"type": "command", "command": command(script, mode), "timeout": TIMEOUT}
        groups = held.setdefault(event, [])
        # One group per matcher, so an event with several matchers keeps them apart.
        for group in groups:
            if group.get("matcher") == matcher or (matcher is None and "matcher" not in group):
                group.setdefault("hooks", []).append(entry)
                break
        else:
            group = {"hooks": [entry]}
            if matcher is not None:
                group["matcher"] = matcher
            groups.append(group)
    return held


def link_skills(removing, showing):
    """Point the skills directory at this checkout's own, one link per skill.

    A link rather than a copy: an edit here reaches every project at once, and
    nothing drifts. A link already naming somewhere else is replaced, because a
    checkout that moved would otherwise leave every skill pointing at nothing.
    """
    ours = sorted(p for p in (HERE / "skills").iterdir() if p.is_dir()) \
        if (HERE / "skills").is_dir() else []
    acted = []
    for skill in ours:
        target = SKILLS / skill.name
        wanted = None if removing else skill
        held = os.readlink(target) if target.is_symlink() else None
        if held == (str(wanted) if wanted else None):
            continue
        if showing:
            acted.append(f"{'unlink' if removing else 'link'} {target}")
            continue
        if target.is_symlink() or target.exists():
            if not target.is_symlink():
                print(f"{target} is not a link; leaving it alone", file=sys.stderr)
                continue
            target.unlink()
        if not removing:
            SKILLS.mkdir(parents=True, exist_ok=True)
            target.symlink_to(skill)
        acted.append(f"{'unlinked' if removing else 'linked'} {target}")

    return acted


def main():
    removing = "--remove" in sys.argv[1:]
    showing = "--print" in sys.argv[1:]

    if not SETTINGS.exists():
        settings = {}
    else:
        try:
            settings = json.loads(SETTINGS.read_text())
        except ValueError as broken:
            print(f"{SETTINGS} is not readable as JSON: {broken}", file=sys.stderr)
            return 1

    hooks = without_ours(settings)
    if not removing:
        hooks = wired(hooks)
        for binary in sorted({script for _, _, script, _ in WIRING if not script.endswith(".py")}):
            if not (go_bin() / binary).is_file():
                print(f"{go_bin() / binary} is missing; run `go install -C source/hooks ./cmd/...` "
                      "from this checkout", file=sys.stderr)

    held = dict(settings)
    if hooks:
        held["hooks"] = hooks
    else:
        held.pop("hooks", None)

    if showing:
        print(json.dumps(held.get("hooks", {}), indent=2))
        for line in link_skills(removing, showing):
            print(line)
        return 0

    skills = link_skills(removing, showing)
    for line in skills:
        print(line)

    if held == settings:
        print(f"{SETTINGS} already says this")
        return 0

    if SETTINGS.exists():
        backup = SETTINGS.with_suffix(f".json.bak-{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}")
        shutil.copy2(SETTINGS, backup)
        print(f"copied the old settings to {backup}")

    SETTINGS.parent.mkdir(parents=True, exist_ok=True)
    SETTINGS.write_text(json.dumps(held, indent=2) + "\n")
    print(f"{'removed from' if removing else 'wired into'} {SETTINGS}")
    print("A session already running keeps the wiring it started with.")

    return 0


if __name__ == "__main__":
    sys.exit(main())
