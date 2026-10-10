/**
 * The bell in the document header: a popover listing workspace members with a
 * checkbox each (Linear's "Change document subscribers"). The viewer's own row
 * uses the subscription API (so the sidebar/inbox preferences refresh); the
 * other rows replace the document's subscriber list.
 */
import * as Popover from '@radix-ui/react-popover'
import { Bell } from 'lucide-react'
import { useMemo, useState } from 'react'

import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument, User } from '@/types/flow'
import { setDocumentSubscribers, type DocumentActionContext } from './document-actions'

const FILTER_THRESHOLD = 8

export function DocumentSubscribersPopover({ ctx, document, subscribed, busy, onToggleViewer, open, onOpenChange, disabled }: {
  ctx: DocumentActionContext
  document: FlowDocument
  /** Whether the viewer is subscribed (subscription records or the document's subscriber list). */
  subscribed: boolean
  busy: boolean
  onToggleViewer: () => void | Promise<unknown>
  open: boolean
  onOpenChange: (open: boolean) => void
  disabled?: boolean
}) {
  const { t } = useI18n()
  const viewer = ctx.data.viewer
  const [query, setQuery] = useState('')
  const [pending, setPending] = useState<string>()
  const users = useMemo(() => {
    const others = ctx.data.users.filter(user => !user.app && user.active !== false && user.id !== viewer.id)
    const needle = query.trim().toLowerCase()
    const everyone = [viewer, ...others]
    return needle ? everyone.filter(user => `${user.displayName} ${user.name} ${user.username ?? ''}`.toLowerCase().includes(needle)) : everyone
  }, [ctx.data.users, viewer, query])

  const toggle = async (user: User, checked: boolean) => {
    if (pending) return
    if (user.id === viewer.id) { await onToggleViewer(); return }
    setPending(user.id)
    const current = document.subscriberIds ?? []
    const next = checked ? current.filter(id => id !== user.id) : [...current, user.id]
    await setDocumentSubscribers(ctx, document, next)
    setPending(undefined)
  }

  const isSubscribed = (user: User) => user.id === viewer.id ? subscribed : (document.subscriberIds ?? []).includes(user.id)
  return <Popover.Root open={open} onOpenChange={next => { onOpenChange(next); if (!next) setQuery('') }}>
    <ScopedFlowTooltip label={t(subscribed ? 'Unsubscribe' : 'Subscribe')} shortcut="⇧ S" disabled={open}>
      <Popover.Trigger asChild>
        <button aria-label={t('Document subscribers')} aria-busy={busy || undefined} className={`document-icon-button${subscribed ? ' is-active' : ''}`} disabled={disabled} type="button">
          <Bell size={16} fill={subscribed ? 'currentColor' : 'none'}/>
        </button>
      </Popover.Trigger>
    </ScopedFlowTooltip>
    <Popover.Portal>
      <Popover.Content align="end" className="document-subscribers" collisionPadding={10} data-flow-motion="floating" onCloseAutoFocus={event => event.preventDefault()} sideOffset={4}>
        {ctx.data.users.length > FILTER_THRESHOLD && <input aria-label={t('Change document subscribers…')} autoComplete="off" className="document-subscribers__filter" onChange={event => setQuery(event.target.value)} placeholder={t('Change document subscribers…')} spellCheck={false} value={query}/>}
        <div className="document-subscribers__list" role="group" aria-label={t('Document subscribers')}>
          {users.map(user => {
            const checked = isSubscribed(user)
            const name = user.displayName || user.name
            return <button aria-checked={checked} aria-label={name} className="document-subscribers__row" disabled={busy || Boolean(pending)} key={user.id} onClick={() => void toggle(user, checked)} role="checkbox" type="button">
              <span className="document-subscribers__check" data-checked={checked}>{checked && <CheckboxMark/>}</span>
              <UserAvatar avatarUrl={user.avatarUrl} className="document-meta-avatar" name={name}/>
              <span className="document-subscribers__name" data-i18n-ignore>{name}</span>
            </button>
          })}
          {!users.length && <p className="document-subscribers__empty">{t('No results')}</p>}
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
