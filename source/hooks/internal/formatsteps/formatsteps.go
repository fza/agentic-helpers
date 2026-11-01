// Package formatsteps moves a persisted component, a folder recording its
// format in `format.json`, from the format it holds to the newest one its
// step table knows. A table holds exactly one step per format, each moving
// the component one format on through an ordered list of operations; the
// runner takes the component's lock, reads the format again under it,
// refuses one newer than the table knows without writing anything, runs every
// step from the format found in order and writes `format.json` after each.
//
// A run killed anywhere resumes at the step it stopped in: that step's
// operations are listed again from what the folder holds then and applied
// from the first, so every operation is safe to run again from any point a
// kill can stop it at. An operation too large to split carries its own crash
// safety. Rejected: a write-ahead journal with an undo for every change,
// whose own format would have to stay stable and whose kill points would need
// hooks only tests use.
package formatsteps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// FileName is the file in a component's folder recording its format; a folder
// without one holds format 1.
const FileName = "format.json"

var (
	ErrNewer = errors.New("a newer format")
	ErrOlder = errors.New("a format older than any step")
	ErrTable = errors.New("a step table out of order")
)

// Operation is one change of a step, safe to apply again from any point a
// kill can stop it at.
type Operation struct {
	Name  string
	Apply func(ctx context.Context) error
}

// Step moves a component from format From to To, From plus one. Ops lists
// the step's operations from what the folder holds when it runs, a run killed
// within the step included, and changes nothing itself.
type Step struct {
	From int
	To   int
	Ops  func(ctx context.Context) ([]Operation, error)
}

// Component is a persisted folder and the steps that move it. Name says the
// folder in a refusal (`this store`, a path); Root bounds every write
// (internal/safefs); Lock holds the component's lock until the function it
// returns runs.
type Component struct {
	Name  string
	Dir   string
	Root  string
	Lock  func(ctx context.Context) (func(), error)
	Steps []Step
}

type formatFile struct {
	Format int `json:"format"`
}

// Read is the format the folder dir records; absent, format 1.
func Read(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil
	}

	if err != nil {
		return 0, fmt.Errorf("reading the format of %s: %w", dir, err)
	}

	var held formatFile

	err = json.Unmarshal(data, &held)
	if err != nil || held.Format < 1 {
		return 0, fmt.Errorf("parsing the format of %s: %s", dir, data)
	}

	return held.Format, nil
}

// Write records version as the format of the folder dir, through a temporary
// file renamed into place.
func Write(root string, dir string, version int) error {
	data, err := json.Marshal(formatFile{Format: version})
	if err != nil {
		return fmt.Errorf("encoding the format of %s: %w", dir, err)
	}

	err = safefs.MkdirAll(root, dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	err = atomicfile.Write(root, filepath.Join(dir, FileName), append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing the format of %s: %w", dir, err)
	}

	return nil
}

// Newest is the format the table's last step reaches, 1 for an empty table.
func Newest(steps []Step) int {
	if len(steps) == 0 {
		return 1
	}

	return steps[len(steps)-1].To
}

// Newer refuses a format newer than the table knows, naming both.
func (component Component) Newer(version int) error {
	newest := Newest(component.Steps)
	if version <= newest {
		return nil
	}

	return fmt.Errorf("%w: %s has format %d; this release reads up to %d", ErrNewer, component.Name, version, newest)
}

// Check is the format the component records, refusing one newer than its
// table knows.
func (component Component) Check() (int, error) {
	version, err := Read(component.Dir)
	if err != nil {
		return 0, err
	}

	return version, component.Newer(version)
}

// Run brings the component to the newest format its table knows. A component
// already there costs one read and takes no lock.
func Run(ctx context.Context, component Component) error {
	err := checkTable(component.Steps, component.Lock)
	if err != nil {
		return err
	}

	version, err := component.Check()
	if err != nil || version == Newest(component.Steps) {
		return err
	}

	unlock, err := component.Lock(ctx)
	if err != nil {
		return err
	}

	defer unlock()

	// Another process may have moved the component between the first read
	// and the lock, a newer release among them.
	version, err = component.Check()
	if err != nil {
		return err
	}

	for version < Newest(component.Steps) {
		step, err := stepFrom(component.Steps, version)
		if err != nil {
			return err
		}

		err = apply(ctx, step)
		if err != nil {
			return fmt.Errorf("moving %s from format %d to %d: %w", component.Name, step.From, step.To, err)
		}

		err = Write(component.Root, component.Dir, step.To)
		if err != nil {
			return err
		}

		version = step.To
	}

	return nil
}

func apply(ctx context.Context, step Step) error {
	operations, err := step.Ops(ctx)
	if err != nil {
		return err
	}

	for _, operation := range operations {
		err = ctx.Err()
		if err != nil {
			return err
		}

		err = operation.Apply(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", operation.Name, err)
		}
	}

	return nil
}

func stepFrom(steps []Step, version int) (Step, error) {
	index := version - steps[0].From
	if index < 0 {
		return Step{}, fmt.Errorf("%w: format %d, the first step starts at %d", ErrOlder, version, steps[0].From)
	}

	return steps[index], nil
}

// checkTable holds the table to one step per format, each moving one format
// on from where the one before it ended, and the component to a lock.
func checkTable(steps []Step, lock func(ctx context.Context) (func(), error)) error {
	if lock == nil {
		return fmt.Errorf("%w: no lock", ErrTable)
	}

	for index, step := range steps {
		if step.To != step.From+1 || (index > 0 && step.From != steps[index-1].To) || step.Ops == nil {
			return fmt.Errorf("%w: the step from %d to %d", ErrTable, step.From, step.To)
		}
	}

	return nil
}
