import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { User } from '@/types/flow'
import { AssigneePicker } from '@/components/issue/core-property-pickers'
import { SettingsSelect } from '@/components/settings/settings-primitives'
import { PeopleProvider } from './people-provider'
import { PropertyMenu } from './property-menu'
import { PeopleMenuItems } from './people-menu-items'
import { PersonInfo } from './person-info'
import { MentionMenu } from '@/components/issue/editor/mention-menu'
import { makeBootstrap, project } from '@/test/fixtures'
import { personSearchText } from '@/lib/people'
import { MyIssuesBulkActionBar } from '@/components/my-issues/my-issues-bulk-action-bar'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'

const users = [
  { id: 'usr-one', userId: 'EMP-1001', displayName: '张伟', name: '张伟', email: '', active: true, emailVerified: false },
  { id: 'usr-two', userId: 'EMP-1002', displayName: '张伟', name: '张伟', email: 'second@example.test', active: true, emailVerified: true },
] as User[]

function Shell({ children }: { children: React.ReactNode }) { return <I18nProvider><PeopleProvider users={users} workspaceName="Enterprise">{children}</PeopleProvider></I18nProvider> }

describe('enterprise people pickers', () => {
  beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
  afterEach(() => vi.unstubAllGlobals())

  it('shows a hover profile by default and finds a same-name assignee by employee ID', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Shell><AssigneePicker users={users} value={users[0]} onChange={onChange}/></Shell>)
    await user.click(screen.getByRole('combobox'))
    await user.hover(screen.getAllByRole('option', { name: '张伟' })[0])
    const card = await screen.findByRole('tooltip')
    expect(card).toHaveTextContent('EMP-1001')
    expect(card).not.toHaveTextContent('usr-one')
    expect(card).not.toHaveTextContent('Internal ID')
    expect(within(card).queryByText('Email')).not.toBeInTheDocument()
    await user.type(screen.getByRole('textbox', { name: 'Change assignee…' }), 'EMP-1001')
    expect(screen.getAllByRole('option', { name: '张伟' })).toHaveLength(1)
    await user.clear(screen.getByRole('textbox', { name: 'Change assignee…' }))
    await user.type(screen.getByRole('textbox', { name: 'Change assignee…' }), 'emp-1002')
    expect(screen.getAllByRole('option', { name: '张伟' })).toHaveLength(1)
    await user.keyboard('{Enter}')
    expect(onChange).toHaveBeenCalledWith('usr-two')
  })

  it('enriches a generic project lead picker even when its options only carry id and name', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Shell><PropertyMenu label="Lead" options={users.map(person => ({ id: person.id, label: person.displayName }))} onChange={onChange}/></Shell>)
    await user.click(screen.getByRole('combobox'))
    const input = screen.getByRole('textbox')
    await user.type(input, 'EMP-1002')
    await user.hover(screen.getByRole('option', { name: '张伟' }))
    const card = await screen.findByRole('tooltip')
    expect(card).toHaveTextContent('EMP-1002')
    expect(card).toHaveTextContent('second@example.test')
    await user.clear(input)
    await user.type(input, 'usr-one')
    await user.keyboard('{Enter}')
    expect(onChange).toHaveBeenCalledWith('usr-one')
  })

  it('supports ID lookup in owner menus and keeps duplicate names independently selectable', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    render(<Shell><DropdownMenu.Root defaultOpen><DropdownMenu.Trigger>Owner</DropdownMenu.Trigger><DropdownMenu.Content><PeopleMenuItems users={users} onSelect={onSelect}/></DropdownMenu.Content></DropdownMenu.Root></Shell>)
    await user.type(screen.getByRole('textbox', { name: 'Search people' }), 'EMP-1002')
    await user.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledWith('usr-two')
  })

  it('uses the same searchable profile picker for settings and preserves disabled controls', async () => {
    const user = userEvent.setup()
    const options = users.map(person => ({ value: person.id, label: person.displayName }))
    const { rerender } = render(<Shell><SettingsSelect label="Assignee" options={options} value="usr-one" onChange={vi.fn()}/></Shell>)
    await user.click(screen.getByRole('combobox'))
    await user.type(screen.getByRole('textbox'), 'EMP-1002')
    expect(screen.getAllByRole('option', { name: '张伟' })).toHaveLength(1)
    rerender(<Shell><SettingsSelect disabled label="Assignee" options={options} value="usr-one" onChange={vi.fn()}/></Shell>)
    expect(screen.getByRole('combobox')).toBeDisabled()
  })

  it('includes both identifiers in search without deriving an email', () => {
    expect(personSearchText(users[0])).toContain('usr-one EMP-1001')
    expect(personSearchText(users[0])).not.toContain('@')
  })

  it('shows email without an internal-ID fallback for users without employee IDs', () => {
    const { container } = render(<I18nProvider><PersonInfo person={{ id:'usr-internal', displayName:'Alex', email:'alex@example.test' }}/></I18nProvider>)
    expect(screen.getByText('alex@example.test')).toBeVisible()
    expect(container).not.toHaveTextContent('usr-internal')
    expect(screen.queryByText('User ID')).not.toBeInTheDocument()
  })

  it('hides internal IDs supplied as business IDs and omits empty identity details', () => {
    const { container } = render(<I18nProvider><PersonInfo person={{id:'usr-internal',userId:'usr-internal',name:'usr-internal',displayName:'usr-internal'}}/></I18nProvider>)
    expect(screen.getByText('Unknown user')).toBeVisible()
    expect(container).not.toHaveTextContent('usr-internal')
    expect(container.querySelector('.person-identity-details')).not.toBeInTheDocument()
  })

  it('keeps employee IDs in mention suggestions and omits internal IDs and dangling separators', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const emailUser = {...users[1],userId:undefined}
    render(<Shell><MentionMenu users={[users[0],emailUser]} selectedIndex={0} position={{left:0,top:0}} query="" onSelect={onSelect}/></Shell>)
    const options = screen.getAllByRole('option')
    expect(options[0]).toHaveTextContent('EMP-1001')
    expect(options[0]).not.toHaveTextContent('usr-one')
    expect(options[1].querySelector('small')).toHaveTextContent(/^second@example\.test$/)
    expect(options[1]).not.toHaveTextContent('usr-two')
    await user.click(options[1])
    expect(onSelect).toHaveBeenCalledWith(emailUser)
  })

  it('shows identity details on the selected person trigger', async () => {
    const user = userEvent.setup()
    render(<Shell><AssigneePicker users={users} value={users[0]} onChange={vi.fn()}/></Shell>)
    await user.hover(screen.getByRole('combobox'))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('EMP-1001')
  })

  it('shows real roles and memberships without inventing presence or local time', () => {
    const data = makeBootstrap()
    render(<I18nProvider><PeopleProvider users={users} members={[{ user: users[0], role: 'owner', status: 'active', joinedAt: '2026-01-01' }]} teams={data.teams} teamMembers={[{ userId: users[0].id, teamId: data.teams[0].id, role: 'member', joinedAt: '2026-01-01' }]} projects={[{ ...project, name: 'Current project', memberIds: [users[0].id] }, { ...project, id: 'archived', name: 'Archived project', archivedAt: '2026-01-01', memberIds: [users[0].id] }]}><PersonInfo person={users[0]}/></PeopleProvider></I18nProvider>)
    expect(screen.getByText('Owner')).toBeVisible()
    expect(screen.getByText(data.teams[0].name)).toBeVisible()
    expect(screen.getByText('Current project')).toBeVisible()
    expect(screen.queryByText('Archived project')).not.toBeInTheDocument()
    expect(screen.queryByText('Offline')).not.toBeInTheDocument()
    expect(screen.queryByText('local time')).not.toBeInTheDocument()
  })

  it('selects the correct same-name user in the bulk assignment command', async () => {
    const user = userEvent.setup()
    const onAction = vi.fn()
    const issues = [{ id: 'issue', identifier: 'TEST-1', title: 'Issue' }] as MyIssuesRowData[]
    render(<Shell><MyIssuesBulkActionBar selectedIssues={issues} onAction={onAction} onClear={vi.fn()} actionOptions={action => action === 'assign' ? users.map(person => ({ id: person.id, label: person.displayName })) : []}/></Shell>)
    await user.click(screen.getByRole('button', { name: 'Open command menu' }))
    await user.click(screen.getByRole('option', { name: /Assign to/ }))
    await user.type(screen.getByRole('combobox', { name: 'Search Assign to...' }), 'EMP-1002')
    await user.keyboard('{Enter}')
    expect(onAction).toHaveBeenCalledWith('assign', issues, 'usr-two')
  })
})
