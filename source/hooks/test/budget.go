package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

var ErrNotListed = errors.New("not in the tool listing")

type toolListing struct {
	Result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	} `json:"result"`
}

// DescriptionSizes is each description in a tools/list answer line, in
// characters: a tool's under its name, an input key's under tool.key.
func DescriptionSizes(line string) (map[string]int, error) {
	var listing toolListing

	err := json.Unmarshal([]byte(line), &listing)
	if err != nil {
		return nil, fmt.Errorf("reading the tool listing: %w", err)
	}

	sizes := map[string]int{}

	for _, tool := range listing.Result.Tools {
		sizes[tool.Name] = utf8.RuneCountInString(tool.Description)

		for key, property := range tool.InputSchema.Properties {
			sizes[tool.Name+"."+key] = utf8.RuneCountInString(property.Description)
		}
	}

	return sizes, nil
}

// DescriptionBudgets is a budget for every description sizes measures: a
// tool's from tools, so that a new tool needs a budget of its own, and a key's
// from keys under its bare name, else the server's one key budget.
func DescriptionBudgets(sizes map[string]int, tools map[string]int, keys map[string]int, key int) map[string]int {
	budgets := map[string]int{}

	for name, budget := range tools {
		budgets[name] = budget
	}

	for name := range sizes {
		_, bare, keyed := strings.Cut(name, ".")
		if !keyed {
			continue
		}

		budgets[name] = key

		special, found := keys[bare]
		if found {
			budgets[name] = special
		}
	}

	return budgets
}

// PadDescription is a tools/list answer line with the description named
// tool or tool.key grown by padding characters, the rest as listed.
func PadDescription(line string, name string, padding int) (string, error) {
	var answer map[string]any

	err := json.Unmarshal([]byte(line), &answer)
	if err != nil {
		return "", fmt.Errorf("reading the tool listing: %w", err)
	}

	toolName, key, keyed := strings.Cut(name, ".")

	result, _ := answer["result"].(map[string]any)
	tools, _ := result["tools"].([]any)

	for _, listed := range tools {
		tool, _ := listed.(map[string]any)
		if tool["name"] != toolName {
			continue
		}

		described := tool

		if keyed {
			schema, _ := tool["inputSchema"].(map[string]any)
			properties, _ := schema["properties"].(map[string]any)

			property, found := properties[key].(map[string]any)
			if !found {
				return "", fmt.Errorf("padding %s: %w", name, ErrNotListed)
			}

			described = property
		}

		description, _ := described["description"].(string)
		described["description"] = description + strings.Repeat("x", padding)

		padded, err := json.Marshal(answer)
		if err != nil {
			return "", fmt.Errorf("writing the padded listing: %w", err)
		}

		return string(padded), nil
	}

	return "", fmt.Errorf("padding %s: %w", name, ErrNotListed)
}

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
