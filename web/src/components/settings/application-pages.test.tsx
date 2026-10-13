import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { OAuthApplication } from '@/types/flow'
import { OAuthManifestError } from '@/lib/api'
import * as api from '@/lib/api'
import { saveApplication } from '@/lib/application-agents'
import { ApplicationCreatePage, ApplicationDetailsPage, ApplicationEditPage } from './application-pages'

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  createOAuthApplicationFromManifest: vi.fn(),
  updateOAuthApplicationFromManifest: vi.fn(),
  validateOAuthAppManifest: vi.fn(),
  exportOAuthApplicationManifest: vi.fn(),
  fetchOAuthApplicationInstalls: vi.fn(),
  fetchOAuthApplicationFailures: vi.fn(),
  fetchOAuthSyncGroupRequests: vi.fn(),
  revokeOAuthAuthorization: vi.fn(),
  deleteOAuthApplication: vi.fn(),
  updateWebhook: vi.fn(),
}))
vi.mock('@/lib/application-agents', () => ({ saveApplication: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))

const app: OAuthApplication = {
  id: 'app-1', clientId: 'client-1', name: 'Acme Sync', creatorId: viewer.id, createdAt: '', updatedAt: '2026-09-01T00:00:00Z',
  redirectUris: ['https://acme.test/cb'], scopes: ['read', 'write', 'app:assignable'], description: 'Syncs things',
  developerName: 'Acme Inc', developerUrl: 'https://acme.test', publicEnabled: false, supportsClientCredentials: true,
  webhookEnabled: true, webhookUrl: 'https://hooks.acme.test', webhookResourceTypes: ['Issue'],
}
const data = makeBootstrap({ oauthApplications: [app], webhooks: [] })

function Where() {
  const location = useLocation()
  return <output data-testid="where">{location.pathname}</output>
}
function renderAt(path: string, element: React.ReactNode) {
  return render(
    <I18nProvider><TooltipProvider>
      <MemoryRouter initialEntries={[path]}>
        <Routes><Route path="*" element={<>{element}<Where /></>} /></Routes>
      </MemoryRouter>
    </TooltipProvider></I18nProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  vi.mocked(api.fetchOAuthApplicationInstalls).mockResolvedValue({ authorizations: [], installations: [] })
  vi.mocked(api.fetchOAuthApplicationFailures).mockResolvedValue([])
  vi.mocked(api.fetchOAuthSyncGroupRequests).mockResolvedValue([])
})

describe('ApplicationCreatePage', () => {
  const search = '?oauth.client_name=Acme%20Sync&developer.name=Acme%20Inc&oauth.redirect_uris=https%3A%2F%2Facme.test%2Fcb&oauth.redirect_uris=https%3A%2F%2Facme.test%2Fcb2&oauth.scopes=write&oauth.grant_types=client_credentials&webhook.enabled=true&webhook.url=https%3A%2F%2Fhooks.acme.test&webhook.resourceTypes=Issue&agent.assignable=true&distribution=public'

  it('pre-fills from URL params, creates through the manifest endpoint and shows the secret once', async () => {
    const user = userEvent.setup()
    const reload = vi.fn().mockResolvedValue(undefined)
    vi.mocked(api.createOAuthApplicationFromManifest).mockResolvedValue({ ...app, clientSecret: 'flow_secret_123', webhookSecret: 'flow_whsec_456' })
    renderAt(`/workspace/settings/applications/new${search}`, <ApplicationCreatePage data={data} onReload={reload} onBack={vi.fn()} />)
    expect(screen.getByLabelText('Name')).toHaveValue('Acme Sync')
    expect(screen.getByLabelText('Developer name')).toHaveValue('Acme Inc')
    expect(screen.getByLabelText('Redirect URIs')).toHaveValue('https://acme.test/cb\nhttps://acme.test/cb2')
    expect(screen.getByRole('checkbox', { name: 'Write' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Client credentials' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Public application' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Assignable' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Webhooks enabled' })).toBeChecked()
    expect(screen.getByLabelText('Webhook URL')).toHaveValue('https://hooks.acme.test')
    expect(screen.getByRole('checkbox', { name: 'Issue' })).toBeChecked()
    expect(screen.getAllByText('User actor only')).toHaveLength(2)

    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createOAuthApplicationFromManifest).toHaveBeenCalledTimes(1))
    const manifest = vi.mocked(api.createOAuthApplicationFromManifest).mock.calls[0][0]
    expect(manifest.oauth).toMatchObject({
      client_name: 'Acme Sync',
      redirect_uris: ['https://acme.test/cb', 'https://acme.test/cb2'],
      grant_types: ['authorization_code', 'client_credentials'],
      scopes: ['read', 'write', 'app:assignable'],
    })
    expect(manifest.distribution).toBe('public')
    expect(manifest.developer).toEqual({ name: 'Acme Inc' })
    expect(manifest.webhook).toEqual({ enabled: true, url: 'https://hooks.acme.test', resourceTypes: ['Issue'] })
    expect(manifest.agent).toEqual({ assignable: true, mentionable: false })

    expect(await screen.findByText('flow_secret_123')).toBeVisible()
    expect(screen.getAllByText(/will not be shown again/)).toHaveLength(2)
    expect(screen.getByRole('button', { name: 'Copy client secret' })).toBeVisible()
    expect(screen.getByText('flow_whsec_456')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Copy webhook signing secret' })).toBeVisible()
    expect(reload).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Open application' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/applications/app-1')
  })

  it('prefers the manifest param over dot params', () => {
    const manifest = encodeURIComponent(JSON.stringify({ oauth: { client_name: 'From manifest' } }))
    renderAt(`/workspace/settings/applications/new?manifest=${manifest}&oauth.client_name=Ignored`, <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    expect(screen.getByLabelText('Name')).toHaveValue('From manifest')
  })

  it('shows server validation issues from a rejected create', async () => {
    const user = userEvent.setup()
    vi.mocked(api.createOAuthApplicationFromManifest).mockRejectedValue(new OAuthManifestError('invalid manifest', [{ path: 'oauth.redirect_uris[0]', message: 'must be an https URL' }]))
    renderAt('/workspace/settings/applications/new?oauth.client_name=Bad', <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Create' }))
    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText('oauth.redirect_uris[0]')).toBeVisible()
    expect(within(alert).getByText('must be an https URL')).toBeVisible()
    expect(screen.queryByText(/will not be shown again/)).toBeNull()
  })

  it('imports a manifest, listing validation issues inline and filling the form', async () => {
    const user = userEvent.setup()
    vi.mocked(api.validateOAuthAppManifest).mockResolvedValue({ valid: false, issues: [{ path: 'webhook.url', message: 'must use https' }] })
    renderAt('/workspace/settings/applications/new', <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Import manifest' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Manifest JSON'), { target: { value: JSON.stringify({ oauth: { client_name: 'Imported' }, webhook: { enabled: true, url: 'http://insecure.test' } }) } })
    await user.click(within(dialog).getByRole('button', { name: 'Validate & import' }))
    await waitFor(() => expect(api.validateOAuthAppManifest).toHaveBeenCalledWith({ oauth: { client_name: 'Imported' }, webhook: { enabled: true, url: 'http://insecure.test' } }))
    expect(await within(dialog).findByText('webhook.url')).toBeVisible()
    expect(within(dialog).getByText('must use https')).toBeVisible()
    expect(screen.getByLabelText('Name', { selector: 'input' })).toHaveValue('Imported')
    expect(screen.getByLabelText('Webhook URL')).toHaveValue('http://insecure.test')
  })

  it('rejects text that is not JSON without calling the server', async () => {
    const user = userEvent.setup()
    renderAt('/workspace/settings/applications/new', <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Import manifest' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Manifest JSON'), { target: { value: '{nope' } })
    await user.click(within(dialog).getByRole('button', { name: 'Validate & import' }))
    expect(await within(dialog).findByText('The manifest is not valid JSON.')).toBeVisible()
    expect(api.validateOAuthAppManifest).not.toHaveBeenCalled()
  })
})

