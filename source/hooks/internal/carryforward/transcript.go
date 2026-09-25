package carryforward

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Claude Code inlines a hook's output only up to 10,000 characters and swaps
// anything longer for a file path with a short preview, which a compaction
// summary then outranks. So the log arrives in pieces under that limit, one per
// `session-log` hook, and only its most recent part does.
const (
	deltaLogPieceChars = 9_000
	deltaLogPieces     = 5
	summaryLimit       = 80
)

var summarizedInputs = []string{"file_path", "path", "notebook_path", "pattern", "command", "url"}

type transcriptRecord struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string                     `json:"type"`
	Text  string                     `json:"text"`
	Name  string                     `json:"name"`
	Input map[string]json.RawMessage `json:"input"`
}

func (hook *hook) logPath(session string) string {
	return filepath.Join(hook.transcriptsDir(), session+".md")
}

// transcriptFor finds the harness transcript from the project path Claude
// Code encodes. A seat driven inside a worktree is encoded under that
// worktree's own path, so every sibling project directory is the fallback.
func (hook *hook) transcriptFor(session string) string {
	projects := filepath.Join(hook.env.Home, ".claude", "projects")
	slug := strings.ReplaceAll(hook.env.Root, "/", "-")

	// The glob matches the checkout's own directory too, and sorts it first.
	siblings, err := filepath.Glob(filepath.Join(projects, slug+"*", session+".jsonl"))
	if err != nil {
		return ""
	}

	slices.Sort(siblings)

	for _, sibling := range siblings {
		if isFile(sibling) {
			return sibling
		}
	}

	return ""
}

func readNewRecords(transcript string, offset int64) ([]transcriptRecord, int64) {
	file, err := os.Open(transcript)
	if err != nil {
		return nil, offset
	}

	defer func() { _ = file.Close() }()

	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, offset
	}

	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, offset
	}

	var records []transcriptRecord

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var record transcriptRecord

		err := json.Unmarshal(line, &record)
		if err == nil {
			records = append(records, record)
		}
	}

	return records, offset + int64(len(raw))
}

func (hook *hook) summarizeTool(name string, input map[string]json.RawMessage) string {
	for _, key := range summarizedInputs {
		var value string

		err := json.Unmarshal(input[key], &value)
		if err != nil || value == "" {
			continue
		}

		short := []rune(strings.ReplaceAll(value, hook.env.Root+"/", ""))
		if len(short) > summaryLimit {
			short = append(short[:summaryLimit-3], []rune("...")...)
		}

		return name + " " + string(short)
	}

	return name
}

// turnBlock renders one turn of the harness transcript, or nothing where the
// records held no text and no tool call.
func (hook *hook) turnBlock(records []transcriptRecord, number int) string {
	var asked, replied, did []string

	for _, record := range records {
		if record.Type != "user" && record.Type != "assistant" {
			continue
		}

		said := &replied
		if record.Type == "user" {
			said = &asked
		}

		var text string

		err := json.Unmarshal(record.Message.Content, &text)
		if err == nil {
			*said = append(*said, text)

			continue
		}

		var blocks []contentBlock

		err = json.Unmarshal(record.Message.Content, &blocks)
		if err != nil {
			continue
		}

		for _, block := range blocks {
			switch block.Type {
			case "text":
				trimmed := strings.TrimSpace(block.Text)
				if trimmed != "" {
					*said = append(*said, trimmed)
				}
			case "tool_use":
				name := block.Name
				if name == "" {
					name = "?"
				}

				did = append(did, hook.summarizeTool(name, block.Input))
			}
		}
	}

	if len(asked) == 0 && len(replied) == 0 && len(did) == 0 {
		return ""
	}

	lines := []string{fmt.Sprintf("## turn %d  %s", number, hook.env.Now().Format("2006-01-02 15:04")), ""}
	if len(asked) > 0 {
		lines = append(lines, "### asked", "", strings.TrimSpace(strings.Join(asked, "\n\n")), "")
	}

	if len(replied) > 0 {
		lines = append(lines, "### replied", "", strings.TrimSpace(strings.Join(replied, "\n\n")), "")
	}

	if len(did) > 0 {
		var ordered []string

		for _, entry := range did {
			if !slices.Contains(ordered, entry) {
				ordered = append(ordered, entry)
			}
		}

		lines = append(lines, "### did", "", strings.Join(ordered, " · "), "")
	}

	return strings.Join(lines, "\n") + "\n"
}

// appendTurn logs what the harness transcript gained since the last stop.
func (hook *hook) appendTurn(session string, transcript string, held *state) error {
	records, end := readNewRecords(transcript, held.LogOffset)
	held.LogOffset = end

	block := hook.turnBlock(records, held.Turns+1)
	if block == "" {
		return nil
	}

	held.Turns++

	err := os.MkdirAll(hook.transcriptsDir(), 0o755)
	if err != nil {
		return fmt.Errorf("creating the transcripts directory: %w", err)
	}

	file, err := os.OpenFile(hook.logPath(session), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening the delta log: %w", err)
	}

	_, err = file.WriteString(block)
	if err != nil {
		_ = file.Close()

		return fmt.Errorf("appending to the delta log: %w", err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("closing the delta log: %w", err)
	}

	return nil
}

// deltaLogPieces is the turns since the last refresh, newest last, as pieces a
// hook can inline. A compaction summary tells the session what to do next, so
// an instruction to go and open a file competes with it and loses; content
// cannot be skipped. Lengths count characters, as the client does.
func (hook *hook) deltaLogPieces(session string) []string {
	data, err := os.ReadFile(hook.logPath(session))
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return nil
	}

	text := []rune(strings.ToValidUTF8(string(data), "\uFFFD"))
	var pieces [][]rune

	var current []rune

	for _, line := range splitLinesKeepingEnds(text) {
		for len(line) > deltaLogPieceChars {
			if len(current) > 0 {
				pieces = append(pieces, current)
				current = nil
			}

			pieces = append(pieces, line[:deltaLogPieceChars])
			line = line[deltaLogPieceChars:]
		}

		if len(current) > 0 && len(current)+len(line) > deltaLogPieceChars {
			pieces = append(pieces, current)
			current = nil
		}

		current = append(current, line...)
	}

	if len(current) > 0 {
		pieces = append(pieces, current)
	}

	if len(pieces) > deltaLogPieces {
		pieces = pieces[len(pieces)-deltaLogPieces:]
	}

	withheld := len(text)
	for _, piece := range pieces {
		withheld -= len(piece)
	}

	older := ""
	if withheld > 0 {
		older = fmt.Sprintf(" The %d earlier characters are in that file and nowhere else; read them "+
			"there before relying on anything they cover.", withheld)
	}

	rendered := make([]string, 0, len(pieces))

	for index, piece := range pieces {
		heading := fmt.Sprintf("## Turns since the last refresh, piece %d\n\n", index+1)
		if index == 0 {
			heading = "## Turns since the last carry-forward refresh\n\n" +
				fmt.Sprintf("Held in `.memory/transcripts/%s.md`, which only a refresh truncates. ", session) +
				fmt.Sprintf("%d pieces follow, oldest first.%s\n\n", len(pieces), older)
		}

		rendered = append(rendered, heading+string(piece))
	}

	return rendered
}

func splitLinesKeepingEnds(text []rune) [][]rune {
	var lines [][]rune

	start := 0

	for index, char := range text {
		if char == '\n' {
			lines = append(lines, text[start:index+1])
			start = index + 1
		}
	}

	if start < len(text) {
		lines = append(lines, text[start:])
	}

	return lines
}

func isFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}
