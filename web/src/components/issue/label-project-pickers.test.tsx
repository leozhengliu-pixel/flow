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

    await user.click(screen.getByRole('button', { name: 'Add label' }))
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

    await user.click(screen.getByRole('button', { name: 'Add label' }))
    const dialog = await screen.findByRole('dialog', { name: 'Change Labels' })
    await user.type(within(dialog).getByRole('textbox'), 'Improvement')
    expect(within(dialog).queryByRole('option', { name: /Create new workspace label/ })).not.toBeInTheDocument()
  })
})
