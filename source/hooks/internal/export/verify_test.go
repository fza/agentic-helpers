package export_test

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/export"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const storeID = "01a17249-ce50-7449-89c0-3bb864c20079-app"

var attachment = []byte("notes kept with the entry\n")

func attachmentSum() string {
	sum := sha256.Sum256(attachment)

	return hex.EncodeToString(sum[:])
}

// wholeArchive is the members of a small archive Verify accepts.
func wholeArchive() []test.Member {
	sum := attachmentSum()

	return []test.Member{
		{Name: "manifest.json", Data: []byte(`{"format":"agent-memory-export","version":1,"exported":"2026-11-07T14:02:11Z",` +
			`"producer":"agentic-helpers test","stores":[{"id":"` + storeID + `","name":"app","counts":{"entries":1,"seats":1}}],` +
			`"attachments":{"files":1,"bytes":26}}`)},
		{Name: "stores/" + storeID + "/identity.json", Data: []byte(`{"id":"` + storeID + `","created":"2026-03-02T09:14:55Z","checkouts":[]}`)},
		{Name: "stores/" + storeID + "/seats.jsonl", Data: []byte(`{"id":"s-4f8k2p","name":"main"}` + "\n")},
		{Name: "stores/" + storeID + "/entries.jsonl", Data: []byte(`{"id":"m-k3x9q2","attachments":[{"sha256":"` + sum +
			`","stored":true}],"events":[]}` + "\n")},
		{Name: "stores/" + storeID + "/slices/s-4f8k2p.txt", Data: []byte("session x\n")},
		{Name: "stores/" + storeID + "/tools/log-scan/meta.json", Data: []byte(`{"name":"log-scan","current":1}`)},
		{Name: "stores/" + storeID + "/tools/log-scan/v1/tool.json", Data: []byte(`{"name":"log-scan"}`)},
		{Name: "stores/" + storeID + "/tools/log-scan/v1/lib/scan.sh", Data: []byte("echo\n")},
		{Name: "attachments/" + sum, Data: attachment},
	}
}

func replaced(members []test.Member, name string, data string) []test.Member {
	changed := slices.Clone(members)

	for index := range changed {
		if changed[index].Name == name {
			changed[index].Data = []byte(data)
		}
	}

	return changed
}

func without(members []test.Member, name string) []test.Member {
	return slices.DeleteFunc(slices.Clone(members), func(held test.Member) bool { return held.Name == name })
}

func TestVerifyRefusesAnArchiveThatIsNotWhole(t *testing.T) {
	whole := wholeArchive()
	manifest := string(whole[0].Data)
	entries := "stores/" + storeID + "/entries.jsonl"

	for _, tc := range []struct {
		name    string
		members []test.Member
		says    string
	}{
		{"the manifest not first", append(slices.Clone(whole[1:]), whole[0]), "first member"},
		{"another format", replaced(whole, "manifest.json", strings.Replace(manifest, "agent-memory-export", "other", 1)), "names the format"},
		{"a newer version", replaced(whole, "manifest.json", strings.Replace(manifest, `"version":1`, `"version":2`, 1)), "version 2"},
		{"an attachment holding other bytes", replaced(whole, "attachments/"+attachmentSum(), "changed\n"), "does not hold the bytes"},
		{"a count the members do not hold", replaced(whole, "manifest.json", strings.Replace(manifest, `"entries":1`, `"entries":2`, 1)),
			"the manifest counts"},
		{"a member outside the layout", append(slices.Clone(whole), test.Member{Name: "notes.txt"}), "outside the layout"},
		{"a member path climbing out", append(slices.Clone(whole), test.Member{Name: "stores/../x"}), "path the format refuses"},
		{"a member twice", append(slices.Clone(whole), whole[1]), "there twice"},
		{"a store the manifest does not list", append(slices.Clone(whole),
			test.Member{Name: "stores/01a17249-ce50-7449-89c0-3bb864c20079-other/identity.json"}), "does not list"},
		{"a line that is no JSON object", replaced(whole, entries, "[1]\n"), "no JSON object"},
		{"a last line without its newline", replaced(whole, entries, `{"id":"m-k3x9q2"}`), "does not end its last line"},
		{"an entry naming a stored attachment the archive lacks", without(whole, "attachments/"+attachmentSum()),
			"does not hold it"},
		{"attachment totals the members do not hold", replaced(whole, "manifest.json",
			strings.Replace(manifest, `"bytes":26`, `"bytes":27`, 1)), "the manifest counts 1 of 27"},
		{"a file the layout does not know inside a store", append(slices.Clone(whole),
			test.Member{Name: "stores/" + storeID + "/notes.txt"}), "outside the layout"},
		{"a store without its identity", without(whole, "stores/"+storeID+"/identity.json"), "has no identity.json"},
		{"an identity naming another store", replaced(whole, "stores/"+storeID+"/identity.json", `{"id":"other"}`), "names no store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "archive.tar.gz")
			test.WriteArchive(t, path, tc.members)

			_, err := export.Verify(path)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the archive should be refused, naming why: %v", err)
			}
		})
	}
}

func TestVerifyReadsAWholeArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	test.WriteArchive(t, path, wholeArchive())

	manifest, err := export.Verify(path)
	if err != nil || len(manifest.Stores) != 1 {
		t.Errorf("a whole archive should read back: %+v %v", manifest, err)
	}
}

func TestVerifyRefusesAStreamCutShort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	test.WriteArchive(t, path, wholeArchive())

	whole := test.ReadFile(t, path)
	test.WriteFile(t, path, whole[:len(whole)-4])

	_, err := export.Verify(path)
	if err == nil {
		t.Errorf("an archive cut short should be refused")
	}
}
