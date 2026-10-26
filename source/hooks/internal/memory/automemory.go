package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/gitignore"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
)

// Claude Code reads a project's local settings from the main checkout's root,
// a worktree's sessions included, and turns its own auto memory off where
// they set this key.
const (
	localSettings   = ".claude/settings.local.json"
	autoMemoryKey   = "autoMemoryEnabled"
	settingsPattern = "**/" + localSettings
)

// turnOffAutoMemory sets autoMemoryEnabled to false in the project's local
// Claude Code settings, so a session started after the claim keeps one memory,
// this one. A key already there is the owner's setting and stays as written.
// It returns the lines the claim prints, none where nothing changed.
func (mem *memory) turnOffAutoMemory(ctx context.Context) []string {
	if !mem.options().MemoryTurnsOffAuto {
		return nil
	}

	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)
	path := filepath.Join(root, filepath.FromSlash(localSettings))

	linked := symlinkOn(root, path)
	if linked != "" {
		return []string{fmt.Sprintf("%s: %s is a symbolic link, so Claude Code's own auto memory stays as it is", self, linked)}
	}

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return []string{fmt.Sprintf("%s: %s cannot be read, so Claude Code's own auto memory stays as it is: %v", self, localSettings, err)}
	}

	updated, why := withAutoMemoryOff(raw)
	if why != "" {
		return []string{fmt.Sprintf("%s: %s %s, so Claude Code's own auto memory stays as it is", self, localSettings, why)}
	}

	if updated == nil {
		return nil
	}

	err = writeSettings(path, updated)
	if err != nil {
		return []string{fmt.Sprintf("%s: Claude Code's own auto memory stays as it is: %v", self, err)}
	}

	lines := []string{fmt.Sprintf("%s: Claude Code's own auto memory is off for this project from the next session (%s); "+
		"plugin option memory_turns_off_auto: false leaves it on", self, localSettings)}

	// Claude Code ignores the file only when it creates it itself, and one
	// person's settings must never reach a commit.
	ignored, err := gitignore.Global{Git: mem.env.Git, Home: mem.env.Home, Getenv: mem.env.Getenv}.
		Ensure(ctx, self, root, localSettings, settingsPattern)

	switch {
	case err != nil:
		lines = append(lines, fmt.Sprintf("%s: %s stays unignored by git: %v", self, localSettings, err))
	case ignored != "":
		lines = append(lines, ignored)
	}

	return lines
}

// withAutoMemoryOff returns the settings text with the key added, keeping every
// other key and the text around them, or nil where the key is already set. A
// text that is no JSON object returns why it is left alone.
func withAutoMemoryOff(raw []byte) ([]byte, string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{\n  \"" + autoMemoryKey + "\": false\n}\n"), ""
	}

	var settings map[string]json.RawMessage

	err := json.Unmarshal(raw, &settings)
	if err != nil {
		return nil, "does not parse as a JSON object"
	}

	_, set := settings[autoMemoryKey]
	if set {
		return nil, ""
	}

	opening := bytes.IndexByte(raw, '{')
	entry := "\n  \"" + autoMemoryKey + "\": false"

	if len(settings) > 0 {
		entry += ","
	}

	updated := slices.Concat(raw[:opening+1], []byte(entry), raw[opening+1:])
	if !json.Valid(updated) {
		return nil, "cannot take the key without rewriting its text"
	}

	return updated, ""
}

// symlinkOn names the part of the settings path inside root that is a
// symbolic link, or nothing where none is. A repository can carry `.claude/`
// or the file as a link to anywhere, and a claim must never write through one.
func symlinkOn(root, path string) string {
	for _, part := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(part)
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			relative, _ := filepath.Rel(root, part)

			return filepath.ToSlash(relative)
		}
	}

	return ""
}

// writeSettings replaces the file through a temporary one beside it, opened
// exclusively under a random name, so no reader sees half a file and no
// planted link at a guessable name receives the write.
func writeSettings(path string, content []byte) error {
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	mode := os.FileMode(0o644)

	info, err := os.Lstat(path)
	if err == nil {
		mode = info.Mode().Perm()
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".settings.local.json.*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", localSettings, err)
	}

	_, err = temporary.Write(content)
	if err == nil {
		err = temporary.Chmod(mode)
	}

	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}

	if err != nil {
		_ = os.Remove(temporary.Name())

		return fmt.Errorf("writing %s: %w", localSettings, err)
	}

	return nil
}
