import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import * as Popover from '@radix-ui/react-popover'
import * as Dialog from '@radix-ui/react-dialog'
import { ChevronDown, ChevronUp, ThumbsDown, X } from 'lucide-react'
import { toast } from 'sonner'
import {
  acceptIssueSuggestion,
  createRelation,
  dismissIssueSuggestion,
  fetchIssueRecord,
  fetchIssueSuggestions,
  refreshIssueSuggestions,
} from '@/lib/api'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { useI18n } from '@/i18n/i18n'
import { UserAvatar } from '@/components/ui/user-avatar'
import { NoAssigneeIcon, PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { isIssueInTriage } from '@/components/triage/triage-model'
import type { BootstrapData, Issue, IssueRelationType, IssueSuggestion } from '@/types/flow'
import './triage-intelligence-suggestions.css'
import { useIssuesById } from './use-issues-by-id'

type PropertySuggestion = IssueSuggestion & {
  type: 'assignee' | 'project' | 'label' | 'team'
}

type RemoteSuggestions = { suggestions: IssueSuggestion[]; generatedAt?: string; thinking?: string; pending: boolean }

/** While suggestions are still being generated (model runs take 5–60 s), poll the list. */
const POLL_MS = 3000
const POLL_LIMIT = 60

const ENTITY_NOUN: Record<PropertySuggestion['type'], string> = { assignee: 'user', project: 'project', label: 'label', team: 'team' }

/** Linear shows a relation list longer than this as the first `COLLAPSED_ROWS` rows plus a "Show N more" toggle. */
const COLLAPSE_AT = 6
const COLLAPSED_ROWS = 4

/**
 * Linear's Triage Intelligence card: shown under the issue title for issues in Triage (and any issue that still has
 * active suggestions). A white card (sparkle + "Triage Intelligence", property chips, "Duplicate of" / "Related to"
 * rows with Apply and a hover card explaining why) sits in a hairline-bordered frame; while suggestions are generated,
 * failed, or empty the frame's footer shows the progress line + timer, or the error / "No suggestions found" with Run again.
 */
export function TriageIntelligenceSuggestions({
  issue,
  data,
  onIssueUpdated,
  maxSuggestions,
  isVisibleInTriageAccept,
  inTriage: inTriageOverride,
  variant = 'card',
}: {
  issue: Issue
  data: BootstrapData
  onIssueUpdated?: (issue: Issue) => void
  /** Cap property suggestion chips (Fast accept uses 3). */
  maxSuggestions?: number
  /** When true, marks the panel as visible in triage accept host. */
  isVisibleInTriageAccept?: boolean
  /** Host already knows the issue is in Triage (e.g. the Triage view); defaults to the team triage check. */
  inTriage?: boolean
  /** `compact` is the tighter card used inside the Fast accept editor. */
  variant?: 'card' | 'compact'
}) {
  const { t } = useI18n()
  const [removed, setRemoved] = useState<Set<string>>(() => new Set())
  const [remote, setRemote] = useState<RemoteSuggestions>()
  const [busy, setBusy] = useState<string>()
  const [refreshing, setRefreshing] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  const [thinkingOpen, setThinkingOpen] = useState(false)
  const [failed, setFailed] = useState(false)
  const enabled = data.workspaceSettings.featureFlags?.['triage-intelligence'] ?? false
  const inTriage = inTriageOverride ?? isIssueInTriage(issue, data.teamSettings)
  const hasLocal = (data.issueSuggestions ?? []).some(item => item.issueId === issue.id && item.state === 'active')
  const shouldFetch = enabled && (inTriage || hasLocal || Boolean(issue.suggestionsGeneratedAt))

  useEffect(() => {
    if (!shouldFetch) {
      setRemote(undefined)
      return
    }
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let attempts = 0
    setFailed(false)
    const load = () => {
      void fetchIssueSuggestions(issue.id, controller.signal)
        .then(result => {
          if (controller.signal.aborted) return
          const next = normalizeRemote(result)
          setRemote(next)
          if (inTriage && next.pending && attempts++ < POLL_LIMIT) timer = setTimeout(load, POLL_MS)
        })
        .catch(() => {
          if (controller.signal.aborted) return
          setRemote(undefined)
          setFailed(true)
        })
    }
    load()
    return () => {
      controller.abort()
      if (timer) clearTimeout(timer)
    }
  }, [shouldFetch, inTriage, issue.id, issue.suggestionsGeneratedAt, reloadKey])

  const suggestions = useMemo(
    () =>
      (remote?.suggestions ?? data.issueSuggestions ?? [])
        .filter(item => item.issueId === issue.id && item.state === 'active' && !removed.has(item.id))
        .sort((left, right) => Number(left.metadata?.rank ?? 0) - Number(right.metadata?.rank ?? 0)),
    [data.issueSuggestions, issue.id, remote, removed],
  )
  const propertySuggestions = suggestions.filter(
    (item): item is PropertySuggestion =>
      item.type === 'assignee' || item.type === 'project' || item.type === 'label' || item.type === 'team',
  )
  const visiblePropertySuggestions =
    typeof maxSuggestions === 'number' ? propertySuggestions.slice(0, maxSuggestions) : propertySuggestions
  const duplicates = suggestions.filter(item => item.type === 'similarIssue')
  const related = suggestions.filter(item => item.type === 'relatedIssue')
  const relatedIssues = useSuggestedIssues([...duplicates, ...related], data)

  const generatedAt = remote?.generatedAt ?? issue.suggestionsGeneratedAt
  // A run is pending while the issue was never generated, or the server reports a background run (first generation,
  // replacing heuristic suggestions, or regenerating after an edit), or "Run again" is in flight.
  const pending = !failed && (refreshing || Boolean(remote?.pending) || (!generatedAt && !suggestions.length))
  const elapsed = usePendingSeconds(enabled && inTriage && pending)

  if (!enabled || (!inTriage && !suggestions.length)) return null
  const thinking = remote?.thinking ?? suggestions.map(item => readThinking(item.metadata?.thinking)).find(Boolean)

  const update = async (suggestion: IssueSuggestion, accept: boolean) => {
    setBusy(suggestion.id)
    try {
      if (accept) await acceptIssueSuggestion(issue.id, suggestion.id)
      else await dismissIssueSuggestion(issue.id, suggestion.id)
      if (accept) {
        const refreshed = await fetchIssueRecord(issue.id, undefined, data.workspace.urlKey)
        onIssueUpdated?.(refreshed)
      }
      setRemoved(current => new Set(current).add(suggestion.id))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Could not update suggestion')
    } finally {
      setBusy(undefined)
    }
  }

  /** "Mark current issue as" another relation than the suggested one: link it, then retire the suggestion. */
  const relate = async (suggestion: IssueSuggestion, type: IssueRelationType) => {
    if (!suggestion.suggestedIssueId) return
    setBusy(suggestion.id)
    try {
      await createRelation(issue.id, type, suggestion.suggestedIssueId)
      await dismissIssueSuggestion(issue.id, suggestion.id).catch(() => undefined)
      onIssueUpdated?.(await fetchIssueRecord(issue.id, undefined, data.workspace.urlKey))
      setRemoved(current => new Set(current).add(suggestion.id))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Could not update suggestion')
    } finally {
      setBusy(undefined)
    }
  }

  const dismissAll = async () => {
    const ids = suggestions.map(item => item.id)
    setBusy('all')
    const results = await Promise.allSettled(ids.map(id => dismissIssueSuggestion(issue.id, id)))
    setRemoved(current => {
      const next = new Set(current)
      results.forEach((result, index) => { if (result.status === 'fulfilled') next.add(ids[index]) })
      return next
    })
    if (results.some(result => result.status === 'rejected')) toast.error('Could not dismiss all suggestions')
    setBusy(undefined)
  }

  const runAgain = async () => {
    setRefreshing(true)
    setFailed(false)
    try {
      await refreshIssueSuggestions(issue.id)
      const result = normalizeRemote(await fetchIssueSuggestions(issue.id))
      setRemoved(new Set())
      setRemote(result)
      // An asynchronous (agentic) run is still pending: keep polling until it lands.
      if (result.pending) setReloadKey(key => key + 1)
    } catch (error) {
      setFailed(true)
      toast.error(error instanceof Error ? error.message : 'Could not refresh suggestions')
    } finally {
      setRefreshing(false)
    }
  }

  const empty = !pending && !failed && !suggestions.length
  const footer = pending ? 'pending' : failed && !suggestions.length ? 'failed' : empty ? 'empty' : undefined
  const reasonsSummary = suggestions.flatMap(item => (item.metadata?.reasons ?? []).filter(reason => typeof reason === 'string'))

  const relationProps = {
    issues: relatedIssues,
    data,
    busy,
    onAccept: (suggestion: IssueSuggestion) => void update(suggestion, true),
    onDismiss: (suggestion: IssueSuggestion) => void update(suggestion, false),
    onRelate: (suggestion: IssueSuggestion, type: IssueRelationType) => void relate(suggestion, type),
  }

  return (
    <section
      className={`triage-intelligence-panel${variant === 'compact' ? ' is-compact' : ''}${footer ? ' has-footer' : ''}`}
      aria-label="Triage Intelligence"
      aria-busy={pending || undefined}
      data-state={pending ? 'pending' : failed && !suggestions.length ? 'failed' : empty ? 'empty' : 'ready'}
      data-triage-accept={isVisibleInTriageAccept || undefined}
    >
      <div className="triage-intelligence-card">
        <header>
          <span className="triage-intelligence-title">
            <LinearGlyph name="aiBurst" size={16} />
            <span className={pending ? 'triage-intelligence-shimmer' : undefined}>Triage Intelligence</span>
          </span>
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
              <button className="triage-intelligence-menu-trigger" type="button" aria-label={t('Triage Intelligence options')}>
                <LinearGlyph name="ellipsis" size={14} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="triage-intelligence-menu">
              <DropdownMenuItem onSelect={() => setThinkingOpen(true)}>{t('Show thinking…')}</DropdownMenuItem>
              <DropdownMenuItem disabled={pending} onSelect={() => void runAgain()}>{t('Run again')}</DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem disabled={!suggestions.length || busy === 'all'} onSelect={() => void dismissAll()}>{t('Dismiss all suggestions')}</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </header>
        {visiblePropertySuggestions.length > 0 && (
          <div className="triage-intelligence-row">
            <span className="triage-intelligence-row-label">Suggestions</span>
            <div className="triage-intelligence-chips">
              {visiblePropertySuggestions.map(suggestion => (
                <PropertySuggestionChip
                  key={suggestion.id}
                  suggestion={suggestion}
                  data={data}
                  busy={busy === suggestion.id || busy === 'all'}
                  onAccept={() => void update(suggestion, true)}
                  onDismiss={() => void update(suggestion, false)}
                />
              ))}
            </div>
          </div>
        )}
        {duplicates.length > 0 && <RelationRow label="Duplicate of" suggestions={duplicates} {...relationProps} />}
        {related.length > 0 && <RelationRow label="Related to" suggestions={related} {...relationProps} />}
      </div>
      {footer && (
        <footer className="triage-intelligence-footer" data-kind={footer}>
          <span className="triage-intelligence-footer-text" role={pending ? 'status' : undefined}>
            {t(pending ? 'Looking at the issue…' : footer === 'failed' ? 'Error while trying to find suggestions' : 'No suggestions found')}
          </span>
          {pending ? (
            <span className="triage-intelligence-elapsed" aria-hidden="true">{formatElapsed(elapsed)}</span>
          ) : (
            <FlowTooltip label={t('Find suggestions')}>
              <button type="button" className="triage-intelligence-run-again" disabled={refreshing} onClick={() => void runAgain()}>
                <LinearGlyph name="rerun" size={14} />
                {t('Run again')}
              </button>
            </FlowTooltip>
          )}
        </footer>
      )}
      <Dialog.Root open={thinkingOpen} onOpenChange={setThinkingOpen}>
        <Dialog.Portal>
          <Dialog.Overlay data-flow-motion="backdrop" className="triage-intelligence-thinking-overlay" />
          <Dialog.Content data-flow-motion="dialog" className="triage-intelligence-thinking-dialog" aria-describedby={undefined}>
            <header>
              <Dialog.Title>
                <LinearGlyph name="aiBurst" size={14} />
                Triage Intelligence thinking
              </Dialog.Title>
              <Dialog.Close asChild>
                <button type="button" className="triage-intelligence-menu-trigger" aria-label="Close">
                  <X size={14} />
                </button>
              </Dialog.Close>
            </header>
            {thinking ? (
              <p className="triage-intelligence-thinking-text" data-i18n-ignore>{thinking}</p>
            ) : reasonsSummary.length ? (
              <ul className="triage-intelligence-thinking-text" data-i18n-ignore>
                {reasonsSummary.map((reason, index) => <li key={`${index}-${reason}`}>{reason}</li>)}
              </ul>
            ) : (
              <p className="triage-intelligence-thinking-text is-empty">No reasoning is available for this run.</p>
            )}
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </section>
  )
}

function RelationRow({
  label,
  suggestions,
  issues,
  data,
  busy,
  onAccept,
  onDismiss,
  onRelate,
}: {
  label: string
  suggestions: IssueSuggestion[]
  issues: Map<string, Issue>
  data: BootstrapData
  busy: string | undefined
  onAccept: (suggestion: IssueSuggestion) => void
  onDismiss: (suggestion: IssueSuggestion) => void
  onRelate: (suggestion: IssueSuggestion, type: IssueRelationType) => void
}) {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)
  const duplicate = label === 'Duplicate of'
  const kind = duplicate ? 'duplicate' : 'related issue'
  const rows = suggestions.filter(suggestion => suggestion.suggestedIssueId && issues.get(suggestion.suggestedIssueId))
  const collapsible = rows.length >= COLLAPSE_AT
  const hidden = collapsible && !expanded ? rows.length - COLLAPSED_ROWS : 0
  const visible = collapsible && !expanded ? rows.slice(0, COLLAPSED_ROWS) : rows
  return (
    <div className="triage-intelligence-row">
      <span className="triage-intelligence-row-label triage-intelligence-relation-label">{t(label)}</span>
      <div className="triage-intelligence-related-list">
        {visible.map(suggestion => {
          const relatedIssue = issues.get(suggestion.suggestedIssueId as string) as Issue
          const disabled = busy === suggestion.id || busy === 'all'
          const url = `${typeof location === 'undefined' ? '' : location.origin}/${data.workspace.urlKey}/issue/${relatedIssue.identifier}`
          const copy = (text: string, message: string) => void navigator.clipboard.writeText(text).then(() => toast.success(t(message))).catch(() => toast.error(t('Could not write to clipboard')))
          return (
            <div className="triage-intelligence-related-row" key={suggestion.id} data-source={sourceOf(suggestion)}>
              <RelatedIssueHoverCard suggestion={suggestion} issue={relatedIssue} data={data} duplicate={duplicate} disabled={disabled} onAccept={() => onAccept(suggestion)} onDismiss={() => onDismiss(suggestion)} />
              <span className="triage-intelligence-row-actions">
                <FlowTooltip label={t(duplicate ? 'Mark as duplicate' : 'Mark as related')}>
                  <button
                    type="button"
                    className="triage-intelligence-small-button"
                    aria-label={`Apply ${kind} ${relatedIssue.identifier}`}
                    disabled={disabled}
                    onClick={() => onAccept(suggestion)}
                  >
                    {t('Apply')}
                  </button>
                </FlowTooltip>
                <DropdownMenu modal={false}>
                  <DropdownMenuTrigger asChild>
                    <button type="button" className="triage-intelligence-icon-button" aria-label={t('Suggestion options')} disabled={disabled}>
                      <LinearGlyph name="ellipsis" size={14} />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="triage-intelligence-menu">
                    <DropdownMenuLabel>{t('Mark current issue as')}</DropdownMenuLabel>
                    <DropdownMenuItem onSelect={() => onRelate(suggestion, 'related')}>{t('Related to')}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => onRelate(suggestion, 'duplicate')}>{t('Duplicate of')}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => onRelate(suggestion, 'blocked_by')}>{t('Blocked by')}</DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>{t('Copy')}</DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="triage-intelligence-menu">
                        <DropdownMenuItem onSelect={() => copy(relatedIssue.identifier, 'Issue ID copied to clipboard')}>{t('Copy issue ID')}</DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => copy(url, 'Issue URL copied to clipboard')}>{t('Copy issue URL')}</DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => copy(relatedIssue.title, 'Issue title copied to clipboard')}>{t('Copy issue title')}</DropdownMenuItem>
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem aria-label={`Dismiss ${kind} ${relatedIssue.identifier}`} onSelect={() => onDismiss(suggestion)}>{t('Dismiss suggestion')}</DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </span>
            </div>
          )
        })}
        {collapsible && (
          <button type="button" className="triage-intelligence-show-more" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>
            {expanded ? <ChevronUp size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
            {expanded ? t('Show less') : t('Show {count} more').replace('{count}', String(hidden))}
          </button>
        )}
      </div>
    </div>
  )
}

