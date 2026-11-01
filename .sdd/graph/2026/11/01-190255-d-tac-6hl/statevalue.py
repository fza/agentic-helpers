import glob
import json
import os
import re
import sys
import time

DAYS = float(sys.argv[1] if len(sys.argv) > 1 else 3)
CUTOFF = time.time() - DAYS * 86400
ROOT = os.path.expanduser('~/.claude/projects')
NUDGES = {
    'shipped': 'Work shipped since your last state write',
    'near compaction': 'Context near compaction',
    'replay overflow': 'no longer fit the post-compaction replay',
    'overdue': 'Memory overdue',
}
STATE_LINE = re.compile(r'\bstate \((?:[^)]*) old')
MEMORY_ID = re.compile(r'\bm-[a-z0-9]{6}\b')
GRAPH_ID = re.compile(r'\b\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]{3}\b|\b[sd]-[a-z]{3}-[a-z0-9]{3}\b')
SHA = re.compile(r'\b[0-9a-f]{7,12}\b')
REPLAY = re.compile(r"turns since seat \S+'s newest entry, in (\d+) pieces(; (\d+) older pieces cut)?")


def texts(record):
    if record.get('type') == 'attachment':
        attachment = record.get('attachment') or {}
        content = attachment.get('stdout') or attachment.get('content') or ''
        if isinstance(content, list):
            content = ' '.join(part.get('text', '') for part in content if isinstance(part, dict))
        yield 'hook', str(content)
        return
    content = (record.get('message') or {}).get('content')
    if isinstance(content, str):
        yield record.get('type', ''), content
    elif isinstance(content, list):
        for part in content:
            if not isinstance(part, dict) or part.get('type') == 'tool_use':
                continue
            value = part.get('text', '') or part.get('content', '')
            if not isinstance(value, str):
                value = json.dumps(value)
            yield part.get('type', ''), value


def clauses(body):
    parts = re.split(r'[.;\n]+\s*', body.lower())
    return {re.sub(r'\s+', ' ', part).strip() for part in parts if len(part.strip()) >= 12}


def words(body):
    return set(re.findall(r'[a-z0-9][a-z0-9_./-]{2,}', body.lower()))


nudges_fired = {}
nudges_answered = {}
writes = []
replays = []
sessions_seen = 0

for path in glob.glob(os.path.join(ROOT, '*', '*.jsonl')):
    if os.path.getmtime(path) < CUTOFF:
        continue
    events = []
    tool_calls = 0
    for line in open(path, encoding='utf-8', errors='replace'):
        try:
            record = json.loads(line)
        except ValueError:
            continue
        if record.get('isCompactSummary'):
            events.append(('compact', None))
        for kind, text in texts(record):
            for name, marker in NUDGES.items():
                if marker in text:
                    events.append(('nudge', name))
            if STATE_LINE.search(text):
                events.append(('read', None))
            match = REPLAY.search(text)
            if match:
                replays.append((int(match.group(1)), int(match.group(3) or 0)))
        content = (record.get('message') or {}).get('content')
        if not isinstance(content, list):
            continue
        for part in content:
            if not isinstance(part, dict) or part.get('type') != 'tool_use':
                continue
            tool_calls += 1
            tool_input = part.get('input') or {}
            name = part.get('name', '')
            body = None
            if name.endswith('_memory__state'):
                body = tool_input.get('body') or ''
            elif name == 'Bash' and re.search(r'agentic-memory\s+state\b', tool_input.get('command', '')):
                heredoc = re.search(r"<<-?\s*'?(\w+)'?\n(.*?)\n\1", tool_input['command'], re.S)
                body = heredoc.group(2) if heredoc else ''
            if body is not None:
                events.append(('write', (body, tool_calls)))
    if not events:
        continue
    sessions_seen += 1
    pending = None
    previous = None
    open_write = None
    for kind, value in events:
        if kind == 'nudge':
            nudges_fired[value] = nudges_fired.get(value, 0) + 1
            pending = value
        elif kind in ('read', 'compact'):
            if open_write is not None:
                open_write['read'] = True
        elif kind == 'write':
            body, calls = value
            entry = {
                'body': body, 'trigger': pending or 'unprompted', 'read': False, 'tail': False,
                'calls_since_prev': None, 'new_clause_share': None, 'word_overlap': None,
            }
            if pending:
                nudges_answered[pending] = nudges_answered.get(pending, 0) + 1
            if previous is not None:
                old, new = clauses(previous[0]), clauses(body)
                entry['new_clause_share'] = len(new - old) / len(new) if new else 0
                old_words, new_words = words(previous[0]), words(body)
                entry['word_overlap'] = len(new_words & old_words) / len(new_words) if new_words else 0
                entry['calls_since_prev'] = calls - previous[1]
            writes.append(entry)
            open_write = entry
            previous = value
            pending = None
    if open_write is not None and not open_write['read']:
        open_write['tail'] = True


