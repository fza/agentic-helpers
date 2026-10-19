// Package capture turns a draft file into a captured sdd graph entry.
//
// A draft opens with a YAML block carrying the fields `sdd new` takes, and its
// body follows. The body reaches `sdd new` as one argument of an argument
// vector, never through a shell word: `sdd new "$(cat body.md)"` re-evaluates
// the string in a second shell, so backticks in a body run as command
// substitution and every code identifier vanishes from the entry.
//
// Each gate stops the run in turn: the block answers what the call needs, the
// project's own lint reports nothing on the body, and the tool's pre-flight
// reports nothing of high severity. A medium finding reaches the caller and
// captures anyway, the rule the tool itself applies; a low one never reaches
// the caller, since nobody acts on one.
//
// A pre-flight the provider failed, rather than the draft, runs again: each try
// is cut off at a time limit, and a provider fault waits and retries a bounded
// number of times. The capture itself runs once.
package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

const usage = "usage: agentic-capture <draft.md> [--force] [--skip-preflight] [--preflight-limit <duration>]"

const LimitVariable = "CAPTURE_PREFLIGHT_LIMIT"

// sdd bounds a pre-flight call by `llm.timeout` (2m by default) and the
// extraction it falls back to by `llm.extract_timeout` (1m): twice the first
// plus the second leaves sdd's own limits to fire first.
const defaultLimit = 5 * time.Minute

var ErrDraft = errors.New("draft refused")

var severity = regexp.MustCompile(`^\s*\[(low|medium|high)\]`)

var retryDelays = []time.Duration{10 * time.Second, 30 * time.Second}

// A shape error, like a high finding, is a refusal of the draft and never
// retries, whatever else its output says.
var refusal = regexp.MustCompile(`validation failed`)

// What a provider fault reads as in sdd's output, and how the retry line names
// it. Statuses a retry cannot change (400, 401, 403, 404) are not faults.
var faults = []struct {
	pattern *regexp.Regexp
	names   string
}{
	{regexp.MustCompile(`no content or tool calls in response`), "answered nothing"},
	{regexp.MustCompile(`RateLimitError|status code 429|status 429`), "rate-limited the call"},
	{regexp.MustCompile(`API error(?:: status code| \[provider \S+ status) (408|409|5\d\d)`), "failed with status %s"},
	{regexp.MustCompile(`timed out`), "timed out"},
}

// Draft keys and the `sdd new` flag each becomes, in the order they are passed.
var (
	valueFlags = [][2]string{{"kind", "--kind"}, {"confidence", "--confidence"}, {"intent", "--intent"},
		{"canonical", "--canonical"}, {"actor", "--actor"}, {"class", "--class"}, {"summary", "--summary"}}
	listFlags = [][2]string{{"topics", "--topics"}, {"closes", "--closes"}, {"supersedes", "--supersedes"},
		{"participants", "--participants"}, {"aliases", "--aliases"}, {"actors", "--actors"}}
	jsonListFlags = [][2]string{{"refs", "--refs"}, {"involvement", "--involvement"}, {"topic", "--topic"}}
	jsonFlags     = [][2]string{{"when", "--when"}}
)

type Runner interface {
	Run(ctx context.Context, dir string, argv []string) (process.Result, error)
}

// Env is where a capture runs: the caller's working directory, what runs `sdd`
// and the project's lint, the value of LimitVariable (empty where unset), and
// what waits out a retry delay.
type Env struct {
	WorkingDir string
	Runner     Runner
	Limit      string
	Pause      func(ctx context.Context, delay time.Duration) error
}

type request struct {
	draft         string
	force         bool
	skipPreflight bool
	limit         time.Duration
}

type draft struct {
	front  map[string]any
	body   string
	offset int
}

type finding struct {
	severity string
	text     string
}

func parseArgs(args []string) (request, bool) {
	var held request

	for index := 0; index < len(args); index++ {
		arg := args[index]

		switch {
		case arg == "--force":
			held.force = true
		case arg == "--skip-preflight":
			held.skipPreflight = true
		case arg == "--preflight-limit" && index+1 < len(args):
			index++

			limit, valid := positiveDuration(args[index])
			if !valid {
				return request{}, false
			}

			held.limit = limit
		case strings.HasPrefix(arg, "-") || held.draft != "":
			return request{}, false
		default:
			held.draft = arg
		}
	}

	return held, held.draft != ""
}

func positiveDuration(text string) (time.Duration, bool) {
	duration, err := time.ParseDuration(text)

	return duration, err == nil && duration > 0
}

