import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceWelcomeInboxView } from './workspace-welcome-inbox-view'

describe('workspace welcome inbox view', () => {
  it('renders the welcome document instead of an unavailable issue state', () => {
    render(<I18nProvider><WorkspaceWelcomeInboxView title="Welcome to Flow" message="Start here." /></I18nProvider>)
    expect(screen.getByRole('heading', { name: 'Welcome to Flow' })).toBeVisible()
    expect(screen.getByText('Start here.')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Resources' })).toBeVisible()
    expect(screen.queryByText('Issue is no longer available')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Flow documentation' })).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/tree/main/docs')
    expect(screen.getByRole('link', { name: 'Community and support' })).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/issues')
    expect(screen.getByText('Introductory video unavailable')).toBeInTheDocument()
  })
})
