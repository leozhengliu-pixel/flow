import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import { createWorkspaceStore } from './workspace-store'

describe('scale-p13 collection indexes', () => {
  it('reuses unaffected maps while isolating workspace, viewer, and epoch changes', () => {
    const data = makeBootstrap()
    const first = createWorkspaceStore(data)
    const next = first.applyBootstrap({ ...data, issues: [makeIssue({ title: 'Changed' })] })
    expect(next.entities.issue).not.toBe(first.entities.issue)
    for (const kind of Object.keys(first.entities).filter(kind => kind !== 'issue')) expect(next.entities[kind as keyof typeof next.entities]).toBe(first.entities[kind as keyof typeof first.entities])
    const switched = next.applyBootstrap({ ...data, viewer: { ...data.viewer, id: 'other' } })
    expect(switched.entities.project).not.toBe(next.entities.project)
    const cleared = next.applyBootstrap(null)
    expect(cleared.entities.issue.size).toBe(0)
    expect(first.getById('issue', 'issue-1')).toMatchObject({ title: 'Test issue' })
  })

  it('does not call a paged or omitted legacy collection complete', () => {
    const store = createWorkspaceStore(makeBootstrap({ issueCollectionPaged: true, resourceDetailsOmitted: true }))
    expect(store.collectionComplete('issue')).toBe(false)
    expect(store.collectionComplete('document')).toBe(false)
    expect(store.collectionComplete('user')).toBe(true)
  })

  it('measures a single collection update with 10,000 unrelated projects', () => {
    const data = makeBootstrap({ projects: Array.from({ length: 10_000 }, (_, index) => ({ ...project, id: `project-${index}` })) })
    let store = createWorkspaceStore(data)
    let rebuilt = 0
    const started = performance.now()
    for (let index = 0; index < 100; index++) {
      const previous = store
      store = store.applyBootstrap({ ...data, issues: [makeIssue({ title: `Update ${index}` })] })
      rebuilt += Object.keys(store.entities).filter(key => store.entities[key as keyof typeof store.entities] !== previous.entities[key as keyof typeof store.entities]).length
    }
    console.info(JSON.stringify({ measurement: 'scale-p13-index', updates: 100, unrelatedProjects: 10_000, rebuilt, elapsedMs: performance.now() - started }))
    expect(store.getById('issue', 'issue-1')).toMatchObject({ title: 'Update 99' })
  })
})
