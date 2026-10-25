# Documentation

Every page states what the tree does now, in the present tense. What is unbuilt or undecided lives
in the decision graph under `.sdd/`, and why each piece is the way it is lives there too.

Each piece has a folder with two pages: `usage.md`, what an agent or a person does with it, and
`architecture.md`, how it works inside.

| Folder | Piece |
|---|---|
| [`plugin/`](plugin/usage.md) | the plugin itself: how a project opts in, the skills, developing on a checkout; the launcher, its fetch and lock, the release ([architecture](plugin/architecture.md)) |
| [`memory/`](memory/usage.md) | `agentic-memory`: seats, entries, search, what comes back ([architecture](memory/architecture.md)) |
| [`tools/`](tools/usage.md) | `agentic-tools`: the library of agents' helper tools, found, taken and registered across projects ([architecture](tools/architecture.md)) |
| [`scratch/`](scratch/usage.md) | `agentic-scratch`: where throwaway goes and what the sweep removes ([architecture](scratch/architecture.md)) |
| [`grounding-gate/`](grounding-gate/usage.md) | `agentic-grounding-gate`: the reads a turn owes before a question, a draft or a capture ([architecture](grounding-gate/architecture.md)) |
| [`capture/`](capture/usage.md) | `agentic-capture`: writing one sdd entry from a draft ([architecture](capture/architecture.md)) |
