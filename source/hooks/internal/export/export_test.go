package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (m *machine) run(args ...string) (int, string, string) {
	m.t.Helper()

	var captured test.Captured

	code := export.Main(context.Background(), args, m.env(), captured.Streams(""))

	return code, captured.Out.String(), captured.Err.String()
}

func member(t *testing.T, members []test.Member, name string) []byte {
	t.Helper()

	for _, held := range members {
		if held.Name == name {
			return held.Data
		}
	}

	t.Fatalf("the archive should hold %s", name)

	return nil
}

func lines(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	var records []map[string]any

	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var record map[string]any

		err := json.Unmarshal([]byte(line), &record)
		if err != nil {
			t.Fatalf("every line should be a JSON object: %q: %v", line, err)
		}

		records = append(records, record)
	}

	return records
}

func byID(t *testing.T, records []map[string]any, id string) map[string]any {
	t.Helper()

	for _, record := range records {
		if record["id"] == id {
			return record
		}
	}

	t.Fatalf("the records should hold %s", id)

	return nil
}

func TestExportWritesEveryStoreAndReadsBackWhole(t *testing.T) {
	m := newMachine(t)
	held := m.populate()
	before := test.TreeState(t, m.roots().Data)
	cacheBefore := test.TreeState(t, m.roots().Cache)
	target := filepath.Join(t.TempDir(), "machine.tar.gz")

	code, out, errOut := m.run(target)
	if code != 0 {
		t.Fatalf("export should succeed: %s%s", out, errOut)
	}

	if !maps.Equal(before, test.TreeState(t, m.roots().Data)) || !maps.Equal(cacheBefore, test.TreeState(t, m.roots().Cache)) {
		t.Errorf("an export should change nothing in the data root or the cache")
	}

	if !strings.Contains(out, target) || !strings.Contains(out, "github.com/acme/other, unclaimed") {
		t.Errorf("the summary should name the archive and each store, the unclaimed one marked: %s", out)
	}

	manifest, err := export.Verify(target)
	if err != nil {
		t.Fatalf("the archive should read back whole: %v", err)
	}

	appID, otherID := filepath.Base(held.appStore), filepath.Base(held.otherStore)
	if len(manifest.Stores) != 2 || manifest.Stores[0].ID != min(appID, otherID) {
		t.Fatalf("the manifest should list both stores in ascending id order: %+v", manifest.Stores)
	}

	for _, store := range manifest.Stores {
		if store.Unclaimed != (store.ID == otherID) {
			t.Errorf("only the store waiting under unclaimed/ should be flagged unclaimed: %+v", store)
		}
	}

	members := test.ArchiveMembers(t, target)
	checkOrder(t, members)

	prefix := "stores/" + appID + "/"
	entries := lines(t, member(t, members, prefix+"entries.jsonl"))

	note := byID(t, entries, held.note)
	if note["status"] != "live" || note["body"] != "The Bash path records reads." || note["anchors"] == nil {
		t.Errorf("a live entry should carry its body and anchors: %v", note)
	}

	links, _ := note["links"].([]any)
	kinds := map[string]bool{}

	for _, link := range links {
		kinds[link.(map[string]any)["kind"].(string)+"/"+link.(map[string]any)["via"].(string)] = true
	}

	if !kinds["graph/explicit"] || !kinds["memory/explicit"] {
		t.Errorf("the entry's graph link and memory link should both be exported: %v", links)
	}

	events, _ := note["events"].([]any)
	filing := events[0].(map[string]any)

	if filing["kind"] != "new" || filing["data"].(map[string]any)["body"] != nil {
		t.Errorf("the events should open with the filing, which carries no body: %v", filing)
	}

	todoFiling := byID(t, entries, held.todo)["events"].([]any)[0].(map[string]any)["data"].(map[string]any)
	if _, kept := todoFiling["anchors"]; kept {
		t.Errorf("a filing should leave out a field the store left null: %v", todoFiling)
	}

	if byID(t, entries, held.retired)["status"] != "retired" || byID(t, entries, held.retired)["status_reason"] != "settled" {
		t.Errorf("a retired entry should keep its status and reason")
	}

	if byID(t, entries, held.first)["superseded_by"] != held.second || byID(t, entries, held.second)["superseded_by"] != held.third ||
		byID(t, entries, held.third)["status"] != "live" {
		t.Errorf("the supersede chain should run from the first entry through the second to the live third")
	}

	items := map[string]string{}
	for _, item := range byID(t, entries, held.todo)["items"].([]any) {
		items[item.(map[string]any)["id"].(string)] = item.(map[string]any)["state"].(string)
	}

	if items[held.doneItem] != "done" || items[held.dropped] != "dropped" || len(items) != 3 {
		t.Errorf("a todo's items should keep their states, open ones included: %v", items)
	}

	attachments := note["attachments"].([]any)
	sha := attachments[0].(map[string]any)["sha256"].(string)

	original, err := os.ReadFile(held.attached)
	if err != nil {
		t.Fatalf("reading the attached file: %v", err)
	}

	if attachments[0].(map[string]any)["stored"] != true || !bytes.Equal(member(t, members, "attachments/"+sha), original) {
		t.Errorf("an attachment should travel as its original bytes, named by their hash")
	}

	if string(member(t, members, prefix+"slices/"+note["seat"].(string)+".txt")) != held.slice {
		t.Errorf("a seat's slice should travel as the bytes the store holds")
	}

	seats := lines(t, member(t, members, prefix+"seats.jsonl"))
	if seats[0]["name"] != "main" || seats[0]["last_claim"] == nil {
		t.Errorf("the seat should travel with its name and last claim: %v", seats)
	}

	if lines(t, member(t, members, prefix+"shelves.jsonl"))[0]["name"] != "main" {
		t.Errorf("the seat's shelf should travel")
	}

	state := lines(t, member(t, members, prefix+"state.jsonl"))
	if state[0]["record"] != "line" || state[0]["text"] != "gate fix half done: the MCP path is next" {
		t.Errorf("the seat's state line should travel: %v", state)
	}

	var meta map[string]any

	_ = json.Unmarshal(member(t, members, prefix+"tools/log-scan/meta.json"), &meta)
	if meta["current"] != float64(3) || len(lines(t, member(t, members, prefix+"tools/log-scan/history.jsonl"))) != 3 {
		t.Errorf("the tool should travel with its current version and its history: %v", meta)
	}

	if string(member(t, members, prefix+"tools/log-scan/v1/scan.sh")) != "#!/bin/sh\necho scan\n" {
		t.Errorf("every stored version should travel byte for byte")
	}

	_ = json.Unmarshal(member(t, members, prefix+"tools/old-scan/meta.json"), &meta)
	if meta["merged_into"] != "log-scan" {
		t.Errorf("a merged tool should name the tool it went into: %v", meta)
	}

	if len(lines(t, member(t, members, "stores/"+otherID+"/entries.jsonl"))) != 1 {
		t.Errorf("the unclaimed store's entries should travel too")
	}
}

