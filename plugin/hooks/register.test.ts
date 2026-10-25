import { describe, expect, mock, test } from 'claude-code/testing'
import type { On, SessionMeasureInput } from 'claude-code'

const HOME = '/home/dev'
const MEMORY_ROOT = `${HOME}/.claude/agentic-memory`
const SESSION = 'f9a21b6a-730c-40af-8c51-b8269b705bb5'

type Write = { path: string; text: string }

// Nothing sits beneath the plugin: each hook here stands for the engine, and
// records what the mod asked of it.
function engine(on: On, options: { home?: string; hasMemoryStore: boolean }) {
  const writes: Write[] = []
  const probed: string[] = []
  const measured: SessionMeasureInput[] = []

  mock.env(on, options.home === undefined ? {} : { HOME: options.home })
  mock.clock(on, { now: 1792893991250 })
  on('session.id', () => ({ value: SESSION }))
  on('fs.exists', (_, e) => {
    probed.push(e.path)

    return { value: options.hasMemoryStore }
  })
  on('fs.write', (_, e) => {
    writes.push({ path: e.path, text: e.text })

    return { value: undefined }
  })
  on('session.measure', (_, e) => {
    measured.push(e)

    return { changed: e.changed }
  })

  return { writes, probed, measured }
}

function measurement(percent?: number): SessionMeasureInput {
  return {
    context: { tokens: percent === undefined ? undefined : percent * 10000, window: 1000000, percent },
    rateLimits: [],
    changed: ['context'],
  }
}

describe('the context reading', () => {
  test('is written for the nudge ladder after a measured turn', async ($, on) => {
    const { writes, probed, measured } = engine(on, { home: HOME, hasMemoryStore: true })

    await $.session.measure(measurement(12))

    expect(probed).toEqual([MEMORY_ROOT])
    expect(writes).toEqual([
      {
        path: `${MEMORY_ROOT}/sessions/${SESSION}.ctx`,
        text: '{"used_percentage":12,"size":1000000,"at":"2026-10-25T02:06:31.250Z"}\n',
      },
    ])
    expect(measured).toHaveLength(1)
  })

  const silent: { name: string; home?: string; hasMemoryStore: boolean; percent?: number }[] = [
    { name: 'before the window reports a fill', home: HOME, hasMemoryStore: true },
    { name: 'on a machine without a memory store', home: HOME, hasMemoryStore: false, percent: 12 },
    { name: 'where no home directory is known', hasMemoryStore: true, percent: 12 },
  ]
  for (const silentCase of silent) {
    test(`is not written ${silentCase.name}`, async ($, on) => {
      const { writes, measured } = engine(on, silentCase)

      await $.session.measure(measurement(silentCase.percent))

      expect(writes).toHaveLength(0)
      expect(measured).toHaveLength(1)
    })
  }
})
