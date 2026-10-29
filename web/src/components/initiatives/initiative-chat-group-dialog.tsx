import * as Dialog from '@radix-ui/react-dialog'
import { Ban, Check, RefreshCw, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'

import { useBotChats } from '@/components/settings/chat-binding-support'
import { FieldRow } from '@/components/settings/chat-group-bindings'
import { IntegrationBrandIcon } from '@/components/settings/integration-brand-icon'
import { SettingsSelect, SettingsToggle } from '@/components/settings/settings-primitives'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { AppLink } from '@/components/ui/app-link'
import { GridLoader } from '@/components/ui/grid-loader'
import { useI18n } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import { integrationSettingsPath } from '@/lib/app-routes'
import { chatErrorMessage, fetchChatInitiativeBindings, removeChatInitiativeBinding, revokeChatInitiativeBinding, saveChatInitiativeBinding } from '@/lib/chat-integrations-api'
import { chatProviderName } from '@/lib/integration-catalog'
import { toast } from '@/lib/toast'
import type { ChatInitiativeBinding, ChatInitiativeBindings, ChatIntegrationProvider } from '@/types/chat-integrations'
import type { Initiative, User } from '@/types/flow'
import '@/components/settings/feature-settings.css'
import '@/components/settings/chat-integration-settings.css'

const NO_SELECTION = '__none__'

/**
 * An initiative's own Lark or Feishu groups, whether or not a team leads it:
 * the initiative's owner or a workspace administrator connects a group, which
 * then receives the initiative's updates (never a private team's content).
 */
export function InitiativeChatGroupDialog({ open, onOpenChange, provider, initiative, users = [] }: { open: boolean; onOpenChange: (open: boolean) => void; provider: ChatIntegrationProvider; initiative: Initiative; users?: User[] }) {
  const { t } = useI18n()
  const name = chatProviderName(provider, t)
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/>
    <Dialog.Content data-flow-motion="dialog" className="project-detail-page__form-dialog project-slack-dialog project-chat-group-dialog initiative-chat-group-dialog">
      <Dialog.Title><IntegrationBrandIcon provider={provider} size={16}/>{t('{name} notifications for').replace('{name}', name)} <span data-i18n-ignore>{initiative.name}</span></Dialog.Title>
      <Dialog.Description>{t("Connect a {name} group to receive this initiative's updates.").replace('{name}', name)}</Dialog.Description>
      {open && <InitiativeChatGroups provider={provider} initiative={initiative} users={users}/>}
      <footer><Dialog.Close asChild><button type="button">{t('Close')}</button></Dialog.Close></footer>
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>
}

function InitiativeChatGroups({ provider, initiative, users }: { provider: ChatIntegrationProvider; initiative: Initiative; users: User[] }) {
  const { t, formatDate } = useI18n()
  const name = chatProviderName(provider, t)
  const [state, setState] = useState<ChatInitiativeBindings | null>(null)
  const [missing, setMissing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [adding, setAdding] = useState(false)
  const mounted = useRef(true)
  const workspace = location.pathname.split('/').filter(Boolean)[0] ?? ''

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const next = await fetchChatInitiativeBindings(provider, initiative.id)
      if (mounted.current) { setState(next); setMissing(false) }
    } catch (cause) {
      if (!mounted.current) return
      if (cause instanceof ApiError && cause.code === 'chat_not_configured') setMissing(true)
      else setError(chatErrorMessage(cause, t, 'Could not load group bindings.'))
    } finally { if (mounted.current) setLoading(false) }
  }, [provider, initiative.id, t])
  useEffect(() => {
    mounted.current = true
    queueMicrotask(() => { if (mounted.current) void load() })
    return () => { mounted.current = false }
  }, [load])

  const mutate = async (action: string, operation: () => Promise<unknown>, success: string, after?: () => void) => {
    setBusy(action)
    setError('')
    try { await operation(); after?.(); toast.success(t(success)); await load() }
    catch (cause) { setError(chatErrorMessage(cause, t, 'Could not update integration')) }
    finally { setBusy('') }
  }
  const revoke = async (binding: ChatInitiativeBinding) => {
    const confirmed = await confirmAction(t('Revoke group binding?'), { description: t("{group} stops receiving this initiative's updates. The binding stays listed as revoked.").replace('{group}', binding.chatName), confirmLabel: t('Revoke'), danger: true })
    if (confirmed) await mutate(`revoke:${binding.id}`, () => revokeChatInitiativeBinding(provider, binding.id), 'Group binding revoked')
  }
  const remove = async (binding: ChatInitiativeBinding) => {
    if (await confirmAction(t('Remove group binding?'), { description: binding.chatName, confirmLabel: t('Remove'), danger: true })) await mutate(`remove:${binding.id}`, () => removeChatInitiativeBinding(provider, binding.id), 'Group binding removed')
  }

  if (loading && !state && !missing) return <p className="chat-muted" role="status">{t('Loading integration…')}</p>
  if (missing || state && !state.tenantAuthorized) return <AppLink className="project-slack-dialog__connect" href={integrationSettingsPath(workspace, provider)}>{t('Connect {name}').replace('{name}', name)}</AppLink>
  return <div className="chat-integration-settings project-chat-groups initiative-chat-groups">
    {error && <p className="chat-integration-alert" role="alert">{error}</p>}
    {state && state.bindings.length > 0 && <div className="feature-card">{state.bindings.map(binding => {
      const authorizer = users.find(user => user.id === binding.authorization?.userId)
      const revoked = Boolean(binding.revokedAt)
      return <div key={binding.id} className="feature-row chat-inline chat-binding-row" data-revoked={revoked || undefined}>
        <div>
          <strong>
            <span className="chat-binding-name" title={binding.chatName} data-i18n-ignore>{binding.chatName}</span>
            {revoked ? <small className="chat-binding-badge is-revoked">{t('Revoked')}</small> : !binding.enabled && <small>{t('Paused')}</small>}
          </strong>
          {binding.authorization && <span className="chat-binding-authorization">
            {t('Authorized by {name} on {date}').replace('{name}', authorizer?.displayName || authorizer?.name || t('a member')).replace('{date}', formatDate(binding.authorization.authorizedAt, { dateStyle: 'medium' }))}
            {revoked && binding.revokedAt && ` · ${t('Revoked on {date}').replace('{date}', formatDate(binding.revokedAt, { dateStyle: 'medium' }))}`}
          </span>}
        </div>
        {state.canManage && <aside>
          {!revoked && <button type="button" className="feature-button chat-icon-button" aria-label={`${t('Revoke group binding')}: ${binding.chatName}`} title={t('Revoke group binding')} disabled={Boolean(busy)} onClick={() => void revoke(binding)}>{busy === `revoke:${binding.id}` ? <GridLoader size={14}/> : <Ban size={14} aria-hidden="true"/>}</button>}
          <button type="button" className="feature-button danger chat-icon-button" aria-label={`${t('Remove group binding')}: ${binding.chatName}`} title={t('Remove group binding')} disabled={Boolean(busy)} onClick={() => void remove(binding)}>{busy === `remove:${binding.id}` ? <GridLoader size={14}/> : <Trash2 size={14} aria-hidden="true"/>}</button>
        </aside>}
      </div>
    })}</div>}
    {!state?.canManage
      ? <p className="chat-muted">{t("Only the initiative's owner and workspace administrators can connect it to groups.")}</p>
      : !state.shareable
        ? <p className="chat-muted">{t("Initiatives led by a private team can't be connected to a group.")}</p>
        : adding
          ? <InitiativeChatBindingEditor provider={provider} initiative={initiative} busy={Boolean(busy)} onCancel={() => setAdding(false)} onSave={(chatId, chatName) => mutate('binding', () => saveChatInitiativeBinding(provider, { initiativeId: initiative.id, chatId, chatName, enabled: true, authorize: true }), 'Group binding saved', () => setAdding(false))}/>
          : <button type="button" className="feature-button" disabled={Boolean(busy)} onClick={() => setAdding(true)}>{t('Connect {name} group').replace('{name}', name)}</button>}
  </div>
}