function createdAgo(value: string, t: (text: string) => string) {
  const days = Math.floor(Math.max(0, Date.now() - Date.parse(value)) / 86_400_000)
  if (days < 1) return t('Created today')
  // Linear counts days up to a month ("Created 11d ago"), then months.
  if (days < 30) return t('Created {count}d ago').replace('{count}', String(days))
  if (days < 365) return t('Created {count}mo ago').replace('{count}', String(Math.floor(days / 30)))
  return t('Created {count}y ago').replace('{count}', String(Math.floor(days / 365)))
}

/** Linear's 321px hover card for a suggested duplicate / related issue: when, what, why, and accept / dismiss. */
function RelatedIssueHoverCard({ suggestion, issue, data, duplicate, disabled, onAccept, onDismiss }: { suggestion: IssueSuggestion; issue: Issue; data: BootstrapData; duplicate: boolean; disabled: boolean; onAccept: () => void; onDismiss: () => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const schedule = (next: boolean) => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setOpen(next), next ? HOVER_OPEN_MS : HOVER_CLOSE_MS)
  }
  const cancel = () => clearTimeout(timer.current)
  const reasons = (suggestion.metadata?.reasons ?? []).filter(reason => typeof reason === 'string')
  const why = t(duplicate ? 'Why this looks like a duplicate' : 'Why this looks related')
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Anchor asChild>
        <a
          className="triage-intelligence-issue-chip"
          href={`/${data.workspace.urlKey}/issue/${issue.identifier}`}
          data-i18n-ignore
          onPointerEnter={event => { if (event.pointerType !== 'touch') schedule(true) }}
          onPointerLeave={event => { if (event.pointerType !== 'touch') schedule(false) }}
          onFocus={() => schedule(true)}
          onBlur={() => schedule(false)}
        >
          <StatusIcon state={issue.state} size={14} />
          <span className="triage-intelligence-issue-id">{issue.identifier}</span>
          <span className="triage-intelligence-issue-title">{issue.title}</span>
        </a>
      </Popover.Anchor>
      <Popover.Portal>
        <Popover.Content
          data-flow-motion="floating"
          className="triage-intelligence-popover triage-intelligence-issue-card"
          side="bottom"
          align="start"
          sideOffset={6}
          collisionPadding={12}
          aria-label={why}
          onOpenAutoFocus={event => event.preventDefault()}
          onPointerEnter={cancel}
          onPointerLeave={event => { if (event.pointerType !== 'touch') schedule(false) }}
        >
          <div className="triage-intelligence-issue-card__head">
            <span className="triage-intelligence-issue-card__created">{createdAgo(issue.createdAt, t)}</span>
            <strong className="triage-intelligence-issue-card__title" data-i18n-ignore>{issue.title}</strong>
            <span className="triage-intelligence-issue-card__meta">
              <span><StatusIcon state={issue.state} size={14} /><span data-i18n-ignore>{issue.state.name}</span></span>
              <span>
                {issue.assignee ? <UserAvatar className="triage-intelligence-avatar" name={issue.assignee.displayName} avatarUrl={issue.assignee.avatarUrl} /> : <NoAssigneeIcon size={16} />}
                <span data-i18n-ignore={issue.assignee ? true : undefined}>{issue.assignee?.displayName ?? t('Unassigned')}</span>
              </span>
              <span><PriorityIcon priority={issue.priority} size={16} /><span>{t(issue.priorityLabel || 'No priority')}</span></span>
            </span>
          </div>
          <div className="triage-intelligence-popover-why">
            <h4>{why}</h4>
            {reasons.length > 0 ? (
              <ul data-i18n-ignore>{reasons.map((reason, index) => <li key={`${index}-${reason}`}>{reason}</li>)}</ul>
            ) : (
              <p>{t('The issues share similar titles and context.')}</p>
            )}
          </div>
          <div className="triage-intelligence-popover-actions">
            <button type="button" disabled={disabled} onClick={() => { setOpen(false); onAccept() }}>
              <LinearGlyph name="acceptSuggestion" size={14} />
              {t(duplicate ? 'Accept duplicate suggestion' : 'Accept related suggestion')}
            </button>
            <button type="button" aria-label={t('Dismiss suggestion')} disabled={disabled} onClick={() => { setOpen(false); onDismiss() }}>
              <ThumbsDown size={14} />
            </button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

