package memory

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// graphMark is how the graph stood at the session's last tool call, in brief:
// the month its root was resolved in, the root, every year folder the last
// look knew with its month folders as a set of bits (bit m-1 for month m), and
// a digest of every one of those folders' stamps. A root of noGraph marks a
// project without a graph.
//
// A folder's modification time moves whenever a name in it is added, removed
// or renamed, whoever writes it: a capture, a git pull, a merge or a checkout.
// So a call stats the root, each year and each month and compares the digest;
// only a digest that moved reads graphFolders and lists the folders whose
// stamp moved: a new year shows in the root's stamp, a new month in its
// year's. The brief stays within the slot directory's own twin, so the common
// call reads no class file and decodes no table of folders. Rejected: watching
// the current month alone, which never sees an entry a pull brings into an
// earlier one; listing every month on every call, a directory read per month
// where a stat answers; one mark holding every folder's stamp, a class file
// read and a decode of every folder on every call.
type graphMark struct {
	Month  string            `json:"month"`
	Root   string            `json:"root"`
	Years  map[string]uint16 `json:"years,omitempty"`
	Digest string            `json:"digest,omitempty"`
}

// graphFolders is the session's last look in full: a mark per folder, keyed
// "." for the root, "<yyyy>" for a year and "<yyyy>/<mm>" for a month, and the
// digest of the graphMark written with it. A pair whose digests differ, as a
// write lost between the two leaves it, reads as a first look.
type graphFolders struct {
	Digest  string                `json:"digest"`
	Folders map[string]folderMark `json:"folders,omitempty"`
}

// folderMark is a folder's stamp and, for a month, the position its table of
// names had reached at the session's last look: a name the table met at or
// past it is one the session has not been told of. A name that leaves and
// comes back, as a checkout of an older ref and back moves it, keeps its
// position and is never told twice. Rejected: a bitset per month of the names
// present, which forgets a name the moment it leaves, names it again when it
// returns, and grows with the graph where a position does not.
type folderMark struct {
	Stamp string `json:"s"`
	Next  int    `json:"n,omitempty"`
}

const (
	noGraph = "-"
	// rootFolder keys the graph's root in a graphMark.
	rootFolder = "."
	// graphNamesPrefix keys the table of a month's entry names, one name per
	// line in the order the store first met them: a name's line is its
	// position. Lines are only ever appended, and a table outlives every
	// session, since every session's mark counts positions in it.
	graphNamesPrefix = "graph-table/"
	// legacyNamesPrefix keys the tables an earlier release keeps for the
	// current and the last month and drops for any month before.
	legacyNamesPrefix = "graph-names/"
	// capturesNamed is how many gained entries the notice names before it
	// folds the rest into a count.
	capturesNamed = 3
)

// captureNotice names, once, the graph entries that arrived since the
// session's last tool call while the seat holds open entries waiting for a
// capture, and how to settle one, and names the entries that arrived. An
// entry arrives in the folder of its month, whichever month that is: a
// capture lands in the current one, a pull, a merge or a checkout in any. The
// sdd engine and agentic-capture both reach it, since nothing reads a
// capture's output.
func (mem *memory) captureNotice(ctx context.Context, seatName string, session string) (string, []string) {
	// A session whose id the hook refused keeps no mark, so every call of it
	// would be a first look listing every month and telling nothing.
	if mem.graphMarkKey(session) == "" {
		return "", nil
	}

	now := mem.now().Local()
	monthKey := now.Format("2006/01")

	var mark graphMark

	had := mem.readSlot(mem.graphMarkKey(session), &mark) && mark.Root != ""

	root := mark.Root
	if !had || mark.Month != monthKey {
		root = cmp.Or(mem.graphProject(ctx).entries, noGraph)
		had = had && root == mark.Root
	}

	if root == noGraph {
		if !had || mark.Month != monthKey {
			mem.writeSlot(mem.graphMarkKey(session), &graphMark{Month: monthKey, Root: noGraph})
		}

		return "", nil
	}

	if had && mark.Month == monthKey && quickDigest(root, mark.Years) == mark.Digest {
		return "", nil
	}

	var full graphFolders
	if had {
		mem.readSlot(mem.graphFoldersKey(session), &full)
	}

	held := map[string]folderMark{}
	if had && full.Digest == mark.Digest {
		held = full.Folders
	} else {
		had = false
	}

	look := graphLook{mem: mem, root: root, held: held, folders: make(map[string]folderMark, len(held)), had: had}
	look.walk()

	digest, years := look.summary()
	mem.writeSlot(mem.graphFoldersKey(session), &graphFolders{Digest: digest, Folders: look.folders})
	mem.writeSlot(mem.graphMarkKey(session), &graphMark{Month: monthKey, Root: root, Years: years, Digest: digest})

	gained := look.gained
	if len(gained) == 0 {
		return "", nil
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return "", gained
	}

	var waiting []string

	for _, held := range folded.entryList() {
		if held.status == statusLive && mem.onShelf(held, seatName) && slices.Contains(held.until, "captured") {
			waiting = append(waiting, held.label()+" "+held.title)
		}
	}

	if len(waiting) == 0 {
		return "", gained
	}

	return gainedNotice(gained, waiting, mem.hintChannel(ctx)), gained
}

