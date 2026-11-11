import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, SLARule } from '@/types/flow'
import { createSLARule, deleteSLARule, reorderSLARules, updateSLARule, updateSLASettings } from '@/lib/api'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { SLASettings } from './sla-settings'

vi.mock('@/lib/api', () => ({
  createSLARule: vi.fn(async () => ({})),
  updateSLARule: vi.fn(async () => ({})),
  deleteSLARule: vi.fn(async () => undefined),
  reorderSLARules: vi.fn(async () => []),
  updateSLASettings: vi.fn(async () => ({})),
}))
vi.mock('@/lib/toast', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn(async () => true) }))

function rule(id: string, overrides: Partial<SLARule> = {}): SLARule {
  return { id, name: id, teamIds: [], filters: {}, targetMinutes: 1440, pauseStatuses: [], businessHours: false, enabled: true, createdAt: '', updatedAt: '', ...overrides }
}
// The reference rule set: urgent and high issues get a deadline, the others have it removed.
const RULES = [
  rule('urgent', { filters: { priority: 1 }, action: 'add', targetMinutes: 1440 }),
  rule('high', { filters: { priority: 2 }, action: 'add', targetMinutes: 10080 }),
  rule('medium', { filters: { priority: 3 }, action: 'remove' }),
]

function data(overrides: Partial<BootstrapData> = {}): BootstrapData {
  return makeBootstrap({ settings: { sla: { enabled: true, workWeek: 'monFri' } }, slaRules: RULES, ...overrides } as Partial<BootstrapData>)
}
function renderPage(value = data(), onReload = vi.fn(async () => {})) {
  render(<I18nProvider><SLASettings data={value} onReload={onReload} /></I18nProvider>)
  return onReload
}
const cards = () => screen.getAllByRole('listitem')

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
})

