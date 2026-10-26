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
// A caller that already ran the pre-flight on the draft's exact bytes names
// their sha256, and the pre-flight is skipped while the entry still records it
// as verified.
//
// A pre-flight the provider failed, rather than the draft, runs again: each try
// is cut off at a time limit, and a provider fault waits and retries a bounded
// number of times. Where the caller names fallback routes, each then gets one
// try under its own model and configuration layer, and the first one answering
// decides. The capture itself runs once, on the caller's own configuration.
package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
)

const usage = "usage: agentic-capture <draft.md> [--force] [--skip-preflight] [--preflight-limit <duration>] [--preflight-route <model>=<layer>]... [--preflight-done <sha256>]"

// A caller computes the --preflight-done value itself, so the help names the
// bytes the hash covers exactly.
const help = usage + `
  --force                            capture despite a high-severity pre-flight finding
  --skip-preflight                   capture with no pre-flight; the entry records that nothing checked it
  --preflight-limit <duration>       cut off each pre-flight try after <duration>; default 5m, or ` + LimitVariable + `
  --preflight-route <model>=<layer>  once the caller's own configuration fails on a provider fault, try <model> under the sdd configuration layer <layer>, once; repeatable
  --preflight-done <sha256>          the sha256 of the draft file's bytes as read from disk, front matter included, as sha256sum <draft.md> prints it; on a match the pre-flight is skipped and the entry recorded as verified
`

const LimitVariable = "CAPTURE_PREFLIGHT_LIMIT"

// sdd bounds a pre-flight call by `llm.timeout` (2m by default) and the
// extraction it falls back to by `llm.extract_timeout` (1m): twice the first
// plus the second leaves sdd's own limits to fire first.
const defaultLimit = 5 * time.Minute

var ErrDraft = errors.New("draft refused")

var severity = regexp.MustCompile(`^\s*\[(low|medium|high)\]`)

var digest = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

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
		{"canonical", "--canonical"}, {"actor", "--actor"}, {"actor_kind", "--actor-kind"}, {"class", "--class"},
		{"summary", "--summary"}, {"title", "--title"}}
	listFlags = [][2]string{{"topics", "--topics"}, {"closes", "--closes"}, {"supersedes", "--supersedes"},
		{"participants", "--participants"}, {"aliases", "--aliases"}, {"actors", "--actors"}}
	jsonListFlags = [][2]string{{"refs", "--refs"}, {"involvement", "--involvement"}, {"topic", "--topic"}}
	jsonFlags     = [][2]string{{"when", "--when"}, {"index", "--index"}}
)

// acceptedKeys is every header key a draft may carry. A key outside it would
// be dropped on the way to `sdd new`, and the entry, immutable once written,
// would lack the field for good, so a draft carrying one is refused instead.
func acceptedKeys() []string {
	keys := []string{"type", "layer", "attach"}

	for _, table := range [][][2]string{valueFlags, listFlags, jsonListFlags, jsonFlags} {
		for _, pair := range table {
			keys = append(keys, pair[0])
		}
	}

	return keys
}

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
	// Settings and Getenv carry the plugin's options: capture's own switch, and
	// the grounding gate's, whose check a capture rests on.
	Settings []string
	Getenv   func(string) string
	// SddCache and LookPath let the probe of what sdd offers keep its answer
	// per binary; either left empty, sdd's help is read on every capture.
	SddCache string
	LookPath func(string) (string, error)
}

type request struct {
	draft         string
	force         bool
	skipPreflight bool
	preflightDone string
	limit         time.Duration
	routes        []route
	help          bool
}

// route is where a pre-flight runs: the caller's own sdd configuration where
// model is empty, else that model under the configuration layer at layer.
type route struct {
	model string
	layer string
}

func (via route) name() string {
	if via.model == "" {
		return "primary"
	}

	return via.model
}

func (via route) command(held draft) []string {
	args := command(held, "--dry-run")
	if via.model == "" {
		return args
	}

	return slices.Concat([]string{"sdd", "--local-config", via.layer}, args[1:len(args)-1],
		[]string{"--model", via.model, "--dry-run"})
}