// checkOrder holds the archive to the member order the format sets.
func checkOrder(t *testing.T, members []test.Member) {
	t.Helper()

	if members[0].Name != "manifest.json" {
		t.Fatalf("manifest.json should come first, not %s", members[0].Name)
	}

	rank := func(name string) string {
		if strings.HasPrefix(name, "attachments/") {
			return "~" + name
		}

		id, file, _ := strings.Cut(strings.TrimPrefix(name, "stores/"), "/")

		for index, fixed := range []string{"identity.json", "seats.jsonl", "shelves.jsonl", "entries.jsonl", "names.jsonl",
			"topic-merges.jsonl", "state.jsonl"} {
			if file == fixed {
				return fmt.Sprintf("%s/%d", id, index)
			}
		}

		if strings.HasPrefix(file, "slices/") {
			return id + "/7" + file
		}

		tool, rest, _ := strings.Cut(strings.TrimPrefix(file, "tools/"), "/")

		for index, fixed := range []string{"meta.json", "history.jsonl", "takes.jsonl", "pruned.jsonl"} {
			if rest == fixed {
				return fmt.Sprintf("%s/8%s/%d", id, tool, index)
			}
		}

		version, path, _ := strings.Cut(strings.TrimPrefix(rest, "v"), "/")
		number, _ := strconv.Atoi(version)
		first := "1"

		if path == "tool.json" {
			first = "0"
		}

		return fmt.Sprintf("%s/8%s/4%06d/%s%s", id, tool, number, first, path)
	}

	names := make([]string, 0, len(members)-1)
	for _, held := range members[1:] {
		names = append(names, rank(held.Name))
	}

	if !slices.IsSorted(names) {
		t.Errorf("members should follow the store, file and attachment order the format sets: %v", names)
	}
}

func TestExportMarksAnAttachmentTheMachineLostAsNotStored(t *testing.T) {
	m := newMachine(t)
	held := m.populate()

	stored, err := filepath.Glob(filepath.Join(m.roots().Shared("attachments"), "[0-9a-f]*"))
	if err != nil || len(stored) != 1 {
		t.Fatalf("the fixture should store one attachment: %v %v", stored, err)
	}

	err = os.Remove(stored[0])
	if err != nil {
		t.Fatalf("removing the stored attachment: %v", err)
	}

	target := filepath.Join(t.TempDir(), "machine.tar.gz")

	code, out, errOut := m.run(target)
	if code != 0 || !strings.Contains(out, "not stored on this machine") || !strings.Contains(out, "notes.txt") {
		t.Fatalf("an attachment without bytes should be reported, not refuse the export: %d %s%s", code, out, errOut)
	}

	members := test.ArchiveMembers(t, target)
	note := byID(t, lines(t, member(t, members, "stores/"+filepath.Base(held.appStore)+"/entries.jsonl")), held.note)

	if note["attachments"].([]any)[0].(map[string]any)["stored"] != false {
		t.Errorf("the attachment record should say the archive holds no bytes for it")
	}

	for _, held := range members {
		if strings.HasPrefix(held.Name, "attachments/") {
			t.Errorf("no attachment member should be written for bytes the machine lost: %s", held.Name)
		}
	}
}

