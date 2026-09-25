#!/usr/bin/env python3
"""Refuse a decision or a draft edit until the area listing behind it has been read.

`AGENTS.md` requires reading an area before touching its work, and
`CLAUDE.local.md` requires searching the graph for a decision that already owns a
subject. Both were reminders, and a reminder is skippable. This is not: it
refuses the tool call.

The failure it exists to stop is narrow and repeats. One semantic search comes
back empty, the emptiness reads as absence, and a subject an active decision
already owns gets presented as an open fork. A semantic query matches wording
rather than subject, so it misses whenever the entry says the same thing in
other words. The area listing cannot miss that way: it enumerates membership.

So the gate demands both, per turn: a search, and at least one area listing.
`sdd show` alone never satisfies it, because following references only reaches
entries something already cited, which is the closed loop that produced the
failure.

It refuses a capture whose draft records a gap. A gap is a finding, and a
finding gets settled: repaired, filed against the row that owns the repair, or
put to the owner. Capturing one writes the problem into the graph and calls that
progress, which leaves the thing as it was and hands the next reader a record
where a repair belongs. The graph is append-only, so a gap captured by mistake
stays forever.

It refuses the tool reached without `scripts/graph.py` in front of it. That
wrapper records what each read returned and holds the capture gate open or shut
on the answer, so a read running around it grounds nothing that any later gate
can see.

It also refuses a graph read that throws a stream away. `--terms` is not a flag,
and the tool answers a wrong flag by printing usage to standard error and
exiting non-zero. A command discarding that stream turns the refusal into an
empty result set, and an empty result set reads as an answer: nothing in the
graph owns this subject. Every conclusion drawn after that is built on a typo.

Modes:
  record  PostToolUse on Bash - notes which graph reads a command performed
  turn    UserPromptSubmit - starts a fresh turn, so evidence never carries over
  gate    PreToolUse - refuses a read that skips the wrapper or discards a
          stream, and refuses AskUserQuestion and Write/Edit under a drafts
          directory until this turn carries both kinds of read
"""

import contextlib
import json
import os
import re
import sys

LEDGER_DIR = os.environ.get("GRAPH_LEDGER_DIR") or os.path.join(
    os.environ.get("CLAUDE_PROJECT_DIR", "."), ".sdd", "autopilot", "turns")

# A listing of one area's whole membership. The one read that cannot report a
# false absence, because it enumerates rather than matching words.
AREA_LISTING = re.compile(r"(?:sdd|graph\.py)\s+view\b[^\n]*topic\(\s*area-")

# Either search mode counts. The literal one finds an identifier a semantic
# query cannot, and the semantic one finds a subject nobody spelled the same way.
SEARCH = re.compile(r"(?:sdd|graph\.py)\s+search\b")

# A `view` reaching the tool at all. What it asked for lives inside a quoted
# argument, so the call is proven on the shell-stripped command and the layout
# read from the raw one.
VIEW = re.compile(r"(?:sdd|graph\.py)\s+view\b")

# The two search modes answer different questions, and one alone misses what the
# other finds. A semantic query matches how somebody phrased a subject, so an
# entry saying the same thing in other words stays invisible to it. A literal
# term matches the token itself - a command name, a package shape, a flag - and
# finds the entry whose wording nobody guessed. A decision already owning the
# subject is exactly what a phrasing mismatch hides.
SEMANTIC = re.compile(r"(?:sdd|graph\.py)\s+search\b[^\n]*--query\b")
LITERAL = re.compile(r"(?:sdd|graph\.py)\s+search\b[^\n]*--term\b")

# Where a draft entry lives. Editing one commits a claim about the graph.
DRAFT_PATH = re.compile(r"[/\\]drafts?[/\\]")

GATED_TOOLS = {"AskUserQuestion", "Write", "Edit", "NotebookEdit"}

