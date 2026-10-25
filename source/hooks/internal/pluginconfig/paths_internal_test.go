package pluginconfig

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestPathsTakeTheConfigDirAndTheDropIns(t *testing.T) {
	managed := t.TempDir()
	test.WriteFile(t, filepath.Join(managed, "managed-settings.d", "20-b.json"), "{}")
	test.WriteFile(t, filepath.Join(managed, "managed-settings.d", "10-a.json"), "{}")
	test.WriteFile(t, filepath.Join(managed, "managed-settings.d", "notes.txt"), "")

	got := pathsUnder("/config", managed)
	want := []string{
		"/config/settings.json", filepath.Join(managed, "managed-settings.json"),
		filepath.Join(managed, "managed-settings.d", "10-a.json"), filepath.Join(managed, "managed-settings.d", "20-b.json"),
	}

	if !slices.Equal(got, want) {
		t.Errorf("the paths should be the user's settings, the managed ones, then the drop-ins by name, got %v", got)
	}

	configured := Paths("/home/u", "linux", func(name string) string {
		return map[string]string{"CLAUDE_CONFIG_DIR": "/elsewhere"}[name]
	})
	if configured[0] != "/elsewhere/settings.json" {
		t.Errorf("CLAUDE_CONFIG_DIR should hold the user's settings, got %v", configured)
	}
}

func TestADropInAndTheHookEnvironmentWin(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(root, "settings.json")
	dropIn := filepath.Join(root, "managed-settings.d", "50.json")
	test.WriteFile(t, user, `{"pluginConfigs":{"`+PluginID+`":{"options":{"memory_enabled":true,"tools_enabled":true}}}}`)
	test.WriteFile(t, dropIn, `{"pluginConfigs":{"`+PluginID+`":{"options":{"memory_enabled":false}}}}`)

	options, err := ReadWith([]string{user, dropIn}, func(name string) string {
		return map[string]string{"CLAUDE_PLUGIN_OPTION_TOOLS_ENABLED": "false", "CLAUDE_PLUGIN_OPTION_ENTRY_MAX_WORDS": "300"}[name]
	})
	if err != nil {
		t.Fatal(err)
	}

	if options.MemoryEnabled || options.ToolsEnabled || options.EntryMaxWords != 300 || !options.ScratchEnabled {
		t.Errorf("the hook environment should win over the user's settings, a drop-in over both, got %+v", options)
	}

	locked, err := ReadWith([]string{user, dropIn}, func(name string) string {
		return map[string]string{"CLAUDE_PLUGIN_OPTION_MEMORY_ENABLED": "true"}[name]
	})
	if err != nil || locked.MemoryEnabled {
		t.Errorf("no hook environment should override what the managed settings set, got %+v %v", locked, err)
	}

	_, err = ReadWith(nil, func(name string) string {
		return map[string]string{"CLAUDE_PLUGIN_OPTION_SHELL_GUARD_FIND_DELETE": "sometimes"}[name]
	})
	if err == nil {
		t.Error("a hook environment value of the wrong kind should be an error")
	}
}