func TestExportOfAnEmptyHomeIsAValidArchiveWithNoStores(t *testing.T) {
	m := newMachine(t)
	target := filepath.Join(t.TempDir(), "empty.tar.gz")

	code, out, errOut := m.run(target)
	if code != 0 || !strings.Contains(out, "no stores on this machine") {
		t.Fatalf("an empty home should export: %d %s%s", code, out, errOut)
	}

	manifest, err := export.Verify(target)
	if err != nil || len(manifest.Stores) != 0 || manifest.Stores == nil {
		t.Errorf("the archive should read back as one listing no store: %+v %v", manifest, err)
	}

	_, err = os.Lstat(m.roots().Data)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an export should create no data root")
	}
}

func TestExportRefusesAnExistingTargetUnlessForced(t *testing.T) {
	m := newMachine(t)
	target := filepath.Join(t.TempDir(), "kept.tar.gz")
	test.WriteFile(t, target, "the user's own file")

	code, _, errOut := m.run(target)
	if code != 1 || !strings.Contains(errOut, "--force replaces it") {
		t.Errorf("an existing target should be refused, naming --force: %d %s", code, errOut)
	}

	kept, _ := os.ReadFile(target)
	if string(kept) != "the user's own file" {
		t.Errorf("a refused export should leave the target as it was")
	}

	code, out, errOut := m.run("--force", target)
	if code != 0 {
		t.Fatalf("--force should replace the target: %s%s", out, errOut)
	}

	_, err := export.Verify(target)
	if err != nil {
		t.Errorf("the replaced target should be the archive: %v", err)
	}

	listed, _ := os.ReadDir(filepath.Dir(target))
	if len(listed) != 1 {
		t.Errorf("no temporary file should stay beside the target: %v", listed)
	}
}

func TestExportRefusesAStoreItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(t *testing.T, m *machine, held populated)
		says   string
	}{
		{"memory of a newer format", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(held.appStore, home.Memory, "format.json"), `{"format": 99}`)
		}, "memory format 99"},
		{"memory of a format no release wrote", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(held.otherStore, home.Memory, "format.json"), `{"format": 0}`)
		}, "reading the memory format"},
		{"memory whose format cannot be read", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(held.otherStore, home.Memory, "format.json"), `{"format": "eleven"}`)
		}, "reading the memory format"},
		{"a tool library of a newer format", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(held.appStore, home.Tools, "format.json"), `{"format": 9}`)
		}, "has format 9"},
		{"a home of a newer format", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, m.roots().FormatFile(), `{"format": 9}`)
		}, "newer format"},
		{"a tool folder without its current version", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(held.appStore, home.Tools, "log-scan", "current"), "7\n")
		}, "a tool folder this release cannot read"},
		{"a tool folder whose current names no version", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(held.appStore, home.Tools, "log-scan", "current"), "latest\n")
		}, "current names no version"},
		{"a tool version without its tool.json", func(t *testing.T, m *machine, held populated) {
			err := os.Remove(filepath.Join(held.appStore, home.Tools, "log-scan", "v1", "tool.json"))
			if err != nil {
				t.Fatalf("removing the version's tool.json: %v", err)
			}
		}, "v1 has no tool.json"},
		{"an old layout waiting for its migration", func(t *testing.T, m *machine, held populated) {
			test.WriteFile(t, filepath.Join(m.userHome, ".claude", "agentic-memory", "keep"), "old")

			err := os.Remove(m.roots().FormatFile())
			if err != nil {
				t.Fatalf("taking the home back to before its migration: %v", err)
			}
		}, "waits for its migration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			held := m.populate()
			tc.break_(t, m, held)

			dir := t.TempDir()
			target := filepath.Join(dir, "machine.tar.gz")

			code, out, errOut := m.run(target)
			if code != 1 || !strings.Contains(errOut, tc.says) || !strings.Contains(errOut, "nothing written") {
				t.Errorf("the export should refuse, naming why: %d %s%s", code, out, errOut)
			}

			listed, _ := os.ReadDir(dir)
			if len(listed) != 0 {
				t.Errorf("a refused export should write nothing beside the target: %v", listed)
			}
		})
	}
}

func TestExportRefusesAMisspeltCall(t *testing.T) {
	for _, args := range [][]string{{}, {"--forc", "x.tar.gz"}, {"one.tar.gz", "two.tar.gz"}} {
		m := newMachine(t)

		code, _, errOut := m.run(args...)
		if code != 1 || !strings.HasPrefix(errOut, "usage: agentic-helpers export") {
			t.Errorf("%v should print the usage and exit 1: %d %s", args, code, errOut)
		}
	}
}