// gainedNotice is the capture notice's text: every gained entry named, or past
// capturesNamed the count, the ones that matter most, and a fold line naming
// where the rest stand, the state ledger's captured lines.
func gainedNotice(gained []string, waiting []string, at channel) string {
	named := strings.Join(gained, ", ")
	if len(gained) > capturesNamed {
		named = fmt.Sprintf("%d entries, among them %s", len(gained), strings.Join(mattering(gained)[:capturesNamed], ", "))
	}

	notice := fmt.Sprintf("%s: graph gained %s; open todos waiting for a capture: %s; link the one it settles: %s",
		self, named, strings.Join(waiting, ", "), linkCaptureHint().in(at))

	if len(gained) <= capturesNamed {
		return notice
	}

	return notice + "\n" + runBefore(fmt.Sprintf("+%d more gained: %s", len(gained)-capturesNamed, showHint(ledgerID, "with_history").in(at)),
		"concluding none settles a todo")
}

// mattering is gained in the order a waiting todo most likely settles by: the
// decisions first, an owner decision being what such a todo waits for, then
// the newest first, as the session's own capture is.
func mattering(gained []string) []string {
	ranked := slices.Clone(gained)

	slices.SortFunc(ranked, func(left string, right string) int {
		return cmp.Or(cmp.Compare(decisionRank(right), decisionRank(left)), strings.Compare(right, left))
	})

	return ranked
}

// decisionRank is 1 for a decision's graph id, 0 for any other.
func decisionRank(id string) int {
	parts := strings.SplitN(id, "-", 4)
	if len(parts) > 2 && parts[2] == "d" {
		return 1
	}

	return 0
}

// graphLook is one look over the graph's folders against the session's last.
type graphLook struct {
	mem     *memory
	root    string
	held    map[string]folderMark
	folders map[string]folderMark
	gained  []string
	had     bool
}

// walk stats the root, every year and every month the last look knew, and
// lists a folder only where its stamp moved: the root for new years, a year
// for new months, a month for its entries. A folder gone keeps its mark under
// the stamp of a missing folder, so its entries are not told again when it
// comes back.
func (look *graphLook) walk() {
	years := look.known(rootFolder)
	if look.stamp(rootFolder) {
		years = withListed(years, look.root, "", isYear)
	}

	for _, year := range years {
		months := look.known(year)
		if look.stamp(year) {
			months = withListed(months, filepath.Join(look.root, year), year+"/", isMonth)
		}

		for _, month := range months {
			if look.stamp(month) {
				look.month(month, look.held[month].Next)
			}
		}
	}
}

// known is the folders the last look knew right under folder, in order.
func (look *graphLook) known(folder string) []string {
	var below []string

	for key := range look.held {
		parent, _, nested := strings.Cut(key, "/")

		switch {
		case key == rootFolder:
		case folder == rootFolder && !nested:
			below = append(below, key)
		case nested && parent == folder:
			below = append(below, key)
		}
	}

	slices.Sort(below)

	return below
}