// Sleep waits out delay, or less where ctx ends first.
func Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting to retry the pre-flight: %w", ctx.Err())
	}
}

// Main captures one draft. Exit 2 is a pre-flight holding the draft back, 1
// every other refusal.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	held, parsed := parseArgs(args)
	if !parsed {
		_, _ = fmt.Fprintln(streams.Err, usage)

		return 1
	}

	if held.limit == 0 && env.Limit != "" {
		limit, valid := positiveDuration(env.Limit)
		if !valid {
			_, _ = fmt.Fprintf(streams.Err, "%s is %q, not a duration above zero such as 5m\n", LimitVariable, env.Limit)

			return 1
		}

		held.limit = limit
	}

	if held.limit == 0 {
		held.limit = defaultLimit
	}

	path := held.draft
	if !filepath.IsAbs(path) {
		path = filepath.Join(env.WorkingDir, path)
	}

	graph := graphconfig.GraphDir(filepath.Dir(path))
	if graph == "" {
		graph = graphconfig.GraphDir(env.WorkingDir)
	}

	if graph == "" {
		_, _ = fmt.Fprintln(streams.Err, "no .sdd graph governs this draft or this directory")

		return 1
	}

	run := &capture{env: env, streams: streams, root: filepath.Dir(graph), graph: graph, request: held, path: path}

	return run.do(ctx)
}

type capture struct {
	env     Env
	streams hookio.Streams
	root    string
	graph   string
	request request
	path    string
}

func (run *capture) say(format string, args ...any) {
	_, _ = fmt.Fprintf(run.streams.Err, format+"\n", args...)
}

func (run *capture) do(ctx context.Context) int {
	config, err := graphconfig.Load(run.graph)
	if err != nil {
		run.say("%v", err)

		return 1
	}

	held, err := parseDraft(run.path)
	if err != nil {
		run.say("%v", err)

		return 1
	}

	err = validate(held.front, config.ListingPrefix)
	if err != nil {
		run.say("%v", err)

		return 1
	}

	if config.DraftLint != "" {
		alerts, err := run.lint(ctx, config.DraftLint, held)
		if err != nil {
			run.say("%v", err)

			return 1
		}

		if len(alerts) > 0 {
			run.say("the draft lint reports on the body, so the pre-flight does not run:")

			for _, alert := range alerts {
				run.say("  %s", alert)
			}

			return 1
		}
	}

	if run.request.skipPreflight {
		run.say("capturing with no pre-flight, so the entry records that nothing checked it")

		return run.capture(ctx, held, "--skip-preflight")
	}

	return run.preflightThenCapture(ctx, held)
}

func (run *capture) preflightThenCapture(ctx context.Context, held draft) int {
	result, err := run.preflight(ctx, held)
	if errors.Is(err, errGaveUp) {
		return 1
	}

	if err != nil {
		run.say("%v", err)

		return 1
	}

	output := result.Stdout + result.Stderr
	found := findings(output)

	// sdd exits non-zero on a high finding, so a failed run carrying one is a
	// held draft, not a refused one.
	if result.Code != 0 && !slices.ContainsFunc(found, isHigh) {
		run.say("pre-flight refused the draft:\n%s", strings.TrimSpace(output))

		return 1
	}

	var blocking []string

	for _, each := range found {
		switch each.severity {
		case "medium":
			run.say("pre-flight notes, not blocking: %s", each.text)
		case "high":
			blocking = append(blocking, each.text)
		}
	}

	if len(blocking) > 0 && !run.request.force {
		run.say("pre-flight reports %d finding(s) of high severity:", len(blocking))

		for _, text := range blocking {
			run.say("  %s", text)
		}

		run.say("fix the draft and run again, or pass --force to capture anyway")

		return 2
	}

	for _, text := range blocking {
		run.say("capturing despite a high-severity finding: %s", text)
	}

	return run.capture(ctx, held, "--preflight-verified")
}

var errGaveUp = errors.New("the pre-flight's provider failed every try")

// preflight runs `sdd new --dry-run` until a try ends in something other than
// a provider fault, waiting out each delay in retryDelays between tries.
func (run *capture) preflight(ctx context.Context, held draft) (process.Result, error) {
	for attempt := 0; ; attempt++ {
		result, fault, err := run.try(ctx, held)
		if err != nil || fault == "" {
			return result, err
		}

		if attempt == len(retryDelays) {
			run.say("%s on all %d tries; the draft did not cause it, so capture it again later", fault, attempt+1)

			output := strings.TrimSpace(result.Stdout + result.Stderr)
			if output != "" {
				run.say("%s", output)
			}

			return result, errGaveUp
		}

		run.say("%s; trying again in %s", fault, retryDelays[attempt])

		err = run.env.Pause(ctx, retryDelays[attempt])
		if err != nil {
			return result, err
		}
	}
}

