import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { loadChineseDictionary } from '@/i18n/translate'
import { LOCALE_STORAGE_KEY } from '@/i18n/locale'
import { slaDisplayText, slaTimeLeft } from '@/lib/issue-sla-copy'
import type { IssueSLA } from '@/types/flow'
import { IssueSLAIndicator } from './issue-sla-indicator'

const now = Date.parse('2026-11-10T12:00:00Z')
const sla = (patch: Partial<IssueSLA> = {}): IssueSLA => ({ id: 'sla', issueId: 'issue', ruleId: 'rule', startedAt: new Date(now - 3_600_000).toISOString(), dueAt: new Date(now + 3_600_000).toISOString(), pausedMinutes: 0, remainingMinutes: 60, status: 'active', ...patch })

afterEach(() => { vi.useRealTimers(); localStorage.removeItem(LOCALE_STORAGE_KEY) })

describe('issue SLA countdown', () => {
  it('moves through the risk and breach boundaries without waiting for a server sweep', () => {
    vi.useFakeTimers(); vi.setSystemTime(now)
    const { container } = render(<IssueSLAIndicator sla={sla({ dueAt: new Date(now + 30_000).toISOString() })}/>)
    expect(container.querySelector('.issue-sla')).toHaveAttribute('data-status', 'high')
    expect(screen.getByText('<1min remaining')).toBeVisible()
    act(() => vi.advanceTimersByTime(30_000))
    expect(container.querySelector('.issue-sla')).toHaveAttribute('data-status', 'breached')
    expect(screen.getByText('Breached by <1min')).toBeVisible()
  })

  it('uses the completed time for achieved and failed results', () => {
    expect(slaDisplayText(sla({ status: 'completed', completedAt: new Date(now).toISOString() }), now + 100_000_000, false, value => value)).toBe('Achieved 1h early')
    expect(slaDisplayText(sla({ status: 'completed', completedAt: new Date(now + 7_200_000).toISOString() }), now, false, value => value)).toBe('Failed by 1h')
  })

  it('keeps paused time stable and omits removed instances', () => {
    expect(slaDisplayText(sla({ status: 'paused', remainingMinutes: 90 }), now + 100_000_000, false, value => value)).toBe('Paused · 1h 30min')
    const { container } = render(<IssueSLAIndicator sla={sla({ status: 'removed' })}/>)
    expect(container).toBeEmptyDOMElement()
  })

  it('exposes the deadline tooltip to keyboard focus', async () => {
    render(<IssueSLAIndicator sla={sla()} compact/>)
    fireEvent.focus(screen.getByLabelText(/SLA:/))
    expect(await screen.findByRole('tooltip')).toHaveTextContent(/(?:Was due|Due) on/)
  })

  it('localizes the countdown without translating a rule name', async () => {
    await loadChineseDictionary()
    localStorage.setItem(LOCALE_STORAGE_KEY, 'zh-CN')
    vi.useFakeTimers(); vi.setSystemTime(now)
    render(<I18nProvider><IssueSLAIndicator sla={sla()} ruleName="Flow parity 超长客户规则"/></I18nProvider>)
    expect(screen.getByText('剩余 1小时')).toBeVisible()
    expect(screen.getByText('Flow parity 超长客户规则')).toBeInTheDocument()
  })

  it('formats short and long countdowns with minute precision', () => {
    expect(slaTimeLeft(59_999)).toBe('<1min')
    expect(slaTimeLeft(60_000)).toBe('1min')
    expect(slaTimeLeft(90_000_000)).toBe('1d 1h')
  })
})
