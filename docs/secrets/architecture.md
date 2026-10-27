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
ended context reaches it. A caller whose context ended checks that itself and reports the scan as
cut short rather than as a credential: `agentic-capture` as an interrupt, the grounding gate with
its own refusal, shell-guard with its own reason under `shell_guard_secrets`.

## What it guards against

The scanner guards against a credential written by accident: a token pasted into a draft, an
environment dump captured into a log, a key file attached by mistake. It does not stand against an
agent working to get a credential past it; a credential in a shape no rule knows, or one the agent
encodes, passes as it always would. Every caller fails closed where it cannot read what it would
scan (standard input, a file not on disk, input it cannot decode), and leaves these accepted limits:

- **A staged file built from patches.** The grounding gate scans each `mcp__sdd__stage_attachment`
  call's text, a patch's `new` text among it, not the file the patches leave behind: that file is
  the sdd engine's staged state, which the gate does not hold. A credential split across patches
  passes. A staged file's `path` is read against the session's working directory, which in every
  supported setup is the project root the sdd MCP server runs in, so it names the file the engine
  reads.
- **The last instant before sdd reads an attached file.** `agentic-capture` scans every attached
  file right before it runs `sdd new`, and sdd then reads the file itself: a file changed in
  between is never scanned. The grounding gate's earlier scan of a plain `sdd new` has the same
  window, wider.
- **What a launcher or an expansion would change.** The grounding gate does not resolve what the
  shell would rewrite or a launcher would run elsewhere: it refuses it. Refused rather than resolved:
  an attachment path holding `$` or `~`; a relative attachment path after a directory change or
  behind a launcher; a plain `sdd new` or `agentic-capture` call with any argument holding an
  unquoted glob or brace; a command naming `sdd`, `new` or `capture` with any shell pattern in its
  command position (the command word, or `sdd`'s subcommand: `sd? new`, `{,}sdd new`, `sdd n?w`);
  and a write it cannot place behind a launcher's options. A path written so that sdd reads one file
  and the gate another would need the gate to follow the shell, which it does not try to do;
  refusing those spellings closes that class instead.
- **A pattern that cuts a tool's name.** A command word whose pattern stands for part of a tool's
  name, with no word of the call naming `sdd`, `new` or `capture` (`agentic-* d.md`), is not seen
  as a write. Refusing every patterned command word would close it, at the cost of refusing
  ordinary commands such as `./scripts/*.sh`.

A file is scanned by its name, so path rules fire, and as nameless text too: gitleaks' global
allowlist skips whole files by name (`.bin`, images, fonts, office files), and a credential renamed
to one would otherwise pass. agentic-tools scans every tool file both ways; the grounding gate and
agentic-capture's rescan scan an attached or staged file by its name in the entry, and as nameless
text where that finds nothing, since a name the allowlist does not skip finds all the nameless scan
would.

## Prompts

`internal/promptvector`, where both prompt hooks share a prompt's vector, scans the prompt with
`promptvector.Withheld` before either hook embeds it. A prompt with a finding is never embedded and
never fills the shared slot; agentic-tools offers nothing for it, and agentic-memory scores recall by
the prompt's words instead and prints the notice. Only agentic-memory prints it, so the agent hears
it once per prompt.

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
