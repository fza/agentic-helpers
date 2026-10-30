package grounding

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/draftlint"
	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// A capture through sdd's engine never passes through `agentic-capture`, so
// the project's `draft_lint` and `listing_prefix` rule are held here, on the
// reports that carry the entry's text and topics. The field names, the
// playback step and the edit rule are sdd's capture procedure's: a change
// there needs one here.
const (
	captureProcedure = "capture"
	playbackStep     = "playback"
	confirmChoice    = "confirm"
)

// engineDraft is what the engine holds of one capture: the last whole body
// and the topics it was given, and whether the gate already asked for the
// whole body once.
type engineDraft struct {
	Body   string   `json:"body"`
	Topics []string `json:"topics"`
	Asked  bool     `json:"asked"`
}

// engineCall is a capture call's content: the state fields a report or the
// start params carry, those inside a chooser answer's `fields` among them.
type engineCall struct {
	instance string
	chooser  string
	choice   string
	fields   map[string]any
}

func engineCallOf(payload hookio.Payload) (engineCall, bool) {
	var input map[string]any

	err := json.Unmarshal(payload.ToolInput.Raw, &input)
	if err != nil {
		return engineCall{}, false
	}

	carried := "report"
	if payload.ToolName == mcpStartProcedure {
		carried = "params"
	}

	call := engineCall{fields: map[string]any{}}
	call.instance, _ = input["instance"].(string)

	values, _ := input[carried].(map[string]any)
	for name, value := range values {
		call.fields[name] = value
	}

	call.chooser, _ = values["chooser"].(string)
	call.choice, _ = values["choice"].(string)

	answered, _ := values["fields"].(map[string]any)
	for name, value := range answered {
		call.fields[name] = value
	}

	return call, true
}

func (call engineCall) topics() ([]string, bool) {
	value, found := call.fields["topics"]
	if !found {
		return nil, false
	}

	var topics []string

	listed, _ := value.([]any)
	for _, item := range listed {
		topic, isText := item.(string)
		if isText {
			topics = append(topics, topic)
		}
	}

	return topics, true
}

// body is the body the call leaves the capture with, from the last whole body
// held: a `body` replaces it, a `bodyPatch` edits it. carried says the call
// changes the body at all; unheld says a patch found no body to edit. A patch
// sdd would refuse (an old text missing or doubled) is no change: sdd refuses
// the report itself.
func (call engineCall) body(held string) (text string, carried bool, unheld bool) {
	whole, hasBody := call.fields["body"].(string)
	patches, hasPatch := call.fields["bodyPatch"].([]any)

	if !hasBody && !hasPatch {
		return "", false, false
	}

	if !hasPatch {
		return whole, true, false
	}

	base := held
	if hasBody {
		base = whole
	}

	if base == "" {
		return "", true, true
	}

	edited, applied := applyPatch(base, patches)
	if !applied {
		return "", false, false
	}

	return edited, true, false
}

// applyPatch applies sdd's edit rule: pairs in order, each old text found
// exactly once in the text as it stands, all or nothing.
func applyPatch(text string, patches []any) (string, bool) {
	for _, patch := range patches {
		pair, _ := patch.(map[string]any)
		old, _ := pair["old"].(string)
		replacement, _ := pair["new"].(string)

		if old == "" || strings.Count(text, old) != 1 {
			return "", false
		}

		text = strings.Replace(text, old, replacement, 1)
	}

	return text, true
}

func prefixed(topics []string, prefix string) bool {
	for _, topic := range topics {
		if strings.HasPrefix(topic, prefix) {
			return true
		}
	}

	return false
}

// engineCaptureRefusal holds a capture start or report to the project's topic
// prefix and draft lint. A report to an instance the gate never saw start as
// a capture passes: the gate cannot tell it from another procedure's.
func engineCaptureRefusal(ctx context.Context, env Env, payload hookio.Payload, config graphconfig.Config) string {
	prefix := config.ListingPrefix
	if prefix == "" && config.DraftLint == "" {
		return ""
	}

	call, decoded := engineCallOf(payload)
	if !decoded {
		return ""
	}

	held := engineDraft{}

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)

	ledger := freshEvidence()
	if err == nil {
		ledger = loadEvidence(ctx, path)
	}

	if payload.ToolName == mcpNext {
		known, found := ledger.Captures[call.instance]
		if !found {
			return ""
		}

		held = known
	}

	topics, reported := call.topics()
	if prefix != "" && reported && !prefixed(topics, prefix) {
		return refusal("engine-topics.txt", "{prefix}", prefix)
	}

	if prefix != "" && call.chooser == playbackStep && call.choice == confirmChoice && !prefixed(held.Topics, prefix) {
		return refusal("engine-confirm.txt", "{prefix}", prefix)
	}

	if config.DraftLint == "" {
		return ""
	}

	handle, named := call.handle()
	if named {
		return handleRefusal(ctx, env, config.DraftLint, ledger, path, handle)
	}

	text, carried, unheld := call.body(held.Body)
	if unheld {
		if payload.ToolName != mcpNext || held.Asked || err != nil {
			return ""
		}

		held.Asked = true
		ledger.Captures[call.instance] = held
		_ = saveEvidence(ctx, env.graphRoot(), path, ledger)

		return refusal("engine-body.txt")
	}

	if !carried {
		return ""
	}

	return lintBody(ctx, env, config.DraftLint, text)
}

