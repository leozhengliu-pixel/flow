import { useState } from 'react'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { IssueLabel, LabelGroup } from '@/types/flow'
import { ProjectLabelControl } from './project-label-control'
import { projectLabelEntries, projectLabelOptions } from './project-label-menu-model'

const groups = [{ id: 'delivery', name: 'Delivery', color: '#D97757', resourceType: 'project' }] as LabelGroup[]
const labels = [
  { id: 'alpha', name: 'Alpha', color: '#D97757', resourceType: 'project', groupId: 'delivery' },
  { id: 'beta', name: 'Beta', color: '#18B99A', resourceType: 'project', groupId: 'delivery' },
  { id: 'plain', name: 'Public', color: '#4cb782', resourceType: 'project' },
] as IssueLabel[]

function Harness({ initial = ['alpha', 'plain'], sidebar = false }: { initial?: string[]; sidebar?: boolean }) {
  const [selected, setSelected] = useState(initial)
  return <I18nProvider><ProjectLabelControl labels={labels} labelGroups={groups} selectedIds={selected} onChange={setSelected} sidebar={sidebar}/><output data-testid="selection">{selected.join(',')}</output></I18nProvider>
}

describe('project labels', () => {
  it('represents a selected group once, with its value and selected child color', () => {
    const entries = projectLabelEntries(projectLabelOptions(labels, groups), ['alpha', 'plain'])
    expect(entries.map(entry => entry.id)).toEqual(['group:delivery', 'plain'])
    expect(entries[0]).toMatchObject({ checked: true, detail: 'Alpha', color: '#D97757' })
    expect(projectLabelEntries(projectLabelOptions(labels, groups), [], 'alp')).toEqual([expect.objectContaining({ id: 'alpha', detail: 'Delivery' })])
    expect(projectLabelEntries(projectLabelOptions(labels, groups), [], 'al')).toEqual([])
  })

  it('shows independent chips and replaces a group value without removing ordinary labels', async () => {
    const user = userEvent.setup()
    render(<Harness sidebar/>)
    await user.click(screen.getByRole('button', { name: 'Change Delivery: Alpha' }))
    expect(screen.queryByRole('option', { name: 'Public' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: 'Beta' }))
    expect(screen.getByTestId('selection')).toHaveTextContent('plain,beta')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Change Delivery: Beta' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Change labels: Public' })).toBeVisible()
  })

  it('navigates the visible group hierarchy, returns to root, then selects a child', async () => {
    const user = userEvent.setup()
    render(<Harness initial={['plain']}/>)
    await user.click(screen.getByRole('button', { name: 'Add label' }))
    const input = screen.getByRole('textbox', { name: 'Change or add labels…' })
    await user.click(input)
    await user.keyboard('{ArrowDown}{ArrowRight}')
    expect(screen.getByRole('listbox', { name: 'Delivery' })).toBeVisible()
    await user.keyboard('{ArrowLeft}')
    await waitFor(() => expect(screen.queryByRole('listbox', { name: 'Delivery' })).not.toBeInTheDocument())
    expect(input).toHaveFocus()
    await user.keyboard('{ArrowRight}{ArrowDown}{Enter}')
    expect(screen.getByTestId('selection')).toHaveTextContent('plain,beta')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('opens groups on hover and unselects a selected child', async () => {
    const user = userEvent.setup()
    render(<Harness/>)
    await user.click(screen.getByRole('button', { name: 'Add label' }))
    const group = screen.getByRole('option', { name: /Delivery Alpha/ })
    expect(group).toHaveAttribute('aria-checked', 'true')
    await user.hover(group)
    const submenu = await screen.findByRole('listbox', { name: 'Delivery' })
    expect(screen.getByRole('textbox', { name: 'Change or add labels…' })).toHaveFocus()
    await user.click(within(submenu).getByRole('option', { name: 'Alpha' }))
    expect(screen.getByTestId('selection')).toHaveTextContent('plain')
  })

  it('clears a group with its selected checkbox without closing the root menu', async () => {
    const user = userEvent.setup()
    render(<Harness/>)
    await user.click(screen.getByRole('button', { name: 'Add label' }))
    const group = screen.getByRole('option', { name: 'Delivery Alpha' })
    await user.click(group.querySelector('.project-label-checkbox')!)
    expect(screen.getByTestId('selection')).toHaveTextContent('plain')
    expect(screen.getByRole('dialog')).toBeVisible()
    expect(screen.getByRole('option', { name: 'Delivery' })).toHaveAttribute('aria-checked', 'false')
  })

  it('searches children from the root and preserves multi-selection for ordinary labels', async () => {
    const user = userEvent.setup()
    render(<Harness initial={[]}/>)
    await user.click(screen.getByRole('button', { name: 'Add label' }))
    await user.click(screen.getByRole('option', { name: 'Public' }))
    expect(screen.getByRole('dialog')).toBeVisible()
    await user.type(screen.getByRole('textbox', { name: 'Change or add labels…' }), 'bet')
    await user.keyboard('{Enter}')
    expect(screen.getByTestId('selection')).toHaveTextContent('plain,beta')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('creates a label in the current group and applies it instead of its peer', async () => {
    const user = userEvent.setup()
    const onCreateLabel = vi.fn().mockResolvedValue({ id: 'new', name: 'Gamma', resourceType: 'project', groupId: 'delivery' })
    const onChange = vi.fn()
    render(<I18nProvider><ProjectLabelControl labels={labels} labelGroups={groups} selectedIds={['alpha', 'plain']} onChange={onChange} onCreateLabel={onCreateLabel}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Change Delivery: Alpha' }))
    await user.type(screen.getByRole('textbox', { name: 'Delivery' }), 'Gamma')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(onCreateLabel).toHaveBeenCalledWith('Gamma', 'delivery'))
    expect(onChange).toHaveBeenCalledWith(['plain', 'new'])
  })

  it('keeps a creation failure visible without applying a phantom label', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><ProjectLabelControl labels={labels} labelGroups={groups} selectedIds={[]} onChange={onChange} onCreateLabel={async () => { throw new Error('Creation failed') }}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Add label' }))
    await user.type(screen.getByRole('textbox', { name: 'Add labels…' }), 'Gamma')
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('alert')).toHaveTextContent('Creation failed')
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeVisible()
  })
})