function InitiativeChatBindingEditor({ provider, initiative, busy, onCancel, onSave }: { provider: ChatIntegrationProvider; initiative: Initiative; busy: boolean; onCancel: () => void; onSave: (chatId: string, chatName: string) => Promise<void> }) {
  const { t } = useI18n()
  const { chats, cursor, hasMore, loading, error, loadChats } = useBotChats(provider, initiative.id)
  const [chatId, setChatId] = useState('')
  const [approved, setApproved] = useState(false)
  const selected = chats.find(chat => chat.id === chatId)
  const options = chats.map(chat => ({ value: chat.id, label: chat.external ? `${chat.name} (${t('External group')})` : chat.name, disabled: chat.external, entityName: true }))
  const valid = Boolean(selected && !selected.external && approved)
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (valid && selected) void onSave(selected.id, selected.name)
  }
  const title = t('Add group binding')
  return <form className="feature-section chat-binding-editor" onSubmit={submit} aria-label={title}>
    <header><h2>{title}</h2><p role="status">{loading ? t('Loading groups…') : !chats.length && !error ? t('No groups found. Add the bot to an internal group and refresh.') : t('Only internal groups can be bound.')}</p></header>
    <fieldset className="chat-fieldset" disabled={busy}>
      {error && <div className="feature-card chat-integration-alert" role="alert"><div className="feature-row"><div><span>{error}</span></div><aside><button type="button" className="feature-button" disabled={loading} onClick={() => void loadChats(cursor)}><RefreshCw size={14} aria-hidden="true"/>{t('Retry')}</button></aside></div></div>}
      <div className="feature-card">
        <FieldRow label={t('Group')}>
          <SettingsSelect label={t('Group')} options={options} value={chatId || NO_SELECTION} placeholder={t('Select group')} disabled={loading || !options.length} onChange={value => { setChatId(value); setApproved(false) }}/>
          <button type="button" className="feature-button chat-icon-button" aria-label={t('Refresh groups')} title={t('Refresh groups')} disabled={loading} onClick={() => void loadChats()}>{loading ? <GridLoader size={14}/> : <RefreshCw size={14} aria-hidden="true"/>}</button>
        </FieldRow>
        {hasMore && <FieldRow label={<span className="chat-muted">{t('More groups are available.')}</span>}><button type="button" className="feature-button" disabled={loading || !cursor} onClick={() => void loadChats(cursor)}>{t('Load more groups')}</button></FieldRow>}
      </div>
      <div className="feature-card chat-approval">
        <div className="feature-row chat-private-notice" role="note"><div><span>{t("The group receives this initiative's updates only. Nothing of its teams' private work is shared, and connecting doesn't change who can see the initiative in Flow.")}</span></div></div>
        <FieldRow inline label={t('Share with this group')} description={t("I approve sharing this initiative's updates with everyone in this group.")}><SettingsToggle label={t("I approve sharing this initiative's updates with everyone in this group.")} checked={approved} onChange={setApproved}/></FieldRow>
      </div>
      <div className="feature-section-action chat-actions">
        <button type="button" className="feature-button" disabled={busy} onClick={onCancel}>{t('Cancel')}</button>
        <button type="submit" className="feature-button primary" disabled={busy || !valid}>{busy ? <GridLoader size={14}/> : <Check size={14} aria-hidden="true"/>}{t('Save group binding')}</button>
      </div>
    </fieldset>
  </form>
}
