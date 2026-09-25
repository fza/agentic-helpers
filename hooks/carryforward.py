#!/usr/bin/env python3
"""Keep each role's carry-forward file current across a compaction.

Subcommands map to hook events: session-start, prompt, stop. The claim, release,
take and refreshed subcommands are driven from the conversation.

A role is a named seat at the work, held by one session at a time: `drive` runs
the lanes, `finalize` merges and records what landed, `validate` grounds an entry
and clears or holds it, `insource` works what waits on the owner, `idea` refines
what is parked. Each seat carries its own lock, its
own carry-forward file and its own nudge ladder, so four sessions coordinate
through files rather than through whoever claimed first.
"""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

CONTEXT_PERCENT_THRESHOLD = 65
TRANSCRIPT_DELTA_THRESHOLD = 400 * 1024
SENSOR_MAX_AGE_SECONDS = 300
NUDGES_BEFORE_BACKSTOP = 2
REARM_PERCENT_MARGIN = 10
ORPHAN_MAX_AGE_SECONDS = 48 * 3600
# Claude Code inlines a hook's output only up to 10,000 characters and swaps
# anything longer for a file path with a short preview, which a compaction
# summary then outranks. So the log arrives in pieces under that limit, one per
# `session-log` hook, and only its most recent part does.
DELTA_LOG_PIECE_CHARS = 9_000
DELTA_LOG_PIECES = 5

# The seats this workflow names. A role outside them is legal: the set is what a
# usage line suggests, never what a claim is held to, so a session can open a
# seat nobody has needed before without the machinery changing.
DEFAULT_ROLES = ("drive", "finalize", "validate", "insource", "idea")

# A role name is a path segment and a file-name stem, so it stays lower case,
# hyphen-joined and short enough to read in a directory listing.
ROLE_PATTERN = re.compile(r"^[a-z][a-z0-9-]{0,23}$")

# What a carry-forward keeps, repeated in every nudge because a seat reads the
# nudge and not the rules file. Workflow state is left out: the board, the
# transcripts, `plan.py agents` and the seat's skill give it back after a
# compaction, and a copy of it here goes stale and then gets believed.
# Every carry-forward is read by an agent, so it is written in the terse register
# every agent-facing text uses.
ULTRA = "Write it caveman ultra: no articles/filler, fragments + arrows; ids, paths, commands exact."

KEEP_RULE = (
    "Keep only what nothing else gives back: fdbox findings (code, tests, tools, env) "
    "not yet on the board or in the graph, + owner decisions not yet captured. Workflow "
    "state out (where work stopped, agents, claims, rows, seat rules): board, transcripts, "
    "`plan.py agents` + skill rebuild it. Amend where something new qualifies, and "
    "leave it untouched where nothing does. " + ULTRA
)

GENERAL_RULE = (
    "Settled since last refresh → `## Uncaptured decisions`. Amend where something "
    "changed, leave untouched where nothing did. " + ULTRA
)

GENERAL_NUDGE_TEXT = (
    "Context near compaction. Fold `.memory/transcripts/{session}.md` into your "
    "carry-forward `.memory/carryforward/{role}-memory.md`, then "
    "`python3 .claude/hooks/carryforward.py refreshed`. " + GENERAL_RULE + " No graph "
    "capture, no other `.memory/` file, no handoff prompt."
)

GENERAL_BACKSTOP_TEXT = (
    "Carry-forward overdue, two nudges unanswered. Fold "
    "`.memory/transcripts/{session}.md` into `.memory/carryforward/{role}-memory.md` now, "
    "then `python3 .claude/hooks/carryforward.py refreshed` before ending the turn. "
    + GENERAL_RULE
)


# The seats working the board. Their workflow state lives on the board, so their
# carry-forward keeps project findings alone. Every other seat, `main` and
# `idea` among them, carries its own work in the file, which nothing rebuilds.
PLAN_SEATS = frozenset({"autopilot", "drive", "validate", "finalize"})


def nudge_text(role: str) -> str:
    return NUDGE_TEXT if role in PLAN_SEATS else GENERAL_NUDGE_TEXT


