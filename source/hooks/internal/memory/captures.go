package memory

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// graphMark is how the graph's directory of the current month stood at the
// session's last tool call: the month, the directory, its stamp, and the entry
// files it held, as a bitset over the positions of the store's table of the
// month's names. A directory of noGraph marks a project without a graph.
//
// The bitset beat the JSON list of names it replaced on BenchmarkCaptureNotice
// over a month of 2000 entries (Apple M4 Pro): 19 µs, 4 KB and 24 allocations
// a call finding the month unchanged against 125 µs, 209 KB and 2035, and
// level where an entry was added, since that call loads the store either way.
type graphMark struct {
	Month string `json:"month"`
	Dir   string `json:"dir"`
	Stamp string `json:"stamp"`
	Seen  []byte `json:"seen,omitempty"`
}

const (
	noGraph = "-"
	// graphNamesPrefix keys the table of a month's entry names, one name per
	// line in the order the store first met them: a name's line is its
	// position in every session's bitset. Lines are only ever appended.
	graphNamesPrefix = "graph-names/"
)

// captureNotice names, once, the graph entries a tool call added while the
// seat holds open entries waiting for a capture, and how to settle one, and
// counts the entries the call added. A
// capture lands in the graph directory of its month, found once a month
// through the graph resolver; that directory is stat'ed on every call and read
// only when its stamp moved. The sdd engine and agentic-capture both reach it,
// since nothing reads a capture's output.
func (mem *memory) captureNotice(ctx context.Context, seatName string, session string) (string, int) {
	now := mem.now().Local()
	monthKey := now.Format("2006/01")

	var mark graphMark

	had := mem.readSlot(mem.graphMarkKey(session), &mark)

	month := mark.Dir
	if !had || mark.Month != monthKey || month == "" {
		month = noGraph

		entries := mem.graphProject(ctx).entries
		if entries != "" {
			month = filepath.Join(entries, now.Format("2006"), now.Format("01"))
		}
	}

	if month == noGraph {
		_ = mem.writeSlot(mem.graphMarkKey(session), graphMark{Month: monthKey, Dir: noGraph})

		return "", 0
	}

	stamp := "-"

	info, err := os.Stat(month)
	if err == nil {
		stamp = fmt.Sprintf("%d@%d", info.ModTime().UnixNano(), info.Size())
	}

	if had && mark.Month == monthKey && mark.Dir == month && mark.Stamp == stamp {
		return "", 0
	}

	files, _ := os.ReadDir(month)

	var names []string

	for _, file := range files {
		name, isEntry := strings.CutSuffix(file.Name(), ".md")
		if !file.IsDir() && isEntry && isGraphID(now.Format("200601")+name) {
			names = append(names, name)
		}
	}

	positions := mem.captureNames(monthKey, names)

	var (
		seen   []byte
		gained []string
	)

	for _, name := range names {
		position, known := positions[name]
		if !known {
			continue
		}

		for len(seen) <= position/8 {
			seen = append(seen, 0)
		}

		seen[position/8] |= 1 << (position % 8)

		marked := position/8 < len(mark.Seen) && mark.Seen[position/8]&(1<<(position%8)) != 0
		if had && (mark.Month != monthKey || !marked) {
			gained = append(gained, now.Format("200601")+name)
		}
	}

	_ = mem.writeSlot(mem.graphMarkKey(session), graphMark{Month: monthKey, Dir: month, Stamp: stamp, Seen: seen})

	if len(gained) == 0 {
		return "", 0
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return "", len(gained)
	}

	var waiting []string

	for _, held := range folded.entryList() {
		if held.status == statusLive && mem.onShelf(held, seatName) && slices.Contains(held.until, "captured") {
			waiting = append(waiting, held.id+" "+held.title)
		}
	}

	if len(waiting) == 0 {
		return "", len(gained)
	}

	return fmt.Sprintf("%s: graph gained %s; open todos waiting for a capture: %s; link the one it settles: %s link <todo> <graph id>",
		self, strings.Join(gained, ", "), strings.Join(waiting, ", "), self), len(gained)
}

// captureNames is the position of each of names in the store's table of the
// month's entry names, the names it lacks appended under the slot file's
// writer lock, so sessions adding names at once never give two names one
// position. A name a full table has no room for has no position: it marks no
// bit, and its capture goes unnamed.
func (mem *memory) captureNames(monthKey string, names []string) map[string]int {
	key := graphNamesPrefix + monthKey
	slotFile := mem.slotFile()

	table, _, _ := slotFile.Get(context.Background(), key)

	positions := namePositions(table)
	if !slices.ContainsFunc(names, func(name string) bool { _, known := positions[name]; return !known }) {
		return positions
	}

	err := slotFile.Update(context.Background(), key, func(held []byte, _ bool) ([]byte, error) {
		positions = namePositions(held)
		lines := bytes.Count(held, []byte("\n"))

		grown := held
		for _, name := range names {
			_, known := positions[name]
			if !known {
				positions[name] = lines
				lines++
				grown = append(grown, name+"\n"...)
			}
		}

		if len(grown) == len(held) {
			return nil, slots.ErrUnchanged
		}

		return grown, nil
	})
	if err != nil {
		table, _, _ = slotFile.Get(context.Background(), key)
		positions = namePositions(table)
	}

	return positions
}

func namePositions(table []byte) map[string]int {
	positions := map[string]int{}
	position := 0

	for line := range strings.Lines(string(table)) {
		name, whole := strings.CutSuffix(line, "\n")
		if !whole {
			continue
		}

		_, known := positions[name]
		if !known {
			positions[name] = position
		}

		position++
	}

	return positions
}
