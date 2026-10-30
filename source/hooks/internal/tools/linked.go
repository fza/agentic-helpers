package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
)

// Linked is the library as agentic-memory reaches it for the tools an entry
// names. A link holds the tool's name, with its store's key for another
// project's tool: a label is worked out against every store in reach and
// changes as projects come and go, a key does not. It never holds a version.
type Linked struct {
	lib *library
	off string
}

// LinkedTake is one named tool as `show` takes it: the version copied and
// where to, its usage, or why it was not taken. Seen says the calling
// context already holds its usage.
type LinkedTake struct {
	Name     string
	Version  int
	Copy     string
	Usage    string
	Foreign  bool
	Seen     bool
	NotTaken string
}

// OpenLinked is the library for the session env describes, located as a
// call of agentic-tools locates it.
func OpenLinked(ctx context.Context, env Env) *Linked {
	lib := &library{env: env, streams: hookio.Streams{}, session: env.SessionID}
	lib.options = lib.readOptions()

	if !pieces.Tools.On(lib.options) {
		return &Linked{lib: lib, off: pieces.Tools.Off()}
	}

	lib.locate(ctx)

	return &Linked{lib: lib, off: lib.callRefusal()}
}

// Resolve is the form a link to the tool given stores, a merged name
// reaching its survivor, or why given names no tool.
func (linked *Linked) Resolve(ctx context.Context, given string) (string, error) {
	if linked.off != "" {
		return "", fmt.Errorf("%s: %s", given, linked.off)
	}

	hit, err := linked.lib.resolve(ctx, given)
	if err != nil {
		return "", err
	}

	return stored(hit), nil
}

func stored(hit found) string {
	if hit.store.own {
		return hit.tool.Name
	}

	return hit.tool.Name + "@" + hit.store.key
}

// reached is the tool a stored link names, by its own name or, where it was
// merged, by its survivor's; ok is false where the tool or its store is gone.
func (linked *Linked) reached(ctx context.Context, link string) (found, bool) {
	name, key, foreign := strings.Cut(link, "@")

	for _, held := range linked.lib.stores(ctx) {
		if (foreign && held.key == key) || (!foreign && held.own) {
			return reach(held, name)
		}
	}

	return found{}, false
}

// Name is how a line names the tool a stored link names: its name, with its
// project's label for another project's tool.
func (linked *Linked) Name(ctx context.Context, link string) string {
	name, key, foreign := strings.Cut(link, "@")
	if !foreign || linked.off != "" {
		return name
	}

	for _, held := range linked.lib.stores(ctx) {
		if held.key == key {
			return name + "@" + held.label
		}
	}

	return link
}

// State is what became of the tool a stored link names: empty while it is
// in the library, `gone` once it or its store is, `merged into <survivor>`
// once merged. Nothing is known while the library is off or unreachable.
func (linked *Linked) State(ctx context.Context, link string) string {
	if linked.off != "" {
		return ""
	}

	hit, ok := linked.reached(ctx, link)

	switch {
	case !ok:
		return "gone"
	case hit.via != "":
		return "merged into " + hit.shown()
	}

	return ""
}

// Take copies the current version of every tool the links name into the
// checkout, as `take` copies, and records the session's holding so `update
// --from` works on the copy. It records no take: a `show` nobody followed up
// does not lift a tool in `find`. A version whose usage the calling context
// already holds comes back Seen, unless again is set.
func (linked *Linked) Take(ctx context.Context, links []string, again bool) []LinkedTake {
	takes := make([]LinkedTake, 0, len(links))

	if linked.off != "" {
		for _, link := range links {
			takes = append(takes, LinkedTake{Name: linked.Name(ctx, link), NotTaken: linked.off})
		}

		return takes
	}

	lib := linked.lib
	base := lib.copyBase(ctx)
	inside := strings.HasPrefix(base, lib.checkout(ctx)+string(filepath.Separator))
	writes := lib.recorder(ctx) == nil
	state := lib.loadSession(ctx)
	seen := lib.presenting(ctx, again)

	for _, link := range links {
		hit, ok := linked.reached(ctx, link)
		if !ok {
			takes = append(takes, LinkedTake{Name: linked.Name(ctx, link), NotTaken: "gone from the library"})

			continue
		}

		tool := hit.tool
		take := LinkedTake{Name: hit.shown(), Version: tool.Version, Usage: tool.Usage, Foreign: !hit.store.own}

		if !writes && !inside {
			take.NotTaken = fmt.Sprintf("this checkout is bound to no store yet and takes only into its own .tmp/: "+
				"create %s", filepath.Join(lib.checkout(ctx), ".tmp"))
			takes = append(takes, take)

			continue
		}

		take.Copy = filepath.Join(base, copyName(hit), versionDir(tool.Version))
		if !exists(take.Copy) {
			err := lib.copyVersion(ctx, hit, take.Copy)
			if err != nil {
				take.Copy, take.NotTaken = "", fmt.Sprintf("copying v%d: %v", tool.Version, err)
				takes = append(takes, take)

				continue
			}
		}

		state.hold(holding{Project: hit.store.key, Tool: tool.Name, Version: tool.Version, Copy: take.Copy,
			Files: tool.Hashes[versionDir(tool.Version)]})
		state.Notified[holdingKey(hit.store.key, tool.Name)] = tool.Version

		take.Seen = seen.printedUsage(hit)
		takes = append(takes, take)
	}

	if writes {
		_ = lib.saveSession(ctx, state)
	}

	seen.save()

	return takes
}
