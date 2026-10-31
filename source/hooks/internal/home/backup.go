package home

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// BackupLifetime is how long a migration backup is kept, fixed in code: a
// value nobody tunes earns no option.
const BackupLifetime = 30 * 24 * time.Hour

// BackupStamp is the UTC stamp closing the name of every backup folder a
// migration writes, `<what>-<stamp>`.
const BackupStamp = "20060102T150405.000000000Z"

// ExpireBackups removes every folder of dir, below root, whose name closes in
// a backup stamp at least BackupLifetime before now. The age is read from the
// name, never the folder's times, so a copy or a touch neither keeps a backup
// alive nor ages it early. A name without a readable stamp, a file and a link
// stay; dir missing is nothing to do.
func ExpireBackups(ctx context.Context, root string, dir string, now time.Time) error {
	held, err := safefs.At(root, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("opening the backups: %w", err)
	}

	defer held.Close()

	listed, err := held.Entries()
	if err != nil {
		return err
	}

	var errs []error

	for _, entry := range listed {
		if ctx.Err() != nil {
			break
		}

		if !entry.IsDir() {
			continue
		}

		stamp, readable := backupStamp(entry.Name())
		if !readable || now.Sub(stamp) < BackupLifetime {
			continue
		}

		errs = append(errs, held.RemoveAll(entry.Name()))
	}

	return errors.Join(append(errs, ctx.Err())...)
}

// backupStamp reads the stamp after the last dash of name.
func backupStamp(name string) (time.Time, bool) {
	cut := strings.LastIndex(name, "-")
	if cut <= 0 {
		return time.Time{}, false
	}

	stamp, err := time.Parse(BackupStamp, name[cut+1:])
	if err != nil {
		return time.Time{}, false
	}

	return stamp, true
}