def backstop_text(role: str) -> str:
    return BACKSTOP_TEXT if role in PLAN_SEATS else GENERAL_BACKSTOP_TEXT


NUDGE_TEXT = (
    "Context near compaction. Read `.memory/transcripts/{session}.md`, bring your "
    "carry-forward `.memory/carryforward/{role}-memory.md` up to date, then "
    "`python3 .claude/hooks/carryforward.py refreshed`. " + KEEP_RULE + " No graph "
    "capture, no other `.memory/` file, no handoff prompt."
)

BACKSTOP_TEXT = (
    "Carry-forward overdue, two nudges unanswered. Bring "
    "`.memory/carryforward/{role}-memory.md` up to date from "
    "`.memory/transcripts/{session}.md` now, then "
    "`python3 .claude/hooks/carryforward.py refreshed` before ending the turn. " + KEEP_RULE
)


def repo_root() -> Path:
    """The main checkout, even for a session driven inside a worktree.

    A worktree carries no `.memory/`, because git ignores that path and nothing
    copies it, so a session there has to reach the one directory every role
    shares. The common git directory names it whatever the session's own working
    directory is.
    """
    configured = os.environ.get("CLAUDE_PROJECT_DIR")
    base = Path(configured) if configured else Path.cwd()
    result = subprocess.run(
        ["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
        capture_output=True, text=True, check=False, cwd=base,
    )
    if result.returncode == 0 and result.stdout.strip():
        return Path(result.stdout.strip()).parent
    return base


def memory_dir() -> Path:
    return repo_root() / ".memory"


def memory_enabled() -> bool:
    """A project opts in by carrying a .memory directory. Nothing is created."""
    return memory_dir().is_dir()


def transcripts_dir() -> Path:
    return memory_dir() / "transcripts"


def roles_dir() -> Path:
    return memory_dir() / "roles"


def budget_path(role: str) -> Path:
    """How many turns this seat has carried on unattended, kept by the seat gate."""
    return roles_dir() / f"{role}.autopilot"


def sessions_path() -> Path:
    """Which session each running Claude process is, recorded at session start.

    A session cannot name its own id from inside a command, so the one place it
    arrives — the session-start payload — writes it down against the process it
    belongs to, and a later claim reads it back.
    """
    return memory_dir() / "sessions.json"


def remember_session(session: str) -> None:
    pid = claude_pid()
    known = read_json(sessions_path())
    known[str(pid)] = session
    known = {
        held: name for held, name in known.items()
        if held == str(pid) or process_is_owner(int(held))
    }
    write_atomic(sessions_path(), json.dumps(known, indent=2, sort_keys=True) + "\n")


def session_of_this_process() -> str:
    return read_json(sessions_path()).get(str(claude_pid()), "")


def lock_path(role: str) -> Path:
    return roles_dir() / f"{role}.json"


def tell_the_tool(*args: str) -> None:
    """Register the same seat with sddap, so one machine answers once.

    This file keeps what only it needs — the session id, the nudge ladder, the
    transcript offsets. Who holds a seat is asked by other sessions and by the
    screens, so it is recorded where they read it. A tool that is not installed
    changes nothing here.
    """
    try:
        subprocess.run(
            ["sddap", *args],
            cwd=repo_root(), capture_output=True, text=True, timeout=10, check=False,
        )
    except (OSError, subprocess.SubprocessError):
        pass


def carryforward_dir() -> Path:
    """Where the seats' carry-forward files live, apart from everything else.

    `.memory/` also holds what no seat owns — the grants, the seat prompts, the
    parked work — and a wipe that walks the directory has to tell the two apart
    at a glance.
    """
    return memory_dir() / "carryforward"


def memory_file(role: str) -> Path:
    return carryforward_dir() / f"{role}-memory.md"


def valid_role(role: str) -> bool:
    return bool(role) and bool(ROLE_PATTERN.match(role))


def role_usage() -> str:
    """The seats worth suggesting: the named ones, plus whatever is held now."""
    return "|".join(sorted(set(DEFAULT_ROLES) | set(role_holders())))


def role_holders() -> dict[str, dict]:
    """Every seat a lock file claims, whether its holder still runs or not."""
    if not roles_dir().is_dir():
        return {}
    held = {}
    for path in sorted(roles_dir().glob("*.json")):
        record = read_json(path)
        if record.get("session_id"):
            held[path.stem] = record
    return held


def role_of_session(session: str) -> str | None:
    for role, record in role_holders().items():
        if record.get("session_id") == session:
            return role
    return None


def role_of_this_process() -> str | None:
    pid = claude_pid()
    for role, record in role_holders().items():
        if int(record.get("pid", 0) or 0) == pid:
            return role
    return None


def read_hook_input() -> dict:
    try:
        return json.loads(sys.stdin.read() or "{}")
    except json.JSONDecodeError:
        return {}


def write_atomic(path: Path, text: str) -> None:
    temporary = path.with_suffix(path.suffix + f".tmp{os.getpid()}")
    temporary.write_text(text, encoding="utf-8")
    temporary.replace(path)


def read_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}


