package pieces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
)

// MovedLine is the SessionStart line in a project carrying an sdd graph while
// the plugin now holding the graph hooks is not installed: the grounding gate
// and agentic-capture ship as sdd-helpers' sdd-grounding-gate and sdd-capture.
const MovedLine = "the grounding gate and agentic-capture moved to the sdd-helpers plugin: " +
	"claude plugin marketplace add fza/sdd-helpers, then claude plugin install sdd-helpers@sdd-helpers"

// movedPluginPrefix opens the key Claude Code lists sdd-helpers under in
// `installed_plugins.json`, whichever marketplace it came from.
const movedPluginPrefix = "sdd-helpers@"

// InstalledPlugins is the file Claude Code lists every installed plugin in,
// under its configuration folder.
func InstalledPlugins(configDir string) string {
	return filepath.Join(configDir, "plugins", "installed_plugins.json")
}

// movedHooksMissing is whether dir carries an sdd graph while installed lists
// no sdd-helpers plugin. A missing or unreadable file counts as no plugin
// installed: the line then names a step the user can check.
func movedHooksMissing(dir string, installed string) bool {
	info, err := os.Stat(filepath.Join(dir, graphconfig.GraphDirName))
	if err != nil || !info.IsDir() {
		return false
	}

	return !holdsMovedPlugin(installed)
}

func holdsMovedPlugin(installed string) bool {
	data, err := os.ReadFile(installed)
	if err != nil {
		return false
	}

	var listing struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}

	err = json.Unmarshal(data, &listing)
	if err != nil {
		return false
	}

	for key := range listing.Plugins {
		if strings.HasPrefix(key, movedPluginPrefix) {
			return true
		}
	}

	return false
}
