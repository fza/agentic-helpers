package space_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestLowLineNamesOnlyAStoreUnderATenthLeft(t *testing.T) {
	const limit = 1000 << 20

	cases := []struct {
		name        string
		attachments int64
		tools       int64
		want        string
	}{
		{name: "both roomy", attachments: 100 << 20, tools: 100 << 20},
		{name: "exactly a tenth left is not low", attachments: 900 << 20, tools: 0},
		{name: "attachments low", attachments: 950 << 20, want: "space left: attachments 50.0 MB of 1000.0 MB"},
		{name: "tools low", tools: 990 << 20, want: "space left: tools 10.0 MB of 1000.0 MB"},
		{
			name: "both low", attachments: 950 << 20, tools: 2000 << 20,
			want: "space left: attachments 50.0 MB of 1000.0 MB, tools 0 B of 1000.0 MB",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := space.LowLine(space.Figure{Kind: space.Attachments, Used: tc.attachments, Limit: limit},
				space.Figure{Kind: space.Tools, Used: tc.tools, Limit: limit})
			if got != tc.want {
				t.Errorf("the line should name the stores under a tenth of their limit, got %q", got)
			}
		})
	}
}

func TestLineSizesInTheLargestFittingUnit(t *testing.T) {
	got := space.Line(space.Figure{Kind: space.Attachments, Used: 512, Limit: 2048 << 20})
	if got != "space left: attachments 2.0 GB of 2.0 GB" {
		t.Errorf("a limit of gigabytes should read in gigabytes, got %q", got)
	}
}

func TestUpdateKeepsEveryRecordAnotherWriterLeft(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger", ".space.json")

	for _, key := range []string{"first", "second"} {
		err := space.Update(ledger, func(records map[string]space.Record) {
			records[key] = space.Record{Stored: 10, Held: 4}
		})
		if err != nil {
			t.Fatalf("updating the ledger: %v", err)
		}
	}

	if total := space.Total(space.Read(ledger)); total.Stored != 20 || total.Held != 8 {
		t.Errorf("each update should keep the records before it, got %+v", total)
	}
}

func TestReadIgnoresAnUnusableLedger(t *testing.T) {
	dir := t.TempDir()

	for name, content := range map[string]string{
		"garbled.json":  "{not json",
		"negative.json": `{"x":{"stored":-5,"held":-5}}`,
	} {
		path := filepath.Join(dir, name)
		test.WriteFile(t, path, content)

		if records := space.Read(path); len(records) != 0 {
			t.Errorf("a %s ledger should read as empty, got %v", name, records)
		}
	}

	if records := space.Read(filepath.Join(dir, "missing.json")); len(records) != 0 {
		t.Errorf("a missing ledger should read as empty, got %v", records)
	}

	target := filepath.Join(dir, "target.json")
	test.WriteFile(t, target, `{"x":{"stored":5,"held":5}}`)

	link := filepath.Join(dir, "link.json")
	if os.Symlink(target, link) != nil {
		t.Fatal("the link should be created")
	}

	if records := space.Read(link); len(records) != 0 {
		t.Errorf("a ledger that is a link should read as empty, got %v", records)
	}
}
