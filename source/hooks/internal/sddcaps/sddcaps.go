// Package sddcaps tells what the sdd on the PATH can do. Stock sdd
// (github.com/networkteam/sdd) carries nearly every surface agentic-helpers
// uses; the fork at github.com/fza/sdd adds a few. Neither has a capability
// command, and fork versions do not order against stock ones, so each surface
// is read from the binary's own help text: urfave/cli lists every command under
// COMMANDS and every flag under OPTIONS.
package sddcaps

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

const (
	Fork         = "github.com/fza/sdd"
	ForkInstall  = "brew install fza/tap/sdd"
	StockInstall = "brew install networkteam/tap/sdd"
)

const (
	limit     = 5 * time.Second
	cacheFile = "sdd-caps.json"
)

// Caps is what one sdd binary offers. Runs is false where no sdd answered;
// Printed then says what the attempt printed, for the message naming it.
type Caps struct {
	Runs              bool   `json:"runs"`
	Printed           string `json:"-"`
	PreflightVerified bool   `json:"preflight_verified"`
	Title             bool   `json:"title"`
	ActorKind         bool   `json:"actor_kind"`
	LocalConfig       bool   `json:"local_config"`
	Migrate           bool   `json:"migrate"`
	Export            bool   `json:"export"`
	ProjectDir        bool   `json:"project_dir"`
}

type Runner interface {
	Run(ctx context.Context, dir string, argv []string) (process.Result, error)
}

// Probe reads a binary's help. CacheDir keeps the answer per binary, keyed by
// its resolved path, modification time and size, so a new build is read again;
// LookPath finds the binary. Either left empty, the probe reads the help on
// every call.
type Probe struct {
	Runner   Runner
	CacheDir string
	LookPath func(string) (string, error)
}

// CacheDir is where the probe keeps its answers under the user's cache root,
// empty where there is no root.
func CacheDir(cacheRoot string) string {
	if cacheRoot == "" {
		return ""
	}

	return filepath.Join(cacheRoot, "agentic-helpers")
}

type cached struct {
	Key  string `json:"key"`
	Caps Caps   `json:"caps"`
}

var (
	flagName = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)
	command  = regexp.MustCompile(`^\s+([a-z][a-z0-9-]*)(?:,\s*[a-z]+)*\s{2,}`)
)

// Of probes the sdd run from dir.
func (probe Probe) Of(ctx context.Context, dir string) Caps {
	key := probe.key()

	if key != "" {
		held, found := probe.load(key)
		if found {
			return held
		}
	}

	root, err := probe.help(ctx, dir, "--help")
	if err != nil {
		return Caps{Printed: err.Error()}
	}

	newHelp, err := probe.help(ctx, dir, "new", "--help")
	if err != nil {
		return Caps{Printed: err.Error()}
	}

	initHelp, err := probe.help(ctx, dir, "init", "--help")
	if err != nil {
		return Caps{Printed: err.Error()}
	}

	commands := commandsIn(root)
	rootFlags, newFlags, initFlags := flagsIn(root), flagsIn(newHelp), flagsIn(initHelp)

	found := Caps{
		Runs:              true,
		PreflightVerified: newFlags["preflight-verified"],
		Title:             newFlags["title"],
		ActorKind:         newFlags["actor-kind"],
		LocalConfig:       rootFlags["local-config"],
		Migrate:           commands["migrate"],
		Export:            commands["export"],
		ProjectDir:        initFlags["project-dir"],
	}

	if key != "" {
		probe.save(key, found)
	}

	return found
}

func (probe Probe) help(ctx context.Context, dir string, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	argv := append([]string{"sdd"}, args...)

	result, err := probe.Runner.Run(bounded, dir, argv)
	if err != nil {
		return "", fmt.Errorf("`%s` printed nothing (%w)", strings.Join(argv, " "), err)
	}

	printed := strings.TrimSpace(result.Stdout + result.Stderr)
	if result.Code != 0 {
		return "", fmt.Errorf("`%s` printed %q and exited %d", strings.Join(argv, " "), printed, result.Code)
	}

	return printed, nil
}

// commandsIn names every command the COMMANDS section lists, aliases left out.
func commandsIn(help string) map[string]bool {
	found := map[string]bool{}
	inside := false

	for line := range strings.SplitSeq(help, "\n") {
		switch {
		case strings.HasPrefix(line, "COMMANDS:"):
			inside = true
		case inside && line != "" && !strings.HasPrefix(line, " "):
			inside = false
		case inside:
			match := command.FindStringSubmatch(line)
			if match != nil {
				found[match[1]] = true
			}
		}
	}

	return found
}

// flagsIn names every flag a help text lists: each `--name` a line opens with,
// and its aliases, read up to the column the usage text starts in.
func flagsIn(help string) map[string]bool {
	found := map[string]bool{}

	for line := range strings.SplitSeq(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "--") {
			continue
		}

		names, _, _ := strings.Cut(trimmed, "  ")
		for _, match := range flagName.FindAllStringSubmatch(names, -1) {
			found[match[1]] = true
		}
	}

	return found
}

func (probe Probe) key() string {
	if probe.CacheDir == "" || probe.LookPath == nil {
		return ""
	}

	path, err := probe.LookPath("sdd")
	if err != nil {
		return ""
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%s|%d|%d", resolved, info.ModTime().UnixNano(), info.Size())
}

func (probe Probe) load(key string) (Caps, bool) {
	raw, err := os.ReadFile(filepath.Join(probe.CacheDir, cacheFile))
	if err != nil {
		return Caps{}, false
	}

	var held cached

	err = json.Unmarshal(raw, &held)
	if err != nil || held.Key != key {
		return Caps{}, false
	}

	return held.Caps, true
}

// save keeps the answer for the next call. A cache that cannot be written only
// costs the next call a probe.
func (probe Probe) save(key string, found Caps) {
	raw, err := json.Marshal(cached{Key: key, Caps: found})
	if err != nil {
		return
	}

	err = os.MkdirAll(probe.CacheDir, 0o755)
	if err != nil {
		return
	}

	temporary := filepath.Join(probe.CacheDir, fmt.Sprintf(".%s.%d", cacheFile, os.Getpid()))

	err = os.WriteFile(temporary, raw, 0o644)
	if err != nil {
		return
	}

	_ = os.Rename(temporary, filepath.Join(probe.CacheDir, cacheFile))
}
