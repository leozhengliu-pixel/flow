import { mkdirSync, writeFileSync } from 'node:fs'
import { serialize } from 'node:v8'
import { expect, it } from 'vitest'
import { ScaleP19ProjectWindow } from './scale-p19-project-window'
import type { Project } from '@/types/flow'

it.skipIf(!process.env.SCALE_P19_MEASURE)('scale-p19 repeatable 23,210-project retention comparison', async () => {
  const rows = Array.from({ length: 23210 }, (_, index) => ({
    id: `project-${index}`, name: `Project ${index}`, slugId: `p-${index}`,
    summary: 'x'.repeat(1024), status: { id: 'started', name: 'Started', type: 'started' },
    teamIds: ['team'], memberIds: [], labelIds: [], initiatives: [], milestones: [],
  })) as unknown as Project[]
  const pairs = []
  for (let pair = 0; pair < 3; pair++) {
    let retained: Project[] = []
    const started = performance.now()
    for (let offset = 0; offset < rows.length; offset += 100) {
      const page = rows.slice(offset, offset + 100)
      retained = [...retained, ...page.filter(item => !retained.some(existing => existing.id === item.id))]
    }
    const before = { rows: retained.length, serializedBytes: serialize(retained).byteLength, utf16JSONBytes: JSON.stringify(retained).length * 2, milliseconds: performance.now() - started }
    retained = []
    const window = new ScaleP19ProjectWindow('workspace', async query => {
      const offset = Number(query.cursor ?? 0), end = Math.min(offset + 100, rows.length)
      return { items: rows.slice(offset, end), total: rows.length, hasMore: end < rows.length, nextCursor: end < rows.length ? String(end) : undefined }
    })
    const snapshots = []
    for (let index = 0; index < 233; index++) {
      await window.read({}, index)
      if ([0, 99, 199, 232].includes(index)) snapshots.push({ page: index + 1, retainedRows: window.retainedProjects, chargedBytes: window.retainedBytes })
    }
    const navigation = []
    for (let index = 0; index < 100; index++) {
      await window.read({}, index % 2 ? 232 : 0)
      if ([19, 49, 99].includes(index)) navigation.push({ navigation: index + 1, retainedRows: window.retainedProjects, chargedBytes: window.retainedBytes })
    }
    expect(window.retainedBytes).toBeLessThanOrEqual(8 * 1024 * 1024)
    expect(window.retainedProjects).toBeLessThanOrEqual(400)
    expect(navigation[2].chargedBytes).toBe(navigation[0].chargedBytes)
    pairs.push({ before, after: { snapshots, navigation } })
    window.dispose()
  }
  mkdirSync('/tmp/codex-scale-p19', { recursive: true })
  writeFileSync('/tmp/codex-scale-p19/after.json', JSON.stringify({ method: 'Three interleaved legacy-append/window pairs on the same 23,210 summaries; encoded retention charges, not retained heap or server throughput', pairs }, null, 2))
  console.log(JSON.stringify(pairs))
}, 120_000)
