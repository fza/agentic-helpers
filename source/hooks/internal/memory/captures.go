package memory

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// graphMark is how the graph's directory of the current month stood at the
// session's last tool call: the month, the directory, its stamp, and the entry
// files it held. A directory of noGraph marks a project without a graph.
type graphMark struct {
	Month string   `json:"month"`
	Dir   string   `json:"dir"`
	Stamp string   `json:"stamp"`
	Names []string `json:"names,omitempty"`
}

const noGraph = "-"

func (mem *memory) graphMarkPath(session string) string {
	return filepath.Join(mem.cacheDir(), "graph-seen", cmp.Or(session, "no-session")+".json")
}

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

	had := readJSON(mem.graphMarkPath(session), &mark)

	month := mark.Dir
	if !had || mark.Month != monthKey || month == "" {
		month = noGraph

		entries := mem.graphProject(ctx).entries
		if entries != "" {
			month = filepath.Join(entries, now.Format("2006"), now.Format("01"))
		}
	}

	if month == noGraph {
		_ = writeJSON(mem.graphMarkPath(session), graphMark{Month: monthKey, Dir: noGraph})

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

	var names, gained []string

	for _, file := range files {
		id := now.Format("200601") + strings.TrimSuffix(file.Name(), ".md")
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") || !isGraphID(id) {
			continue
		}

		names = append(names, file.Name())

		if had && (mark.Month != monthKey || !slices.Contains(mark.Names, file.Name())) {
			gained = append(gained, id)
		}
	}

	_ = writeJSON(mem.graphMarkPath(session), graphMark{Month: monthKey, Dir: month, Stamp: stamp, Names: names})

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
