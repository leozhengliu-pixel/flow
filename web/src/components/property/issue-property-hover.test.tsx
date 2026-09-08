import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { AssigneeHoverPreview } from './issue-property-hover'
import { I18nProvider } from '@/i18n/i18n'

const user = {
  id: 'user-1',
  name: 'leo.zheng.liu',
  displayName: 'zheng liu',
  email: 'leo@example.com',
  active: true,
  emailVerified: true,
}

describe('AssigneeHoverPreview presence status', () => {
  it('omits unknown presence and timezone information', () => {
    render(<I18nProvider><AssigneeHoverPreview user={user} workspaceName="Workspace" /></I18nProvider>)

    expect(screen.queryByText('Offline')).not.toBeInTheDocument()
    expect(screen.queryByText('Online')).not.toBeInTheDocument()
    expect(screen.queryByText('local time')).not.toBeInTheDocument()
  })

  it('shows online only for an explicit live presence flag', () => {
    render(<I18nProvider><AssigneeHoverPreview online user={user} workspaceName="Workspace" /></I18nProvider>)

    const details = screen.getByText('Online').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Online')).toBeVisible()
    expect(within(details!).queryByText('Offline')).not.toBeInTheDocument()
  })

  it('shows suspension instead of stale presence', () => {
    render(<I18nProvider><AssigneeHoverPreview online member={{ user, role: 'member', status: 'suspended', joinedAt: '2026-01-01T00:00:00Z' }} user={user} workspaceName="Workspace" /></I18nProvider>)

    expect(screen.getByText('Suspended')).toBeVisible()
    expect(screen.queryByText('Online')).not.toBeInTheDocument()
  })
})