# An `sdd` invocation, rather than the `.sdd/` directory or the word as an
# argument to something else. It has to sit where a command word sits: opening
# the line, following a separator, after the `--` that ends a runner's flags, or
# after a runner that hands the rest of the line to another program. A path
# spelling reaches the same binary, so `/opt/homebrew/bin/sdd` counts as the tool.
# A `(` counts as a separator only where nothing word-like precedes it, so a
# subshell matches and a search pattern such as `Bash(sdd new` does not.
RUNNER = (r"env(?:\s+[A-Za-z_]\w*=\S*)*|command|exec|nohup|time|sudo"
          r"|xargs(?:\s+-\S+)*|stdbuf(?:\s+-\S+)*|caffeinate(?:\s+-\S+)*")

AT_COMMAND = rf"(?:^|[\n;|&]\s*|(?<![\w/.-])\(\s*|--\s+|\b(?:{RUNNER})\s+)"

TOOL = AT_COMMAND + r"(?:\S*/)?sdd\s+[a-z]"

SDD_CALL = re.compile(TOOL + r"|graph\.py\s+[a-z]")

# The tool reached without the wrapper. Nothing records such a read, so the
# grounding it performed counts toward no subject and the capture gate cannot
# see it.
BARE_SDD = re.compile(TOOL)

# A project may put a wrapper in front of the tool, so that every read is
# recorded against the subject it served. Only a project carrying one is held to
# it: elsewhere the bare tool is the normal way to reach the graph, and demanding
# a wrapper that does not exist refuses every legitimate read.
WRAPPER = os.path.join("scripts", "graph.py")


def wrapper():
    """The wrapper this project puts in front of the tool, or None."""
    root = graph_dir()
    if not root:
        return None
    held = os.path.join(os.path.dirname(root), WRAPPER)

    return WRAPPER if os.path.isfile(held) else None


def tool():
    """How a suggested command reaches the graph in this project."""
    held = wrapper()

    return f"python3 {held}" if held else "sdd"

# A shell handed a string to run. What that string holds is a command rather
# than an argument, so it is read as one. Only a shell qualifies: `grep -c` takes
# a `-c` of its own and counts lines.
SHELL_C = re.compile(
    r"(?:^|[\s;|&(])(?:ba|z|da|k)?sh\s+(?:-[A-Za-z]+\s+)*-c\s+('[^']*'|\"[^\"]*\"|\S+)"
)

# The tool reached under another name. A variable holding it, or a lookup
# resolving it, spells the same binary while the call itself reads as `$S`.
ALIASED = re.compile(
    r"\b[A-Za-z_]\w*=['\"]?(?:\S*/)?sdd['\"]?(?=[\s;&|)]|$)"
    r"|(?:\$\(|`)\s*(?:command\s+-v|which|type\s+-p)\s+sdd\s*(?:\)|`)"
)

# Every shape that sends a stream to the null device, including the forms that
# reach it through a duplication rather than by naming it.
TO_NULL = re.compile(r"(?:&>>?|(?:[12]?>>?)(?:&\s*[12])?)\s*\|?\s*/dev/null|>\s*&\s*/dev/null")

# Reading one entry with its downstream chain suppressed. A body is immutable, so
# a name a later entry renamed still reads as current in the entry that coined it.
# Only the downstream chain carries the refinement saying so.
SHOW = re.compile(r"(?:sdd|graph\.py)\s+show\b[^\n|;&]*")
DEPTH = {"down": re.compile(r"--down[=\s]+(\d+)\b"),
         "up": re.compile(r"--up[=\s]+(\d+)\b")}

# How far a grounding read reaches, in each direction. A body is immutable, so a
# surface it names keeps that spelling after a later entry renamed it: the rename
# lives in the downstream chain and nowhere else. Two reaches a refinement of a
# refinement. One upstream shows what the entry was answering, which is what says
# whether it still applies.
REACH = {"down": 2, "up": 1}

# A rules entry in the process layer is the documented exception: its downstream
# chain is every entry ever captured under it, and reading that settles nothing.
ENTRY_ID = re.compile(r"\b\d{8}-\d{6}-[sd]-([a-z]{3})-[a-z0-9]{3}\b")

SUBJECT_FLAG = re.compile(r"--entry[=\s]+\S+")