def share(items, test):
    return f'{sum(1 for item in items if test(item))}/{len(items)}'


def pct(values, q):
    values = sorted(value for value in values if value is not None)
    return round(values[int(len(values) * q)], 2) if values else None


print('transcripts with state events', sessions_seen, 'writes', len(writes))
print('nudges fired / answered by a write before the next nudge:')
for name in NUDGES:
    print(f'  {name:<16} {nudges_fired.get(name, 0):>5} fired  {nudges_answered.get(name, 0):>5} answered')
print('writes read before overwritten (brief/claim/compaction):', share(writes, lambda w: w['read']),
      ' overwritten unread:', share(writes, lambda w: not w['read'] and not w['tail']),
      ' last of transcript, unread there:', share(writes, lambda w: w['tail']))
for trigger in ['unprompted', 'shipped', 'near compaction', 'overdue', 'replay overflow']:
    group = [w for w in writes if w['trigger'] == trigger]
    if group:
        print(f'  {trigger:<16} n={len(group):>4} read {share(group, lambda w: w["read"])}'
              f'  unread-overwritten {share(group, lambda w: not w["read"] and not w["tail"])}'
              f'  chars p50 {pct([len(w["body"]) for w in group], 0.5)}')
rewrites = [w for w in writes if w['new_clause_share'] is not None]
print('rewrites vs previous write in same transcript:', len(rewrites))
print('  share of clauses new  p25/p50/p75:', pct([w['new_clause_share'] for w in rewrites], 0.25),
      pct([w['new_clause_share'] for w in rewrites], 0.5), pct([w['new_clause_share'] for w in rewrites], 0.75))
print('  share of words already in previous  p25/p50/p75:', pct([w['word_overlap'] for w in rewrites], 0.25),
      pct([w['word_overlap'] for w in rewrites], 0.5), pct([w['word_overlap'] for w in rewrites], 0.75))
print('  tool calls since previous write p25/p50/p75:', pct([w['calls_since_prev'] for w in rewrites], 0.25),
      pct([w['calls_since_prev'] for w in rewrites], 0.5), pct([w['calls_since_prev'] for w in rewrites], 0.75))
repeated = sum(len(w['body']) * (1 - (w['new_clause_share'] or 0)) for w in rewrites)
print('  chars of rewrites restating a clause of the previous write:', int(repeated), 'of',
      sum(len(w['body']) for w in rewrites))
print('writes citing memory ids:', share(writes, lambda w: MEMORY_ID.search(w['body'])),
      ' graph ids:', share(writes, lambda w: GRAPH_ID.search(w['body'])),
      ' shas:', share(writes, lambda w: SHA.search(w['body'])))
print('ids per citing write p50:', pct([len(set(MEMORY_ID.findall(w['body']))) for w in writes if MEMORY_ID.search(w['body'])], 0.5))
lines = [line for w in writes for line in re.split(r'(?<=[.;])\s+|\n', w['body']) if line.strip()]
print('state clauses naming next/todo/pending/waits:',
      share(lines, lambda l: re.search(r'\b(next|todo|pending|waits?|owed|open|then)\b', l, re.I)))
print('compaction replays seen:', len(replays), ' with older pieces cut:', share(replays, lambda r: r[1] > 0),
      ' pieces p50:', pct([r[0] for r in replays], 0.5))
