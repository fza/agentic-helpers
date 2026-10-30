package space_test

import (
	"context"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func listed(t *testing.T, dir string) map[string]space.LedgerFile {
	t.Helper()

	files := map[string]space.LedgerFile{}
	for _, file := range space.ReadLedger(dir) {
		files[file.Name] = file
	}

	return files
}

func TestARefreshListsEveryDataFileWithItsLengthAndChecksum(t *testing.T) {
	dir := t.TempDir()
	data := map[string]string{
		"index.json":           `{"tools":{}}`,
		"word-count/v1/wc.py":  "print(len(input().split()))\n",
		"word-count/takes.jnl": "AGMJNL\x00\x01",
		".lock":                "4242\n",
		"log/.lock":            "",
		"log/.tmp-123":         "half a segment",
		"word-count/v1/x.lock": "",
	}

	for name, content := range data {
		test.WriteFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}

	err := space.Refresh(context.Background(), dir, dir, func(name string) space.Account {
		if name == "word-count/v1/wc.py" {
			return space.Account{Counted: true, Held: true, Bytes: 7}
		}

		return space.Account{Bytes: -1}
	})
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}

	files := listed(t, dir)

	for _, name := range []string{"index.json", "word-count/v1/wc.py", "word-count/takes.jnl"} {
		file, found := files[name]
		if !found || file.Length != int64(len(data[name])) ||
			file.CRC != crc32.Checksum([]byte(data[name]), crc32.MakeTable(crc32.Castagnoli)) {
			t.Errorf("the ledger should list %s with its length and checksum", name)
		}
	}

	if len(files) != 3 {
		t.Errorf("lock files, temp files and the ledger itself should stay out, got %d files", len(files))
	}

	if total := space.LedgerTotal(space.ReadLedger(dir)); total.Stored != 7 || total.Held != 7 {
		t.Errorf("the total should count the bytes the component accounts, got %+v", total)
	}
}

func TestARefreshCarriesWhatTheLedgerCountedBefore(t *testing.T) {
	dir := t.TempDir()
	test.WriteFile(t, filepath.Join(dir, "a1b2.gz"), "compressed")

	err := space.Refresh(context.Background(), dir, dir, func(string) space.Account {
		return space.Account{Counted: true, Held: true, Bytes: 600}
	})
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}

	test.WriteFile(t, filepath.Join(dir, "planted"), strings.Repeat("y", 512))

	err = space.Refresh(context.Background(), dir, dir, func(string) space.Account { return space.Account{Carry: true} })
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}

	files := listed(t, dir)
	if file := files["a1b2.gz"]; !file.Counted || file.Bytes != 600 {
		t.Error("a file nobody accounts for afresh should keep what the ledger counted for it")
	}

	if file := files["planted"]; file.Counted || file.Held || file.Length != 512 {
		t.Error("a file the ledger never listed should be listed with its length and count nothing")
	}
}

func TestConcurrentRefreshesLeaveEveryWritersFile(t *testing.T) {
	dir := t.TempDir()

	var group sync.WaitGroup

	for writer := range 8 {
		group.Go(func() {
			test.WriteFile(t, filepath.Join(dir, fmt.Sprintf("file-%d", writer)), fmt.Sprint(writer))

			err := space.Refresh(context.Background(), dir, dir, nil)
			if err != nil {
				t.Errorf("refreshing: %v", err)
			}
		})
	}

	group.Wait()

	if len(listed(t, dir)) != 8 {
		t.Error("the ledger the last writer leaves should list every writer's file")
	}
}

func TestReadLedgerIgnoresAnUnusableLedger(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, dir string)
	}{
		{name: "missing", plant: func(*testing.T, string) {}},
		{name: "not a journal", plant: func(t *testing.T, dir string) {
			test.WriteFile(t, filepath.Join(dir, space.LedgerName), `{"x":{"stored":5,"held":5}}`)
		}},
		{name: "a link", plant: func(t *testing.T, dir string) {
			other := t.TempDir()
			test.WriteFile(t, filepath.Join(other, "data"), "x")

			err := space.Refresh(context.Background(), other, other, nil)
			if err != nil {
				t.Fatalf("refreshing: %v", err)
			}

			if os.Symlink(filepath.Join(other, space.LedgerName), filepath.Join(dir, space.LedgerName)) != nil {
				t.Fatal("the link should be created")
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.plant(t, dir)

			if files := space.ReadLedger(dir); len(files) != 0 {
				t.Errorf("an unusable ledger should read as empty, got %v", files)
			}
		})
	}
}