type draft struct {
	front  map[string]any
	body   string
	offset int
	digest string
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
		case arg == "--help":
			return request{help: true}, true
		case arg == "--force":
			held.force = true
		case arg == "--skip-preflight":
			held.skipPreflight = true
		case arg == "--preflight-done" && index+1 < len(args):
			index++

			if !digest.MatchString(args[index]) {
				return request{}, false
			}

			held.preflightDone = strings.ToLower(args[index])
		case arg == "--preflight-limit" && index+1 < len(args):
			index++

			limit, valid := positiveDuration(args[index])
			if !valid {
				return request{}, false
			}

			held.limit = limit
		case arg == "--preflight-route" && index+1 < len(args):
			index++

			model, layer, found := strings.Cut(args[index], "=")
			if !found || model == "" || layer == "" {
				return request{}, false
			}

			held.routes = append(held.routes, route{model: model, layer: layer})
		case strings.HasPrefix(arg, "-") || held.draft != "":
			return request{}, false
		default:
			held.draft = arg
		}
	}

	if held.skipPreflight && held.preflightDone != "" {
		return request{}, false
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
	options, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		options = pluginconfig.Defaults()
	}

	switch {
	case !pieces.Capture.On(options):
		_, _ = fmt.Fprintln(streams.Err, pieces.Capture.Off())

		return 1
	case !pieces.CaptureGrounded.Holds(options):
		_, _ = fmt.Fprintln(streams.Err, pieces.CaptureGrounded.Off())

		return 1
	}

	held, parsed := parseArgs(args)
	if !parsed {
		_, _ = fmt.Fprintln(streams.Err, usage)

		return 1
	}

	if held.help {
		_, _ = fmt.Fprint(streams.Out, help)

		return 0
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

	for index, each := range held.routes {
		if !filepath.IsAbs(each.layer) {
			held.routes[index].layer = filepath.Join(env.WorkingDir, each.layer)
		}
	}

	governing := graphconfig.GraphDir(filepath.Dir(path))
	if governing == "" {
		governing = graphconfig.GraphDir(env.WorkingDir)
	}

	if governing == "" {
		_, _ = fmt.Fprintln(streams.Err, "no .sdd graph governs this draft or this directory")

		return 1
	}

	// sdd runs where the governing `.sdd/` sits and resolves a split layout's
	// graph repository itself; the project's settings and the capture lock
	// belong to the graph project, where every capture of it meets.
	graph := graphconfig.ResolveAt(filepath.Dir(governing)).Dir

	run := &capture{env: env, streams: streams, root: filepath.Dir(governing), graph: graph, request: held, path: path}

	return run.do(ctx)
}

type capture struct {
	env     Env
	streams hookio.Streams
	root    string
	graph   string
	request request
	path    string
	caps    sddcaps.Caps
	// single is a capture through an sdd without `--preflight-verified`: one
	// `sdd new` runs the pre-flight and writes, so no separate dry run comes
	// first and the lock covers the whole call.
	single bool
}

// forkOnly names what the draft or the call asks of sdd that this sdd lacks,
// or nothing where it has it all. A field sdd cannot write is refused rather
// than dropped: an entry written without it never gains it.
func (run *capture) forkOnly(held draft) string {
	needs := func(what string) string {
		return fmt.Sprintf("%s, which only sdd built from %s carries; install it with `%s`", what, sddcaps.Fork, sddcaps.ForkInstall)
	}

	_, title := held.front["title"]
	_, actorKind := held.front["actor_kind"]

	switch {
	case title && !run.caps.Title:
		return needs("the draft's header carries `title`, written through `sdd new --title`") + ", or drop the key"
	case actorKind && !run.caps.ActorKind:
		return needs("the draft's header carries `actor_kind`, written through `sdd new --actor-kind`") + ", or drop the key"
	case len(run.request.routes) > 0 && !run.caps.LocalConfig:
		return needs("--preflight-route runs sdd under `--local-config`")
	case run.request.preflightDone != "" && !run.caps.PreflightVerified:
		return needs("--preflight-done records the entry through `sdd new --preflight-verified`")
	}

	version := schemaOf(run.graph)
	if version > 1 && !run.caps.Migrate {
		return fmt.Sprintf("the graph is on schema %d, which only sdd built from %s writes; this sdd writes schema 1. Install the fork with `%s`",
			version, sddcaps.Fork, sddcaps.ForkInstall)
	}

	return ""
}

// schemaOf reads the graph's schema version from its `meta.json`, 0 where it
// names none.
func schemaOf(graph string) int {
	raw, err := os.ReadFile(filepath.Join(graph, "meta.json"))
	if err != nil {
		return 0
	}

	var meta struct {
		Version int `json:"graph_schema_version"`
	}

	err = json.Unmarshal(raw, &meta)
	if err != nil {
		return 0
	}

	return meta.Version
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

	// Before the lint or a pre-flight sees the draft: the pre-flight hands it
	// to a model provider.
	leaked := run.leaks(ctx, held)
	if leaked != "" {
		run.say("%s", leaked)

		return 1
	}

	run.caps = sddcaps.Probe{Runner: run.env.Runner, CacheDir: run.env.SddCache, LookPath: run.env.LookPath}.Of(ctx, run.root)
	if !run.caps.Runs {
		run.say("agentic-capture needs sdd on the PATH; %s. Install it with `%s`", run.caps.Printed, sddcaps.StockInstall)

		return 1
	}

	missing := run.forkOnly(held)
	if missing != "" {
		run.say("%s", missing)

		return 1
	}

	run.single = !run.caps.PreflightVerified

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

	// The caller's pre-flight is the same `sdd new --dry-run`, so a match is a
	// verified entry, never an unchecked one.
	if run.request.preflightDone != "" && run.request.preflightDone == held.digest {
		run.say("pre-flight skipped: the caller ran it on these exact bytes")

		return run.capture(ctx, held, "--preflight-verified")
	}

	if run.request.preflightDone != "" {
		run.say("--preflight-done names %s, but the draft hashes to %s; running the pre-flight", run.request.preflightDone, held.digest)
	}

	return run.preflightThenCapture(ctx, held)
}

