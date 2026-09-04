import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { AssigneeHoverPreview } from './issue-property-hover'

const user = {
  id: 'user-1',
  name: 'leo.zheng.liu',
  displayName: 'zheng liu',
  email: 'leo@example.com',
  active: true,
  emailVerified: true,
}

describe('AssigneeHoverPreview presence status', () => {
  it('defaults active users to offline until explicit presence is supplied', () => {
    render(<AssigneeHoverPreview user={user} workspaceName="Workspace" />)

    const details = screen.getByText('Offline').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Offline')).toBeVisible()
    expect(within(details!).queryByText('Online')).not.toBeInTheDocument()
  })

  it('shows online only for an explicit live presence flag', () => {
    render(<AssigneeHoverPreview online user={user} workspaceName="Workspace" />)

    const details = screen.getByText('Online').closest('.assignee-hover-preview__details') as HTMLElement | null
    expect(details).not.toBeNull()
    expect(within(details!).getByText('Online')).toBeVisible()
    expect(within(details!).queryByText('Offline')).not.toBeInTheDocument()
  })

  it('keeps suspended users offline even when presence is stale', () => {
    render(<AssigneeHoverPreview online member={{ user, role: 'member', status: 'suspended', joinedAt: '2026-01-01T00:00:00Z' }} user={user} workspaceName="Workspace" />)

    expect(screen.getByText('Offline')).toBeVisible()
    expect(screen.queryByText('Online')).not.toBeInTheDocument()
  })
})
