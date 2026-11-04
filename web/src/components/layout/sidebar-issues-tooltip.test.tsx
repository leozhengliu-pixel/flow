import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SidebarIssuesTooltip } from './sidebar-issues-tooltip'

describe('sidebar issue shortcuts', () => {
  it('exposes the three shortcuts on focus and dismisses with Escape without navigating', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><TooltipProvider><SidebarIssuesTooltip><a href="/acme/team/ENG/all">Issues</a></SidebarIssuesTooltip></TooltipProvider></I18nProvider>)
    await user.tab()
    const tooltip = await screen.findByRole('tooltip')
    expect(tooltip).toHaveTextContent('All issuesGthenEActiveGthenABacklogGthenB')
    expect(screen.getByRole('link')).toHaveFocus()
    expect(tooltip.querySelector('a,button')).toBeNull()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    expect(screen.getByRole('link')).toHaveFocus()
  })
})
