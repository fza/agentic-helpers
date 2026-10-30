package memory_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var withheldNotice = regexp.MustCompile(`(?m)^agentic-memory: this prompt looks like it holds a credential \(([a-z0-9-]+(?:, [a-z0-9-]+)*)\); it was not sent to the embedding endpoint$`)

func TestAPromptHoldingACredentialIsRecalledByTermsWithoutAnEmbed(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]

	cases := []struct {
		name     string
		prompt   string
		withheld bool
	}{
		{name: "a prompt holding a credential", prompt: "how does the tail seal manifest rename work\n" + credential, withheld: true},
		{name: "a clean prompt", prompt: "how does the tail seal manifest rename work", withheld: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")

			target := h.file("Tail seal manifest rename", "the tail seal writes the manifest by rename")
			for _, word := range unrelated[:19] {
				h.file("Unrelated finding about "+word, word+" "+word+" "+word)
			}

			calls := h.embedder.Calls()

			out := h.must(hookPayload("", "", tc.prompt), "prompt")

			notices := withheldNotice.FindAllStringSubmatch(out, -1)
			if !tc.withheld {
				if len(notices) != 0 || h.embedder.Calls() == calls {
					t.Errorf("a clean prompt should be embedded without a notice:\n%s", out)
				}

				return
			}

			if len(notices) != 1 || !strings.Contains(notices[0][1], "github-pat") {
				t.Errorf("the agent should hear once that the prompt was kept from the endpoint, naming the rule:\n%s", out)
			}

			if h.embedder.Calls() != calls {
				t.Error("a prompt holding a credential should never reach the embedding endpoint")
			}

			if !strings.Contains(out, "agentic-memory: related") || !strings.Contains(out, target) {
				t.Errorf("recall should still answer by the prompt's words:\n%s", out)
			}

			if strings.Contains(out, token) {
				t.Error("the notice must never repeat the credential")
			}

			_, held := promptvector.Load(context.Background(), promptvector.Dir(h.roots().Cache), h.session, tc.prompt, h.embedder.Space())
			if held {
				t.Error("a withheld prompt should leave no vector for agentic-tools")
			}
		})
	}
}
