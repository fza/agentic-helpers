package test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"
)

// SkillSizes is each skill's SKILL.md under root, in characters, by the
// skill's directory name. Reference files beside it are not counted: they
// load only when their trigger shows up.
func SkillSizes(root string) (map[string]int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("listing the skills: %w", err)
	}

	sizes := map[string]int{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		content, err := os.ReadFile(filepath.Join(root, entry.Name(), "SKILL.md"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, fmt.Errorf("reading the skill %s: %w", entry.Name(), err)
		}

		sizes[entry.Name()] = utf8.RuneCount(content)
	}

	return sizes, nil
}

// OverBudget lists every measured size above its budget, every size with no
// budget, and every budget naming nothing measured, so that a new skill, tool
// or key gets a budget of its own and a removed one takes its budget along.
func OverBudget(sizes map[string]int, budgets map[string]int) []string {
	var breaches []string

	for name, size := range sizes {
		budget, found := budgets[name]

		switch {
		case !found:
			breaches = append(breaches, fmt.Sprintf("%s: %d characters, no budget", name, size))
		case size > budget:
			breaches = append(breaches, fmt.Sprintf("%s: %d characters, over its budget of %d", name, size, budget))
		}
	}

	for name := range budgets {
		_, found := sizes[name]
		if !found {
			breaches = append(breaches, fmt.Sprintf("%s: a budget for nothing measured", name))
		}
	}

	slices.Sort(breaches)

	return breaches
}

// CopyTree copies every file under source into target, keeping the layout.
func CopyTree(source string, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("placing %s: %w", path, err)
		}

		destination := filepath.Join(target, relative)

		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		return os.WriteFile(destination, content, 0o644)
	})
}
