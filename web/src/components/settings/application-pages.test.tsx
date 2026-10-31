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
import { ApplicationCreatePage, ApplicationDetailsPage, ApplicationEditPage } from './application-pages'
import { OAuthApplicationsSection } from './oauth-applications-section'

const layout = vi.hoisted(() => ({ phone: false }))
vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => layout.phone }))

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  createOAuthApplicationFromManifest: vi.fn(),
  updateOAuthApplicationFromManifest: vi.fn(),
  validateOAuthAppManifest: vi.fn(),
  exportOAuthApplicationManifest: vi.fn(),
  fetchOAuthApplicationFailures: vi.fn(),
  fetchOAuthSyncGroupRequests: vi.fn(),
  deleteOAuthApplication: vi.fn(),
  uploadOAuthApplicationIcon: vi.fn(),
  fetchApplicationWebhookSecret: vi.fn(),
  rotateApplicationWebhookSecret: vi.fn(),
  updateApplicationWebhook: vi.fn(),
}))
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: vi.fn().mockResolvedValue(true) }))

const app: OAuthApplication = {
  id: 'app-1', clientId: 'client-1', name: 'Acme Sync', creatorId: viewer.id, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z',
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
  layout.phone = false
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  vi.mocked(api.fetchOAuthApplicationFailures).mockResolvedValue([])
  vi.mocked(api.fetchOAuthSyncGroupRequests).mockResolvedValue([])
})

