import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { clearFeatureFlagOverrides } from '@/lib/feature-flag-registry'
import { FeatureFlagDrawer } from './feature-flag-drawer'

afterEach(() => {
  clearFeatureFlagOverrides()
  localStorage.clear()
})

describe('FeatureFlagDrawer (LS-0263)', () => {
  it('lists flags and supports override clear', async () => {
    const user = userEvent.setup()
    render(
      <FeatureFlagDrawer
        open
        forceEnable
        onOpenChange={() => undefined}
        workspaceFlags={{ loops: true }}
      />,
    )
    expect(screen.getByRole('dialog', { name: 'Feature flags' })).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Search feature flags…')).toBeInTheDocument()
    const overrideButtons = screen.getAllByRole('button', { name: 'Override off' })
    await user.click(overrideButtons[0]!)
    expect(screen.getByRole('button', { name: 'Clear override' })).toBeInTheDocument()
  })
})
