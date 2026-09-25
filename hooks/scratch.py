#!/usr/bin/env python3
"""Keep a project's scratch directory from becoming a graveyard.

An agent writes probes, captures, half-finished drafts and one-off scripts. Sent
to the system temporary directory they are invisible to whoever is reviewing the
work; left at the root of the project's own `.tmp/` they pile up, and nothing
ever tells one throwaway apart from a draft that still matters.

So a session writes its throwaway under a directory named for itself, and that
directory goes when the session ends. Anything worth keeping is written
somewhere else under `.tmp/`, where nothing here can reach it.

    scratch.py end     remove this session's own directory
    scratch.py start   reap directories abandoned by sessions that crashed,
                       and report what the memory directory has let slip

**Only a directory named for a session is ever removed.** A name this cannot
parse as a session identifier is somebody's deliberate keep, and is left alone
whatever its age.
"""

import json
import os
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

# A session identifier as the client writes it. Matching this shape is the whole
# safety of the sweep: a directory named anything else is never removed.
SESSION = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

# How long a directory whose session never ended stays. Long enough that a
# session resumed the next morning still finds its own work.
ORPHAN_DAYS = 7

# Past this a carry-forward has stopped being a handover and become a knowledge
# store, which goes stale and then gets believed.
CARRY_LIMIT = 20 * 1024


def repo_root():
    """The main checkout, even for a session driven inside a worktree."""
    base = Path(os.environ.get("CLAUDE_PROJECT_DIR") or Path.cwd())
    done = subprocess.run(
        ["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
        capture_output=True, text=True, check=False, cwd=base,
    )
    if done.returncode == 0 and done.stdout.strip():
        return Path(done.stdout.strip()).parent

    return base


def scratch_dir():
    return repo_root() / ".tmp"


def ours():
    return scratch_dir() / "claude"


def enabled():
    """A project opts in by carrying a scratch directory. Nothing is created."""
    return scratch_dir().is_dir()


def session_of(payload):
    return str(payload.get("session_id") or "").strip()


def remove(directory):
    """Drop one directory, reporting nothing a caller cannot act on."""
    try:
        shutil.rmtree(directory)
        return True
    except OSError:
        return False


def command_end(payload):
    """Remove the directory this session wrote its throwaway into."""
    session = session_of(payload)
    if not SESSION.match(session):
        return 0

    held = ours() / session
    if held.is_dir() and remove(held):
        print(f"swept {held}")

    return 0


def reap():
    """Every session directory old enough that nobody is still writing it."""
    if not ours().is_dir():
        return []

    stale = time.time() - ORPHAN_DAYS * 86400
    gone = []
    for held in sorted(ours().iterdir()):
        if not held.is_dir() or not SESSION.match(held.name):
            continue
        if held.stat().st_mtime < stale and remove(held):
            gone.append(held.name)

    return gone


def slipped():
    """What the memory directory has let slip, in the words a reader acts on."""
    memory = repo_root() / ".memory"
    if not memory.is_dir():
        return []

    found = []
    carry = memory / "carryforward"
    if carry.is_dir():
        for held in sorted(carry.glob("*-memory.md")):
            size = held.stat().st_size
            if size > CARRY_LIMIT:
                found.append(f"{held.name} is {size // 1024} KB, past the {CARRY_LIMIT // 1024} KB "
                             "a carry-forward holds. Anything the repository can rebuild goes.")

    index = memory / "MEMORY.md"
    if index.is_file():
        for line in index.read_text(encoding="utf-8", errors="replace").splitlines():
            for target in re.findall(r"\]\(([^)]+)\)", line):
                if target.startswith(("http://", "https://")):
                    continue
                if not (memory / target).exists():
                    found.append(f"MEMORY.md points at {target}, which is not there.")

    return found


def command_start(payload):
    """Reap what crashed sessions left, and say what memory has let slip."""
    said = []
    gone = reap()
    if gone:
        said.append(f"Scratch: reaped {len(gone)} session directory(s) nobody came back to.")
    said.extend(slipped())
    if said:
        print("\n".join(said))

    return 0


MODES = {"end": command_end, "start": command_start}


def main():
    if len(sys.argv) < 2 or sys.argv[1] not in MODES:
        print(f"usage: {sys.argv[0]} {'|'.join(MODES)}", file=sys.stderr)
        return 1

    if not enabled():
        return 0

    try:
        payload = json.load(sys.stdin)
    except ValueError:
        payload = {}
    if not isinstance(payload, dict):
        payload = {}

    return MODES[sys.argv[1]](payload)


if __name__ == "__main__":
    sys.exit(main())