// try runs one pre-flight under the limit and names the provider fault that
// ended it, or nothing where the provider answered.
func (run *capture) try(ctx context.Context, held draft) (process.Result, string, error) {
	bounded, cancel := context.WithTimeout(ctx, run.request.limit)
	defer cancel()

	result, err := run.env.Runner.Run(bounded, run.root, command(held, "--dry-run"))

	if ctx.Err() != nil {
		return result, "", fmt.Errorf("the pre-flight was interrupted: %w", ctx.Err())
	}

	if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return result, fmt.Sprintf("the pre-flight gave no answer within %s", run.request.limit), nil
	}

	if err != nil || result.Code == 0 {
		return result, "", err
	}

	fault := providerFault(result.Stdout + result.Stderr)
	if fault == "" {
		return result, "", nil
	}

	return result, "the pre-flight's provider " + fault, nil
}

func providerFault(output string) string {
	if refusal.MatchString(output) || slices.ContainsFunc(findings(output), isHigh) {
		return ""
	}

	for _, fault := range faults {
		match := fault.pattern.FindStringSubmatch(output)
		if match == nil {
			continue
		}

		if len(match) > 1 {
			return fmt.Sprintf(fault.names, match[1])
		}

		return fault.names
	}

	return ""
}

// capture writes the entry. It runs once and never retries: the one model call
// in it, the summary, ends in a warning rather than a failure and comes before
// the write, so a failure here is a file or git one, after which the entry may
// already be on disk.
//
// `sdd new` writes and commits in one step, so two captures at once race for
// the git index and one dies leaving an entry uncommitted; the lock covers
// that step alone.
func (run *capture) capture(ctx context.Context, held draft, preflight string) int {
	unlock, err := run.lock()
	if err != nil {
		run.say("%v", err)

		return 1
	}

	defer unlock()

	result, err := run.env.Runner.Run(ctx, run.root, command(held, preflight))
	if err != nil {
		run.say("%v", err)

		return 1
	}

	_, _ = fmt.Fprint(run.streams.Out, result.Stdout)
	_, _ = fmt.Fprint(run.streams.Err, result.Stderr)

	return result.Code
}

func (run *capture) lock() (func(), error) {
	dir := filepath.Join(run.graph, "tmp")

	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("creating the lock directory: %w", err)
	}

	file, err := os.OpenFile(filepath.Join(dir, "capture.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the capture lock: %w", err)
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("taking the capture lock: %w", err)
	}

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// lint runs the project's command on the body, written next to the draft so
// the lint sees the path exclusions the draft itself has. A run that fails and
// says nothing never read the body, and an entry is immutable, so that blocks
// too.
func (run *capture) lint(ctx context.Context, lint string, held draft) ([]string, error) {
	body := filepath.Join(filepath.Dir(run.path), "."+filepath.Base(run.path)+".body.md")

	err := os.WriteFile(body, []byte(held.body+"\n"), 0o644)
	if err != nil {
		return nil, fmt.Errorf("writing the body for the lint: %w", err)
	}

	defer func() { _ = os.Remove(body) }()

	result, err := run.env.Runner.Run(ctx, run.root, []string{"sh", "-c", lint + ` "$@"`, "sh", body})
	if err != nil {
		return nil, fmt.Errorf("the draft lint did not run: %w", err)
	}

	if result.Code == 0 {
		return nil, nil
	}

	var alerts []string

	for line := range strings.SplitSeq(result.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			alerts = append(alerts, shift(line, body, run.path, held.offset))
		}
	}

	if len(alerts) == 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if len(stderr) > 200 {
			stderr = stderr[:200]
		}

		return nil, fmt.Errorf("%w: the draft lint exited %d and reported nothing, so the body went unread: %s",
			ErrDraft, result.Code, stderr)
	}

	return alerts, nil
}

// shift moves one `path:line:...` finding from the body file to the line the
// draft carries it on. A line in any other shape passes as it is.
func shift(alert string, body string, draft string, offset int) string {
	named := strings.Replace(alert, body, draft, 1)
	if named == alert {
		return alert
	}

	rest := strings.TrimPrefix(named, draft+":")
	line, after, found := strings.Cut(rest, ":")

	number, err := strconv.Atoi(line)
	if !found || err != nil {
		return named
	}

	return fmt.Sprintf("%s:%d:%s", draft, number+offset, after)
}

