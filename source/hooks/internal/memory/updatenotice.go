package memory

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/updates"
)

// updateNotice is what a claim says of a newer release. Auto-update may be
// turned on in the managed and user settings the options come from, or in
// the project settings Claude Code reads at the checkout's top level;
// `config.yaml`, among the option files, parses as no settings and adds
// nothing.
func (mem *memory) updateNotice(ctx context.Context) []string {
	if mem.env.Releases == nil || !mem.options().UpdateNotice {
		return nil
	}

	root := mem.checkout(ctx).root
	settings := append(slices.Clone(mem.env.Settings),
		filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json"))

	check := updates.Check{
		Client: mem.env.Releases, Data: mem.env.Roots.Data, Running: mem.env.Version, Settings: settings,
		Known: updates.KnownMarketplaces(pluginconfig.ConfigDir(mem.env.Home, mem.env.Getenv)), Now: mem.env.Now,
	}

	return check.Notice(ctx)
}
