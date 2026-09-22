import { describe, expect, it } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import {
  AgentSessionExternalUrlsButton,
  shouldShowAgentSessionExternalUrlsButton,
} from './agent-session-external-urls-button'

describe('LS-0039 AgentSessionExternalUrlsButton', () => {
  it('hides on touch or empty', () => {
    expect(shouldShowAgentSessionExternalUrlsButton({ urls: [], isPureTouchDevice: false })).toBe(false)
    expect(
      shouldShowAgentSessionExternalUrlsButton({
        urls: [{ url: 'https://github.com/x', label: 'GitHub' }],
        isPureTouchDevice: true,
      }),
    ).toBe(false)
  })

  it('renders single link and multi menu', () => {
    const { rerender } = render(
      <I18nProvider>
        <AgentSessionExternalUrlsButton urls={[{ url: 'https://github.com/acme/flow', label: 'GitHub' }]} />
      </I18nProvider>,
    )
    expect(screen.getByRole('link', { name: /GitHub/i })).toHaveAttribute('href', 'https://github.com/acme/flow')

    rerender(
      <I18nProvider>
        <AgentSessionExternalUrlsButton
          urls={[
            { url: 'https://github.com/acme/flow', label: 'GitHub' },
            { url: 'https://www.figma.com/file/1', label: 'Figma' },
          ]}
        />
      </I18nProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Links' }))
    expect(screen.getByRole('menuitem', { name: /Figma/i })).toBeInTheDocument()
  })
})
