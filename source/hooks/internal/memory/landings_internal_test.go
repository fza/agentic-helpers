package memory

import (
	"context"
	"strings"
	"testing"
)

func TestLandingsFoldAMergesSide(t *testing.T) {
	base := landedCommit{sha: "base", parents: []string{"root"}}
	side := landedCommit{sha: "side", parents: []string{"base"}}
	first := landedCommit{sha: "first", parents: []string{"base"}}
	merge := landedCommit{sha: "merge", parents: []string{"first", "side"}}

	mem := &memory{}

	landings := mem.landings(context.Background(), "", []landedCommit{base, side, first, merge}, false)

	shas := make([]string, 0, len(landings))
	for _, landed := range landings {
		shas = append(shas, landed.commit.sha)
	}

	if strings.Join(shas, " ") != "merge base first" {
		t.Fatalf("a merge's side should fold into the merge, every other commit standing alone: %v", shas)
	}

	if len(landings[0].evidence) != 2 || landings[0].evidence[1].sha != "side" {
		t.Errorf("the merge should carry the commits it brings in as evidence: %+v", landings[0].evidence)
	}
}
