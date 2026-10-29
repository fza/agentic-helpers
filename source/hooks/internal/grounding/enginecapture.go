package grounding

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/draftlint"
	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
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

	path, err := ledgerPath(env.ledgerDir(), payload)

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

	text, carried, unheld := call.body(held.Body)
	if unheld {
		if payload.ToolName != mcpNext || held.Asked || err != nil {
			return ""
		}

		held.Asked = true
		ledger.Captures[call.instance] = held
		_ = saveEvidence(ctx, path, ledger)

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
	dir := env.ledgerDir()

	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return refusal("engine-lint-unread.txt", "{error}", fmt.Sprintf("writing the body for the lint: %v", err))
	}

	file, err := os.CreateTemp(dir, ".body-*.md")
	if err != nil {
		return refusal("engine-lint-unread.txt", "{error}", fmt.Sprintf("writing the body for the lint: %v", err))
	}

	defer func() { _ = os.Remove(file.Name()) }()

	_, err = file.WriteString(text + "\n")
	closeErr := file.Close()

	if err != nil || closeErr != nil {
		return refusal("engine-lint-unread.txt", "{error}", fmt.Sprintf("writing the body for the lint: %v", cmp.Or(err, closeErr)))
	}

	alerts, err := draftlint.Run(ctx, env.Runner, env.project(), command, file.Name())
	if err != nil {
		return refusal("engine-lint-unread.txt", "{error}", err.Error())
	}

	if len(alerts) == 0 {
		return ""
	}

	for index, alert := range alerts {
		alerts[index] = "  " + strings.Replace(alert, file.Name(), "body", 1)
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
// hands it on: the serve itself, the MCP result carrying it as structured
// content or as JSON in a text block, or the bare list of blocks.
func serveOf(raw json.RawMessage) serve {
	var direct serve

	err := json.Unmarshal(raw, &direct)
	if err == nil && direct.Instance != "" {
		return direct
	}

	var wrapped struct {
		Structured serve       `json:"structuredContent"`
		Content    []textBlock `json:"content"`
	}

	err = json.Unmarshal(raw, &wrapped)
	if err == nil && wrapped.Structured.Instance != "" {
		return wrapped.Structured
	}

	blocks := wrapped.Content
	if err != nil {
		_ = json.Unmarshal(raw, &blocks)
	}

	for _, block := range blocks {
		var served serve

		if json.Unmarshal([]byte(block.Text), &served) == nil && served.Instance != "" {
			return served
		}
	}

	return serve{}
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

	path, err := ledgerPath(env.ledgerDir(), payload)
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
		_ = saveEvidence(ctx, path, ledger)

		return
	}

	text, carried, unheld := call.body(held.Body)
	if carried && !unheld {
		held.Body, held.Asked = text, false
	}

	topics, reported := call.topics()
	if reported {
		held.Topics = topics
	}

	ledger.Captures[instance] = held
	_ = saveEvidence(ctx, path, ledger)
}
