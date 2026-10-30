package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// Every verb scans what it writes before embedding; writeVersion scans every
// version again, so a write path added later cannot skip the check.
func TestEveryVersionWrittenIsScanned(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")

	cases := []struct {
		name  string
		next  version
		where string
	}{
		{name: "a file", next: version{name: "leak", number: 1, files: []safefs.Walked{{Name: "leak.sh", Data: []byte(credential)}}}, where: "leak.sh"},
		{name: "a description field", next: version{name: "leak", number: 1, spec: spec{Usage: credential}}, where: "--usage"},
		{name: "the history line", next: version{name: "leak", number: 1, why: credential}, where: "--why"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			lib := &library{env: Env{Home: home, Base: home, Roots: test.Roots(home)}}

			err := lib.writeVersion(context.Background(), tc.next)

			if err == nil || !strings.HasPrefix(err.Error(), "agentic-tools: "+tc.where+" looks like a credential (") {
				t.Errorf("a version holding a credential should be refused before the store is touched, got %v", err)
			}

			if test.Exists(t, test.Roots(home).Data) {
				t.Error("a refused version should leave no store behind")
			}
		})
	}
}
