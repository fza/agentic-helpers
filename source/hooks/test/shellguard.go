package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GuardTree / Project / Tmp / Home / Outside are the directories a shell-guard
// test judges against. Project carries sub/ and link, a symlink to Outside,
// and .tmp/ where scratch is set.
type GuardTree struct {
	Project string
	Tmp     string
	Home    string
	Outside string
}

func NewGuardTree(t *testing.T, scratch bool) GuardTree {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the test root: %v", err)
	}

	tree := GuardTree{
		Project: filepath.Join(root, "home", "project"),
		Tmp:     filepath.Join(root, "tmp"),
		Home:    filepath.Join(root, "home"),
		Outside: filepath.Join(root, "outside"),
	}

	directories := []string{filepath.Join(tree.Project, "sub"), tree.Tmp, tree.Outside}
	if scratch {
		directories = append(directories, filepath.Join(tree.Project, ".tmp"))
	}

	for _, directory := range directories {
		err = os.MkdirAll(directory, 0o755)
		if err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
	}

	err = os.Symlink(tree.Outside, filepath.Join(tree.Project, "link"))
	if err != nil {
		t.Fatalf("linking outside: %v", err)
	}

	return tree
}

// ProviderConfig is a settings file holding a token on one line and a private
// key over several, built from the credential samples: the token's and the
// key's lines are what a redaction withholds, every other line what it keeps.
func ProviderConfig(t *testing.T) (string, string, string) {
	t.Helper()

	token := SecretFixture(t, "positive/github-pat.txt")
	key := SecretFixture(t, "positive/private-key.txt")
	content := strings.NewReplacer("{token}", token, "{key}", key).Replace(Fixture(t, "shellguard/provider-config.yaml"))

	return content, token, key
}

// PostToolUse is the payload Claude Code hands a PostToolUse hook.
func PostToolUse(t *testing.T, cwd string, tool string, input any, response any) []byte {
	t.Helper()

	encoded, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "cwd": cwd, "tool_name": tool, "tool_input": input, "tool_response": response,
	})
	if err != nil {
		t.Fatalf("encoding the tool call: %v", err)
	}

	return encoded
}
