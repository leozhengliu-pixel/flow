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
const preferences = Object.fromEntries([...order.personal, ...order.workspace].map(id => [id, id === 'customers' ? 'never' : 'always'])) as SidebarPreferences

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
  projects: 'always', views: 'always',
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

describe('sidebar without joined teams', () => {
  it('offers joining or creating beneath Your teams when both are allowed', () => {
    const data = sidebarData({})
    render(sidebarTree({ ...data, teamMembers: [], workspaceSettings: { ...data.workspaceSettings, teamCreatePermission: 'members' } as WorkspaceSettings }))
    const link = screen.getByRole('link', { name: 'Join or create a team' })
    expect(link).toHaveAttribute('href', `/${data.workspace.urlKey}/teams`)
    expect(link.closest('.sidebar-section-teams')).toHaveTextContent('Your teams')
  })

  it('offers only joining when team creation is restricted', () => {
    const data = sidebarData({})
    render(sidebarTree({ ...data, teamMembers: [], workspaceSettings: { ...data.workspaceSettings, teamCreatePermission: 'admins' } as WorkspaceSettings }))
    expect(screen.getAllByRole('link', { name: 'Join a team' })).toHaveLength(2)
    expect(screen.queryByText('Join or create a team')).not.toBeInTheDocument()
  })

  it('hides an empty team section when neither action is permitted', () => {
    const data = sidebarData({})
    const { container } = render(sidebarTree({ ...data, viewerRole: 'guest', teamMembers: [] }))
    expect(container.querySelector('.sidebar-section-teams')).toBeNull()
  })
})

function renderSidebar(flags: Record<string, boolean>, locale: 'en-US' | 'zh-CN' = 'en-US') {
  localStorage.setItem('flow:locale', locale)
  return render(sidebarTree(sidebarData(flags)))
}

it('keeps help without a paid-plan pill', () => {
  renderSidebar({})
  expect(screen.queryByRole('button', { name: 'Free plan' })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Open Help menu' })).toBeInTheDocument()
})

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

  it('uses filled team navigation and the standalone help glyph on phones', () => {
    const media = Object.assign(new EventTarget(), { matches: true, media: '(max-width: 767px)' })
    vi.stubGlobal('matchMedia', () => media)
    const { container } = renderSidebar({})
    const home = container.querySelector('.team-links a[href$="/overview"] .nav-icon svg')
    const issues = container.querySelector('.team-links a[href$="/all"] .nav-icon svg')
    expect(home?.querySelector('path')).toHaveAttribute('d', expect.stringContaining('M6.56055 1.52061'))
    expect(issues?.querySelectorAll('path')).toHaveLength(2)
    expect(issues?.querySelector('rect')).toBeNull()
    expect(container.querySelector('.team-disclosure path')).toHaveAttribute('d', expect.stringContaining('M6.75194 10.6239'))
    expect(container.querySelector('.sidebar-help use')).toHaveAttribute('href', '#QuestionMark')
  })

  it('offers a dismissible phone import entry without adding it to desktop navigation', async () => {
    const desktop = renderSidebar({})
    expect(desktop.container.querySelector('.sidebar-section-try a[href$="/settings/import-export"]')).toBeNull()
    desktop.unmount()
    const media = Object.assign(new EventTarget(), { matches: true, media: '(max-width: 767px)' })
    vi.stubGlobal('matchMedia', () => media)
    const phone = renderSidebar({})
    const entry = phone.container.querySelector('.sidebar-section-try a[href$="/settings/import-export"]')
    expect(entry).toHaveTextContent('Import issues')
    expect(entry?.querySelector('path')).toHaveAttribute('d', expect.stringContaining('M13.25 5.25'))
    await userEvent.setup().click(phone.container.querySelector('[aria-label="Dismiss Import issues"]')!)
    expect(phone.container.querySelector('.sidebar-section-try a[href$="/settings/import-export"]')).toBeNull()
    expect(JSON.parse(localStorage.getItem('flow.sidebar.dismissed-try')!)).toContain('import')
  })

  it.each([[false, false], [false, true], [true, false], [true, true]])('shows Asks=%s and Customers=%s independently after a settings refresh', async (asks, customers) => {
    const view = renderSidebar({ asks: !asks, 'customer-requests': !customers })
    await openMore()
    view.rerender(sidebarTree(sidebarData({ asks, 'customer-requests': customers })))
    const menu = await openMore()
    expect(Boolean(moreLink(menu, asksPath('workspace')))).toBe(asks)
    expect(Boolean(moreLink(menu, customersPath('workspace')))).toBe(customers)
  })

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

  it('gives sidebar links a tooltip beside the row with the go-to text and its key sequence', () => {
    renderSidebar({ loops: true })
    const inbox = document.querySelector('a.nav-item[href$="/inbox"]')!
    expect(inbox).toHaveAttribute('title', 'Go to inbox (G then I)')
    expect(inbox).toHaveAttribute('data-tooltip-side', 'right')
    expect(document.querySelector('a.nav-item[href$="/pulse/all"], a.nav-item[href*="/pulse"]')).toHaveAttribute('title', 'Go to pulse (G then F)')
    expect(screen.getByRole('link', { name: 'Home' })).toHaveAttribute('title', expect.stringMatching(/ \(Ctrl ⇧ 1\)$/))
    expect(screen.getByRole('button', { name: 'Open Help menu' })).not.toHaveAttribute('title')
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

  it('lists the hidden entries then Customize sidebar, all drawn with filled glyphs', async () => {
    localStorage.setItem('flow.sidebar.preferences', JSON.stringify({ ...defaultVisibility, members: 'never', customers: 'never', teams: 'never', releases: 'never' }))
    renderSidebar({ 'customer-requests': true, asks: true, releases: true })
    const menu = await openMore()
    const rows = [...menu.querySelectorAll('[role="menuitem"]')]
    expect(rows.map(row => row.textContent)).toEqual(['Members', 'Customers', 'Releases', 'Teams', 'Asks', 'Customize sidebar'])
    // One glyph family: no stroked icon libraries, every glyph is a 16px viewBox that fills with the row colour.
    for (const row of rows) {
      const glyph = row.querySelector(':scope > svg')!
      expect(glyph, row.textContent ?? '').not.toHaveClass('lucide')
      expect(glyph.getAttribute('viewBox')).toBe('0 0 16 16')
      expect(glyph.querySelector('[stroke]')).toBeNull()
    }
    expect(menu.querySelectorAll('[role="separator"]')).toHaveLength(1)
    expect(rows.at(-1)!.querySelector('svg')).toHaveClass('sidebar-more-customize-icon')
  })

  it('does not keep Loops in primary nav or More when the flag is off', async () => {
    localStorage.setItem('flow.sidebar.preferences', JSON.stringify(defaultVisibility))
    renderSidebar({ loops: false, 'customer-requests': true, asks: true })
    expect(screen.queryByRole('link', { name: 'Loops' })).not.toBeInTheDocument()
    const menu = await openMore()
    expect(moreLink(menu, loopsPath('workspace'))).toBeNull()
  })
})
