import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Composer } from './composer'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { viewer } from '@/test/fixtures'
import type { Draft } from '@/types/flow'

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

const renderComposer = (props: Parameters<typeof Composer>[0]) => render(<I18nProvider><TooltipProvider><Composer {...props}/></TooltipProvider></I18nProvider>)
const box = () => screen.findByRole('textbox', { name: 'Leave a comment…' })

describe('sending a comment', () => {
  it('uses the filled mobile issue glyphs only when requested', async () => {
    const { container } = renderComposer({ mobileIssue: true })
    await box()
    expect(container.querySelector('.composer-tools svg')).toHaveAttribute('viewBox', '0 0 16 16')
    expect(container.querySelector('.composer-send svg path')).toHaveAttribute('d', expect.stringContaining('M11.48 5.674'))
    expect(container.querySelector('.composer-tools svg path')).toHaveAttribute('d', expect.stringContaining('M12.6429 7.69048'))
    expect(screen.getByRole('button', { name: 'Submit comment' })).toBeDisabled()
  })

  it('keeps typed inline dollar math as literal paragraph text', async () => {
    const submit = vi.fn().mockResolvedValue(undefined)
    const { container } = renderComposer({ onSubmit: submit })
    await userEvent.type(await box(), '$x^2$')
    expect(container.querySelector('.math-block-container')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(submit).toHaveBeenCalledWith('$x^2$', expect.objectContaining({ content: [{ type: 'paragraph', content: [{ type: 'text', text: '$x^2$' }] }] }))
  })

  it('edits a $$ math block without submitting on Enter and posts its Markdown source', async () => {
    const user = userEvent.setup()
    const submit = vi.fn().mockResolvedValue(undefined)
    const { container } = renderComposer({ onSubmit: submit })
    await user.type(await box(), '$$')
    await user.keyboard('{Enter}x^2{Enter}+ y')
    expect(submit).not.toHaveBeenCalled()
    expect(container.querySelector('.math-block-container')).toHaveAttribute('data-editing')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(submit).toHaveBeenCalledWith('$$\nx^2\n+ y\n$$', expect.objectContaining({ type: 'doc', content: expect.arrayContaining([expect.objectContaining({ type: 'mathBlock' })]) }))
  })

  it('opens a Markdown-only formula for comment editing without flattening it', async () => {
    const submit = vi.fn().mockResolvedValue(undefined)
    const { container } = renderComposer({ initialValue: '$$\nx^2\n$$', onSubmit: submit })
    await waitFor(() => expect(container.querySelector('.katex')).not.toBeNull())
    await userEvent.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(submit).toHaveBeenCalledWith('$$\nx^2\n$$', expect.anything())
  })

  it('clears the composer the moment it is sent, while the request is still pending', async () => {
    const user = userEvent.setup()
    let finish: () => void = () => undefined
    const submit = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    renderComposer({ onSubmit: submit })
    const input = await box()
    await user.type(input, 'hello there')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(submit).toHaveBeenCalledWith('hello there', expect.objectContaining({ type: 'doc' }))
    expect(input).toHaveTextContent('')
    expect(screen.getByRole('button', { name: 'Submit comment' })).toBeDisabled()
    finish()
    await waitFor(() => expect(input).toHaveTextContent(''))
  })

  it('clears a comment that started from a saved draft just the same', async () => {
    const user = userEvent.setup()
    const submit = vi.fn(() => new Promise<void>(() => undefined))
    const draft = { id: 'draft-1', type: 'comment', resourceId: 'issue-1', title: 'T', body: 'from a draft', metadata: {}, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' } as unknown as Draft
    renderComposer({ onSubmit: submit, draftType: 'comment', draftResourceId: 'issue-1', drafts: [draft] })
    const input = await box()
    await waitFor(() => expect(input).toHaveTextContent('from a draft'))
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(submit).toHaveBeenCalledWith('from a draft', expect.anything())
    expect(input).toHaveTextContent('')
  })

  it('does not post twice when the shortcut is pressed twice', async () => {
    const user = userEvent.setup()
    const submit = vi.fn(() => new Promise<void>(() => undefined))
    renderComposer({ onSubmit: submit })
    await user.type(await box(), 'once')
    await user.keyboard('{Enter}{Enter}')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(submit).toHaveBeenCalledTimes(1)
  })

  it('puts the draft back, with its mention, and shows the error when the send fails', async () => {
    const user = userEvent.setup()
    const app = { ...viewer, id: 'app-fixture', name: 'worker', displayName: 'Build agent', app: true, appScopes: ['app:mentionable'] }
    const submit = vi.fn().mockRejectedValueOnce(new Error('Network down')).mockResolvedValue(undefined)
    renderComposer({ users: [app], onSubmit: submit })
    const input = await box()
    await user.type(input, 'ping @')
    await user.click(await screen.findByRole('option', { name: /Build agent/ }))
    await user.type(input, 'please')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Network down')
    await waitFor(() => expect(input).toHaveTextContent('ping @Build agent please'))
    expect(JSON.stringify(submit.mock.calls[0][1])).toContain('app-fixture')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(2))
    expect(submit.mock.calls[1][0]).toBe(submit.mock.calls[0][0])
    expect(JSON.stringify(submit.mock.calls[1][1])).toContain('app-fixture')
    expect(input).toHaveTextContent('')
  })

  it('keeps what was typed after a failed send and retries the failed comment', async () => {
    const user = userEvent.setup()
    let fail: (reason: Error) => void = () => undefined
    const submit = vi.fn().mockImplementationOnce(() => new Promise<void>((_, reject) => { fail = reject })).mockResolvedValue(undefined)
    renderComposer({ onSubmit: submit })
    const input = await box()
    await user.type(input, 'first')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await user.type(input, 'second')
    fail(new Error('Offline'))
    await screen.findByRole('alert')
    expect(input).toHaveTextContent('second')
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(2))
    expect(submit.mock.calls[1][0]).toBe('first')
  })

  it('keeps an edited comment in place until the save finishes', async () => {
    const user = userEvent.setup()
    let finish: () => void = () => undefined
    const submit = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    renderComposer({ initialValue: 'old text', placeholder: 'Edit comment…', onSubmit: submit })
    const input = await screen.findByRole('textbox', { name: 'Edit comment…' })
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(input).toHaveTextContent('old text')
    finish()
  })
})
