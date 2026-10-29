package tools

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
)

// recallLimit bounds the prompt embed as agentic-memory bounds its own: a
// timeout adds nothing.
const recallLimit = 5 * time.Second

const offerSize = 3

var scriptExtensions = []string{".py", ".sh", ".go", ".awk"}

var hooks = map[string]func(lib *library, ctx context.Context) int{
	"prompt": (*library).prompt,
	"notice": (*library).notice,
	"stop":   (*library).stop,
	"end":    (*library).end,
	"start":  (*library).start,
}

// hookPayload reads the event and takes its session. A hook never fails its
// event: whatever goes wrong, it adds nothing. A machine holding no library,
// which the first `add` creates, hears nothing and gets nothing created.
func (lib *library) hookPayload(ctx context.Context) (hookio.Payload, bool) {
	if len(storeIDs(lib.env.Roots)) == 0 {
		return hookio.Payload{}, false
	}

	payload, err := hookio.ReadPayload(lib.streams.In)
	if err != nil {
		return hookio.Payload{}, false
	}

	lib.session = cmp.Or(payload.SessionID, lib.session)

	usable := lib.session != "" && !lib.outsideGit && sessionShape.MatchString(lib.session)

	// Every hook writes the session's file into the own store: a checkout
	// matching no store gets one here, and one whose store is the user's call
	// hears nothing until it is answered.
	return payload, usable && lib.recorder(ctx) == nil
}

// prompt delivers the nudges stop recorded, then offers up to three own tools
// near the prompt, each once per session.
func (lib *library) prompt(ctx context.Context) int {
	payload, usable := lib.hookPayload(ctx)
	if !usable || !exists(lib.ownDir()) {
		return 0
	}

	var lines []string

	pending := lib.takeNudges()
	if pending != "" {
		lines = append(lines, pending)
	}

	offered := lib.offer(ctx, payload.Prompt)
	if offered != "" {
		lines = append(lines, offered)
	}

	lib.say(strings.Join(lines, "\n"))

	return 0
}

func (lib *library) offer(ctx context.Context, prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}

	own := lib.ownOnly()
	if len(own.index.Tools) == 0 {
		return ""
	}

	embedder, err := lib.embedder()
	if err != nil {
		return ""
	}

	// The prompt is embedded only where some own tool not yet shown has a
	// vector to compare it with.
	snapshot, err := lib.vectorStore(embedder.Fingerprint()).Snapshot()
	if err != nil {
		return ""
	}

	defer func() { _ = snapshot.Close() }()

	// The offer names tools only, so it never counts as printing their lines;
	// it skips what find or list printed, and what it named itself, since the
	// context last started afresh.
	seen := lib.presenting(ctx, false)

	var candidates []string

	wanted := map[string]bool{}

	for _, name := range own.names() {
		hit := found{store: own, tool: own.index.Tools[name]}
		described := hit.tool.Described

		offered := seen != nil && (slices.Contains(seen.set.Offered, name) || slices.Contains(seen.set.Lines, versionKey(hit)))
		if !offered && snapshot.Holds(described, described) {
			candidates = append(candidates, name)
			wanted[described] = true
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	bounded, cancel := context.WithTimeout(ctx, recallLimit)
	defer cancel()

	vector, _, err := lib.embedPrompt(bounded, prompt)
	if err != nil {
		return ""
	}

	scores, err := snapshot.Scores(vector, func(id string, _ string) bool { return wanted[id] })
	if err != nil {
		return ""
	}

	role := lib.role(ctx)

	type scored struct {
		name     string
		score    float32
		sameRole bool
	}

	var near []scored

	for _, name := range candidates {
		tool := own.index.Tools[name]

		score := scores[tool.Described]
		if float64(score) >= lib.options.ToolsQueryFloor {
			near = append(near, scored{name: name, score: score, sameRole: role != "" && slices.Contains(tool.Roles, role)})
		}
	}

	slices.SortStableFunc(near, func(a, b scored) int {
		if a.sameRole != b.sameRole {
			if a.sameRole {
				return -1
			}

			return 1
		}

		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}

		return strings.Compare(a.name, b.name)
	})

	near = near[:min(offerSize, len(near))]
	if len(near) == 0 {
		return ""
	}

	var named []string
	for _, hit := range near {
		named = append(named, hit.name)

		if seen != nil {
			seen.set.Offered = append(seen.set.Offered, hit.name)
			seen.changed = true
		}
	}

	seen.save()

	return fmt.Sprintf("tool library: near this prompt: %s. `%s take <name>` copies one", strings.Join(named, ", "), self)
}

// ownOnly is the own store with its index, without listing any other store:
// the offer names own tools alone.
func (lib *library) ownOnly() *store {
	held := &store{key: lib.key, path: lib.ownDir(), own: true}
	held.index = lib.indexOf(context.Background(), held)

	return held
}

