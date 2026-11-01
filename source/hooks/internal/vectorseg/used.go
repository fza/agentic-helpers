package vectorseg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// Format names the store format this binary reads and writes, the key its
// use is stamped under: a store goes only once no format has used it within
// the retention period, so a binary of another format keeps the stores it
// still reads.
const Format = "vectorseg/1"

// usedName holds the last-use stamp, after sdd's own stamp file.
const usedName = "used.json"

// stampEvery is how often a use refreshes the stamp: once a day keeps the
// read path at one small write per store a day.
const stampEvery = 24 * time.Hour

// usedLimit bounds the stamp file read: it holds one time per format.
const usedLimit = 64 << 10

// stamps is `used.json`: per store format, when a binary of it last used the
// store, as RFC 3339 UTC.
type stamps map[string]string

// touch refreshes this format's stamp where it is older than a day. The write
// never creates the store's folder, so a read of a store nobody filled yet
// creates nothing. A failure is passed over, since a missed stamp costs at
// worst an early re-embed and never a search.
func (store *Store) touch() {
	now := time.Now().UTC()
	held := store.stamps()

	last, err := time.Parse(time.RFC3339, held[Format])
	if err == nil && now.Sub(last) < stampEvery && !last.After(now) {
		return
	}

	held[Format] = now.Format(time.RFC3339)

	data, err := json.Marshal(held)
	if err != nil {
		return
	}

	_ = atomicfile.Write(store.root, filepath.Join(store.dir, usedName), data, 0o600)
}

func (store *Store) stamps() stamps {
	held := stamps{}

	file, err := os.Open(filepath.Join(store.dir, usedName))
	if err != nil {
		return held
	}

	defer func() { _ = file.Close() }()

	data := make([]byte, usedLimit)

	read, _ := file.Read(data)
	if json.Unmarshal(data[:read], &held) != nil {
		return stamps{}
	}

	return held
}

// LastUse is when any binary last used the store: the newest stamp of any
// format or modification time of its files, whichever is later. A release
// before the stamp writes none, so a store only it uses is judged by its last
// write, and a write such a release makes is never hidden behind an older
// stamp. It answers false where the store holds nothing to judge by.
func (store *Store) LastUse() (time.Time, bool) {
	var newest time.Time

	for _, value := range store.stamps() {
		stamped, err := time.Parse(time.RFC3339, value)
		if err == nil && stamped.After(newest) {
			newest = stamped
		}
	}

	listed, _ := os.ReadDir(store.dir)

	for _, entry := range listed {
		// A lock file is created by whoever takes it, a prune's hold among
		// them, and the stamp file is read for its content: neither is a write.
		if entry.Name() == lockName || entry.Name() == fillName || entry.Name() == usedName {
			continue
		}

		info, err := entry.Info()
		if err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}

	return newest, !newest.IsZero()
}

// ErrHeld is a store another process is writing or filling.
var ErrHeld = errors.New("the vector store is in use")

// TryHold takes the store's segment and fill locks without waiting, so a
// caller removing the store never waits on a write or a fill and never
// removes one under way. It answers ErrHeld while another process holds
// either.
func (store *Store) TryHold() (release func(), err error) {
	var held []*os.File

	release = func() {
		for _, file := range held {
			_ = filelock.Unlock(file)
			_ = file.Close()
		}
	}

	for _, name := range []string{lockName, fillName} {
		file, err := safefs.Lock(store.root, filepath.Join(store.dir, name), 0o600)
		if err != nil {
			release()

			return nil, fmt.Errorf("opening the vector store lock %s: %w", name, err)
		}

		taken, err := filelock.TryExclusive(file)
		if err != nil || !taken {
			_ = file.Close()
			release()

			if err != nil {
				return nil, fmt.Errorf("locking the vector store %s: %w", name, err)
			}

			return nil, ErrHeld
		}

		held = append(held, file)
	}

	return release, nil
}
