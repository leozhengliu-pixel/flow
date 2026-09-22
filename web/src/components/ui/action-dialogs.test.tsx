import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { ActionDialogHost } from './action-dialogs'
import {
  completeActionDialog,
  confirmAction,
  currentActionDialog,
  promptAction,
  promptDateAction,
  promptDateOrIntervalAction,
  promptDiffAction,
  promptDropdownAction,
  promptValidatedStringAction,
  validatePromptString,
} from './action-dialog-service'

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

  it('prompts a date via DateTimeControl (LS-0501)', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = promptDateAction('Set due date', '2026-09-20', { confirmLabel: 'Set date' })
    expect(await screen.findByRole('heading', { name: 'Set due date' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Set due date' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Set date' }))
    await expect(result).resolves.toBe('2026-09-20')
  })

  it('validates string prompts with Required / Invalid value (LS-0501)', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = promptValidatedStringAction('Team key', '', {
      pattern: /^[A-Z]{2,6}$/,
      patternMessage: 'Invalid value.',
      confirmLabel: 'Save key',
    })
    const input = await screen.findByRole('textbox', { name: 'Team key' })
    await user.type(input, 'ab')
    await user.click(screen.getByRole('button', { name: 'Save key' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid value.')
    await user.clear(input)
    await user.type(input, 'FLOW')
    await user.click(screen.getByRole('button', { name: 'Save key' }))
    await expect(result).resolves.toBe('FLOW')
  })

  it('returns date + compare option from dateOrInterval (LS-0501)', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = promptDateOrIntervalAction('Filter by date', '2026-01-15', { initialCompare: 'before' })
    expect(await screen.findByRole('tab', { name: 'Before' })).toHaveAttribute('aria-selected', 'true')
    await user.click(screen.getByRole('tab', { name: 'On' }))
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    await expect(result).resolves.toEqual({ date: '2026-01-15', compare: 'on' })
  })

  it('resolves dropdown selection (LS-0501)', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = promptDropdownAction('Move to status', [
      { id: 'todo', label: 'Todo', value: 'todo' },
      { id: 'done', label: 'Done', value: 'done' },
    ], { selectedId: 'todo', confirmLabel: 'Move' })
    expect(await screen.findByRole('heading', { name: 'Move to status' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Move' }))
    await expect(result).resolves.toBe('todo')
  })

  it('resolves diff accept / reject / cancel (LS-0501)', async () => {
    const user = userEvent.setup()
    render(<ActionDialogHost/>)
    const result = promptDiffAction('Apply agent patch?', ['- old line', '+ new line'], {
      confirmLabel: 'Accept',
      rejectLabel: 'Reject',
    })
    expect(await screen.findByText('- old line')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Accept' }))
    await expect(result).resolves.toBe('accept')
  })
})

describe('validatePromptString', () => {
  it('returns Required for empty non-optional input', () => {
    expect(validatePromptString('  ', {})).toBe('Required')
  })
})
