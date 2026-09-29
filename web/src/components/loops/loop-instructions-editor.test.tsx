import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { instructionsEditorMarkdown, loopEntityMarkdown, LoopInstructionsEditor } from './loop-instructions-editor'

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  listIssueRecords: vi.fn().mockResolvedValue({ items: [] }),
}))

const originalRects = Object.getOwnPropertyDescriptor(Range.prototype, 'getClientRects')
const originalRect = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect')
const originalFromPoint = Document.prototype.elementFromPoint
beforeAll(() => {
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
  Document.prototype.elementFromPoint = function (this: Document) { return this.body }
})
afterAll(() => {
  if (originalRects) Object.defineProperty(Range.prototype, 'getClientRects', originalRects)
  else Reflect.deleteProperty(Range.prototype, 'getClientRects')
  if (originalRect) Object.defineProperty(Range.prototype, 'getBoundingClientRect', originalRect)
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect')
  Document.prototype.elementFromPoint = originalFromPoint
})

const chipDoc = {
  type: 'doc',
  content: [
    { type: 'paragraph', content: [{ type: 'text', text: 'Check ' }, { type: 'mention', attrs: { mentionType: 'issue', id: 'issue-1', label: 'TST-1', title: 'Test issue', href: '/workspace/issue/TST-1/test-issue' } }] },
    { type: 'bulletList', content: [{ type: 'listItem', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Ping ' }, { type: 'mention', attrs: { mentionType: 'user', id: 'user-2', label: 'Teammate' } }] }] }] },
  ],
}

describe('LoopInstructionsEditor', () => {
  it('shows the trigger placeholder while empty', async () => {
    const view = render(<I18nProvider><LoopInstructionsEditor ariaLabel="Instructions" data={makeBootstrap()} value="" placeholder="For example, summarize…" onChange={vi.fn()}/></I18nProvider>)
    await screen.findByRole('textbox', { name: 'Instructions' })
    await waitFor(() => expect(view.container.querySelector('[data-placeholder]')).toHaveAttribute('data-placeholder', 'For example, summarize…'))
  })

  it('renders stored rich instructions with entity chips, lists and model markdown', async () => {
    const view = render(<I18nProvider><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={makeBootstrap()} value="Check TST-1" valueData={chipDoc}/></I18nProvider>)
    const document = await screen.findByRole('document', { name: 'Instructions' })
    await waitFor(() => expect(view.container.querySelector('[data-entity-kind="issue"]')).not.toBeNull())
    const issue = view.container.querySelector('a[data-entity-kind="issue"]')!
    expect(issue).toHaveTextContent('TST-1Test issue')
    expect(issue).toHaveAttribute('href', '/workspace/issue/TST-1/test-issue')
    expect(view.container.querySelector('[data-entity-kind="user"]')).toHaveTextContent('Teammate')
    expect(document.querySelector('ul li')).not.toBeNull()
  })

  it('turns "@" into a chip and reports markdown plus the editor document', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><LoopInstructionsEditor ariaLabel="Instructions" data={makeBootstrap()} value="" onChange={onChange}/></I18nProvider>)
    await user.type(await screen.findByRole('textbox', { name: 'Instructions' }), 'Ask @Team')
    await user.click(await screen.findByRole('option', { name: /Teammate/ }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.stringContaining('Ask @Teammate'), expect.objectContaining({ type: 'doc' })))
    expect(JSON.stringify(onChange.mock.lastCall![1])).toContain('"type":"mention"')
    expect(JSON.stringify(onChange.mock.lastCall![1])).toContain('"id":"user-2"')
  })

  it('turns agent-written markdown references into chips', () => {
    const data = makeBootstrap()
    const markdown = instructionsEditorMarkdown('Review TST-1 and ask @Teammate about [Project one](/workspace/project/project-one/overview).', data)
    expect(markdown).toMatch(/\[mention id="issue-1" label="TST-1"[^\]]*mentionType="issue"\]/)
    expect(markdown).toContain('[mention id="user-2" label="Teammate" mentionType="user"]')
    expect(markdown).toMatch(/\[mention id="project-1" label="Project one"[^\]]*mentionType="project"\]/)
    expect(loopEntityMarkdown({ mentionType: 'issue', label: 'TST-1' })).toBe('TST-1')
    expect(loopEntityMarkdown({ mentionType: 'user', label: 'Teammate' })).toBe('@Teammate')
    expect(loopEntityMarkdown({ mentionType: 'document', label: 'Spec', href: '/workspace/document/spec' })).toBe('[Spec](/workspace/document/spec)')
  })

  it('renders ordered and bullet lists with markers (Tailwind preflight strips them)', async () => {
    const view = render(<I18nProvider><LoopInstructionsEditor readOnly ariaLabel="Instructions" data={makeBootstrap()} value={'1. Read the issue\n2. Route it\n\n- Ping the owner'} onChange={vi.fn()}/></I18nProvider>)
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
