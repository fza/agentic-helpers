package shellguard

import (
	"context"
	"fmt"
)

// An ask the agent can fix by rewriting the command refuses with its reason
// the first times in a session and prompts only on the attempt reaching
// rewriteLimit, so the person sees only a command the agent could not make
// judgeable. An outside-project removal and find -delete prompt at once: no
// rewording makes them judgeable.
const (
	rewriteLimit      = 3
	rewriteNotFixed   = "not_fixed"
	rewriteCredential = "credential"
	noteRewrite       = "%s; rewrite the command (attempt %d of %d)"
)

// rewritable is the verdict of a guard set to ask, deny or off whose ask the
// agent can fix by rewriting, counted under key.
func rewritable(setting string, reason string, key string) Verdict {
	verdict := mode(setting, reason)
	verdict.Rewrite = key

	return verdict
}

// refuseFirst counts the attempt and turns the ask into a refusal before the
// last one. A session it cannot keep a count for, with no usable session id
// or no state directory, is on its first attempt every time: the agent can
// always rewrite the command, while a prompt would be the one the rule spares
// the person. Claude Code sends a session id with every call.
func refuseFirst(ctx context.Context, env Env, session string, verdict Verdict) Verdict {
	held := stateOf(ctx, env, session)
	if held.Attempts == nil {
		held.Attempts = map[string]int{}
	}

	held.Attempts[verdict.Rewrite]++
	attempt := held.Attempts[verdict.Rewrite]

	held.save()

	if attempt >= rewriteLimit {
		return verdict
	}

	return Verdict{Decision: Deny, Reason: fmt.Sprintf(noteRewrite, verdict.Reason, attempt, rewriteLimit)}
}
