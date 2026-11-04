package memory

import (
	"fmt"
	"testing"
)

func TestTermScoresWeighARareWordOverCommonOnes(t *testing.T) {
	target := &entry{id: "m-target", title: "Tail seal", body: "the seal writes the manifest by rename"}
	live := []*entry{target}

	for _, word := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		live = append(live, &entry{id: "m-" + word, title: "Note on " + word,
			body: fmt.Sprintf("the %s and the build for the run with the end of the day", word)})
	}

	scores := termScores(live, "what is the and for with of manifest")

	for _, held := range live[1:] {
		if scores[held.id] >= scores[target.id] {
			t.Errorf("an entry holding the prompt's common words should score below the one holding its rare word: %v", scores)

			break
		}
	}

	everywhere := termScores(live, "the")
	for _, held := range live {
		if everywhere[held.id] != 0 {
			t.Errorf("a word every entry holds should weigh nothing: %v", everywhere)

			break
		}
	}
}
