import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { MyIssuesDisplayMenu } from './my-issues-display-menu'
import { createIssueDisplayOptions } from './my-issues-display-defaults'
import { PAGED_GROUPINGS, PAGED_ORDERINGS } from '@/components/issue-explorer/issue-grouping'

describe('display options: nested sub-issues', () => {
  it('offers Nested sub-issues in list layout, also for server-backed lists, and turns sub-issues on with it', async () => {
    const onChange = vi.fn()
    const options = createIssueDisplayOptions({ showSubIssues: false })
    render(<I18nProvider><MyIssuesDisplayMenu open onOpenChange={vi.fn()} options={options} onChange={onChange} availableGroupings={PAGED_GROUPINGS} availableOrderings={PAGED_ORDERINGS}/></I18nProvider>)
    expect(screen.getByRole('dialog', { name: 'Display options' })).toHaveAttribute('data-phone-popover')
    const toggle = screen.getByRole('checkbox', { name: 'Nested sub-issues' })
    expect(toggle).not.toBeChecked()
    await userEvent.click(toggle)
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ nestedSubIssues: true, showSubIssues: true }))
  })

  it('has no nesting option in board layout', () => {
    render(<I18nProvider><MyIssuesDisplayMenu open onOpenChange={vi.fn()} options={createIssueDisplayOptions({ layout: 'board' })} onChange={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('checkbox', { name: 'Nested sub-issues' })).not.toBeInTheDocument()
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
