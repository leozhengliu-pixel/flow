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
})
