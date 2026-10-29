package tools

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

var longOption = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// versionFiles reads a stored version's files, its `tool.json` left out.
func versionFiles(held *store, name string, v int) ([]walked, error) {
	root, err := openDir(held.path)
	if err != nil {
		return nil, err
	}

	defer root.close()

	folder, err := root.subPath(name + "/" + versionDir(v))
	if err != nil {
		return nil, err
	}

	defer folder.close()

	return folder.walk(versionLimit, func(entry string) bool { return entry == "tool.json" })
}

func (lib *library) update(ctx context.Context, req request) result {
	if strings.TrimSpace(req.Why) == "" || clean(req.Why, false) != req.Why {
		return refused("--why is required: one line saying what changed")
	}

	hit, err := lib.resolve(ctx, req.Name)
	if err != nil {
		return refused("%v", err)
	}

	old := hit.tool

	previous, err := versionFiles(hit.store, old.Name, old.Version)
	if err != nil {
		return refused("reading %s v%d: %v", hit.shown(), old.Version, err)
	}

	next := old.spec()
	next.ForkedFrom = nil

	for field, value := range map[*string]*string{&next.Summary: req.Summary, &next.Usage: req.Usage,
		&next.Inputs: req.Inputs, &next.Outputs: req.Outputs} {
		if value != nil {
			*field = *value
		}
	}

	files := previous

	if req.From != "" {
		from, err := lib.readSource(ctx, req.From)
		if err != nil {
			return refused("%v", err)
		}

		files = from.files
	}

	err = checkFields(next)
	if err != nil {
		return refused("%v", err)
	}

	if sameFiles(previous, files) && describe(next) == describe(old.spec()) {
		return refused("nothing changes: give --from, --summary, --usage, --inputs or --outputs")
	}

	if req.Usage == nil {
		stale := staleUsage(old.Usage, previous, files)
		if stale != "" {
			return refused("the usage names %s, which the new version no longer holds: give --usage", stale)
		}
	}

	// A fork keeps the foreign version's files and description as its first
	// version, so they are scanned as well as the next, before anything is
	// embedded.
	scanned := files
	fields := append(describedFields(next), labelled{label: "--why", text: req.Why})

	if !hit.store.own {
		scanned = append(slices.Clone(files), previous...)
		fields = append(fields, describedFields(old.spec())...)
	}

	credential := credentialIn(ctx, scanned, fields)
	if credential != "" {
		return refused("%s", credential)
	}

	vector, _ := lib.embedOne(ctx, next)

	if hit.store.own {
		next.ForkedFrom = old.ForkedFrom
		number := old.Version + 1

		err = lib.locked(ctx, func() error {
			err := lib.makeRoom(ctx, walkedBytes(files))
			if err != nil {
				return err
			}

			return lib.writeVersion(ctx, version{name: old.Name, number: number, files: files, spec: next, why: req.Why,
				vector: vector})
		})
		if err != nil {
			return refused("%v", err)
		}

		lib.moveHolding(ctx, req.From, hit, old.Name, number)

		return done(fmt.Sprintf("updated %s to v%d, now current\n%s", old.Name, number, space.Line(lib.recordSpace(ctx)))).
			with(updatedFields{Name: old.Name, Version: number})
	}

	return lib.fork(ctx, req, hit, previous, files, next, vector)
}

// fork copies a foreign tool's current version into the own store as v1, then
// lays the update over it as v2. The origin is never written.
func (lib *library) fork(ctx context.Context, req request, hit found, previous []walked, files []walked, next spec,
	vector []float32,
) result {
	old := hit.tool

	own := lib.own(ctx)

	_, taken := own.tool(old.Name)
	_, merged := own.index.Merged[old.Name]

	if taken || merged {
		return refused("%s is already a name in this project; give the foreign tool as %s", old.Name, hit.shown())
	}

	origin := &forkOrigin{Project: hit.store.key, Tool: old.Name, V: old.Version}
	first := old.spec()
	first.ForkedFrom = origin
	next.ForkedFrom = origin

	err := lib.locked(ctx, func() error {
		err := lib.makeRoom(ctx, walkedBytes(previous, files))
		if err != nil {
			return err
		}

		err = lib.writeVersion(ctx, version{name: old.Name, number: 1, files: previous, spec: first,
			why: fmt.Sprintf("forked from %s %s v%d", lib.storeName(hit.store.key), old.Name, old.Version)})
		if err != nil {
			return err
		}

		return lib.writeVersion(ctx, version{name: old.Name, number: 2, files: files, spec: next, why: req.Why,
			vector: vector})
	})
	if err != nil {
		return refused("%v", err)
	}

	lib.moveHolding(ctx, req.From, hit, old.Name, 2)

	return done(fmt.Sprintf("forked %s from %s into this project; updated it, now current\n%s", old.Name,
		lib.storeName(hit.store.key), space.Line(lib.recordSpace(ctx)))).
		with(updatedFields{Name: old.Name, Version: 2, ForkedFrom: lib.storeName(hit.store.key)})
}

// moveHolding points the session's holding at the version just written where
// --from lay in the copy the session took.
func (lib *library) moveHolding(ctx context.Context, from string, hit found, name string, written int) {
	if from == "" {
		return
	}

	at := from
	if !filepath.IsAbs(at) {
		at = filepath.Join(lib.env.Base, at)
	}

	at = filepath.Clean(at)
	state := lib.loadSession(ctx)

	for index, held := range state.Holdings {
		if held.Project != hit.store.key || held.Tool != hit.tool.Name {
			continue
		}

		relative, err := filepath.Rel(held.Copy, at)
		if err != nil || strings.HasPrefix(relative, "..") {
			continue
		}

		state.Holdings[index].Project, state.Holdings[index].Tool = lib.key, name
		state.Holdings[index].Version = written
		_ = lib.saveSession(ctx, state)

		return
	}
}

func sameFiles(a []walked, b []walked) bool {
	if len(a) != len(b) {
		return false
	}

	sorted := func(files []walked) []walked {
		copied := slices.Clone(files)
		slices.SortFunc(copied, func(x, y walked) int { return strings.Compare(x.name, y.name) })

		return copied
	}

	left, right := sorted(a), sorted(b)
	for index := range left {
		if left[index].name != right[index].name || !bytes.Equal(left[index].data, right[index].data) {
			return false
		}
	}

	return true
}

// staleUsage names a file or a long option the old usage mentions that the
// new files no longer hold, or nothing.
func staleUsage(usage string, previous []walked, files []walked) string {
	holds := map[string]bool{}

	var text bytes.Buffer

	for _, file := range files {
		holds[file.name], holds[path.Base(file.name)] = true, true
		text.Write(file.data)
	}

	for _, file := range previous {
		for _, name := range []string{file.name, path.Base(file.name)} {
			if strings.Contains(usage, name) && !holds[name] {
				return name
			}
		}
	}

	for _, option := range longOption.FindAllString(usage, -1) {
		if !bytes.Contains(text.Bytes(), []byte(option)) {
			return option
		}
	}

	return ""
}
