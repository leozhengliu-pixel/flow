import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'

import { DueDateCommand } from './due-date-picker'

describe('DueDateCommand', () => {
  it('keeps the desktop end-of-week preset on Friday and applies it', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-30T12:00:00'))
    const media = vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query === '(min-width: 1024px)', addEventListener: vi.fn(), removeEventListener: vi.fn() }) as unknown as MediaQueryList)
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
})
