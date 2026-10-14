import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import { consentPermissions, oauthErrorMessage } from '@/lib/oauth-consent'
import { OAuthAuthorizePage } from './oauth-authorize-page'

const api = vi.hoisted(() => ({ fetchOAuthAuthorizationRequest: vi.fn(), decideOAuthAuthorization: vi.fn(), fetchUserSettings: vi.fn() }))
const client = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/api-client', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api-client')>()), request: client.request }))

const workspace = { id: 'w1', name: 'flow-app', urlKey: 'flow-app', color: '#b89b38', region: 'us' }
const viewer = { id: 'u1', name: 'Skyler', displayName: 'Skyler', email: 'sky@example.test', active: true, emailVerified: true }
const account = { viewer, workspaces: [{ workspace, viewerRole: 'admin' }], lastWorkspaceKey: 'flow-app' } as never
const consent = (patch = {}) => ({
  client: { client_id: 'c1', client_name: 'QA OAuth', token_endpoint_auth_method: 'client_secret_basic' },
  redirectUri: 'http://localhost:8765/callback',
  scopes: ['read', 'write'],
  scopeLabels: ['Read access to your workspace', 'Write access to your workspace'],
  workspaces: [{ workspace, viewerRole: 'admin' }],
  viewer,
  ...patch,
})

function renderAt(search: string) {
  return render(<I18nProvider><MemoryRouter initialEntries={[`/oauth/authorize${search}`]}><Routes><Route path="/oauth/authorize" element={<OAuthAuthorizePage account={account}/>}/></Routes></MemoryRouter></I18nProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  api.fetchUserSettings.mockResolvedValue({})
  api.decideOAuthAuthorization.mockResolvedValue({ redirect: 'http://localhost:8765/callback?code=x&state=s' })
  client.request.mockResolvedValue([{ id: 't1', name: 'Flow-app', key: 'FLO', color: '#f00', icon: 'Team' }])
  Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, assign: vi.fn() } })
})

describe('OAuth consent page', () => {
  it('shows the reference app\'s consent card: title, workspace, permissions, Cancel / Authorize and the notice', async () => {
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent())
    renderAt('?client_id=c1&redirect_uri=http%3A%2F%2Flocalhost%3A8765%2Fcallback&response_type=code&scope=read,write&state=s')
    expect(await screen.findByRole('heading', { name: 'QA OAuth is requesting access' })).toBeVisible()
    expect(screen.getByText((_, element) => element?.tagName === 'P' && element.textContent === 'Authenticating with your flow-app workspace')).toBeVisible()
    const list = screen.getByRole('list')
    expect(within(list).getAllByRole('listitem').map(item => item.textContent)).toEqual(['Read access to your workspace', 'Write access to your workspace'])
    expect(screen.getByText(/By approving, you authorize Flow to share this data with QA OAuth/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'third-party applications' })).toBeVisible()
    expect(screen.queryByRole('radiogroup')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Authorize' }))
    await waitFor(() => expect(api.decideOAuthAuthorization).toHaveBeenCalledWith(expect.objectContaining({ approve: true, workspaceKey: 'flow-app', clientId: 'c1', state: 's' })))
  })

  it('asks for team access on actor=app installs and needs a team for "Only select teams…"', async () => {
    const user = userEvent.setup()
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ actor: 'app', scopes: ['read', 'app:assignable'], scopeLabels: [] }))
    renderAt('?client_id=c1&redirect_uri=x&response_type=code&scope=read,app:assignable&actor=app')
    expect(await screen.findByText('Assign issues and projects to the app in teams it can access')).toBeVisible()
    const access = screen.getByRole('radiogroup', { name: 'Team access' })
    expect(within(access).getByRole('radio', { name: 'All public teams' })).toBeChecked()
    await user.click(within(access).getByRole('radio', { name: 'Only select teams…' }))
    expect(screen.getByRole('button', { name: 'Authorize' })).toBeDisabled()
    await user.click(screen.getByRole('combobox', { name: 'Select teams…' }))
    await user.click(await screen.findByRole('option', { name: 'Flow-app' }))
    expect(screen.getByRole('button', { name: 'Authorize' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Authorize' }))
    await waitFor(() => expect(api.decideOAuthAuthorization).toHaveBeenCalledWith(expect.objectContaining({ actor: 'app', allPublicTeams: false, teamIds: ['t1'] })))
  })

  it('shows an invalid request: the mark and the server\'s message', async () => {
    api.fetchOAuthAuthorizationRequest.mockRejectedValue(new ApiError('Could not find OAuth client with clientId abc. Please check that you are logged into the correct workspace.', 400, 'invalid_client'))
    renderAt('?client_id=abc&redirect_uri=x&response_type=code')
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not find OAuth client with clientId abc. Please check that you are logged into the correct workspace.')
    expect(screen.queryByRole('button', { name: 'Authorize' })).toBeNull()
  })
})

describe('consent wording', () => {
  it('lists write alone when it covers the narrower write scopes', () => {
    expect(consentPermissions(['read', 'write', 'create_issues', 'create_comments', 'timeSchedule:write', 'admin']).map(item => item.scope)).toEqual(['read', 'write', 'admin'])
    expect(consentPermissions(['read', 'create_issues']).map(item => item.label)).toEqual(['Read access to your workspace', 'Create issues for your workspace'])
    // The reference app's wording for the resource scopes (measured on its consent screen).
    expect(consentPermissions(['initiative:read', 'initiative:write', 'customer:read', 'customer:write', 'release:read', 'release:create', 'releaseStage:update']).map(item => item.label)).toEqual([
      'Read data about initiatives in the workspace',
      'Read and write data about initiatives in the workspace',
      'Read data about customers in the workspace',
      'Read and write data about customers in the workspace',
      'Read releases in the workspace',
      'Create releases in the workspace',
      'Update release stages in the workspace',
    ])
  })

  it('translates the server\'s measured errors with their values', () => {
    const t = (value: string) => value === 'Invalid scope: {value}' ? '无效的权限范围：{value}' : value
    expect(oauthErrorMessage(new Error('Invalid scope: bogus'), t)).toBe('无效的权限范围：bogus')
  })
})
