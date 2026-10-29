// Package space keeps the machine-wide size figures of agentic-memory's
// attachment store and the agentic-tools library: a ledger of sizes each store
// records as it writes, read instead of walking the store, since a figure the
// brief and every write reply print cannot wait on a stat of every file.
package space

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
)

// LowShare is the share of a limit under which space counts as running low:
// below it, the brief prints the store's space left.
const LowShare = 10

// ledgerLimit bounds a ledger read: a store anything sandboxed can write must
// not make a read exhaust memory.
const ledgerLimit = 16 << 20

const (
	Attachments = "attachments"
	Tools       = "tools"
)

// Record is what one ledger key holds on disk: Stored counts every byte kept,
// Held only those nothing may remove to make room.
type Record struct {
	Stored int64 `json:"stored"`
	Held   int64 `json:"held"`
}

// Figure is a store's space: what it uses against its limit.
type Figure struct {
	Kind  string
	Used  int64
	Limit int64
}

func (figure Figure) Left() int64 { return max(figure.Limit-figure.Used, 0) }

func (figure Figure) Low() bool { return figure.Left()*LowShare < figure.Limit }

func (figure Figure) part() string {
	return fmt.Sprintf("%s %s of %s", figure.Kind, Size(figure.Left()), Size(figure.Limit))
}

// Line is `space left: <kind> <left> of <limit>, ...` for each figure given,
// or nothing where none is.
func Line(figures ...Figure) string {
	parts := make([]string, 0, len(figures))
	for _, figure := range figures {
		parts = append(parts, figure.part())
	}

	if len(parts) == 0 {
		return ""
	}

	return "space left: " + strings.Join(parts, ", ")
}

// LowLine is Line for the figures running low only: the brief's line.
func LowLine(figures ...Figure) string {
	var low []Figure

	for _, figure := range figures {
		if figure.Low() {
			low = append(low, figure)
		}
	}

	return Line(low...)
}

func Size(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	}

	return fmt.Sprintf("%d B", bytes)
}

// Add sums two sizes, saturating instead of wrapping: a figure read from a
// store anything sandboxed can write must not overflow into a small one.
func Add(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}

	return a + b
}

// Total sums a ledger's records.
func Total(records map[string]Record) Record {
	var total Record

	for _, record := range records {
		total.Stored = Add(total.Stored, record.Stored)
		total.Held = Add(total.Held, record.Held)
	}

	return total
}

// Read is the ledger at path, empty where it is missing, unreadable or not a
// regular file.
func Read(path string) map[string]Record {
	records := map[string]Record{}

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > ledgerLimit {
		return records
	}

	file, err := os.Open(path)
	if err != nil {
		return records
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, ledgerLimit))
	if err != nil {
		return records
	}

	var parsed map[string]Record

	err = json.Unmarshal(data, &parsed)
	if err != nil {
		return records
	}

	for key, record := range parsed {
		if record.Stored >= 0 && record.Held >= 0 {
			records[key] = record
		}
	}

	return records
}

// Update changes the ledger at path under its lock, so two writers on the
// machine never lose each other's record.
func Update(path string, change func(records map[string]Record)) error {
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening the lock of %s: %w", path, err)
	}

	defer func() { _ = lock.Close() }()

	err = filelock.Exclusive(lock)
	if err != nil {
		return fmt.Errorf("locking %s: %w", path, err)
	}

	defer func() { _ = filelock.Unlock(lock) }()

	records := Read(path)
	change(records)

	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	err = atomicfile.Write(path, data, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
