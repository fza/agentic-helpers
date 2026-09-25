package carryforward

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const orphanMaxAge = 48 * time.Hour

// sweep frees every seat whose holder is gone, and reports each transcript no
// live seat owns, deleting the ones past the orphan age.
func (hook *hook) sweep(ctx context.Context) []string {
	entries, err := os.ReadDir(hook.transcriptsDir())
	if err != nil {
		return nil
	}

	live := map[string]bool{}

	for role, held := range hook.holders() {
		if hook.ownerAlive(ctx, held.PID, held.Started) {
			live[held.SessionID] = true
		} else {
			_ = os.Remove(hook.lockPath(role))
		}
	}

	var sessions []string

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}

		session, _, _ := strings.Cut(entry.Name(), ".")
		if !slices.Contains(sessions, session) {
			sessions = append(sessions, session)
		}
	}

	slices.Sort(sessions)

	var report []string

	for _, session := range sessions {
		if live[session] {
			continue
		}

		row, found := hook.sweepSession(session)
		if found {
			report = append(report, row)
		}
	}

	return report
}

func (hook *hook) sweepSession(session string) (string, bool) {
	var files []string

	var newest time.Time

	var size int64

	entries, err := os.ReadDir(hook.transcriptsDir())
	if err != nil {
		return "", false
	}

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), session+".") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, filepath.Join(hook.transcriptsDir(), entry.Name()))
		size += info.Size()

		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}

	if len(files) == 0 {
		return "", false
	}

	age := hook.env.Now().Sub(newest)
	short := session
	if len(short) > 8 {
		short = short[:8]
	}

	prefix := fmt.Sprintf("  %s  %.0fh  %d KB  ", short, age.Hours(), size/1024)

	if age > orphanMaxAge {
		for _, file := range files {
			_ = os.Remove(file)
		}

		return prefix + "deleted", true
	}

	info, err := os.Stat(hook.logPath(session))
	if err == nil && info.Size() > 0 {
		return prefix + "log holds unfolded turns", true
	}

	return prefix + "log empty", true
}
