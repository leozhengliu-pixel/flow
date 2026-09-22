import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CompleteOAuthView } from './complete-oauth-view'
import { CompleteFigmaAuthView } from './complete-figma-auth-view'
import { CompleteSentryAuthView } from './complete-sentry-auth-view'
import { AuthDesktopRedirectFigma } from './auth-desktop-redirect-figma'
import { finishFigmaAuth } from '@/lib/finish-figma-auth'
import { startIntegrationOAuth } from '@/lib/api'

vi.mock('@/lib/finish-figma-auth', () => ({ finishFigmaAuth: vi.fn() }))
vi.mock('@/lib/api', () => ({ startIntegrationOAuth: vi.fn() }))

describe('CompleteOAuthView', () => {
  it('shows success flash for connected status', () => {
    render(
      <MemoryRouter initialEntries={['/connect/oauth/complete?provider=slack&status=connected&workspace=acme']}>
        <CompleteOAuthView />
      </MemoryRouter>,
    )
    expect(screen.getByRole('status')).toHaveTextContent('Slack connected')
    expect(screen.getByRole('link', { name: 'Continue to integrations' })).toHaveAttribute(
      'href',
      '/acme/settings/integrations',
    )
  })

  it('shows error flash', () => {
    render(
      <MemoryRouter initialEntries={['/connect/oauth/complete?provider=github&status=error&error=access_denied']}>
        <CompleteOAuthView />
      </MemoryRouter>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('Unable to complete connection with GitHub')
    expect(screen.getByRole('alert')).toHaveTextContent('access_denied')
  })

  it('labels Microsoft Teams / PagerDuty / Front on shared complete flash', () => {
    const { unmount: u1 } = render(
      <MemoryRouter initialEntries={['/connect/oauth/complete?provider=microsoftteams&status=connected&workspace=acme']}>
        <CompleteOAuthView />
      </MemoryRouter>,
    )
    expect(screen.getByRole('status')).toHaveTextContent('Microsoft Teams connected')
    u1()

    const { unmount: u2 } = render(
      <MemoryRouter initialEntries={['/connect/oauth/complete?provider=pagerduty&status=connected&workspace=acme']}>
        <CompleteOAuthView />
      </MemoryRouter>,
    )
    expect(screen.getByRole('status')).toHaveTextContent('PagerDuty connected')
    u2()

    render(
      <MemoryRouter initialEntries={['/connect/oauth/complete?provider=front&status=error&error=access_denied']}>
        <CompleteOAuthView />
      </MemoryRouter>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('Unable to complete connection with Front')
  })
})

describe('CompleteFigmaAuthView', () => {
  beforeEach(() => vi.clearAllMocks())

  it('renders failure copy when finish rejects', async () => {
    vi.mocked(finishFigmaAuth).mockResolvedValue({ ok: false, error: 'boom' })
    render(
      <MemoryRouter initialEntries={['/connect/figma/callback?code=x&state=y']}>
        <CompleteFigmaAuthView />
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Unable to complete connection with Figma'))
    expect(screen.getByRole('alert')).toHaveTextContent('boom')
  })
})

describe('CompleteSentryAuthView', () => {
  it('labels MCP-only and links to agents', async () => {
    render(
      <MemoryRouter initialEntries={['/connect/sentry/callback?workspace=acme']}>
        <CompleteSentryAuthView />
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('MCP'))
    expect(screen.getByText(/Sentry · MCP only/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open MCP connectors' })).toHaveAttribute(
      'href',
      '/acme/settings/account/agents',
    )
  })
})

describe('AuthDesktopRedirectFigma', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows honest unavailable copy when oauth/start fails', async () => {
    vi.mocked(startIntegrationOAuth).mockRejectedValue(new Error('OAuth integration is unavailable'))
    render(
      <MemoryRouter initialEntries={['/connect/figma/desktop-redirect?workspace=acme']}>
        <AuthDesktopRedirectFigma />
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('not configured'))
    expect(screen.getByRole('button', { name: 'Open Flow' })).toBeInTheDocument()
  })
})
