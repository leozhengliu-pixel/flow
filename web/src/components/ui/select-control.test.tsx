import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { SelectControl } from './select-control'

describe('SelectControl', () => {
  it('renders a portal menu and preserves option values', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<SelectControl label="Team" value="workspace" onChange={onChange} options={[{ value: 'workspace', label: 'Workspace' }, { value: 'team', label: 'Team' }]}/>)
    await user.click(screen.getByRole('combobox', { name: 'Team' }))
    await user.click(screen.getByRole('option', { name: 'Team' }))
    expect(onChange).toHaveBeenCalledWith('team')
  })

  it('renders grouped options without translating entity labels', async () => {
    const user = userEvent.setup()
    render(<SelectControl label="Target" value="new" onChange={vi.fn()} options={[{ value: 'new', label: 'New issue' }, { value: 'issue-1', label: 'ENG-1 Checkout', entityName: true, groupLabel: 'Existing issues' }]}/>)
    await user.click(screen.getByRole('combobox', { name: 'Target' }))
    expect(screen.getByText('Existing issues')).toBeVisible()
    expect(screen.getByRole('option', { name: 'ENG-1 Checkout' })).toBeVisible()
  })
})
