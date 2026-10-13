import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { InstalledApplicationsSection } from './installed-applications-section'

const apps = vi.hoisted(() => ({ listApplications: vi.fn(), uninstallApplication: vi.fn() }))
const confirmAction = vi.hoisted(() => vi.fn())
vi.mock('@/lib/application-agents', () => apps)
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value, formatDate: () => 'Oct 1, 2026' }) }))

const installation = (patch: Record<string, unknown>) => ({ id: 'installation_a', workspaceKey: 'acme', clientId: 'client_a', name: 'Deploy bot', userId: 'app_a', installedBy: 'u1', scopes: ['read'], teamIds: ['t1'], builtin: false, active: true, createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z', ...patch })
const data = (role = 'admin') => ({ workspace: { urlKey: 'acme' }, viewerRole: role }) as never

describe('InstalledApplicationsSection', () => {
  beforeEach(() => {
    apps.listApplications.mockResolvedValue([installation({}), installation({ id: 'installation_flow', name: 'Flow', builtin: true })])
    apps.uninstallApplication.mockResolvedValue(undefined)
    confirmAction.mockReset()
  })

  it('lists installed applications except the built-in agent', async () => {
    render(<InstalledApplicationsSection data={data()} onReload={vi.fn()} />)
    expect(await screen.findByText('Deploy bot')).toBeVisible()
    expect(screen.queryByText('Flow')).toBeNull()
    expect(screen.getByText('Installed applications')).toBeVisible()
  })

  it('revokes access after confirmation and removes the row', async () => {
    confirmAction.mockResolvedValue(true)
    const onReload = vi.fn().mockResolvedValue(undefined)
    render(<InstalledApplicationsSection data={data()} onReload={onReload} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke access' }))
    await waitFor(() => expect(apps.uninstallApplication).toHaveBeenCalledWith('acme', 'installation_a'))
    expect(confirmAction).toHaveBeenCalledWith('Revoke access for {name}?'.replace('{name}', 'Deploy bot'), expect.objectContaining({ confirmLabel: 'Revoke access' }))
    await waitFor(() => expect(screen.queryByText('Deploy bot')).toBeNull())
    expect(onReload).toHaveBeenCalled()
  })

  it('does nothing when the confirmation is cancelled', async () => {
    confirmAction.mockResolvedValue(false)
    render(<InstalledApplicationsSection data={data()} onReload={vi.fn()} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke access' }))
    await waitFor(() => expect(confirmAction).toHaveBeenCalled())
    expect(apps.uninstallApplication).not.toHaveBeenCalled()
  })

  it('is hidden from non-admins', () => {
    const { container } = render(<InstalledApplicationsSection data={data('member')} onReload={vi.fn()} />)
    expect(container).toBeEmptyDOMElement()
    expect(apps.listApplications).not.toHaveBeenCalled()
  })
})
