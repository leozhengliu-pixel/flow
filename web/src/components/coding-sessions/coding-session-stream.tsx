import { useEffect, useState, type ReactNode } from 'react'
import { AlertTriangle, ArrowDown, ArrowUpRight, Link2 } from 'lucide-react'
import { fetchGitHubApp, startGitHubAppAuthorization } from '@/lib/coding-sessions-api'
import type { ApplicationActivity } from '@/lib/application-agents'
import { AppLink } from '@/components/ui/app-link'
import { GridLoader } from '@/components/ui/grid-loader'
import { useI18n } from '@/i18n/i18n'
import { integrationSettingsPath, membersPath, newSigningKeyPath } from '@/lib/app-routes'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { codingErrorInfo, isCodingErrorCode } from './coding-session-model'
import { errorVariant, streamTimeLabel, workedForLabel, type GroupChild, type StreamAction, type StreamItem } from './coding-session-stream-model'
import './coding-session-stream.css'

/** The coding sessions guide (the settings page links the same page). */
const CODING_SESSIONS_DOCS = 'https://flow.app/docs/coding-sessions'

/** The agent's live label: the accent sweep over the muted text (static under reduced motion). */
export function CodingShimmer({ children, active = true, className }: { children: ReactNode; active?: boolean; className?: string }) {
  return <span className={`cs-shimmer${className ? ` ${className}` : ''}`} data-active={active || undefined}>
    {children}
    {active && <span className="cs-shimmer-sweep" aria-hidden="true">{children}</span>}
  </span>
}

/** A one-line label that slides up (with a short blur) whenever its text changes. */
export function SlideLabel({ text, children }: { text: string; children: ReactNode }) {
  const [shown, setShown] = useState({ text, node: children })
  const [leaving, setLeaving] = useState<{ text: string; node: ReactNode }>()
  useEffect(() => {
    if (text === shown.text) { setShown(current => ({ ...current, node: children })); return }
    setLeaving(shown)
    setShown({ text, node: children })
    const timer = setTimeout(() => setLeaving(undefined), 150)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- swap on text changes only; the node follows each render
  }, [text, children])
  return <span className="cs-slide-label">
    {leaving && <span className="cs-slide-label-item is-leaving" aria-hidden="true" key={`out-${leaving.text}`}>{leaving.node}</span>}
    <span className={`cs-slide-label-item${leaving ? ' is-entering' : ''}`} key={`in-${shown.text}`}>{shown.node}</span>
  </span>
}

/** Backticked spans of an action label or detail, shown as plain monospace (no chip). */
function InlineCode({ text }: { text: string }) {
  const parts = text.split('`')
  return <>{parts.map((part, index) => index % 2 ? <code key={index}>{part}</code> : part)}</>
}

