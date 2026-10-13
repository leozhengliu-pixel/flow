import { useCallback, useEffect, useState } from 'react'
import { AppWindow } from 'lucide-react'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { listApplications, uninstallApplication, type ApplicationInstallation } from '@/lib/application-agents'
import type { BootstrapData } from '@/types/flow'
import { SettingsRow as Row, SettingsSection as Section } from './settings-primitives'
import './installed-applications-section.css'

/**
 * Settings › Applications: applications installed in the workspace (agents and other app users), with the admin's
 * action to remove one (the reference app's "Revoke access"): its tokens are revoked, its active sessions stop and it leaves the
 * assignee and delegate pickers. The built-in Flow agent follows the server's agent setting and is not listed.
 */
export function InstalledApplicationsSection({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t, formatDate } = useI18n()
  const [items, setItems] = useState<ApplicationInstallation[]>()
  const [busy, setBusy] = useState('')
  const workspace = data.workspace.urlKey
  const admin = data.viewerRole === 'admin' || data.viewerRole === 'owner'
  const load = useCallback(() => listApplications(workspace).then(setItems).catch(() => setItems([])), [workspace])
  useEffect(() => { if (admin) void load() }, [admin, load])
  if (!admin) return null
  const installed = (items ?? []).filter(item => !item.builtin)
  const revoke = async (item: ApplicationInstallation) => {
    const confirmed = await confirmAction(t('Revoke access for {name}?').replace('{name}', item.name), {
      description: t('{name} will be removed from this workspace. Its access tokens are revoked, its active sessions are stopped and it can no longer be assigned or mentioned. Issues it worked on keep their history.').replace('{name}', item.name),
      confirmLabel: t('Revoke access'),
    })
    if (!confirmed) return
    setBusy(item.id)
    try {
      await uninstallApplication(workspace, item.id)
      setItems(current => current?.filter(entry => entry.id !== item.id))
      await onReload()
      toast.success(t('Access revoked for {name}').replace('{name}', item.name))
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not revoke access'))
    } finally {
      setBusy('')
    }
  }
  return <Section title={t('Installed applications')}>
    {installed.map(item => <Row
      key={item.id}
      icon={item.avatarUrl ? <img className="settings-installed-app-avatar" src={item.avatarUrl} alt="" /> : <AppWindow size={16} />}
      title={<span data-i18n-ignore>{item.name}</span>}
      description={`${t(item.active ? 'Active' : 'Suspended')} · ${t('installed')} ${formatDate(item.createdAt)}`}
    >
      <button type="button" className="settings-action danger" disabled={busy === item.id} onClick={() => void revoke(item)}>{t('Revoke access')}</button>
    </Row>)}
    {items !== undefined && !installed.length && <div className="settings-empty compact">
      <AppWindow size={24} />
      <h3>{t('No installed applications')}</h3>
      <p>{t('Applications installed in this workspace will appear here.')}</p>
    </div>}
  </Section>
}
