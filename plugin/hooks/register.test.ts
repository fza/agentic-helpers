import { describe, expect, mock, test } from 'claude-code/testing'
import type { On, SessionMeasureInput } from 'claude-code'

const HOME = '/home/dev'
const DATA = `${HOME}/.local/share/agentic-helpers`
const SESSION = 'f9a21b6a-730c-40af-8c51-b8269b705bb5'
const READING = `${DATA}/sessions/claude/${SESSION}/context.json`

type Write = { path: string; text: string }

// Nothing sits beneath the plugin: each hook here stands for the engine, and
// records what the mod asked of it.
type Usage = 'unanswered' | 'throws' | { rawMaxTokens?: number }

function engine(
  on: On,
  options: { home?: string; variables?: Record<string, string>; hasMemoryStore: boolean; usage?: Usage },
) {
  const writes: Write[] = []
  const probed: string[] = []
  const measured: SessionMeasureInput[] = []

  mock.env(on, { ...(options.home === undefined ? {} : { HOME: options.home }), ...options.variables })
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
  const usage = options.usage ?? 'unanswered'
  if (usage === 'throws') {
    on('session.usage', () => {
      throw new Error('no breakdown in this engine')
    })
  } else if (usage !== 'unanswered') {
    on('session.usage', () => ({
      value: {
        startedAt: 0,
        context: {
          window: 1000000,
          breakdown:
            usage.rawMaxTokens === undefined ? undefined : ({ rawMaxTokens: usage.rawMaxTokens } as never),
        },
        rateLimits: [],
        cost: { usd: 0 },
      } as never,
    }))
  }
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

    expect(probed).toEqual([`${DATA}/stores`])
    expect(writes).toEqual([
      {
        path: READING,
        text: '{"used_percentage":12,"size":1000000,"at":"2026-10-25T02:06:31.250Z"}\n',
      },
    ])
    expect(measured).toHaveLength(1)
  })

  test('carries the window Claude Code compacts at', async ($, on) => {
    const { writes, measured } = engine(on, { home: HOME, hasMemoryStore: true, usage: { rawMaxTokens: 400000 } })

    await $.session.measure(measurement(12))

    expect(writes).toEqual([
      {
        path: READING,
        text: '{"used_percentage":12,"size":1000000,"compact_window":400000,"at":"2026-10-25T02:06:31.250Z"}\n',
      },
    ])
    expect(measured).toHaveLength(1)
  })

  const roots: { name: string; variables: Record<string, string>; data: string }[] = [
    { name: 'AGENTIC_HELPERS_HOME', variables: { AGENTIC_HELPERS_HOME: '/srv/agents', XDG_DATA_HOME: '/xdg' }, data: '/srv/agents' },
    { name: 'XDG_DATA_HOME', variables: { XDG_DATA_HOME: '/xdg' }, data: '/xdg/agentic-helpers' },
    { name: 'a relative XDG_DATA_HOME', variables: { XDG_DATA_HOME: 'xdg' }, data: DATA },
  ]
  for (const rootCase of roots) {
    test(`follows ${rootCase.name} to the home`, async ($, on) => {
      const { writes, probed } = engine(on, { home: HOME, variables: rootCase.variables, hasMemoryStore: true })

      await $.session.measure(measurement(12))

      expect(probed).toEqual([`${rootCase.data}/stores`])
      expect(writes.map((write) => write.path)).toEqual([`${rootCase.data}/sessions/claude/${SESSION}/context.json`])
    })
  }

  const windowless: { name: string; usage: Usage }[] = [
    { name: 'the engine answers no usage call', usage: 'unanswered' },
    { name: 'the usage call throws', usage: 'throws' },
    { name: 'the usage answer carries no breakdown', usage: {} },
  ]
  for (const windowlessCase of windowless) {
    test(`leaves the compaction window out where ${windowlessCase.name}`, async ($, on) => {
      const { writes, measured } = engine(on, { home: HOME, hasMemoryStore: true, usage: windowlessCase.usage })

      await $.session.measure(measurement(12))

      expect(writes.map((write) => write.text)).toEqual([
        '{"used_percentage":12,"size":1000000,"at":"2026-10-25T02:06:31.250Z"}\n',
      ])
      expect(measured).toHaveLength(1)
    })
  }

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
