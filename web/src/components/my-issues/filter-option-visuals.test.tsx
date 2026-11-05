import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { avatarColor } from '@/lib/avatar-color'
import { BUILTIN_AGENT_AVATAR_URL } from '@/components/ui/agent-avatar-url'
import { explorerFilterOptions, explorerPropertyOptions } from '@/components/issue-explorer/issue-explorer-model'
import { makeBootstrap } from '@/test/fixtures'
import { ProjectsFilterBar } from '@/components/projects-page/projects-filter-bar'
import { ProjectsInsightsSidebar } from '@/components/projects-page/projects-insights-sidebar'
import { ProjectsBulkActionBar } from '@/components/projects-page/projects-bulk-action-bar'
import { MyIssuesBulkActionBar } from './my-issues-bulk-action-bar'
import type { ProjectPageItem } from '@/components/projects-page/projects-data-view'
import type { MyIssuesRowData } from './my-issues-list'
import { filterChipItem } from './my-issues-filter-chips'
import { OptionMark } from './my-issues-filter-menu'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })

describe('filter option identity visuals', () => {
  it('renders uploaded images and falls back to the same person after an image error', () => {
    const { container } = render(<OptionMark field="assignee" option={{ id: 'person-a', label: 'Ada Lovelace', avatarUrl: '/uploads/ada.png' }}/>)
    const avatar = container.querySelector('[data-user-avatar]')!
    expect(avatar.querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
    expect(avatar).not.toHaveTextContent('AL')
    fireEvent.error(avatar.querySelector('img')!)
    expect(avatar).toHaveTextContent('AL')
    expect((avatar as HTMLElement).style.getPropertyValue('--avatar')).toBe(avatarColor('person-a'))
  })

  it.each(['assignee', 'creator', 'owner', 'subscribers'] as const)('uses shared Latin and CJK initials and per-person colours for %s', field => {
    const { container } = render(<><OptionMark field={field} option={{ id: 'person-a', label: 'Ada Lovelace' }}/><OptionMark field={field} option={{ id: 'person-b', label: '林舟' }}/></>)
    const avatars = container.querySelectorAll<HTMLElement>('[data-user-avatar]')
    expect(avatars[0]).toHaveTextContent('AL')
    expect(avatars[1]).toHaveTextContent('林')
    expect(avatars[1]).not.toHaveTextContent('舟')
    expect(avatars[0].style.getPropertyValue('--avatar')).toBe(avatarColor('person-a'))
    expect(avatars[1].style.getPropertyValue('--avatar')).toBe(avatarColor('person-b'))
    expect(avatars[0].style.getPropertyValue('--avatar')).not.toBe(avatars[1].style.getPropertyValue('--avatar'))
  })

  it.each([
    ['projectProperties', 'projectLead', 'project-lead:'],
    ['customers', 'customerOwner', 'customer-owner:'],
    ['triageIntelligence', 'assignee', 'assignee:'],
  ] as const)('keys %s people by their real user IDs', (field, kind, prefix) => {
    const { container } = render(<OptionMark field={field} option={{ id: `${prefix}person-a`, label: 'Ada Lovelace', kind }}/>)
    expect(container.querySelector<HTMLElement>('[data-user-avatar]')!.style.getPropertyValue('--avatar')).toBe(avatarColor('person-a'))
  })

  it('keeps the built-in agent tile', () => {
    const { container } = render(<OptionMark field="agent" option={{ id: 'agent-a', label: 'Flow', avatarUrl: BUILTIN_AGENT_AVATAR_URL }}/>)
    expect(container.querySelector('[data-agent-avatar-slot]')).not.toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('[data-agent-avatar-slot] svg')).not.toBeNull()
  })

  it('carries configured team visuals through root, suggested-team and applied-filter options', () => {
    const data = makeBootstrap()
    data.teams = [{ ...data.teams[0], icon: 'Favorite', color: '#14b8a6' }]
    const options = explorerPropertyOptions(data)
    const team = explorerFilterOptions('team', options)![0]
    const suggestion = explorerFilterOptions('triageIntelligence', options)!.find(option => option.id === 'triage-team')!.children![1]
    const chip = filterChipItem({ id: 'team-filter', field: 'team', fieldLabel: 'Team', operator: 'is', value: team.id, valueLabel: team.label }, [team])
    const { container } = render(<><OptionMark field="team" option={team}/><OptionMark field="triageIntelligence" option={suggestion}/>{chip.values[0].icon}</>)
    const icons = container.querySelectorAll('svg')
    expect(icons).toHaveLength(3)
    for (const icon of icons) {
      expect(icon.querySelector('use')).toHaveAttribute('href', '#Favorite')
      expect(icon).toHaveStyle({ color: '#14b8a6', width: '14px', height: '14px' })
    }
  })

  it('rehydrates saved project lead/member chips and their value menus with live person avatars', async () => {
    render(<I18nProvider><ProjectsFilterBar filters={[{ id: 'lead', field: 'lead', fieldLabel: 'Lead', operator: 'is', values: [{ id: 'person-a', label: 'Ada Lovelace' }] }, { id: 'member', field: 'members', fieldLabel: 'Members', operator: 'is', values: [{ id: 'person-b', label: '林舟' }] }]} options={{ lead: [{ id: 'person-a', label: 'Ada Lovelace', avatarUrl: '/uploads/ada.png' }], members: [{ id: 'person-b', label: '林舟' }] }} onAdd={vi.fn()} onChange={vi.fn()} onClear={vi.fn()} onRemove={vi.fn()}/></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Lead values' }).querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
    expect(screen.getByRole('button', { name: 'Members values' }).querySelector('[data-user-avatar]')).toHaveTextContent('林')
    await userEvent.click(screen.getByRole('button', { name: 'Lead values' }))
    expect(within(screen.getByRole('listbox')).getByRole('option').querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
  })

  it('uses person avatars in project insight lead filters', () => {
    const projects = [{ id: 'one', lead: { id: 'person-a', name: 'Ada Lovelace', avatarUrl: '/uploads/ada.png' } }, { id: 'two', lead: { id: 'person-b', name: '林舟' } }] as ProjectPageItem[]
    const { container } = render(<ProjectsInsightsSidebar activeFilter={null} mode="leads" projects={projects} onChangeFilter={vi.fn()} onChangeMode={vi.fn()}/>)
    expect(container.querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
    expect(container.querySelector('[aria-label="林舟"]')).toHaveTextContent('林')
  })

  it('keeps individual avatars when a project chip summarizes several people', () => {
    render(<I18nProvider><ProjectsFilterBar filters={[{ id: 'members', field: 'members', fieldLabel: 'Members', operator: 'is', values: [{ id: 'person-a', label: 'Ada Lovelace' }, { id: 'person-b', label: '林舟' }] }]} options={{ members: [{ id: 'person-a', label: 'Ada Lovelace', avatarUrl: '/uploads/ada.png' }, { id: 'person-b', label: '林舟' }] }} onAdd={vi.fn()} onChange={vi.fn()} onClear={vi.fn()} onRemove={vi.fn()}/></I18nProvider>)
    const chip = screen.getByRole('button', { name: 'Members values' })
    expect(chip.querySelectorAll('[data-user-avatar]')).toHaveLength(2)
    expect(chip.querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
    expect(chip.querySelector('[aria-label="林舟"]')).toHaveTextContent('林')
  })

  it('keeps images in project lead bulk choices', async () => {
    render(<I18nProvider><ProjectsBulkActionBar projects={[{ id: 'one', name: 'Project' } as ProjectPageItem]} options={action => action === 'lead' ? [{ id: 'person-a', label: 'Ada Lovelace', avatarUrl: '/uploads/ada.png' }] : undefined} onAction={vi.fn()} onAsk={vi.fn()} onClear={vi.fn()}/></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Open project command menu' }))
    await userEvent.click(screen.getByRole('option', { name: /Set project lead/ }))
    expect(screen.getByRole('option', { name: /Ada Lovelace/ }).querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
  })

  it('shows person avatars in subscriber bulk choices without depending on a directory lookup', async () => {
    render(<I18nProvider><MyIssuesBulkActionBar selectedIssues={[{ id: 'one', title: 'Issue', identifier: 'TST-1' } as MyIssuesRowData]} actionOptions={action => action === 'subscribers' ? [{ id: 'person-a', label: 'Ada Lovelace', avatarUrl: '/uploads/ada.png' }] : undefined} onAction={vi.fn()} onClear={vi.fn()}/></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Open command menu' }))
    await userEvent.click(screen.getByRole('option', { name: /Change subscribers/ }))
    expect(screen.getByRole('option', { name: /Ada Lovelace/ }).querySelector('img')).toHaveAttribute('src', '/uploads/ada.png')
  })
})
