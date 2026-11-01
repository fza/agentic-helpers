import glob
import json
import os
import re

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.expanduser('~/.claude/projects/-Users-felix-Codepot-sources-agentic-helpers')
STOP = set('the and for with from that this then into onto over once when what which while after before since about '
           'are was were has have had not but its their there here only also each every all any per via one two '
           'done now next still yet will can may must shall agent seat state main'.split())
INTENT = re.compile(r'\b(next|then|waits?|waiting|pending|owed|todo|open|after|until|once|blocked|remaining|left)\b', re.I)
MEMORY_ID = re.compile(r'\bm-[a-z0-9]{6}\b')
SHA = re.compile(r'\b[0-9a-f]{7,12}\b')


def tokens(text):
    return {word for word in re.findall(r'[a-z][a-z0-9_-]{2,}', text.lower()) if word not in STOP}


entries = json.load(open(os.path.join(HERE, 'entries.json')))['entries']
titles = [tokens(entry['title']) for entry in entries]
status = {entry['id']: entry.get('status', '') for entry in entries}
subjects = [tokens(line.split(' ', 1)[1]) for line in open(os.path.join(HERE, 'commits.txt')) if ' ' in line]


def covered(clause_tokens, pool):
    if not clause_tokens:
        return False
    return any(len(clause_tokens & other) / len(clause_tokens) >= 0.6 for other in pool if other)


def bodies():
    for path in glob.glob(os.path.join(ROOT, '*.jsonl')):
        if os.path.getmtime(path) < os.path.getmtime(os.path.join(HERE, 'commits.txt')) - 5 * 86400:
            continue
        for line in open(path, encoding='utf-8', errors='replace'):
            try:
                record = json.loads(line)
            except ValueError:
                continue
            content = (record.get('message') or {}).get('content')
            if not isinstance(content, list):
                continue
            for part in content:
                if isinstance(part, dict) and part.get('type') == 'tool_use' and part.get('name', '').endswith('_memory__state'):
                    yield path, (part.get('input') or {}).get('body') or ''


chars = {'commit': 0, 'entry': 0, 'pointer only': 0, 'intent': 0, 'residual': 0}
dropped_cited = dropped_cited_ended = 0
previous_by_path = {}
samples = []
writes = 0
for path, body in bodies():
    writes += 1
    clauses = [part.strip() for part in re.split(r'(?<=[.;])\s+|\n+', body) if part.strip()]
    for clause in clauses:
        clause_tokens = tokens(MEMORY_ID.sub('', SHA.sub('', clause)))
        if len(clause_tokens) <= 1 and (MEMORY_ID.search(clause) or SHA.search(clause)):
            kind = 'pointer only'
        elif covered(clause_tokens, subjects):
            kind = 'commit'
        elif covered(clause_tokens, titles):
            kind = 'entry'
        elif INTENT.search(clause):
            kind = 'intent'
        else:
            kind = 'residual'
        chars[kind] += len(clause)
        if kind == 'residual' and len(samples) < 12 and len(clause) > 60:
            samples.append(clause[:160])
    previous = previous_by_path.get(path)
    if previous:
        now_ids = set(MEMORY_ID.findall(body))
        for clause in previous:
            ids = set(MEMORY_ID.findall(clause))
            if ids and not ids & now_ids:
                dropped_cited += 1
                if any(status.get(one, '') in ('retired', 'superseded') for one in ids):
                    dropped_cited_ended += 1
    previous_by_path[path] = clauses

total = sum(chars.values())
print('state writes (agentic-helpers transcripts, MCP form):', writes, 'chars', total)
for kind, count in chars.items():
    print(f'  {kind:<13} {count:>8}  {count / total:.0%}')
print('clauses citing memory ids that left the next write:', dropped_cited,
      ' of those, a cited entry is now retired or superseded:', dropped_cited_ended)
print('residual samples:')
for sample in samples:
    print('  -', sample)
