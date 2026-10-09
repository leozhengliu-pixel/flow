import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Editor } from '@tiptap/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { makeIssue } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(), realtimeClientId: () => 'mention-test' }))
vi.mock('@/lib/api', () => api)

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { mentionFixture, mentionUrls } from './mention-fixtures'

function Shell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}</WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}

async function mountEditor(data: BootstrapData, props: { value?: string; state?: string } = {}) {
  let editor: Editor | null = null
  render(<Shell data={data}><IssueDescriptionEditor value={props.value ?? ''} state={props.state} users={data.users} editorRef={value => { editor = value }}/></Shell>)
  await waitFor(() => expect(editor).not.toBeNull())
  return () => editor as Editor
}

function paste(editor: Editor, text: string, html = '') {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: html ? ['text/plain', 'text/html'] : ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : type === 'text/html' ? html : '' } })
  act(() => { editor.view.dom.dispatchEvent(event) })
}

const mentions = (editor: Editor) => {
  const found: Array<Record<string, unknown>> = []
  editor.state.doc.descendants(node => { if (node.type.name === 'mention') found.push({ ...node.attrs }) })
  return found
}

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
  document.elementFromPoint = () => document.body
})
afterEach(() => { vi.unstubAllGlobals() })

describe('pasting references into rich text', () => {
  it('turns a pasted Flow URL of each resource type into a mention', async () => {
    const data = mentionFixture()
    for (const kind of ['issue', 'project', 'initiative', 'document', 'team', 'cycle', 'label', 'milestone', 'customer', 'release', 'view', 'review'] as const) {
      const editor = await mountEditor(data)
      editor().commands.focus()
      paste(editor(), `${window.location.origin}${mentionUrls[kind]}`)
      await waitFor(() => expect(mentions(editor()), kind).toHaveLength(1))
      expect(mentions(editor())[0], kind).toMatchObject({ mentionType: kind })
      expect(editor().getMarkdown(), kind).toMatch(new RegExp(`^\\[.+\\]\\(${mentionUrls[kind].replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\)$`))
      document.body.innerHTML = ''
    }
  })

  it('converts URLs, identifiers and @names inside pasted text and keeps the rest as text', async () => {
    const data = mentionFixture()
    const editor = await mountEditor(data)
    editor().commands.focus()
    paste(editor(), `Blocked by TST-1, see ${mentionUrls.document} (cc @Teammate). Version UTF-8 unchanged.`)
    await waitFor(() => expect(mentions(editor()).map(item => item.mentionType)).toEqual(['issue', 'document', 'user']))
    expect(editor().getText()).toBe('Blocked by TST-1, see Launch plan (cc @Teammate). Version UTF-8 unchanged.')
  })

  it('converts a pasted link from another Flow page (HTML) when its text names the resource', async () => {
    const data = mentionFixture()
    const editor = await mountEditor(data)
    editor().commands.focus()
    paste(editor(), 'TST-1 Test issue', `<a href="${mentionUrls.issue}" data-agent-entity="issue">TST-1 Test issue</a>`)
    await waitFor(() => expect(mentions(editor())).toHaveLength(1))
  })

  it('round-trips a copied mention through the clipboard HTML', async () => {
    const data = mentionFixture()
    const editor = await mountEditor(data)
    editor().commands.focus()
    paste(editor(), 'x', '<a data-flow-mention="document-1" data-mention-type="document" data-mention-label="Launch plan" data-mention-href="/workspace/document/plan-abc" href="/workspace/document/plan-abc">Launch plan</a>')
    await waitFor(() => expect(mentions(editor())).toEqual([expect.objectContaining({ id: 'document-1', mentionType: 'document', label: 'Launch plan' })]))
  })

  it('keeps a URL pasted over selected text as that text\'s link', async () => {
    const data = mentionFixture()
    const editor = await mountEditor(data, { value: 'the plan' })
    editor().commands.focus()
    editor().commands.selectAll()
    paste(editor(), `${window.location.origin}${mentionUrls.document}`)
    await waitFor(() => expect(editor().getMarkdown()).toContain(`](${window.location.origin}${mentionUrls.document})`))
    expect(mentions(editor())).toHaveLength(0)
  })

  it('leaves URLs to other hosts and other workspaces as text', async () => {
    const editor = await mountEditor(mentionFixture())
    editor().commands.focus()
    paste(editor(), 'https://github.com/acme/web/issues/9 and /other/document/plan-abc')
    await waitFor(() => expect(editor().getText()).toContain('github.com'))
    expect(mentions(editor())).toHaveLength(0)
  })

  it('fetches a pasted issue the client does not hold and fills in its title', async () => {
    api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
    const data = mentionFixture({ issueCollectionPaged: true, issues: [] })
    const editor = await mountEditor(data)
    editor().commands.focus()
    paste(editor(), '/workspace/issue/TST-9/remote-issue')
    await waitFor(() => expect(mentions(editor())).toEqual([expect.objectContaining({ mentionType: 'issue', title: 'Remote issue', id: '0a1b2c3d-1111-2222-3333-444455556666' })]))
    expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
  })
})

