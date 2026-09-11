import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { asksPath, customersPath, loopsPath } from '@/lib/app-routes'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { AccountBootstrap, BootstrapData, WorkspaceSettings } from '@/types/flow'
import { sidebarEntryAvailable, workspaceFeatureEnabled, type SidebarOrder, type SidebarPreferences } from './sidebar-customization-state'
import { Sidebar, SidebarCustomization } from './sidebar'

const order: SidebarOrder = {
  personal: ['inbox', 'reviews', 'myIssues', 'pulse', 'drafts', 'agent'],
  workspace: ['members', 'initiatives', 'projects', 'teams', 'views', 'releases', 'loops', 'customers'],
}
const preferences = Object.fromEntries([...order.personal, ...order.workspace, 'documents'].map(id => [id, id === 'customers' ? 'never' : 'always'])) as SidebarPreferences

it('matches sidebar customization controls and visibility rules', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<I18nProvider><SidebarCustomization open onOpenChange={vi.fn()} preferences={preferences} order={order} badgeStyle="count" onBadgeStyleChange={vi.fn()} onChange={onChange} onReorder={vi.fn()}/></I18nProvider>)

  expect(screen.getByRole('dialog')).toHaveClass('sidebar-customize-dialog')
  expect(screen.getByRole('combobox', { name: 'Default badge style' })).toHaveTextContent('1Count')
  expect(screen.getByRole('button', { name: 'Inbox' }).querySelector('svg')).toHaveAttribute('viewBox', '0 0 6 10')

  const inboxSelect = within(screen.getByRole('button', { name: 'Inbox' })).getByRole('combobox')
  await user.click(inboxSelect)
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Always show', 'Show when badged'])
  await user.keyboard('{Escape}')

  const reviewsSelect = within(screen.getByRole('button', { name: 'Reviews' })).getByRole('combobox')
  await user.click(reviewsSelect)
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Always show', 'Show when badged', "Don't show"])
  await user.click(screen.getByRole('option', { name: "Don't show" }))
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ reviews: 'never' }))
})

const defaultVisibility: SidebarPreferences = {
  inbox: 'always', reviews: 'always', myIssues: 'always', pulse: 'always',
  drafts: 'always', agent: 'always', initiatives: 'always',
  projects: 'always', documents: 'always', views: 'always',
  members: 'always', customers: 'never', teams: 'always',
  releases: 'always', loops: 'always',
}

function workspaceSettings(flags: Record<string, boolean>): WorkspaceSettings {
  return { featureFlags: flags, featureSettings: {} } as WorkspaceSettings
}

function sidebarData(flags: Record<string, boolean>): BootstrapData {
  return makeBootstrap({
    viewerRole: 'member',
    favorites: [],
    favoriteFolders: [],
    notifications: [],
    reviews: [],
    drafts: [],
    cycles: [],
    subscriptions: [],
    teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member', joinedAt: '2026-08-01T00:00:00Z' }],
    teamSettings: {},
    cycleSettings: {},
    userSettings: {},
    workspaceSettings: workspaceSettings(flags),
  })
}

function accountFor(data: BootstrapData): AccountBootstrap {
  return {
    viewer: data.viewer,
    workspaces: [{ workspace: data.workspace, role: 'Admin', joinedAt: '2026-01-01T00:00:00Z', issueCount: 0 }],
    workspaceRegionSelectorEnabled: false,
    workspaceDefaultRegion: 'us',
  }
}

function sidebarTree(data: BootstrapData) {
  return (
    <MemoryRouter>
      <TooltipProvider>
        <I18nProvider>
          <Sidebar
            account={accountFor(data)}
            data={data}
            page="inbox"
            onSearch={vi.fn()}
            onCreate={vi.fn()}
            onOpenSettings={vi.fn()}
            onSwitchWorkspace={vi.fn()}
            onCreateWorkspace={vi.fn()}
            onLogout={vi.fn(async () => undefined)}
          />
        </I18nProvider>
      </TooltipProvider>
    </MemoryRouter>
  )
}

function renderSidebar(flags: Record<string, boolean>, locale: 'en-US' | 'zh-CN' = 'en-US') {
  localStorage.setItem('flow:locale', locale)
  return render(sidebarTree(sidebarData(flags)))
}

