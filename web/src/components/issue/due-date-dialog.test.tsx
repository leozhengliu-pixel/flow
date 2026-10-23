import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { DueDateDialogHost } from './due-date-dialog'
import { requestDueDate } from './due-date-dialog-service'

function open(value?: string) {
  render(<I18nProvider><DueDateDialogHost/></I18nProvider>)
  let result: Promise<string | null> = Promise.resolve(null)
  act(() => { result = requestDueDate(value) })
  return result
}

describe('DueDateDialog', () => {
  it('shows the field and two months, and a click on a day picks it', async () => {
    const result = open('2026-10-22')
    expect(await screen.findByRole('heading', { name: 'Set due date' })).toBeInTheDocument()
    expect(screen.getByLabelText('Due date')).toHaveValue('10/22/2026')
    expect(screen.getAllByRole('table')).toHaveLength(2)
    expect(screen.getByRole('table', { name: 'October 2026' })).toBeInTheDocument()
    expect(screen.getByRole('table', { name: 'November 2026' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Wednesday, October 28, 2026' }))
    await expect(result).resolves.toBe('2026-10-28')
  })

  it('saves what was typed and resolves null on cancel', async () => {
    const typed = open()
    await userEvent.type(await screen.findByLabelText('Due date'), '2026-12-01')
    await userEvent.click(screen.getByRole('button', { name: 'Save due date' }))
    await expect(typed).resolves.toBe('2026-12-01')

    const cancelled = open('2026-10-22')
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    await expect(cancelled).resolves.toBeNull()
  })

  it('pages the months with the arrows in the second header', async () => {
    open('2026-10-22')
    await userEvent.click(await screen.findByRole('button', { name: 'Next month' }))
    expect(screen.getByRole('table', { name: 'November 2026' })).toBeInTheDocument()
    expect(screen.getByRole('table', { name: 'December 2026' })).toBeInTheDocument()
  })
})
