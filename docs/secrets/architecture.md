# Credential scanner: architecture

## The rules

The rules are gitleaks' default rule set, pinned: gitleaks v8.30.1, commit
`83d9cd684c87d95d656c1458ef04895a7f1cbd8e`, `config/gitleaks.toml`, provider rules for keys and
tokens (AWS, GitHub, Slack, Stripe, private keys, generic API keys and others) and one global
allowlist. They are vendored as `internal/secrets/rules.json`, embedded with `go:embed`, with
gitleaks' MIT notice beside them as `LICENSE-gitleaks`.

`internal/secrets/vendor-rules.py` converts the TOML with Python's `tomllib`, so the module carries
no TOML dependency:

    python3 vendor-rules.py <gitleaks.toml> <version> <commit>

It writes `rules.json` beside itself. Refreshing the rules means moving the pin, rerunning it and
committing the result. The shape:

- top level `{source: {repository, file, version, commit}, allowlists: [global], rules: [...]}`;
- a rule `{id, regex?, path?, entropy?, secretGroup?, keywords?, allowlists?}`, keywords lowercased,
  deduplicated and sorted;
- an allowlist `{condition?, regexTarget?, regexes?, paths?, stopwords?}`, condition upper-cased,
  stopwords lowercased.

Descriptions and tags are dropped, and gitleaks' singular and plural allowlist forms map to one
list. The sdd fza fork vendors the same pin with the same converter, and the two `rules.json` files
are byte-identical, so both tools refuse the same strings.

## The engine

`secrets.Scan(ctx, text, name)` returns the findings, each a rule id and the 1-based line the match
starts on, sorted by line. It mirrors gitleaks' `detectRule` and `checkFindingAllowed` with Go's
`regexp`, as the fork does:

- **Keywords gate a rule.** A rule with keywords runs only when one occurs in the lowercased text,
  and its patterns compile on first use, so a clean text compiles almost nothing. A rule without
  keywords always runs.
- **Path rules need a name.** `name` is the file the text is the content of, and empty for text that
  is no file (a flag, a prompt, a command). A rule with a `path` applies only to a named file whose
  name it matches; a rule with a path and no regex fires on the name alone, at line 1. An allowlist
  naming the file skips it whole when it is OR-conditioned, or AND-conditioned with paths only: the
  global allowlist before every rule, a rule's own before that rule.
- **The secret** is the match's `secretGroup` group where the rule sets one (none, where the match
  holds no such group), else its first non-empty capture group, else the whole match trimmed of
  newlines. A secret whose Shannon entropy is at or below the rule's `entropy` floor is skipped.
- **Allowlists** run global first, then the rule's own. `regexTarget` empty tests the secret,
  `match` the match, `line` every full line the match spans. An OR list passes on a regex hit or on
  a stopword the lowercased secret contains; an AND list passes only when every check it carries
  does (its paths, its regexes, its stopwords).
- **Not done**, as in the fork: the inline `gitleaks:allow` marker is ignored, since nothing overrides
  a refusal; encoded segments (base64, hex, percent) are not decoded; `requiredRules` is unused by
  the file.

`secrets.Describe(findings)` is what a refusal names: every distinct rule found on the first
finding's line, sorted and comma-separated, and that line.

A scan that cannot finish, the context ended or a vendored pattern failed to compile, returns one
finding of rule `scan-incomplete`, so a caller refusing on any finding refuses rather than passes
unscanned text. The vendored rules decode and compile in a test, so in a released binary only an
ended context reaches it.

## Tests

`internal/secrets` holds the engine to the fixtures under `test/fixtures/secrets/`, the same files
the sdd fork tests against: positive samples each raising the rule their file names, negative
samples and `clean.txt` raising nothing, and gitleaks' own per-rule samples, `gitleaks/<rule-id>.json`
(`{rule, truePositives: [{text} | {path, text}], falsePositives: [...]}`), dumped from gitleaks
v8.30.1's config generator. A sample with a path is scanned as a file of that name, which covers
the path rules. Hand-made rule sets pin each engine check on its own: the entropy floor, each
allowlist target and condition, stopwords, `secretGroup`, the group fallbacks, the keyword gate,
the path rules. Further tests check that text with no keyword compiles no pattern, that every
vendored rule compiles, and that `rules.json` is the fork's file byte for byte with the pin in its
`source`. The fixtures are synthetic; `.github/secret_scanning.yml` keeps GitHub's push protection
from refusing them.
