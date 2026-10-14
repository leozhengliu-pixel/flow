import { renderHook } from '@testing-library/react'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AgentEntityDataContext } from '@/components/agent/agent-entity-data'
import { OPEN_FLOATING_AGENT_EVENT } from '@/components/agent/agent-floating-request'
import { MentionExtension } from '@/components/issue/editor/mention-extension'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { Issue } from '@/types/flow'
import { askAgentAboutSelection, CREATE_ISSUE_INLINE_EVENT, createIssueFromSelection, selectionIssueStateId, selectionIssueFields, teamDefaultStateId, useComposerSelectionActions, type InlineIssueRequest } from './selection-issue'
import { SelectionIssueProvider } from './selection-issue-provider'

let editors: Editor[] = []
function makeEditor(html: string) {
  const element = document.createElement('div')
  document.body.appendChild(element)
  const editor = new Editor({ element, extensions: [StarterKit, MentionExtension], content: html })
  editors.push(editor)
  return editor
}
function rangeOf(editor: Editor, text: string) {
  let found: { from: number; to: number } | undefined
  editor.state.doc.descendants((node, pos) => {
    if (found || !node.isText) return
    const index = node.text!.indexOf(text)
    if (index >= 0) found = { from: pos + index, to: pos + index + text.length }
  })
  return found!
}
/** Stands in for the app: answers the inline create request with an issue built from the input. */
function answerCreates(issue: (input: InlineIssueRequest['input']) => Issue) {
  const requests: InlineIssueRequest['input'][] = []
  const listener = (event: Event) => {
    const detail = (event as CustomEvent<InlineIssueRequest>).detail
    requests.push(detail.input)
    detail.result = Promise.resolve(issue(detail.input))
  }
  window.addEventListener(CREATE_ISSUE_INLINE_EVENT, listener)
  return { requests, stop: () => window.removeEventListener(CREATE_ISSUE_INLINE_EVENT, listener) }
}

afterEach(() => { editors.forEach(editor => editor.destroy()); editors = []; document.body.innerHTML = '' })

describe('selection issue fields', () => {
  it('takes the first line as the title and the rest as the description', () => {
    expect(selectionIssueFields('Nova title line\n​Second body line here')).toEqual({ title: 'Nova title line', description: 'Second body line here' })
    expect(selectionIssueFields('  \n  Only a title  ')).toEqual({ title: 'Only a title', description: '' })
    expect(selectionIssueFields('x'.repeat(300))?.title).toHaveLength(255)
    expect(selectionIssueFields(' \n ')).toBeUndefined()
  })
})

