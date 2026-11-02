import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeIssue, teammate, viewer } from '@/test/fixtures'
import { IssueSubscriberPicker } from './issue-subscriber-picker'

describe('issue subscriber picker', () => {
  it('uses the supplied avatar in the subscription trigger and picker', async () => {
    const user = userEvent.setup()
    const member = { ...viewer, avatarUrl: '/uploads/member-avatar.png' }
    const issue = makeIssue({ subscriberIds: [member.id] })
    render(<I18nProvider><IssueSubscriberPicker issue={issue} users={[member]} onToggle={vi.fn()}/></I18nProvider>)
    const trigger = screen.getByRole('button', { name: 'Change subscribers' })
    expect(trigger.querySelector('img')).toHaveAttribute('src', member.avatarUrl)
    await user.click(trigger)
    expect((await screen.findByRole('option', { name: /Viewer/ })).querySelector('img')).toHaveAttribute('src', member.avatarUrl)
  })
  it('orders selected users, filters options, and keeps the multi-select open', async () => {
    const user = userEvent.setup()
    const onToggle = vi.fn()
    const issue = makeIssue({ subscriberIds: [viewer.id], assignee: viewer })
    render(<I18nProvider><IssueSubscriberPicker issue={issue} users={[teammate, viewer]} onToggle={onToggle}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Change subscribers' }))
    const selected = await screen.findByRole('option', { name: /Viewer/ })
    expect(selected).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText('Assignee')).toBeVisible()
    const search = screen.getByRole('searchbox', { name: 'Change subscribers…' })
    await user.type(search, 'team')
    expect(screen.queryByRole('option', { name: /Viewer/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: /Teammate/ }))
    expect(onToggle).toHaveBeenCalledWith(teammate.id)
    expect(screen.getByRole('dialog', { name: 'Change subscribers' })).toBeVisible()
    await user.clear(search)
    await user.type(search, 'missing')
    expect(screen.getByText('No results')).toBeVisible()
  })
})