describe('OAuthApplicationsSection (Settings › API)', () => {
  it('shows phone documentation, a compact date and an actionable application menu', async () => {
    layout.phone = true
    const user = userEvent.setup()
    renderAt('/workspace/settings/api', <OAuthApplicationsSection data={data} />)
    expect(screen.getByRole('link', { name: /Docs/ })).toHaveAttribute('href', expect.stringContaining('oauth.md'))
    expect(screen.getByText('Updated Oct 13')).toBeVisible()
    expect(screen.queryByText('Updated Oct 13, 2026')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(screen.getByRole('option', { name: 'Edit application' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api/applications/app-1/edit')
  })

  it('lists applications with the reference app\'s count header and opens an application\'s page', async () => {
    const user = userEvent.setup()
    renderAt('/workspace/settings/api', <OAuthApplicationsSection data={data} />)
    expect(screen.getByText('1 OAuth application')).toBeVisible()
    expect(screen.getByText(/^Updated Oct 13, 2026$/)).toBeVisible()
    await user.click(screen.getByText('Acme Sync'))
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api/applications/app-1')
  })

  it('shows the empty row with "New OAuth application"', async () => {
    const user = userEvent.setup()
    renderAt('/workspace/settings/api', <OAuthApplicationsSection data={makeBootstrap({ oauthApplications: [] })} />)
    expect(screen.getByText('No OAuth applications')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'New OAuth application' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api/applications/new')
  })
})

describe('ApplicationCreatePage', () => {
  const search = '?oauth.client_name=Acme%20Sync&developer.name=Acme%20Inc&oauth.redirect_uris=https%3A%2F%2Facme.test%2Fcb&oauth.redirect_uris=https%3A%2F%2Facme.test%2Fcb2&oauth.scopes=write&oauth.grant_types=client_credentials&webhook.enabled=true&webhook.url=https%3A%2F%2Fhooks.acme.test&webhook.resourceTypes=Issue&agent.assignable=true&distribution=public&oauth.client_uri=https%3A%2F%2Facme.test'

  it('pre-fills from URL params, creates through the manifest endpoint and opens the new application', async () => {
    const user = userEvent.setup()
    const reload = vi.fn().mockResolvedValue(undefined)
    vi.mocked(api.createOAuthApplicationFromManifest).mockResolvedValue({ ...app, id: 'app-2' })
    renderAt(`/workspace/settings/api/applications/new${search}`, <ApplicationCreatePage data={data} onReload={reload} onBack={vi.fn()} />)
    expect(screen.getByRole('heading', { name: 'Create new application' })).toBeVisible()
    expect(screen.getByLabelText(/^Application name/)).toHaveValue('Acme Sync')
    expect(screen.getByLabelText(/^Developer name/)).toHaveValue('Acme Inc')
    expect(screen.getByLabelText(/^Redirect URIs/)).toHaveValue('https://acme.test/cb\nhttps://acme.test/cb2')
    expect(screen.getByRole('checkbox', { name: 'Client credentials' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Public' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Webhooks' })).toBeChecked()
    expect(screen.getByLabelText('Webhook URL')).toHaveValue('https://hooks.acme.test')
    expect(screen.getByRole('checkbox', { name: 'Issues' })).toBeChecked()
    expect(screen.getByText('Data change events')).toBeVisible()
    expect(screen.queryByText('Scopes')).toBeNull()

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
    // Like the reference app, the signing secret is generated in the form, shown unmasked and saved with the webhook.
    expect(manifest.webhook).toEqual({ enabled: true, url: 'https://hooks.acme.test', resourceTypes: ['Issue'], secret: expect.stringMatching(/^flow_wh_[A-Za-z0-9_-]{32}$/) })
    expect(manifest.github).toBeUndefined()
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api/applications/app-2'))
    expect(reload).toHaveBeenCalled()
  })

  it('shows the generated webhook signing secret in the form, keeps it while toggling and copies it', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    renderAt('/workspace/settings/api/applications/new', <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    expect(screen.queryByLabelText('Webhook signing secret')).toBeNull()
    await user.click(screen.getByRole('checkbox', { name: 'Webhooks' }))
    const field = screen.getByLabelText<HTMLInputElement>('Webhook signing secret')
    const secret = field.value
    expect(secret).toMatch(/^flow_wh_/)
    expect(field).toHaveAttribute('type', 'text')
    expect(field).toHaveAttribute('readonly')
    await user.click(screen.getByRole('button', { name: 'Hide' }))
    expect(screen.getByLabelText('Webhook signing secret')).toHaveAttribute('type', 'password')
    await user.click(screen.getByRole('checkbox', { name: 'Webhooks' }))
    await user.click(screen.getByRole('checkbox', { name: 'Webhooks' }))
    expect(screen.getByLabelText('Webhook signing secret')).toHaveValue(secret)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    await user.click(screen.getByRole('button', { name: 'Copy to clipboard' }))
    expect(writeText).toHaveBeenCalledWith(secret)
  })

  it('has the reference app\'s GitHub username field and checks the account on GitHub when creating', async () => {
    const user = userEvent.setup()
    const lookup = vi.fn().mockResolvedValue(new Response('{}', { status: 404 }))
    vi.stubGlobal('fetch', lookup)
    vi.mocked(api.createOAuthApplicationFromManifest).mockResolvedValue(app)
    renderAt('/workspace/settings/api/applications/new?oauth.client_name=Coder&developer.name=Dev&oauth.redirect_uris=https%3A%2F%2Fa.test%2Fcb', <ApplicationCreatePage data={data} onReload={vi.fn().mockResolvedValue(undefined)} onBack={vi.fn()} />)
    const input = screen.getByLabelText(/^GitHub username/)
    expect(input).toHaveAttribute('placeholder', 'github-username')
    expect(screen.getByText('actor=app')).toBeVisible()
    expect(screen.getByText('my-app[bot]')).toBeVisible()
    // "[[" types a literal "[" (user-event reads "[name]" as a key).
    await user.type(input, 'missing-bot[[bot]')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Invalid GitHub username')).toBeVisible()
    expect(lookup).toHaveBeenCalledWith('https://api.github.com/users/missing-bot%5Bbot%5D', expect.anything())
    expect(api.createOAuthApplicationFromManifest).not.toHaveBeenCalled()
    lookup.mockResolvedValue(new Response('{}', { status: 200 }))
    await user.clear(input)
    await user.type(input, 'acme-coder[[bot]')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createOAuthApplicationFromManifest).toHaveBeenCalled())
    expect(vi.mocked(api.createOAuthApplicationFromManifest).mock.calls[0][0].github).toEqual({ username: 'acme-coder[bot]' })
    vi.unstubAllGlobals()
  })

  it('marks the reference app\'s required fields and does not call the server', async () => {
    const user = userEvent.setup()
    renderAt('/workspace/settings/api/applications/new', <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(screen.getAllByText('Required')).toHaveLength(3)
    expect(api.createOAuthApplicationFromManifest).not.toHaveBeenCalled()
    await user.click(screen.getByRole('checkbox', { name: 'Public' }))
    expect(screen.getAllByText('Required')).toHaveLength(4)
  })

  it('creates a new application able to request every scope', async () => {
    const user = userEvent.setup()
    vi.mocked(api.createOAuthApplicationFromManifest).mockResolvedValue(app)
    renderAt('/workspace/settings/api/applications/new', <ApplicationCreatePage data={data} onReload={vi.fn().mockResolvedValue(undefined)} onBack={vi.fn()} />)
    await user.type(screen.getByLabelText(/^Application name/), 'Bug Reporter')
    await user.type(screen.getByLabelText(/^Developer name/), 'Bug Corp')
    await user.type(screen.getByLabelText(/^Redirect URIs/), 'http://localhost:8765/callback')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.createOAuthApplicationFromManifest).toHaveBeenCalled())
    const manifest = vi.mocked(api.createOAuthApplicationFromManifest).mock.calls[0][0]
    expect(manifest.oauth?.scopes).toEqual(expect.arrayContaining(['read', 'write', 'admin', 'app:assignable', 'app:mentionable']))
    expect(manifest.distribution).toBe('private')
  })

  it('prefers the manifest param over dot params', () => {
    const manifest = encodeURIComponent(JSON.stringify({ oauth: { client_name: 'From manifest' } }))
    renderAt(`/workspace/settings/api/applications/new?manifest=${manifest}&oauth.client_name=Ignored`, <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    expect(screen.getByLabelText(/^Application name/)).toHaveValue('From manifest')
  })

  it('shows server validation issues from a rejected create', async () => {
    const user = userEvent.setup()
    vi.mocked(api.createOAuthApplicationFromManifest).mockRejectedValue(new OAuthManifestError('invalid manifest', [{ path: 'oauth.redirect_uris[0]', message: 'must be an https URL' }]))
    renderAt('/workspace/settings/api/applications/new?oauth.client_name=Bad&developer.name=Dev&oauth.redirect_uris=ftp%3A%2F%2Fx', <ApplicationCreatePage data={data} onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('oauth.redirect_uris[0]')).toBeVisible()
    expect(screen.getByText('must be an https URL')).toBeVisible()
  })
})

describe('ApplicationEditPage', () => {
  it('edits the application with the reference app\'s form, keeps its scopes and returns to its page', async () => {
    const user = userEvent.setup()
    const reload = vi.fn().mockResolvedValue(undefined)
    const saved = { ...app, webhookSecretPrefix: 'flow_wh_saved', githubUsername: 'acme-coder[bot]' }
    vi.mocked(api.updateOAuthApplicationFromManifest).mockResolvedValue(saved)
    vi.mocked(api.fetchApplicationWebhookSecret).mockResolvedValue({ secret: 'flow_wh_saved-secret' })
    renderAt('/workspace/settings/api/applications/app-1/edit', <ApplicationEditPage data={makeBootstrap({ oauthApplications: [saved], webhooks: [] })} applicationId="app-1" onReload={reload} onBack={vi.fn()} />)
    expect(screen.getByRole('heading', { name: 'Edit application' })).toBeVisible()
    expect(screen.getByLabelText(/^Developer URL/)).toHaveValue('https://acme.test')
    expect(screen.getByLabelText(/^GitHub username/)).toHaveValue('acme-coder[bot]')
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    // The saved signing secret is loaded into the form, masked until "Show".
    await waitFor(() => expect(screen.getByLabelText('Webhook signing secret')).toHaveValue('flow_wh_saved-secret'))
    expect(screen.getByLabelText('Webhook signing secret')).toHaveAttribute('type', 'password')
    expect(api.fetchApplicationWebhookSecret).toHaveBeenCalledWith('app-1')
    await user.clear(screen.getByLabelText(/^Application name/))
    await user.type(screen.getByLabelText(/^Application name/), 'Renamed')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateOAuthApplicationFromManifest).toHaveBeenCalledTimes(1))
    const [id, manifest] = vi.mocked(api.updateOAuthApplicationFromManifest).mock.calls[0]
    expect(id).toBe('app-1')
    expect(manifest.oauth).toMatchObject({ client_name: 'Renamed', scopes: ['read', 'write', 'app:assignable'], grant_types: ['authorization_code', 'client_credentials'] })
    // An existing secret is never sent back; an unchanged GitHub username is not looked up again.
    expect(manifest.webhook).toEqual({ enabled: true, url: 'https://hooks.acme.test', resourceTypes: ['Issue'] })
    expect(manifest.github).toEqual({ username: 'acme-coder[bot]' })
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api/applications/app-1'))
    expect(reload).toHaveBeenCalled()
  })
})

describe('ApplicationDetailsPage', () => {
  it('shows the reference app\'s header, details and credentials', async () => {
    renderAt('/workspace/settings/api/applications/app-1', <ApplicationDetailsPage data={data} applicationId="app-1" onReload={vi.fn()} onBack={vi.fn()} />)
    expect(screen.getByRole('heading', { name: 'Acme Sync' })).toBeVisible()
    expect(screen.getByText((_, element) => element?.tagName === 'P' && Boolean(element.textContent?.startsWith('Created Oct 13, 2026 by')))).toBeVisible()
    expect(screen.getByText('Acme Inc · https://acme.test')).toBeVisible()
    expect(screen.getByText('https://acme.test/cb')).toBeVisible()
    expect(screen.getByText('Private to this workspace')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'OAuth credentials' })).toBeVisible()
    expect(screen.queryByText('Installs')).toBeNull()
  })

  it('shows the reference app\'s Webhooks section: status, URL, masked secret, events and delivery failures', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    const reload = vi.fn().mockResolvedValue(undefined)
    vi.mocked(api.fetchApplicationWebhookSecret).mockResolvedValue({ secret: 'flow_wh_current' })
    vi.mocked(api.rotateApplicationWebhookSecret).mockResolvedValue({ applicationId: 'app-1', clientId: 'client-1', enabled: true, url: 'https://hooks.acme.test', resourceTypes: ['Issue'], signingEnabled: true, installations: 1, secret: 'flow_wh_rotated' })
    vi.mocked(api.updateApplicationWebhook).mockResolvedValue({ applicationId: 'app-1', clientId: 'client-1', enabled: false, url: 'https://hooks.acme.test', resourceTypes: ['Issue'], signingEnabled: true, installations: 1 })
    const configured = { ...app, webhookResourceTypes: ['OAuthAuthorization', 'Issue'], githubUsername: 'acme-coder[bot]' }
    renderAt('/workspace/settings/api/applications/app-1', <ApplicationDetailsPage data={makeBootstrap({ oauthApplications: [configured], webhooks: [] })} applicationId="app-1" onReload={reload} onBack={vi.fn()} />)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    expect(screen.getByText('acme-coder[bot]')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Webhooks' })).toBeVisible()
    expect(screen.getByText('Enabled')).toBeVisible()
    expect(screen.getByText('https://hooks.acme.test')).toBeVisible()
    expect(screen.getByLabelText('Signing secret (hidden)')).toHaveTextContent('•'.repeat(32))
    expect(screen.getByText('Issues, OAuth authorization events')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Webhook delivery failures' })).toBeVisible()
    expect(await screen.findByText('No failures recorded for this webhook')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Copy webhook signing secret' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('flow_wh_current'))
    await user.click(screen.getByRole('button', { name: 'Rotate webhook signing secret' }))
    const { confirmAction } = await import('@/components/ui/action-dialog-service')
    expect(confirmAction).toHaveBeenCalledWith('Rotate your signing secret?', expect.objectContaining({ description: 'The current signing secret will be invalidated. You will need to update any services that verify webhook signatures. You cannot undo this action.', confirmLabel: 'Rotate' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('flow_wh_rotated'))
    expect(screen.queryByText('flow_wh_rotated')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Webhook options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Disable webhooks' }))
    await waitFor(() => expect(api.updateApplicationWebhook).toHaveBeenCalledWith('app-1', { enabled: false }))
  })

  it('opens Edit application from the menu and deletes only after typing the name', async () => {
    const user = userEvent.setup()
    vi.mocked(api.deleteOAuthApplication).mockResolvedValue(undefined)
    const reload = vi.fn().mockResolvedValue(undefined)
    renderAt('/workspace/settings/api/applications/app-1', <ApplicationDetailsPage data={data} applicationId="app-1" onReload={reload} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(await screen.findByRole('menuitem', { name: 'Download manifest' })).toBeVisible()
    expect(screen.getByRole('menuitem', { name: 'View in workspace' })).toBeVisible()
    await user.click(screen.getByRole('menuitem', { name: 'Delete application' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Delete the application "Acme Sync"?')).toBeVisible()
    const remove = within(dialog).getByRole('button', { name: 'Delete' })
    expect(remove).toBeDisabled()
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'Acme Sync' } })
    await user.click(remove)
    await waitFor(() => expect(api.deleteOAuthApplication).toHaveBeenCalledWith('app-1'))
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/api'))
  })

  it('downloads the manifest from the menu', async () => {
    const user = userEvent.setup()
    vi.mocked(api.exportOAuthApplicationManifest).mockResolvedValue({ schemaVersion: '1.0.0', oauth: { client_name: 'Acme Sync' } })
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:manifest'), revokeObjectURL: vi.fn() })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    renderAt('/workspace/settings/api/applications/app-1', <ApplicationDetailsPage data={data} applicationId="app-1" onReload={vi.fn()} onBack={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Download manifest' }))
    await waitFor(() => expect(click).toHaveBeenCalled())
    expect(api.exportOAuthApplicationManifest).toHaveBeenCalledWith('app-1')
    click.mockRestore()
  })
})