DOWNSTREAM_REFUSAL = """GROUNDING GATE: refused. {missing}

  {command}

→ rerun w/ `--down 2 --up 1`. Depths are part of the command, not a default to
  weigh: `--down` carries any rename, `--up` says whether the entry still applies.
  Only a `prc`-layer rules entry may read shallower."""

WRAPPER_REFUSAL = """GROUNDING GATE: refused. Tool reached without the wrapper → read unrecorded.

  {command}

Via `scripts/graph.py`, naming the subject:

  {tool} search --query '<subject>' --entry <entry>
  {tool} view --layout "topic(area-<name>):as-list" --entry <entry>
  {tool} show <entry> --down 2 --entry <entry>

Subject still owes: {tool} ground <entry>"""

STREAM_REFUSAL = """GROUNDING GATE: refused. A discarded stream turns a refusal into an empty result.

  {command}

Keep both streams; filter instead:

  {tool} search --term '<literal>' --limit 8 | grep -E '^  [0-9]'

Other cmd in same line needing null device → own step."""

REFUSAL = """GROUNDING GATE: refused. {missing}

Run both + an area listing, then ask again:

  {tool} search --term '<literal>' --entry <entry>
  {tool} search --query '<subject>' --entry <entry>
  {tool} view --layout "topic(area-<name>):as-list" --entry <entry>

Empty search ≠ absence; area listing settles membership.

This turn carries: {have}"""


CAPTURE = re.compile(r"graph\.py\s+capture\b")
DRAFT = re.compile(r"[\w./-]+\.md\b")
GAP_KIND = re.compile(r"^kind:\s*gap\s*$", re.M)

REFUSAL_GAP = """NO GAP CAPTURE: refused. {draft} carries `kind: gap`: records a problem, leaves it standing.

next | by what the gap needs:

  repair somebody can make          make it
  repair another row owns           plan.py finding <that row> "..."
  question only owner settles       plan.py hold <entry> --why '<what they settle>'
  decision that settles it          capture the decision, never the gap
  nobody can act yet                plan.py postpone <entry> --reason '<who waits on what>'
"""


def bases():
    """Where a draft path in the command resolves from.

    The working directory the command runs in, and the project root. Every other
    checkout is somebody else's: a relative name resolved against a worktree
    reads that checkout's file, so a capture of a clean draft here is refused
    for a draft nobody named.
    """
    found = [os.getcwd(), os.environ.get("CLAUDE_PROJECT_DIR", ".")]

    return list(dict.fromkeys(found))


def gap_draft(command):
    """The first draft named in this command that records a gap."""
    for candidate in DRAFT.findall(command):
        for base in bases():
            path = candidate if os.path.isabs(candidate) else os.path.join(base, candidate)
            try:
                with open(path, encoding="utf-8") as handle:
                    head = handle.read(4096)
            except OSError:
                continue
            if GAP_KIND.search(head):
                return candidate

    return None

def ledger_path(payload):
    """Where this session's evidence lives, or nothing where it cannot.

    A session identifier arrives from outside and may carry a separator, and the
    directory may be occupied by a file. Neither is worth a traceback: this runs
    on every prompt, and a non-zero exit there blocks the prompt itself.
    """
    session = str(payload.get("session_id") or "unknown").replace("/", "-")
    # A spawned agent shares its parent's session, so its evidence is its own
    # file: no prompt ever starts a turn for it, and the parent's reads are not its.
    agent = str(payload.get("agent_id") or "").replace("/", "-")
    if agent:
        session = f"{session}.{agent}"
    try:
        os.makedirs(LEDGER_DIR, exist_ok=True)
    except OSError:
        return None

    return os.path.join(LEDGER_DIR, f"{session}.json")


