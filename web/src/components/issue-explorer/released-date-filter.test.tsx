import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary, translateToChinese } from '@/i18n/translate'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { filterChipItem } from '@/components/my-issues/my-issues-filter-chips'
import { filterOperatorChoices, toggleFilterOption, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { decodeFiltersParam, encodeFiltersParam } from './advanced-filter'
import { dateFilterMenu, dateFilterValueLabel, durationFilterOptions, releasedDateFilterCategory } from './issue-date-filter'
import { matchesExplorerFilter } from './issue-explorer-model'
import { filterToQueryNode } from './issue-filter-query'

class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

const labels = (options: MyIssuesFilterOption[] = []) => options.map(option => option.label)
const PAST = ['1 day ago', '3 days ago', '1 week ago', '1 month ago', '3 months ago', '6 months ago', '1 year ago', 'Custom date or timeframe…']

describe("the reference app's date filter presets", () => {
  it('offers every Dates category with the reference app presets (Released date only with release pipelines)', () => {
    const menu = dateFilterMenu()
    expect(labels(menu)).toEqual(['Due date', 'Created date', 'Updated date', 'Started date', 'Completed date', 'Auto-closed date', 'Triaged date', 'Time in current status'])
    const children = (id: string, source = menu) => labels(source.find(option => option.id === id)?.children)
    expect(children('due-date')).toEqual(['Overdue', '1 day from now', '3 days from now', '1 week from now', '1 month from now', '3 months from now', 'Custom date or timeframe…', 'No due date'])
    for (const id of ['created-date', 'updated-date', 'started-date', 'auto-closed-date', 'triaged-date']) expect(children(id)).toEqual(PAST)
    expect(children('completed-date')).toEqual(['No completed date', ...PAST])
    expect(children('time-current-status')).toEqual(['1 day', '1 week', '2 weeks', '1 month', '3 months', '6 months'])
    const withReleases = dateFilterMenu({ releasedDate: true })
    expect(labels(withReleases)).toEqual(['Due date', 'Created date', 'Updated date', 'Started date', 'Completed date', 'Auto-closed date', 'Released date', 'Triaged date', 'Time in current status'])
    expect(children('released-date', withReleases)).toEqual(['No released date', ...PAST])
    expect(releasedDateFilterCategory().children?.map(option => option.id)).toEqual(['no-released-date', 'released:-1d', 'released:-3d', 'released:-1w', 'released:-1m', 'released:-3m', 'released:-6m', 'released:-1y', 'released-custom'])
  })

  it('reads typed durations under Time in current status and labels stored values', () => {
    expect(durationFilterOptions('status:', '2 days').map(option => [option.id, option.label])).toEqual([['status:-2d', '2 days']])
    expect(durationFilterOptions('status:', '3w')[0].id).toBe('status:-3w')
    expect(durationFilterOptions('status:', '10 months')[0].label).toBe('10 months')
    expect(durationFilterOptions('status:', '2 天')[0].id).toBe('status:-2d')
    expect(durationFilterOptions('status:', 'soon')).toEqual([])
    expect(durationFilterOptions('status:', '0 days')).toEqual([])
    expect(dateFilterValueLabel('status:-2d')).toBe('2 days')
    expect(dateFilterValueLabel('released:-1w')).toBe('1 week ago')
    expect(dateFilterValueLabel('released:-2w')).toBe('2 weeks ago')
    expect(dateFilterValueLabel('released:2026-10-01')).toBe('2026-10-01')
    expect(dateFilterValueLabel('autoClosed:-6m')).toBe('6 months ago')
  })
})

describe('Released date chips', () => {
  const option = (id: string, label: string): MyIssuesFilterOption => ({ id, label, kind: 'dueDate', filterLabel: 'Released date' })
  it('applies one before/after chip per Released date, replacing the preset and serializing through ?filter=', () => {
    let filters = toggleFilterOption([], 'releases', 'Releases', option('released:-1w', '1 week ago'))
    expect(filters).toHaveLength(1)
    expect(filters[0]).toMatchObject({ field: 'releases', fieldLabel: 'Released date', operator: 'after', value: 'released:-1w' })
    expect(filterOperatorChoices('releases', ['released:-1w']).map(choice => choice.label)).toEqual(['before', 'after'])
    // A release value next to it stays its own chip.
    filters = toggleFilterOption(filters, 'releases', 'Releases', { id: 'release:r1', label: '1.0', filterLabel: 'Release' })
    filters = toggleFilterOption(filters, 'releases', 'Releases', option('released:-3m', '3 months ago'))
    expect(filters.map(filter => [filter.fieldLabel, filter.value, filter.operator])).toEqual([['Released date', 'released:-3m', 'after'], ['Release', 'release:r1', 'is']])
    const restored = decodeFiltersParam(encodeFiltersParam(filters))
    expect(restored).toEqual(filters)
    const chip = filterChipItem({ ...restored[0], operator: 'before' }, releasedDateFilterCategory().children)
    expect(chip.values.map(value => value.label)).toEqual(['3 months ago'])
    expect(chip.operatorChoices?.map(choice => choice.operator)).toEqual(['before', 'after'])
    // "No released date" is an is / is not chip.
    const none = toggleFilterOption([], 'releases', 'Releases', option('no-released-date', 'No released date'))
    expect(none[0].operator).toBe('is')
    expect(filterOperatorChoices('releases', ['no-released-date']).map(choice => choice.operator)).toEqual(['is', 'isNot'])
  })

  it('matches client rows and the server query the same way (any release; strict before / after)', () => {
    const now = Date.parse('2026-10-10T12:00:00Z')
    const row = (id: string, releasedDates: string[]) => ({ id, releasedDates, releaseIds: releasedDates.length ? ['r'] : [] }) as unknown as MyIssuesRowData
    const rows = [row('old', ['2026-09-01T00:00:00Z']), row('both', ['2026-09-01T00:00:00Z', '2026-10-09T00:00:00Z']), row('recent', ['2026-10-09T00:00:00Z']), row('none', [])]
    const chip = (field: MyIssuesFilterKey, value: string, operator: MyIssuesAppliedFilter['operator']): MyIssuesAppliedFilter => ({ id: 'f', field, fieldLabel: 'Released date', operator, value, valueLabel: value, values: [{ value, valueLabel: value }] })
    const match = (filter: MyIssuesAppliedFilter) => rows.filter(item => matchesExplorerFilter(item, filter, now)).map(item => item.id)
    expect(match(chip('releases', 'released:-1w', 'after'))).toEqual(['both', 'recent'])
    expect(match(chip('releases', 'released:-1w', 'before'))).toEqual(['old', 'both'])
    expect(match(chip('dates', 'released:-1w', 'after'))).toEqual(['both', 'recent'])
    expect(match(chip('releases', 'no-released-date', 'is'))).toEqual(['none'])
    expect(match(chip('dates', 'no-released-date', 'isNot'))).toEqual(['old', 'both', 'recent'])
    expect(filterToQueryNode(chip('releases', 'released:-1w', 'before'), { data: { projects: [], releases: [], viewer: { id: 'me' } } as never, now })).toEqual({ field: 'releasedAt', operator: 'before', values: ['2026-10-03T12:00:00.000Z'] })
  })
})

describe('filter menu', () => {
  const releases: MyIssuesFilterOption[] = [
    { id: 'release', label: 'Release', children: [{ id: 'release:r1', label: '1.0' }] },
    { id: 'release-pipeline', label: 'Release pipeline', children: [{ id: 'release-pipeline:p1', label: 'Web' }] },
    releasedDateFilterCategory(),
    { id: 'no-releases', label: 'No releases', count: 3 },
  ]
  const options = (field: MyIssuesFilterKey) => field === 'dates' ? dateFilterMenu({ releasedDate: true }) : field === 'releases' ? releases : undefined

  it('opens Releases › Released date presets and applies them as Released date chips', async () => {
    const onToggle = vi.fn()
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={onToggle}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Releases/ }))
    const list = await screen.findByRole('listbox', { name: 'Releases' })
    // The reference app: icon rows, no search band, "No releases" without a checkbox.
    expect(within(list).getAllByRole('option').map(row => row.textContent?.replace('▶', '').trim())).toEqual(['Release', 'Release pipeline', 'Released date', 'No releases'])
    for (const row of within(list).getAllByRole('option')) expect(row.querySelector('svg'), row.textContent ?? '').not.toBeNull()
    expect(within(list).queryByRole('checkbox')).toBeNull()
    fireEvent.mouseMove(within(list).getByRole('option', { name: /Released date/ }))
    const presets = await screen.findByRole('listbox', { name: 'Released date' })
    expect(within(presets).getAllByRole('option').map(row => row.textContent)).toEqual(['No released date', ...PAST])
    fireEvent.click(within(presets).getByRole('option', { name: '1 week ago' }))
    expect(onToggle).toHaveBeenCalledWith('releases', expect.objectContaining({ id: 'released:-1w', filterLabel: 'Released date' }))
  })

  it('offers typed durations under Time in current status ("Try: 2 days, 3 weeks…")', async () => {
    const onToggle = vi.fn()
    const user = userEvent.setup()
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={onToggle}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: /^Dates/ }))
    const dates = await screen.findByRole('listbox', { name: 'Dates' })
    expect(within(dates).getAllByRole('option').map(row => row.textContent?.replace('▶', '').trim())).toContain('Released date')
    fireEvent.mouseMove(within(dates).getByRole('option', { name: /Time in current status/ }))
    const search = await screen.findByPlaceholderText('Try: 2 days, 3 weeks…')
    await user.type(search, '10 days')
    const list = screen.getByRole('listbox', { name: 'Time in current status' })
    fireEvent.click(within(list).getByRole('option', { name: '10 days' }))
    expect(onToggle).toHaveBeenCalledWith('dates', expect.objectContaining({ id: 'status:-10d', filterLabel: 'Time in current status' }))
  })

  it('shows the presets in Chinese', async () => {
    await loadChineseDictionary()
    localStorage.setItem('flow:locale', 'zh-CN')
    render(<I18nProvider><MyIssuesFilterMenu open trigger={<button type="button">Filter</button>} options={options} onOpenChange={vi.fn()} onToggle={vi.fn()}/></I18nProvider>)
    fireEvent.mouseMove(await screen.findByRole('option', { name: new RegExp(`^${translateToChinese('Releases')}`) }))
    const list = await screen.findByRole('listbox', { name: 'Releases' })
    fireEvent.mouseMove(within(list).getByRole('option', { name: /发布日期/ }))
    const presets = await screen.findByRole('listbox', { name: '发布日期' })
    expect(within(presets).getAllByRole('option').map(row => row.textContent)).toEqual(['无发布日期', '1 天前', '3 天前', '1 周前', '1 个月前', '3 个月前', '6 个月前', '1 年前', '自定义日期或时间范围…'])
    expect(translateToChinese('10 days')).toBe('10 天')
    expect(translateToChinese('2 weeks ago')).toBe('2 周前')
  })
})
