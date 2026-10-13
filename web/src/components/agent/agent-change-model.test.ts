import { describe, expect, it } from 'vitest'
import { translateToChinese } from '@/i18n/translate'
import type { AgentMessagePart } from '@/types/flow'
import { agentChangeCards, agentChangeEntityKeys, agentChangeErrorMessage, agentChangeHeader, agentChangeSkippedNote, agentChangeSummary, changedBlockTexts, diffMarkdownBlocks, markdownPlainText } from './agent-change-model'

const part = (id: string, name: string, change: NonNullable<AgentMessagePart['toolCall']>['change'], status: 'completed' | 'error' = 'completed'): AgentMessagePart => ({
  id, type: 'toolCall', status, toolCall: { id: `call-${id}`, name, status, change },
})
const identity = (source: string) => source

describe('agent result cards', () => {
  it('maps successful writes to cards and skips reads, failures and loops', () => {
    const cards = agentChangeCards({ parts: [
      { id: 'text', type: 'text', text: 'Done' },
      part('p1', 'save_issue', { action: 'created', entityType: 'issue', entityId: 'issue-9', label: 'DEV-50', undoable: true }),
      part('p2', 'list_issues', undefined),
      part('p3', 'save_project', { action: 'updated', entityType: 'project', entityId: 'project-1', label: 'Export revamp', fields: ['description'], before: { description: 'Old' }, after: { description: 'Old\n\nRisks' }, undoable: true }),
      part('p4', 'save_view', { action: 'created', entityType: 'view', entityId: 'view-1', label: 'Mine', undoable: true }, 'error'),
      part('p5', 'save_loop', { action: 'created', entityType: 'loop', entityId: 'loop-1', label: 'Digest', undoable: false }),
    ] })
    expect(cards.map(card => [card.action, card.entityType, card.kind])).toEqual([['created', 'issue', 'issue'], ['updated', 'project', 'project']])
    expect(agentChangeHeader(cards[0], identity)).toBe('Created DEV-50')
    expect(agentChangeHeader(cards[1], identity)).toBe('Updated project')
    expect(cards[1].text).toEqual({ field: 'description', before: 'Old', after: 'Old\n\nRisks' })
    expect(agentChangeSummary(cards[1], identity)).toBe('Changed the description.')
  })

  it('marks an undone create that was deleted permanently (customers) as final, with no Restore', () => {
    const [created, deleted] = agentChangeCards({ parts: [
      part('a', 'save_customer', { action: 'created', entityType: 'customer', entityId: 'c1', label: 'Acme', undoable: false, state: 'undone', note: 'Customer "Acme" was deleted permanently and can\'t be restored.' }),
      part('b', 'delete_customer', { action: 'deleted', entityType: 'customer', entityId: 'c2', label: 'Beta', undoable: false }),
    ] })
    expect(created).toMatchObject({ undone: true, undoable: false, finalNote: expect.stringContaining('permanently') })
    expect(deleted).toMatchObject({ action: 'deleted', undoable: false, finalNote: undefined })
  })

  it('degrades unknown write tools to a generic card', () => {
    const [card] = agentChangeCards({ parts: [part('p1', 'save_widget', { action: 'updated', entityType: 'widget', entityId: 'w1', label: 'Gizmo', undoable: false })] })
    expect(card.kind).toBeUndefined()
    expect(agentChangeHeader(card, identity)).toBe('Updated item')
  })

  it('folds edits of one entity into one card and keeps a created entity’s later edits in its Created card', () => {
    const cards = agentChangeCards({ parts: [
      part('a', 'save_document', { action: 'created', entityType: 'document', entityId: 'd1', label: 'Plan', undoable: true }),
      part('b', 'save_document', { action: 'updated', entityType: 'document', entityId: 'd1', label: 'Plan v2', fields: ['content'], before: { content: 'x' }, after: { content: 'y' }, undoable: true }),
      part('c', 'save_project', { action: 'updated', entityType: 'project', entityId: 'p1', label: 'P', fields: ['description'], before: { description: 'one' }, after: { description: 'two' }, undoable: true, state: 'undone' }),
      part('d', 'save_project', { action: 'updated', entityType: 'project', entityId: 'p1', label: 'P', fields: ['summary', 'description'], before: { description: 'two', summary: '' }, after: { description: 'three', summary: 's' }, undoable: true }),
    ] })
    expect(cards).toHaveLength(2)
    expect(cards[0]).toMatchObject({ action: 'created', label: 'Plan v2', partIds: ['a'] })
    expect(cards[1]).toMatchObject({ partIds: ['c', 'd'], fields: ['description', 'summary'], undone: false, text: { before: 'one', after: 'three' } })
    expect(agentChangeEntityKeys({ parts: [part('a', 'save_document', { action: 'created', entityType: 'document', entityId: 'd1', undoable: true })] })).toEqual(['document:d1'])
  })

  it('summarises fields in the viewer’s language', () => {
    expect(agentChangeSummary({ fields: ['description', 'state', 'priority'] }, identity)).toBe('Changed the description, status and priority.')
    expect(agentChangeSummary({ fields: ['description', 'state'] }, translateToChinese, 'zh-CN')).toBe('更改了描述、状态。')
    expect(agentChangeSkippedNote(['description'], translateToChinese, 'zh-CN')).toContain('描述')
    expect(agentChangeErrorMessage('Project "X" changed after the agent\'s edit (description), so nothing was changed', identity)).toBe('This changed after the agent’s edit, so nothing was undone.')
    for (const source of ['Created {label}', 'Updated project', 'Created custom view', 'Created initiative update', 'Highlight changes', 'Restore', 'Created {count} issues']) {
      expect(translateToChinese(source), source).not.toBe(source)
    }
    expect(translateToChinese('Created {label}')).toContain('{label}')
  })

  it('diffs Markdown by block for Highlight changes', () => {
    const before = 'Intro paragraph.\n\n## Scope\n\n- One\n- Two'
    const after = 'Intro paragraph.\n\n## Scope\n\n- One\n- Two\n- **Three** items\n\n## Risks\n\nLarge exports may [time out](https://x.test).'
    expect(diffMarkdownBlocks(before, after).filter(line => line.kind !== 'same').map(line => line.kind)).toEqual(['added', 'added', 'added'])
    expect(changedBlockTexts(before, after)).toEqual(['Three items', 'Risks', 'Large exports may time out.'])
    expect(diffMarkdownBlocks('A\n\nB', 'A\n\nC').map(line => line.kind)).toEqual(['same', 'added', 'removed'])
    expect(markdownPlainText('> *quoted* `code`')).toBe('quoted code')
  })
})
