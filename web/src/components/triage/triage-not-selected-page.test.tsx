import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { TriageNotSelectedPage } from './triage-not-selected-page'

describe('TriageNotSelectedPage (LS-0381)', () => {
  it('shows count + create CTA when issues remain', () => {
    const onCreate = vi.fn()
    render(
      <I18nProvider>
        <TriageNotSelectedPage issueCount={3} onCreate={onCreate} />
      </I18nProvider>,
    )
    expect(screen.getByText('3 issues to triage')).toBeTruthy()
    screen.getByRole('button', { name: /Create triage issue/i }).click()
    expect(onCreate).toHaveBeenCalled()
  })

  it('shows empty copy when nothing to triage', () => {
    render(
      <I18nProvider>
        <TriageNotSelectedPage issueCount={0} />
      </I18nProvider>,
    )
    expect(screen.getByText('Nothing to triage')).toBeTruthy()
  })
})
