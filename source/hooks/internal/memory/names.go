package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// namesFile is the store's name index: every entry's name by its id. A name
// is an alias the user can say; the id stays the entry's identity in the log.
// A name, once given, is never given to another entry, even after its own
// entry is purged, so a name a user once said never comes to mean another.
const namesFile = "names.json"

const (
	nameWords     = 2
	nameMaxWords  = 3
	nameShortest  = 8
	nameWordLimit = 20
	unnamedTitle  = "entry"
)

// stopWords are left out of a name: English and German function words, the
// languages entries are mostly written in. A word both languages use as a
// content word (`man`, `war`, `hat`) stays in.
var stopWords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`
		a about above after again against all also am an and any are as at be because been before being
		below between both but by can cannot could did do does doing down during each else ever every few
		for from further had has have having he her here hers him his how if in into is it its itself just
		may me might more most must my no nor not now of off on once one only or other our ours out over
		own per same shall she should so some such than that the their them then there these they this
		those through to too under until up upon us very via was we were what when where which while who
		whom why will with within without would yet you your
		aber alle als am an auch auf aus bei beim bin bis bist da damit dann das dass dem den der des dich
		dir doch du durch ein eine einem einen einer eines er es etwas fur gegen habe haben ich ihr im in
		ins ist ja jede jedem jeden jeder jedes kein keine mich mir mit nach nicht noch nur ob oder ohne
		sein seine sich sie sind so uber um und uns unter vom von vor wann was weil wenn wer wie wir wird
		wo zu zum zur`) {
		stopWords[word] = true
	}
}

// nameIndex is the name index as one process reads it.
type nameIndex struct {
	names map[string]string
	ids   map[string]string
}

func newNameIndex(names map[string]string) nameIndex {
	index := nameIndex{names: map[string]string{}, ids: map[string]string{}}

	for id, name := range names {
		index.add(id, name)
	}

	return index
}

func (index nameIndex) add(id string, name string) {
	index.names[id] = name
	index.ids[name] = id
}

func (index nameIndex) remove(id string) {
	delete(index.ids, index.names[id])
	delete(index.names, id)
}

// taken is whether name cannot go to a new entry: another entry has it, or
// it is what the state ledger is addressed by. No name takes an id's shape,
// since a name's first word is never a single character.
func (index nameIndex) taken(name string) bool {
	_, used := index.ids[name]

	return used || name == ledgerID
}

// name gives the entry id a name from title, unless it has one.
func (index nameIndex) name(id string, title string) string {
	if held, named := index.names[id]; named {
		return held
	}

	name := freeName(nameBase(title), index.taken)
	index.add(id, name)

	return name
}

func (mem *memory) namesPath() string { return filepath.Join(mem.store(), namesFile) }

func (mem *memory) loadNames() (nameIndex, error) {
	raw, err := os.ReadFile(mem.namesPath())
	if errors.Is(err, fs.ErrNotExist) {
		return newNameIndex(nil), nil
	}

	if err != nil {
		return nameIndex{}, fmt.Errorf("reading the name index: %w", err)
	}

	var names map[string]string

	err = json.Unmarshal(raw, &names)
	if err != nil {
		return nameIndex{}, fmt.Errorf("parsing the name index: %w", err)
	}

	return newNameIndex(names), nil
}

func (mem *memory) saveNames(index nameIndex) error {
	data, err := json.MarshalIndent(index.names, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the name index: %w", err)
	}

	err = atomicfile.Write(mem.env.Roots.Data, mem.namesPath(), append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("placing the name index: %w", err)
	}

	return nil
}

// nameBase is the name title suggests before a clash is numbered apart: its
// first content words, lowercased and stripped of accents, two of them, or
// three where two make a name shorter than nameShortest. A title of stop
// words alone is named by those; one with no letter or digit an ASCII name
// can hold is named unnamedTitle.
func nameBase(title string) string {
	words := titleWords(title)

	content := slices.DeleteFunc(slices.Clone(words), func(word string) bool { return stopWords[word] })
	if len(content) == 0 {
		content = words
	}

	if len(content) == 0 {
		return unnamedTitle
	}

	taken := content[:min(nameWords, len(content))]
	if len(content) > nameWords && len(strings.Join(taken, "-")) < nameShortest {
		taken = content[:nameMaxWords]
	}

	return strings.Join(taken, "-")
}

