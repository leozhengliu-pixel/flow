import { DESKTOP_LAYOUT_QUERY } from '@/lib/layout-breakpoints'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'

import { DueDateCommand, DueDatePicker } from './due-date-picker'

describe('DueDateCommand', () => {
  it('keeps the desktop end-of-week preset on Friday and applies it', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-30T12:00:00'))
    const media = vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query === DESKTOP_LAYOUT_QUERY, addEventListener: vi.fn(), removeEventListener: vi.fn() }) as unknown as MediaQueryList)
    try {
      const onSelect = vi.fn().mockResolvedValue(undefined)
      render(<I18nProvider><DueDateCommand onSelect={onSelect}/></I18nProvider>)
      fireEvent.click(screen.getByRole('option', { name: /End of this week/ }))
      expect(onSelect).toHaveBeenCalledWith('2026-10-30')
    } finally { media.mockRestore(); vi.useRealTimers() }
  })
  it('offers "Remove due date" only when removing is allowed', () => {
    const onSelect = vi.fn().mockResolvedValue(undefined)
    const view = render(<I18nProvider><DueDateCommand value="2026-10-10" onSelect={onSelect}/></I18nProvider>)
    expect(screen.getByRole('option', { name: /Remove due date/ })).toBeInTheDocument()
    // A recurring issue's due date anchors its schedule: the API refuses to clear it.
    view.rerender(<I18nProvider><DueDateCommand value="2026-10-10" allowRemove={false} onSelect={onSelect}/></I18nProvider>)
    expect(screen.queryByRole('option', { name: /Remove due date/ })).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Tomorrow/ })).toBeInTheDocument()
  })

  it('opens the shared phone sheet instead of an anchored popover on a phone, with the extra rows', () => {
    const media = vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query === '(max-width: 767px)', addEventListener: vi.fn(), removeEventListener: vi.fn() }) as unknown as MediaQueryList)
    try {
      const onChange = vi.fn().mockResolvedValue(undefined)
      render(<I18nProvider><DueDatePicker value="2026-10-10" onChange={onChange} extraOptions={[{ label: 'End of this cycle', value: '2026-11-04' }]}/></I18nProvider>)
      fireEvent.click(screen.getByRole('button', { name: /Change due date/ }))
      const sheet = document.querySelector('.phone-menu-surface')!
      expect(sheet).toHaveAttribute('data-phone-popover')
      expect(document.querySelector('.due-date-popover')).toBeNull()
      expect(sheet.querySelector('.due-date-command.is-sheet')).not.toBeNull()
      fireEvent.click(screen.getByRole('option', { name: /End of this cycle/ }))
      expect(onChange).toHaveBeenCalledWith('2026-11-04')
    } finally { media.mockRestore() }
  })
})

