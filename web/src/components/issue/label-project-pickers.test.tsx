import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import type { IssueLabel } from '@/types/flow'
import { LabelPicker } from './label-project-pickers'

const existing = { id: 'label-1', name: 'Improvement', color: '#4cb782', resourceType: 'issue' } as IssueLabel

describe('LabelPicker create command', () => {
  it('offers a create item when the query matches no label', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><LabelPicker labels={[existing]} onCreate={onCreate} onToggle={vi.fn()} value={[]}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Add labels' }))
    const dialog = await screen.findByRole('dialog', { name: 'Change Labels' })
    await user.type(within(dialog).getByRole('textbox'), 'test')
    const create = await within(dialog).findByRole('option', { name: /Create new workspace label.*test/ })
    expect(within(dialog).queryByText('No results')).not.toBeInTheDocument()
    await user.click(create)
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith('test'))
  })

  it('does not show create when the typed name already exists', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><LabelPicker labels={[existing]} onCreate={vi.fn()} onToggle={vi.fn()} value={[]}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Add labels' }))
    const dialog = await screen.findByRole('dialog', { name: 'Change Labels' })
    await user.type(within(dialog).getByRole('textbox'), 'Improvement')
    expect(within(dialog).queryByRole('option', { name: /Create new workspace label/ })).not.toBeInTheDocument()
  })
})

describe('LabelPicker headings', () => {
  const labels = [{ id: 'bug', name: 'Bug', color: '#eb5757', resourceType: 'issue', issueCount: 3 }, { id: 'feature', name: 'Feature', color: '#bb87fc', resourceType: 'issue', issueCount: 0 }, existing] as IssueLabel[]
  const open = async () => {
    const user = userEvent.setup()
    return { user, ...await (async () => { await user.click(screen.getByRole('button', { name: 'Add labels' })); return { dialog: await screen.findByRole('dialog', { name: 'Change Labels' }) } })() }
  }

  it('lists used labels under Frequently used and the rest under Workspace labels', async () => {
    render(<I18nProvider><LabelPicker labels={labels} onToggle={vi.fn()} value={[]}/></I18nProvider>)
    const { dialog } = await open()
    expect([...dialog.querySelectorAll('.property-command-group')].map(item => item.textContent)).toEqual(['Frequently used', 'Workspace labels'])
  })

  it('shows only Workspace labels when nothing is used', async () => {
    render(<I18nProvider><LabelPicker labels={labels.map(label => ({ ...label, issueCount: 0 }))} onToggle={vi.fn()} value={[]}/></I18nProvider>)
    const { dialog } = await open()
    expect([...dialog.querySelectorAll('.property-command-group')].map(item => item.textContent)).toEqual(['Workspace labels'])
  })

  it('lists selected labels first without a heading, then Workspace labels', async () => {
    render(<I18nProvider><LabelPicker labels={labels} onToggle={vi.fn()} value={[labels[0]]}/></I18nProvider>)
    const { dialog } = await open()
    expect([...dialog.querySelectorAll('.property-command-group')].map(item => item.textContent)).toEqual(['Workspace labels'])
    expect(within(dialog).getAllByRole('option')[0]).toHaveTextContent('Bug')
  })

  it('drops the headings while searching', async () => {
    render(<I18nProvider><LabelPicker labels={labels} onToggle={vi.fn()} value={[]}/></I18nProvider>)
    const { user, dialog } = await open()
    await user.type(within(dialog).getByRole('textbox'), 'Fea')
    expect(dialog.querySelectorAll('.property-command-group')).toHaveLength(0)
  })
})