describe('create issue from a comment selection', () => {
  it('creates the issue in the page context and swaps the selected text for a mention of it', async () => {
    const base = makeBootstrap()
    // A sub-issue starts in the team's first unstarted status ("Todo"), not its default ("Backlog").
    const [first, ...rest] = base.states
    const data = { ...base, states: [{ ...first, default: true }, ...rest, { ...first, id: 'state-todo', name: 'Todo', type: 'unstarted' as const, position: 1 }] }
    const editor = makeEditor('<p>Reply words Oscar papa quebec</p>')
    const created = { ...data.issues[0], id: 'issue-new', identifier: 'FLO-34', title: 'Oscar papa quebec' } as Issue
    const app = answerCreates(() => created)
    const selection = { ...rangeOf(editor, 'Oscar papa quebec'), text: 'Oscar papa quebec' }
    await createIssueFromSelection({ editor, selection, data, context: { teamId: 'team-9', projectId: 'project-2', parentId: 'issue-1' } })
    app.stop()
    expect(app.requests).toEqual([{ title: 'Oscar papa quebec', description: '', teamId: 'team-9', stateId: 'state-todo', projectId: 'project-2', parentId: 'issue-1' }])
    expect(teamDefaultStateId(data, 'team-9')).toBe('state-backlog')
    const mention = editor.getJSON().content?.[0].content?.find(node => node.type === 'mention') as { attrs?: Record<string, unknown> } | undefined
    expect(mention?.attrs).toMatchObject({ mentionType: 'issue', label: 'FLO-34' })
    expect(editor.state.doc.textContent).not.toContain('Oscar papa quebec')
    expect(editor.state.doc.textContent.startsWith('Reply words ')).toBe(true)
  })

  it('starts a sub-issue in its parent\'s status while the parent has not started', () => {
    const base = makeBootstrap()
    const todo = { ...base.states[0], id: 'state-todo', name: 'Todo', type: 'unstarted' as const, position: 1 }
    const parent = { ...base.issues[0], id: 'parent-x', state: base.states[0] }
    const data = { ...base, states: [...base.states, todo], issues: [parent] }
    const team = parent.team.id
    expect(selectionIssueStateId(data, team, 'parent-x')).toBe(base.states[0].id)
    const startedParent = { ...parent, state: base.states.find(state => state.type === 'started')! }
    expect(selectionIssueStateId({ ...data, issues: [startedParent] }, team, 'parent-x')).toBe('state-todo')
  })

  it('falls back to the workspace\'s first team without a page context', async () => {
    const data = makeBootstrap()
    const editor = makeEditor('<p>Plan the beta</p>')
    const app = answerCreates(input => ({ ...data.issues[0], id: 'issue-x', title: input.title }) as Issue)
    await createIssueFromSelection({ editor, selection: { ...rangeOf(editor, 'Plan the beta'), text: 'Plan the beta' }, data, context: {} })
    app.stop()
    expect(app.requests[0]).toMatchObject({ title: 'Plan the beta', teamId: data.teams[0].id, projectId: undefined, parentId: undefined })
    // It starts in the team's default status (an issue made in the app skips Triage).
    expect(app.requests[0].stateId).toBeDefined()
    expect(app.requests[0].stateId).toBe(teamDefaultStateId(data, data.teams[0].id))
  })
})

describe('composer selection actions', () => {
  it('asks the floating agent with the selection as a quote, and creates issues with the provided context', () => {
    const data = makeBootstrap()
    const wrapper = ({ children }: { children: ReactNode }) => <I18nProvider><AgentEntityDataContext.Provider value={data}><SelectionIssueProvider value={{ teamId: 'team-9', parentId: 'issue-1' }}>{children}</SelectionIssueProvider></AgentEntityDataContext.Provider></I18nProvider>
    const { result } = renderHook(() => useComposerSelectionActions(), { wrapper })
    expect(result.current.onComment).toBeUndefined()
    const requests: unknown[] = []
    const listener = (event: Event) => requests.push((event as CustomEvent).detail)
    window.addEventListener(OPEN_FLOATING_AGENT_EVENT, listener)
    const editor = makeEditor('<p>Reply words</p>')
    result.current.onAskAgent?.({ ...rangeOf(editor, 'Reply'), text: 'Reply' }, editor)
    window.removeEventListener(OPEN_FLOATING_AGENT_EVENT, listener)
    expect(requests).toEqual([{ quote: 'Reply' }])
    // The composer lets go of the selection (the toolbar closes).
    expect(editor.state.selection.empty).toBe(true)

    const app = answerCreates(input => ({ ...data.issues[0], id: 'issue-y', title: input.title }) as Issue)
    result.current.onCreateIssue?.({ ...rangeOf(editor, 'words'), text: 'words' }, editor)
    app.stop()
    expect(app.requests[0]).toMatchObject({ title: 'words', teamId: 'team-9', parentId: 'issue-1' })
  })

  it('keeps a faint highlight on the quoted text (a CSS highlight over the editor range)', () => {
    const registry = new Map<string, { ranges: Range[] }>()
    vi.stubGlobal('Highlight', class { ranges: Range[]; constructor(...ranges: Range[]) { this.ranges = ranges } })
    vi.stubGlobal('CSS', { ...globalThis.CSS, highlights: registry })
    try {
      const editor = makeEditor('<p>Reply words here</p>')
      askAgentAboutSelection({ ...rangeOf(editor, 'words'), text: 'words' }, editor)
      expect(registry.get('flow-agent-quote')?.ranges[0].toString()).toBe('words')
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
