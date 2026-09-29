import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { formatScheduleDate, formatTime12, relativeTime, triggerSummary } from './loop-model'
import { LoopTriggerEditor } from './loop-trigger'

function renderTrigger(props: Partial<Parameters<typeof LoopTriggerEditor>[0]> = {}) {
  const onChange = vi.fn()
  const data = makeBootstrap()
  const view = render(
    <I18nProvider>
      <LoopTriggerEditor data={data} triggerType="issue" config={{ event: 'status' }} onChange={onChange} {...props}/>
    </I18nProvider>,
  )
  return { onChange, view }
}

describe('LoopTriggerEditor', () => {
  it('renders a status event as Linear’s sentence', () => {
    const { view } = renderTrigger()
    const sentence = view.container.querySelector('.loops-trigger-sentence')!
    expect(sentence).toHaveTextContent('Issue')
    expect(sentence).toHaveTextContent('status is set')
    expect(sentence).toHaveTextContent('to')
    expect(within(sentence as HTMLElement).getByRole('button', { name: 'Status' })).toHaveTextContent('Any status')
    expect(within(sentence as HTMLElement).getByRole('button', { name: 'Teams' })).toHaveTextContent('Select teams…')
  })

  it('renders the triage template trigger with its filter chip', () => {
    const { view } = renderTrigger({ config: { event: 'triage', filters: [{ field: 'assignee', operator: 'is', value: null }] } })
    const sentence = view.container.querySelector('.loops-trigger-sentence')!
    expect(sentence).toHaveTextContent('An issue')
    expect(sentence).toHaveTextContent('is in triage')
    const chip = view.container.querySelector('.loops-filter-chip-row')!
    expect(chip).toHaveTextContent('Assignee')
    expect(chip).toHaveTextContent('is')
    expect(chip).toHaveTextContent('No assignee')
    expect(screen.getByRole('button', { name: 'Add filter' })).toBeVisible()
  })

  it('toggles the filter operator and removes filters', async () => {
    const user = userEvent.setup()
    const { onChange } = renderTrigger({ config: { event: 'created', filters: [{ field: 'assignee', operator: 'is', value: null }] } })
    await user.click(screen.getByRole('button', { name: 'is' }))
    expect(onChange).toHaveBeenLastCalledWith('issue', expect.objectContaining({ filters: [{ field: 'assignee', operator: 'isNot', value: null }] }))
    await user.click(screen.getByRole('button', { name: 'Remove filter' }))
    expect(onChange).toHaveBeenLastCalledWith('issue', expect.objectContaining({ filters: [] }))
  })

  it('picks Issue ▸ Status ▸ Triage from the trigger type menu', async () => {
    const user = userEvent.setup()
    const { onChange } = renderTrigger({ triggerType: 'schedule', config: { startDate: '2026-09-29', interval: 1, unit: 'day', time: '07:00' } })
    await user.click(screen.getByRole('button', { name: 'Trigger type' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: 'Schedule' })).toBeVisible()
    for (const name of ['Project', 'Initiative', 'Release', 'Team']) expect(within(menu).getByRole('menuitem', { name })).toBeVisible()
    await user.click(within(menu).getByRole('menuitem', { name: 'Issue' }))
    await user.keyboard('{ArrowRight}')
    expect(await screen.findByPlaceholderText('Filter…')).toBeVisible()
    for (const name of ['Created', 'Property updated', 'Priority', 'Assignee', 'Agent', 'Labels', 'New comment', 'New customer request']) expect(screen.getByRole('menuitem', { name })).toBeVisible()
    screen.getByRole('menuitem', { name: 'Status' }).focus()
    await user.keyboard('{ArrowRight}')
    expect(await screen.findByText('Set to…')).toBeVisible()
    expect(screen.getByRole('menuitem', { name: 'Any status' })).toBeVisible()
    screen.getByRole('menuitem', { name: 'Triage' }).focus()
    await user.keyboard('{Enter}')
    expect(onChange).toHaveBeenCalledWith('issue', { event: 'triage', teamIds: undefined, filters: undefined })
  })

  it('renders the schedule row with 12-hour times and weekly day chips', async () => {
    const user = userEvent.setup()
    const { onChange, view } = renderTrigger({ triggerType: 'schedule', config: { startDate: '2026-09-29', interval: 1, unit: 'week', time: '07:00', weekdays: ['mon'] } })
    const sentence = view.container.querySelector('.loops-trigger-sentence')!
    expect(sentence).toHaveTextContent('Starting')
    expect(screen.getByRole('button', { name: 'Start date' })).toHaveTextContent('09/29/2026')
    expect(screen.getByRole('spinbutton', { name: 'Interval' })).toHaveValue(1)
    expect(screen.getByRole('combobox', { name: 'Time' })).toHaveTextContent('7AM')
    const days = screen.getByRole('group', { name: 'Weekdays' })
    expect(within(days).getAllByRole('button').map(button => button.textContent)).toEqual(['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'])
    expect(within(days).getByRole('button', { name: 'Mo' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(within(days).getByRole('button', { name: 'Fr' }))
    expect(onChange).toHaveBeenLastCalledWith('schedule', expect.objectContaining({ weekdays: ['mon', 'fri'] }))
  })

  it('keeps Cycle out of the trigger menu for new loops', async () => {
    const user = userEvent.setup()
    renderTrigger({ triggerType: 'schedule', config: { startDate: '2026-09-29', interval: 1, unit: 'day', time: '07:00' } })
    await user.click(screen.getByRole('button', { name: 'Trigger type' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).queryByRole('menuitem', { name: 'Cycle' })).toBeNull()
  })

  it('renders and edits an existing cycle loop', async () => {
    const user = userEvent.setup()
    const { onChange, view } = renderTrigger({ triggerType: 'cycle', config: { event: 'started', teamIds: ['team-1'] } })
    const sentence = view.container.querySelector('.loops-trigger-sentence')!
    expect(sentence).toHaveTextContent('Cycle')
    expect(sentence).toHaveTextContent('starts')
    await user.click(screen.getByRole('button', { name: 'Trigger type' }))
    const menu = await screen.findByRole('menu')
    await user.click(within(menu).getByRole('menuitem', { name: 'Cycle' }))
    await user.keyboard('{ArrowRight}')
    ;(await screen.findByRole('menuitem', { name: 'Completed' })).focus()
    await user.keyboard('{Enter}')
    expect(onChange).toHaveBeenLastCalledWith('cycle', { event: 'completed', teamIds: ['team-1'] })
  })

  it('renders a read-only cycle trigger', () => {
    const { view } = renderTrigger({ readOnly: true, onChange: undefined, triggerType: 'cycle', config: { event: 'completed' } })
    expect(view.container.querySelector('.loops-trigger-sentence')).toHaveTextContent('Cycleis completed')
  })

  it('renders a read-only summary for the loop page', () => {
    const { view } = renderTrigger({ readOnly: true, onChange: undefined, config: { event: 'status', value: 'Done', teamIds: ['team-1'] } })
    expect(view.container.querySelector('.loops-trigger-sentence')).toHaveTextContent('Issuestatus is settoDoneinTest team')
    expect(screen.queryByRole('button')).toBeNull()
  })
})

describe('loop model', () => {
  it('formats Linear schedule values', () => {
    expect(formatTime12('07:00')).toBe('7AM')
    expect(formatTime12('13:30')).toBe('1:30PM')
    expect(formatTime12('00:00')).toBe('12AM')
    expect(formatScheduleDate('2026-09-29')).toBe('09/29/2026')
  })

  it('summarizes triggers for the list', () => {
    expect(triggerSummary({ triggerType: 'issue', triggerConfig: { event: 'triage' } })).toBe('Triage')
    expect(triggerSummary({ triggerType: 'issue', triggerConfig: { action: 'created' } })).toBe('Issue created')
    expect(triggerSummary({ triggerType: 'schedule', triggerConfig: { unit: 'week', interval: 1 } })).toBe('Weekly')
    expect(triggerSummary({ triggerType: 'schedule', triggerConfig: { unit: 'day', interval: 3 } })).toBe('Every 3 days')
    expect(triggerSummary({ triggerType: 'project', triggerConfig: { event: 'update' } })).toBe('New project update')
  })

  it('formats relative times', () => {
    const now = Date.parse('2026-09-29T12:00:00Z')
    expect(relativeTime('2026-09-29T11:59:30Z', now)).toBe('just now')
    expect(relativeTime('2026-09-29T11:00:00Z', now)).toBe('1h ago')
    expect(relativeTime('2026-09-28T11:00:00Z', now)).toBe('Yesterday')
  })
})
