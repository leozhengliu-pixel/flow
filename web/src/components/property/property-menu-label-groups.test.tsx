import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { PropertyMenu, type PropertyOption } from './property-menu'

const longGroup = '事务类型与优先级分类标签组（客户可见）'
const options: PropertyOption[] = [
  { id: 'defect', label: '缺陷', color: '#26b5ce', scope: 'Workspace', groupId: 'kind', groupLabel: longGroup, groupColor: '#26b5ce', i18nIgnore: true },
  { id: 'request', label: '功能请求', color: '#26b5ce', scope: 'Workspace', groupId: 'kind', groupLabel: longGroup, groupColor: '#26b5ce', i18nIgnore: true },
  { id: 'team-a', label: '团队A', color: '#4cb782', scope: 'team_1', groupId: 'team-group', groupLabel: '团队范围的标签组', i18nIgnore: true },
  { id: 'onboarding', label: '新手入门', color: '#4cb782', scope: 'Workspace', i18nIgnore: true },
]

function renderMenu(selectedIds: string[] = [], props: Partial<Parameters<typeof PropertyMenu>[0]> = {}) {
  const onChange = vi.fn()
  const onCreate = vi.fn()
  render(<I18nProvider><PropertyMenu multiple kind="labels" label="Labels" options={options} selectedIds={selectedIds} onChange={onChange} onCreate={onCreate} {...props}/></I18nProvider>)
  return { onChange, onCreate }
}

async function openMenu() {
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: /Change Labels/ }))
  return { user, dialog: await screen.findByRole('dialog', { name: 'Change Labels' }) }
}

const groupRow = (dialog: HTMLElement, name: string) => [...dialog.querySelectorAll<HTMLElement>('.property-command-label-group-option')].find(row => row.querySelector('.property-command-group-copy > .property-command-label')?.textContent === name)!

describe('label group rows', () => {
  it('render the icon, the name and the submenu marker as direct cells of one row', async () => {
    renderMenu()
    const { dialog } = await openMenu()
    const row = groupRow(dialog, longGroup)
    // Cells sit side by side in the row's grid: icon, then the name (which ellipsizes), then the 16px marker.
    expect([...row.children].map(child => child.className)).toEqual(['property-command-option-background', 'property-command-icon', 'property-command-group-copy', 'property-command-group-chevron'])
    expect(row.querySelector('.property-command-group-copy > .property-command-label')).toHaveTextContent(longGroup)
    expect(row.querySelector('.property-command-group-chevron .flow-submenu-chevron')).toHaveTextContent('▶')
    // No checkbox until one of the group's labels is selected.
    expect(row.querySelector('.property-command-checkbox')).toBeNull()
    expect(row).toHaveAttribute('aria-checked', 'false')
  })

  it('list team labels under their own heading above the workspace labels', async () => {
    renderMenu()
    const { dialog } = await openMenu()
    expect([...dialog.querySelectorAll('.property-command-group')].map(item => item.textContent)).toEqual(['Team labels', 'Workspace labels'])
    const rows = [...dialog.querySelectorAll('.property-command-options [role=option]')].map(row => row.querySelector('.property-command-label')?.textContent)
    expect(rows).toEqual(['团队范围的标签组', longGroup, '新手入门'])
  })

  it('tick a group with a selected label, name the label and lead the list with it', async () => {
    const { onChange } = renderMenu(['request'])
    const { user, dialog } = await openMenu()
    const row = within(dialog).getAllByRole('option')[0]
    expect(row).toHaveClass('property-command-label-group-option')
    expect(row).toHaveAttribute('aria-checked', 'true')
    expect(row.querySelector('.property-command-group-detail')).toHaveTextContent('功能请求')
    // Clearing the tick unselects the group's label.
    await user.click(row.querySelector('.property-command-checkbox')!)
    expect(onChange).toHaveBeenCalledWith('request')
  })

  it('open the group submenu and choosing a label closes the picker', async () => {
    const { onChange } = renderMenu()
    const { user, dialog } = await openMenu()
    await user.click(groupRow(dialog, longGroup))
    const submenu = await waitFor(() => { const element = document.querySelector<HTMLElement>('.property-command-label-submenu'); expect(element).not.toBeNull(); return element! })
    expect(within(submenu).getAllByRole('option').map(option => option.textContent)).toEqual(['缺陷', '功能请求'])
    await user.click(within(submenu).getByRole('option', { name: '功能请求' }))
    expect(onChange).toHaveBeenCalledWith('request')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change Labels' })).not.toBeInTheDocument())
  })

  it('create a label inside the group from its submenu', async () => {
    const { onCreate } = renderMenu()
    const { user, dialog } = await openMenu()
    await user.click(groupRow(dialog, longGroup))
    const submenu = await waitFor(() => { const element = document.querySelector<HTMLElement>('.property-command-label-submenu'); expect(element).not.toBeNull(); return element! })
    await user.type(within(submenu).getByRole('textbox'), '性能')
    await user.click(within(submenu).getByRole('option', { name: /性能/ }))
    expect(onCreate).toHaveBeenCalledWith('性能', 'kind')
  })

  it('a search lists labels of groups as "Group › Label"', async () => {
    renderMenu()
    const { user, dialog } = await openMenu()
    await user.type(within(dialog).getByRole('textbox'), '缺陷')
    const row = within(dialog).getAllByRole('option')[0]
    expect(row.querySelector('.property-command-group-copy.is-path .property-command-group-path')).toHaveTextContent(longGroup)
    expect(row.querySelector('.property-command-group-copy.is-path > .property-command-label')).toHaveTextContent('缺陷')
    expect(dialog.querySelectorAll('.property-command-label-group-option')).toHaveLength(0)
  })

  it('a frequently used label of a group names its group after it', async () => {
    render(<I18nProvider><PropertyMenu multiple kind="labels" label="Labels" options={options.map(option => option.id === 'defect' ? { ...option, issueCount: 2 } : option)} selectedIds={[]} onChange={vi.fn()}/></I18nProvider>)
    const { dialog } = await openMenu()
    expect(dialog.querySelector('.property-command-group')).toHaveTextContent('Frequently used')
    const row = within(dialog).getAllByRole('option')[0]
    expect(row.querySelector('.property-command-group-copy > .property-command-label')).toHaveTextContent('缺陷')
    expect(row.querySelector('.property-command-group-detail')).toHaveTextContent(longGroup)
  })
})
