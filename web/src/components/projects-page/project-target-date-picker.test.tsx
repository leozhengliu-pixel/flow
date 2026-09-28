import { fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { setRuntimePreferences } from '@/lib/runtime-preferences'
import { ProjectDatePicker } from './project-target-date-picker'

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))
afterEach(() => { vi.useRealTimers(); setRuntimePreferences({}) })

function renderPicker(value?: string, onChange = vi.fn()) {
  render(<I18nProvider><ProjectDatePicker label="Target date" onChange={onChange} value={value}><span>Target</span></ProjectDatePicker></I18nProvider>)
  fireEvent.click(screen.getByRole('button', { name: /Change project target date/ }))
  return onChange
}

it('shows the selected day as MM/DD/YYYY and parses typed US dates', () => {
  const onChange = renderPicker('2026-09-28')
  const input = screen.getByRole('textbox')
  expect(input).toHaveValue('09/28/2026')
  fireEvent.change(input, { target: { value: '10/05/2026' } })
  fireEvent.keyDown(input, { key: 'Enter' })
  return vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('2026-10-05', undefined))
})

it('clears a set date from the input clear button', () => {
  const onChange = renderPicker('2026-09-28')
  fireEvent.click(screen.getByRole('button', { name: 'Clear date' }))
  expect(onChange).toHaveBeenCalledWith('')
})

it('hides the clear button while no date is set', () => {
  renderPicker()
  expect(screen.queryByRole('button', { name: 'Clear date' })).not.toBeInTheDocument()
})

it('shades weekend columns, rings today, and keeps the first-day preference', () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 8, 27, 12))
  setRuntimePreferences({ firstDay: 'Sunday' })
  renderPicker('2026-09-28')
  const grid = screen.getByRole('grid')
  const cells = within(grid).getAllByRole('gridcell')
  expect(cells[0]).toHaveTextContent('30')
  expect(cells[0]).toHaveAttribute('data-weekend', 'true')
  expect(cells[1]).not.toHaveAttribute('data-weekend')
  expect(cells[6]).toHaveAttribute('data-weekend', 'true')
  expect(grid.querySelectorAll('[aria-hidden=true]')).toHaveLength(2)
  const today = cells.filter(cell => cell.getAttribute('data-today') === 'true')
  expect(today).toHaveLength(1)
  expect(today[0]).toHaveTextContent('27')
})

it('starts the week on Monday when that is the preference', () => {
  setRuntimePreferences({ firstDay: 'Monday' })
  renderPicker('2026-09-28')
  const cells = within(screen.getByRole('grid')).getAllByRole('gridcell')
  expect(cells[0]).not.toHaveAttribute('data-weekend')
  expect(cells[5]).toHaveAttribute('data-weekend', 'true')
  expect(cells[6]).toHaveAttribute('data-weekend', 'true')
})

it('uses the Linear English placeholder', () => {
  renderPicker()
  expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', 'Try: May 2027, Q4, 05/20/2027')
})

it('starts the week on Sunday for English when no first day is chosen', () => {
  renderPicker('2026-09-28')
  const cells = within(screen.getByRole('grid')).getAllByRole('gridcell')
  expect(cells[0]).toHaveTextContent('30')
  expect(cells[0]).toHaveAttribute('data-weekend', 'true')
})

it('starts the week on Monday for zh-CN when no first day is chosen, and keeps the zh placeholder', () => {
  localStorage.setItem('flow:locale', 'zh-CN')
  render(<I18nProvider><ProjectDatePicker label="Target date" onChange={vi.fn()} value="2026-09-28"><span>Target</span></ProjectDatePicker></I18nProvider>)
  fireEvent.click(screen.getByRole('button'))
  expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', '例如：2027/05/20、2027 年第 4 季度')
  const cells = within(screen.getByRole('grid')).getAllByRole('gridcell')
  expect(cells[0]).toHaveTextContent('31')
  expect(cells[0]).not.toHaveAttribute('data-weekend')
})