// embedPrompt reuses the vector agentic-memory left for this prompt where it
// finished first, and leaves its own for agentic-memory otherwise. A prompt
// holding a credential is never embedded, and the offer stays silent on it:
// agentic-memory's recall tells the agent once.
func (lib *library) embedPrompt(ctx context.Context, prompt string) ([]float32, string, error) {
	if promptvector.Withheld(ctx, prompt) != "" {
		return nil, "", ErrPromptWithheld
	}

	embedder, err := lib.embedder()
	if err != nil {
		return nil, "", err
	}

	fingerprint := embedder.Fingerprint()

	cached, hit := promptvector.Load(lib.promptVectors(), lib.session, prompt, fingerprint)
	if hit {
		return cached, fingerprint, nil
	}

	vectors, err := embedder.Embed(ctx, []string{prompt})
	if err != nil || len(vectors) != 1 {
		return nil, "", fmt.Errorf("embedding the prompt: %w", cmp.Or(err, fmt.Errorf("no vector answered")))
	}

	_ = promptvector.Save(lib.promptVectors(), lib.session, prompt, fingerprint, vectors[0])

	return vectors[0], fingerprint, nil
}

func (lib *library) embedText(ctx context.Context, phrase string) ([]float32, string, error) {
	embedder, err := lib.embedder()
	if err != nil {
		return nil, "", err
	}

	bounded, cancel := context.WithTimeout(ctx, embedLimit)
	defer cancel()

	vectors, err := embedder.Embed(bounded, []string{phrase})
	if err != nil {
		return nil, "", fmt.Errorf("embedding the query: %w", err)
	}

	if len(vectors) != 1 {
		return nil, "", fmt.Errorf("the embedder answered no vector")
	}

	return vectors[0], embedder.Fingerprint(), nil
}

// notice tells a working session after a tool call of newer versions of what
// it holds, each once, and a fork's holders of newer versions of its origin.
// Nothing is read beyond the session file and the indexes' stats when neither
// moved since the last look.
func (lib *library) notice(ctx context.Context) int {
	_, usable := lib.hookPayload(ctx)
	if !usable {
		return 0
	}

	state := lib.loadSession()
	if len(state.Holdings) == 0 {
		return 0
	}

	signature := lib.noticeSignature(state)
	if signature == state.Signature {
		return 0
	}

	stores := map[string]*store{}
	for _, held := range lib.stores(ctx) {
		stores[held.key] = held
	}

	var (
		newer   []string
		origins []string
	)

	for _, held := range state.Holdings {
		home, present := stores[held.Project]
		if !present {
			continue
		}

		tool, present := home.tool(held.Tool)
		if !present {
			continue
		}

		key := holdingKey(held.Project, held.Tool)
		if tool.Version > held.Version && tool.Version > state.Notified[key] {
			newer = append(newer, found{store: home, tool: tool}.shown())
			state.Notified[key] = tool.Version
		}

		if tool.ForkedFrom == nil {
			continue
		}

		origin, present := stores[tool.ForkedFrom.Project]
		if !present {
			continue
		}

		source, present := origin.tool(tool.ForkedFrom.Tool)
		originKey := holdingKey(tool.ForkedFrom.Project, "origin\x00"+tool.ForkedFrom.Tool)

		if present && source.Version > tool.ForkedFrom.V && source.Version > state.Notified[originKey] {
			origins = append(origins, fmt.Sprintf("tool library: `%s` in %s has v%d; yours forked at v%d",
				tool.ForkedFrom.Tool, lib.storeName(tool.ForkedFrom.Project), source.Version, tool.ForkedFrom.V))
			state.Notified[originKey] = source.Version
		}
	}

	var lines []string

	if len(newer) > 0 {
		quoted := make([]string, 0, len(newer))
		for _, name := range newer {
			quoted = append(quoted, "`"+name+"`")
		}

		lines = append(lines, fmt.Sprintf("tool library: newer versions of %s. `%s take %s` copies them",
			strings.Join(quoted, ", "), self, strings.Join(newer, " ")))
	}

	lines = append(lines, origins...)

	state.Signature = ""
	_ = lib.saveSession(state)
	state.Signature = lib.noticeSignature(state)
	_ = lib.saveSession(state)

	if len(lines) > 0 {
		_ = hookio.AddContext(lib.streams.Out, "PostToolUse", strings.Join(lines, "\n"))
	}

	return 0
}

// noticeSignature is the stats of the indexes the holdings live in and of
// their forks' origins: what a newer version would have to move.
func (lib *library) noticeSignature(state *sessionState) string {
	projects := []string{}

	for _, held := range state.Holdings {
		if !slices.Contains(projects, held.Project) {
			projects = append(projects, held.Project)
		}
	}

	if !slices.Contains(projects, lib.key) {
		projects = append(projects, lib.key)
	}

	slices.Sort(projects)

	parts := make([]string, 0, len(projects))

	for _, project := range projects {
		info, err := os.Lstat(filepath.Join(lib.env.Roots.Store(project, home.Tools), "index.json"))
		if err != nil {
			parts = append(parts, project+"=-")

			continue
		}

		parts = append(parts, fmt.Sprintf("%s=%d@%d", project, info.Size(), info.ModTime().UnixNano()))
	}

	if lib.scopeProject() {
		parts = append(parts, "scope=project")
	}

	return strings.Join(parts, " ")
}

