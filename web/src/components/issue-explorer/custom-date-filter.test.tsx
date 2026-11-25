import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { filterOperatorChoices, toggleFilterOption } from '@/components/my-issues/my-issues-filter-types'
import { DateFilterDialog } from './date-filter-dialog'
import { parseDateFilterInput } from './date-filter-input'
import { compareDateFilter, dateFilterMenu, dateFilterQueryConditions, defaultDateFilterOperator, parseDateFilterValue, withDateFilterCounts } from './issue-date-filter'

// Wednesday, 11 November 2026, local noon.
const NOW = new Date(2026, 10, 11, 12).getTime()
const iso = (year: number, month: number, day: number, ms = 0) => new Date(new Date(year, month - 1, day).getTime() + ms).toISOString()

describe('custom date and period values', () => {
  it('parses the dialog field like the reference app', () => {
    expect(parseDateFilterInput('05/20/2027', NOW)).toEqual({ date: '2027-05-20' })
    expect(parseDateFilterInput('5/20', NOW)).toEqual({ date: '2026-05-20' })
    expect(parseDateFilterInput('2027/05/20', NOW)).toEqual({ date: '2027-05-20' })
    expect(parseDateFilterInput('02/30/2027', NOW)).toBeUndefined()
    expect(parseDateFilterInput('May 2027', NOW)?.period).toMatchObject({ unit: 'month', year: 2027, index: 5, start: '2027-05-01', end: '2027-06-01' })
    expect(parseDateFilterInput('feb', NOW)?.period).toMatchObject({ unit: 'month', year: 2026, index: 2 })
    expect(parseDateFilterInput('Q4', NOW)?.period).toMatchObject({ unit: 'quarter', year: 2026, index: 4, start: '2026-10-01', end: '2027-01-01' })
    expect(parseDateFilterInput('2027 q1', NOW)?.period).toMatchObject({ unit: 'quarter', year: 2027, index: 1 })
    expect(parseDateFilterInput('H2 2026', NOW)?.period).toMatchObject({ unit: 'half', start: '2026-07-01', end: '2027-01-01' })
    expect(parseDateFilterInput('2026', NOW)?.period).toMatchObject({ unit: 'year', start: '2026-01-01', end: '2027-01-01' })
    expect(parseDateFilterInput('2027 年第 4 季度', NOW)?.period).toMatchObject({ unit: 'quarter', year: 2027, index: 4 })
    expect(parseDateFilterInput('2027年下半年', NOW)?.period).toMatchObject({ unit: 'half', year: 2027, index: 2 })
    expect(parseDateFilterInput('soon', NOW)).toBeUndefined()
  })

  it('reads stored values and their default comparison', () => {
    expect(parseDateFilterValue('created:2022-02')?.period).toMatchObject({ start: '2022-02-01', end: '2022-03-01' })
    expect(parseDateFilterValue('created:2026-Q4')?.period?.unit).toBe('quarter')
    expect(parseDateFilterValue('canceled:-1w')?.kind).toBe('canceled')
    expect(defaultDateFilterOperator('created:2022-02')).toBe('within')
    expect(defaultDateFilterOperator('created:2026-11-05')).toBe('after')
    expect(defaultDateFilterOperator('due:2026-11-05')).toBe('before')
    expect(defaultDateFilterOperator('created:-1w')).toBe('after')
  })

  it('sends on / before / after a day and within a period as bounded server conditions', () => {
    const day = parseDateFilterValue('created:2026-11-05')!
    expect(dateFilterQueryConditions(day, 'on', NOW)).toEqual([{ operator: 'after', value: iso(2026, 11, 5, -1) }, { operator: 'before', value: iso(2026, 11, 6) }])
    expect(dateFilterQueryConditions(day, 'after', NOW)).toEqual([{ operator: 'after', value: iso(2026, 11, 5, -1) }])
    expect(dateFilterQueryConditions(day, 'before', NOW)).toEqual([{ operator: 'before', value: iso(2026, 11, 5) }])
    const due = parseDateFilterValue('due:2026-11-05')!
    expect(dateFilterQueryConditions(due, 'after', NOW)).toEqual([{ operator: 'after', value: '2026-11-04' }])
    expect(dateFilterQueryConditions(parseDateFilterValue('due:2026-Q4')!, 'within', NOW)).toEqual([{ operator: 'after', value: '2026-09-30' }, { operator: 'before', value: '2027-01-01' }])
    // Presets keep their single comparison.
    expect(dateFilterQueryConditions(parseDateFilterValue('created:-1w')!, 'after', NOW)).toHaveLength(1)
  })

  it('matches loaded rows with the same bounds (after a day includes that day)', () => {
    const day = parseDateFilterValue('created:2026-11-05')!
    expect(compareDateFilter(iso(2026, 11, 5, 3_600_000), day, 'on', NOW)).toBe(true)
    expect(compareDateFilter(iso(2026, 11, 6, 3_600_000), day, 'on', NOW)).toBe(false)
    expect(compareDateFilter(iso(2026, 11, 5, 3_600_000), day, 'after', NOW)).toBe(true)
    expect(compareDateFilter(iso(2026, 11, 4, 3_600_000), day, 'after', NOW)).toBe(false)
    expect(compareDateFilter(iso(2026, 11, 4, 3_600_000), day, 'before', NOW)).toBe(true)
    expect(compareDateFilter('2026-11-05', parseDateFilterValue('due:2026-11-05')!, 'after', NOW)).toBe(true)
    const feb = parseDateFilterValue('created:2022-02')!
    expect(compareDateFilter(iso(2022, 2, 28, 3_600_000), feb, 'within', NOW)).toBe(true)
    expect(compareDateFilter(iso(2022, 3, 1, 3_600_000), feb, 'within', NOW)).toBe(false)
  })

  it('offers within for periods and on / before / after with marks for a custom day', () => {
    expect(filterOperatorChoices('dates', ['created:2022-02']).map(choice => choice.operator)).toEqual(['within'])
    expect(filterOperatorChoices('dates', ['created:2026-11-05']).map(choice => [choice.operator, choice.glyph])).toEqual([['on', 'dateOn'], ['before', 'dateBefore'], ['after', 'dateAfter']])
    expect(filterOperatorChoices('dates', ['created:-1w']).map(choice => choice.operator)).toEqual(['before', 'after'])
  })

  it('applies a dialog value with its comparison, replacing the chip even for the same day', () => {
    const first = toggleFilterOption([], 'dates', 'Dates', { id: 'created:2026-11-05', label: 'Nov 5', filterLabel: 'Created date', dateOperator: 'on' })
    expect(first[0]).toMatchObject({ operator: 'on', value: 'created:2026-11-05' })
    const again = toggleFilterOption(first, 'dates', 'Dates', { id: 'created:2026-11-05', label: 'Nov 5', filterLabel: 'Created date', dateOperator: 'before' })
    expect(again).toHaveLength(1)
    expect(again[0]).toMatchObject({ operator: 'before', value: 'created:2026-11-05' })
  })

  it('counts the loaded issues each Dates option matches', () => {
    const menu = withDateFilterCounts(dateFilterMenu(), [{ createdAt: iso(2026, 11, 11, 3_600_000), dueDate: '2026-11-01' }, { createdAt: iso(2026, 10, 1), canceledAt: iso(2026, 11, 10) }], NOW)
    const created = menu.find(option => option.id === 'created-date')!.children!
    expect(created.find(option => option.id === 'created:-1d')?.count).toBe(1)
    expect(created.find(option => option.id === 'created:-1y')?.count).toBe(2)
    expect(created.find(option => option.id === 'created-custom')?.count).toBeUndefined()
    expect(menu.find(option => option.id === 'canceled-date')!.children!.find(option => option.id === 'canceled:-1w')?.count).toBe(1)
    const due = menu.find(option => option.id === 'due-date')!.children!
    expect(due.find(option => option.id === 'overdue')?.count).toBe(1)
    expect(due.find(option => option.id === 'no-due-date')?.count).toBe(1)
  })
})

