import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { InstalledApplicationPage, InstalledApplicationsPage } from './installed-applications-section'

const apps = vi.hoisted(() => ({ listApplications: vi.fn(), uninstallApplication: vi.fn(), saveApplication: vi.fn() }))
const api = vi.hoisted(() => ({ fetchAuthorizedApplications: vi.fn(), revokeOAuthAuthorization: vi.fn() }))
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }))
const toasts = vi.hoisted(() => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/lib/application-agents', () => apps)
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/components/ui/action-dialog-service', () => dialogs)
vi.mock('@/lib/toast', () => toasts)

const authorization = { id: 'auth-1', clientId: 'client_a', clientName: 'Deploy bot', userId: viewer.id, scopes: ['read', 'write'], createdAt: '2026-10-13T12:00:00Z' }
const installation = { id: 'installation_a', workspaceKey: 'workspace', clientId: 'client_b', name: 'Triage agent', userId: 'app-user', installedBy: viewer.id, scopes: ['read', 'app:assignable'], teamIds: [], allPublicTeams: true, builtin: false, active: true, createdAt: '2026-10-13T12:00:00Z', updatedAt: '2026-10-13T12:00:00Z' }
const data = makeBootstrap({ viewerRole: 'admin' })

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
