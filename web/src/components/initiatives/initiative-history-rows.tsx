import { useEffect, useMemo, useState } from 'react'

import { coalesceHistoryEntries, describeHistoryEntry } from '@/components/activity/entity-history-model'
import { Avatar } from '@/components/issue/issue-row'
import { AttributedAction } from '@/components/ui/via-app'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { activityActor } from '@/lib/actor-attribution'
import { listInitiativeHistory } from '@/lib/api'
import type { AuditLogEntry, Initiative, User } from '@/types/flow'

/**
 * The initiative's own activity rows, newest first: property changes from its history and its creation, each naming
 * who did it ("Flow changed the status to Planned on behalf of Dev User").
 */
export function InitiativeHistoryRows({ initiative, users }: { initiative: Initiative; users: User[] }) {
  const { t, locale } = useI18n()
  const [history, setHistory] = useState<AuditLogEntry[]>([])
  useEffect(() => {
    let active = true
    listInitiativeHistory(initiative.id).then(page => { if (active) setHistory(page?.nodes ?? []) }).catch(() => undefined)
    return () => { active = false }
  }, [initiative.id, initiative.updatedAt])
  const rows = useMemo(() => {
    const changes = coalesceHistoryEntries(history).flatMap(entry => {
      const text = entry.action === 'created' ? null : describeHistoryEntry(entry, 'initiative', t, locale)
      return text ? [{ id: entry.id, actor: activityActor(entry.actor, entry.botActor, users), text, createdAt: entry.createdAt, created: false }] : []
    })
    const creator = initiative.creator?.id ? initiative.creator : undefined
    const created = { id: `created-${initiative.id}`, actor: activityActor(creator, initiative.botActor, users), text: t('created the initiative'), createdAt: initiative.createdAt, created: true }
    return [...changes.reverse(), created]
  }, [history, initiative.botActor, initiative.createdAt, initiative.creator, initiative.id, locale, t, users])
  const day = (value: string) => { const date = new Date(value); return Number.isNaN(date.getTime()) ? '' : new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(date) }
  return <>{rows.map(row => (
    <div className="li-created-event" data-history-row={row.created ? 'created' : 'change'} key={row.id}>
      {row.created
        ? <ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/>
        : <span className="li-history-avatar"><Avatar name={row.actor.name} avatarUrl={row.actor.avatarUrl}/></span>}
      <span>{row.actor.name ? <AttributedAction actor={row.actor}>{row.text}</AttributedAction> : row.text}{day(row.createdAt) && ` · ${day(row.createdAt)}`}</span>
    </div>
  ))}</>
}
