import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { IssueOptionsMenu, type IssueOptionsActions } from '@/components/issue/issue-options-menu'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { ADD_CUSTOMER_REQUEST_EVENT } from './customer-request-events'

function renderMenu() {
  const actions = { addLink: vi.fn(), addCustomerRequest: vi.fn(), addDocument: vi.fn(), linkReview: vi.fn(), unlinkReview: vi.fn(), toggleRelease: vi.fn(), createRelated: vi.fn(), convert: vi.fn(), setRecurring: vi.fn(), toggleFavorite: vi.fn(), remind: vi.fn(), runLoop: vi.fn(), restoreDescription: vi.fn() } as unknown as IssueOptionsActions
  const issue = makeIssue()
  render(<I18nProvider><TooltipProvider><IssueOptionsMenu issue={issue} data={makeBootstrap({ customers: [], reviews: [], releases: [] })} actions={actions} onRelation={vi.fn()} onUpdate={vi.fn()} onDelete={vi.fn()}/></TooltipProvider></I18nProvider>)
  return issue
}

describe('"Add customer request…" in the issue menu', () => {
  beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
  afterEach(() => vi.unstubAllGlobals())

  it('asks the issue page to start a request (Linear’s "Select customer…" step)', async () => {
    const user = userEvent.setup()
    const issue = renderMenu()
    const seen: string[] = []
    const listener = (event: Event) => { seen.push((event as CustomEvent<{ issueId: string }>).detail.issueId); event.preventDefault() }
    window.addEventListener(ADD_CUSTOMER_REQUEST_EVENT, listener)
    await user.click(screen.getByRole('button', { name: 'Issue options' }))
    await user.click(await screen.findByRole('option', { name: /Add customer request…/ }))
    window.removeEventListener(ADD_CUSTOMER_REQUEST_EVENT, listener)
    expect(seen).toEqual([issue.id])
    expect(document.querySelector('.customer-request-create-dialog')).toBeNull()
  })

  it('opens the composer in a dialog where the issue page is not open', async () => {
    const user = userEvent.setup()
    renderMenu()
    await user.click(screen.getByRole('button', { name: 'Issue options' }))
    await user.click(await screen.findByRole('option', { name: /Add customer request…/ }))
    await waitFor(() => expect(document.querySelector('.customer-request-create-dialog')).not.toBeNull())
    expect(screen.getByRole('textbox', { name: 'Request' })).toBeInTheDocument()
  })
})
