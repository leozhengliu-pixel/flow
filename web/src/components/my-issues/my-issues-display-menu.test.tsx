import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { MyIssuesDisplayMenu } from './my-issues-display-menu'
import { createIssueDisplayOptions, issueDisplayEqual } from './my-issues-display-defaults'
import { PAGED_GROUPINGS, PAGED_ORDERINGS } from '@/components/issue-explorer/issue-grouping'
const layout=vi.hoisted(()=>({desktop:false,phone:false}))
vi.mock('@/hooks/use-desktop-layout',()=>({useDesktopLayout:()=>layout.desktop}))
vi.mock('@/hooks/use-phone-layout',()=>({usePhoneLayout:()=>layout.phone}))

describe('display options: nested sub-issues', () => {
  beforeEach(()=>{layout.desktop=false;layout.phone=false})
  it('compares saved defaults independently of property insertion order and absent optional values', () => {
    const left = createIssueDisplayOptions({ properties: ['id', 'status'] })
    expect(issueDisplayEqual(left, createIssueDisplayOptions({ properties: ['status', 'id'], showArchived: undefined }))).toBe(true)
    expect(issueDisplayEqual(left, { ...left, ordering: 'priority' })).toBe(false)
  })
  it('omits completed issues for a scoped active view', () => {
    render(<I18nProvider><MyIssuesDisplayMenu open hideCompletedIssues onOpenChange={vi.fn()} options={createIssueDisplayOptions()} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('combobox', { name: 'Completed issues' })).not.toBeInTheDocument()
  })
  it('uses the desktop property inventory without trapping saved extra selections', async () => {
    layout.desktop=true
    const options=createIssueDisplayOptions({properties:['id','sla']})
    const onChange=vi.fn()
    render(<I18nProvider><MyIssuesDisplayMenu open onOpenChange={vi.fn()} options={options} onChange={onChange}/></I18nProvider>)
    expect(screen.queryByRole('button',{name:'Release'})).toBeNull()
    expect(screen.queryByRole('button',{name:'My activity date'})).toBeNull()
    await userEvent.click(screen.getByRole('button',{name:'SLA'}))
    expect(onChange.mock.calls[0][0].properties.has('sla')).toBe(false)
  })
  it('retains the additional property choices off desktop', () => {
    render(<I18nProvider><MyIssuesDisplayMenu open onOpenChange={vi.fn()} options={createIssueDisplayOptions()} onChange={vi.fn()}/></I18nProvider>)
    for(const name of ['SLA','Release','My activity date'])expect(screen.getByRole('button',{name})).toBeVisible()
  })
  it('offers Nested sub-issues in list layout, also for server-backed lists, and turns sub-issues on with it', async () => {
    const onChange = vi.fn()
    const options = createIssueDisplayOptions({ showSubIssues: false })
    render(<I18nProvider><MyIssuesDisplayMenu open phoneIssueMenu onOpenChange={vi.fn()} options={options} onChange={onChange} availableGroupings={PAGED_GROUPINGS} availableOrderings={PAGED_ORDERINGS}/></I18nProvider>)
    expect(screen.getByRole('dialog', { name: 'Display options' })).toHaveAttribute('data-phone-popover')
    const select = screen.getByRole('combobox', { name: 'Nested sub-issues' })
    expect(select).toHaveTextContent('Hide')
    await userEvent.click(select)
    await userEvent.click(screen.getByRole('option', { name: 'Show matching' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ nestedSubIssues: true, showSubIssues: true }))
  })

  it('preserves the desktop My issues nesting toggle and its inclusion of context rows', async () => {
    layout.desktop = true
    const onChange = vi.fn()
    render(<I18nProvider><MyIssuesDisplayMenu open phoneIssueMenu myIssuesPhoneMenu onOpenChange={vi.fn()} options={createIssueDisplayOptions({ showSubIssues: false })} onChange={onChange}/></I18nProvider>)
    await userEvent.click(screen.getByRole('checkbox', { name: 'Nested sub-issues' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ nestedSubIssues: true, showSubIssues: true }))
  })

  it('keeps the generic phone nesting toggle when the explorer select is not enabled', async () => {
    layout.phone = true
    const onChange = vi.fn()
    render(<I18nProvider><MyIssuesDisplayMenu open onOpenChange={vi.fn()} options={createIssueDisplayOptions({ showSubIssues: false })} onChange={onChange}/></I18nProvider>)
    await userEvent.click(screen.getByRole('checkbox', { name: 'Nested sub-issues' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ nestedSubIssues: true, showSubIssues: true }))
  })

  it('keeps only the existing nesting select in the compact phone My issues menu', () => {
    layout.phone = true
    render(<I18nProvider><MyIssuesDisplayMenu open phoneIssueMenu myIssuesPhoneMenu onOpenChange={vi.fn()} options={createIssueDisplayOptions()} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('checkbox', { name: 'Nested sub-issues' })).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Nested sub-issues' })).toBeInTheDocument()
  })

  it('has no nesting option in board layout', () => {
    render(<I18nProvider><MyIssuesDisplayMenu open onOpenChange={vi.fn()} options={createIssueDisplayOptions({ layout: 'board' })} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('combobox', { name: 'Nested sub-issues' })).not.toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'Show empty columns' })).toBeInTheDocument()
  })

  it('changes the same sub-team option on desktop and keeps group controls accessible', async () => {
    const onChange = vi.fn()
    const onReset = vi.fn(), onSaveDefault = vi.fn()
    render(<I18nProvider><MyIssuesDisplayMenu open desktopIssueMenu onOpenChange={vi.fn()} options={createIssueDisplayOptions({ layout: 'board' })} onChange={onChange} toggles={['subTeam']} groups={[{ id: 'one', label: 'One', count: 1 }, { id: 'two', label: 'Two', count: 2 }]} onReset={onReset} onSaveDefault={onSaveDefault} saveDefaultLabel="Set default for everyone"/></I18nProvider>)
    await userEvent.click(screen.getByRole('checkbox', { name: 'Show sub-team issues' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ showSubTeamIssues: false }))
    expect(screen.getByText('Columns')).toBeVisible()
    expect(screen.getByText('Rows')).toBeVisible()
    expect(screen.getByText('Groups', { selector: 'summary' }).parentElement).not.toHaveAttribute('open')
    await userEvent.click(screen.getByText('Groups', { selector: 'summary' }))
    await userEvent.click(screen.getByRole('button', { name: 'Hide group One' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ hiddenGroupIds: ['one'] }))
    await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
    await userEvent.click(screen.getByRole('button', { name: 'Set default for everyone' }))
    expect(onReset).toHaveBeenCalledOnce()
    expect(onSaveDefault).toHaveBeenCalledOnce()
  })
})
