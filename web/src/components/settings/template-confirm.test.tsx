import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { confirmTemplateAction } from './template-confirm'

const options = { title: 'Discard changes?', description: 'Confirm that you want to discard your changes.', confirmLabel: 'Discard', cancelLabel: 'Cancel' }

describe('confirmTemplateAction', () => {
  it('shows the reference app\'s confirm card with the destructive button focused and resolves true on confirm', async () => {
    const user = userEvent.setup()
    let result: Promise<boolean> | undefined
    act(() => { result = confirmTemplateAction(options) })
    const dialog = await screen.findByRole('dialog', { name: 'Discard changes?' })
    expect(dialog).toHaveTextContent('Confirm that you want to discard your changes.')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Discard' })).toHaveFocus())
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    await expect(result).resolves.toBe(true)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('resolves false on Cancel', async () => {
    const user = userEvent.setup()
    let result: Promise<boolean> | undefined
    act(() => { result = confirmTemplateAction(options) })
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    await expect(result).resolves.toBe(false)
  })
})
