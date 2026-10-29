package pieces_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestDumpPrintsADataFileAsJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "takes.jnl")
	test.WriteFile(t, path, string(memorylog.EncodeJournal(
		memorylog.Record{Kind: memorylog.RecordTake, Schema: 1, Body: []byte(`{"v":1,"by":"s1"}`)})))

	cases := []struct {
		name string
		args []string
		code int
		out  string
		err  string
	}{
		{name: "a journal", args: []string{"dump", path}, out: `{"record":"take","schema":1,"body":{"v":1,"by":"s1"}}` + "\n"},
		{name: "no file named", args: []string{"dump"}, code: 1, err: "usage: agentic-helpers dump <file>"},
		{name: "a file it cannot read", args: []string{"dump", path + ".gone"}, code: 1, err: "agentic-helpers: opening "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured test.Captured

			code := pieces.Main(context.Background(), tc.args, pieces.Env{}, captured.Streams(""))
			if code != tc.code || captured.Out.String() != tc.out || !strings.Contains(captured.Err.String(), tc.err) {
				t.Errorf("dump should print the file or say why it cannot, got %d %q %q", code, captured.Out.String(),
					captured.Err.String())
			}
		})
	}
}
