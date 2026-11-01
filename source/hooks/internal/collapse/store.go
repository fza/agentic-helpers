package collapse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
)

// Tool prefixes the names of sdd's MCP tools.
const Tool = "mcp__sdd__"

const (
	setSuffix = ".shown.json"
	lockName  = "collapse.lock"
)

// Store keeps each agent's set in Dir, a folder below Root that no write
// walks out of: the main thread's as `<session>.shown.json`, a subagent's as
// `<session>.<agent>.shown.json`, and what a fork's spawn kept for it as
// `<session>.spawn-<call>.shown.json` until the fork's first answer.
type Store struct {
	Root string
	Dir  string
}

// Collapse answers the PostToolUse output replacing an sdd tool's answer
// with its collapsed form, and nothing where nothing collapses or anything
// fails, which leaves the answer as sdd gave it.
func (store Store) Collapse(ctx context.Context, payload hookio.Payload) ([]byte, bool) {
	tool, found := strings.CutPrefix(payload.ToolName, Tool)
	if !found || payload.SessionID == "" || len(payload.ToolResponse) == 0 || payload.Failed() {
		return nil, false
	}

	var output []byte

	err := store.locked(ctx, func() error {
		path := store.setPath(payload.SessionID, payload.AgentID)
		held, exists := store.read(path)

		if !exists && payload.AgentID != "" {
			held = store.adopt(payload)
		}

		before := held
		shortened, collapsed := Answer(tool, payload.ToolResponse, &held)

		if collapsed {
			encoded, err := updatedOutput(shortened)
			if err != nil {
				return err
			}

			output = encoded
		}

		if collapsed || !sameSet(before, held) || !exists {
			return store.write(path, held)
		}

		return nil
	})
	if err != nil || output == nil {
		return nil, false
	}

	return output, true
}

// Keep saves, for a fork an Agent call spawns, its spawner's set as it
// stands, since the fork's context starts as a copy of the spawner's.
func (store Store) Keep(ctx context.Context, payload hookio.Payload) {
	if payload.ToolInput.SubagentType != "fork" || payload.SessionID == "" || !shaped(payload.ToolUseID) {
		return
	}

	_ = store.locked(ctx, func() error {
		held, _ := store.read(store.setPath(payload.SessionID, payload.AgentID))

		return store.write(store.spawnPath(payload.SessionID, payload.ToolUseID), held)
	})
}

// Forget empties the main thread's set, as its context empties at a
// compaction, `/clear` and resume.
func (store Store) Forget(ctx context.Context, session string) {
	if session == "" {
		return
	}

	_ = store.locked(ctx, func() error {
		err := os.Remove(store.setPath(session, ""))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	})
}

// adopt is a subagent's first set: a fork's from what its spawn kept, which
// is then spent; a fresh subagent's empty.
func (store Store) adopt(payload hookio.Payload) Set {
	call := transcript.SpawnCall(transcript.OfSubagent(payload.TranscriptPath, "", payload.AgentID))
	if !shaped(call) {
		return Set{}
	}

	path := store.spawnPath(payload.SessionID, call)

	held, found := store.read(path)
	if found {
		_ = os.Remove(path)
	}

	return held
}

func (store Store) locked(ctx context.Context, work func() error) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("collapsing sdd output: %w", err)
	}

	err = safefs.MkdirAll(store.Root, store.Dir, 0o755)
	if err != nil {
		return fmt.Errorf("making the collapse folder: %w", err)
	}

	lock, err := safefs.Lock(store.Root, filepath.Join(store.Dir, lockName), 0o644)
	if err != nil {
		return fmt.Errorf("opening the collapse lock: %w", err)
	}

	defer lock.Close()

	err = filelock.Exclusive(lock)
	if err != nil {
		return fmt.Errorf("taking the collapse lock: %w", err)
	}

	defer func() { _ = filelock.Unlock(lock) }()

	return work()
}

// read is the set at path; an unreadable one holds nothing, so everything
// prints in full.
func (store Store) read(path string) (Set, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Set{}, false
	}

	var held Set

	err = json.Unmarshal(data, &held)
	if err != nil {
		return Set{}, false
	}

	return held, true
}

func (store Store) write(path string, held Set) error {
	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("encoding the shown set: %w", err)
	}

	err = atomicfile.Write(store.Root, path, data, 0o644)
	if err != nil {
		return fmt.Errorf("writing the shown set: %w", err)
	}

	return nil
}

func (store Store) setPath(session string, agent string) string {
	name := fileSafe(session)
	if agent != "" {
		name += "." + fileSafe(agent)
	}

	return filepath.Join(store.Dir, name+setSuffix)
}

func (store Store) spawnPath(session string, call string) string {
	return filepath.Join(store.Dir, fileSafe(session)+".spawn-"+call+setSuffix)
}

func fileSafe(name string) string {
	return strings.ReplaceAll(name, "/", "-")
}

func shaped(call string) bool {
	return transcript.AgentIDShaped(call)
}

func sameSet(before Set, after Set) bool {
	if len(before.Lines) != len(after.Lines) || len(before.Pieces) != len(after.Pieces) {
		return false
	}

	for id, digest := range after.Lines {
		if before.Lines[id] != digest {
			return false
		}
	}

	for key, digest := range after.Pieces {
		if before.Pieces[key] != digest {
			return false
		}
	}

	return true
}

// updatedOutput is the PostToolUse answer replacing an MCP tool's output
// with answer, as the one text block Claude Code hands the agent.
func updatedOutput(answer json.RawMessage) ([]byte, error) {
	var replaced struct {
		HookSpecificOutput struct {
			HookEventName        string `json:"hookEventName"`
			UpdatedMCPToolOutput struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"updatedMCPToolOutput"`
		} `json:"hookSpecificOutput"`
	}

	replaced.HookSpecificOutput.HookEventName = "PostToolUse"
	replaced.HookSpecificOutput.UpdatedMCPToolOutput.Content = append(
		replaced.HookSpecificOutput.UpdatedMCPToolOutput.Content,
		struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: string(answer)},
	)

	encoded, err := json.Marshal(replaced)
	if err != nil {
		return nil, fmt.Errorf("encoding the collapsed output: %w", err)
	}

	return encoded, nil
}
