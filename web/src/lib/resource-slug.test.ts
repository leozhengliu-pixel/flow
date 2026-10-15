import { describe, expect, it } from 'vitest'
import { findBySlugRef, findLoopByRef, slugSuffix } from './resource-slug'
import { createIssueShortcutContext } from './create-issue-shortcut'
import { parseAppRoute } from './app-routes'
import type { BootstrapData } from '@/types/flow'

const projects = [
  { id: 'a', slugId: 'same-name-aaaaaaaaaaaa', previousSlugIds: ['same-name'] },
  { id: 'b', slugId: 'same-name-bbbbbbbbbbbb', previousSlugIds: ['same-name'] },
  { id: 'c', slugId: 'renamed-cccccccccccc', previousSlugIds: ['original-cccccccccccc'] },
]

describe('resource slugs', () => {
  it('reads the stable 12-hex suffix', () => {
    expect(slugSuffix('compare-test-566151d57d0a')).toBe('566151d57d0a')
    expect(slugSuffix('566151d57d0a')).toBe('566151d57d0a')
    expect(slugSuffix('qa-spike-resp-export-revamp')).toBeUndefined()
    expect(slugSuffix('imported-a1b2c3')).toBeUndefined()
  })

  it('resolves ids, current, stale-prefix, bare-suffix and legacy slugs', () => {
    expect(findBySlugRef(projects, 'a')?.id).toBe('a')
    expect(findBySlugRef(projects, 'same-name-bbbbbbbbbbbb')?.id).toBe('b')
    expect(findBySlugRef(projects, 'old-name-bbbbbbbbbbbb')?.id).toBe('b')
    expect(findBySlugRef(projects, 'bbbbbbbbbbbb')?.id).toBe('b')
    expect(findBySlugRef(projects, 'original-cccccccccccc')?.id).toBe('c')
    // A legacy slug two records shared opens the first, as it did before.
    expect(findBySlugRef(projects, 'same-name')?.id).toBe('a')
    expect(findBySlugRef(projects, 'missing-dddddddddddd')).toBeUndefined()
    expect(findBySlugRef(projects, '')).toBeUndefined()
  })

  it('finds the project of a pre-rename URL for route consumers', () => {
    const route = parseAppRoute('/acme/project/before-rename-cccccccccccc/overview')
    const data = { projects: [{ ...projects[2], teamIds: ['team-1'] }] } as unknown as BootstrapData
    expect(createIssueShortcutContext(route, data)).toMatchObject({ projectId: 'c' })
  })

  it('finds a loop by its id, its slug, or a slug with a stale name prefix', () => {
    const loops = [{ id: 'loop_1', slugId: 'triage-agent-96b66416bc6a' }, { id: 'loop_2', slugId: 'weekly-wrap-0123456789ab' }, { id: 'loop_3' }]
    expect(findLoopByRef(loops, 'loop_2')?.id).toBe('loop_2')
    expect(findLoopByRef(loops, 'triage-agent-96b66416bc6a')?.id).toBe('loop_1')
    expect(findLoopByRef(loops, 'renamed-96b66416bc6a')?.id).toBe('loop_1')
    expect(findLoopByRef(loops, 'loop_3')?.id).toBe('loop_3')
    expect(findLoopByRef(loops, 'missing-ffffffffffff')).toBeUndefined()
    expect(findLoopByRef(loops, undefined)).toBeUndefined()
  })
})
