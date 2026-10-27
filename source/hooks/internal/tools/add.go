package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// source is what --from named, read: its files, and whether it is a taken copy.
type source struct {
	files []walked
	copy  string
}

// readSource reads --from: a file or a folder inside the checkout (outside
// `.git`), AGENTIC_TOOLS_SCRATCH or the own store's `copies/`, every component
// checked, no link, no hard link, no pipe, at most a tool's size in all. A
// folder's files land at the top of the version; its own `tool.json` is the
// description a take wrote beside the files, never a file of the tool.
func (lib *library) readSource(ctx context.Context, given string) (source, error) {
	if strings.TrimSpace(given) == "" {
		return source{}, errors.New("--from names no file or folder")
	}

	at := given
	if !filepath.IsAbs(at) {
		at = filepath.Join(lib.env.Base, at)
	}

	at = filepath.Clean(at)

	base, relative, inside := lib.reachOf(ctx, at)
	if !inside {
		return source{}, fmt.Errorf("%s lies outside the checkout, AGENTIC_TOOLS_SCRATCH and the taken copies; register from inside one", given)
	}

	root, err := openDir(base)
	if err != nil {
		return source{}, err
	}

	defer root.close()

	parent := root
	components := strings.Split(filepath.ToSlash(relative), "/")

	for _, name := range components[:len(components)-1] {
		next, err := parent.sub(name)
		if parent != root {
			parent.close()
		}

		if err != nil {
			return source{}, err
		}

		parent = next
	}

	if parent != root {
		defer parent.close()
	}

	last := components[len(components)-1]

	folder, err := parent.sub(last)
	if err == nil {
		defer folder.close()

		files, err := folder.walk(toolLimit, func(name string) bool { return name == "tool.json" })
		if err != nil {
			return source{}, err
		}

		return source{files: files, copy: at}, lib.checkFiles(folder, files)
	}

	data, err := parent.read(last, toolLimit)
	if err != nil {
		return source{}, err
	}

	return source{files: []walked{{name: last, data: data}}}, nil
}

func (lib *library) checkFiles(folder *dir, files []walked) error {
	if len(files) == 0 {
		return fmt.Errorf("%s holds no file", folder.path)
	}

	return nil
}

// reachOf is the reach root holding at, and at relative to it.
func (lib *library) reachOf(ctx context.Context, at string) (string, string, bool) {
	var roots []string

	// A worktree's own files lie under its top, apart from the main checkout.
	top := lib.workTree(ctx)
	if top != "" {
		roots = append(roots, top)
	}

	checkout := lib.checkout(ctx)
	if checkout != "" && checkout != top {
		roots = append(roots, checkout)
	}

	scratch := strings.TrimSpace(lib.getenv("AGENTIC_TOOLS_SCRATCH"))
	if scratch != "" && filepath.IsAbs(scratch) {
		roots = append(roots, filepath.Clean(scratch))
	}

	// A checkout without `.tmp/` takes into the own store's `copies/`, and an
	// edited copy there registers like one under `.tmp/tools/`.
	roots = append(roots, filepath.Join(lib.ownDir(), "copies"))

	for _, root := range roots {
		relative, err := filepath.Rel(root, at)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}

		// A `.git` at any depth is a repository's own, a submodule's included, and
		// a case-insensitive filesystem reaches it as `.GIT` too.
		if slices.ContainsFunc(strings.Split(filepath.ToSlash(relative), "/"), func(name string) bool {
			return strings.EqualFold(name, ".git")
		}) {
			continue
		}

		return root, relative, true
	}

	return "", "", false
}

func checkFields(described spec) error {
	switch {
	case !namePattern.MatchString(described.Name) || len(described.Name) > nameMax:
		return fmt.Errorf("%q is no tool name: lowercase words joined by dashes, at most %d characters", described.Name, nameMax)
	case strings.TrimSpace(described.Summary) == "":
		return errors.New("--summary is required: one line saying what the tool does")
	case clean(described.Summary, false) != described.Summary:
		return errors.New("--summary is one line with no control characters")
	case len(described.Summary) > summaryMax:
		return fmt.Errorf("--summary is at most %d characters", summaryMax)
	case strings.TrimSpace(described.Usage) == "":
		return errors.New("--usage is required: how to run the tool")
	}

	for flag, value := range map[string]string{"usage": described.Usage, "inputs": described.Inputs, "outputs": described.Outputs} {
		if clean(value, true) != value {
			return fmt.Errorf("--%s holds a control character", flag)
		}
	}

	return nil
}