// titleWords are the words of title a name can be made of: runs of ASCII
// letters and digits after accents are stripped, each cut to nameWordLimit,
// single characters left out.
func titleWords(title string) []string {
	var plain strings.Builder

	for _, char := range norm.NFD.String(strings.ToLower(title)) {
		switch {
		case unicode.Is(unicode.Mn, char):
		case char == 'ß':
			plain.WriteString("ss")
		case char <= unicode.MaxASCII && (unicode.IsLetter(char) || unicode.IsDigit(char)):
			plain.WriteRune(char)
		default:
			plain.WriteRune(' ')
		}
	}

	var words []string

	for _, word := range strings.Fields(plain.String()) {
		if len(word) > 1 {
			words = append(words, word[:min(len(word), nameWordLimit)])
		}
	}

	return words
}

// mintName gives the entry id about to be filed a name, recorded in the
// index before the entry reaches the log, under the store lock, so two
// filings never take one name. forget takes the name back where the filing
// fails; a kill between the two leaves a name no entry holds, never an entry
// without one.
func (mem *memory) mintName(ctx context.Context, id string, title string) (func(), error) {
	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return nil, err
	}

	defer unlock()

	index, err := mem.loadNames()
	if err != nil {
		return nil, err
	}

	index.name(id, title)

	err = mem.saveNames(index)
	if err != nil {
		return nil, err
	}

	forget := func() {
		unlock, err := mem.lockStore(ctx)
		if err != nil {
			return
		}

		defer unlock()

		index, err := mem.loadNames()
		if err != nil {
			return
		}

		index.remove(id)

		_ = mem.saveNames(index)
		mem.recordStore(ctx)
	}

	return forget, nil
}

func (mem *memory) nameEntries(folded *state) {
	index, err := mem.loadNames()
	if err != nil {
		return
	}

	for id, held := range folded.entries {
		held.name = index.names[id]
	}
}

func (held *entry) label() string {
	if held.name == "" {
		return held.id
	}

	return held.id + " (" + held.name + ")"
}

// resolveNames turns every entry name a request carries into the entry's id,
// wherever the command takes an id: ids, the entry a link starts from and its
// memory target, a successor, absorbed entries, links and items naming an
// entry. A word the index does not hold stays as given, so an id, a graph id,
// a todo item's text or a store id passes through. An unreadable index
// resolves nothing, and a filing then refuses naming why.
func (mem *memory) resolveNames(req *request, command string) {
	index, _ := mem.loadNames()

	resolve := func(given string) string {
		if id, named := index.ids[strings.ToLower(strings.TrimSpace(given))]; named {
			return id
		}

		return given
	}

	addressed := func(given string) string {
		entry, item, isItem := strings.Cut(given, "#")
		if !isItem {
			return resolve(given)
		}

		return resolve(entry) + "#" + item
	}

	req.ID = addressed(req.ID)
	req.By = resolve(req.By)

	for _, list := range [][]string{req.IDs, req.Absorbs, req.Links, req.Items, req.Add} {
		for position, given := range list {
			list[position] = addressed(given)
		}
	}

	if command == "link" || command == "unlink" {
		req.Target = resolve(req.Target)
	}
}

// nameSteps move a format-8 store to format 9: every entry the log holds gets
// a name from its title as it stands, in filing order, so of two entries
// suggesting one name the older keeps it bare. A run carrying a cut step on
// finds the index whole or absent, since it is written in one replace, and
// names afresh only what it lacks: the names it finds are the ones it would
// give.
func (mem *memory) nameSteps(ctx context.Context) ([]formatsteps.Operation, error) {
	events, err := mem.allEvents(ctx)
	if err != nil {
		return nil, err
	}

	var filed []string

	titles := map[string]string{}

	for _, event := range events {
		if event.Kind != memorylog.KindNew && event.Kind != memorylog.KindRetitle {
			continue
		}

		var titled retitlePayload

		err = json.Unmarshal(event.Payload, &titled)
		if err != nil {
			return nil, fmt.Errorf("reading the title of %s: %w", event.Entry, err)
		}

		if event.Kind == memorylog.KindNew {
			filed = append(filed, event.Entry)
		}

		titles[event.Entry] = titled.Title
	}

	index, err := mem.loadNames()
	if err != nil {
		return nil, err
	}

	for _, id := range filed {
		index.name(id, titles[id])
	}

	return []formatsteps.Operation{{Name: "naming every entry", Apply: func(context.Context) error {
		return mem.saveNames(index)
	}}}, nil
}

// joinNames adds the source's names to the target's index, each entry under
// its own name, a name the target already gives numbered apart.
func joinNames(source *memory, target *memory) error {
	from, err := source.loadNames()
	if err != nil {
		return err
	}

	into, err := target.loadNames()
	if err != nil {
		return err
	}

	for _, id := range slices.Sorted(maps.Keys(from.names)) {
		into.add(id, freeName(from.names[id], into.taken))
	}

	return target.saveNames(into)
}
