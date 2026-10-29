import type { EngineInterface, Register } from 'claude-code'

// `compact_window` is the window Claude Code compacts at, as it settled it from
// the environment, the --autocompact flag, settings, account or experiment;
// the `summary` breakdown that carries it is estimated locally, with no
// request. A Claude Code without the call leaves the key out.
async function compactionWindow($: EngineInterface): Promise<number | undefined> {
  try {
    const usage = await $.session.usage({ breakdown: 'summary' })

    return usage.context.breakdown?.rawMaxTokens
  } catch {
    return undefined
  }
}

// The agent-neutral home's data root, as internal/home locates it:
// AGENTIC_HELPERS_HOME, else $XDG_DATA_HOME/agentic-helpers, else
// ~/.local/share/agentic-helpers; a relative value is ignored.
async function dataRoot($: EngineInterface): Promise<string | undefined> {
  const own = await $.env.get('AGENTIC_HELPERS_HOME')
  if (own?.startsWith('/')) {
    return own
  }

  const xdg = await $.env.get('XDG_DATA_HOME')
  if (xdg?.startsWith('/')) {
    return `${xdg}/agentic-helpers`
  }

  const home = await $.env.get('HOME')

  return home ? `${home}/.local/share/agentic-helpers` : undefined
}

// Claude Code hands the context fill to the status line and to mods, never to a
// command hook, so the reading agentic-memory's nudge ladder reads is written
// here, after every main-thread turn, into the session's folder of the home,
// `<data>/sessions/claude/<session>/context.json`. Path and format are the ones
// `internal/memory/hooks.go` reads, `at` in RFC 3339 UTC as every time the store
// keeps; a machine whose home holds no store gets nothing written.
export const register: Register = (on) => {
  on('session.measure', async ($, measurement, next) => {
    const percent = measurement.context.percent
    const data = await dataRoot($)
    if (percent !== undefined && data) {
      if (await $.fs.exists(`${data}/stores`)) {
        const reading = {
          used_percentage: percent,
          size: measurement.context.window,
          compact_window: await compactionWindow($),
          at: new Date(await $.clock.now()).toISOString(),
        }
        await $.fs.write(`${data}/sessions/claude/${await $.session.id()}/context.json`, `${JSON.stringify(reading)}\n`)
      }
    }

    return next(measurement)
  })
}
