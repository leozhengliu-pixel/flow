import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, useAccountLanguage, useI18n } from './i18n'
import { LOCALE_STORAGE_KEY } from './locale'

function Probe({ language }: { language?: string }) {
  useAccountLanguage(language)
  const { locale, setLocale } = useI18n()
  return <>
    <span data-testid="locale">{locale}</span>
    <button type="button" onClick={() => setLocale('en-US')}>switch</button>
  </>
}

describe('useAccountLanguage', () => {
  afterEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('applies the account language over the browser language when nothing is stored on this device', () => {
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-US')
    localStorage.removeItem(LOCALE_STORAGE_KEY)
    render(<I18nProvider><Probe language="en-US"/></I18nProvider>)
    expect(screen.getByTestId('locale').textContent).toBe('en-US')
  })

  it('replaces the browser default with the saved language once the account settings load', async () => {
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('zh-CN')
    localStorage.removeItem(LOCALE_STORAGE_KEY)
    const view = render(<I18nProvider><Probe/></I18nProvider>)
    expect(screen.getByTestId('locale').textContent).toBe('zh-CN')
    view.rerender(<I18nProvider><Probe language="en-US"/></I18nProvider>)
    expect(screen.getByTestId('locale').textContent).toBe('en-US')
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('en-US')
  })

  it('does not undo a local switch while the older saved value is still in place', async () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'en-US')
    render(<I18nProvider><Probe language="en-US"/></I18nProvider>)
    await act(async () => { screen.getByText('switch').click() })
    expect(screen.getByTestId('locale').textContent).toBe('en-US')
  })

  it('ignores missing or unknown saved values', () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, 'en-US')
    render(<I18nProvider><Probe language="fr-FR"/></I18nProvider>)
    expect(screen.getByTestId('locale').textContent).toBe('en-US')
  })
})
