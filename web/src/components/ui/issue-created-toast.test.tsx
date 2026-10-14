import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { OPEN_FLOATING_AGENT_EVENT } from '@/components/agent/agent-floating-request'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import fs from 'node:fs'

const sonner = vi.hoisted(() => ({ custom: vi.fn(), dismiss: vi.fn() }))
vi.mock('sonner', () => ({ toast: sonner }))
const favorites = vi.hoisted(() => ({ toggleFavoriteFor: vi.fn(() => Promise.resolve()) }))
vi.mock('@/lib/favorites', () => favorites)
const notices = vi.hoisted(() => Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast: notices }))
const navigation = vi.hoisted(() => ({ openToastHref: vi.fn() }))
vi.mock('@/lib/toast-registry', () => navigation)

import { ISSUE_CREATED_IN_PLACE_TOAST_DURATION, toastIssueCreatedInPlace } from '@/lib/created-toasts'
import { IssueCreatedToast } from './issue-created-toast'

const css = fs.readFileSync('src/components/ui/issue-created-toast.css', 'utf8')
const tokens = fs.readFileSync('src/styles/tokens.css', 'utf8')

describe('"Issue created" from a selection (the reference app\'s in-place toast)', () => {
  const data = makeBootstrap()
  const issue = data.issues[0]
  const href = `/${data.workspace.urlKey}/issue/${issue.identifier}`
  const show = () => render(<I18nProvider><IssueCreatedToast data={data} href={href} id="toast-1" issue={issue}/></I18nProvider>)

  beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale', 'en-US') })

  it('stays 6s and shows the issue with Ask Flow and the copy / favorite buttons, without a leading icon', () => {
    toastIssueCreatedInPlace(issue, href, data)
    expect(sonner.custom).toHaveBeenCalledWith(expect.any(Function), expect.objectContaining({ duration: 6000 }))
    expect(ISSUE_CREATED_IN_PLACE_TOAST_DURATION).toBe(6000)
    render(<I18nProvider>{(sonner.custom.mock.calls[0][0] as (id: string) => ReactElement)('toast-1')}</I18nProvider>)
    const card = screen.getByRole('status')
    expect(card).toHaveTextContent('Issue created')
    expect(card.querySelector('.flow-toast__icon')).toBeNull()
    expect(screen.getByRole('link', { name: new RegExp(`${issue.identifier}.*${issue.title}`) })).toHaveAttribute('href', href)
    for (const name of ['Ask Flow', 'Copy issue URL', 'Favorite issue', 'Copy as prompt', 'Copy issue ID', 'Dismiss']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
  })

  it('"Ask Flow" opens the floating chat with the issue mentioned and dismisses the toast', () => {
    show()
    const requests: unknown[] = []
    const listener = (event: Event) => requests.push((event as CustomEvent).detail)
    window.addEventListener(OPEN_FLOATING_AGENT_EVENT, listener)
    fireEvent.click(screen.getByRole('button', { name: 'Ask Flow' }))
    window.removeEventListener(OPEN_FLOATING_AGENT_EVENT, listener)
    expect(requests).toEqual([{ mentions: [{ type: 'issue', id: issue.id, label: issue.identifier }] }])
    expect(sonner.dismiss).toHaveBeenCalledWith('toast-1')
  })

  it('opens the issue from its row, copies its URL and ID, and favorites it', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    show()
    fireEvent.click(screen.getByRole('button', { name: 'Copy issue ID' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(issue.identifier))
    // The reference app's clipboard notice (the copy icon), not a success check.
    expect(notices).toHaveBeenCalledWith('Issue ID copied to clipboard', { icon: expect.anything() })
    fireEvent.click(screen.getByRole('button', { name: 'Copy issue URL' }))
    await waitFor(() => expect(writeText).toHaveBeenLastCalledWith(new URL(href, window.location.origin).href))
    // The reference app keeps the button's name and shows a filled star; the tooltip turns to "Unfavorite issue".
    fireEvent.click(screen.getByRole('button', { name: 'Favorite issue' }))
    expect(favorites.toggleFavoriteFor).toHaveBeenCalledWith(data, 'issue', issue.id, true, false)
    expect(screen.getByRole('button', { name: 'Favorite issue' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByRole('link', { name: new RegExp(issue.title) }))
    expect(navigation.openToastHref).toHaveBeenCalledWith(href)
    expect(sonner.dismiss).toHaveBeenCalledWith('toast-1')
  })

  it('"Copy as prompt" copies the issue header\'s prompt and moves the issue to started like it (not with Option)', async () => {
    const writeText = vi.fn((_text: string) => Promise.resolve())
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const startedState = { ...data.states.find(state => state.type === 'started')!, teamId: undefined }
    const states = data.states.map(state => state.id === startedState.id ? startedState : state)
    const backlogIssue = { ...issue, state: states.find(state => state.type === 'backlog')! }
    const settings = { ...data, userSettings: { [data.viewer.id]: { codingToolMoveStarted: true } }, states, issues: [backlogIssue] } as typeof data
    const onUpdate = vi.fn(() => Promise.resolve())
    render(<I18nProvider><IssueCreatedToast data={settings} href={href} id="toast-1" issue={backlogIssue} onUpdate={onUpdate}/></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'Copy as prompt' }), { altKey: true })
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    expect(writeText.mock.calls[0][0]).toMatch(new RegExp(`^Work on Flow issue ${issue.identifier}:\\n\\nSuggested branch name: `))
    expect(onUpdate).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Copy as prompt' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalledWith({ stateId: startedState.id }))
    await waitFor(() => expect(notices).toHaveBeenCalledWith('Prompt copied to clipboard', { icon: expect.anything() }))
  })

  it('pins the measured look in CSS: hairline footer, 24px "Ask" pill, 28px icon buttons 7px apart, both themes', () => {
    // Glyphs (the favorited star included) are the muted grey and brighten to the title colour under the pointer.
    expect(css).toMatch(/\.flow-issue-toast__icon \{[^}]*color: var\(--toast-issue-identifier\);/)
    expect(css).toMatch(/\.flow-issue-toast__icon:hover \{ background: var\(--toast-issue-icon-hover-bg\); color: var\(--toast-issue-title\); \}/)
    expect(css).not.toMatch(/issue-header-favorite/)
    expect(tokens).toMatch(/--toast-issue-icon-hover-bg: lch\(21\.206% 1\.043 272\);/)
    expect(tokens).toMatch(/--toast-issue-icon-hover-bg: lch\(96\.914% 0 282\);/)
    expect(tokens).toMatch(/--toast-issue-shadow: 0 0 0 \.5px lch\(100 0 0 \/ \.148\)/)
    expect(tokens).toMatch(/--toast-issue-rule: lch\(91\.9% 0 282\)/)
    expect(tokens).toMatch(/--agent-quote-highlight: lch\(61\.803% 1\.2 272 \/ \.1\)/)
    expect(tokens).toMatch(/--agent-quote-highlight: lch\(39\.176% 1\.25 282 \/ \.1\)/)
    expect(css).toMatch(/\.flow-issue-toast::after \{ box-shadow: var\(--toast-issue-shadow\); \}/)
    expect(css).toMatch(/\.flow-issue-toast__actions \{[^}]*padding-top: 8px;[^}]*border-top: \.5px solid var\(--toast-issue-rule\)/)
    expect(css).toMatch(/\.flow-issue-toast__ask \{[^}]*height: 24px;[^}]*padding: 0 8px 0 6px;/)
    expect(css).toMatch(/\.flow-issue-toast__icons \{[^}]*gap: 7px;/)
    expect(css).toMatch(/\.flow-issue-toast__icon \{[^}]*width: 28px;[^}]*height: 28px;/)
  })
})
