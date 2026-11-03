package memory

import (
	"cmp"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

var entryIDShape = regexp.MustCompile(`^m-[a-z0-9]{4,16}$`)

func TestNameBaseTakesTheTitlesFirstContentWords(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{name: "two content words long enough", title: "APFS span of clones", want: "apfs-span"},
		{name: "stop words left out", title: "The gate ignores MCP reads", want: "gate-ignores"},
		{name: "a third word where two are short", title: "Go vet runs first", want: "go-vet-runs"},
		{name: "two short words and no third", title: "Go vet", want: "go-vet"},
		{name: "punctuation and code spans split words", title: "`agentic-memory` keeps names", want: "agentic-memory"},
		{name: "accents stripped", title: "Überprüfung der Größe", want: "uberprufung-grosse"},
		{name: "single characters left out", title: "A b plan for M x", want: "plan"},
		{name: "a title of stop words alone", title: "What is this about", want: "what-is-this"},
		{name: "a title holding no ASCII letter", title: "日本語", want: unnamedTitle},
		{name: "a long word cut", title: "Internationalizationization works", want: "internationalization-works"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nameBase(tc.title); got != tc.want {
				t.Errorf("the name should be the title's first content words, got %q", got)
			}
		})
	}
}

func TestAClashingNameIsNumberedApart(t *testing.T) {
	index := newNameIndex(map[string]string{"m-aaaaaa": "apfs-span"})

	if got := index.name("m-bbbbbb", "APFS span again"); got != "apfs-span-2" {
		t.Errorf("a name another entry holds should take the first free suffix, got %q", got)
	}

	if got := index.name("m-cccccc", "APFS span thrice"); got != "apfs-span-3" {
		t.Errorf("every clash should take the next free suffix, got %q", got)
	}

	if got := index.name("m-aaaaaa", "Retitled entry"); got != "apfs-span" {
		t.Errorf("an entry already named should keep its name whatever its title, got %q", got)
	}
}

func TestANameNeverTakesAnIdsOrTheLedgersShape(t *testing.T) {
	index := newNameIndex(nil)

	if got := index.name("m-aaaaaa", "State"); got != "state-2" {
		t.Errorf("a title suggesting the ledger's address should be numbered apart, got %q", got)
	}

	if got := index.name("m-bbbbbb", "M abcd1234 notes"); entryIDShape.MatchString(got) {
		t.Errorf("a title spelling an id should never give a name in an id's shape, got %q", got)
	}
}

func TestALabelNamesTheEntryBesideItsID(t *testing.T) {
	if got := (&entry{id: "m-aaaaaa", name: "apfs-span"}).label(); got != "m-aaaaaa (apfs-span)" {
		t.Errorf("a named entry's label should give its name beside its id, got %q", got)
	}

	if got := (&entry{id: "m-aaaaaa"}).label(); got != "m-aaaaaa" {
		t.Errorf("an entry the index names nothing should be labelled by its id alone, got %q", got)
	}
}

func TestEveryNameFitsTheKeysPattern(t *testing.T) {
	pattern := regexp.MustCompile("^" + entryRef + "$")
	index := newNameIndex(nil)

	for position, title := range []string{"APFS span", "APFS span", "日本語", "Go vet", "X 1 y", strings.Repeat("word ", 30)} {
		name := index.name("m-"+strings.Repeat(string(rune('a'+position)), 6), title)
		if !pattern.MatchString(name) {
			t.Errorf("every name should pass the MCP keys' pattern, %q does not", name)
		}
	}
}

// TestEveryIDKeyTakesAName holds every key whose schema pins an id's shape to
// take a name in its place, alone and as a todo item's address where the key
// takes one.
func TestEveryIDKeyTakesAName(t *testing.T) {
	type property struct {
		Pattern string `json:"pattern"`
		Items   struct {
			Pattern string `json:"pattern"`
		} `json:"items"`
	}

	pinned := 0

	for _, held := range commands() {
		var schema struct {
			Properties map[string]property `json:"properties"`
		}

		err := json.Unmarshal([]byte(held.schema), &schema)
		if err != nil {
			t.Fatalf("%s: %v", held.name, err)
		}

		for key, spec := range schema.Properties {
			pattern := cmp.Or(spec.Pattern, spec.Items.Pattern)
			if pattern == "" {
				continue
			}

			pinned++

			shape := regexp.MustCompile(pattern)
			if !shape.MatchString("m-r3t1ab") {
				continue
			}

			if !shape.MatchString("apfs-span-2") && !shape.MatchString("apfs-span-2#k3x") {
				t.Errorf("%s.%s takes an id and should take a name too", held.name, key)
			}
		}
	}

	if pinned == 0 {
		t.Error("the tools should pin some key to an id's shape")
	}
}

func TestResolveNamesTurnsEveryEntryKeyIntoItsID(t *testing.T) {
	mem := &memory{key: "s1"}
	mem.env.Roots.Data = t.TempDir()

	index := newNameIndex(map[string]string{"m-aaaaaa": "apfs-span", "m-bbbbbb": "gate-reads"})

	err := os.MkdirAll(mem.store(), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = mem.saveNames(index)
	if err != nil {
		t.Fatal(err)
	}

	req := request{
		ID: "APFS-Span", IDs: []string{"apfs-span", "gate-reads#k3x", "m-cccccc", "state"}, By: "gate-reads",
		Absorbs: []string{"apfs-span"}, Links: []string{"gate-reads", "20261102-232808-d-tac-fvh"},
		Items: []string{"apfs-span", "read the timer"}, Add: []string{"gate-reads"}, Target: "apfs-span",
	}

	mem.resolveNames(&req, "link")

	switch {
	case req.ID != "m-aaaaaa":
		t.Errorf("a name in any case should resolve to its id, got %q", req.ID)
	case strings.Join(req.IDs, " ") != "m-aaaaaa m-bbbbbb#k3x m-cccccc state":
		t.Errorf("ids and item addresses should resolve by the entry's name, the rest pass through, got %v", req.IDs)
	case req.By != "m-bbbbbb" || req.Absorbs[0] != "m-aaaaaa" || req.Target != "m-aaaaaa":
		t.Errorf("a successor, an absorbed entry and a link target should resolve, got %q %v %q", req.By, req.Absorbs, req.Target)
	case strings.Join(req.Links, " ") != "m-bbbbbb 20261102-232808-d-tac-fvh":
		t.Errorf("a link should resolve where it names an entry, a graph id pass through, got %v", req.Links)
	case strings.Join(req.Items, "|") != "m-aaaaaa|read the timer" || req.Add[0] != "m-bbbbbb":
		t.Errorf("an item naming an entry should link it, other text pass through, got %v %v", req.Items, req.Add)
	}

	bound := request{Target: "apfs-span"}
	mem.resolveNames(&bound, "bind")

	if bound.Target != "apfs-span" {
		t.Error("a target that names a store should never resolve as an entry")
	}
}