describe('SLASettings', () => {
  it('saves the issue creation clock and displays it on existing rules', async () => {
    const user = userEvent.setup()
    renderPage(data({ slaRules: [rule('creation', { startMode: 'issueCreation' })] }))
    expect(cards()[0]).toHaveTextContent('of 24 hours from issue creation')
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    const dialog = screen.getByRole('dialog', { name: 'Add rule' })
    await user.click(within(dialog).getByRole('combobox', { name: 'Start counting' }))
    await user.click(await screen.findByRole('option', { name: 'from issue creation' }))
    await user.click(within(dialog).getByRole('button', { name: 'Add rule' }))
    await waitFor(() => expect(createSLARule).toHaveBeenCalledWith(expect.objectContaining({ startMode: 'issueCreation' })))
  })
  it('reads in Chinese: rule sentences, durations and the rule dialog', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    renderPage(data({ slaRules: [...RULES, rule('weeks', { filters: { priority: 4 }, targetMinutes: 7200, businessHours: true })] }))
    expect(screen.getByText('自动化规则')).toBeInTheDocument()
    const [urgent, , medium, business] = cards()
    expect(urgent).toHaveTextContent('当优先级是紧急则添加 SLA时限 24 小时，自升级起计')
    expect(medium).toHaveTextContent('则移除 SLA')
    expect(business).toHaveTextContent('时限 1 个工作周，自升级起计')
    await user.click(screen.getByRole('button', { name: '添加规则' }))
    const dialog = await screen.findByRole('dialog', { name: '添加规则' })
    expect(within(dialog).getByRole('button', { name: '添加条件' })).toBeInTheDocument()
    expect(within(dialog).getByText('自升级起计')).toBeInTheDocument()
  })

  it('lays out the header, the SLAs card and one card per rule in order', () => {
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'SLAs' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', 'https://flow.app/docs/sla')
    expect(screen.getByRole('heading', { level: 3, name: 'Automation rules' })).toBeInTheDocument()
    expect(screen.getByText('Use automation rules to automatically add or remove SLAs based on filters.')).toBeInTheDocument()
    expect(cards().map(card => card.getAttribute('data-rule-id'))).toEqual(['urgent', 'high', 'medium'])
  })

  it('reads each rule as "When <property> is <value> Then <action>"', () => {
    renderPage()
    const [urgent, high, medium] = cards()
    expect(urgent).toHaveTextContent('WhenPriorityisUrgentThenAdd SLAof 24 hours from escalation')
    expect(high).toHaveTextContent('WhenPriorityisHighThenAdd SLAof 1 week from escalation')
    expect(medium).toHaveTextContent('WhenPriorityisMediumThenRemove SLA')
    expect(within(urgent).getByRole('button', { name: 'Edit configuration' })).toBeInTheDocument()
    expect(within(urgent).getByRole('button', { name: 'Delete configuration' })).toBeInTheDocument()
  })

  it('shows a rule written before actions existed as one that adds an SLA, in business time when it was', () => {
    renderPage(data({ slaRules: [rule('old', { filters: { priority: 2 }, targetMinutes: 4320, businessHours: true, teamIds: ['team-1'] })] } as never))
    expect(cards()[0]).toHaveTextContent('Add SLAof 3 business days from escalation')
  })

  it('does not list the teams an older rule was saved with as a condition', () => {
    const base = data()
    renderPage({ ...base, slaRules: [rule('old', { teamIds: base.teams.map(team => team.id) })] })
    expect(cards()[0]).toHaveTextContent('WhenAny issue')
  })

  it('switches SLAs on and off from the card toggle', async () => {
    const user = userEvent.setup()
    const onReload = renderPage()
    await user.click(screen.getByRole('checkbox', { name: 'Enable SLAs' }))
    expect(updateSLASettings).toHaveBeenCalledWith({ enabled: false })
    await waitFor(() => expect(onReload).toHaveBeenCalled())
  })

  it('offers the work week only while SLAs are on, and greys the rules out when they are off', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<I18nProvider><SLASettings data={data()} onReload={vi.fn(async () => {})} /></I18nProvider>)
    expect(screen.getByRole('combobox', { name: 'Work week' })).toBeInTheDocument()
    await user.click(screen.getByRole('combobox', { name: 'Work week' }))
    await user.click(await screen.findByRole('option', { name: 'Sun-Thu' }))
    expect(updateSLASettings).toHaveBeenCalledWith({ workWeek: 'sunThu' })
    unmount()
    renderPage(data({ settings: { sla: { enabled: false } } } as never))
    expect(screen.queryByRole('combobox', { name: 'Work week' })).not.toBeInTheDocument()
    expect(document.getElementById('automation-rules')).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add rule' })).toBeDisabled()
    for (const button of screen.getAllByRole('button', { name: 'Edit configuration' })) expect(button).toBeDisabled()
  })

  it('says so when there are no rules, and what to do next', () => {
    renderPage(data({ slaRules: [] }))
    expect(screen.getByText('No automation rules')).toBeInTheDocument()
    expect(screen.getByText('Add a rule to begin applying SLAs.')).toBeInTheDocument()
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
  })

  it('creates a rule from the dialog: conditions, an action and a duration', async () => {
    const user = userEvent.setup()
    const onReload = renderPage()
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add rule' })
    await user.click(within(dialog).getByRole('combobox', { name: 'Priority' }))
    await user.click(await screen.findByRole('option', { name: /High/ }))
    const amount = within(dialog).getByRole('textbox', { name: 'Amount' })
    await user.clear(amount)
    await user.type(amount, '3')
    await user.click(within(dialog).getByRole('combobox', { name: 'Unit' }))
    await user.click(await screen.findByRole('option', { name: 'Days' }))
    await user.click(within(dialog).getByRole('button', { name: 'Add rule' }))
    await waitFor(() => expect(createSLARule).toHaveBeenCalledTimes(1))
    expect(createSLARule).toHaveBeenCalledWith({
      name: 'Priority is High', filters: { priority: 2 }, teamIds: [], action: 'add', enabled: true, businessHours: false, targetMinutes: 4320,
    })
    await waitFor(() => expect(onReload).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('creates a rule that removes the SLA without a duration', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add rule' })
    await user.click(within(dialog).getByRole('combobox', { name: 'Action' }))
    await user.click(await screen.findByRole('option', { name: 'Remove SLA' }))
    expect(within(dialog).queryByRole('textbox', { name: 'Amount' })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Add rule' }))
    await waitFor(() => expect(createSLARule).toHaveBeenCalled())
    const input = vi.mocked(createSLARule).mock.calls[0][0]
    expect(input).toMatchObject({ action: 'remove', filters: { priority: 1 } })
    expect(input).not.toHaveProperty('targetMinutes')
  })

  it('refuses a duration that is not a whole number of 1 or more', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add rule' })
    const amount = within(dialog).getByRole('textbox', { name: 'Amount' })
    for (const bad of ['0', '1.5', 'abc', '']) {
      await user.clear(amount)
      if (bad) await user.type(amount, bad)
      expect(within(dialog).getByRole('button', { name: 'Add rule' })).toBeDisabled()
    }
    expect(createSLARule).not.toHaveBeenCalled()
    await user.clear(amount)
    await user.type(amount, '12')
    expect(within(dialog).getByRole('button', { name: 'Add rule' })).toBeEnabled()
  })

  it('adds a second condition of another kind and removes it again', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole('button', { name: 'Add rule' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add rule' })
    expect(within(dialog).queryByRole('button', { name: 'Remove condition' })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Add condition' }))
    expect(within(dialog).getAllByRole('combobox', { name: 'Condition property' })).toHaveLength(2)
    expect(within(dialog).getAllByRole('combobox', { name: 'Condition property' })[1]).toHaveTextContent('Team')
    await user.click(within(dialog).getAllByRole('button', { name: 'Remove condition' })[1])
    expect(within(dialog).getAllByRole('combobox', { name: 'Condition property' })).toHaveLength(1)
  })

  it('edits a rule in place, keeping the rule name out of the form', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(within(cards()[0]).getByRole('button', { name: 'Edit configuration' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit rule' })
    expect(within(dialog).getByRole('textbox', { name: 'Amount' })).toHaveValue('24')
    expect(within(dialog).getByRole('checkbox', { name: 'Rule enabled' })).toBeChecked()
    const amount = within(dialog).getByRole('textbox', { name: 'Amount' })
    await user.clear(amount)
    await user.type(amount, '48')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(updateSLARule).toHaveBeenCalledTimes(1))
    expect(updateSLARule).toHaveBeenCalledWith('urgent', expect.objectContaining({ action: 'add', targetMinutes: 2880, filters: { priority: 1 } }))
  })

  it('asks before deleting a rule', async () => {
    const user = userEvent.setup()
    const onReload = renderPage()
    await user.click(within(cards()[1]).getByRole('button', { name: 'Delete configuration' }))
    expect(confirmAction).toHaveBeenCalledWith('Delete this SLA?', expect.objectContaining({ confirmLabel: 'Delete' }))
    await waitFor(() => expect(deleteSLARule).toHaveBeenCalledWith('high'))
    await waitFor(() => expect(onReload).toHaveBeenCalled())
  })

  it('keeps a rule when the delete is not confirmed', async () => {
    vi.mocked(confirmAction).mockResolvedValueOnce(false)
    const user = userEvent.setup()
    renderPage()
    await user.click(within(cards()[1]).getByRole('button', { name: 'Delete configuration' }))
    await waitFor(() => expect(confirmAction).toHaveBeenCalled())
    expect(deleteSLARule).not.toHaveBeenCalled()
  })

  it('moves a rule with Alt + arrow keys and saves the new order', async () => {
    const user = userEvent.setup()
    renderPage()
    cards()[1].focus()
    await user.keyboard('{Alt>}{ArrowUp}{/Alt}')
    await waitFor(() => expect(reorderSLARules).toHaveBeenCalledWith(['high', 'urgent', 'medium']))
  })

  it('does not move a rule past either end of the list', async () => {
    const user = userEvent.setup()
    renderPage()
    cards()[0].focus()
    await user.keyboard('{Alt>}{ArrowUp}{/Alt}')
    cards()[2].focus()
    await user.keyboard('{Alt>}{ArrowDown}{/Alt}')
    expect(reorderSLARules).not.toHaveBeenCalled()
  })
})