func given(field *string) string {
	if field == nil {
		return ""
	}

	return *field
}

func (lib *library) add(ctx context.Context, req request) result {
	described := spec{Name: req.Name, Summary: given(req.Summary), Usage: given(req.Usage), Inputs: given(req.Inputs),
		Outputs: given(req.Outputs)}

	err := checkFields(described)
	if err != nil {
		return refused("%v", err)
	}

	if clean(req.Distinct, false) != req.Distinct {
		return refused("--distinct is one line with no control characters")
	}

	own := lib.own(ctx)

	_, taken := own.tool(req.Name)
	if taken {
		return refused("%s is taken in this project: `%s update %s --why '...'` writes its next version", req.Name, self, req.Name)
	}

	survivor, merged := own.index.Merged[req.Name]
	if merged {
		return refused("%s was merged into %s: `%s update %s --why '...'` writes its next version", req.Name, survivor,
			self, survivor)
	}

	from, err := lib.readSource(ctx, req.From)
	if err != nil {
		return refused("%v", err)
	}

	credential := credentialIn(ctx, from.files, append(describedFields(described),
		labelled{label: "--distinct", text: req.Distinct}))
	if credential != "" {
		return refused("%s", credential)
	}

	same := lib.identical(ctx, from.files)
	if same != "" {
		return refused("%s", same)
	}

	vector, fingerprint := lib.embedOne(ctx, described)

	if req.Distinct == "" {
		near := lib.nearest(ctx, vector, fingerprint, 3)
		if len(near) > 0 {
			return refused("the description is close to %s: `%s update %s --why '...'` writes a new version instead; "+
				"--distinct '<how it differs>' adds it anyway", strings.Join(near, ", "), self, near[0])
		}
	}

	why := "added"
	if req.Distinct != "" {
		why = "added; distinct: " + req.Distinct
	}

	err = lib.locked(ctx, func() error {
		return lib.writeVersion(ctx, version{name: req.Name, number: 1, files: from.files, spec: described, why: why,
			vector: vector})
	})
	if err != nil {
		return refused("%v", err)
	}

	lib.forget()

	return done(fmt.Sprintf("added %s: `%s take %s` copies it", req.Name, self, req.Name))
}

// identical names the first file byte-identical to a file of any version of
// any tool in reach, or nothing.
func (lib *library) identical(ctx context.Context, files []walked) string {
	sums := map[string]string{}
	for _, file := range files {
		sums[sum(file.data)] = file.name
	}

	for _, held := range lib.stores(ctx) {
		for _, name := range held.names() {
			tool := held.index.Tools[name]

			for _, v := range sortedKeys(tool.Hashes) {
				for _, hash := range tool.Hashes[v] {
					file, match := sums[hash.Sum]
					if !match {
						continue
					}

					hit := found{store: held, tool: tool}

					return fmt.Sprintf("%s is %s %s byte for byte: `%s take %s` copies it", file, hit.shown(), v,
						self, hit.shown())
				}
			}
		}
	}

	return ""
}

// nearest names up to limit current tools in reach whose vector lies at or
// above the duplicate floor of vector, closest first.
func (lib *library) nearest(ctx context.Context, vector []float32, fingerprint string, limit int) []string {
	if len(vector) == 0 {
		return nil
	}

	type scored struct {
		name  string
		score float32
	}

	var near []scored

	floor := float32(lib.options.ToolsDuplicateFloor)

	stores := lib.stores(ctx)
	wanted := map[string]bool{}

	for _, held := range stores {
		for _, tool := range held.index.Tools {
			wanted[tool.Described] = true
		}
	}

	scores := lib.scores(vector, fingerprint, wanted)

	for _, held := range stores {
		for _, name := range held.names() {
			tool := held.index.Tools[name]

			score := scores[tool.Described]
			if score >= floor {
				near = append(near, scored{name: found{store: held, tool: tool}.shown(), score: score})
			}
		}
	}

	slices.SortStableFunc(near, func(a, b scored) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}

			return 1
		}

		return strings.Compare(a.name, b.name)
	})

	var named []string
	for _, hit := range near[:min(limit, len(near))] {
		named = append(named, hit.name)
	}

	return named
}
