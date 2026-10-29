package space

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// LedgerName is a component's size ledger, in the component's own directory:
// a journal (memorylog) of one RecordLedgerFile record per data file below the
// directory, rewritten whole whenever a writer of the component has changed
// a file. The space figures read ledgers, never the files they list.
const LedgerName = ".ledger"

// ledgerSchema is the body of a RecordLedgerFile: flags (1 counted, 2 held),
// the length as a uvarint, the crc32c, the modification time in Unix
// nanoseconds, the bytes the space accounting counts as a uvarint, and the
// slash-separated name below the directory as the rest.
const ledgerSchema = 1

const (
	flagCounted = 1 << iota
	flagHeld
)

var errNotLedger = errors.New("not a size ledger")

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// LedgerFile is one data file a ledger lists: its length, checksum and time
// on disk. Counted says the space accounting counts Bytes of it as stored,
// Held as kept: bytes nothing may remove to make room. Bytes is what its
// component recorded at write time, the size of what the file holds where it
// is stored compressed.
type LedgerFile struct {
	Name     string
	Length   int64
	CRC      uint32
	Modified time.Time
	Counted  bool
	Held     bool
	Bytes    int64
}

// Account is what the space accounting makes of one file. Bytes below zero
// counts its length. Carry accounts for it as the ledger did before, and
// counts nothing of a file the ledger did not list: a file in the store no
// write of its component recorded.
type Account struct {
	Counted bool
	Held    bool
	Bytes   int64
	Carry   bool
}

// Classify accounts for the file at name, a slash-separated path below the
// component's directory.
type Classify func(name string) Account

// LedgerRecord is file as a journal record.
func LedgerRecord(file LedgerFile) memorylog.Record {
	var flags byte
	if file.Counted {
		flags |= flagCounted
	}

	if file.Held {
		flags |= flagHeld
	}

	body := []byte{flags}
	body = binary.AppendUvarint(body, uint64(max(file.Length, 0)))
	body = binary.LittleEndian.AppendUint32(body, file.CRC)
	body = binary.LittleEndian.AppendUint64(body, uint64(file.Modified.UnixNano()))
	body = binary.AppendUvarint(body, uint64(max(file.Bytes, 0)))
	body = append(body, file.Name...)

	return memorylog.Record{Kind: memorylog.RecordLedgerFile, Schema: ledgerSchema, Body: body}
}

// DecodeLedgerFile reads a RecordLedgerFile record.
func DecodeLedgerFile(record memorylog.Record) (LedgerFile, error) {
	if record.Kind != memorylog.RecordLedgerFile || record.Schema != ledgerSchema || len(record.Body) < 1 {
		return LedgerFile{}, fmt.Errorf("%w: record kind %d schema %d", errNotLedger, record.Kind, record.Schema)
	}

	length, read := binary.Uvarint(record.Body[1:])
	if read <= 0 || length > 1<<62 || len(record.Body) < 1+read+12 {
		return LedgerFile{}, fmt.Errorf("%w: a file record cut short", errNotLedger)
	}

	rest := record.Body[1+read:]

	accounted, counted := binary.Uvarint(rest[12:])
	if counted <= 0 || accounted > 1<<62 {
		return LedgerFile{}, fmt.Errorf("%w: a file record cut short", errNotLedger)
	}

	return LedgerFile{
		Name:     string(rest[12+counted:]),
		Length:   int64(length),
		CRC:      binary.LittleEndian.Uint32(rest[:4]),
		Modified: time.Unix(0, int64(binary.LittleEndian.Uint64(rest[4:12]))).UTC(),
		Counted:  record.Body[0]&flagCounted != 0,
		Held:     record.Body[0]&flagHeld != 0,
		Bytes:    int64(accounted),
	}, nil
}

