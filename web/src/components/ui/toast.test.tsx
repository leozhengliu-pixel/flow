import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { toast, TOAST_DURATION } from '@/lib/toast'
import { resetToastRegistry, setToastNavigator } from '@/lib/toast-registry'
import { ThemedToaster } from './themed-toaster'

const items = () => [...document.querySelectorAll<HTMLElement>('li[data-sonner-toast]')]
/** Advances fake time in small steps, flushing React between them, so rAF-then-timeout chains (dismiss, then unmount) complete. */
const settle = async (ms = 400) => {
  const step = 25
  for (let elapsed = 0; elapsed < ms; elapsed += step) await act(async () => { await vi.advanceTimersByTimeAsync(Math.min(step, ms - elapsed)) })
}

function setPlatform(platform: string) {
  Object.defineProperty(navigator, 'platform', { configurable: true, value: platform })
}

describe('toast system', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setPlatform('MacIntel')
    render(<ThemedToaster/>)
  })
  afterEach(async () => {
    act(() => { toast.dismiss() })
    await settle(500)
    resetToastRegistry()
    vi.useRealTimers()
  })

  it('renders kind, title, description and a dismiss button', async () => {
    act(() => { toast.success('Release created', { description: 'QA release alpha' }) })
    await settle(50)
    expect(screen.getByText('Release created')).toBeInTheDocument()
    expect(screen.getByText('QA release alpha')).toBeInTheDocument()
    expect(document.querySelector('.flow-toast')).toHaveAttribute('data-kind', 'success')
    expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument()
  })

  it('puts a button option on the title row, runs it and dismisses the toast', async () => {
    const onClick = vi.fn()
    act(() => { toast.info('Chat deleted', { button: { label: 'Undo', onClick } }) })
    await settle(50)
    const undo = screen.getByRole('button', { name: 'Undo' })
    expect(undo).toHaveClass('flow-toast__button')
    // Beside dismiss on the title's row, not in the links row under it.
    expect(undo.parentElement).toHaveClass('flow-toast__actions')
    expect(undo.parentElement).toContainElement(screen.getByRole('button', { name: 'Dismiss' }))
    expect(document.querySelector('.flow-toast__links')).toBeNull()
    fireEvent.click(undo)
    expect(onClick).toHaveBeenCalledTimes(1)
    await settle(500)
    expect(screen.queryByText('Chat deleted')).not.toBeInTheDocument()
  })

  it('stacks distinct toasts newest-first, always expanded, and caps the visible stack at four', async () => {
    act(() => { for (let index = 1; index <= 5; index += 1) toast.info(`Toast ${index}`) })
    await settle(50)
    const list = items()
    expect(list).toHaveLength(5)
    // data-index 0 is the newest (the bottom card); the oldest falls out of the visible stack.
    expect(list[0]).toHaveTextContent('Toast 5')
    expect(list.map(item => item.getAttribute('data-expanded'))).toEqual(Array(5).fill('true'))
    expect(list.map(item => item.getAttribute('data-visible'))).toEqual(['true', 'true', 'true', 'true', 'false'])
    expect(document.querySelector('[data-sonner-toaster]')).toHaveStyle({ '--gap': '8px', '--width': '384px' })
  })

  it('de-duplicates identical toasts instead of stacking copies', async () => {
    act(() => { toast.info('Copied issue link to clipboard'); toast.info('Copied issue link to clipboard') })
    await settle(50)
    expect(items()).toHaveLength(1)
    act(() => { toast.info('Copied issue ID to clipboard') })
    await settle(50)
    expect(items()).toHaveLength(2)
  })

  it('marks clipboard successes for the desktop copy glyph without replacing compact success icons', async () => {
    act(() => { toast.success('Issue URL copied to clipboard'); toast.success('已复制事项链接'); toast.success('Release created') })
    await settle(50)
    const confirmations = [...document.querySelectorAll('.flow-toast[data-clipboard]')]
    expect(confirmations).toHaveLength(2)
    for (const confirmation of confirmations) {
      expect(confirmation.querySelector('.flow-toast__icon circle')).not.toBeNull()
      expect(confirmation.querySelector('.flow-toast__copy-icon rect')).not.toBeNull()
    }
    expect(screen.getByText('Release created').closest('.flow-toast')).not.toHaveAttribute('data-clipboard')
  })

  it('updates a toast in place: loading becomes success under the same id', async () => {
    act(() => { toast.loading('Creating release…', { id: 'create' }) })
    await settle(50)
    expect(document.querySelector('.flow-toast')).toHaveAttribute('data-kind', 'loading')
    expect(document.querySelector('.flow-toast__spinner')).not.toBeNull()
    act(() => { toast.success('Release created', { id: 'create' }) })
    await settle(50)
    expect(items()).toHaveLength(1)
    expect(screen.queryByText('Creating release…')).toBeNull()
    expect(document.querySelector('.flow-toast')).toHaveAttribute('data-kind', 'success')
  })

  it('resolves toast.promise from loading to success or error', async () => {
    let resolve!: (value: string) => void
    const done = new Promise<string>(next => { resolve = next })
    act(() => { void toast.promise(done, { loading: 'Saving…', success: value => `Saved ${value}`, error: 'Failed' }).unwrap() })
    await settle(50)
    expect(screen.getByText('Saving…')).toBeInTheDocument()
    await act(async () => { resolve('draft') })
    await settle(50)
    expect(items()).toHaveLength(1)
    expect(screen.getByText('Saved draft')).toBeInTheDocument()

    const failing = Promise.reject(new Error('nope'))
    failing.catch(() => undefined)
    act(() => { void toast.promise(failing, { loading: 'Deleting…', success: 'Deleted', error: error => `Could not delete: ${(error as Error).message}` }).unwrap().catch(() => undefined) })
    await settle(50)
    expect(screen.getByText('Could not delete: nope')).toBeInTheDocument()
  })

  it('auto-dismisses after the default duration', async () => {
    act(() => { toast.info('Short lived') })
    await settle(TOAST_DURATION - 500)
    expect(items()).toHaveLength(1)
    await settle(1200)
    expect(items()).toHaveLength(0)
  })

  it('pauses the timer while the stack is hovered and resumes on leave', async () => {
    act(() => { toast.info('Hover me') })
    await settle(50)
    const stack = document.querySelector('[data-sonner-toaster]') as HTMLElement
    fireEvent.mouseEnter(stack)
    await settle(TOAST_DURATION * 2)
    expect(items()).toHaveLength(1)
    fireEvent.mouseLeave(stack)
    await settle(TOAST_DURATION + 1000)
    expect(items()).toHaveLength(0)
  })

  it('re-flows the stack when a toast is dismissed', async () => {
    act(() => { toast.info('First'); toast.info('Second'); toast.info('Third') })
    await settle(50)
    expect(items().map(item => item.textContent)).toEqual([expect.stringContaining('Third'), expect.stringContaining('Second'), expect.stringContaining('First')])
    const second = items()[1]
    fireEvent.click(second.querySelector('button[aria-label="Dismiss"]')!)
    await settle(50)
    expect(second).toHaveAttribute('data-removed', 'true')
    await settle(500)
    const remaining = items()
    expect(remaining).toHaveLength(2)
    expect(remaining.map(item => item.getAttribute('data-index'))).toEqual(['0', '1'])
    expect(remaining[1]).toHaveTextContent('First')
    expect(remaining[1].getAttribute('style')).toContain('--toasts-before: 1')
  })

  it('dismisses the focused toast on Escape', async () => {
    act(() => { toast.info('Escape me') })
    await settle(50)
    fireEvent.keyDown(document.querySelector('.flow-toast')!, { key: 'Escape' })
    await settle(500)
    expect(items()).toHaveLength(0)
  })

  it('renders entity chips with identifier and icon and opens the entity on click', async () => {
    const onClick = vi.fn()
    act(() => {
      toast.success('Issue created', {
        entity: { icon: <svg data-testid="status-icon"/>, identifier: 'FLO-12', title: 'Fix the login bug', onClick },
        action: { label: 'View issue', onClick: vi.fn() },
      })
    })
    await settle(50)
    const chip = screen.getByRole('button', { name: /FLO-12\s*Fix the login bug/ })
    expect(chip).toHaveClass('flow-toast__chip')
    expect(screen.getByTestId('status-icon')).toBeInTheDocument()
    fireEvent.click(chip)
    expect(onClick).toHaveBeenCalledTimes(1)
    await settle(500)
    expect(items()).toHaveLength(0)
  })

  it('renders a non-interactive chip when the entity has no target', async () => {
    act(() => { toast.success('Release created', { entity: { title: 'QA release alpha' } }) })
    await settle(50)
    expect(screen.getByText('QA release alpha').closest('.flow-toast__chip')).toHaveAttribute('data-static')
    expect(screen.queryByRole('button', { name: /QA release alpha/ })).toBeNull()
  })

  it('runs link actions, navigating app paths through the router bridge, then dismisses', async () => {
    const navigate = vi.fn()
    setToastNavigator(navigate)
    act(() => { toast.success('Project created', { action: { label: 'View project', href: '/ws/project/p1/overview' } }) })
    await settle(50)
    fireEvent.click(screen.getByRole('button', { name: 'View project' }))
    expect(navigate).toHaveBeenCalledWith('/ws/project/p1/overview')
    await settle(500)
    expect(items()).toHaveLength(0)
  })

  it('opens the newest toast link with the ⌘ ⌥ O shortcut', async () => {
    const older = vi.fn()
    const newer = vi.fn()
    act(() => {
      toast.success('Older', { action: { label: 'View older', onClick: older } })
      toast.success('Newer', { action: { label: 'View newer', onClick: newer } })
    })
    await settle(50)
    const wrongChord = fireEvent.keyDown(window, { key: 'o', code: 'KeyO', metaKey: true })
    expect(wrongChord).toBe(true)
    expect(newer).not.toHaveBeenCalled()
    const prevented = !fireEvent.keyDown(window, { key: 'ø', code: 'KeyO', metaKey: true, altKey: true })
    expect(prevented).toBe(true)
    expect(newer).toHaveBeenCalledTimes(1)
    expect(older).not.toHaveBeenCalled()
    await settle(500)
    expect(items()).toHaveLength(1)
    // With the newest gone, the shortcut falls through to the next link.
    fireEvent.keyDown(window, { key: 'ø', code: 'KeyO', metaKey: true, altKey: true })
    expect(older).toHaveBeenCalledTimes(1)
  })

  it('does not swallow the shortcut when no toast has a link', async () => {
    act(() => { toast.info('Plain') })
    await settle(50)
    const notPrevented = fireEvent.keyDown(window, { key: 'ø', code: 'KeyO', metaKey: true, altKey: true })
    expect(notPrevented).toBe(true)
  })

  it('uses an alert role for errors and the copy glyph for clipboard confirmations', async () => {
    act(() => { toast.error('Could not save'); toast.info('Copied issue link to clipboard') })
    await settle(50)
    expect(screen.getByRole('alert')).toHaveTextContent('Could not save')
    const copy = items()[0].querySelector('.flow-toast__icon svg')
    expect(copy?.querySelector('rect')).not.toBeNull()
  })

  it('waits for a loading toast to be resolved instead of timing out', async () => {
    act(() => { toast.loading('Working…') })
    await settle(TOAST_DURATION * 3)
    expect(items()).toHaveLength(1)
  })
})