def owner_process_name() -> str:
    return os.environ.get("CARRYFORWARD_PROCESS_NAME", "claude")


def process_start_time(pid: int) -> str:
    result = subprocess.run(
        ["ps", "-p", str(pid), "-o", "lstart="],
        capture_output=True, text=True, check=False,
    )
    return result.stdout.strip()


def process_parent_and_name(pid: int) -> tuple[int, str]:
    result = subprocess.run(
        ["ps", "-p", str(pid), "-o", "ppid=,comm="],
        capture_output=True, text=True, check=False,
    )
    fields = result.stdout.strip().split(None, 1)
    if len(fields) != 2:
        return 0, ""
    return int(fields[0]), Path(fields[1].strip()).name


def process_is_owner(pid: int) -> bool:
    return process_parent_and_name(pid)[1] == owner_process_name()


def owner_is_alive(pid: int, started: str) -> bool:
    if pid <= 0 or not process_is_owner(pid):
        return False
    return not started or process_start_time(pid) == started


def claude_pid() -> int:
    """The nearest ancestor running the owner process.

    A hook command runs through a shell, so the immediate parent is not reliably
    the Claude process.
    """
    name = owner_process_name()
    pid = os.getpid()
    for _ in range(12):
        parent, _ = process_parent_and_name(pid)
        if parent <= 1:
            break
        _, parent_name = process_parent_and_name(parent)
        if parent_name == name:
            return parent
        pid = parent
    return os.getppid()


def transcript_for(session: str) -> Path | None:
    """The harness transcript, found from the project path Claude Code encodes.

    A seat driven inside a worktree is encoded under that worktree's own path
    rather than the main checkout's, so the directory named by this checkout is
    the first guess and every sibling project directory is the fallback.
    """
    projects = Path.home() / ".claude" / "projects"
    slug = str(repo_root()).replace("/", "-")
    candidate = projects / slug / f"{session}.jsonl"
    if candidate.is_file():
        return candidate
    for sibling in sorted(projects.glob(f"{slug}*/{session}.jsonl")):
        if sibling.is_file():
            return sibling
    return None


def state_path(session: str) -> Path:
    return transcripts_dir() / f"{session}.state"


def load_state(session: str) -> dict:
    state = read_json(state_path(session))
    state.setdefault("transcript_offset_at_refresh", 0)
    state.setdefault("log_offset", 0)
    state.setdefault("nudges", 0)
    state.setdefault("percent_rung", None)
    state.setdefault("turns", 0)
    return state


def save_state(session: str, state: dict) -> None:
    transcripts_dir().mkdir(exist_ok=True)
    write_atomic(state_path(session), json.dumps(state, indent=2) + "\n")


def context_percent(session: str) -> float | None:
    sensor = read_json(transcripts_dir() / f"{session}.ctx")
    recorded_at = sensor.get("at", 0)
    if not recorded_at or time.time() - recorded_at > SENSOR_MAX_AGE_SECONDS:
        return None
    percent = sensor.get("used_percentage")
    if percent is None:
        return None

    # The reading is a share of the model's whole window, while a seat compacts
    # at its own, smaller one. Every rung here is a share of the window the
    # session actually compacts at, so a seat compacting at 200k is nudged before
    # that compaction rather than at a rung its context never reaches.
    size = sensor.get("size") or 0
    window = compact_window()
    if size and window and window < size:
        return float(percent) * size / window

    return float(percent)


