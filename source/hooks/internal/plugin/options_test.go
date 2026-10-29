package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

func userConfigKeys(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}

	var manifest struct {
		UserConfig map[string]json.RawMessage `json:"userConfig"`
	}

	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}

	keys := make([]string, 0, len(manifest.UserConfig))
	for key := range manifest.UserConfig {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func TestTheReadmeListsEveryOption(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(repository, "README.md"))
	if err != nil {
		t.Fatalf("reading the README: %v", err)
	}

	_, after, found := strings.Cut(string(readme), "\n## Options\n")
	section, _, _ := strings.Cut(after, "\n## ")

	if !found {
		t.Fatal("the README should carry an Options section")
	}

	for _, key := range pluginconfig.Keys {
		if !strings.Contains(section, "`"+key+"`") {
			t.Errorf("the README's Options section should name the option %s", key)
		}
	}

	if !strings.Contains(string(readme), "\n## Dependencies\n") {
		t.Error("the README should carry a Dependencies section")
	}
}

func TestEveryDeclaredOptionIsReadAndNoConfigOnlyOneIsDeclared(t *testing.T) {
	read := slices.DeleteFunc(slices.Clone(pluginconfig.Keys), func(key string) bool {
		return slices.Contains(pluginconfig.ConfigOnly, key)
	})
	slices.Sort(read)

	if !slices.Equal(read, userConfigKeys(t)) {
		t.Errorf("the plugin should declare every option it reads but those config.yaml alone sets, read %v", read)
	}
}

// A declared default reaches every hook as an answer, hiding config.yaml.
func TestNoDeclaredOptionCarriesADefault(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}

	var manifest struct {
		UserConfig map[string]map[string]json.RawMessage `json:"userConfig"`
	}

	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}

	for key, declaration := range manifest.UserConfig {
		_, declared := declaration["default"]
		if declared {
			t.Errorf("an option should name its default in its description and declare none: %s", key)
		}
	}
}
