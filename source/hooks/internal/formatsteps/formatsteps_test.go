package formatsteps_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// recorded is a component of three steps, 1 to 4, each of two operations
// that note their name and the format `format.json` held when they ran.
type recorded struct {
	dir     string
	applied []string
	locks   int
	failAt  string
	onLock  func()
}

func (held *recorded) component() formatsteps.Component {
	var steps []formatsteps.Step

	for from := 1; from <= 3; from++ {
		steps = append(steps, formatsteps.Step{From: from, To: from + 1, Ops: func(context.Context) ([]formatsteps.Operation, error) {
			var operations []formatsteps.Operation

			for _, part := range []string{"a", "b"} {
				name := fmt.Sprintf("%d%s", from, part)
				operations = append(operations, formatsteps.Operation{Name: name, Apply: func(context.Context) error {
					if name == held.failAt {
						return errors.New("the disk is full")
					}

					version, err := formatsteps.Read(held.dir)
					if err != nil {
						return err
					}

					held.applied = append(held.applied, fmt.Sprintf("%s@%d", name, version))

					return nil
				}})
			}

			return operations, nil
		}})
	}

	return formatsteps.Component{
		Name: "this store", Dir: held.dir, Root: filepath.Dir(held.dir), Steps: steps,
		Lock: func(context.Context) (func(), error) {
			held.locks++

			if held.onLock != nil {
				held.onLock()
			}

			return func() {}, nil
		},
	}
}

func TestRunStepsFromTheFormatFound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  string
		applied []string
		locks   int
	}{
		{name: "no format.json", applied: []string{"1a@1", "1b@1", "2a@2", "2b@2", "3a@3", "3b@3"}, locks: 1},
		{name: "format 1", format: `{"format":1}`, applied: []string{"1a@1", "1b@1", "2a@2", "2b@2", "3a@3", "3b@3"}, locks: 1},
		{name: "format 2", format: `{"format":2}`, applied: []string{"2a@2", "2b@2", "3a@3", "3b@3"}, locks: 1},
		{name: "format 3", format: `{"format":3}`, applied: []string{"3a@3", "3b@3"}, locks: 1},
		{name: "the newest format", format: `{"format":4}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := &recorded{dir: filepath.Join(t.TempDir(), "store")}
			if tc.format != "" {
				test.WriteFile(t, filepath.Join(held.dir, formatsteps.FileName), tc.format)
			}

			err := formatsteps.Run(context.Background(), held.component())
			if err != nil {
				t.Fatalf("the run should succeed: %v", err)
			}

			if !slices.Equal(held.applied, tc.applied) {
				t.Errorf("every step from the format found should run in order, format.json written after each, got %v", held.applied)
			}

			if held.locks != tc.locks {
				t.Errorf("the lock should be taken only where a step runs, taken %d times", held.locks)
			}

			version, err := formatsteps.Read(held.dir)
			if err != nil || version != 4 {
				t.Errorf("the component should end at the newest format, got %d %v", version, err)
			}
		})
	}
}

func TestRunRefusesANewerFormat(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
		onLock string
		found  int
		left   string
	}{
		{name: "found before the lock", format: `{"format":5}`, found: 5, left: `{"format":5}`},
		{name: "written under the lock by a newer release", format: `{"format":2}`, onLock: `{"format":9}`, found: 9,
			left: `{"format":9}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := &recorded{dir: filepath.Join(t.TempDir(), "store")}
			path := filepath.Join(held.dir, formatsteps.FileName)
			test.WriteFile(t, path, tc.format)

			if tc.onLock != "" {
				held.onLock = func() { test.WriteFile(t, path, tc.onLock) }
			}

			err := formatsteps.Run(context.Background(), held.component())
			if !errors.Is(err, formatsteps.ErrNewer) {
				t.Fatalf("a newer format should be refused, got %v", err)
			}

			want := fmt.Sprintf("a newer format: this store has format %d; this release reads up to 4", tc.found)
			if err.Error() != want {
				t.Errorf("the refusal should name both formats, got %q", err)
			}

			if len(held.applied) != 0 {
				t.Errorf("no step should run on a newer format, got %v", held.applied)
			}

			written, _ := os.ReadFile(path)
			if string(written) != tc.left {
				t.Errorf("a newer format should never be written back, got %s", written)
			}
		})
	}
}