def compact_window() -> int | None:
    try:
        return int(os.environ.get("CLAUDE_CODE_AUTO_COMPACT_WINDOW", ""))
    except ValueError:
        return None


def threshold_crossed(session: str, transcript: Path, state: dict) -> bool:
    percent = context_percent(session)
    if percent is not None:
        # A refresh does not shrink the context, so the percentage stays over the
        # threshold afterwards and the trigger re-arms one fixed rung at a time.
        # The rung is never the reading the nudge landed on: a nudge arrives only
        # at a prompt, and a long turn can carry the context twenty points past
        # the rung it answered, which would push every later rung out of reach.
        rung = state.get("percent_rung") or CONTEXT_PERCENT_THRESHOLD
        return percent >= rung
    try:
        size = transcript.stat().st_size
    except OSError:
        return False
    return size - state["transcript_offset_at_refresh"] >= TRANSCRIPT_DELTA_THRESHOLD


def summarize_tool(name: str, tool_input: dict) -> str:
    for key in ("file_path", "path", "notebook_path", "pattern", "command", "url"):
        value = tool_input.get(key)
        if isinstance(value, str) and value:
            short = value.replace(str(repo_root()) + "/", "")
            if len(short) > 80:
                short = short[:77] + "..."
            return f"{name} {short}"
    return name


