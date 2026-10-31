package memory

import "testing"

func TestLinkOutcomeKeepsOnlyWhatBecameOfTheTarget(t *testing.T) {
	cases := []struct {
		name string
		flag string
		want string
	}{
		{"an open target", "→ d-a", ""},
		{"a closed target", "→ d-a closed by s-b", "closed by s-b"},
		{"a gone target", "→ m-a gone", "gone"},
		{"a superseded target, its successor open", "→ d-a → d-b", "superseded by d-b"},
		{"a chain ending settled", "→ d-a → d-b → d-c settled", "superseded by d-b, superseded by d-c, settled"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkOutcome(tc.flag); got != tc.want {
				t.Errorf("the outcome should drop the target and spell each later hop as a supersede, got %q", got)
			}
		})
	}
}