describe('DateFilterDialog', () => {
  const open = (props: Partial<Parameters<typeof DateFilterDialog>[0]> = {}) => {
    const onApply = vi.fn(), onClose = vi.fn()
    render(<I18nProvider><DateFilterDialog open fieldLabel="Created date" kind="created" onApply={onApply} onClose={onClose} now={NOW} {...props}/></I18nProvider>)
    return { onApply, dialog: screen.getByRole('dialog') }
  }

  it('applies a period cell at once', () => {
    const { onApply, dialog } = open()
    expect(within(dialog).getByRole('tab', { name: 'Quarter' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Month' }))
    const year = dialog.querySelector('[data-year="2026"]') as HTMLElement
    fireEvent.click(within(year).getByRole('button', { name: 'Feb' }))
    expect(onApply).toHaveBeenCalledWith('2026-02', 'within')
  })

  it('previews typed text and applies it with Enter', () => {
    const { onApply, dialog } = open()
    const input = within(dialog).getByRole('textbox')
    fireEvent.change(input, { target: { value: 'Q4' } })
    const year = dialog.querySelector('[data-year="2026"]') as HTMLElement
    expect(within(year).getByRole('button', { name: 'Q4' })).toHaveAttribute('data-preview')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onApply).toHaveBeenCalledWith('2026-Q4', 'within')
  })

  it('applies a day with the chosen comparison, and reopens an applied value selected', () => {
    const { onApply, dialog } = open()
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Day' }))
    expect(within(dialog).getByRole('button', { name: 'after' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(within(dialog).getByRole('button', { name: 'on' }))
    fireEvent.click(within(dialog).getByRole('button', { name: '2026-11-05' }))
    expect(onApply).toHaveBeenCalledWith('2026-11-05', 'on')
  })

  it('shows an applied period selected with its text', () => {
    const { dialog } = open({ value: '2022-02', operator: 'within' })
    expect(within(dialog).getByRole('tab', { name: 'Month' })).toHaveAttribute('aria-selected', 'true')
    expect(within(dialog).getByRole('textbox')).toHaveValue('Feb 2022')
    const year = dialog.querySelector('[data-year="2022"]') as HTMLElement
    expect(within(year).getByRole('button', { name: 'Feb' })).toHaveAttribute('data-selected')
  })

  it('reads in Chinese: labels, months and weekdays', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    try {
      const { dialog } = open({ value: '2022-02', operator: 'within' })
      expect(within(dialog).getByText('创建日期')).toBeInTheDocument()
      expect(within(dialog).getByRole('tab', { name: '月' })).toHaveAttribute('aria-selected', 'true')
      for (const tab of ['日', '季度', '半年', '年']) expect(within(dialog).getByRole('tab', { name: tab })).toBeInTheDocument()
      const year = dialog.querySelector('[data-year="2022"]') as HTMLElement
      expect(within(year).getByRole('button', { name: '2月' })).toHaveAttribute('data-selected')
      fireEvent.click(within(dialog).getByRole('tab', { name: '日' }))
      expect(within(dialog).getByText('2022年2月')).toBeInTheDocument()
      // Chinese weeks start on Monday unless the account chose another first day.
      expect(dialog.querySelector('[class*="weekdayName"]')?.textContent).toBe('一')
    } finally {
      localStorage.removeItem('flow:locale')
    }
  })
})
