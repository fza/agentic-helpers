package carryforward

import "strings"

const refreshed = self + " refreshed"

// Every carry-forward is read by an agent, so it is written in the terse
// register every agent-facing text uses. The rule rides in every nudge because
// a seat reads the nudge and not the rules file.
const keepRule = "Settled since last refresh → `## Uncaptured decisions`. Amend where something " +
	"changed, leave untouched where nothing did. " +
	"Write it caveman ultra: no articles/filler, fragments + arrows; ids, paths, commands exact."

const nudge = "Context near compaction. Fold `.memory/transcripts/{session}.md` into your " +
	"carry-forward `.memory/carryforward/{role}-memory.md`, then " +
	"`" + refreshed + "`. " + keepRule + " No graph capture, no other `.memory/` file, no handoff prompt."

const backstop = "Carry-forward overdue, two nudges unanswered. Fold " +
	"`.memory/transcripts/{session}.md` into `.memory/carryforward/{role}-memory.md` now, " +
	"then `" + refreshed + "` before ending the turn. " + keepRule

func fill(text string, role string, session string) string {
	return strings.NewReplacer("{role}", role, "{session}", session).Replace(text)
}

func nudgeText(role string, session string) string {
	return fill(nudge, role, session)
}

func backstopText(role string, session string) string {
	return fill(backstop, role, session)
}