func (run *capture) preflightThenCapture(ctx context.Context, held draft) int {
	if run.single {
		unlock, err := run.lock()
		if err != nil {
			run.say("%v", err)

			return 1
		}

		defer unlock()

		// This sdd's pre-flight writes the entry too.
		leaked := run.leaks(ctx, held)
		if leaked != "" {
			run.say("%s", leaked)

			return 1
		}
	}

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

	if result.Code != 0 && run.refusedForSchema(output) {
		return 1
	}

	// sdd exits non-zero on a high finding, so a failed run carrying one is a
	// held draft, not a refused one.
	if result.Code != 0 && !slices.ContainsFunc(found, isHigh) {
		if run.single {
			run.say("sdd new failed; it checks and writes in one call, so check `git log` before capturing again:\n%s", strings.TrimSpace(output))

			return 1
		}

		run.say("pre-flight refused the draft:\n%s", strings.TrimSpace(output))

		return 1
	}

	var blocking []string

	for _, each := range found {
		switch each.severity {
		case "medium":
			if !run.single {
				run.say("pre-flight notes, not blocking: %s", each.text)
			}
		case "high":
			blocking = append(blocking, each.text)
		}
	}

	// The single call wrote the entry where nothing blocked it, and its own
	// output already carries every finding.
	if run.single && len(blocking) == 0 {
		_, _ = fmt.Fprint(run.streams.Out, result.Stdout)
		_, _ = fmt.Fprint(run.streams.Err, result.Stderr)

		return 0
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

	// The single call refused the entry on its finding; this sdd has no way to
	// record a checked one, so the forced write records that nothing checked it.
	if run.single {
		run.say("this sdd writes the forced entry with --skip-preflight, so the entry records that nothing checked it")

		return run.write(ctx, held, "--skip-preflight")
	}

	return run.capture(ctx, held, "--preflight-verified")
}

var errGaveUp = errors.New("the pre-flight's provider failed every try")

// sdd refuses every write, `--dry-run` included, to a graph whose schema is
// older than its own, and names `sdd migrate` as the fix. The draft has no
// part in it, so the refusal is told apart from one the draft caused.
const schemaRefusal = "refusing to write — graph incompatible with this binary"

func (run *capture) refusedForSchema(output string) bool {
	if !strings.Contains(output, schemaRefusal) {
		return false
	}

	if !run.caps.Migrate {
		run.say("sdd refuses to write this graph; the draft did not cause this:\n%s", strings.TrimSpace(output))

		return true
	}

	run.say("the graph needs `sdd migrate` before sdd writes to it; the draft did not cause this, so run `sdd migrate` and capture again:\n%s",
		strings.TrimSpace(output))

	return true
}

// preflight runs `sdd new --dry-run` on the caller's own configuration, then
// on each fallback route in turn, until a try ends in something other than a
// provider fault.
func (run *capture) preflight(ctx context.Context, held draft) (process.Result, error) {
	result, fault, err := run.retried(ctx, held)
	if err != nil {
		return result, err
	}

	if fault == "" {
		run.answered(route{})

		return result, nil
	}

	tries := len(retryDelays) + 1

	if len(run.request.routes) == 0 {
		run.say("%s on all %d tries; the draft did not cause it, so capture it again later", fault, tries)

		output := strings.TrimSpace(result.Stdout + result.Stderr)
		if output != "" {
			run.say("%s", output)
		}

		return result, errGaveUp
	}

	run.say("%s on all %d tries; trying each fallback route once", fault, tries)

	passedOver := []string{lastError(route{}, fault, result)}

	for _, via := range run.request.routes {
		reason := unusable(via.layer)
		if reason != "" {
			run.say("route %s skipped: %s %s", via.model, via.layer, reason)

			continue
		}

		result, fault, err = run.try(ctx, held, via)
		if err != nil {
			return result, err
		}

		if fault == "" {
			run.answered(via)

			return result, nil
		}

		passedOver = append(passedOver, lastError(via, fault, result))
	}

	for _, line := range passedOver {
		run.say("%s", line)
	}

	run.say("the pre-flight's provider failed on every route; the draft did not cause it, so capture it again later")

	return result, errGaveUp
}

// retried runs the pre-flight on the caller's own configuration until a try
// ends in something other than a provider fault, waiting out each delay in
// retryDelays between tries, and names the fault the last try ended in.
func (run *capture) retried(ctx context.Context, held draft) (process.Result, string, error) {
	for attempt := 0; ; attempt++ {
		result, fault, err := run.try(ctx, held, route{})
		if err != nil || fault == "" || attempt == len(retryDelays) {
			return result, fault, err
		}

		run.say("%s; trying again in %s", fault, retryDelays[attempt])

		err = run.env.Pause(ctx, retryDelays[attempt])
		if err != nil {
			return result, "", err
		}
	}
}

// answered names the route that decided the pre-flight, ahead of the capture's
// own output. A caller naming no fallback route gets no such line, so its
// output stays what it was before routes existed.
func (run *capture) answered(via route) {
	if len(run.request.routes) == 0 {
		return
	}

	_, _ = fmt.Fprintf(run.streams.Out, "AGENTIC_CAPTURE_ROUTE %s\n", via.name())
}

// unusable says why a route's layer is passed over, or nothing where it may be
// used. The layer holds the provider's API key, so one the caller does not own,
// or one others can read, is refused. It is opened, never read.
func unusable(layer string) string {
	info, err := os.Stat(layer)
	if errors.Is(err, fs.ErrNotExist) {
		return "is missing"
	}

	if err != nil || !info.Mode().IsRegular() {
		return "is not a readable file"
	}

	owner, known := info.Sys().(*syscall.Stat_t)
	if !known || int(owner.Uid) != os.Getuid() {
		return "is not the caller's own"
	}

	if info.Mode().Perm()&0o044 != 0 {
		return "is readable by group or others"
	}

	file, err := os.Open(layer)
	if err != nil {
		return "is not a readable file"
	}

	_ = file.Close()

	return ""
}

func lastError(via route, fault string, result process.Result) string {
	lines := strings.Split(strings.TrimSpace(result.Stdout+result.Stderr), "\n")

	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return fmt.Sprintf("route %s: %s", via.name(), fault)
	}

	return fmt.Sprintf("route %s: %s: %s", via.name(), fault, last)
}

