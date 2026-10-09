import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { defaultScheduleConfig } from './loop-model'
import { LoopTriggerEditor } from './loop-trigger'

/** The loop page's read-only schedule line, split into its visible segments. */
function scheduleSegments(config: Record<string, unknown>) {
  const { container } = render(
    <I18nProvider>
      <LoopTriggerEditor data={makeBootstrap()} triggerType="schedule" config={config} readOnly />
    </I18nProvider>,
  )
  const sentence = container.querySelector('.loops-trigger-sentence')!
  return { sentence, segments: [...sentence.querySelectorAll('.loops-sentence-text, .loops-sentence-token, .loops-weekday, .loops-sentence-subject')].map(node => node.textContent) }
}

afterEach(() => {
  cleanup()
  localStorage.clear()
})

describe('loop page schedule line', () => {
  it('ends at the time for a loop created in the UI (default schedule)', () => {
    const { segments, sentence } = scheduleSegments({ ...defaultScheduleConfig(new Date(2026, 8, 29)) })
    expect(segments).toEqual(['Schedule', 'Starting', '09/29/2026', 'every', '1', 'day', 'at', '7AM'])
    expect(sentence.querySelector('.loops-weekdays')).toBeNull()
  })

  it('ignores API-only fields and drops an empty weekday group instead of a dangling "On"', () => {
    const { segments, sentence } = scheduleSegments({ startDate: '2026-09-29', interval: 2, unit: 'week', time: '09:30', timezone: 'Asia/Shanghai', weekdays: [] })
    expect(segments).toEqual(['Schedule', 'Starting', '09/29/2026', 'every', '2', 'weeks', 'at', '9:30AM'])
    expect(sentence.querySelector('.loops-weekdays')).toBeNull()
    expect(sentence).not.toHaveTextContent('—')
  })

  it('lists only the chosen weekdays for a weekly schedule', () => {
    const { segments } = scheduleSegments({ startDate: '2026-09-29', interval: 1, unit: 'week', time: '07:00', weekdays: ['mon', 'fri'] })
    expect(segments.slice(-3)).toEqual(['On', 'Mo', 'Fr'])
  })

  it('labels weekdays with two characters in Chinese so Monday never reads as a stray dash', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const { segments, sentence } = scheduleSegments({ startDate: '2026-09-29', interval: 1, unit: 'week', time: '07:00', weekdays: ['mon'] })
    expect(segments.at(-1)).toBe('周一')
    expect(sentence.querySelector('.loops-weekday')?.textContent?.trim().length).toBeGreaterThan(1)
  })
})
