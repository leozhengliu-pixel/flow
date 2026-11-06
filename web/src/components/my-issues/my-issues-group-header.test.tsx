import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { MyIssuesGroupHeader, type MyIssuesGroupData } from './my-issues-list'

const group: MyIssuesGroupData = { id: 'started', label: 'In Progress', issues: [], stateType: 'started' }

function renderHeader(props: Partial<Parameters<typeof MyIssuesGroupHeader>[0]> = {}) {
  const onGroupCollapsedChange = vi.fn()
  const onCollapseAll = vi.fn()
  const onCreateIssue = vi.fn()
  render(<I18nProvider><TooltipProvider delayDuration={0}><MyIssuesGroupHeader collapsed={false} createIssueLabel="Create new issue" group={group} onCollapseAll={onCollapseAll} onCreateIssue={onCreateIssue} onGroupCollapsedChange={onGroupCollapsedChange} {...props}/></TooltipProvider></I18nProvider>)
  return { onGroupCollapsedChange, onCollapseAll, onCreateIssue }
}

describe('issue group header', () => {
  it('hints T and Option-click on the chevron and "Add issue…" on the plus, in the raised tooltip palette', async () => {
    renderHeader()
    await userEvent.hover(screen.getByRole('button', { name: 'Collapse group' }))
    const tooltip = await screen.findByRole('tooltip')
    expect(tooltip).toHaveTextContent('to collapse group')
    expect(tooltip).toHaveTextContent('to collapse all')
    expect(tooltip.className).toContain('flow-tooltip-content--raised')
    expect([...tooltip.querySelectorAll('kbd')].map(key => key.textContent)).toEqual(['T', '⌥', 'Click'])
  })

  it('hints "Add issue…" on the plus', async () => {
    renderHeader()
    await userEvent.hover(screen.getByRole('button', { name: 'Create new issue' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Add issue…')
  })

  it('collapses with a plain click, collapses every group with Option-click and toggles with T while hovered', async () => {
    const { onGroupCollapsedChange, onCollapseAll } = renderHeader()
    const chevron = screen.getByRole('button', { name: 'Collapse group' })
    await userEvent.click(chevron)
    expect(onGroupCollapsedChange).toHaveBeenCalledWith('started', true)
    fireEvent.click(chevron, { altKey: true })
    expect(onCollapseAll).toHaveBeenCalledTimes(1)
    expect(onGroupCollapsedChange).toHaveBeenCalledTimes(1)
    const header = document.querySelector('[data-my-issues-group-header]') as HTMLElement
    fireEvent.pointerLeave(header)
    fireEvent.keyDown(document, { key: 't' })
    expect(onGroupCollapsedChange).toHaveBeenCalledTimes(1)
    fireEvent.pointerEnter(header)
    fireEvent.keyDown(document, { key: 't' })
    expect(onGroupCollapsedChange).toHaveBeenCalledTimes(2)
    fireEvent.pointerLeave(header)
    fireEvent.keyDown(document, { key: 't' })
    expect(onGroupCollapsedChange).toHaveBeenCalledTimes(2)
  })

  it('says to expand a collapsed group', async () => {
    renderHeader({ collapsed: true })
    await userEvent.hover(screen.getByRole('button', { name: 'Expand group' }))
    expect(await screen.findByRole('tooltip')).toHaveTextContent('to expand group')
  })
})