// try runs one pre-flight under the limit and names the provider fault that
// ended it, or nothing where the provider answered.
func (run *capture) try(ctx context.Context, held draft, via route) (process.Result, string, error) {
	bounded, cancel := context.WithTimeout(ctx, run.request.limit)
	defer cancel()

	argv := via.command(held)
	if run.single {
		argv = command(held, "")
	}

	result, err := run.env.Runner.Run(bounded, run.root, argv)

	if ctx.Err() != nil {
		return result, "", fmt.Errorf("the pre-flight was interrupted: %w", ctx.Err())
	}

	// A single call cut off by the limit may have written already, so it is
	// never tried again.
	if errors.Is(bounded.Err(), context.DeadlineExceeded) && run.single {
		return result, "", fmt.Errorf("sdd new gave no answer within %s; it checks and writes in one call, so check `git log` before capturing again", run.request.limit)
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

	return run.write(ctx, held, preflight)
}

// write runs the capture step under a lock its caller holds, scanning what it
// hands sdd once more first: an attached file may have changed during the
// pre-flight.
func (run *capture) write(ctx context.Context, held draft, preflight string) int {
	leaked := run.leaks(ctx, held)
	if leaked != "" {
		run.say("%s", leaked)

		return 1
	}

	result, err := run.env.Runner.Run(ctx, run.root, command(held, preflight))
	if err != nil {
		run.say("%v", err)

		return 1
	}

	if result.Code != 0 && run.refusedForSchema(result.Stdout+result.Stderr) {
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

	// Hashing the read the draft is parsed from leaves no window for the file
	// to change between the hash and the capture.
	sum := sha256.Sum256(data)

	return draft{front: front, body: body, offset: offset, digest: hex.EncodeToString(sum[:])}, nil
}

// validate refuses what `sdd new` cannot judge: the fields this call needs,
// and the project's topic prefix. Every value check belongs to `sdd new`.
func validate(front map[string]any, prefix string) error {
	for _, key := range []string{"type", "layer"} {
		if _, found := front[key]; !found {
			return fmt.Errorf("%w: the leading block needs a %s", ErrDraft, key)
		}
	}

	accepted := acceptedKeys()

	var unknown []string

	for key := range front {
		if !slices.Contains(accepted, key) {
			unknown = append(unknown, key)
		}
	}

	if unknown != nil {
		slices.Sort(unknown)

		return fmt.Errorf("%w: the leading block carries %s, which reaches no `sdd new` flag; it takes %s",
			ErrDraft, strings.Join(unknown, ", "), strings.Join(accepted, ", "))
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

	if preflight == "" {
		return args
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
