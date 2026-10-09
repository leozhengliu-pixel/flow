import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeIssue } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(), realtimeClientId: () => 'loop-test' }))
vi.mock('@/lib/api', () => api)

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { parseAgentEntityUrl } from '@/components/agent/agent-entity-refs'
import { mentionFixture, mentionLabels, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { mentionAttrsForTarget } from '@/components/editor/mentions/mention-model'
import { loopEntityMarkdown, LoopInstructionsEditor } from './loop-instructions-editor'

function Where() {
  const location = useLocation()
  return <output data-testid="location">{`${location.pathname}${location.hash}`}</output>
}

function Shell({ children }: { children: ReactNode }) {
  return <I18nProvider><MemoryRouter>{children}<Where/></MemoryRouter></I18nProvider>
}

type Doc = Record<string, unknown>
const mentionAttrs = (document: unknown) => {
  const found: Array<Record<string, unknown>> = []
  const walk = (node: unknown) => {
    const current = node as { type?: string; attrs?: Record<string, unknown>; content?: unknown[] }
    if (current.type === 'mention') found.push(current.attrs ?? {})
    current.content?.forEach(walk)
  }
  walk(document)
  return found
}

function attrsFor(data: BootstrapData, kind: keyof typeof mentionUrls) {
  if (kind === 'user') return { mentionType: 'user', id: 'user-2', label: 'Teammate' }
  return mentionAttrsForTarget(data, parseAgentEntityUrl(mentionUrls[kind], data)!, mentionUrls[kind])
}

const docOf = (...attrs: Array<Record<string, unknown>>) => ({
  type: 'doc',
  content: [{ type: 'paragraph', content: attrs.flatMap((item, index) => [{ type: 'text', text: index ? ' ' : 'Check ' }, { type: 'mention', attrs: item }]) }],
})

function paste(element: Element, text: string, html = '') {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: html ? ['text/plain', 'text/html'] : ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : type === 'text/html' ? html : '' } })
  act(() => { element.dispatchEvent(event) })
}