const HOVER_OPEN_MS = 250
const HOVER_CLOSE_MS = 150

function PropertySuggestionChip({
  suggestion,
  data,
  busy,
  onAccept,
  onDismiss,
}: {
  suggestion: PropertySuggestion
  data: BootstrapData
  busy: boolean
  onAccept: () => void
  onDismiss: () => void
}) {
  const [open, setOpen] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const target = propertySuggestionTarget(suggestion, data)
  if (!target) return null
  const reasons = (suggestion.metadata?.reasons ?? []).filter(reason => typeof reason === 'string')
  const noun = ENTITY_NOUN[suggestion.type]
  const schedule = (next: boolean) => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setOpen(next), next ? HOVER_OPEN_MS : HOVER_CLOSE_MS)
  }
  const cancel = () => clearTimeout(timer.current)
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Anchor asChild>
        <button
          className={`triage-intelligence-chip triage-intelligence-chip-${suggestion.type}`}
          type="button"
          aria-label={`${target.label}: ${target.detail}`}
          aria-haspopup="dialog"
          aria-expanded={open}
          data-state={open ? 'open' : 'closed'}
          data-source={sourceOf(suggestion)}
          onPointerEnter={event => { if (event.pointerType !== 'touch') schedule(true) }}
          onPointerLeave={event => { if (event.pointerType !== 'touch') schedule(false) }}
          onClick={() => { cancel(); setOpen(true) }}
        >
          {target.icon}
          <span data-i18n-ignore>{target.chip}</span>
        </button>
      </Popover.Anchor>
      <Popover.Portal>
        <Popover.Content
          data-flow-motion="floating"
          className="triage-intelligence-popover"
          side="bottom"
          align="start"
          sideOffset={6}
          collisionPadding={12}
          aria-label={target.whyTitle}
          onOpenAutoFocus={event => event.preventDefault()}
          onPointerEnter={cancel}
          onPointerLeave={event => { if (event.pointerType !== 'touch') schedule(false) }}
        >
          <div className="triage-intelligence-popover-target">{target.header}</div>
          <div className="triage-intelligence-popover-why">
            <h4>{target.whyTitle}</h4>
            {reasons.length > 0 ? (
              <ul data-i18n-ignore>
                {reasons.map((reason, index) => (
                  <li key={`${index}-${reason}`}>{reason}</li>
                ))}
              </ul>
            ) : (
              <p>The workspace context points to this value.</p>
            )}
          </div>
          <div className="triage-intelligence-popover-actions">
            <button type="button" disabled={busy} onClick={() => { setOpen(false); onAccept() }}>
              <LinearGlyph name="acceptSuggestion" size={14} />
              {`Accept ${noun} suggestion`}
            </button>
            <button type="button" aria-label="Dismiss suggestion" disabled={busy} onClick={() => { setOpen(false); onDismiss() }}>
              <ThumbsDown size={14} />
            </button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