func TestAFailedOperationResumesItsStep(t *testing.T) {
	held := &recorded{dir: filepath.Join(t.TempDir(), "store"), failAt: "2b"}

	err := formatsteps.Run(context.Background(), held.component())
	if err == nil || err.Error() != "moving this store from format 2 to 3: 2b: the disk is full" {
		t.Fatalf("a failed operation should stop the run naming the step and the operation, got %v", err)
	}

	version, _ := formatsteps.Read(held.dir)
	if version != 2 || !slices.Equal(held.applied, []string{"1a@1", "1b@1", "2a@2"}) {
		t.Fatalf("the component should stay at the failed step's format, got %d after %v", version, held.applied)
	}

	held.failAt, held.applied = "", nil

	err = formatsteps.Run(context.Background(), held.component())
	if err != nil || !slices.Equal(held.applied, []string{"2a@2", "2b@2", "3a@3", "3b@3"}) {
		t.Errorf("the next run should start the failed step again from its first operation, got %v %v", held.applied, err)
	}
}

func TestACancelledRunAppliesNoFurtherOperation(t *testing.T) {
	held := &recorded{dir: filepath.Join(t.TempDir(), "store")}
	ctx, cancel := context.WithCancel(context.Background())
	component := held.component()
	listed := component.Steps[0].Ops

	component.Steps[0].Ops = func(ctx context.Context) ([]formatsteps.Operation, error) {
		operations, err := listed(ctx)
		first := operations[0].Apply
		operations[0].Apply = func(ctx context.Context) error {
			cancel()

			return first(ctx)
		}

		return operations, err
	}

	err := formatsteps.Run(ctx, component)
	if !errors.Is(err, context.Canceled) || !slices.Equal(held.applied, []string{"1a@1"}) {
		t.Errorf("a cancelled run should stop before its next operation, got %v after %v", err, held.applied)
	}
}

func TestRunRefusesATableItCannotFollow(t *testing.T) {
	none := func(context.Context) ([]formatsteps.Operation, error) { return nil, nil }
	lock := func(context.Context) (func(), error) { return func() {}, nil }

	for _, tc := range []struct {
		name  string
		steps []formatsteps.Step
		lock  func(context.Context) (func(), error)
		want  error
	}{
		{name: "a step skipping a format", steps: []formatsteps.Step{{From: 1, To: 3, Ops: none}}, lock: lock, want: formatsteps.ErrTable},
		{name: "a gap between steps", steps: []formatsteps.Step{{From: 1, To: 2, Ops: none}, {From: 3, To: 4, Ops: none}}, lock: lock,
			want: formatsteps.ErrTable},
		{name: "a step without operations", steps: []formatsteps.Step{{From: 1, To: 2}}, lock: lock, want: formatsteps.ErrTable},
		{name: "no lock", steps: []formatsteps.Step{{From: 1, To: 2, Ops: none}}, want: formatsteps.ErrTable},
		{name: "a format older than the first step", steps: []formatsteps.Step{{From: 3, To: 4, Ops: none}}, lock: lock,
			want: formatsteps.ErrOlder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "store")

			err := formatsteps.Run(context.Background(), formatsteps.Component{Name: "this store", Dir: dir, Root: filepath.Dir(dir),
				Lock: tc.lock, Steps: tc.steps})
			if !errors.Is(err, tc.want) {
				t.Errorf("the run should refuse, got %v", err)
			}

			if test.Exists(t, filepath.Join(dir, formatsteps.FileName)) {
				t.Error("a refused run should write no format")
			}
		})
	}
}

func TestReadTakesAMissingFileForFormat1(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		version int
		fails   bool
	}{
		{name: "absent", version: 1},
		{name: "recorded", content: `{"format":7}`, version: 7},
		{name: "not JSON", content: `seven`, fails: true},
		{name: "below 1", content: `{"format":0}`, fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != "" {
				test.WriteFile(t, filepath.Join(dir, formatsteps.FileName), tc.content)
			}

			version, err := formatsteps.Read(dir)
			if (err != nil) != tc.fails || version != tc.version {
				t.Errorf("the format read should match what the file records, got %d %v", version, err)
			}
		})
	}
}
