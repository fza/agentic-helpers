package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/dump"
)

// CanonicalTree is every regular file below root that skip leaves in, by
// path, with its content: a data file kept in a binary format as
// `agentic-helpers dump` prints it, any other as it is. Paths and contents
// pass through names, so two trees holding ids minted apart compare by what
// the ids stand for.
func CanonicalTree(t *testing.T, root string, names interface{ Replace(string) string },
	skip func(relative string) bool,
) string {
	t.Helper()

	var files []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}

		relative, err := filepath.Rel(root, path)
		if err != nil || skip(filepath.ToSlash(relative)) {
			return err
		}

		var content bytes.Buffer

		err = dump.Dump(context.Background(), &content, path)
		if errors.Is(err, dump.ErrUnknownFile) {
			content.Reset()

			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}

			content.Write(raw)
		} else if err != nil {
			return err
		}

		files = append(files, "== "+names.Replace(filepath.ToSlash(relative))+"\n"+sortedJSON(names.Replace(content.String())))

		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("walking %s: %v", root, err)
	}

	slices.Sort(files)

	return strings.Join(files, "\n")
}

// sortedJSON is a JSON document with every object's keys sorted, so a map
// keyed by ids keeps one order once the ids are named; any other text as it
// is.
func sortedJSON(text string) string {
	var held any

	err := json.Unmarshal([]byte(text), &held)
	if err != nil {
		return text
	}

	sorted, err := json.MarshalIndent(held, "", "  ")
	if err != nil {
		return text
	}

	return string(sorted) + "\n"
}
