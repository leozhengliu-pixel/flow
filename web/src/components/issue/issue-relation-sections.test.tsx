import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { ActivityEvent, Issue, IssueRelation } from '@/types/flow'
import { issueRelationEntries } from './issue-relation-model'
import { IssueRelationSections } from './issue-relation-sections'

const deleteRelation = vi.hoisted(() => vi.fn(async () => undefined))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), deleteRelation }))

const relation = (id: string, type: IssueRelation['type'], issueId: string, relatedIssueId: string): IssueRelation => ({ id, type, issueId, relatedIssueId })

const blocker = makeIssue({ id: 'blocker', identifier: 'TST-2', title: 'Blocker issue' })
const blocked = makeIssue({ id: 'blocked', identifier: 'TST-3', title: 'Blocked issue' })
const relatedOld = makeIssue({ id: 'related-old', identifier: 'TST-4', title: 'Older related issue' })
const relatedNew = makeIssue({ id: 'related-new', identifier: 'TST-5', title: 'Newer related issue' })
const duplicate = makeIssue({ id: 'duplicate', identifier: 'TST-6', title: 'Duplicate report' })
const main = makeIssue({
  id: 'main', identifier: 'TST-1', title: 'Main issue',
  relations: [
    relation('r-related-old', 'related', 'main', 'related-old'),
    relation('r-dup', 'duplicate', 'main', 'duplicate'),
    relation('r-blocking', 'blocks', 'main', 'blocked'),
    relation('r-blocked-by', 'blocked_by', 'main', 'blocker'),
    relation('r-related-new', 'related', 'main', 'related-new'),
  ],
})

function renderSections(issue: Issue = main, options: { activities?: ActivityEvent[]; issues?: Issue[] } = {}) {
  const data = makeBootstrap({ issues: options.issues ?? [issue, blocker, blocked, relatedOld, relatedNew, duplicate] })
  const onOpenIssue = vi.fn()
  const onDeleteRelation = vi.fn(async () => undefined)
  const view = render(<I18nProvider><TooltipProvider><aside className="issue-properties"><IssueRelationSections issue={issue} data={data} activities={options.activities} onOpenIssue={onOpenIssue} onDeleteRelation={onDeleteRelation}/></aside></TooltipProvider></I18nProvider>)
  return { ...view, onOpenIssue, onDeleteRelation }
}

const sectionTitles = () => [...document.querySelectorAll('.issue-relation-section h3')].map(node => node.textContent)
const rowTitles = (title: string) => within(screen.getByRole('region', { name: title })).getAllByRole('link').map(link => link.textContent)

describe('IssueRelationSections', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    deleteRelation.mockClear()
  })

  it('renders nothing when the issue has no relations', () => {
    const lonely = makeIssue({ id: 'lonely', relations: [] })
    renderSections(lonely, { issues: [lonely] })
    expect(document.querySelector('.issue-relation-section')).toBeNull()
  })

  it('shows only non-empty sections, in the reference app order, newest relation first', () => {
    renderSections()
    expect(sectionTitles()).toEqual(['Blocked by', 'Blocking', 'Related', 'Duplicates'])
    expect(rowTitles('Related')).toEqual(['Newer related issue', 'Older related issue'])
    expect(rowTitles('Blocked by')).toEqual(['Blocker issue'])
    expect(screen.getByRole('link', { name: 'Blocked issue' })).toHaveAttribute('href', '/workspace/issue/TST-3')
  })

  it('lists a duplicate under Duplicate of on the issue that was marked as the duplicate', () => {
    const activities = [{ id: 'a1', type: 'issue.relation_added', createdAt: '2026-08-01T00:00:00.000Z', actor: main.creator, metadata: { type: 'duplicate', relatedIssueId: 'duplicate' } }] as ActivityEvent[]
    renderSections(main, { activities })
    expect(sectionTitles()).toEqual(['Blocked by', 'Blocking', 'Related', 'Duplicate of'])
  })

  it('includes relations recorded only on the other issue, inverted', () => {
    const source = makeIssue({ id: 'source', identifier: 'TST-9', title: 'Inbound blocker', relations: [relation('r-in', 'blocks', 'source', 'main')] })
    const target = makeIssue({ id: 'main', identifier: 'TST-1', relations: [] })
    expect(issueRelationEntries(target, [target, source])).toEqual([{ id: 'r-in', kind: 'blockedBy', targetId: 'source', ownerId: 'source' }])
  })

  it('removes a relation from the row X with no confirmation and hides it immediately', async () => {
    const { onDeleteRelation } = renderSections()
    const remove = screen.getByRole('button', { name: 'Remove blocking issue TST-2' })
    await act(async () => { fireEvent.click(remove) })
    expect(onDeleteRelation).toHaveBeenCalledWith('r-blocked-by')
    expect(screen.queryByRole('region', { name: 'Blocked by' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Remove related issue TST-5' })).toBeInTheDocument()
  })

  it('removes an inbound-only relation through the issue that owns it', async () => {
    const source = makeIssue({ id: 'source', identifier: 'TST-9', title: 'Inbound blocker', relations: [relation('r-in', 'blocks', 'source', 'main')] })
    const target = makeIssue({ id: 'main', identifier: 'TST-1', relations: [] })
    const { onDeleteRelation } = renderSections(target, { issues: [target, source] })
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Remove blocking issue TST-9' })) })
    expect(deleteRelation).toHaveBeenCalledWith('source', 'r-in')
    expect(onDeleteRelation).not.toHaveBeenCalled()
  })

  it('opens the related issue on a plain click and leaves modified clicks to the browser', () => {
    const { onOpenIssue } = renderSections()
    const link = screen.getByRole('link', { name: 'Blocker issue' })
    fireEvent.click(link)
    expect(onOpenIssue).toHaveBeenCalledWith(expect.objectContaining({ id: 'blocker' }))
    onOpenIssue.mockClear()
    fireEvent.click(link, { metaKey: true })
    expect(onOpenIssue).not.toHaveBeenCalled()
  })
})