// stamp keeps the folder's stamp, and is whether it moved since the last
// look, as it has for a folder the last look did not know.
func (look *graphLook) stamp(folder string) bool {
	stamp := string(appendStamp(nil, filepath.Join(look.root, filepath.FromSlash(folder))))

	before, known := look.held[folder]
	look.folders[folder] = folderMark{Stamp: stamp, Next: before.Next}

	return !known || before.Stamp != stamp
}

// summary is the digest of the look's folders and its years, each with the
// set of its months, as graphMark keeps them.
func (look *graphLook) summary() (string, map[string]uint16) {
	years := map[string]uint16{}

	for key := range look.folders {
		year, month, nested := strings.Cut(key, "/")

		switch {
		case key == rootFolder:
		case nested:
			years[year] |= 1 << (monthNumber(month) - 1)
		default:
			_, listed := years[year]
			if !listed {
				years[year] = 0
			}
		}
	}

	digest := newFolderDigest()

	for _, key := range slices.Sorted(maps.Keys(look.folders)) {
		digest.add(key, []byte(look.folders[key].Stamp))
	}

	return digest.sum(), years
}

// quickDigest is the digest of the folders years names under root as they
// stand: the stats of a call that finds the graph as its last look left it,
// and nothing more.
func quickDigest(root string, years map[string]uint16) string {
	digest := newFolderDigest()
	stamp := make([]byte, 0, 48)

	digest.add(rootFolder, appendStamp(stamp[:0], root))

	months := [...]string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"}

	for _, year := range slices.Sorted(maps.Keys(years)) {
		yearDir := root + string(filepath.Separator) + year
		digest.add(year, appendStamp(stamp[:0], yearDir))

		for index, month := range months {
			if years[year]&(1<<index) != 0 {
				digest.add(year+"/"+month, appendStamp(stamp[:0], yearDir+string(filepath.Separator)+month))
			}
		}
	}

	return digest.sum()
}

// folderDigest is the folder keys in order, each with its stamp, hashed once
// whole.
type folderDigest struct{ lines []byte }

func newFolderDigest() *folderDigest { return &folderDigest{lines: make([]byte, 0, 2048)} }

func (digest *folderDigest) add(key string, stamp []byte) {
	digest.lines = append(append(append(append(digest.lines, key...), 0), stamp...), '\n')
}

func (digest *folderDigest) sum() string {
	hashed := fnv.New64a()
	_, _ = hashed.Write(digest.lines)

	return strconv.FormatUint(hashed.Sum64(), 36)
}

// appendStamp appends the folder's stamp, its modification time and size, or
// noGraph where it is missing.
func appendStamp(stamp []byte, folder string) []byte {
	info, err := os.Stat(folder)
	if err != nil {
		return append(stamp, noGraph...)
	}

	stamp = strconv.AppendInt(stamp, info.ModTime().UnixNano(), 10)
	stamp = append(stamp, '@')

	return strconv.AppendInt(stamp, info.Size(), 10)
}

func isYear(name string) bool { return len(name) == len("2006") && allDigits(name) }

func isMonth(name string) bool { return len(name) == len("01") && monthNumber(name) > 0 }

// monthNumber is the month a folder's two digits name, 0 for none.
func monthNumber(name string) int {
	number, err := strconv.Atoi(name)
	if err != nil || len(name) != len("01") || number < 1 || number > 12 {
		return 0
	}

	return number
}

// month lists a month whose stamp moved, takes its names into the month's
// table and counts as gained every name the table met at or past next, the
// position the last look reached; a first look counts none.
func (look *graphLook) month(month string, next int) {
	files, _ := os.ReadDir(filepath.Join(look.root, filepath.FromSlash(month)))
	prefix := strings.ReplaceAll(month, "/", "")

	var names []string

	for _, file := range files {
		name, isEntry := strings.CutSuffix(file.Name(), ".md")
		if !file.IsDir() && isEntry && isGraphID(prefix+name) {
			names = append(names, name)
		}
	}

	positions := look.mem.captureNames(month, names)

	reached := 0
	for _, position := range positions {
		reached = max(reached, position+1)
	}

	if look.had {
		for _, name := range names {
			position, known := positions[name]
			if known && position >= next {
				look.gained = append(look.gained, prefix+name)
			}
		}
	}

	mark := look.folders[month]
	mark.Next = reached
	look.folders[month] = mark
}

