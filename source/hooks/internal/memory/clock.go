package memory

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/space"
)

// clockMarkName is the store's high-water mark, beside its log: the newest
// time the store holds, so a system clock set back never stamps a time
// before one already stored. Older releases ignore the file, and a store
// without one starts from the newest stamp in its log, so the store format
// stays as it is.
const clockMarkName = "clock.json"

type clockFile struct {
	Mark time.Time `json:"mark"`
}

// storeClock is the mark of the store named store as this process read or
// wrote it, and latest the latest time now answered on it.
type storeClock struct {
	store  string
	read   bool
	mark   time.Time
	latest time.Time
}

func (mem *memory) clockPath() string { return filepath.Join(mem.store(), clockMarkName) }

// now is memory's time, in UTC so every time the store and its cache write
// reads the same whatever zone the machine runs in; printed stamps turn it
// local. While the system clock trails the store's mark, memory's time stands
// one nanosecond past the mark, so everything stamped then sorts after
// everything stored before, and no age reads negative. A clock standing
// exactly at the mark is taken as it is, as two stamps in one instant always
// were: a nanosecond added there would shift every age a clock that has not
// moved reads.
func (mem *memory) now() time.Time {
	at := mem.env.Now().UTC()

	mark := mem.storeMark()
	if at.Before(mark) {
		at = mark.Add(time.Nanosecond)
	}

	if at.After(mem.clock.latest) {
		mem.clock.latest = at
	}

	return at
}

// storeMark is the store's mark, read once per store and call: the mark file
// where one is readable, else the newest stamp in the log.
func (mem *memory) storeMark() time.Time {
	if mem.clock.read && mem.clock.store == mem.key {
		return mem.clock.mark
	}

	mem.clock = storeClock{store: mem.key, read: true, mark: mem.markOnDisk()}

	return mem.clock.mark
}

func (mem *memory) markOnDisk() time.Time {
	var held clockFile
	if readJSON(mem.clockPath(), &held) {
		return held.Mark.UTC()
	}

	return mem.newestLogged()
}

// newestLogged is the newest stamp the log holds; zero where it holds none
// or cannot be read.
func (mem *memory) newestLogged() time.Time {
	var newest time.Time

	_, err := os.Stat(mem.logDir())
	if err != nil {
		return newest
	}

	log, err := mem.openLog()
	if err != nil {
		return newest
	}

	snapshot, err := log.Snapshot(context.Background())
	if err != nil {
		return newest
	}

	defer func() { _ = snapshot.Close() }()

	for _, row := range snapshot.Rows() {
		if row.Time.After(newest) {
			newest = row.Time
		}
	}

	return newest.UTC()
}

// keepMark raises the store's mark to the latest time now answered, after a
// write that stored one: the log or a claim (through recordStore), a slot,
// the heat cache. Every stored time comes from now, so the latest answer is
// never earlier than what was just written. It never creates a store, and
// never writes to a store this release cannot write. It says whether it
// wrote the mark.
func (mem *memory) keepMark() bool {
	latest := mem.clock.latest
	if mem.outsideGit || mem.unreadable != "" || !mem.storeExists() {
		return false
	}

	// Read afresh: another process may have raised the mark further since
	// this one read it, and the higher mark stays.
	if !latest.After(mem.markOnDisk()) {
		return false
	}

	err := writeJSON(mem.env.Roots.Data, mem.clockPath(), clockFile{Mark: latest})
	if err != nil {
		return false
	}

	mem.clock.mark = latest

	return true
}

// keepMarkOfCache raises the mark after a write to the cache, which keeps
// no store ledger, so the store's ledger takes the mark's new bytes here.
func (mem *memory) keepMarkOfCache() {
	if mem.keepMark() {
		_ = space.Refresh(context.Background(), mem.env.Roots.Data, mem.store(), nil)
	}
}
