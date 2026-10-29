package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

const triggerForms = "path:<glob>, command:<regex> or tool:<name>"

var ErrBadTrigger = errors.New("a trigger takes " + triggerForms)

// trigger is one tool call a trap's `--when` or a rule's `--proof` names.
type trigger struct {
	form    string
	value   string
	command *regexp.Regexp
}

func (held trigger) String() string {
	return held.form + ":" + held.value
}

// parseTrigger reads a trigger as `--when` and `--proof` spell it: a glob
// matched by doublestar, `**` crossing directories, an RE2 pattern over a Bash
// command, or a tool name.
func parseTrigger(text string) (trigger, error) {
	form, value, named := strings.Cut(strings.TrimSpace(text), ":")
	if !named || value == "" {
		return trigger{}, fmt.Errorf("%w, not %q", ErrBadTrigger, text)
	}

	switch form {
	case "path":
		if !doublestar.ValidatePattern(value) {
			return trigger{}, fmt.Errorf("%w: %q is not a glob", ErrBadTrigger, value)
		}

		return trigger{form: form, value: value}, nil
	case "command":
		pattern, err := regexp.Compile(value)
		if err != nil {
			return trigger{}, fmt.Errorf("%w: %q is not an RE2 pattern: %w", ErrBadTrigger, value, err)
		}

		return trigger{form: form, value: value, command: pattern}, nil
	case "tool":
		if strings.ContainsAny(value, " \t\n") {
			return trigger{}, fmt.Errorf("%w: %q is not a tool name", ErrBadTrigger, value)
		}

		return trigger{form: form, value: value}, nil
	}

	return trigger{}, fmt.Errorf("%w, not %q", ErrBadTrigger, text)
}

// triggersFor checks the triggers a filing names and spells each the one way.
func triggersFor(flag string, given []string) ([]string, error) {
	var spelled []string

	for _, text := range given {
		held, err := parseTrigger(text)
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", flag, err)
		}

		if !slices.Contains(spelled, held.String()) {
			spelled = append(spelled, held.String())
		}
	}

	return spelled, nil
}

// life is what a filing declares about the moments an entry acts at: a trap's
// triggers, and the moments a rule is owed at with the calls proving it.
type life struct {
	when  []string
	on    []string
	proof []string
}

// lifeFor checks a filing's --when, --on and --proof against its kind.
func lifeFor(kind string, req request) (life, error) {
	var held life

	switch {
	case len(req.When) > 0 && kind != kindTrap:
		return held, errors.New("--when is for a trap: a tool call touching what the trap is about")
	case (len(req.On) > 0 || len(req.Proof) > 0) && kind != kindRule:
		return held, errors.New("--on and --proof are for a rule the user gave: an action owed at a moment")
	case len(req.Proof) > 0 && len(req.On) == 0:
		return held, errors.New("--proof needs --on claim|compact: the moment the action is owed at")
	}

	for _, moment := range req.On {
		if !slices.Contains(doNowMoments, moment) {
			return held, fmt.Errorf("--on takes claim or compact, not %q", moment)
		}

		if !slices.Contains(held.on, moment) {
			held.on = append(held.on, moment)
		}
	}

	var err error

	held.when, err = triggersFor("when", req.When)
	if err != nil {
		return held, err
	}

	held.proof, err = triggersFor("proof", req.Proof)

	return held, err
}

// toolCall is what a tool call names: its tool, a Bash call's command, and
// every path its input names, relative to the work tree's root.
type toolCall struct {
	tool    string
	command string
	paths   []string
}

// callOf reads the paths a call names from its input's `file_path`,
// `notebook_path` and `path`, and from a Bash command's path tokens, found as
// anchors are found.
func callOf(root string, payload hookio.Payload) toolCall {
	call := toolCall{tool: payload.ToolName, command: payload.ToolInput.Command}

	var other struct {
		Path string `json:"path"`
	}

	_ = json.Unmarshal(payload.ToolInput.Raw, &other)

	tokens := []string{payload.ToolInput.FilePath, payload.ToolInput.NotebookPath, other.Path}
	if payload.ToolName == "Bash" {
		tokens = append(tokens, pathToken.FindAllString(payload.ToolInput.Command, -1)...)
	}

	for _, token := range tokens {
		path := relative(root, token)
		if path != "" && !slices.Contains(call.paths, path) {
			call.paths = append(call.paths, path)
		}
	}

	return call
}

// matches is what of the call the trigger names, or nothing where it names
// none of it.
func (held trigger) matches(call toolCall) string {
	switch held.form {
	case "path":
		for _, path := range call.paths {
			matched, err := doublestar.Match(held.value, path)
			if err == nil && matched {
				return path
			}
		}
	case "command":
		if call.tool == "Bash" && held.command != nil {
			return held.command.FindString(call.command)
		}
	case "tool":
		if call.tool == held.value {
			return held.value
		}
	}

	return ""
}

