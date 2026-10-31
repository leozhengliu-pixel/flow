import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { DesktopIntegrations } from './desktop-integrations'

function mount(connected = true) {
  const onOpen = vi.fn()
  render(<I18nProvider><DesktopIntegrations data={makeBootstrap({ integrationConnections: connected ? [{ id: 'gh', provider: 'github', status: 'connected' }, { id: 'chat', provider: 'lark', status: 'pending_authorization' }, { id: 'old', provider: 'codex', status: 'connected' }] as never[] : [] })} onOpen={onOpen}/></I18nProvider>)
  return onOpen
}

describe('desktop integrations discovery', () => {
  it('separates installed services, preserves chat configuration, and excludes unsupported services', async () => {
    const open = mount()
    const enabled = screen.getByRole('heading', { name: 'Enabled' }).closest('section')!
    expect(within(enabled).getByRole('button', { name: 'GitHub Enabled' })).toBeVisible()
    await userEvent.click(within(enabled).getByRole('button', { name: 'Lark Configured' }))
    expect(open).toHaveBeenLastCalledWith('lark')
    expect(screen.queryByText('Codex')).not.toBeInTheDocument()
    expect(screen.queryByText('Figma')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Enabled' }))
    expect(open).toHaveBeenLastCalledWith('enabled')
  })

  it('switches featured services in both directions, opens details, and searches the full working catalog', async () => {
    const open = mount(false)
    expect(screen.getByText('No enabled integrations')).toBeVisible()
    const tabs = screen.getByRole('tablist')
    await userEvent.click(within(tabs).getByRole('tab', { name: 'Feishu' }))
    expect(screen.getByRole('tab', { name: 'Feishu' })).toHaveAttribute('aria-selected', 'true')
    await userEvent.click(screen.getByRole('button', { name: /Essentials Feishu/ }))
    expect(open).toHaveBeenLastCalledWith('feishu')
    await userEvent.click(screen.getByRole('button', { name: 'Next integration' }))
    expect(screen.getByRole('tab', { name: 'Lark' })).toHaveAttribute('aria-selected', 'true')
    await userEvent.click(screen.getByRole('button', { name: 'Previous integration' }))
    expect(screen.getByRole('tab', { name: 'Feishu' })).toHaveAttribute('aria-selected', 'true')
    await userEvent.type(screen.getByRole('textbox', { name: 'Search integrations' }), 'gitlab')
    expect(screen.getByRole('button', { name: 'GitLab Engineering' })).toBeVisible()
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
    expect(screen.queryByText('Slack')).not.toBeInTheDocument()
  })
})
