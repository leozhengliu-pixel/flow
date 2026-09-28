/**
 * LS-0099 BaseUpdateInboxView — shared update split chrome for project and
 * initiative update hosts (title header, subscribe, write form, update stream).
 */
import { useMemo, useState } from 'react'
import { Check, PenSquare } from 'lucide-react'

import type { Project, User } from '@/types/flow'

import { healthLabel } from './inbox-host-types'

import './inbox-hosts.css'

export type BaseUpdateItem = {
  id: string
  body: string
  health: Project['health']
  createdAt: string
  editedAt?: string
  user: User
  commentCount?: number
}

export type BaseUpdateInboxViewProps = {
  /** LS id for storybook / tests */
  surfaceId: 'LS-0099' | 'LS-0493' | 'LS-0320' | 'LS-0491' | 'LS-0317'
  entityKind: 'project' | 'initiative'
  entityName: string
  entityColor: string
  entityIcon?: string
  subscribed?: boolean
  onSubscribeChange?: (subscribed: boolean) => void
  updates: BaseUpdateItem[]
  /** Highlight a specific update from the notification payload when present. */
  initialUpdateId?: string
  viewer: User
  promptMode?: boolean
  emptyTitle?: string
  emptyBody?: string
  onOpenEntity: () => void
  onCreateUpdate?: (input: { body: string; health: Project['health'] }) => Promise<void>
}

const HEALTH_OPTIONS: { id: Project['health']; label: string }[] = [
  { id: 'onTrack', label: 'On track' },
  { id: 'atRisk', label: 'At risk' },
  { id: 'offTrack', label: 'Off track' },
]


export function BaseUpdateInboxView({
  surfaceId,
  entityKind,
  entityName,
  entityColor,
  subscribed = true,
  onSubscribeChange,
  updates,
  initialUpdateId,
  viewer,
  promptMode = false,
  emptyTitle,
  emptyBody,
  onOpenEntity,
  onCreateUpdate,
}: BaseUpdateInboxViewProps) {
  const [composing, setComposing] = useState(promptMode || updates.length === 0)
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>('onTrack')
  const [saving, setSaving] = useState(false)
  const [localSubscribed, setLocalSubscribed] = useState(subscribed)

  const ordered = useMemo(() => {
    const list = [...updates].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    if (!initialUpdateId) return list
    const match = list.find(item => item.id === initialUpdateId)
    if (!match) return list
    return [match, ...list.filter(item => item.id !== initialUpdateId)]
  }, [initialUpdateId, updates])

  const submit = async () => {
    if (!body.trim() || !onCreateUpdate || saving) return
    setSaving(true)
    try {
      await onCreateUpdate({ body: body.trim(), health })
      setBody('')
      setComposing(false)
    } finally {
      setSaving(false)
    }
  }

  const toggleSubscribe = () => {
    const next = !localSubscribed
    setLocalSubscribed(next)
    onSubscribeChange?.(next)
  }

  return (
    <div className="flow-inbox-host flow-inbox-host--update" data-surface={surfaceId} data-entity={entityKind}>
      <header className="flow-inbox-host__title">
        <button className="flow-inbox-host__entity" onClick={onOpenEntity} type="button">
          <span aria-hidden className="flow-inbox-host__swatch" style={{ background: entityColor }} />
          <div>
            <small>{entityKind === 'project' ? 'Project updates' : 'Initiative updates'}</small>
            <strong data-i18n-ignore>{entityName}</strong>
          </div>
        </button>
        <div className="flow-inbox-host__actions">
          <button
            aria-pressed={localSubscribed}
            className="flow-inbox-host__ghost"
            onClick={toggleSubscribe}
            type="button"
          >
            {localSubscribed ? <Check size={13} /> : null}
            {localSubscribed ? 'Subscribed' : 'Subscribe'}
          </button>
          {onCreateUpdate ? (
            <button className="flow-inbox-host__primary" onClick={() => setComposing(true)} type="button">
              <PenSquare size={13} />
              New update
            </button>
          ) : null}
        </div>
      </header>

      {composing && onCreateUpdate ? (
        <section className="flow-inbox-host__composer" aria-label="Write update">
          <header>
            <strong>{promptMode ? 'Post an update' : 'New update'}</strong>
            <div className="flow-inbox-host__healths" role="group" aria-label="Update health">
              {HEALTH_OPTIONS.map(option => (
                <button
                  className={health === option.id ? 'is-active' : undefined}
                  key={option.id}
                  onClick={() => setHealth(option.id)}
                  type="button"
                >
                  <i className={`flow-inbox-host__health-dot is-${option.id}`} />
                  {option.label}
                </button>
              ))}
            </div>
          </header>
          <textarea
            aria-label={entityKind === 'project' ? 'Project update' : 'Initiative update'}
            autoFocus
            onChange={event => setBody(event.target.value)}
            onKeyDown={event => {
              if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                event.preventDefault()
                void submit()
              }
            }}
            placeholder={
              entityKind === 'project'
                ? 'Share progress, risks, and what’s next…'
                : 'Share initiative progress and decisions…'
            }
            value={body}
          />
          <footer>
            <span data-i18n-ignore>{viewer.displayName || viewer.name}</span>
            <div>
              <button onClick={() => setComposing(false)} type="button">
                Cancel
              </button>
              <button
                className="flow-inbox-host__primary"
                disabled={!body.trim() || saving}
                onClick={() => void submit()}
                type="button"
              >
                {saving ? 'Posting…' : 'Post update'}
              </button>
            </div>
          </footer>
        </section>
      ) : null}

      <div className="flow-inbox-host__stream">
        {ordered.length === 0 && !composing ? (
          <div className="flow-inbox-host__empty">
            <strong>{emptyTitle ?? (entityKind === 'project' ? 'No project updates yet' : 'No initiative updates yet')}</strong>
            <p>
              {emptyBody ??
                (entityKind === 'project'
                  ? 'Write a short status report to keep subscribers up to date on progress and health.'
                  : 'Post an update so followers can see initiative health and decisions.')}
            </p>
            {onCreateUpdate ? (
              <button className="flow-inbox-host__primary" onClick={() => setComposing(true)} type="button">
                New update
              </button>
            ) : null}
          </div>
        ) : null}
        {ordered.map(update => (
          <article
            className={`flow-inbox-host__update${initialUpdateId === update.id ? ' is-focus' : ''}`}
            key={update.id}
          >
            <header>
              <span className={`flow-inbox-host__health-pill is-${update.health}`}>
                <i />
                {healthLabel(update.health)}
              </span>
              <strong data-i18n-ignore>{update.user.displayName || update.user.name}</strong>
              <time dateTime={update.createdAt}>{formatRelative(update.createdAt)}</time>
              {update.editedAt ? <span className="flow-inbox-host__muted">Edited</span> : null}
            </header>
            <p>{update.body}</p>
            {typeof update.commentCount === 'number' && update.commentCount > 0 ? (
              <footer>{update.commentCount} comment{update.commentCount === 1 ? '' : 's'}</footer>
            ) : null}
          </article>
        ))}
      </div>

      <footer className="flow-inbox-host__footer">
        <button onClick={onOpenEntity} type="button">
          Open {entityKind}
        </button>
      </footer>
    </div>
  )
}

function formatRelative(value: string) {
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime())
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (elapsed < hour) return `${Math.max(1, Math.floor(elapsed / minute))}m`
  if (elapsed < day) return `${Math.floor(elapsed / hour)}h`
  if (elapsed < 30 * day) return `${Math.floor(elapsed / day)}d`
  return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(new Date(value))
}
