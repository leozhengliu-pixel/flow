import { describe, expect, it } from 'vitest'
import { resolveSnoozeDate, snoozePresetItems } from './snooze-presets'

describe('snooze presets', () => {
  it('offers "This evening" only while the working day lasts', () => {
    expect(snoozePresetItems(new Date(2026, 10, 4, 6, 57)).map(([id]) => id)).toEqual(['hour', 'threeHours', 'evening', 'tomorrow', 'nextWeek', 'month'])
    expect(snoozePresetItems(new Date(2026, 10, 4, 17, 0)).map(([id]) => id)).toEqual(['hour', 'threeHours', 'tomorrow', 'nextWeek', 'month'])
  })

  it('resolves this evening to 18:00 today', () => {
    const date = resolveSnoozeDate(new Date(2026, 10, 4, 6, 57), 'evening')
    expect([date.getDate(), date.getHours(), date.getMinutes()]).toEqual([4, 18, 0])
  })
})
