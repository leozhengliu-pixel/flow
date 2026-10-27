import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { InstalledApplicationPage, InstalledApplicationsPage } from './installed-applications-section'

const apps = vi.hoisted(() => ({ listApplications: vi.fn(), uninstallApplication: vi.fn(), saveApplication: vi.fn() }))
const api = vi.hoisted(() => ({ fetchAuthorizedApplications: vi.fn(), revokeOAuthAuthorization: vi.fn(), revokeMCPAccess: vi.fn() }))
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }))
const toasts = vi.hoisted(() => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/lib/application-agents', () => apps)
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/components/ui/action-dialog-service', () => dialogs)
vi.mock('@/lib/toast', () => toasts)

const authorization = { id: 'auth-1', clientId: 'client_a', clientName: 'Deploy bot', userId: viewer.id, scopes: ['read', 'write'], createdAt: '2026-10-13T12:00:00Z' }
const installation = { id: 'installation_a', workspaceKey: 'workspace', clientId: 'client_b', name: 'Triage agent', userId: 'app-user', installedBy: viewer.id, scopes: ['read', 'app:assignable'], teamIds: [], allPublicTeams: true, builtin: false, active: true, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z' }
const data = makeBootstrap({ viewerRole: 'admin' })
const mcpAuthorization = { id: 'mcp-1', clientId: 'flow_mcp_claude', clientName: 'Claude', userId: viewer.id, scopes: ['read'], createdAt: '2026-10-20T12:00:00Z', lastUsedAt: '2026-10-22T12:00:00Z' }
const other = data.users.find(user => user.id !== viewer.id)!
const mcpEntry = {
  clientId: 'mcp', kind: 'mcp' as const, name: 'MCP', developerName: 'Flow', developerUrl: 'https://flow.example.test',
  clientIds: ['flow_mcp_claude', 'flow_mcp_cursor'],
  authorizations: [mcpAuthorization, { ...mcpAuthorization, id: 'mcp-2', clientId: 'flow_mcp_cursor', clientName: 'Cursor', userId: other.id, scopes: ['read', 'write'], createdAt: '2026-10-21T12:00:00Z', lastUsedAt: undefined }],
  members: [{ userId: viewer.id, authorizedAt: '2026-10-20T12:00:00Z', lastUsedAt: '2026-10-22T12:00:00Z', scopes: ['read'] }, { userId: other.id, authorizedAt: '2026-10-21T12:00:00Z', scopes: ['read', 'write'] }],
  permissions: [{ scope: 'read', activelyUsed: true }, { scope: 'write', activelyUsed: false }],
}

function Where() {
  return <output data-testid="where">{useLocation().pathname}</output>
}
function renderAt(element: React.ReactNode) {
  return render(<I18nProvider><TooltipProvider><MemoryRouter initialEntries={['/workspace/settings/applications']}><Routes><Route path="*" element={<>{element}<Where/></>}/></Routes></MemoryRouter></TooltipProvider></I18nProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
  api.fetchAuthorizedApplications.mockResolvedValue([
    { clientId: 'client_a', name: 'Deploy bot', description: 'Ships builds', developerName: 'Deploy Co', developerUrl: 'https://deploy.test', authorizations: [authorization] },
    { clientId: 'client_b', name: 'Triage agent', authorizations: [] },
  ])
  apps.listApplications.mockResolvedValue([installation, { ...installation, id: 'builtin', clientId: 'flow', name: 'Flow', builtin: true }])
  api.revokeOAuthAuthorization.mockResolvedValue(undefined)
  apps.uninstallApplication.mockResolvedValue(undefined)
  apps.saveApplication.mockResolvedValue({ application: installation, webhookSecret: '' })
})

describe('InstalledApplicationsPage', () => {
  it('lists authorized applications like the reference app and opens one', async () => {
    const user = userEvent.setup()
    renderAt(<InstalledApplicationsPage data={data}/>)
    expect(await screen.findByText('Authorized by 1 member')).toBeVisible()
    expect(screen.getByText(/^App user installed by .+ on Oct 13$/)).toBeVisible()
    expect(screen.queryByText('Flow')).toBeNull()
    await user.click(screen.getByText('Deploy bot'))
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/applications/client_a')
  })

  it('shows well-known clients\' brand marks and keeps the generic icon for other applications', async () => {
    api.fetchAuthorizedApplications.mockResolvedValue([
      { clientId: 'client_a', name: 'Deploy bot', authorizations: [authorization] },
      { clientId: 'client_c', name: 'Cursor', redirectUris: ['cursor://anysphere.cursor-mcp/oauth/callback'], authorizations: [{ ...authorization, id: 'auth-2', clientId: 'client_c', clientName: 'Cursor' }] },
      { clientId: 'client_d', name: 'mcp-remote', redirectUris: ['https://claude.ai/api/mcp/auth_callback'], authorizations: [{ ...authorization, id: 'auth-3', clientId: 'client_d', clientName: 'mcp-remote' }] },
      { clientId: 'client_e', name: 'Uploaded', logoUrl: 'https://cdn.example.test/u.png', authorizations: [{ ...authorization, id: 'auth-4', clientId: 'client_e', clientName: 'Uploaded' }] },
    ])
    apps.listApplications.mockResolvedValue([])
    renderAt(<InstalledApplicationsPage data={data}/>)
    const row = (name: string) => screen.getByText(name).closest('a') as HTMLElement
    expect(await screen.findByRole('img', { name: 'Cursor' })).toHaveAttribute('data-known-app', 'cursor')
    expect(within(row('mcp-remote')).getByRole('img', { name: 'Claude' })).toHaveAttribute('data-known-app', 'claude')
    expect(row('Deploy bot').querySelector('.oauth-app-avatar.is-placeholder .oauth-icon-placeholder')).not.toBeNull()
    expect(within(row('Uploaded')).getByRole('img', { name: 'Uploaded' })).toHaveAttribute('src', 'https://cdn.example.test/u.png')
    expect(row('Cursor').querySelector('.oauth-app-avatar')).toHaveStyle({ width: '32px', height: '32px' })
  })

  it('lists every MCP client as one "MCP" application by Flow next to other applications', async () => {
    const user = userEvent.setup()
    api.fetchAuthorizedApplications.mockResolvedValue([mcpEntry, { clientId: 'client_a', name: 'Deploy bot', authorizations: [authorization] }])
    apps.listApplications.mockResolvedValue([])
    renderAt(<InstalledApplicationsPage data={data}/>)
    const row = (await screen.findByText('MCP')).closest('a') as HTMLElement
    expect(within(row).getByText('Authorized by 2 members')).toBeVisible()
    expect(within(row).getByRole('img', { name: 'Flow' })).toHaveClass('is-flow')
    expect(screen.getAllByRole('link').map(link => link.textContent)).toEqual(['MCPAuthorized by 2 members', 'Deploy botAuthorized by 1 member'])
    expect(screen.queryByText('Claude')).toBeNull()
    await user.click(row)
    expect(screen.getByTestId('where')).toHaveTextContent('/workspace/settings/applications/mcp')
  })

  it('folds a member\'s own MCP authorizations into "MCP" (singular count)', async () => {
    const member = makeBootstrap({ viewerRole: 'member', oauthAuthorizations: [
      { ...mcpAuthorization, id: 'own-1' },
      { ...mcpAuthorization, id: 'own-2', clientId: 'flow_mcp_cursor', clientName: 'Cursor' },
      authorization,
    ] })
    renderAt(<InstalledApplicationsPage data={member}/>)
    const row = (await screen.findByText('MCP')).closest('a') as HTMLElement
    expect(within(row).getByText('Authorized by 1 member')).toBeVisible()
    expect(screen.getByText('Deploy bot')).toBeVisible()
    expect(screen.queryByText('Cursor')).toBeNull()
    expect(api.fetchAuthorizedApplications).not.toHaveBeenCalled()
  })

  it('shows the reference app\'s empty state', async () => {
    api.fetchAuthorizedApplications.mockResolvedValue([])
    apps.listApplications.mockResolvedValue([])
    renderAt(<InstalledApplicationsPage data={data}/>)
    expect(await screen.findByText('Your workspace has not yet authorized any external applications to connect with your Flow account')).toBeVisible()
  })
})

describe('InstalledApplicationPage', () => {
  it('shows the permissions and members, and revokes every token after confirmation', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(true)
    const onReload = vi.fn().mockResolvedValue(undefined)
    renderAt(<InstalledApplicationPage data={data} clientId="client_a" onReload={onReload}/>)
    expect(await screen.findByText('Read access to your workspace')).toBeVisible()
    expect(screen.getByText('Write access to your workspace')).toBeVisible()
    expect(screen.getByText('by Deploy Co')).toBeVisible()
    expect(screen.getByText('Authorized on Oct 13')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Revoke access' }))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Revoke access for "Deploy bot"?', expect.objectContaining({ description: 'This will revoke all existing tokens for "Deploy bot". You cannot undo this action.', confirmLabel: 'Revoke', danger: true }))
    await waitFor(() => expect(api.revokeOAuthAuthorization).toHaveBeenCalledWith('auth-1'))
    expect(toasts.toast.success).toHaveBeenCalledWith('Tokens revoked', { description: 'The tokens for "Deploy bot" have been revoked.' })
    expect(onReload).toHaveBeenCalled()
  })

  it('shows the app user with its team access and revokes the installation', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(true)
    renderAt(<InstalledApplicationPage data={data} clientId="client_b" onReload={vi.fn().mockResolvedValue(undefined)}/>)
    expect(await screen.findByRole('heading', { name: 'App user' })).toBeVisible()
    expect(screen.getByText('Agent')).toBeVisible()
    const access = screen.getByRole('radiogroup', { name: 'Team access' })
    expect(within(access).getByRole('radio', { name: 'All public teams' })).toBeChecked()
    expect(within(access).getByRole('button', { name: 'Update' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'App user options' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Revoke access…' }))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Revoke access for Triage agent?', expect.objectContaining({ description: 'The application will lose access to the workspace for now. It can be reauthorized in the future.', confirmLabel: 'Confirm' }))
    await waitFor(() => expect(apps.uninstallApplication).toHaveBeenCalledWith('workspace', 'installation_a'))
    expect(toasts.toast.success).toHaveBeenCalledWith('Access revoked', { description: 'Access has been revoked for Triage agent' })
  })

  it('lists "Receive realtime updates" alphabetically when the application has a webhook', async () => {
    api.fetchAuthorizedApplications.mockResolvedValue([{ clientId: 'client_a', name: 'Deploy bot', authorizations: [authorization], realtime: true }])
    renderAt(<InstalledApplicationPage data={data} clientId="client_a" onReload={vi.fn()}/>)
    await screen.findByText('Receive realtime updates about your workspace')
    const rows = [...document.querySelectorAll('.oauth-app-permission')].map(row => row.textContent)
    expect(rows).toEqual(['Read access to your workspace', 'Receive realtime updates about your workspace', 'Write access to your workspace'])
  })

  it('grants all public teams and select private teams after the reference app\'s confirmation', async () => {
    const user = userEvent.setup()
    const secret = { ...data.teams[0], id: 'team-secret', name: 'Secret', key: 'SEC', private: true }
    const withPrivate = { ...data, teams: [...data.teams, secret] }
    renderAt(<InstalledApplicationPage data={withPrivate} clientId="client_b" onReload={vi.fn().mockResolvedValue(undefined)}/>)
    const access = await screen.findByRole('radiogroup', { name: 'Team access' })
    await user.click(within(access).getByRole('radio', { name: 'All public teams and select other teams…' }))
    expect(within(access).getByRole('button', { name: 'Update' })).toBeDisabled()
    await user.click(within(access).getByRole('combobox', { name: 'Select other teams…' }))
    // Only private teams are offered next to all public teams.
    expect(within(access).getAllByRole('option').map(option => option.textContent)).toEqual(['Secret'])
    await user.click(within(access).getByRole('option', { name: 'Secret' }))
    await user.click(within(access).getByRole('button', { name: 'Update' }))
    const dialog = await screen.findByRole('dialog', { name: 'Update team access for Triage agent?' })
    expect(dialog).toHaveTextContent('The application currently has access to all public teams.')
    expect(dialog).toHaveTextContent('The application will now have access to all public teams and these other teams:Secret')
    expect(apps.saveApplication).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(apps.saveApplication).toHaveBeenCalledWith('workspace', expect.objectContaining({ id: 'installation_a', allPublicTeams: true, teamIds: ['team-secret'] })))
    expect(toasts.toast.success).toHaveBeenCalledWith('Team access updated', { description: 'The application’s team access has been updated.' })
  })

  it('shows "MCP" by Flow with its website, used permissions and members, and revokes every MCP client at once', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(true)
    api.fetchAuthorizedApplications.mockResolvedValue([mcpEntry])
    api.revokeMCPAccess.mockResolvedValue({ revoked: 2 })
    const onReload = vi.fn().mockResolvedValue(undefined)
    renderAt(<InstalledApplicationPage data={data} clientId="mcp" onReload={onReload}/>)
    expect(await screen.findByRole('heading', { name: 'MCP' })).toBeVisible()
    expect(screen.getByText('by Flow')).toBeVisible()
    expect(document.querySelector('.oauth-app-page-header .oauth-app-avatar.is-flow')).toHaveStyle({ width: '48px', height: '48px' })
    expect(screen.getByText('Developer website')).toBeVisible()
    expect(screen.getByText('https://flow.example.test')).toBeVisible()
    const rows = [...document.querySelectorAll('.oauth-app-permission')].map(row => row.textContent)
    expect(rows).toEqual(['Read access to your workspaceActively used', 'Write access to your workspace'])
    const members = [...document.querySelectorAll('.oauth-app-member')]
    expect(members).toHaveLength(2)
    expect(members[0]).toHaveTextContent('Authorized on Oct 20 · Last used on Oct 22')
    expect(members[1]).toHaveTextContent('Authorized on Oct 21')
    expect(members[1]).not.toHaveTextContent('Last used')
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(screen.getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Revoke access'])
    await user.click(screen.getByRole('menuitem', { name: 'Revoke access' }))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Revoke access for "MCP"?', expect.objectContaining({ confirmLabel: 'Revoke', danger: true }))
    await waitFor(() => expect(api.revokeMCPAccess).toHaveBeenCalledTimes(1))
    expect(api.revokeOAuthAuthorization).not.toHaveBeenCalled()
    expect(toasts.toast.success).toHaveBeenCalledWith('Tokens revoked', { description: 'The tokens for "MCP" have been revoked.' })
    expect(onReload).toHaveBeenCalled()
  })

  it('opens "MCP" from an MCP client\'s own link, and lets a member revoke their own MCP access', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(true)
    api.revokeMCPAccess.mockResolvedValue({ revoked: 1 })
    const member = makeBootstrap({ viewerRole: 'member', oauthAuthorizations: [mcpAuthorization] })
    renderAt(<InstalledApplicationPage data={member} clientId="flow_mcp_claude" onReload={vi.fn().mockResolvedValue(undefined)}/>)
    expect(await screen.findByRole('heading', { name: 'MCP' })).toBeVisible()
    expect(screen.getByText(window.location.origin)).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Revoke access' }))
    await waitFor(() => expect(api.revokeMCPAccess).toHaveBeenCalledTimes(1))
  })

  it('does nothing when the confirmation is cancelled', async () => {
    const user = userEvent.setup()
    dialogs.confirmAction.mockResolvedValue(false)
    renderAt(<InstalledApplicationPage data={data} clientId="client_a" onReload={vi.fn()}/>)
    await user.click(await screen.findByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Revoke access' }))
    await waitFor(() => expect(dialogs.confirmAction).toHaveBeenCalled())
    expect(api.revokeOAuthAuthorization).not.toHaveBeenCalled()
  })
})
