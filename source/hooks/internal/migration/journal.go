package migration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

const journalName = ".migration.journal"

var (
	ErrJournalUnreadable = errors.New("the migration journal cannot be read")
	// errHalted stops a run where a kill would: no undo, the journal left.
	errHalted = errors.New("the run was halted")
)

// The kinds of change a journal records, each named before the change is
// made, so a run killed between the record and the change undoes a change
// that never happened as well as one that did.
const (
	// stepData: the data root held Names before the run; the undo removes
	// every other entry.
	stepData = "data"
	// stepRename: From was renamed to To; the undo renames To back where To
	// exists and From does not, since a rename that failed leaves From and
	// may meet a To that was there before.
	stepRename = "rename"
	// stepCreated: Path was created, a copy or a folder; the undo removes it.
	stepCreated = "created"
	// stepStub: a stub was placed at Path; the undo removes it only while it
	// is a stub or an empty folder.
	stepStub = "stub"
	// stepFence: a fence was written at Path; the undo removes it only while
	// it is a fence.
	stepFence = "fence"
	// stepPointer: the pointer at Path held Content, or nothing where
	// Existed is false; the undo puts that back.
	stepPointer = "pointer"
	// stepDiscard: Path, a tree set aside, is removed once the run
	// completes; it has no undo.
	stepDiscard = "discard"
)

// step is one line of the journal: a JSON object of its own, appended and
// synced before the change it names.
type step struct {
	Kind    string   `json:"kind"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Path    string   `json:"path,omitempty"`
	Content []byte   `json:"content,omitempty"`
	Existed bool     `json:"existed,omitempty"`
	Names   []string `json:"names,omitempty"`
}

// journal is what a run changed so far, kept on disk at
// `<data>/.migration.journal` as it goes, so a run killed half way is undone
// by the next call exactly as a failed run undoes itself.
type journal struct {
	file   *os.File
	steps  []step
	data   string
	rename func(string, string) error
	halt   func() bool
}

func (env Env) journalPath() string { return filepath.Join(env.Roots.Data, journalName) }

func (env Env) openJournal() (*journal, error) {
	file, err := safefs.OpenChecked(env.Roots.Data, env.journalPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND,
		0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the migration journal: %w", err)
	}

	data := env.Roots.Data

	rename := env.rename
	if rename == nil {
		rename = func(from string, to string) error { return moveEntry(data, from, to) }
	}

	return &journal{file: file, data: data, rename: rename, halt: env.halt}, nil
}

// rootOf is the root a write to path walks from: the data home for a path
// in it, else the path's own folder, the owner's: the old layout under
// `.claude` and a checkout's git common directory.
func rootOf(data string, path string) string {
	relative, err := filepath.Rel(data, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return data
	}

	return filepath.Dir(path)
}

// moveEntry renames from to to, each side walked from its own root.
func moveEntry(data string, from string, to string) error {
	return safefs.Move(rootOf(data, from), from, rootOf(data, to), to)
}

// checkpoint is where a kill may land; halt stops a run there.
func (log *journal) checkpoint() error {
	if log.halt != nil && log.halt() {
		return errHalted
	}

	return nil
}

// record appends the step and syncs it to disk before the change it names.
func (log *journal) record(next step) error {
	err := log.checkpoint()
	if err != nil {
		return err
	}

	line, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("recording a migration step: %w", err)
	}

	_, err = log.file.Write(append(line, '\n'))
	if err == nil {
		err = log.file.Sync()
	}

	if err != nil {
		return fmt.Errorf("recording a migration step: %w", err)
	}

	log.steps = append(log.steps, next)

	return log.checkpoint()
}

// created records that the run is about to create path, refusing a path
// that exists already: its undo removes the path whole.
func (log *journal) created(path string) error {
	if exists(path) {
		return fmt.Errorf("creating %s: %w", path, fs.ErrExist)
	}

	return log.record(step{Kind: stepCreated, Path: path})
}

func (log *journal) close() { _ = log.file.Close() }

// rollback undoes every step, newest first.
func rollback(data string, steps []step) error {
	var errs []error

	for _, done := range slices.Backward(steps) {
		errs = append(errs, undo(data, done))
	}

	return errors.Join(errs...)
}

// finish removes every tree a completed run set aside.
func finish(steps []step) {
	for _, done := range steps {
		if done.Kind == stepDiscard {
			_ = os.RemoveAll(done.Path)
		}
	}
}

func undo(data string, done step) error {
	switch done.Kind {
	case stepData:
		return removeCreated(done.Path, done.Names)
	case stepRename:
		if !exists(done.To) || exists(done.From) {
			return nil
		}

		return moveEntry(data, done.To, done.From)
	case stepCreated:
		return os.RemoveAll(done.Path)
	case stepStub:
		if !exists(done.Path) {
			return nil
		}

		if !isStub(done.Path) && !emptyDir(done.Path) {
			return fmt.Errorf("%s is no stub any more", done.Path)
		}

		return os.RemoveAll(done.Path)
	case stepFence:
		if isFence(done.Path) {
			return os.Remove(done.Path)
		}

		return nil
	case stepPointer:
		if done.Existed {
			return safefs.WriteFile(filepath.Dir(done.Path), done.Path, done.Content, 0o644)
		}

		err := os.Remove(done.Path)
		if notExist(err) {
			return nil
		}

		return err
	}

	return nil
}

func emptyDir(path string) bool {
	listed, err := os.ReadDir(path)

	return err == nil && len(listed) == 0
}

// readJournal reads the steps a run recorded. A last line cut short is a
// record a kill interrupted: the change it names never started.
func readJournal(path string) ([]step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJournalUnreadable, err)
	}

	var steps []step

	lines := bufio.NewScanner(bytes.NewReader(data))
	lines.Buffer(make([]byte, 0, 64<<10), metadataLimit)

	for lines.Scan() {
		var next step

		err = json.Unmarshal(lines.Bytes(), &next)
		if err != nil {
			if !lines.Scan() && !bytes.HasSuffix(data, []byte{'\n'}) {
				break
			}

			return nil, fmt.Errorf("%w: a line is no step", ErrJournalUnreadable)
		}

		steps = append(steps, next)
	}

	return steps, lines.Err()
}

// recoverJournal settles a run a kill interrupted: one that wrote
// `format.json` is finished, any other undone. The journal goes once that
// holds; where the undo fails it stays for the next call.
func (env Env) recoverJournal() error {
	steps, err := readJournal(env.journalPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	if exists(env.Roots.FormatFile()) {
		finish(steps)

		return os.Remove(env.journalPath())
	}

	err = rollback(env.Roots.Data, steps)
	if err != nil {
		return fmt.Errorf("undoing an interrupted migration: %w", err)
	}

	return os.Remove(env.journalPath())
}
