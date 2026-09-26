package graphconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestGraphDir(t *testing.T) {
	root := graphconfig.RealPath(t.TempDir())
	below := filepath.Join(root, "source", "internal")

	err := os.MkdirAll(below, 0o755)
	if err != nil {
		t.Fatalf("creating the tree: %v", err)
	}

	if graphconfig.GraphDir(below) != "" {
		t.Error("a tree without a graph should have none")
	}

	err = os.Mkdir(filepath.Join(root, ".sdd"), 0o755)
	if err != nil {
		t.Fatalf("creating the graph: %v", err)
	}

	if graphconfig.GraphDir(below) != filepath.Join(root, ".sdd") {
		t.Error("the graph above should govern the directory below")
	}
}

func TestLoad(t *testing.T) {
	defaults := graphconfig.Default()

	cases := []struct {
		name    string
		content string
		want    graphconfig.Config
		fails   bool
	}{
		{name: "no file", want: defaults},
		{name: "prefix and lint", content: test.Fixture(t, "grounding/full.yaml"), want: graphconfig.Config{ListingPrefix: "area-", DraftLint: "vale --output=line", ShowDepth: defaults.ShowDepth}},
		{name: "empty file", content: "", want: defaults},
		{name: "not yaml", content: "listing_prefix: [unclosed", fails: true},
		{name: "show depth", content: test.Fixture(t, "grounding/show-depth.yaml"), want: graphconfig.Config{ListingPrefix: "area-", ShowDepth: graphconfig.ShowDepth{Down: 3, Up: 2, ExemptSeats: []string{"reviewer", "scout"}}}},
		{name: "one direction", content: test.Fixture(t, "grounding/show-depth-down.yaml"), want: graphconfig.Config{ShowDepth: graphconfig.ShowDepth{Down: 3, Up: 1}}},
		{name: "no depth owed", content: test.Fixture(t, "grounding/show-depth-none.yaml"), want: graphconfig.Config{}},
		{name: "exempt seats alone", content: test.Fixture(t, "grounding/show-depth-exempt.yaml"), want: graphconfig.Config{ShowDepth: graphconfig.ShowDepth{Down: 2, Up: 1, ExemptSeats: []string{"reviewer"}}}},
		{name: "empty block", content: test.Fixture(t, "grounding/show-depth-empty.yaml"), want: defaults},
		{name: "negative depth", content: test.Fixture(t, "grounding/show-depth-negative.yaml"), fails: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			graph := filepath.Join(t.TempDir(), ".sdd")
			if tc.name != "no file" {
				test.WriteFile(t, filepath.Join(graph, "grounding.yaml"), tc.content)
			}

			got, err := graphconfig.Load(graph)
			if (err != nil) != tc.fails {
				t.Fatalf("only an unparsable file or a negative depth should fail, got: %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the config should be read as written, defaults filling what it leaves out, got: %+v", got)
			}
		})
	}

	t.Run("a negative depth names itself", func(t *testing.T) {
		graph := filepath.Join(t.TempDir(), ".sdd")
		test.WriteFile(t, filepath.Join(graph, "grounding.yaml"), test.Fixture(t, "grounding/show-depth-negative.yaml"))

		_, err := graphconfig.Load(graph)
		if !errors.Is(err, graphconfig.ErrNegativeDepth) {
			t.Errorf("the error should say a depth went below 0, got: %v", err)
		}
	})
}
