# The scratch sweep: usage

An agent writes probes, one-off scripts and captured output. Sent to the system temporary directory
they are invisible to whoever reviews the work; left at the root of the project's own `.tmp/` they
pile up, and nothing tells a throwaway apart from a draft that still matters.

So a session writes its throwaway under a directory named for itself, and that directory goes when
the session ends. A directory abandoned by a session that crashed is reaped a week later.

**Only a directory named for a session is ever removed.** A name the sweep cannot parse as a session
identifier is somebody's deliberate keep, and survives whatever its age.

```
.tmp/
├── claude/<session-id>/   throwaway. Swept.
├── claude/<name>/         kept. Never swept.
├── drafts/                awaiting capture. Never swept.
└── <the project's own>    never touched
```

A project opts in by carrying `.tmp/`. As a session starts there, the sweep keeps `.tmp/` out of every
repository: where git does not ignore it already, `.tmp/` is appended to git's global excludes file
(`core.excludesFile`, else `$XDG_CONFIG_HOME/git/ignore`, else `~/.config/git/ignore`), said once:
`agentic-scratch: added .tmp/ to <file>, so git ignores it in every repository`.

## Off on the machine

The plugin option `scratch_enabled` switches the sweep on or off for the machine, on unless set. Off, `agentic-scratch` removes nothing and says nothing. Shell-guard's `mktemp` ban goes off with it, since its refusal would send scratch files to a `.tmp/` nothing sweeps.