def load(path):
    if not path:
        return {"search": 0, "query": 0, "term": 0, "areas": []}

    try:
        with open(path, encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return {"search": 0, "query": 0, "term": 0, "areas": []}


def save(path, state):
    if not path:
        return

    try:
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(state, handle)
    except OSError:
        pass


def failed(payload):
    """Whether the command this records came back refused.

    A read that errored found nothing, and crediting it is the very failure this
    exists to stop: a wrong flag prints usage and exits non-zero, and counting
    that as a read performed opens the gate on an empty result.
    """
    answer = payload.get("tool_response")
    if isinstance(answer, dict):
        if answer.get("is_error") or answer.get("interrupted"):
            return True
        code = answer.get("exit_code")

        return code is not None and code != 0

    return False


def record(payload):
    command = (payload.get("tool_input") or {}).get("command") or ""
    # Two readings of one command. The shell-stripped form says whether a call
    # happened, because a quoted mention is an argument and crediting one lets an
    # `echo` satisfy the whole gate. The raw form says what that call asked for,
    # because the layout and the search mode live inside quoted arguments.
    spoken = shell_only(command)
    if not command or failed(payload):
        return 0

    path = ledger_path(payload)
    state = load(path)
    changed = False

    if SEARCH.search(spoken):
        state["search"] = state.get("search", 0) + 1
        changed = True
    if SEMANTIC.search(command) and SEARCH.search(spoken):
        state["query"] = state.get("query", 0) + 1
        changed = True
    if LITERAL.search(command) and SEARCH.search(spoken):
        state["term"] = state.get("term", 0) + 1
        changed = True

    for match in (AREA_LISTING.finditer(command) if VIEW.search(spoken) else ()):
        name = command[match.end():].split(")")[0].strip()
        area = f"area-{name}"
        if area not in state["areas"]:
            state["areas"].append(area)
        changed = True

    if changed:
        save(path, state)

    return 0


def turn(payload):
    """Start a fresh turn, so evidence never carries over.

    A turn that failed to clear leaves the last one's evidence standing, and the
    gate then opens on grounding nobody performed. So a failure here is worse
    than no ledger at all: the file is removed rather than left.
    """
    path = ledger_path(payload)
    if not path:
        return 0

    try:
        save(path, {"search": 0, "query": 0, "term": 0, "areas": []})
    except Exception:
        with contextlib.suppress(OSError):
            os.unlink(path)

    return 0


def gated(payload):
    """Whether this call commits a claim the gate covers."""
    tool = payload.get("tool_name")
    if tool == "AskUserQuestion":
        return True
    if tool not in GATED_TOOLS:
        return False

    target = (payload.get("tool_input") or {}).get("file_path") or ""

    return bool(DRAFT_PATH.search(target)) and not verified_before(target)


def verified_before(target):
    """Whether a verification already read this draft against a grounded subject.

    Registering the draft needed the subject's reading, and the verification
    that follows judges the next version's change against its own findings. An
    edit answering those findings reopens no question the reads of a turn would
    settle, so it owes none of them.
    """
    wanted = os.path.realpath(target)
    try:
        names = [name for name in os.listdir(LEDGER_DIR) if name.endswith(".json")]
    except OSError:
        return False
    for name in names:
        try:
            with open(os.path.join(LEDGER_DIR, name), encoding="utf-8") as handle:
                drafts = (json.load(handle) or {}).get("drafts") or {}
        except (OSError, ValueError, AttributeError):
            continue
        for path, record in drafts.items():
            if os.path.realpath(path) == wanted and (record or {}).get("history"):
                return True

    return False


def captures_a_gap(command):
    """Refuse a capture whose draft records a problem rather than an answer."""
    if not CAPTURE.search(command):
        return 0

    draft = gap_draft(command)
    if not draft:
        return 0

    print(REFUSAL_GAP.format(tool=tool(), draft=draft), file=sys.stderr)

    return 2


def skips_the_wrapper(texts, raw):
    """Refuse a read the ledger never sees, however the call is spelled.

    Only where this project carries a wrapper. Without one the bare tool is how
    the graph is reached, and every read is legitimate.
    """
    if not wrapper():
        return 0

    reached = any(BARE_SDD.search(text) for text in texts) or aliases_the_tool(raw)
    if not reached:
        return 0

    print(WRAPPER_REFUSAL.format(tool=tool(), command=raw.strip()), file=sys.stderr)

    return 2


def discards_a_stream(texts, raw):
    """Refuse a graph read whose output goes where nobody can read it."""
    discarded = any(SDD_CALL.search(text) and TO_NULL.search(text) for text in texts)
    if not discarded:
        return 0

    print(STREAM_REFUSAL.format(tool=tool(), command=raw.strip()), file=sys.stderr)

    return 2


def reads_too_shallow(command):
    """What a show falls short of, in the words the refusal needs, or nothing."""
    found = SHOW.search(command)
    if not found:
        return ""

    short = []
    for flag, wanted in REACH.items():
        given = DEPTH[flag].search(found.group(0))
        reached = int(given.group(1)) if given else 0
        if reached < wanted:
            short.append(f"`--{flag} {reached}`" if given else f"no `--{flag}`")

    if not short:
        return ""

    return " and ".join(short) + " reads short of `--down 2 --up 1`."


def hides_the_downstream(command):
    """Refuse a read that stops before the chain carrying a rename."""
    missing = reads_too_shallow(command)
    if not missing:
        return 0

    # The subject a read is recorded against is not what it reads, so its layer
    # never decides whether the read may stop short.
    read = SUBJECT_FLAG.sub(" ", command)
    layers = set(ENTRY_ID.findall(read))
    if layers and layers == {"prc"}:
        return 0

    print(DOWNSTREAM_REFUSAL.format(tool=tool(), command=command.strip(), missing=missing), file=sys.stderr)

    return 2


HEREDOC = re.compile(r"<<-?\s*(['\"]?)(\w+)\1\r?\n(.*?)^\2$", re.S | re.M)

# A quoted span is an argument rather than shell syntax. A grep pattern holds
# pipes and parentheses that read as a pipeline or a subshell, and matching them
# refuses the command searching for a mistake alongside the one making it.
QUOTED = re.compile(r"'[^']*'|\"[^\"]*\"")


def quoted_spans(command):
    """Every span the shell reads as text, scanned the way the shell scans one.

    A backslash escapes the character after it, so `\\'` is an apostrophe rather
    than an opening quote, and pairing it with a later quote moves every boundary
    after it. A quote nothing closes opens no span for the same reason.
    """
    spans = []
    index = 0
    while index < len(command):
        char = command[index]
        if char == "\\":
            index += 2
            continue
        if char in "'\"":
            close = command.find(char, index + 1)
            if close == -1:
                index += 1
                continue
            spans.append((index, close + 1))
            index = close + 1
            continue
        index += 1

    return spans


def unquoted(command):
    """The command with every quoted span replaced by an empty one."""
    kept = []
    last = 0
    for start, end in quoted_spans(command):
        kept.append(command[last:start])
        kept.append("''")
        last = end
    kept.append(command[last:])

    return "".join(kept)


def shell_only(command):
    """The command with every heredoc body removed.

    A body is data the shell hands to a program, so a graph read quoted inside one
    is prose rather than a call. Matching it refuses a write for describing the
    very mistake this exists to stop.
    """
    return unquoted(HEREDOC.sub("<<REDACTED\n", command))


def inside_quotes(command):
    """Every span the shell hands on as text rather than reading as syntax."""
    return quoted_spans(command)


def quoted_at(spans, position):
    return any(start < position < end for start, end in spans)


def carried(command, depth=0):
    """Every command string a shell inside this one would run.

    A shell handed `-c 'sdd show ...'` runs the tool while the outer line reads
    as an argument to `bash`, so the payload is read as the command it is. A
    `bash -c` quoted inside another argument is a mention, and stays one.
    """
    spans = inside_quotes(command)
    found = []
    for match in SHELL_C.finditer(command):
        if quoted_at(spans, match.start()):
            continue
        inner = match.group(1)
        if inner[:1] in "'\"":
            inner = inner[1:-1]
        found.append(inner)
        if depth < 3:
            found.extend(carried(inner, depth + 1))

    return found


def aliases_the_tool(command):
    """Whether the line gives the tool another name to be reached by."""
    spans = inside_quotes(command)

    return any(not quoted_at(spans, match.start()) for match in ALIASED.finditer(command))


# A command that walks into another repository is that repository's business.
# The rules here derive from this project's graph, and a session working on a
# different tree would otherwise answer to a corpus it never reads.
ENTERS = re.compile(r"\bcd\s+(?:'([^']+)'|\"([^\"]+)\"|([^\s;&|]+))")


def elsewhere(command):
    """Whether the command starts by leaving this project's tree."""
    here = os.path.realpath(os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd())
    for match in ENTERS.finditer(command):
        target = next(group for group in match.groups() if group)
        target = os.path.expanduser(os.path.expandvars(target))
        if not os.path.isabs(target):
            continue
        walked = os.path.realpath(target)
        if walked == here or walked.startswith(here + os.sep):
            continue
        # A repository of its own, rather than a path that merely reads as one:
        # a step into somewhere that does not exist stays this tree's business.
        if any(os.path.isdir(os.path.join(walked, mark)) for mark in (".git", ".sdd")):
            return True

    return False


def reads_badly(payload):
    raw = (payload.get("tool_input") or {}).get("command") or ""
    if elsewhere(raw):
        return 0
    # A heredoc body is data whatever the shape inside it, so it is dropped
    # before the payload scan as well as before the call scan.
    written = HEREDOC.sub("<<REDACTED\n", raw)
    spoken = shell_only(raw)
    texts = [spoken] + [shell_only(inner) for inner in carried(written)]

    return (captures_a_gap(spoken) or skips_the_wrapper(texts, written)
            or discards_a_stream(texts, raw)
            or next((held for held in map(hides_the_downstream, texts) if held), 0))


def gate(payload):
    if payload.get("tool_name") == "Bash":
        return reads_badly(payload)

    if not gated(payload):
        return 0

    state = load(ledger_path(payload))
    semantic = state.get("query", 0) > 0
    literal = state.get("term", 0) > 0
    areas = state.get("areas", [])

    if semantic and literal and areas:
        return 0

    have = []
    if semantic:
        have.append("a semantic query")
    if literal:
        have.append("a literal term search")
    if areas:
        have.append("an area listing of " + ", ".join(areas))
    missing = []
    if not semantic:
        missing.append("No `--query` search ran this turn.")
    if not literal:
        missing.append("No `--term` search ran this turn.")
    if not areas:
        missing.append("No area listing ran this turn.")

    print(REFUSAL.format(tool=tool(), have=" and ".join(have) or "neither read",
                         missing=" ".join(missing)), file=sys.stderr)

    return 2


MODES = {"record": record, "turn": turn, "gate": gate}

# The name of the directory a decision graph lives in. A project carrying one is
# a project this gate has something to say about; every other project is not.
GRAPH = ".sdd"


def graph_dir():
    """The graph governing this session, or None where the project carries none.

    The search walks up, because a session may sit in a subdirectory of the
    project and a worktree sits beside the checkout that owns it. It stops at
    the filesystem root rather than at a repository boundary, so a graph one
    level above a nested checkout still answers for it.
    """
    walked = os.path.realpath(os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd())
    while True:
        if os.path.isdir(os.path.join(walked, GRAPH)):
            return os.path.join(walked, GRAPH)
        parent = os.path.dirname(walked)
        if parent == walked:
            return None
        walked = parent


def graph_enabled():
    """A project opts in by carrying a graph. Nothing is created."""
    return graph_dir() is not None


def main():
    if len(sys.argv) < 2 or sys.argv[1] not in MODES:
        print(f"usage: {sys.argv[0]} {'|'.join(MODES)}", file=sys.stderr)
        return 1

    # Every mode reasons about what a session read out of a decision graph, so a
    # project carrying none gets no answer rather than a refusal it cannot act on.
    if not graph_enabled():
        return 0

    try:
        payload = json.load(sys.stdin)
    except ValueError:
        payload = {}

    # A payload that is not a mapping carries no fields to read, and this runs
    # where a non-zero exit blocks the user's own prompt.
    if not isinstance(payload, dict):
        payload = {}

    return MODES[sys.argv[1]](payload)


if __name__ == "__main__":
    sys.exit(main())