def read_new_records(transcript: Path, offset: int) -> tuple[list[dict], int]:
    try:
        with transcript.open("rb") as handle:
            handle.seek(offset)
            raw = handle.read()
            end = handle.tell()
    except OSError:
        return [], offset
    records = []
    for line in raw.decode("utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return records, end


def build_turn_block(records: list[dict], number: int) -> str | None:
    asked, replied, did = [], [], []
    for record in records:
        kind = record.get("type")
        if kind not in ("user", "assistant"):
            continue
        content = record.get("message", {}).get("content")
        if isinstance(content, str):
            (asked if kind == "user" else replied).append(content)
            continue
        if not isinstance(content, list):
            continue
        for block in content:
            block_type = block.get("type")
            if block_type == "text":
                text = block.get("text", "").strip()
                if text:
                    (asked if kind == "user" else replied).append(text)
            elif block_type == "tool_use":
                did.append(summarize_tool(block.get("name", "?"), block.get("input", {}) or {}))
    if not (asked or replied or did):
        return None
    stamp = time.strftime("%Y-%m-%d %H:%M")
    lines = [f"## turn {number}  {stamp}", ""]
    if asked:
        lines += ["### asked", "", "\n\n".join(asked).strip(), ""]
    if replied:
        lines += ["### replied", "", "\n\n".join(replied).strip(), ""]
    if did:
        seen, ordered = set(), []
        for entry in did:
            if entry not in seen:
                seen.add(entry)
                ordered.append(entry)
        lines += ["### did", "", " · ".join(ordered), ""]
    return "\n".join(lines) + "\n"


def append_turn(session: str, transcript: Path, state: dict) -> None:
    records, end = read_new_records(transcript, state["log_offset"])
    if end == state["log_offset"]:
        return
    state["log_offset"] = end
    state["turns"] += 1
    block = build_turn_block(records, state["turns"])
    if block is None:
        state["turns"] -= 1
        return
    transcripts_dir().mkdir(exist_ok=True)
    with (transcripts_dir() / f"{session}.md").open("a", encoding="utf-8") as handle:
        handle.write(block)


def sweep() -> list[str]:
    directory = transcripts_dir()
    if not directory.is_dir():
        return []
    live = set()
    for role, record in role_holders().items():
        if owner_is_alive(int(record.get("pid", 0) or 0), record.get("started", "")):
            live.add(record.get("session_id"))
        else:
            lock_path(role).unlink(missing_ok=True)
    report, now = [], time.time()
    sessions = {path.name.split(".")[0] for path in directory.iterdir() if path.is_file()}
    for session in sorted(sessions):
        if session in live:
            continue
        files = sorted(directory.glob(f"{session}.*"))
        if not files:
            continue
        age = now - max(path.stat().st_mtime for path in files)
        size = sum(path.stat().st_size for path in files)
        log = directory / f"{session}.md"
        unfolded = log.exists() and log.stat().st_size > 0
        if age > ORPHAN_MAX_AGE_SECONDS:
            for path in files:
                path.unlink(missing_ok=True)
            report.append(f"  {session[:8]}  {age / 3600:.0f}h  {size // 1024} KB  deleted")
        else:
            note = "log holds unfolded turns" if unfolded else "log empty"
            report.append(f"  {session[:8]}  {age / 3600:.0f}h  {size // 1024} KB  {note}")
    return report


def delta_log_pieces(session: str) -> list[str]:
    """The turns since the last refresh, newest last, as pieces a hook can inline.

    A compaction summary tells the session what to do next, so an instruction to
    go and open a file competes with it and loses. Content cannot be skipped, as
    long as each piece stays under the limit Claude Code inlines.
    """
    log = transcripts_dir() / f"{session}.md"
    try:
        text = log.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return []
    if not text.strip():
        return []
    budget = DELTA_LOG_PIECE_CHARS * DELTA_LOG_PIECES - 1_000
    kept = text
    if len(text) > budget:
        kept = text[-budget:]
        kept = kept[kept.find("\n") + 1:] if "\n" in kept else kept
    pieces, current = [], ""
    for line in kept.splitlines(keepends=True):
        while len(line) > DELTA_LOG_PIECE_CHARS:
            if current:
                pieces.append(current)
                current = ""
            pieces.append(line[:DELTA_LOG_PIECE_CHARS])
            line = line[DELTA_LOG_PIECE_CHARS:]
        if current and len(current) + len(line) > DELTA_LOG_PIECE_CHARS:
            pieces.append(current)
            current = ""
        current += line
    if current:
        pieces.append(current)
    pieces = pieces[-DELTA_LOG_PIECES:]
    withheld = len(text) - sum(len(piece) for piece in pieces)
    older = (f" The {withheld} earlier characters are in that file and nowhere else; read them "
             "there before relying on anything they cover.") if withheld else ""
    header = ("## Turns since the last carry-forward refresh\n\n"
              f"Held in `.memory/transcripts/{session}.md`, which only a refresh truncates. "
              f"{len(pieces)} pieces follow, oldest first.{older}\n\n")

    return [(header if index == 0 else f"## Turns since the last refresh, piece {index + 1}\n\n")
            + piece for index, piece in enumerate(pieces)]


def command_session_log() -> int:
    """Print one piece of the delta log after a compaction; one hook runs per piece."""
    if not memory_enabled():
        return 0
    payload = read_hook_input()
    if payload.get("source") != "compact":
        return 0
    session = payload.get("session_id", "")
    if not session or not role_of_session(session):
        return 0
    try:
        index = int(sys.argv[2])
    except (IndexError, ValueError):
        print("usage: carryforward.py session-log <piece number, from 1>", file=sys.stderr)
        return 1
    pieces = delta_log_pieces(session)
    if 1 <= index <= len(pieces):
        print(pieces[index - 1])
    return 0


def command_session_start() -> int:
    if not memory_enabled():
        return 0
    payload = read_hook_input()
    session = payload.get("session_id", "")
    source = payload.get("source", "")
    if session:
        remember_session(session)
    output = []
    role = role_of_session(session)
    if role:
        lock = read_json(lock_path(role))
        pid = claude_pid()
        lock.update({"pid": pid, "started": process_start_time(pid)})
        write_atomic(lock_path(role), json.dumps(lock, indent=2) + "\n")
        rows = sweep()
        if rows:
            output.append("Sweep of .memory/transcripts/:")
            output.extend(rows)
        if source == "compact":
            # The window is fresh, so the ladder starts at the threshold again.
            state = load_state(session)
            state["percent_rung"] = None
            save_state(session, state)
            prompt = repo_root() / ".claude" / "settings.local.md"
            if prompt.exists():
                output.append(prompt.read_text(encoding="utf-8"))
    elif source in ("startup", "resume", "clear"):
        held = ", ".join(
            f"{name} ({record.get('session_id', '?')[:8]})"
            for name, record in sorted(role_holders().items())
        )
        output.append(
            "This session holds no role and writes no memory file. Read "
            "`.memory/carryforward/main-memory.md` for context, and any other file "
            "under `.memory/carryforward/` "
            "for a seat's own carry-forward. Claim one with "
            f"`carryforward.py claim <role> <session-id>` (seats: {role_usage()}). "
            f"Seats held: {held if held else 'none'}."
        )
    if output:
        print("\n".join(output))
    return 0


def command_prompt() -> int:
    if not memory_enabled():
        return 0
    payload = read_hook_input()
    session = payload.get("session_id", "")
    role = role_of_session(session) or role_of_this_process()
    if not role:
        return 0
    # The owner spoke, so the seat's unattended stretch starts again. The seat
    # gate's resting line promises exactly this. It lives here because this hook
    # already runs on every prompt in every session, so the promise holds from
    # the moment the file changes rather than from each session's next start.
    try:
        budget_path(role).unlink()
    except OSError:
        pass
    state = load_state(session)
    if threshold_crossed(session, Path(payload.get("transcript_path", "")), state):
        state["nudges"] += 1
        save_state(session, state)
        print(nudge_text(role).format(session=session, role=role))
    return 0


def command_stop() -> int:
    if not memory_enabled():
        return 0
    payload = read_hook_input()
    session = payload.get("session_id", "")
    role = role_of_session(session)
    if not role:
        return 0
    transcript = Path(payload.get("transcript_path", ""))
    state = load_state(session)
    append_turn(session, transcript, state)
    overdue = (
        threshold_crossed(session, transcript, state)
        and state["nudges"] >= NUDGES_BEFORE_BACKSTOP
    )
    save_state(session, state)
    if overdue:
        print(backstop_text(role).format(session=session, role=role), file=sys.stderr)
        return 2
    return 0


def command_claim(force: bool) -> int:
    if not memory_enabled():
        print(f"no {memory_dir()} directory; this project has not opted in", file=sys.stderr)
        return 1
    role = sys.argv[2] if len(sys.argv) > 2 else ""
    given = sys.argv[3] if len(sys.argv) > 3 else ""
    known = session_of_this_process()
    # The id this process recorded at start outranks one typed on the command
    # line. A session reading ids out of the sweep, or out of another seat's
    # files, takes one that is not its own, and every lookup by session then
    # names the wrong seat. A prefix of its own id is the same session, short.
    if given and known and not known.startswith(given):
        print(
            f"this process is session {known[:8]}, not {given[:8]}; "
            f"claim it with no id: carryforward.py claim {role}",
            file=sys.stderr,
        )
        return 1
    session = known or given
    if not valid_role(role) or not session:
        print(
            f"usage: carryforward.py claim|take <role> [session-id]  "
            f"(seats: {role_usage()}; a new name is fine, lower case and hyphens). "
            "The id is only needed where this session started before the machinery "
            "recorded it.",
            file=sys.stderr,
        )
        return 1
    lock = read_json(lock_path(role))
    pid = claude_pid()
    if lock and not force and owner_is_alive(int(lock.get("pid", 0) or 0), lock.get("started", "")):
        print(
            f"{role} is held by {lock.get('session_id', '?')[:8]} "
            f"(pid {lock.get('pid')}, alive). Release it there, or say `take {role}`.",
            file=sys.stderr,
        )
        return 1
    seated = role_of_session(session)
    if seated and seated != role:
        print(
            f"this session already holds {seated}; release that seat before taking {role}",
            file=sys.stderr,
        )
        return 1
    record = {
        "session_id": session,
        "pid": pid,
        "started": process_start_time(pid),
        "claimed": time.strftime("%Y-%m-%dT%H:%M:%S"),
    }
    if force and lock:
        record["seized_from"] = lock.get("session_id")
    roles_dir().mkdir(exist_ok=True)
    carryforward_dir().mkdir(exist_ok=True)
    write_atomic(lock_path(role), json.dumps(record, indent=2) + "\n")
    tell_the_tool("seat", "take", role, "--session", session, "--pid", str(pid))
    transcripts_dir().mkdir(exist_ok=True)
    state = load_state(session)
    if not state_path(session).exists():
        transcript = transcript_for(session)
        start = transcript.stat().st_size if transcript else 0
        state["log_offset"] = start
        state["transcript_offset_at_refresh"] = start
        save_state(session, state)
    print(
        f"{role}: {session[:8]} (pid {pid}), carry-forward "
        f"carryforward/{memory_file(role).name}, log starts at "
        f"{state['log_offset']} bytes"
    )
    return 0


def command_release() -> int:
    role = sys.argv[2] if len(sys.argv) > 2 else role_of_this_process()
    if not valid_role(role):
        print(f"usage: carryforward.py release <{role_usage()}>", file=sys.stderr)
        return 1
    lock_path(role).unlink(missing_ok=True)
    tell_the_tool("seat", "release", role)
    print(f"{role} released")
    return 0


def command_refreshed() -> int:
    role = sys.argv[2] if len(sys.argv) > 2 else role_of_this_process()
    if not valid_role(role):
        print(
            f"usage: carryforward.py refreshed [{role_usage()}] — no seat matches "
            "this process, so name the role",
            file=sys.stderr,
        )
        return 1
    session = read_json(lock_path(role)).get("session_id")
    if not session:
        print(f"no session holds {role}", file=sys.stderr)
        return 1
    state = load_state(session)
    (transcripts_dir() / f"{session}.md").unlink(missing_ok=True)
    state["transcript_offset_at_refresh"] = state["log_offset"]
    # Climb to the lowest rung above where the context actually stands: a rung
    # already passed can never fire again inside this window. A compaction opens
    # a fresh window and resets the ladder to the threshold.
    rung = state.get("percent_rung") or CONTEXT_PERCENT_THRESHOLD
    percent = context_percent(session)
    rung += REARM_PERCENT_MARGIN
    while percent is not None and rung <= percent:
        rung += REARM_PERCENT_MARGIN
    state["percent_rung"] = rung
    state["nudges"] = 0
    save_state(session, state)
    print(f"{role} carry-forward recorded; delta log truncated")
    return 0


def command_sweep() -> int:
    if not memory_enabled():
        print(f"no {memory_dir()} directory; this project has not opted in", file=sys.stderr)
        return 1
    rows = sweep()
    print("\n".join(rows) if rows else "nothing to sweep")
    return 0


def command_holder() -> int:
    """Exit 0 and name the holder where a live session holds the seat, else exit 1.

    `seat-up.sh` asks before opening a seat, because a second session on one seat
    starts its work a second time.
    """
    role = sys.argv[2] if len(sys.argv) > 2 else ""
    lock = read_json(lock_path(role)) if role else {}
    pid = int(lock.get("pid", 0) or 0)
    if pid and owner_is_alive(pid, lock.get("started", "")):
        print(f"{role} is held by session {str(lock.get('session_id', '?'))[:8]} (pid {pid})")
        return 0
    return 1


COMMANDS = {
    "session-start": command_session_start,
    "session-log": command_session_log,
    "holder": command_holder,
    "prompt": command_prompt,
    "stop": command_stop,
    "claim": lambda: command_claim(force=False),
    "take": lambda: command_claim(force=True),
    "release": command_release,
    "refreshed": command_refreshed,
    "sweep": command_sweep,
}


def main() -> int:
    if len(sys.argv) < 2 or sys.argv[1] not in COMMANDS:
        print(f"usage: carryforward.py {'|'.join(COMMANDS)}", file=sys.stderr)
        return 1
    return COMMANDS[sys.argv[1]]()


if __name__ == "__main__":
    sys.exit(main())