// lintBody runs the project's lint on the body, written to a file of its own
// in the gate's scratch directory, from the project's root as `agentic-capture`
// runs it. A finding names the body by `body` and its line from the body's
// first.
func lintBody(ctx context.Context, env Env, command string, text string) string {
	folder, err := safefs.EnsureAt(env.graphRoot(), env.ledgerDir(), 0o755)
	if err != nil {
		return refusal("engine-lint-unread.txt", "{error}", fmt.Sprintf("writing the body for the lint: %v", err))
	}

	defer folder.Close()

	file, name, err := folder.CreateTemp(".body-*.md", 0o600)
	if err != nil {
		return refusal("engine-lint-unread.txt", "{error}", fmt.Sprintf("writing the body for the lint: %v", err))
	}

	defer func() { _ = folder.Remove(name) }()

	_, err = file.WriteString(text + "\n")
	closeErr := file.Close()

	if err != nil || closeErr != nil {
		return refusal("engine-lint-unread.txt", "{error}", fmt.Sprintf("writing the body for the lint: %v", cmp.Or(err, closeErr)))
	}

	body := filepath.Join(env.ledgerDir(), name)

	alerts, err := draftlint.Run(ctx, env.Runner, env.project(), command, body)
	if err != nil {
		return refusal("engine-lint-unread.txt", "{error}", err.Error())
	}

	if len(alerts) == 0 {
		return ""
	}

	for index, alert := range alerts {
		alerts[index] = "  " + strings.Replace(alert, body, "body", 1)
	}

	return refusal("engine-lint.txt", "{findings}", strings.Join(alerts, "\n"))
}

// serve is what an engine call answers with, as far as the gate reads it:
// the instance it serves, its procedure and where it stands.
type serve struct {
	Instance  string `json:"instance"`
	Procedure string `json:"procedure"`
	Status    string `json:"status"`
}

// serveOf reads the serve from a tool response, whichever way the client
// hands it on: the serve's JSON as one string (Claude Code's form), the serve
// itself, the MCP result carrying it as structured content or as JSON in a
// text block, or the bare list of blocks.
func serveOf(raw json.RawMessage) serve {
	return answerOf(raw, func(served serve) bool { return served.Instance != "" })
}

// answerOf reads an engine tool's answer in each of those forms, the first
// one valid accepts.
func answerOf[Answer any](raw json.RawMessage, valid func(Answer) bool) Answer {
	var text string

	if json.Unmarshal(raw, &text) == nil {
		raw = json.RawMessage(text)
	}

	var direct Answer

	err := json.Unmarshal(raw, &direct)
	if err == nil && valid(direct) {
		return direct
	}

	var wrapped struct {
		Structured Answer      `json:"structuredContent"`
		Content    []textBlock `json:"content"`
	}

	err = json.Unmarshal(raw, &wrapped)
	if err == nil && valid(wrapped.Structured) {
		return wrapped.Structured
	}

	blocks := wrapped.Content
	if err != nil {
		_ = json.Unmarshal(raw, &blocks)
	}

	for _, block := range blocks {
		var answered Answer

		if json.Unmarshal([]byte(block.Text), &answered) == nil && valid(answered) {
			return answered
		}
	}

	var none Answer

	return none
}

type textBlock struct {
	Text string `json:"text"`
}

// recordEngineCapture keeps what a capture call that went through left the
// engine holding: its body and topics, under the instance it serves. A
// capture that ended is dropped.
func recordEngineCapture(ctx context.Context, env Env, payload hookio.Payload) {
	if payload.Failed() || (payload.ToolName != mcpNext && !gatesCapture(payload)) {
		return
	}

	call, decoded := engineCallOf(payload)
	if !decoded {
		return
	}

	served := serveOf(payload.ToolResponse)

	instance := call.instance
	if payload.ToolName == mcpStartProcedure {
		instance = served.Instance
	}

	if instance == "" {
		return
	}

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
	if err != nil {
		return
	}

	ledger := loadEvidence(ctx, path)

	held, known := ledger.Captures[instance]
	ownServe := served.Instance == instance

	if !known && payload.ToolName == mcpNext && !(ownServe && served.Procedure == captureProcedure) {
		return
	}

	if ownServe && (served.Status == "completed" || served.Status == "abandoned") {
		delete(ledger.Captures, instance)
		_ = saveEvidence(ctx, env.graphRoot(), path, ledger)

		return
	}

	text, carried, unheld := call.body(held.Body)
	if carried && !unheld {
		held.Body, held.Asked = text, false
	}

	// A body taken by handle is the one a later `bodyPatch` edits, once the
	// capture drops the handle. A handle the gate holds no text for leaves no
	// body, so that patch asks for the whole body rather than editing an older
	// one.
	handle, named := call.handle()
	if named {
		held.Body, held.Asked = stagedBody(ledger.Staged[handle].Text), false
	}

	topics, reported := call.topics()
	if reported {
		held.Topics = topics
	}

	ledger.Captures[instance] = held
	_ = saveEvidence(ctx, env.graphRoot(), path, ledger)
}
