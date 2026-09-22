import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { EntityActivityPanel } from './entity-activity-panel'

describe('LS-0250 EntityActivityPanel', () => {
  it('renders shared activity chrome and empty state', () => {
    render(
      <EntityActivityPanel
        entityId="doc-1"
        entityTitle="Spec"
        entityType="document"
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByRole('complementary', { name: 'Activity' })).toBeTruthy()
    expect(screen.getByText('No activity yet')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Close Activity' })).toBeTruthy()
  })

  it('switches to Agent slot and back', async () => {
    const user = userEvent.setup()
    render(
      <EntityActivityPanel
        agentSlot={<div>Agent thread</div>}
        entityId="issue-1"
        entityTitle="FLOW-1"
        entityType="issue"
      />,
    )
    await user.click(screen.getByRole('button', { name: 'Agent chat' }))
    expect(screen.getByText('Agent thread')).toBeTruthy()
    await user.click(screen.getByRole('button', { name: 'Back to FLOW-1' }))
    expect(screen.getByRole('complementary', { name: 'Activity' })).toBeTruthy()
  })
})
