// Package transcript condenses a Claude Code session transcript, the JSONL
// file a hook payload names in transcript_path, into a short log of turns.
package transcript

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
)

type Turn struct {
	Number  int
	At      time.Time
	Asked   string
	Replied []string
	Did     []string
}

type record struct {
	Type             string `json:"type"`
	Timestamp        string `json:"timestamp"`
	Cwd              string `json:"cwd"`
	IsSidechain      bool   `json:"isSidechain"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string                     `json:"type"`
	Text  string                     `json:"text"`
	Name  string                     `json:"name"`
	Input map[string]json.RawMessage `json:"input"`
}

// Since reads a transcript Claude Code has already cleaned up as holding no
// turns rather than failing: nothing is left to re-inject.
func Since(ctx context.Context, path string, after time.Time) ([]Turn, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("opening the transcript: %w", err)
	}

	defer func() { _ = file.Close() }()

	turns, err := readTurns(ctx, bufio.NewReader(file))
	if err != nil {
		return nil, err
	}

	selected := make([]Turn, 0, len(turns))

	for _, turn := range turns {
		if after.IsZero() || turn.At.After(after) {
			selected = append(selected, turn)
		}
	}

	return selected, nil
}

func readTurns(ctx context.Context, reader *bufio.Reader) ([]Turn, error) {
	var turns []Turn

	for {
		err := ctx.Err()
		if err != nil {
			return nil, fmt.Errorf("reading the transcript: %w", err)
		}

		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("reading the transcript: %w", readErr)
		}

		turns = addRecord(turns, line)

		if readErr != nil {
			return turns, nil
		}
	}
}

// addRecord files one transcript line under the turn it belongs to. A
// subagent's records, Claude Code's own injected context and a compaction
// summary carry the user type too, and belong to no turn.
func addRecord(turns []Turn, line []byte) []Turn {
	var held record

	err := json.Unmarshal(line, &held)
	if err != nil || held.IsSidechain {
		return turns
	}

	texts, blocks := parseContent(held.Message.Content)

	switch held.Type {
	case "user":
		if held.IsMeta || held.IsCompactSummary || !isPrompt(texts, blocks) {
			return turns
		}

		at, _ := time.Parse(time.RFC3339Nano, held.Timestamp)

		return append(turns, Turn{Number: len(turns) + 1, At: at, Asked: strings.Join(texts, "\n\n")})
	case "assistant":
		if len(turns) == 0 {
			return turns
		}

		current := &turns[len(turns)-1]
		current.Replied = append(current.Replied, texts...)

		for _, block := range blocks {
			if block.Type == "tool_use" {
				current.Did = append(current.Did, summarizeTool(block.Name, block.Input, held.Cwd))
			}
		}
	}

	return turns
}

// isPrompt tells a typed prompt from the user record that carries a tool's
// result back to the model.
func isPrompt(texts []string, blocks []contentBlock) bool {
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return false
		}
	}

	return len(texts) > 0
}

func parseContent(content json.RawMessage) ([]string, []contentBlock) {
	var text string

	err := json.Unmarshal(content, &text)
	if err == nil {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil, nil
		}

		return []string{trimmed}, nil
	}

	var blocks []contentBlock

	err = json.Unmarshal(content, &blocks)
	if err != nil {
		return nil, nil
	}

	var texts []string

	for _, block := range blocks {
		trimmed := strings.TrimSpace(block.Text)
		if block.Type == "text" && trimmed != "" {
			texts = append(texts, trimmed)
		}
	}

	return texts, blocks
}

// FirstUUID is the uuid of the first record in the transcript carrying one,
// or nothing where none does yet or the transcript is gone.
func FirstUUID(ctx context.Context, path string) (string, error) {
	first := ""

	err := eachUUID(ctx, path, func(uuid string, _ bool) bool {
		first = uuid

		return false
	})

	return first, err
}

// ForkStart is the uuid a fork of the transcript's session would start with:
// its latest compaction boundary's, else its first record's.
func ForkStart(ctx context.Context, path string) (string, error) {
	first, boundary := "", ""

	err := eachUUID(ctx, path, func(uuid string, isBoundary bool) bool {
		if first == "" {
			first = uuid
		}

		if isBoundary {
			boundary = uuid
		}

		return true
	})

	return cmp.Or(boundary, first), err
}

var boundaryKey = []byte(`"compactMetadata"`)

// eachUUID hands every record carrying a uuid to visit, saying whether it is a
// compaction boundary, until visit answers false. A transcript that is gone
// holds none.
func eachUUID(ctx context.Context, path string, visit func(uuid string, isBoundary bool) bool) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening the transcript: %w", err)
	}

	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)

	for {
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("reading the transcript: %w", err)
		}

		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("reading the transcript: %w", readErr)
		}

		var record struct {
			UUID     string          `json:"uuid"`
			Boundary json.RawMessage `json:"compactMetadata"`
		}

		if json.Unmarshal(line, &record) == nil && record.UUID != "" {
			isBoundary := bytes.Contains(line, boundaryKey) && len(record.Boundary) > 0 && string(record.Boundary) != "null"
			if !visit(record.UUID, isBoundary) {
				return nil
			}
		}

		if readErr != nil {
			return nil
		}
	}
}
