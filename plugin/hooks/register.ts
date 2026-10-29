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

// Claude Code hands the context fill to the status line and to mods, never to a
// command hook, so the reading agentic-memory's nudge ladder reads is written
// here, after every main-thread turn. Path and format are the ones
// `internal/memory/hooks.go` reads, `at` in RFC 3339 UTC as every time the store
// keeps; a machine without a memory store gets nothing written.
export const register: Register = (on) => {
  on('session.measure', async ($, measurement, next) => {
    const percent = measurement.context.percent
    const home = await $.env.get('HOME')
    if (percent !== undefined && home) {
      const memoryRoot = `${home}/.claude/agentic-memory`
      if (await $.fs.exists(memoryRoot)) {
        const reading = {
          used_percentage: percent,
          size: measurement.context.window,
          compact_window: await compactionWindow($),
          at: new Date(await $.clock.now()).toISOString(),
        }
        await $.fs.write(`${memoryRoot}/sessions/${await $.session.id()}.ctx`, `${JSON.stringify(reading)}\n`)
      }
    }

    return next(measurement)
  })
}