/** Suggested duplicate/related issues, loading any that the workspace snapshot does not include. */
function useSuggestedIssues(suggestions: IssueSuggestion[], data: BootstrapData) {
  return useIssuesById(suggestions.map(item => item.suggestedIssueId), data)
}

function normalizeRemote(result: unknown): RemoteSuggestions {
  const value = (result ?? {}) as { suggestions?: unknown; suggestionsGeneratedAt?: unknown; thinking?: unknown; pending?: unknown }
  const suggestions = Array.isArray(value.suggestions) ? (value.suggestions as IssueSuggestion[]) : []
  const generatedAt = typeof value.suggestionsGeneratedAt === 'string' ? value.suggestionsGeneratedAt : undefined
  return {
    suggestions,
    generatedAt,
    thinking: readThinking(value.thinking),
    pending: value.pending === true || (!generatedAt && !suggestions.length),
  }
}

/** `m:ss` like Linear's thinking timer. */
function formatElapsed(seconds: number) {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

/** Seconds since `active` turned on (Linear's thinking timer); resets when it turns off. */
function usePendingSeconds(active: boolean) {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    if (!active) return
    const started = Date.now()
    setSeconds(0)
    const timer = setInterval(() => setSeconds(Math.floor((Date.now() - started) / 1000)), 1000)
    return () => clearInterval(timer)
  }, [active])
  return seconds
}