func parseDraft(path string) (draft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return draft{}, fmt.Errorf("%w: reading %s: %w", ErrDraft, path, err)
	}

	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return draft{}, fmt.Errorf("%w: %s opens with no --- line, so it carries no leading block", ErrDraft, path)
	}

	end := strings.Index(text[3:], "\n---\n")
	if end < 0 {
		return draft{}, fmt.Errorf("%w: %s carries no closing --- line for its leading block", ErrDraft, path)
	}

	end += 3

	front := map[string]any{}

	err = yaml.Unmarshal([]byte(text[4:end]), &front)
	if err != nil {
		return draft{}, fmt.Errorf("%w: %s's leading block is not a mapping of keys to values: %w", ErrDraft, path, err)
	}

	raw := text[end+5:]
	body := strings.TrimSpace(raw)

	if body == "" {
		return draft{}, fmt.Errorf("%w: %s carries no body below its leading block", ErrDraft, path)
	}

	// A lint finding names a line of the body, and a reader looks for it in
	// the draft.
	offset := strings.Count(text[:end+5], "\n") + strings.Count(raw[:len(raw)-len(strings.TrimLeft(raw, "\n"))], "\n")

	return draft{front: front, body: body, offset: offset}, nil
}

// validate refuses what `sdd new` cannot judge: the fields this call needs,
// and the project's topic prefix. Every value check belongs to `sdd new`.
func validate(front map[string]any, prefix string) error {
	for _, key := range []string{"type", "layer"} {
		if _, found := front[key]; !found {
			return fmt.Errorf("%w: the leading block needs a %s", ErrDraft, key)
		}
	}

	if prefix == "" {
		return nil
	}

	for _, topic := range listOf(front["topics"]) {
		if strings.HasPrefix(topic, prefix) {
			return nil
		}
	}

	return fmt.Errorf("%w: topics carry at least one label opening with %s, and these carry none", ErrDraft, prefix)
}

func listOf(value any) []string {
	switch held := value.(type) {
	case nil:
		return nil
	case string:
		var parts []string
		for part := range strings.SplitSeq(held, ",") {
			parts = append(parts, strings.TrimSpace(part))
		}

		return parts
	case []any:
		parts := make([]string, 0, len(held))
		for _, item := range held {
			parts = append(parts, fmt.Sprint(item))
		}

		return parts
	default:
		return []string{fmt.Sprint(held)}
	}
}

func items(value any) []any {
	held, isList := value.([]any)
	if isList {
		return held
	}

	if value == nil {
		return nil
	}

	return []any{value}
}

func jsonOf(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}

	return string(data)
}

// command is the argument vector `sdd new` runs, the body one argument of it.
func command(held draft, preflight string) []string {
	args := []string{"sdd", "new", fmt.Sprint(held.front["type"]), fmt.Sprint(held.front["layer"]), held.body}

	for _, pair := range valueFlags {
		value, found := held.front[pair[0]]
		if found {
			args = append(args, pair[1], fmt.Sprint(value))
		}
	}

	for _, pair := range listFlags {
		value, found := held.front[pair[0]]
		if found {
			args = append(args, pair[1], strings.Join(listOf(value), ","))
		}
	}

	for _, pair := range jsonListFlags {
		for _, item := range items(held.front[pair[0]]) {
			args = append(args, pair[1], jsonOf(item))
		}
	}

	for _, pair := range jsonFlags {
		value, found := held.front[pair[0]]
		if found {
			args = append(args, pair[1], jsonOf(value))
		}
	}

	for _, item := range items(held.front["attach"]) {
		args = append(args, "--attach", fmt.Sprint(item))
	}

	return append(args, preflight)
}

// findings pairs every pre-flight finding's severity with its whole text. A
// finding opens on its severity line and runs through the indented lines
// below it, so a wrapped finding stays one.
func findings(output string) []finding {
	var found []finding

	for line := range strings.SplitSeq(output, "\n") {
		match := severity.FindStringSubmatch(line)

		switch {
		case match != nil:
			found = append(found, finding{severity: match[1], text: strings.TrimSpace(line)})
		case len(found) > 0 && strings.TrimSpace(line) != "" && (line[0] == ' ' || line[0] == '\t'):
			found[len(found)-1].text += " " + strings.TrimSpace(line)
		}
	}

	return found
}

func isHigh(each finding) bool {
	return each.severity == "high"
}
