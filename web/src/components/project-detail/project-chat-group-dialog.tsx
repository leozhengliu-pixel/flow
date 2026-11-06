import { useState } from 'react'

import { ChatBindingEditor, ChatBindingRow } from '@/components/settings/chat-group-bindings'
import { bindingProjectIds, confirmRemoveBinding, confirmRevokeBinding, useChatIntegrationSettings } from '@/components/settings/chat-binding-support'
import { ChatGroupDialog } from '@/components/settings/chat-group-dialog'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { integrationSettingsPath } from '@/lib/app-routes'
import { chatErrorMessage, removeChatBinding, revokeChatBinding, saveChatBinding } from '@/lib/chat-integrations-api'
import { chatProviderName } from '@/lib/integration-catalog'
import { toast } from '@/lib/toast'
import type { ChatIntegrationProvider } from '@/types/chat-integrations'
import type { BootstrapData, Project } from '@/types/flow'

/**
 * A project's Lark or Feishu groups, in place of its Slack channel: a binding
 * of one of the project's teams, scoped to the project. The team's owners
 * authorize it like any other group binding.
 */
export function ProjectChatGroupDialog({ open, onOpenChange, provider, project, data }: { open: boolean; onOpenChange: (open: boolean) => void; provider: ChatIntegrationProvider; project: Project; data?: BootstrapData }) {
  const { t } = useI18n()
  const name = chatProviderName(provider, t)
  return <ChatGroupDialog open={open} onOpenChange={onOpenChange} provider={provider} subject={project.name} description={t('Connect a {name} group to receive notifications about this project.').replace('{name}', () => name)} className="project-chat-group-dialog">
    {open && <ProjectChatGroups provider={provider} project={project} data={data}/>}
  </ChatGroupDialog>
}

function ProjectChatGroups({ provider, project, data }: { provider: ChatIntegrationProvider; project: Project; data?: BootstrapData }) {
  const { t } = useI18n()
  const name = chatProviderName(provider, t)
  const { settings, loading, error, setError, reload } = useChatIntegrationSettings(provider)
  const [busy, setBusy] = useState('')
  const [adding, setAdding] = useState(false)
  const workspace = data?.workspace.urlKey ?? location.pathname.split('/').filter(Boolean)[0] ?? ''
  const tenantAuthorized = settings?.permissions ? settings.permissions.tenant.status === 'authorized' : settings?.connection?.status === 'connected'
  const teamIds = (settings?.bindableTeamIds ?? []).filter(id => project.teamIds.includes(id))
  const bindings = (settings?.bindings ?? []).filter(binding => bindingProjectIds(binding)?.includes(project.id))
  const mutate = async (action: string, operation: () => Promise<unknown>, success: string, after?: () => void) => {
    setBusy(action)
    setError('')
    try { await operation(); after?.(); toast.success(t(success)); await reload() }
    catch (cause) { setError(chatErrorMessage(cause, t, 'Could not update integration')) }
    finally { setBusy('') }
  }
  if (loading && !settings) return <p className="chat-muted" role="status">{t('Loading integration…')}</p>
  if (!settings?.connection || !tenantAuthorized) return <AppLink className="feature-button primary chat-group-dialog__connect" href={integrationSettingsPath(workspace, provider)}>{t('Connect {name}').replace('{name}', () => name)}</AppLink>
  return <div className="chat-integration-settings project-chat-groups">
    {error && <p className="chat-integration-alert" role="alert">{error}</p>}
    {data && bindings.length > 0 && <div className="feature-card">{bindings.map(binding => <ChatBindingRow key={binding.id} binding={binding} data={data} busy={busy} canEdit={false}
      onRevoke={() => void (async () => { if (await confirmRevokeBinding(binding, t)) await mutate(`revoke:${binding.id}`, () => revokeChatBinding(provider, binding.id), 'Group binding revoked') })()}
      onRemove={() => void (async () => { if (await confirmRemoveBinding(binding, t)) await mutate(`remove:${binding.id}`, () => removeChatBinding(provider, binding.id), 'Group binding removed') })()}/>)}</div>}
    {!teamIds.length
      ? <p className="chat-muted">{t("Only owners of this project's teams can connect groups to it.")}</p>
      : data && (adding
        ? <ChatBindingEditor provider={provider} data={data} bindableTeamIds={teamIds} privateTeamIds={settings.permissions?.teams.privateTeamIds} fixedProjectId={project.id} defaultEvents={['project.update_created', 'issue.created', 'issue.status_changed']} busy={Boolean(busy)} onCancel={() => setAdding(false)} onSave={input => mutate('binding', () => saveChatBinding(provider, input), 'Group binding saved', () => setAdding(false))}/>
        : <button type="button" className="feature-button" disabled={Boolean(busy)} onClick={() => setAdding(true)}>{t('Connect {name} group').replace('{name}', () => name)}</button>)}
  </div>
}
