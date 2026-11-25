import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Command } from 'cmdk'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { LabelCommandItems, type LabelCommandLabel, type LabelCommandState } from './label-command-items'

const longGroup = '事务类型与优先级分类标签组（客户可见）'
const labels: LabelCommandLabel[] = [
  { id: 'defect', name: '缺陷', color: '#26b5ce', scope: 'Workspace', groupId: 'kind', groupLabel: longGroup, groupColor: '#26b5ce' },
  { id: 'request', name: '功能请求', color: '#26b5ce', scope: 'Workspace', groupId: 'kind', groupLabel: longGroup, groupColor: '#26b5ce' },
  { id: 'team-c', name: 'Flow parity C', color: '#4cb782', scope: 'team_1', groupId: 'team-group', groupLabel: 'Team group' },
  { id: 'bug', name: 'Bug', color: '#eb5757', scope: 'Workspace' },
]

function Harness({ state, onToggle = vi.fn(), onRemoveAll }: { state: Record<string, LabelCommandState>; onToggle?: (id: string) => void; onRemoveAll?: () => void }) {
  const [query, setQuery] = useState('')
  const [group, setGroup] = useState<{ id: string; name: string }>()
  return <I18nProvider><Command>
    <Command.Input aria-label="Search labels" value={query} onValueChange={setQuery}/>
    <Command.List>
      <LabelCommandItems labels={labels} state={id => state[id] ?? false} query={query} groupId={group?.id} onToggle={onToggle} onOpenGroup={setGroup} onRemoveAll={onRemoveAll}/>
    </Command.List>
  </Command></I18nProvider>
}

const rows = () => [...document.querySelectorAll<HTMLElement>('[cmdk-item]')]

describe('LabelCommandItems', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  })

  it('lists a group with a selected label first, ticked or mixed, naming the label, then team and workspace sections', () => {
    render(<Harness state={{ request: 'mixed' }} onRemoveAll={vi.fn()}/>)
    expect(rows().map(row => row.dataset.labelCommand)).toEqual(['remove-all', 'group', 'group', 'label'])
    const group = rows()[1]
    expect(group).toHaveAttribute('aria-checked', 'mixed')
    expect(group.querySelector('.label-command-name')).toHaveTextContent(longGroup)
    expect(group.querySelector('.label-command-detail')).toHaveTextContent('功能请求')
    expect(group.querySelector('.label-command-checkbox')).toHaveAttribute('data-checked', 'mixed')
    expect([...document.querySelectorAll('[cmdk-group-heading]')].map(heading => heading.textContent)).toEqual(['Team labels', 'Workspace labels'])
  })

  it('keeps every group row on one line: checkbox track, icon, then the name and detail in one copy cell', () => {
    render(<Harness state={{}}/>)
    const group = rows().find(row => row.dataset.labelCommand === 'group')!
    expect([...group.children].map(child => child.className)).toEqual(['label-command-check', 'label-command-icon', 'label-command-copy'])
  })

  it('opens a group step listing its labels with the current one ticked', async () => {
    const user = userEvent.setup()
    const onToggle = vi.fn()
    render(<Harness state={{ defect: true }} onToggle={onToggle}/>)
    await user.click(rows().find(row => row.textContent?.startsWith(longGroup))!)
    expect(rows().map(row => row.textContent)).toEqual(['缺陷', '功能请求'])
    expect(within(rows()[0]).getByLabelText('Current')).toBeInTheDocument()
    await user.click(rows()[1])
    expect(onToggle).toHaveBeenCalledWith('request')
  })

  it('lists the labels of groups as "Group › Label" from three characters (two for Chinese)', async () => {
    const user = userEvent.setup()
    render(<Harness state={{}}/>)
    await user.type(screen.getByLabelText('Search labels'), '缺陷')
    const path = rows().find(row => row.dataset.labelCommand === 'path')!
    expect(path.querySelector('.label-command-path')).toHaveTextContent(longGroup)
    expect(path.querySelector('.label-command-name')).toHaveTextContent('缺陷')
  })

  it('offers Remove all labels only when a label is set', async () => {
    const user = userEvent.setup()
    const onRemoveAll = vi.fn()
    const { unmount } = render(<Harness state={{}} onRemoveAll={onRemoveAll}/>)
    expect(rows().some(row => row.dataset.labelCommand === 'remove-all')).toBe(false)
    unmount()
    render(<Harness state={{ bug: true }} onRemoveAll={onRemoveAll}/>)
    await user.click(rows().find(row => row.dataset.labelCommand === 'remove-all')!)
    expect(onRemoveAll).toHaveBeenCalled()
  })
})