async function openMore() {
  const existing = document.querySelector('.sidebar-more-menu')
  if (existing) return existing as HTMLElement
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /Show more links|显示更多链接/ }))
  return waitFor(() => {
    const node = document.querySelector('.sidebar-more-menu')
    if (!node) throw new Error('More menu did not open')
    return node as HTMLElement
  })
}

function moreLink(menu: HTMLElement, href: string) {
  return menu.querySelector(`a[href="${href}"]`)
}

describe('sidebar feature flags', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('flow.sidebar.preferences', JSON.stringify({ ...defaultVisibility, loops: 'never', customers: 'never' }))
    const media = Object.assign(new EventTarget(), { matches: false, media: '(max-width: 1024px)' })
    vi.stubGlobal('matchMedia', () => media)
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it('maps Loops, Customers, and Asks to distinct flags', () => {
    const off = { loops: false, 'customer-requests': false, asks: false }
    expect(sidebarEntryAvailable('loops', off)).toBe(false)
    expect(sidebarEntryAvailable('customers', off)).toBe(false)
    expect(sidebarEntryAvailable('customers', { asks: false })).toBe(true)
    expect(sidebarEntryAvailable('loops', { 'customer-requests': false })).toBe(true)
    expect(workspaceFeatureEnabled({ asks: false }, 'asks')).toBe(false)
    expect(workspaceFeatureEnabled({ 'customer-requests': false }, 'asks')).toBe(true)
    expect(workspaceFeatureEnabled({ loops: false }, 'loops')).toBe(false)
  })

  it('hides Loops and Customers from More after flags are off, including after bootstrap refresh', async () => {
    const enabled = { loops: true, 'customer-requests': true, asks: true }
    const view = renderSidebar(enabled)
    let menu = await openMore()
    expect(moreLink(menu, loopsPath('workspace'))).toBeTruthy()
    expect(moreLink(menu, customersPath('workspace'))).toBeTruthy()
    expect(moreLink(menu, asksPath('workspace'))).toBeTruthy()

    view.rerender(sidebarTree(sidebarData({ loops: false, 'customer-requests': false, asks: true })))

    expect(screen.queryByRole('link', { name: 'Loops' })).not.toBeInTheDocument()
    menu = await openMore()
    expect(moreLink(menu, loopsPath('workspace'))).toBeNull()
    expect(moreLink(menu, customersPath('workspace'))).toBeNull()
    expect(moreLink(menu, asksPath('workspace'))).toBeTruthy()
    expect(menu).toHaveTextContent('Asks')
    expect(menu).not.toHaveTextContent('Customers')
    expect(menu).not.toHaveTextContent('Loops')
  })

  it('keeps Asks independent of Customer requests in English and Chinese', async () => {
    for (const [locale, asks, customers] of [
      ['en-US', 'Asks', 'Customers'],
      ['zh-CN', '请求', '客户'],
    ] as const) {
      localStorage.setItem('flow.sidebar.preferences', JSON.stringify({ ...defaultVisibility, loops: 'never', customers: 'never' }))
      const hidden = renderSidebar({ loops: false, 'customer-requests': false, asks: true }, locale)
      let menu = await openMore()
      expect(moreLink(menu, loopsPath('workspace'))).toBeNull()
      expect(moreLink(menu, customersPath('workspace'))).toBeNull()
      expect(moreLink(menu, asksPath('workspace'))).toHaveTextContent(asks)
      expect(menu).not.toHaveTextContent(customers)
      hidden.unmount()

      const shown = renderSidebar({ loops: true, 'customer-requests': true, asks: false }, locale)
      menu = await openMore()
      expect(moreLink(menu, customersPath('workspace'))).toHaveTextContent(customers)
      expect(moreLink(menu, asksPath('workspace'))).toBeNull()
      expect(menu).not.toHaveTextContent(asks)
      shown.unmount()
    }
  })

  it('does not keep Loops in primary nav or More when the flag is off', async () => {
    localStorage.setItem('flow.sidebar.preferences', JSON.stringify(defaultVisibility))
    renderSidebar({ loops: false, 'customer-requests': true, asks: true })
    expect(screen.queryByRole('link', { name: 'Loops' })).not.toBeInTheDocument()
    const menu = await openMore()
    expect(moreLink(menu, loopsPath('workspace'))).toBeNull()
  })
})