// firstMatch is what the first of the triggers that matches the call names.
func firstMatch(texts []string, call toolCall) string {
	for _, text := range texts {
		held, err := parseTrigger(text)
		if err != nil {
			continue
		}

		matched := held.matches(call)
		if matched != "" {
			return matched
		}
	}

	return ""
}

// trapIndex is the traps a seat holds, its own and received, as the tool hook
// matches them: the file anchors and the declared triggers; and its notes and
// todos declaring the paths they are about.
type trapIndex struct {
	Traps []indexedTrap `json:"traps"`
	Notes []indexedNote `json:"notes,omitempty"`
}

// indexedNote is a note or todo the tool hook tells of at a call touching a
// path it declares with --about. Only declared paths count: a note mentions
// many paths, and telling it at each would print a notice on most calls.
type indexedNote struct {
	ID    string   `json:"id"`
	About []string `json:"about"`
}

type indexedTrap struct {
	ID      string   `json:"id"`
	Anchors []string `json:"anchors,omitempty"`
	When    []string `json:"when,omitempty"`
}

// touchedBy is what of the call the trap is about: a file it anchors to, or
// what a declared trigger matched; nothing where the call touches neither.
func (trap indexedTrap) touchedBy(call toolCall) string {
	for _, path := range call.paths {
		if slices.Contains(trap.Anchors, path) {
			return path
		}
	}

	return firstMatch(trap.When, call)
}

// indexTraps writes the seat's traps for the tool hook, and removes the index
// where it holds none, so a seat without traps reads nothing on a tool call.
func (mem *memory) indexTraps(folded *state, seatName string) int {
	var index trapIndex

	for _, held := range mem.ownLive(folded, seatName, "") {
		if (held.kind == kindNote || held.kind == kindTodo) && len(held.about) > 0 {
			index.Notes = append(index.Notes, indexedNote{ID: held.id, About: held.about})
		}

		if held.kind != kindTrap {
			continue
		}

		trap := indexedTrap{ID: held.id, When: held.when}
		for _, anchored := range held.anchors {
			trap.Anchors = append(trap.Anchors, anchored.Path)
		}

		index.Traps = append(index.Traps, trap)
	}

	if len(index.Traps) == 0 && len(index.Notes) == 0 {
		mem.clearSlot(mem.trapIndexKey(seatName))

		return 0
	}

	_ = mem.writeSlot(mem.trapIndexKey(seatName), index)

	return len(index.Traps) + len(index.Notes)
}

// trapNotices tells the session, once per trap until its next compaction,
// about every trap the call touches: a file it anchors to, or a trigger its
// `--when` names.
func (mem *memory) trapNotices(ctx context.Context, seatName string, session string, call toolCall) []string {
	var index trapIndex
	if !mem.readSlot(mem.trapIndexKey(seatName), &index) {
		return nil
	}

	var shown shownSet
	mem.readSlot(mem.shownKey(session), &shown)

	matched := map[string]string{}

	var order []string

	noted := map[string]string{}

	var notes []string

	for _, note := range index.Notes {
		if slices.Contains(shown.Notes, note.ID) {
			continue
		}

		for _, path := range call.paths {
			if slices.Contains(note.About, path) {
				noted[note.ID] = path
				notes = append(notes, note.ID)

				break
			}
		}
	}

	for _, trap := range index.Traps {
		if slices.Contains(shown.Traps, trap.ID) {
			continue
		}

		what := trap.touchedBy(call)
		if what != "" {
			matched[trap.ID] = what
			order = append(order, trap.ID)
		}
	}

	if len(order) == 0 && len(notes) == 0 {
		return nil
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return nil
	}

	here := mem.checkout(ctx)

	var lines []string

	for _, id := range order {
		held, found := folded.entries[id]
		if !found {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s: trap for %s:", self, matched[id]),
			"  "+shortLine(here, held, seatName, mem.now()))
		shown.Traps = append(shown.Traps, id)
	}

	told := mem.hottest(folded, notes)
	if told != nil {
		lines = append(lines, fmt.Sprintf("%s: about %s: %s", self, noted[told.id], shortLine(here, told, seatName, mem.now())))
		shown.Notes = append(shown.Notes, told.id)
	}

	_ = mem.writeSlot(mem.shownKey(session), &shown)

	return lines
}

// hottest is the entry of ids with the most heat, the first named on a tie:
// one notice per call, so a call touching several notes tells the one most used.
func (mem *memory) hottest(folded *state, ids []string) *entry {
	heats := mem.heats()

	var best *entry

	bestHeat := 0.0

	for _, id := range ids {
		held, found := folded.entries[id]
		if !found {
			continue
		}

		heat := mem.heat(heats, held)
		if best == nil || heat > bestHeat {
			best, bestHeat = held, heat
		}
	}

	return best
}