// withListed is known with every folder under dir whose name shaped names,
// keyed after prefix, in order.
func withListed(known []string, dir string, prefix string, shaped func(string) bool) []string {
	files, _ := os.ReadDir(dir)

	for _, file := range files {
		key := prefix + file.Name()
		if file.IsDir() && shaped(file.Name()) && !slices.Contains(known, key) {
			known = append(known, key)
		}
	}

	slices.Sort(known)

	return known
}

func allDigits(name string) bool {
	return !strings.ContainsFunc(name, func(char rune) bool { return char < '0' || char > '9' })
}

// namesPerPart is how many names one part of a month's table holds: a busy
// month's table grows by parts, so no value grows without end. A name is a
// graph id's 19 bytes and its line break, so a part takes 120,000 bytes.
const namesPerPart = 6_000

// namesKey is the key of a part of the month's table of names; the first
// part is keyed by the month alone, as a table of one part always was.
func namesKey(monthKey string, part int) string {
	if part == 0 {
		return graphNamesPrefix + monthKey
	}

	return fmt.Sprintf("%s%s/%d", graphNamesPrefix, monthKey, part)
}

// legacyNamesMonth is the month a key of an earlier release's table of names
// belongs to.
func legacyNamesMonth(key string) (string, bool) {
	rest, isNames := strings.CutPrefix(key, legacyNamesPrefix)
	if !isNames || len(rest) < len("2006/01") {
		return "", false
	}

	return rest[:len("2006/01")], true
}

// firstNames is the names a part holds of table, its first ones.
func firstNames(table []byte) []byte {
	end := 0

	for range namesPerPart {
		next := bytes.IndexByte(table[end:], '\n')
		if next < 0 {
			break
		}

		end += next + 1
	}

	return table[:end]
}

// captureNames is the position of each name in the store's table of the
// month's entry names, names it lacks appended to its last part under the
// slot file's writer lock, so sessions adding names at once never give two
// names one position. A part full, the names go to a new one. A name whose
// write failed has no position: it counts as nothing gained until a later
// look keeps it.
func (mem *memory) captureNames(monthKey string, names []string) map[string]int {
	slotFile := mem.slotFile()

	positions, last := monthNames(slotFile, monthKey)

	missing := slices.DeleteFunc(slices.Clone(names), func(name string) bool { _, known := positions[name]; return known })

	for part := last; len(missing) > 0; part++ {
		err := slotFile.Update(context.Background(), namesKey(monthKey, part), func(held []byte, _ bool) ([]byte, error) {
			lines := bytes.Count(held, []byte("\n"))

			for name, line := range namePositions(held) {
				_, known := positions[name]
				if !known {
					positions[name] = part*namesPerPart + line
				}
			}

			grown := held

			var left []string

			for _, name := range missing {
				_, known := positions[name]

				switch {
				case known:
				case lines >= namesPerPart:
					left = append(left, name)
				default:
					positions[name] = part*namesPerPart + lines
					lines++
					grown = append(grown, name+"\n"...)
				}
			}

			missing = left

			if len(grown) == len(held) {
				return nil, slots.ErrUnchanged
			}

			return grown, nil
		})
		if err != nil {
			mem.reportSlot("keeping", namesKey(monthKey, part), err)
			positions, _ = monthNames(slotFile, monthKey)

			break
		}
	}

	return positions
}

// monthNames is the position of every name the month's table holds, and the
// index of its last part.
func monthNames(slotFile *slots.File, monthKey string) (map[string]int, int) {
	positions := map[string]int{}

	for part := 0; ; part++ {
		table, found, _ := slotFile.Get(context.Background(), namesKey(monthKey, part))
		if !found {
			return positions, max(part-1, 0)
		}

		for name, line := range namePositions(table) {
			_, known := positions[name]
			if !known {
				positions[name] = part*namesPerPart + line
			}
		}
	}
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