describe('typing references', () => {
  it('turns a typed team-key identifier into an issue mention, and backspace gives the text back', async () => {
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('Fix TST-1 now')
    expect(mentions(editor())).toEqual([expect.objectContaining({ mentionType: 'issue', id: 'issue-1', label: 'TST-1', title: 'Test issue' })])
    expect(editor().getMarkdown()).toBe('Fix [TST-1](/workspace/issue/TST-1/test-issue) now')
    expect(editor().getText()).toBe('Fix TST-1 now')
  })

  it('gives the text back when backspace follows the conversion', async () => {
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('TST-1 ')
    expect(mentions(editor())).toHaveLength(1)
    await user.keyboard('{Backspace}')
    expect(mentions(editor())).toHaveLength(0)
    expect(editor().getText()).toBe('TST-1 ')
  })

  it('does not convert identifiers that are not team keys, or inside code', async () => {
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('UTF-8 and DEV-1 ')
    editor().commands.setCodeBlock()
    await user.keyboard('TST-1 ')
    expect(mentions(editor())).toHaveLength(0)
    expect(editor().getText()).toContain('TST-1')
  })

  it('converts a typed identifier of an issue the client fetches by id', async () => {
    api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-5555-2222-3333-444455556666', identifier: 'TST-88', title: 'Far away' }))
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture({ issueCollectionPaged: true, issues: [] }))
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('See TST-88 ')
    await waitFor(() => expect(mentions(editor())).toEqual([expect.objectContaining({ mentionType: 'issue', label: 'TST-88', title: 'Far away' })]))
  })
})

describe('the @ menu', () => {
  it('lists people first and then issues, projects and documents, each under a heading', async () => {
    const user = userEvent.setup()
    await mountEditor(mentionFixture())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('@')
    const menu = await screen.findByRole('listbox', { name: 'Mention' })
    const headings = [...menu.querySelectorAll('.description-mention-group')].map(item => item.textContent)
    expect(headings).toEqual(['People', 'Issues', 'Projects', 'Documents'])
  })

  it('searches every resource type by name and inserts the chosen one as a mention', async () => {
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('@Road')
    const option = await screen.findByRole('option', { name: /Roadmap/ })
    await user.click(option)
    expect(mentions(editor())).toEqual([expect.objectContaining({ mentionType: 'initiative', id: 'initiative-1', label: 'Roadmap', href: '/workspace/initiative/roadmap/overview' })])
    expect(editor().getMarkdown()).toBe('[Roadmap](/workspace/initiative/roadmap/overview) ')
  })

  it('picks a person with Enter and writes @name', async () => {
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('@Team')
    await screen.findByRole('option', { name: /Teammate/ })
    await user.keyboard('{Enter}')
    expect(mentions(editor())).toEqual([expect.objectContaining({ mentionType: 'user', id: 'user-2', label: 'Teammate' })])
    expect(editor().getMarkdown()).toBe('@Teammate ')
  })

  it('finds an issue the client does not hold by asking the server', async () => {
    api.listIssueRecords.mockResolvedValue({ items: [makeIssue({ id: '0a1b2c3d-7777-2222-3333-444455556666', identifier: 'TST-42', title: 'Server side match' })], hasMore: false, total: 1 })
    const user = userEvent.setup()
    const editor = await mountEditor(mentionFixture({ issueCollectionPaged: true, issues: [] }))
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('@Server')
    await waitFor(() => expect(api.listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ q: 'Server' }), expect.anything(), 'workspace'))
    await user.click(await screen.findByRole('option', { name: /Server side match/ }))
    expect(mentions(editor())).toEqual([expect.objectContaining({ mentionType: 'issue', label: 'TST-42', title: 'Server side match' })])
  })
})

describe('loading content with references', () => {
  it('shows links to Flow resources in markdown as chips without saving a change', async () => {
    const onChange = vi.fn()
    const data = mentionFixture()
    render(<Shell data={data}><IssueDescriptionEditor value={`Plan: [the rollout plan](${mentionUrls.document}) for TST-1`} onChange={onChange} users={data.users}/></Shell>)
    await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity]')).toHaveLength(2))
    expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('keeps a person\'s own link text when the stored document says so', async () => {
    const data = mentionFixture()
    const state = JSON.stringify({ type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'the rollout plan', marks: [{ type: 'link', attrs: { href: mentionUrls.document } }] }] }] })
    render(<Shell data={data}><IssueDescriptionEditor value="" state={state} users={data.users}/></Shell>)
    await screen.findByText('the rollout plan')
    expect(document.querySelector('a[data-agent-entity]')).toBeNull()
  })
})


describe('the @ menu inside a modal dialog', () => {
  it('can be clicked although the dialog disables pointer events on the page, and keeps the dialog open', async () => {
    const { Root, Portal, Overlay, Content, Title } = await import('@radix-ui/react-dialog')
    const user = userEvent.setup()
    const data = mentionFixture()
    let editor: Editor | null = null
    render(<Shell data={data}><Root defaultOpen><Portal><Overlay/><Content aria-describedby={undefined}><Title>Create</Title><IssueDescriptionEditor value="" users={data.users} editorRef={value => { editor = value }}/></Content></Portal></Root></Shell>)
    await waitFor(() => expect(editor).not.toBeNull())
    await user.click(screen.getByRole('textbox'))
    await user.keyboard('@Road')
    await user.click(await screen.findByRole('option', { name: /Roadmap/ }))
    expect(mentions(editor!)).toEqual([expect.objectContaining({ mentionType: 'initiative', label: 'Roadmap' })])
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
