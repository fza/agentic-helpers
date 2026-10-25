package memory

import "testing"

func TestWordsCountUnspacedScriptsByCharacter(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{name: "Latin", text: "Lieferschein export fails", want: 3},
		{name: "Japanese", text: "納品書の出力が失敗", want: 9},
		{name: "Chinese", text: "交货单导出失败", want: 7},
		{name: "Thai", text: "ส่งออกล้มเหลว", want: 13},
		{name: "mixed", text: "Fix 納品書 export", want: 5},
		{name: "a Latin run touching Han", text: "API納品書", want: 4},
		{name: "nothing", text: "  ", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := words(tc.text); got != tc.want {
				t.Errorf("each unspaced character and each other run should count once, got %d", got)
			}
		})
	}
}

func TestWordSetsOfUnspacedTextOverlap(t *testing.T) {
	first := wordSet("納品書の出力が失敗する")
	second := wordSet("納品書の出力を直す")

	if jaccard(first, second) <= 0 {
		t.Error("two Japanese entries on one subject should share words")
	}

	if !wordSet("the lock export")["lock"] || wordSet("an ox")["ox"] {
		t.Error("spaced scripts should keep the three-letter minimum")
	}
}