/** Model thinking may arrive as a string, a list of steps, or `{ summary }`. */
function readThinking(value: unknown): string | undefined {
  if (typeof value === 'string') return value.trim() || undefined
  if (Array.isArray(value)) return value.filter(item => typeof item === 'string').join('\n').trim() || undefined
  if (value && typeof value === 'object' && 'summary' in value) return readThinking((value as { summary: unknown }).summary)
  return undefined
}

function sourceOf(suggestion: IssueSuggestion) {
  return typeof suggestion.metadata?.source === 'string' ? suggestion.metadata.source : undefined
}

function propertySuggestionTarget(
  suggestion: PropertySuggestion,
  data: BootstrapData,
): { detail: string; chip: string; label: string; whyTitle: string; icon: ReactNode; header: ReactNode } | undefined {
  if (suggestion.type === 'assignee') {
    const user = data.users.find(item => item.id === suggestion.suggestedUserId)
    return user
      ? {
          detail: user.displayName,
          chip: user.displayName,
          label: 'Assign to user',
          whyTitle: 'Why this assignee was suggested',
          icon: <UserAvatar className="triage-intelligence-avatar" name={user.displayName} avatarUrl={user.avatarUrl} />,
          header: (
            <div className="triage-intelligence-entity">
              <UserAvatar className="triage-intelligence-avatar is-large" name={user.displayName} avatarUrl={user.avatarUrl} />
              <div data-i18n-ignore>
                <strong>{user.displayName}</strong>
                {user.email ? <span>{user.email}</span> : null}
              </div>
            </div>
          ),
        }
      : undefined
  }
  if (suggestion.type === 'project') {
    const project = data.projects.find(item => item.id === suggestion.suggestedProjectId)
    return project
      ? {
          detail: project.name,
          chip: project.name,
          label: 'Add to project',
          whyTitle: 'Why this project was suggested',
          icon: <ProjectIcon style={{ color: project.color }} />,
          header: (
            <div className="triage-intelligence-entity">
              <ProjectIcon className="triage-intelligence-entity-icon" style={{ color: project.color }} />
              <div>
                <strong data-i18n-ignore>{project.name}</strong>
                {project.summary ? <span data-i18n-ignore>{project.summary}</span> : null}
                <span className="triage-intelligence-entity-meta">
                  {project.status ? (
                    <span>
                      <i className="triage-intelligence-chip-dot" style={{ backgroundColor: project.status.color }} />
                      {project.status.name}
                    </span>
                  ) : null}
                  {project.priorityLabel ? <span>{project.priorityLabel}</span> : null}
                </span>
              </div>
            </div>
          ),
        }
      : undefined
  }
  if (suggestion.type === 'label') {
    const label = data.labels.find(item => item.id === suggestion.suggestedLabelId)
    const dot = label ? <span className="triage-intelligence-chip-dot" style={{ backgroundColor: label.color }} /> : null
    return label
      ? {
          detail: label.name,
          chip: label.name,
          label: 'Add label',
          whyTitle: 'Why this label was suggested',
          icon: dot,
          header: (
            <div className="triage-intelligence-entity is-inline">
              {dot}
              <strong data-i18n-ignore>{label.name}</strong>
            </div>
          ),
        }
      : undefined
  }
  const team = data.teams.find(item => item.id === suggestion.suggestedTeamId)
  return team
    ? {
        detail: team.name,
        chip: team.key,
        label: 'Move to team',
        whyTitle: 'Why this team was suggested',
        icon: <TeamIcon team={team} />,
        header: (
          <div className="triage-intelligence-entity is-inline">
            <TeamIcon team={team} className="triage-intelligence-entity-icon" />
            <strong data-i18n-ignore>{team.name}</strong>
          </div>
        ),
      }
    : undefined
}
