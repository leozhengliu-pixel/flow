import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { ActionDialogHost } from './action-dialogs'
import { completeActionDialog, confirmAction, currentActionDialog, promptAction } from './action-dialog-service'

afterEach(() => {
  cleanup()
  while (currentActionDialog()) completeActionDialog(null)
})

describe('ActionDialogHost', () => {
  it('resolves confirmation and destructive emphasis', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = confirmAction('Delete project?', { description: 'This cannot be undone.', confirmLabel: 'Delete', danger: true })
    expect(await screen.findByRole('heading', { name: 'Delete project?' })).toBeVisible()
    expect(screen.getByText('This cannot be undone.')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await expect(result).resolves.toBe(true)
  })

  it('trims prompt values submitted with Enter', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = promptAction('Rename view', ' Draft ', { confirmLabel: 'Save' })
    const input = await screen.findByRole('textbox', { name: 'Rename view' })
    await user.clear(input)
    await user.type(input, '  Roadmap  {Enter}')
    await expect(result).resolves.toBe('Roadmap')
  })

  it('resolves cancellation without applying the action', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = confirmAction('Archive issue?')
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    await expect(result).resolves.toBe(false)
  })
})
