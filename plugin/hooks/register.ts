import type { Register } from 'claude-code'

// Claude Code hands the context fill to the status line and to mods, never to a
// command hook, so the reading agentic-memory's nudge ladder reads is written
// here, after every main-thread turn. Path and format are the ones
// `internal/memory/hooks.go` reads; a machine without a memory store gets
// nothing written.
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
          at: Math.floor((await $.clock.now()) / 1000),
        }
        await $.fs.write(`${memoryRoot}/sessions/${await $.session.id()}.ctx`, `${JSON.stringify(reading)}\n`)
      }
    }

    return next(measurement)
  })
}
