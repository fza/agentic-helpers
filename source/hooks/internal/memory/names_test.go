package memory_test

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var filedName = regexp.MustCompile(`^filed (m-[a-z0-9]{4,16}) \(([a-z0-9-]+)\)`)

// fileNamed files an entry and answers its id and the name the reply gives it.
func (h *harness) fileNamed(title string, extra ...string) (string, string) {
	h.t.Helper()

	out := h.must("body", append([]string{"new", "--title", title, "--topic", "gate"}, extra...)...)

	match := filedName.FindStringSubmatch(out)
	if match == nil {
		h.t.Fatalf("filing should name the new entry's id and name: %s", out)
	}

	return match[1], match[2]
}

// names is the store's name index as the file holds it.
func (h *harness) names() map[string]string {
	h.t.Helper()

	raw, err := os.ReadFile(filepath.Join(h.store(), "names.json"))
	if err != nil {
		h.t.Fatalf("the store should hold a name index: %v", err)
	}

	var names map[string]string

	err = json.Unmarshal(raw, &names)
	if err != nil {
		h.t.Fatalf("the name index should be JSON: %v", err)
	}

	return names
}

// label is how a line names the entry id: its id, then its name.
func (h *harness) label(id string) string {
	h.t.Helper()

	name := h.names()[id]
	if name == "" {
		h.t.Fatalf("the index should name %s", id)
	}

	return id + " (" + name + ")"
}

func TestEveryFiledEntryGetsAUniqueName(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	first, firstName := h.fileNamed("APFS span of clones")
	second, secondName := h.fileNamed("APFS span again")

	if firstName != "apfs-span" || secondName != "apfs-span-2" {
		t.Errorf("each entry should be named from its title, a clash numbered apart, got %s and %s", firstName, secondName)
	}

	if names := h.names(); names[first] != firstName || names[second] != secondName {
		t.Errorf("the index should hold the name each filing gave, got %v", names)
	}

	if listed := h.must("", "search", "--status", "live"); !strings.Contains(listed, first+" (apfs-span) APFS span of clones") {
		t.Errorf("a listing should name each entry beside its id: %s", listed)
	}
}

func TestMergeCarriesNamesNumberingAClashApart(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	mine, _ := h.fileNamed("Gate reads Bash only")
	into := h.storeID()

	other := otherProject(h)
	other.draws = rand.Reader
	other.must("", "claim", "main")
	theirs, theirName := other.fileNamed("Gate reads MCP too")
	from := other.storeID()

	if theirName != "gate-reads" {
		t.Fatalf("each store should name its entry on its own, got %s", theirName)
	}

	joined := h.atUserShell("merge", from, into)
	if joined.code != 0 {
		t.Fatalf("merge should join the stores: %s%s", joined.out, joined.err)
	}

	for name, id := range map[string]string{"gate-reads": mine, "gate-reads-2": theirs} {
		if shown := h.must("", "show", name, "--again"); !strings.HasPrefix(shown, id+" ("+name+")") {
			t.Errorf("the joined store should keep its own names and number the joining ones apart (%s): %s", name, shown)
		}
	}
}

func TestATitleHoldingACredentialNamesNothing(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]

	h := newHarness(t)
	h.must("", "claim", "main")
	h.fileNamed("APFS span of clones")

	got := h.run("clean", "new", "--title", token+" leaked", "--topic", "gate")
	if got.code == 0 {
		t.Fatalf("a title holding a credential should be refused: %s", got.out)
	}

	for _, name := range h.names() {
		if strings.HasPrefix(name, "ghp-") || strings.Contains(name, strings.ToLower(token[4:12])) {
			t.Errorf("the index should never hold a name made of a credential: %s", name)
		}
	}
}

func TestAFilingTheLogRefusesGivesItsNameBack(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.fileNamed("Gate reads Bash only")

	tail := filepath.Join(h.store(), "log", memorylog.TailName)

	err := os.Chmod(tail, 0o400)
	if err != nil {
		t.Fatal(err)
	}

	got := h.run("body", "new", "--title", "APFS span of clones", "--topic", "gate")

	err = os.Chmod(tail, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got.code == 0 || !strings.Contains(got.err, "writing to the log") {
		t.Fatalf("a log taking no write should refuse the filing at the write: %s%s", got.out, got.err)
	}

	for _, name := range h.names() {
		if name == "apfs-span" {
			t.Error("a refused filing should give its name back")
		}
	}
}

func TestARetitledEntryKeepsItsName(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	id, name := h.fileNamed("APFS span of clones")
	h.must("", "retitle", name, "Clone sizes on APFS")

	shown := h.must("", "show", name)
	if !strings.HasPrefix(shown, id+" ("+name+") Clone sizes on APFS") {
		t.Errorf("a retitled entry should keep its name and answer to it: %s", shown)
	}
}

func TestANameWorksWhereverAnIDDoes(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	_, gate := h.fileNamed("Gate reads Bash only")
	todo, plan := h.fileNamed("Port the waiter", "--kind", "todo", "--item", "read the timer", "--item", gate)
	later, laterName := h.fileNamed("Gate reads MCP too")

	shown := h.must("", "show", plan, "--again")
	item := regexp.MustCompile(`#([a-z0-9]{3}): read the timer`).FindStringSubmatch(shown)

	switch {
	case item == nil:
		t.Fatalf("the todo should list its text item: %s", shown)
	case !strings.Contains(shown, "Gate reads Bash only"):
		t.Errorf("an item naming an entry should link it: %s", shown)
	}

	h.must("", "tag", plan, "waiter")
	h.must("", "link", plan, laterName)
	h.must("", "ack", plan+"#"+item[1], "--done")

	shown = h.must("", "show", todo, "--again", "--all-items")
	for _, want := range []string{"topics: gate, waiter", "→ " + later} {
		if !strings.Contains(shown, want) {
			t.Errorf("tag and link by name should change the entry (%s): %s", want, shown)
		}
	}

	h.must("", "supersede", gate, "--by", laterName)

	if status := h.must("", "show", gate, "--again"); !strings.Contains(status, "superseded") {
		t.Errorf("supersede should take names for the entry and its successor: %s", status)
	}

	answer := h.mcp(`{"name":"retire","arguments":{"ids":["` + laterName + `"],"because":"done"}}`)
	if message := mcpMessage(t, answer); !strings.Contains(message, "retired") {
		t.Errorf("an MCP id key should take a name: %s", answer)
	}

	object := decoded(t, mcpText(t, h.mcp(`{"name":"show","arguments":{"ids":["`+plan+`"],"again":true}}`)))

	entries, _ := object["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["name"] != plan {
		t.Errorf("show over MCP should take a name and answer it beside the id: %v", object)
	}
}