const plainText = (text: string) => text.replace(/`/g, '')

/** Disclosure state kept per activity / group id for the page's lifetime (re-renders and refetches keep it). */
const openState = new Map<string, boolean>()
function useRememberedOpen(id: string, initial = false): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(() => openState.get(id) ?? initial)
  return [open, value => { openState.set(id, value); setOpen(value) }]
}

/** The 16px disclosure triangle (right when closed, rotated down when open). */
function DisclosureTriangle({ open }: { open: boolean }) {
  return <svg className="cs-triangle" data-open={open || undefined} aria-hidden="true" viewBox="0 0 16 16" width="16" height="16"><path d="M6.5 5.2v5.6a.4.4 0 0 0 .65.3l3.5-2.8a.4.4 0 0 0 0-.6l-3.5-2.8a.4.4 0 0 0-.65.3Z" fill="currentColor"/></svg>
}

const actionLabel = (activity: ApplicationActivity) => activity.metadata?.action || activity.body

/** One tool call: label and truncated detail (full text in the tooltip); a result makes it a disclosure. */
export function CodingActionRow({ action }: { action: StreamAction }) {
  const { activity, loading } = action
  const label = actionLabel(activity)
  const detail = activity.metadata?.parameter
  const result = activity.metadata?.result
  const [open, setOpen] = useRememberedOpen(`action:${activity.id}`)
  const content = <>
    <span className="cs-action-label" data-i18n-ignore>{loading ? <CodingShimmer><InlineCode text={label}/></CodingShimmer> : <InlineCode text={label}/>}</span>
    {detail && <span className="cs-action-detail" title={plainText(detail)} data-i18n-ignore><InlineCode text={detail}/></span>}
  </>
  if (!result) return <div className="cs-action" data-loading={loading || undefined}><div className="cs-action-header">{content}</div></div>
  return <div className="cs-action" data-loading={loading || undefined}>
    <div role="button" tabIndex={0} className="cs-action-header is-disclosure" aria-expanded={open} onClick={() => setOpen(!open)} onKeyDown={event => {
      if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setOpen(!open) }
    }}>{content}<DisclosureTriangle open={open}/></div>
    {open && <div className="cs-action-result"><pre data-i18n-ignore>{result}</pre></div>}
  </div>
}

/** A thought: 12px muted text beside a 2px rail. */
export function CodingReasoning({ activity }: { activity: ApplicationActivity }) {
  return <div className="cs-reasoning"><p data-i18n-ignore>{activity.body}</p></div>
}

function GroupChildView({ child }: { child: GroupChild }) {
  const { t } = useI18n()
  const [open, setOpen] = useRememberedOpen(child.kind === 'tools' ? child.id : '')
  if (child.kind === 'reasoning') return <CodingReasoning activity={child.activity}/>
  if (child.kind === 'action') return <CodingActionRow action={child}/>
  return <div className="cs-tools">
    <button type="button" className="cs-group-header is-sub" aria-expanded={open} onClick={() => setOpen(!open)}>
      <span>{t('Used {count} tools').replace('{count}', String(child.actions.length))}</span><DisclosureTriangle open={open}/>
    </button>
    {open && <div className="cs-group-children">{child.actions.map(action => <CodingActionRow key={action.activity.id} action={action}/>)}</div>}
  </div>
}

/** Adjacent thoughts and actions: the live step (shimmering) while running, "Worked for …" once done; folded by default. */
export function CodingActivityGroup({ group }: { group: Extract<StreamItem, { kind: 'group' }> }) {
  const { t } = useI18n()
  const [open, setOpen] = useRememberedOpen(group.id)
  const live = group.live
  const label = group.loading ? (live ? actionLabel(live) : t('Working…')) : workedForLabel(group.startedAt, group.endedAt, t)
  const detail = group.loading ? live?.metadata?.parameter : undefined
  return <div className="cs-group" data-loading={group.loading || undefined}>
    <button type="button" className="cs-group-header" aria-expanded={open} onClick={() => setOpen(!open)}>
      <SlideLabel text={`${label}\u0000${detail ?? ''}`}>
        <span className="cs-group-label" data-i18n-ignore={live ? true : undefined}>{group.loading ? <CodingShimmer><InlineCode text={label}/></CodingShimmer> : label}</span>
        {detail && <span className="cs-action-detail" title={plainText(detail)} data-i18n-ignore><InlineCode text={detail}/></span>}
      </SlideLabel>
      <DisclosureTriangle open={open}/>
    </button>
    {open && <div className="cs-group-children">{group.children.map(child => <GroupChildView key={child.kind === 'tools' ? child.id : child.activity.id} child={child}/>)}</div>}
    {!open && group.promoted && <div className="cs-reasoning is-promoted"><p data-i18n-ignore>{group.promoted}</p></div>}
  </div>
}

/** The trailing "Thinking…" while the agent has not shown a step yet. */
export function CodingThinking() {
  const { t } = useI18n()
  return <div className="cs-action" data-loading role="status"><div className="cs-action-header"><span className="cs-action-label"><CodingShimmer>{t('Thinking…')}</CodingShimmer></span></div></div>
}

export function CodingTimeMarker({ at }: { at: string }) {
  const { t, locale } = useI18n()
  const label = streamTimeLabel(at, locale, t)
  return label ? <time className="cs-time-marker" dateTime={at}>{label}</time> : null
}

/** "Session restarted" / "Session archived": a faint label between two hairlines. */
export function CodingSeparator({ label }: { label: string }) {
  return <div className="cs-separator" role="separator"><span>{label}</span></div>
}

/** The pill of an access error: icon, purple label and an optional action. */
function AccessPill({ label, children }: { label: string; children?: ReactNode }) {
  return <div className="cs-access-pill"><GridLoader variant="read" size={14} className="cs-access-pill-icon"/><span>{label}</span>{children && <span className="cs-access-pill-actions">{children}</span>}</div>
}

/**
 * An error in the stream. Signing key and code host access errors explain what is missing with a purple pill and the one
 * action that fixes it; every other error is the red-striped card, whose "Retry" only the last error of a failed session offers.
 */
export function CodingErrorCard({ activity, session, data, onRetry, busy, retry }: { activity?: ApplicationActivity; session: CodingSession; data: BootstrapData; onRetry: () => void; busy: boolean; /** Offer Retry (the last error of a failed session). */ retry: boolean }) {
  const { t } = useI18n()
  const metadataCode = activity?.metadata?.errorCode
  const code = isCodingErrorCode(metadataCode) ? metadataCode : activity ? undefined : session.errorCode
  const host = code?.toLowerCase().includes('gitlab') || session.codeHost === 'gitlab' ? 'gitlab' : 'github'
  const hostName = host === 'gitlab' ? 'GitLab' : 'GitHub'
  const workspace = data.workspace.urlKey
  const admin = data.viewerRole === 'admin' || data.viewerRole === 'owner'
  const creator = data.users.find(user => user.id === session.creatorId)
  const variant = errorVariant(code, session.creatorId === data.viewer.id)
  const [linked, setLinked] = useState(false)
  useEffect(() => {
    if (variant !== 'connect' || host !== 'github') return
    let live = true
    fetchGitHubApp().then(app => { if (live) setLinked(Boolean(app.linkedAccount)) }).catch(() => undefined)
    return () => { live = false }
  }, [variant, host])
  const connect = async () => {
    if (host === 'gitlab') return
    try {
      const { url } = await startGitHubAppAuthorization()
      if (/^https?:\/\//i.test(url)) window.open(url, '_blank', 'noopener')
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : t('Could not connect')) }
  }
  if (variant === 'signingKey') return <div className="cs-access" role="alert" data-code={code}>
    <p>{t('Your workspace requires signed commits for coding sessions. Add a signing key to continue.')}</p>
    <AccessPill label={t('Signing key needed to proceed')}><AppLink className="cs-button is-primary is-small" href={newSigningKeyPath(workspace)}>{t('Add signing key')}</AppLink></AccessPill>
  </div>
  if (variant === 'codeAccess') {
    const firstAdmin = data.members?.find(member => (member.role === 'admin' || member.role === 'owner') && member.status === 'active')?.user
    return <div className="cs-access" role="alert" data-code={code}>
      <p>{t('Flow doesn’t have {host} code access yet. Enable code access for your workspace to allow Flow to make code changes.').replace('{host}', hostName)}</p>
      {admin
        ? <AccessPill label={t('{host} code access needed').replace('{host}', hostName)}><AppLink className="cs-button is-small" href={integrationSettingsPath(workspace, host)}>{t('Enable')}<ArrowUpRight size={14} aria-hidden="true"/></AppLink></AccessPill>
        : <AccessPill label={t('Request Flow code access from {name}').replace('{name}', firstAdmin?.displayName || firstAdmin?.name || t('an admin'))}><AppLink className="cs-button is-small" href={membersPath(workspace)}>{t('View admins')}</AppLink></AccessPill>}
    </div>
  }
  if (variant === 'connect' || variant === 'awaiting') return <div className="cs-access" role="alert" data-code={code}>
    <p>{t('Flow needs to confirm you’re authorized to make code changes on {host}. Confirm your {host} access to proceed.').replaceAll('{host}', hostName)} <a className="cs-inline-link" href={CODING_SESSIONS_DOCS} target="_blank" rel="noreferrer">{t('Docs')}</a></p>
    {variant === 'awaiting'
      ? <AccessPill label={t('Awaiting confirmation of code access from {name}').replace('{name}', creator?.displayName || creator?.name || t('the session creator'))}/>
      : linked
        ? <AccessPill label={t('Code access confirmed')}><button type="button" className="cs-button is-primary is-small" disabled={busy} onClick={onRetry}>{t('Resume')}</button></AccessPill>
        : <AccessPill label={t('Connect {host}').replace('{host}', hostName)}>{host === 'github'
          ? <button type="button" className="cs-button is-small" onClick={() => void connect()}>{t('Connect')}<ArrowUpRight size={14} aria-hidden="true"/></button>
          : <AppLink className="cs-button is-small" href={integrationSettingsPath(workspace, 'gitlab')}>{t('Connect')}<ArrowUpRight size={14} aria-hidden="true"/></AppLink>}</AccessPill>}
  </div>
  const info = code ? codingErrorInfo(code, { workspace, codeHost: session.codeHost }) : undefined
  const raw = activity ? activity.body.replace(/\s*\([a-zA-Z]+\)\s*$/, '') : session.error
  const message = info && code !== 'internal' ? t(info.message) : raw || t('Something went wrong while running this session.')
  return <div className="cs-error-card" role="alert" data-code={code}>
    <div className="cs-error-body">
      <div className="cs-error-title"><AlertTriangle size={14} aria-hidden="true"/><span>{t('Error')}</span></div>
      <p data-i18n-ignore={message === raw || undefined}>{message}</p>
      {info?.adminDetail && admin && raw && raw !== message && <p className="cs-error-detail" data-i18n-ignore>{raw}</p>}
    </div>
    {retry && <button type="button" className="cs-button is-medium" disabled={busy} onClick={onRetry}>{t('Retry')}</button>}
  </div>
}

/** A session that never got an answer from its agent ("Flow failed to start."). */
export function CodingStaleCard({ onRetry, busy }: { onRetry: () => void; busy: boolean }) {
  const { t } = useI18n()
  return <div className="cs-error-card is-muted" role="alert">
    <div className="cs-error-body"><p>{t('Flow failed to start.')}</p></div>
    <button type="button" className="cs-button is-primary is-medium" disabled={busy} onClick={onRetry}>{t('Retry')}</button>
  </div>
}

/** "<Agent> requested <user> to connect their GitHub account." under an auth question. */
export function CodingAuthLine({ activity, data }: { activity: ApplicationActivity; data: BootstrapData }) {
  const { t } = useI18n()
  const user = data.users.find(candidate => candidate.id === (activity.signalMetadata?.userId || data.viewer.id))
  const provider = activity.signalMetadata?.providerName || 'GitHub'
  return <p className="cs-auth-line"><Link2 size={13} aria-hidden="true"/>{t('Flow requested {name} to connect their {provider} account.').replace('{name}', user?.displayName || user?.name || t('you')).replace('{provider}', provider)}</p>
}

/** The floating "Latest" button; its label shimmers while the agent works. */
export function CodingLatestButton({ working, onClick }: { working: boolean; onClick: () => void }) {
  const { t } = useI18n()
  return <div className="cs-latest">
    <button type="button" className="cs-latest-button" onClick={onClick}>
      <ArrowDown size={16} aria-hidden="true"/>
      <span className={working ? 'cs-legacy-shimmer' : undefined}>{t('Latest')}</span>
    </button>
  </div>
}