// ReadLedger is the ledger of the component at dir: every file it lists,
// none where it has none or it cannot be read.
func ReadLedger(dir string) []LedgerFile {
	path := filepath.Join(dir, LedgerName)

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > ledgerLimit {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	records, _, _ := memorylog.ReadJournal(data)

	files := make([]LedgerFile, 0, len(records))

	for _, record := range records {
		file, err := DecodeLedgerFile(record)
		if err == nil && file.Length >= 0 {
			files = append(files, file)
		}
	}

	return files
}

// LedgerTotal sums what a ledger's files count and hold.
func LedgerTotal(files []LedgerFile) Record {
	var total Record

	for _, file := range files {
		if file.Counted {
			total.Stored = Add(total.Stored, file.Bytes)
		}

		if file.Held {
			total.Held = Add(total.Held, file.Bytes)
		}
	}

	return total
}

// Refresh rewrites the ledger of the component at dir from the files below
// it, holding the ledger's own lock, so the last of several writers to
// refresh lists what every one of them left. Lengths and times come from a
// stat of each file; a checksum is carried over for a file whose length and
// time are those the ledger last listed, and computed for the rest. Lock
// files, temp files and links are no data files and stay out. A directory
// that does not exist keeps no ledger.
func Refresh(ctx context.Context, dir string, classify Classify) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("refreshing the ledger of %s: %w", dir, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("refreshing the ledger of %s: not a directory", dir)
	}

	lock, err := os.OpenFile(filepath.Join(dir, LedgerName+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening the ledger lock of %s: %w", dir, err)
	}

	defer func() { _ = lock.Close() }()

	err = filelock.Exclusive(lock)
	if err != nil {
		return fmt.Errorf("locking the ledger of %s: %w", dir, err)
	}

	defer func() { _ = filelock.Unlock(lock) }()

	known := map[string]LedgerFile{}
	for _, file := range ReadLedger(dir) {
		known[file.Name] = file
	}

	files, err := scan(ctx, dir, known, classify)
	if err != nil {
		return err
	}

	records := make([]memorylog.Record, 0, len(files))
	for _, file := range files {
		records = append(records, LedgerRecord(file))
	}

	err = atomicfile.Write(filepath.Join(dir, LedgerName), memorylog.EncodeJournal(records...), 0o600)
	if err != nil {
		return fmt.Errorf("writing the ledger of %s: %w", dir, err)
	}

	return nil
}

// dataFile is whether a file of this name is data the ledger lists.
func dataFile(name string) bool {
	return !strings.HasPrefix(name, LedgerName) && name != ".lock" && !strings.HasSuffix(name, ".lock") &&
		!strings.HasPrefix(name, ".tmp")
}

func scan(ctx context.Context, dir string, known map[string]LedgerFile, classify Classify) ([]LedgerFile, error) {
	var files []LedgerFile

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			return err
		}

		ctxErr := ctx.Err()
		if ctxErr != nil {
			return ctxErr
		}

		if entry.IsDir() || !entry.Type().IsRegular() || !dataFile(entry.Name()) {
			return nil
		}

		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		name := filepath.ToSlash(rel)
		file := LedgerFile{Name: name, Length: info.Size(), Modified: info.ModTime().UTC(), Bytes: -1}

		var account Account
		if classify != nil {
			account = classify(name)
			file.Counted, file.Held, file.Bytes = account.Counted, account.Held, account.Bytes
		}

		before, found := known[name]
		unchanged := found && before.Length == file.Length && before.Modified.Equal(file.Modified)

		switch {
		case classify == nil:
		case account.Carry && found:
			file.Counted, file.Held, file.Bytes = before.Counted, before.Held, before.Bytes
		case account.Carry:
			file.Counted, file.Held, file.Bytes = false, false, file.Length
		}

		if file.Bytes < 0 {
			file.Bytes = file.Length
		}

		if unchanged {
			file.CRC = before.CRC
		} else {
			file.CRC, file.Length, err = checksum(path)
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			if err != nil {
				return err
			}
		}

		files = append(files, file)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the files of %s: %w", dir, err)
	}

	slices.SortFunc(files, func(a, b LedgerFile) int { return strings.Compare(a.Name, b.Name) })

	return files, nil
}

// checksum is a file's crc32c and the length it was read at.
func checksum(path string) (uint32, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}

	defer func() { _ = file.Close() }()

	hash := crc32.New(castagnoli)

	length, err := io.Copy(hash, file)
	if err != nil {
		return 0, 0, err
	}

	return hash.Sum32(), length, nil
}
