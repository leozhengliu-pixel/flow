import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { UniversalCustomFilterPanel } from './universal-custom-filter-panel'
import { clearFilterValueRegistry } from './register-filter-values'

describe('UniversalCustomFilterPanel', () => {
  beforeEach(() => clearFilterValueRegistry())

  it('loads pack, adds a filter, and emits REST and/or AST', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <UniversalCustomFilterPanel
        entityType="issue"
        onChange={onChange}
        optionsFor={block => block.id === 'status' ? [{ id: 'started', label: 'Started' }] : []}
      />,
    )

    await waitFor(() => expect(screen.getByText(/Filter issues by/i)).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Add filter' })) // empty-state CTA
    await waitFor(() => expect(screen.getByRole('option', { name: 'Status' })).toBeInTheDocument())
    await user.click(screen.getByRole('option', { name: 'Status' }))

    await waitFor(() => expect(screen.getByLabelText('Filter field')).toBeInTheDocument())
    const valueSelect = screen.getByLabelText('Status values')
    await user.selectOptions(valueSelect, 'started')

    await waitFor(() => {
      expect(onChange).toHaveBeenCalled()
      const last = onChange.mock.calls.at(-1)?.[0]
      expect(last).toMatchObject({ field: 'status', operator: 'is', values: ['started'] })
    })
  })

  it('shows read-only empty copy', async () => {
    render(<UniversalCustomFilterPanel entityType="notification" onChange={() => {}} readOnly />)
    await waitFor(() => expect(screen.getByText('This filter is read-only.')).toBeInTheDocument())
  })
})