describe('ApplicationEditPage', () => {
  it('saves the form through the manifest PUT and surfaces 400 issues', async () => {
    const user = userEvent.setup()
    const reload = vi.fn().mockResolvedValue(undefined)
    vi.mocked(api.updateOAuthApplicationFromManifest).mockRejectedValueOnce(new OAuthManifestError('invalid', [{ path: 'oauth.client_name', message: 'is required' }]))
    renderAt('/workspace/settings/applications/app-1/edit', <ApplicationEditPage data={data} applicationId="app-1" onReload={reload} onBack={vi.fn()} />)
    expect(screen.getByLabelText('Developer URL')).toHaveValue('https://acme.test')
    expect(screen.getByRole('checkbox', { name: 'Assignable' })).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('is required')).toBeVisible()

    vi.mocked(api.updateOAuthApplicationFromManifest).mockResolvedValueOnce(app)
    await user.clear(screen.getByLabelText('Name'))
    await user.type(screen.getByLabelText('Name'), 'Renamed')
    await user.click(screen.getByRole('checkbox', { name: 'Mentionable' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateOAuthApplicationFromManifest).toHaveBeenCalledTimes(2))
    const [id, manifest] = vi.mocked(api.updateOAuthApplicationFromManifest).mock.calls[1]
    expect(id).toBe('app-1')
    expect(manifest.oauth).toMatchObject({ client_name: 'Renamed', scopes: ['read', 'write', 'app:assignable', 'app:mentionable'], grant_types: ['authorization_code', 'client_credentials'] })
    expect(manifest.developer).toEqual({ name: 'Acme Inc' })
    expect(manifest.webhook).toEqual({ enabled: true, url: 'https://hooks.acme.test', resourceTypes: ['Issue'] })
    await waitFor(() => expect(reload).toHaveBeenCalled())
    expect(screen.queryByText('is required')).toBeNull()
  })

  it('exports the manifest to a download and the clipboard', async () => {
    const user = userEvent.setup()
    const manifest = { schemaVersion: '1.0.0', oauth: { client_name: 'Acme Sync' } }
    vi.mocked(api.exportOAuthApplicationManifest).mockResolvedValue(manifest)
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const createObjectURL = vi.fn(() => 'blob:manifest')
    const revokeObjectURL = vi.fn()
    Object.assign(URL, { createObjectURL, revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    renderAt('/workspace/settings/applications/app-1/edit', <ApplicationEditPage data={data} applicationId="app-1" onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Export manifest' }))
    await waitFor(() => expect(api.exportOAuthApplicationManifest).toHaveBeenCalledWith('app-1'))
    await waitFor(() => expect(click).toHaveBeenCalled())
    expect(writeText).toHaveBeenCalledWith(JSON.stringify(manifest, null, 2))
    expect(createObjectURL).toHaveBeenCalled()
    click.mockRestore()
  })

  it('validates an imported manifest and saves it with PUT only when valid', async () => {
    const user = userEvent.setup()
    vi.mocked(api.validateOAuthAppManifest).mockResolvedValueOnce({ valid: false, issues: [{ path: 'oauth.scopes[1]', message: 'unknown scope' }] })
    vi.mocked(api.updateOAuthApplicationFromManifest).mockResolvedValue(app)
    renderAt('/workspace/settings/applications/app-1/edit', <ApplicationEditPage data={data} applicationId="app-1" onReload={vi.fn().mockResolvedValue(undefined)} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Import manifest' }))
    const dialog = await screen.findByRole('dialog')
    const text = JSON.stringify({ oauth: { client_name: 'Acme Sync', scopes: ['read', 'nope'] } })
    fireEvent.change(within(dialog).getByLabelText('Manifest JSON'), { target: { value: text } })
    await user.click(within(dialog).getByRole('button', { name: 'Validate & save' }))
    expect(await within(dialog).findByText('unknown scope')).toBeVisible()
    expect(api.updateOAuthApplicationFromManifest).not.toHaveBeenCalled()

    vi.mocked(api.validateOAuthAppManifest).mockResolvedValueOnce({ valid: true, issues: [] })
    await user.click(within(dialog).getByRole('button', { name: 'Validate & save' }))
    await waitFor(() => expect(api.updateOAuthApplicationFromManifest).toHaveBeenCalledWith('app-1', { oauth: { client_name: 'Acme Sync', scopes: ['read', 'nope'] } }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('does not create a bogus webhook; New webhook opens the webhook editor', async () => {
    const user = userEvent.setup()
    renderAt('/workspace/settings/applications/app-1/edit', <ApplicationEditPage data={data} applicationId="app-1" onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'New webhook' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api/webhooks/new')
  })
})

describe('ApplicationDetailsPage installs', () => {
  it('lists active authorizations and installations and revokes them', async () => {
    const user = userEvent.setup()
    const authorization = { id: 'auth-1', clientId: 'client-1', clientName: 'Acme Sync', userId: viewer.id, scopes: ['read', 'write'], createdAt: '2026-09-01T00:00:00Z', lastUsedAt: '2026-09-02T00:00:00Z' }
    const installation = { id: 'install-1', workspaceKey: 'workspace', clientId: 'client-1', name: 'Acme Agent', userId: 'app-user', installedBy: viewer.id, scopes: ['read'], teamIds: [], builtin: false, active: true, createdAt: '', updatedAt: '' }
    vi.mocked(api.fetchOAuthApplicationInstalls)
      .mockResolvedValueOnce({ authorizations: [authorization, { ...authorization, id: 'auth-old', revokedAt: '2026-09-03T00:00:00Z' }], installations: [installation, { ...installation, id: 'install-off', active: false }] })
      .mockResolvedValue({ authorizations: [], installations: [] })
    vi.mocked(api.revokeOAuthAuthorization).mockResolvedValue(undefined)
    vi.mocked(saveApplication).mockResolvedValue({ application: { ...installation, active: false }, webhookSecret: '' })
    renderAt('/workspace/settings/applications/app-1', <ApplicationDetailsPage data={data} applicationId="app-1" onReload={vi.fn().mockResolvedValue(undefined)} onBack={vi.fn()} />)

    expect(await screen.findByText(viewer.displayName)).toBeVisible()
    expect(screen.getByText('Acme Agent')).toBeVisible()
    expect(screen.getAllByRole('button', { name: 'Revoke' })).toHaveLength(2)
    expect(screen.getByText('Acme Inc · https://acme.test')).toBeVisible()
    expect(screen.getByText('https://hooks.acme.test')).toBeVisible()

    await user.click(screen.getAllByRole('button', { name: 'Revoke' })[0])
    await waitFor(() => expect(api.revokeOAuthAuthorization).toHaveBeenCalledWith('auth-1'))
    await waitFor(() => expect(api.fetchOAuthApplicationInstalls).toHaveBeenCalledTimes(2))
    expect(await screen.findByText('No installs yet')).toBeVisible()
  })

  it('deactivates an application installation with the existing installation endpoint', async () => {
    const user = userEvent.setup()
    const installation = { id: 'install-1', workspaceKey: 'workspace', clientId: 'client-1', name: 'Acme Agent', userId: 'app-user', installedBy: viewer.id, scopes: ['read'], teamIds: [], builtin: false, active: true, createdAt: '', updatedAt: '' }
    vi.mocked(api.fetchOAuthApplicationInstalls).mockResolvedValueOnce({ authorizations: [], installations: [installation] }).mockResolvedValue({ authorizations: [], installations: [] })
    vi.mocked(saveApplication).mockResolvedValue({ application: { ...installation, active: false }, webhookSecret: '' })
    renderAt('/workspace/settings/applications/app-1', <ApplicationDetailsPage data={data} applicationId="app-1" onReload={vi.fn().mockResolvedValue(undefined)} onBack={vi.fn()} />)
    await user.click(await screen.findByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(saveApplication).toHaveBeenCalledWith('workspace', { ...installation, active: false }))
  })
})