async function chip(kind: string) {
  return waitFor(() => {
    const found = document.querySelector(`a[data-agent-entity="${kind}"]`)
    expect(found, kind).not.toBeNull()
    return found as HTMLAnchorElement
  })
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

describe('LoopInstructionsEditor', () => {
  it('shows the trigger placeholder while empty', async () => {
    const view = render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={mentionFixture()} value="" placeholder="For example, summarize…" onChange={vi.fn()}/></Shell>)
    await screen.findByRole('textbox', { name: 'Instructions' })
    await waitFor(() => expect(view.container.querySelector('[data-placeholder]')).toHaveAttribute('data-placeholder', 'For example, summarize…'))
  })

  it('renders every kind of stored mention as the shared chip, next to lists', async () => {
    const data = mentionFixture()
    const kinds = Object.keys(mentionUrls) as Array<keyof typeof mentionUrls>
    const document_ = docOf(...kinds.map(kind => attrsFor(data, kind)))
    document_.content.push({ type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Ping the owner' }] }] }] } as never)
    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value="" valueData={document_}/></Shell>)
    const expected: Record<string, string> = { ...mentionLabels, issue: 'TST-1 Test issue', user: '@Teammate', milestone: 'Alpha · Project one' }
    for (const kind of kinds) {
      const found = await chip(kind)
      expect(found.textContent?.replace(/ /g, ' ').trim(), kind).toBe(expected[kind])
    }
    expect(document.querySelector('a[data-agent-entity="issue"] svg')).not.toBeNull()
    expect(screen.getByRole('document', { name: 'Instructions' }).querySelector('ul li')).toHaveTextContent('Ping the owner')
  })

  it('shows the current title of a mentioned issue and falls back to the stored label when the resource is gone', async () => {
    const data = mentionFixture({ issues: [makeIssue({ title: 'Renamed issue' })] })
    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value="" valueData={docOf({ mentionType: 'issue', id: 'issue-1', label: 'TST-1', title: 'Old title', href: mentionUrls.issue }, { mentionType: 'document', id: 'gone', label: 'Old plan', href: '/workspace/document/gone' })}/></Shell>)
    expect(await chip('issue')).toHaveTextContent('TST-1 Renamed issue')
    const missing = await chip('document')
    expect(missing).toHaveTextContent('Old plan')
    expect(missing).toHaveAttribute('data-mention-state', 'missing')
  })

  it('opens the hover card of a chip', async () => {
    const data = mentionFixture()
    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value="" valueData={docOf(attrsFor(data, 'project'))}/></Shell>)
    await act(async () => { (await chip('project')).focus() })
    const card = await waitFor(() => {
      const element = document.querySelector('[data-agent-entity-card="project"]')
      expect(element).not.toBeNull()
      return element as HTMLElement
    })
    expect(card).toHaveTextContent('Project one')
  })

  it('navigates in the app through onNavigate and leaves a modified click to the browser', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    const data = mentionFixture()
    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value="" valueData={docOf(attrsFor(data, 'document'))} onNavigate={onNavigate}/></Shell>)
    await user.click(await chip('document'))
    expect(onNavigate).toHaveBeenCalledWith(mentionUrls.document)
    expect(screen.getByTestId('location')).toHaveTextContent('/')
    onNavigate.mockClear()
    await user.keyboard('{Meta>}')
    await user.click(await chip('document'))
    await user.keyboard('{/Meta}')
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('navigates through the router when the page gives no onNavigate', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value="" valueData={docOf(attrsFor(data, 'document'))}/></Shell>)
    await user.click(await chip('document'))
    expect(screen.getByTestId('location')).toHaveTextContent('/workspace/document/plan-abc')
  })

  it('fetches a mentioned issue the client does not hold', async () => {
    api.fetchIssueRecord.mockResolvedValue(makeIssue({ id: '0a1b2c3d-1111-2222-3333-444455556666', identifier: 'TST-9', title: 'Remote issue' }))
    const data = mentionFixture({ issueCollectionPaged: true, issues: [] })
    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value="" valueData={docOf({ mentionType: 'issue', id: '0a1b2c3d-1111-2222-3333-444455556666', label: 'TST-9', title: '', href: '/workspace/issue/TST-9/remote-issue' })}/></Shell>)
    expect(await waitFor(() => { const found = document.querySelector('a[data-agent-entity="issue"]'); expect(found).toHaveTextContent('TST-9 Remote issue'); return found })).not.toBeNull()
    expect(api.fetchIssueRecord).toHaveBeenCalledTimes(1)
  })

  it('offers every kind of resource in the "@" menu and saves the chosen one with the stored node shape', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={mentionFixture()} value="" onChange={onChange}/></Shell>)
    await user.click(await screen.findByRole('textbox', { name: 'Instructions' }))
    await user.keyboard('Plan @')
    const menu = await screen.findByRole('listbox', { name: 'Mention' })
    expect([...menu.querySelectorAll('.description-mention-group')].map(item => item.textContent)).toEqual(['People', 'Issues', 'Projects', 'Documents'])
    await user.keyboard('Road')
    await user.click(await screen.findByRole('option', { name: /Roadmap/ }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith('Plan [Roadmap](/workspace/initiative/roadmap/overview) ', expect.objectContaining({ type: 'doc' })))
    expect(mentionAttrs(onChange.mock.lastCall![1])).toEqual([{ mentionType: 'initiative', id: 'initiative-1', label: 'Roadmap', title: '', href: '/workspace/initiative/roadmap/overview' }])
    expect(await chip('initiative')).toHaveTextContent('Roadmap')
  })

  it('picks a person with Enter and writes @name for the model', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={mentionFixture()} value="" onChange={onChange}/></Shell>)
    await user.type(await screen.findByRole('textbox', { name: 'Instructions' }), 'Ask @Team')
    await screen.findByRole('option', { name: /Teammate/ })
    await user.keyboard('{Enter}')
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.stringContaining('Ask @Teammate'), expect.objectContaining({ type: 'doc' })))
    expect(mentionAttrs(onChange.mock.lastCall![1])).toEqual([expect.objectContaining({ mentionType: 'user', id: 'user-2', label: 'Teammate' })])
  })

  it('writes an issue as its identifier and a document as a link, as the server reads them', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={mentionFixture()} value="" onChange={onChange}/></Shell>)
    await user.type(await screen.findByRole('textbox', { name: 'Instructions' }), '@TST-1')
    await user.click(await screen.findByRole('option', { name: /TST-1/ }))
    await user.keyboard('and @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    await waitFor(() => expect(mentionAttrs(onChange.mock.lastCall![1])).toHaveLength(2))
    expect(onChange.mock.lastCall![0]).toBe('TST-1 and [Launch plan](/workspace/document/plan-abc) ')
    expect(mentionAttrs(onChange.mock.lastCall![1])[0]).toEqual({ mentionType: 'issue', id: 'issue-1', label: 'TST-1', title: 'Test issue', href: mentionUrls.issue })
    expect(loopEntityMarkdown({ mentionType: 'issue', label: 'TST-1' })).toBe('TST-1')
    expect(loopEntityMarkdown({ mentionType: 'user', label: 'Teammate' })).toBe('@Teammate')
    expect(loopEntityMarkdown({ mentionType: 'document', label: 'Spec', href: '/workspace/document/spec' })).toBe('[Spec](/workspace/document/spec)')
  })

  it('turns a pasted Flow URL of each kind into a mention', async () => {
    const data = mentionFixture()
    for (const kind of ['issue', 'project', 'initiative', 'document', 'team', 'cycle', 'label', 'milestone', 'customer', 'release', 'view', 'review'] as const) {
      const onChange = vi.fn()
      const view = render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={data} value="" onChange={onChange}/></Shell>)
      const box = await screen.findByRole('textbox', { name: 'Instructions' })
      act(() => box.focus())
      paste(box, `${window.location.origin}${mentionUrls[kind]}`)
      await waitFor(() => expect(onChange, kind).toHaveBeenCalled())
      expect(mentionAttrs(onChange.mock.lastCall![1]), kind).toEqual([expect.objectContaining({ mentionType: kind })])
      view.unmount()
    }
  })

  it('converts a typed team-key identifier into an issue mention and a pasted identifier inside text', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={mentionFixture()} value="" onChange={onChange}/></Shell>)
    const box = await screen.findByRole('textbox', { name: 'Instructions' })
    await user.click(box)
    await user.keyboard('Fix TST-1 now')
    expect(mentionAttrs(onChange.mock.lastCall![1])).toEqual([expect.objectContaining({ mentionType: 'issue', id: 'issue-1', label: 'TST-1' })])
    paste(box, `see ${mentionUrls.document}`)
    await waitFor(() => expect(mentionAttrs(onChange.mock.lastCall![1]).map(item => item.mentionType)).toEqual(['issue', 'document']))
  })

  it('shows the saved chips after a reload from the saved document and from the saved markdown', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const onChange = vi.fn()
    const first = render(<Shell><LoopInstructionsEditor ariaLabel="Instructions" data={data} value="" onChange={onChange}/></Shell>)
    await user.type(await screen.findByRole('textbox', { name: 'Instructions' }), 'Review @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    await waitFor(() => expect(mentionAttrs(onChange.mock.lastCall![1])).toHaveLength(1))
    const [markdown, saved] = onChange.mock.lastCall! as [string, Doc]
    first.unmount()

    const fromJson = render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value={markdown} valueData={saved}/></Shell>)
    expect(await chip('document')).toHaveTextContent('Launch plan')
    fromJson.unmount()

    render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={data} value={`${markdown} and TST-1, ask @Teammate`}/></Shell>)
    expect(await chip('document')).toHaveTextContent('Launch plan')
    expect(await chip('issue')).toHaveTextContent('TST-1 Test issue')
    expect(await chip('user')).toHaveTextContent('@Teammate')
  })

  it('renders ordered and bullet lists with markers (Tailwind preflight strips them)', async () => {
    const view = render(<Shell><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={mentionFixture()} value={'1. Read the issue\n2. Route it\n\n- Ping the owner'} onChange={vi.fn()}/></Shell>)
    await waitFor(() => expect(view.container.querySelector('.loops-instructions-prosemirror ol li')).toHaveTextContent('Read the issue'))
    expect(view.container.querySelector('.loops-instructions-prosemirror ul li')).toHaveTextContent('Ping the owner')
    // jsdom loads no stylesheets; read the rule itself (Node APIs are untyped in this project).
    const fs = (await import(/* @vite-ignore */ `node:${'fs'}`)) as { readFileSync: (path: string, encoding: 'utf8') => string }
    const cwd = (globalThis as unknown as { process: { cwd: () => string } }).process.cwd()
    const css = fs.readFileSync(`${cwd}/src/components/loops/loops-page.css`, 'utf8')
    expect(css).toMatch(/\.loops-instructions-prosemirror ul,\s*\.loops-run-answer ul \{ list-style: disc; \}/)
    expect(css).toMatch(/\.loops-instructions-prosemirror ol,\s*\.loops-run-answer ol \{ list-style: decimal; \}/)
  })
})
