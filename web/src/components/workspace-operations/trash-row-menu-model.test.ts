import { describe, expect, it } from 'vitest'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { TrashEntry } from '@/types/flow'
import { trashCopyItems, trashRestoreLabel } from './trash-row-menu-model'

const entry = (patch: Partial<TrashEntry>): TrashEntry => ({ id: 'trash', resourceType: 'issue', resourceId: 'issue-1', title: 'ENG-1 Fix login', payload: {}, deletedBy: viewer, deletedAt: '2026-10-01T00:00:00Z', expiresAt: '2026-10-31T00:00:00Z', ...patch } as TrashEntry)

describe('trash row menu', () => {
  it("lists the reference app's Copy items for a deleted issue", () => {
    const data = makeBootstrap()
    const issue = data.issues[0]
    const items = trashCopyItems(entry({ resourceId: issue.id, title: `${issue.identifier} ${issue.title}`, payload: { issue, comments: [], activities: [] } }), data)
    expect(items.map(item => item.label)).toEqual(['Copy ID', 'Copy URL', 'Copy title', 'Copy title as link', 'Copy issue as Markdown', 'Copy everything as Markdown', 'Copy git branch name'])
    expect(items[0].value()).toBe(issue.identifier)
    expect(items[1].value()).toContain(`/issue/${issue.identifier}`)
  })

  it('names the restore item', () => {
    expect(['issue', 'project', 'initiative', 'document'].map(trashRestoreLabel)).toEqual(['Restore', 'Restore project', 'Restore initiative', 'Restore document'])
  })
})