// stop records, without blocking, scripts in the session's scratch no tool
// holds and taken copies changed since the take, for the next prompt to say.
func (lib *library) stop(ctx context.Context) int {
	_, usable := lib.hookPayload(ctx)
	if !usable {
		return 0
	}

	state := lib.loadSession()
	before := len(state.Pending)

	known, names := lib.knownFiles(ctx)

	// A script counts as recorded once its key is among Nudged: the line naming
	// it is pending, and goes out with the next prompt.
	var fresh []string

	for _, script := range lib.scratchScripts(ctx) {
		key := "script\x00" + script.path + "\x00" + script.sum
		if known[script.sum] || names[filepath.Base(script.path)] || slices.Contains(state.Nudged, key) {
			continue
		}

		fresh = append(fresh, filepath.Base(script.path))
		state.Nudged = append(state.Nudged, key)
	}

	if len(fresh) > 0 {
		state.addPending(nudge{
			Key: "scripts\x00" + strings.Join(fresh, ","),
			Line: fmt.Sprintf("tool library: your scratch holds scripts the library lacks: %s. "+
				"`%s add <name> --from <path> --summary '...' --usage '...'` registers one", strings.Join(fresh, ", "), self),
		})
	}

	for _, held := range state.Holdings {
		changed, current := copyChanged(held)
		if !changed {
			continue
		}

		state.addPending(nudge{
			Key: "copy\x00" + held.Copy + "\x00" + current,
			Line: fmt.Sprintf("tool library: you changed your copy of `%s` (%s): `%s update %s --from %s --why '...'`",
				held.Tool, held.Copy, self, held.Tool, held.Copy),
		})
	}

	if len(state.Pending) != before || len(fresh) > 0 {
		_ = lib.saveSession(state)
	}

	return 0
}

type script struct {
	path string
	sum  string
}

// scratchScripts lists the scripts in the session's scratch folder, top level
// and below, read with the same hardening as any store.
func (lib *library) scratchScripts(ctx context.Context) []script {
	at := strings.TrimSpace(lib.getenv("AGENTIC_TOOLS_SCRATCH"))
	if at == "" {
		at = filepath.Join(lib.checkout(ctx), ".tmp", "claude", lib.session)
	}

	root, err := openDir(at)
	if err != nil {
		return nil
	}

	defer root.close()

	files, err := root.walk(toolLimit*4, nil)
	if err != nil {
		return nil
	}

	var found []script

	for _, file := range files {
		if slices.Contains(scriptExtensions, filepath.Ext(file.name)) {
			found = append(found, script{path: filepath.Join(at, filepath.FromSlash(file.name)), sum: sum(file.data)})
		}
	}

	return found
}

// knownFiles is every file hash and file name any version of any tool in
// reach holds.
func (lib *library) knownFiles(ctx context.Context) (map[string]bool, map[string]bool) {
	sums, names := map[string]bool{}, map[string]bool{}

	for _, held := range lib.stores(ctx) {
		for _, tool := range held.index.Tools {
			for _, hashes := range tool.Hashes {
				for _, hash := range hashes {
					sums[hash.Sum] = true
					names[filepath.Base(hash.Name)] = true
				}
			}
		}
	}

	return sums, names
}

// copyChanged says whether a taken copy's files differ from the version taken,
// and a digest of how they stand now.
func copyChanged(held holding) (bool, string) {
	root, err := openDir(held.Copy)
	if err != nil {
		return false, ""
	}

	defer root.close()

	files, err := root.walk(versionLimit, func(name string) bool { return name == "tool.json" })
	if err != nil {
		return false, ""
	}

	now := map[string]string{}

	var digest strings.Builder

	for _, file := range files {
		now[file.name] = sum(file.data)
	}

	for _, name := range sortedKeys(now) {
		digest.WriteString(name + "=" + now[name] + ";")
	}

	if len(now) != len(held.Files) {
		return true, sum([]byte(digest.String()))
	}

	for _, taken := range held.Files {
		if now[taken.Name] != taken.Sum {
			return true, sum([]byte(digest.String()))
		}
	}

	return false, ""
}

// end removes the session's file, its prompt vector slot and what its
// context held.
func (lib *library) end(ctx context.Context) int {
	_, usable := lib.hookPayload(ctx)
	if !usable {
		return 0
	}

	_ = os.Remove(lib.sessionPath())
	promptvector.Remove(lib.promptVectors(), lib.session)
	lib.forgetSeen(ctx)

	return 0
}

// start empties what the context held where it starts afresh: a compaction,
// a `/clear` or a resume.
func (lib *library) start(ctx context.Context) int {
	payload, usable := lib.hookPayload(ctx)
	if usable && slices.Contains([]string{"compact", "clear", "resume"}, payload.Source) {
		lib.forgetSeen(ctx)
	}

	return 0
}
