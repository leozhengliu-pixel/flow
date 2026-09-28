import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { JSONContent } from '@tiptap/core'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IssueDescriptionEditor } from '../issue-description-editor'
import { headingLink, headingSlug, makeHeadingCollapsible } from './heading-actions'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'editor-test' }))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe('description heading actions', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })
  afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, '', '/') })

  it('builds stable heading slugs and page links', () => {
    expect(headingSlug('Launch Plan: Phase 2!')).toBe('launch-plan-phase-2')
    expect(headingSlug('发布 计划')).toBe('发布-计划')
    expect(headingSlug('   ')).toBe('heading')
    expect(headingLink('scope', { origin: 'https://flow.test', pathname: '/p/1', search: '?tab=overview' })).toBe('https://flow.test/p/1?tab=overview#scope')
  })

  it('shows a 20px heading actions button 28px left of a hovered heading', async () => {
    const { container } = render(<IssueDescriptionEditor value={'# Scope\n\nBody'}/>)
    const heading = await screen.findByRole('heading', { level: 1, name: 'Scope' })
    const root = container.querySelector('.issue-description-root') as HTMLElement
    root.getBoundingClientRect = () => new DOMRect(100, 50, 600, 400)
    heading.getBoundingClientRect = () => new DOMRect(114, 70, 500, 30)
    expect(screen.queryByRole('button', { name: 'Heading actions' })).toBeNull()
    fireEvent.mouseMove(heading)
    const button = await screen.findByRole('button', { name: 'Heading actions' })
    expect(button.style.left).toBe('-14px')
    expect(button.style.top).toBe('25px')
    fireEvent.mouseLeave(root)
    expect(screen.queryByRole('button', { name: 'Heading actions' })).toBeNull()
  })

  it('reveals the heading actions button when the caret is in a heading', async () => {
    let editor: Editor | null = null
    render(<IssueDescriptionEditor value={'# Scope\n\nBody'} editorRef={value => { if (value) editor = value }}/>)
    await screen.findByRole('heading', { level: 1, name: 'Scope' })
    await waitFor(() => expect(editor).not.toBeNull())
    const current = editor as unknown as Editor
    expect(screen.queryByRole('button', { name: 'Heading actions' })).toBeNull()
    act(() => { current.commands.focus(); current.commands.setTextSelection(2) })
    expect(await screen.findByRole('button', { name: 'Heading actions' })).toBeTruthy()
    act(() => { current.commands.setTextSelection(current.state.doc.content.size - 1) })
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Heading actions' })).toBeNull())
  })

  it('copies a heading anchor link from the menu', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(<IssueDescriptionEditor value={'# Scope\n\nBody\n\n## Scope'}/>)
    const heading = await screen.findByRole('heading', { level: 2, name: 'Scope' })
    fireEvent.mouseMove(heading)
    await userEvent.click(await screen.findByRole('button', { name: 'Heading actions' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Copy link' }))
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/#scope-1`)
  })

  it('makes a heading and its section collapsible from the menu', async () => {
    let editor: Editor | null = null
    render(<IssueDescriptionEditor value={'# Scope\n\nOne\n\n## Detail\n\nTwo\n\n# Next\n\nThree'} editorRef={value => { if (value) editor = value }}/>)
    const heading = await screen.findByRole('heading', { level: 1, name: 'Scope' })
    fireEvent.mouseMove(heading)
    await userEvent.click(await screen.findByRole('button', { name: 'Heading actions' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Make collapsible' }))
    await waitFor(() => expect(editor!.getJSON().content?.[0].type).toBe('details'))
    const json = editor!.getJSON() as JSONContent
    const [summary, content] = json.content![0].content! as JSONContent[]
    expect(summary.content?.[0].text).toBe('Scope')
    expect(content.content?.map((node: JSONContent) => node.type)).toEqual(['paragraph', 'heading', 'paragraph'])
    expect(json.content?.[1]).toMatchObject({ type: 'heading', attrs: { level: 1 } })
  })

  it('wraps a trailing heading with an empty body', async () => {
    let editor: Editor | null = null
    render(<IssueDescriptionEditor value={'Intro\n\n### Last'} editorRef={value => { if (value) editor = value }}/>)
    await screen.findByRole('heading', { level: 3, name: 'Last' })
    let pos = -1
    editor!.state.doc.forEach((node, offset) => { if (node.type.name === 'heading') pos = offset })
    act(() => { expect(makeHeadingCollapsible(editor!, pos)).toBe(true) })
    const last = (editor!.getJSON() as JSONContent).content!.find(node => node.type === 'details')!
    expect(editor!.getJSON().content![1].type).toBe('details')
    expect(last.content?.[1].content).toEqual([{ type: 'paragraph' }])
  })

  it('scrolls to the heading named in the URL hash', async () => {
    const scrollIntoView = vi.fn()
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, writable: true, value: scrollIntoView })
    window.history.replaceState(null, '', '/#detail')
    render(<IssueDescriptionEditor value={'# Scope\n\n## Detail'}/>)
    await screen.findByRole('heading', { level: 2, name: 'Detail' })
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalled())
    expect((scrollIntoView.mock.contexts[0] as HTMLElement).textContent).toBe('Detail')
    Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView')
  })
})
