import { useState } from 'react'

import { ChatBindingEditor, ChatBindingRow } from '@/components/settings/chat-group-bindings'
import { bindingProjectIds, confirmRemoveBinding, confirmRevokeBinding, useChatIntegrationSettings, useChatProjectGroups } from '@/components/settings/chat-binding-support'
import { ChatGroupDialog } from '@/components/settings/chat-group-dialog'
import { AppLink } from '@/components/ui/app-link'
import { ProjectGlyph } from '@/components/views/project-glyph'
import { useI18n } from '@/i18n/i18n'
import { integrationSettingsPath } from '@/lib/app-routes'
import { chatErrorMessage, removeChatBinding, revokeChatBinding, saveChatBinding } from '@/lib/chat-integrations-api'
import { chatProviderName } from '@/lib/integration-catalog'
import { toast } from '@/lib/toast'
import type { ChatIntegrationProvider, ChatProjectGroup } from '@/types/chat-integrations'
import type { BootstrapData, Project } from '@/types/flow'

/**
 * A project's Lark or Feishu groups, in place of its Slack channel: a binding
 * of one of the project's teams, scoped to the project. The team's owners
 * authorize it like any other group binding; everyone else who can see the
 * project sees the connected groups' names, read-only.
 */
export function ProjectChatGroupDialog({ open, onOpenChange, provider, project, data, canManage }: { open: boolean; onOpenChange: (open: boolean) => void; provider: ChatIntegrationProvider; project: Project; data?: BootstrapData; /** False when the opener already knows the viewer can't change the groups. */ canManage?: boolean }) {
  const { t } = useI18n()
  const name = chatProviderName(provider, t)
  const description = canManage === false ? t('The {name} groups connected to this project receive its notifications.') : t('Connect a {name} group to receive notifications about this project.')
  return <ChatGroupDialog open={open} onOpenChange={onOpenChange} provider={provider} subject={project.name} subjectIcon={<ProjectGlyph project={project} size={16}/>} description={description.replace('{name}', () => name)} className="project-chat-group-dialog">
    {open && <ProjectChatGroups provider={provider} project={project} data={data} onClose={() => onOpenChange(false)}/>}
  </ChatGroupDialog>
}

function ProjectChatGroups({ provider, project, data, onClose }: { provider: ChatIntegrationProvider; project: Project; data?: BootstrapData; onClose: () => void }) {
  const { t } = useI18n()
  const { groups, canManage, loading, reload } = useChatProjectGroups(provider, project.id)
  if (loading) return <p className="chat-muted" role="status">{t('Loading integration…')}</p>
  // Members who can't change the groups see which ones are connected, without the binding configuration.
  if (canManage === false) return <div className="chat-integration-settings project-chat-groups is-read-only">
    {groups && groups.length > 0 && <ProjectChatGroupRows groups={groups}/>}
    <p className="chat-muted">{t("Only owners of this project's teams can change its groups.")}</p>
  </div>
  return <ProjectChatGroupsEditor provider={provider} project={project} data={data} onClose={onClose} groups={groups ?? []} reloadGroups={reload}/>
}

/** Connected groups listed by name only: the read-only view, and other teams' groups in a manager's view. */
function ProjectChatGroupRows({ groups }: { groups: ChatProjectGroup[] }) {
  return <div className="feature-card">{groups.map(group => <div key={group.id} className="feature-row chat-inline chat-binding-row">
    <div><strong><span className="chat-binding-name" title={group.chatName} data-i18n-ignore>{group.chatName}</span></strong></div>
  </div>)}</div>
}

function ProjectChatGroupsEditor({ provider, project, data, onClose, groups, reloadGroups }: { provider: ChatIntegrationProvider; project: Project; data?: BootstrapData; onClose: () => void; groups: ChatProjectGroup[]; reloadGroups: () => Promise<void> }) {
  const { t } = useI18n()
  const name = chatProviderName(provider, t)
  const { settings, loading, error, setError, reload } = useChatIntegrationSettings(provider)
  const [busy, setBusy] = useState('')
  const [adding, setAdding] = useState(false)
  const workspace = data?.workspace.urlKey ?? location.pathname.split('/').filter(Boolean)[0] ?? ''
  const tenantAuthorized = settings?.permissions ? settings.permissions.tenant.status === 'authorized' : settings?.connection?.status === 'connected'
  const teamIds = (settings?.bindableTeamIds ?? []).filter(id => project.teamIds.includes(id))
  const bindings = (settings?.bindings ?? []).filter(binding => bindingProjectIds(binding)?.includes(project.id))
  // Groups of the project's other teams, which this viewer sees but doesn't manage.
  const others = groups.filter(group => !bindings.some(binding => binding.id === group.id))
  const mutate = async (action: string, operation: () => Promise<unknown>, success: string, after?: () => void) => {
    setBusy(action)
    setError('')
    try { await operation(); after?.(); toast.success(t(success)); await Promise.all([reload(), reloadGroups()]) }
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
    {others.length > 0 && <ProjectChatGroupRows groups={others}/>}
    {!teamIds.length
      ? <p className="chat-muted">{t("Only owners of this project's teams can connect groups to it.")}</p>
      : data && (adding || !bindings.length
        ? <ChatBindingEditor provider={provider} data={data} bindableTeamIds={teamIds} privateTeamIds={settings.permissions?.teams.privateTeamIds} fixedProjectId={project.id} defaultEvents={['project.update_created', 'issue.created', 'issue.status_changed']} notificationsTitle={t('Project notifications')} notificationsDescription={t('Send notifications to a {name} group.').replace('{name}', () => name)} busy={Boolean(busy)} onCancel={() => bindings.length ? setAdding(false) : onClose()} onSave={input => mutate('binding', () => saveChatBinding(provider, input), 'Group binding saved', () => setAdding(false))}/>
        : <button type="button" className="feature-button chat-dialog-add" disabled={Boolean(busy)} onClick={() => setAdding(true)}>{t('Connect {name} group').replace('{name}', () => name)}</button>)}
  </div>
}
