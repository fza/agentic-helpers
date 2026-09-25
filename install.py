#!/usr/bin/env python3
"""Wire this checkout's hooks into Claude Code, or take them back out.

Claude Code reads one settings file per user. That file also carries credentials
and everything else a person configured, so this edits only the hook entries
naming this checkout and leaves every other key byte for byte as it found it. A
timestamped copy goes beside it before anything is written.

Running it twice changes nothing the second time: every entry running one of
these scripts is removed before the current set goes in, whichever directory it
named, so a moved or renamed checkout leaves nothing stale behind.

    install.py            wire the hooks in
    install.py --remove   take them out
    install.py --print    write nothing, show what would land
"""

import json
import os
import shutil
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
    ("UserPromptSubmit", None, "grounding_gate.py", "turn"),
    ("Stop", None, "carryforward.py", "stop"),
    ("PostToolUse", "Bash", "grounding_gate.py", "record"),
]

TIMEOUT = 15


def command(script, mode):
    return f'python3 "{HERE / "hooks" / script}" {mode}'


# Matching on the script names rather than on this checkout's path is what makes
# a move survivable: an entry written before the checkout moved still names the
# same scripts, so it is found and replaced instead of standing dead.
SCRIPTS = ("carryforward.py", "grounding_gate.py")


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

    held = dict(settings)
    if hooks:
        held["hooks"] = hooks
    else:
        held.pop("hooks", None)

    if showing:
        print(json.dumps(held.get("hooks", {}), indent=2))
        return 0

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
