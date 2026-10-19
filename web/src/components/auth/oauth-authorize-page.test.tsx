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

/** Serves the consent page's team list and installation state. */
function serve({ teams = [{ id: 't1', name: 'Flow-app', key: 'FLO', color: '#f00', icon: 'Team' }], installation = null }: { teams?: object[]; installation?: object | null } = {}) {
  client.request.mockImplementation(async (path: string) => path.startsWith('/api/oauth/installation?') ? { installation } : teams)
}

function renderAt(search: string) {
  return render(<I18nProvider><MemoryRouter initialEntries={[`/oauth/authorize${search}`]}><Routes><Route path="/oauth/authorize" element={<OAuthAuthorizePage account={account}/>}/></Routes></MemoryRouter></I18nProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  api.fetchUserSettings.mockResolvedValue({})
  api.decideOAuthAuthorization.mockResolvedValue({ redirect: 'http://localhost:8765/callback?code=x&state=s' })
  serve()
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

  it('shows a well-known client\'s brand mark (Claude via claude.ai) and keeps the generic icon for a name on a foreign callback', async () => {
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ client: { client_id: 'c2', client_name: 'Claude', token_endpoint_auth_method: 'none' }, redirectUri: 'https://claude.ai/api/mcp/auth_callback' }))
    const { unmount, container } = renderAt('?client_id=c2&redirect_uri=x&response_type=code&scope=read')
    const mark = await screen.findByRole('img', { name: 'Claude icon' })
    expect(mark).toHaveClass('oauth-mark', 'is-known')
    expect(mark.querySelector('svg[data-known-app="claude"]')).not.toBeNull()
    expect(container.querySelector('.oauth-icon-placeholder')).toBeNull()
    unmount()

    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ client: { client_id: 'c3', client_name: 'Claude', token_endpoint_auth_method: 'none' }, redirectUri: 'https://evil.example.test/callback' }))
    renderAt('?client_id=c3&redirect_uri=x&response_type=code&scope=read')
    const generic = await screen.findByRole('img', { name: 'Claude icon' })
    expect(generic).toHaveClass('is-placeholder')
    expect(generic.querySelector('[data-known-app]')).toBeNull()
  })

  it('recognises a CLI client by name on a loopback callback, and prefers the client\'s own logo_uri', async () => {
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ client: { client_id: 'c4', client_name: 'Claude Code (flow)', token_endpoint_auth_method: 'none' }, redirectUri: 'http://localhost:54321/callback' }))
    const { unmount } = renderAt('?client_id=c4&redirect_uri=x&response_type=code&scope=read')
    expect((await screen.findByRole('img', { name: 'Claude Code (flow) icon' })).querySelector('svg[data-known-app="claude-code"]')).not.toBeNull()
    unmount()

    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ client: { client_id: 'c5', client_name: 'Cursor', logo_uri: 'https://cdn.example.test/logo.png', token_endpoint_auth_method: 'none' }, redirectUri: 'cursor://anysphere.cursor-mcp/oauth/callback' }))
    renderAt('?client_id=c5&redirect_uri=x&response_type=code&scope=read')
    const own = await screen.findByRole('img', { name: 'Cursor icon' })
    expect(own.querySelector('img')).toHaveAttribute('src', 'https://cdn.example.test/logo.png')
    expect(own.querySelector('[data-known-app]')).toBeNull()
  })

  it('lists the realtime permission last when the application has a webhook', async () => {
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ realtime: true }))
    renderAt('?client_id=c1&redirect_uri=x&response_type=code&scope=read,write')
    const list = await screen.findByRole('list')
    expect(within(list).getAllByRole('listitem').map(item => item.textContent)).toEqual(['Read access to your workspace', 'Write access to your workspace', 'Receive realtime updates about your workspace'])
  })

  it('offers "All public teams and select other teams…" with the private teams when the workspace has one', async () => {
    const user = userEvent.setup()
    serve({ teams: [{ id: 't1', name: 'Flow-app', key: 'FLO' }, { id: 't2', name: 'Secret', key: 'SEC', private: true }] })
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ actor: 'app', scopes: ['read'], scopeLabels: [] }))
    renderAt('?client_id=c1&redirect_uri=x&response_type=code&scope=read&actor=app')
    const access = await screen.findByRole('radiogroup', { name: 'Team access' })
    expect(within(access).getAllByRole('radio').map(radio => radio.getAttribute('aria-label') ?? radio.parentElement?.textContent)).toEqual(['All public teams', 'All public teams and select other teams…', 'Only select teams…'])
    await user.click(within(access).getByRole('radio', { name: 'All public teams and select other teams…' }))
    expect(screen.getByRole('button', { name: 'Authorize' })).toBeDisabled()
    await user.click(screen.getByRole('combobox', { name: 'Select other teams…' }))
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Secret'])
    await user.click(screen.getByRole('option', { name: 'Secret' }))
    await user.click(screen.getByRole('button', { name: 'Authorize' }))
    await waitFor(() => expect(api.decideOAuthAuthorization).toHaveBeenCalledWith(expect.objectContaining({ actor: 'app', allPublicTeams: true, teamIds: ['t2'] })))
  })

  it('shows the reference app\'s "Update access" card for an installed application asking for more', async () => {
    const user = userEvent.setup()
    serve({ installation: { installedBy: 'u1', installerName: 'Skyler', createdAt: '2026-10-13T12:00:00Z', scopes: ['read', 'app:assignable'], realtime: false } })
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ actor: 'app', scopes: ['read', 'write', 'app:mentionable'], scopeLabels: [], realtime: true }))
    renderAt('?client_id=c1&redirect_uri=x&response_type=code&scope=read,write,app:mentionable&actor=app')
    expect(await screen.findByRole('heading', { name: 'QA OAuth is requesting access' })).toBeVisible()
    const [existing, requested] = screen.getAllByRole('list')
    expect(within(existing).getAllByRole('listitem').map(item => item.textContent)).toEqual(['Assign issues and projects to the app in teams it can access', 'Read access to your workspace'])
    expect(within(requested).getAllByRole('listitem').map(item => item.textContent)).toEqual(['Write access to your workspace', 'Mention app in issues, projects, and documents it can access', 'Receive realtime updates about your workspace'])
    expect(screen.getByRole('heading', { name: 'Existing permissions' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Requested permissions' })).toBeVisible()
    expect(screen.queryByRole('radiogroup')).toBeNull()
    expect(screen.queryByText(/By approving, you authorize Flow/)).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Update access' }))
    await waitFor(() => expect(api.decideOAuthAuthorization).toHaveBeenCalledWith(expect.objectContaining({ actor: 'app', approve: true, teamIds: undefined, allPublicTeams: undefined })))
  })

  it('shows "<App> already installed" with Continue when nothing new is asked', async () => {
    serve({ installation: { installedBy: 'u1', installerName: 'Skyler', createdAt: '2026-10-13T12:00:00Z', scopes: ['read', 'write'], realtime: true } })
    api.fetchOAuthAuthorizationRequest.mockResolvedValue(consent({ actor: 'app', scopes: ['read'], scopeLabels: [], realtime: true }))
    renderAt('?client_id=c1&redirect_uri=x&response_type=code&scope=read&actor=app')
    expect(await screen.findByRole('heading', { name: 'QA OAuth already installed' })).toBeVisible()
    expect(screen.getByText((_, element) => element?.tagName === 'P' && element.textContent === 'Installed Oct 13 by Skyler')).toBeVisible()
    // The realtime permission is not repeated among the existing ones.
    expect(within(screen.getByRole('list')).getAllByRole('listitem').map(item => item.textContent)).toEqual(['Read access to your workspace', 'Write access to your workspace'])
    expect(screen.queryByRole('heading', { name: 'Requested permissions' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
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
