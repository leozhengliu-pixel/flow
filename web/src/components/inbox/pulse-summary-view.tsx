import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { formatDistanceToNowStrict } from 'date-fns'
import { useRequestEntityUpdates } from '@/lib/entity-updates'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { RichComment } from '@/components/activity/rich-comment'
import { PulseDiffBlock } from '@/components/pulse/pulse-diff-block'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { healthColor } from '@/components/project-detail/health-color'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { fetchPulseSummary, reportPulseSummary } from '@/lib/api'
import { formatPlaybackRate, formatPulseAudioTime, PULSE_PLAYBACK_RATES, pulseAudio, usePulseAudio } from '@/lib/pulse-audio'
import type { BootstrapData, InitiativeUpdate, Project, ProjectUpdate, PulseCapabilities, PulseSummary, PulseSummaryItem } from '@/types/flow'

import { healthLabel } from './hosts/inbox-host-types'
import { loadPulseCapabilities, readPulseSummaryDisplay, writePulseSummaryDisplay, type PulseSummaryDisplay } from './pulse-summary-model'
import { markdownPlainText } from '@/lib/markdown-plain-text'
import './pulse-summary-view.css'

type SourceKind = 'project' | 'initiative'
type ResolvedUpdate = { kind: SourceKind; sourceId: string; update: ProjectUpdate | InitiativeUpdate }
type ReportTarget = { updateId: string; sourceName: string }

export interface PulseSummaryViewProps {
  notificationId: string
  /** "Daily Pulse" / "Weekly Pulse"; the page falls back to "Your Pulse". */
  title?: string
  data: Pick<BootstrapData, 'viewer' | 'projectUpdates' | 'initiativeUpdates' | 'projects' | 'initiatives'>
  onOpenProject?: (project: Project) => void
  onOpenInitiative?: (initiative: BootstrapData['initiatives'][number]) => void
}

/** Linear's "Your Pulse": the summary of a Pulse summary notification in the inbox detail pane. */
export function PulseSummaryView({ notificationId, title, data, onOpenProject, onOpenInitiative }: PulseSummaryViewProps) {
  const { t } = useI18n()
  const [summary, setSummary] = useState<PulseSummary>()
  const [failed, setFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [capabilities, setCapabilities] = useState<PulseCapabilities>()
  const [display, setDisplayState] = useState<PulseSummaryDisplay>(() => readPulseSummaryDisplay(data.viewer.id))
  const [reportTarget, setReportTarget] = useState<ReportTarget | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  // "Updates" shows full update cards: load their projects'/initiatives' updates (paged workspaces).
  const sourceIds = (kind: 'project' | 'initiative') => display === 'updates' ? (summary?.sections ?? []).filter(section => section.kind === kind).flatMap(section => section.items.map(item => item.sourceId)) : []
  useRequestEntityUpdates('project', sourceIds('project'))
  useRequestEntityUpdates('initiative', sourceIds('initiative'))

  useEffect(() => {
    const controller = new AbortController()
    setSummary(undefined)
    setFailed(false)
    fetchPulseSummary(notificationId, controller.signal)
      .then(next => { if (!controller.signal.aborted) setSummary(next) })
      .catch(() => { if (!controller.signal.aborted) setFailed(true) })
    return () => controller.abort()
  }, [attempt, notificationId])

  useEffect(() => {
    let active = true
    void loadPulseCapabilities().then(next => { if (active) setCapabilities(next) })
    return () => { active = false }
  }, [])

  const setDisplay = (next: PulseSummaryDisplay) => {
    setDisplayState(next)
    writePulseSummaryDisplay(data.viewer.id, next)
  }

  const updates = useMemo(() => indexUpdates(data), [data])
  const sections = useMemo(() => orderedSections(summary), [summary])
  const heading = title?.trim() || summary?.title?.trim() || 'Your Pulse'
  const showUpdates = display === 'updates'

  const openSource = (kind: SourceKind, sourceId?: string) => {
    if (!sourceId) return
    if (kind === 'project') {
      const project = data.projects.find(item => item.id === sourceId)
      if (project) onOpenProject?.(project)
    } else {
      const initiative = data.initiatives.find(item => item.id === sourceId)
      if (initiative) onOpenInitiative?.(initiative)
    }
  }

  const scrollTo = (id: string) => {
    const target = scrollRef.current?.querySelector<HTMLElement>(`[data-pulse-anchor="${cssEscape(id)}"]`)
    target?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  const tocEntries = useMemo(() => {
    const grouped = sections.length > 1
    const entries: Array<{ id: string; label: string; level: 1 | 2 }> = []
    for (const section of sections) {
      if (grouped) entries.push({ id: `group-${section.kind}`, label: t(groupTitle(section.kind)), level: 1 })
      for (const item of section.items) entries.push({ id: item.updateId, label: item.sourceName, level: grouped ? 2 : 1 })
    }
    return entries
  }, [sections, t])

  let body: ReactNode
  if (failed) {
    body = (
      <div className="flow-pulse-summary__state flow-pulse-summary__state--error" role="alert">
        <strong>{t('Unable to load summary')}</strong>
        <button type="button" onClick={() => setAttempt(value => value + 1)}>{t('Try again')}</button>
      </div>
    )
  } else if (!summary) {
    body = (
      <div className="flow-pulse-summary__state" role="status" aria-label={t('Loading summary')}>
        <i /><span /><span /><i /><span /><span />
      </div>
    )
  } else if (!sections.length) {
    body = summary.text.trim()
      ? <p className="flow-pulse-summary__text">{markdownPlainText(summary.text)}</p>
      : <p className="flow-pulse-summary__empty">{t('No updates')}</p>
  } else {
    body = sections.map(section => (
      <section key={section.kind} className="flow-pulse-summary__group" data-pulse-anchor={`group-${section.kind}`} aria-label={t(groupTitle(section.kind))}>
        <header className="flow-pulse-summary__group-title"><span>{t(groupTitle(section.kind))}</span><i aria-hidden="true" /></header>
        <div className="flow-pulse-summary__cards" data-display={display}>
          {section.items.map(item => {
            const resolved = updates.get(item.updateId)
            const sourceId = resolved?.sourceId ?? (item as PulseSummaryItem & { sourceId?: string }).sourceId
            const common = { item, kind: section.kind, resolved, onOpenSource: () => openSource(section.kind, sourceId), canOpenSource: Boolean(sourceId) }
            return showUpdates
              ? <PulseFullUpdateCard key={item.updateId} {...common} />
              : <PulseSummaryCard key={item.updateId} {...common} onReport={() => setReportTarget({ updateId: item.updateId, sourceName: item.sourceName })} />
          })}
        </div>
      </section>
    ))
  }

  return (
    <div className="flow-pulse-summary" data-display={display}>
      <div className="flow-pulse-summary__scroll" ref={scrollRef}>
        <div className="flow-pulse-summary__column">
          <header className="flow-pulse-summary__header">
            <h1 data-i18n-ignore>{t(heading)}</h1>
            <div className="flow-pulse-summary__controls">
              {capabilities?.audio ? <PulseListenControls notificationId={notificationId} /> : null}
              <PulseDisplayOptions value={display} onChange={setDisplay} />
            </div>
          </header>
          <div className="flow-pulse-summary__body">{body}</div>
        </div>
      </div>
      {showUpdates && summary && tocEntries.length >= 3 ? (
        <nav className="flow-pulse-summary__toc" aria-label={t('Table of contents')}>
          {tocEntries.map(entry => (
            <button key={entry.id} type="button" data-level={entry.level} title={entry.label} onClick={() => scrollTo(entry.id)}>
              <span data-i18n-ignore>{entry.label}</span>
            </button>
          ))}
        </nav>
      ) : null}
      <ReportSummaryDialog notificationId={notificationId} target={reportTarget} onClose={() => setReportTarget(null)} />
    </div>
  )
}

/** "Listen" — play/pause the summary audio, with the "Playback speed" menu beside it. */
function PulseListenControls({ notificationId }: { notificationId: string }) {
  const { t } = useI18n()
  const audio = usePulseAudio()
  const current = audio.notificationId === notificationId
  const loading = current && audio.status === 'loading'
  const playing = current && audio.status === 'playing'
  const showTime = current && !loading && (playing || audio.currentTime > 0)
  const failedRef = useRef(false)

  useEffect(() => {
    if (current && audio.status === 'error' && !failedRef.current) {
      failedRef.current = true
      toast.error(t('Couldn’t play the summary'))
    }
    if (audio.status !== 'error') failedRef.current = false
  }, [audio.status, current, t])

  const timeTitle = showTime && audio.duration > 0 ? `${formatPulseAudioTime(audio.currentTime)} / ${formatPulseAudioTime(audio.duration)}` : undefined
  return (
    <div className="flow-pulse-listen">
      <button
        type="button"
        className="flow-pulse-listen__button"
        data-loading={loading || undefined}
        disabled={loading}
        aria-label={playing ? t('Pause') : t('Listen')}
        aria-pressed={playing}
        title={timeTitle}
        onClick={() => pulseAudio.toggle(notificationId)}
      >
        {playing ? <PauseIcon /> : <PlayIcon />}
        {showTime
          ? <span className="flow-pulse-listen__time" data-i18n-ignore>{formatPulseAudioTime(audio.currentTime)}</span>
          : <span className="flow-pulse-listen__label" data-shimmer={loading || undefined}>{t('Listen')}</span>}
      </button>
      <DropdownMenu.Root modal={false}>
        <DropdownMenu.Trigger asChild>
          <button type="button" className="flow-pulse-listen__speed" aria-label={t('Playback speed')} title={t('Playback speed')}>
            <span data-i18n-ignore>{formatPlaybackRate(audio.playbackRate)}</span>
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content data-flow-motion="floating" className="flow-inbox-menu flow-pulse-summary-menu" align="end" sideOffset={4} aria-label={t('Playback speed')}>
            <DropdownMenu.Label className="flow-pulse-summary-menu__label">{t('Playback speed')}</DropdownMenu.Label>
            <DropdownMenu.RadioGroup value={String(audio.playbackRate)} onValueChange={value => pulseAudio.setPlaybackRate(Number(value))}>
              {PULSE_PLAYBACK_RATES.map(rate => (
                <DropdownMenu.RadioItem key={rate} value={String(rate)} className="flow-inbox-menu__item">
                  <span className="flow-inbox-menu__item-label" data-i18n-ignore>{formatPlaybackRate(rate)}</span>
                  <DropdownMenu.ItemIndicator className="flow-inbox-menu__trailing"><CheckIcon /></DropdownMenu.ItemIndicator>
                </DropdownMenu.RadioItem>
              ))}
            </DropdownMenu.RadioGroup>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </div>
  )
}

function PulseDisplayOptions({ value, onChange }: { value: PulseSummaryDisplay; onChange: (value: PulseSummaryDisplay) => void }) {
  const { t } = useI18n()
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button type="button" className="flow-pulse-summary__icon-button" aria-label={t('Pulse display options')} title={t('Pulse display options')}>
          <DisplayIcon />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating" className="flow-inbox-menu flow-pulse-summary-menu" align="end" sideOffset={4} aria-label={t('Pulse display options')}>
          <DropdownMenu.RadioGroup value={value} onValueChange={next => onChange(next === 'updates' ? 'updates' : 'summaries')}>
            <DropdownMenu.RadioItem value="summaries" className="flow-inbox-menu__item">
              <span className="flow-inbox-menu__item-icon"><SummariesIcon /></span>
              <span className="flow-inbox-menu__item-label">{t('Summaries')}</span>
              <DropdownMenu.ItemIndicator className="flow-inbox-menu__trailing"><CheckIcon /></DropdownMenu.ItemIndicator>
            </DropdownMenu.RadioItem>
            <DropdownMenu.RadioItem value="updates" className="flow-inbox-menu__item">
              <span className="flow-inbox-menu__item-icon"><UpdatesIcon /></span>
              <span className="flow-inbox-menu__item-label">{t('Updates')}</span>
              <DropdownMenu.ItemIndicator className="flow-inbox-menu__trailing"><CheckIcon /></DropdownMenu.ItemIndicator>
            </DropdownMenu.RadioItem>
          </DropdownMenu.RadioGroup>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

type CardProps = {
  item: PulseSummaryItem
  kind: SourceKind
  resolved?: ResolvedUpdate
  canOpenSource: boolean
  onOpenSource: () => void
}

function CardHeader({ item, resolved, canOpenSource, onOpenSource, menu }: CardProps & { menu?: ReactNode }) {
  const { t } = useI18n()
  const health = resolved?.update.health ?? item.health
  const author = resolved?.update.user
  const authorName = author ? author.displayName || author.name : undefined
  return (
    <header className="flow-pulse-summary-card__header">
      <div className="flow-pulse-summary-card__title">
        {canOpenSource
          ? <button type="button" className="flow-pulse-summary-card__source" onClick={onOpenSource} data-i18n-ignore>{item.sourceName}</button>
          : <strong className="flow-pulse-summary-card__source" data-i18n-ignore>{item.sourceName}</strong>}
        {menu}
      </div>
      <div className="flow-pulse-summary-card__byline">
        {health && health !== 'noUpdate' ? (
          <span className="flow-pulse-summary-card__health" style={{ color: healthColor(health) }}>
            <HealthGlyph health={health} className="flow-pulse-summary-card__health-icon" />
            <span>{t(healthLabel(health))}</span>
          </span>
        ) : null}
        {author && authorName ? <>
          <UserAvatar className="avatar flow-pulse-summary-card__avatar" name={authorName} avatarUrl={author.avatarUrl} />
          <span className="flow-pulse-summary-card__author" data-i18n-ignore>{authorName}</span>
        </> : null}
        {resolved ? <time dateTime={resolved.update.createdAt} title={new Date(resolved.update.createdAt).toLocaleString()}>{formatDistanceToNowStrict(new Date(resolved.update.createdAt), { addSuffix: true })}</time> : null}
      </div>
    </header>
  )
}

/** "Summaries": a short summary per update, with "Report invalid summary…". */
function PulseSummaryCard(props: CardProps & { onReport: () => void }) {
  const { t } = useI18n()
  const menu = (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button type="button" className="flow-pulse-summary__icon-button flow-pulse-summary-card__menu" aria-label={t('Update actions')} title={t('Update actions')}>
          <MoreIcon />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating" className="flow-inbox-menu flow-pulse-summary-menu" align="end" sideOffset={4} aria-label={t('Update actions')}>
          <DropdownMenu.Item className="flow-inbox-menu__item" onSelect={props.onReport}>
            <span className="flow-inbox-menu__item-icon"><ReportIcon /></span>
            <span className="flow-inbox-menu__item-label">{t('Report invalid summary…')}</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
  return (
    <article className="flow-pulse-summary-card" data-pulse-anchor={props.item.updateId} aria-label={props.item.sourceName}>
      <CardHeader {...props} menu={menu} />
      <p className="flow-pulse-summary-card__summary" data-i18n-ignore>{markdownPlainText(props.item.summary)}</p>
    </article>
  )
}

/** "Updates": the full update body when it is loaded, otherwise its summary. */
function PulseFullUpdateCard(props: CardProps) {
  const update = props.resolved?.update
  return (
    <article className="flow-pulse-summary-card flow-pulse-summary-card--full" data-pulse-anchor={props.item.updateId} aria-label={props.item.sourceName}>
      <CardHeader {...props} />
      {update
        ? <div className="flow-pulse-summary-card__body" data-i18n-ignore><RichComment body={update.body} data={update.bodyData} /></div>
        : <p className="flow-pulse-summary-card__summary" data-i18n-ignore>{markdownPlainText(props.item.summary)}</p>}
      {update ? <PulseDiffBlock diff={update.diff} kind={props.kind} /> : null}
    </article>
  )
}

function ReportSummaryDialog({ notificationId, target, onClose }: { notificationId: string; target: ReportTarget | null; onClose: () => void }) {
  const { t } = useI18n()
  const [reason, setReason] = useState('')
  const [sending, setSending] = useState(false)
  useEffect(() => { if (target) setReason('') }, [target])
  const submit = useCallback(async () => {
    const text = reason.trim()
    if (!target || !text || sending) return
    setSending(true)
    try {
      await reportPulseSummary(notificationId, text, target.updateId)
      toast.success(t('Feedback sent'), { description: t('Thanks! We’ll use your feedback to improve this feature.') })
      onClose()
    } catch {
      toast.error(t('Couldn’t send feedback'), { description: t('Something went wrong. Please try again.') })
    } finally {
      setSending(false)
    }
  }, [notificationId, onClose, reason, sending, t, target])
  return (
    <Dialog open={Boolean(target)} onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent className="flow-pulse-report" closeLabel={t('Close')} aria-describedby="flow-pulse-report-description">
        <form onSubmit={event => { event.preventDefault(); void submit() }}>
          <div className="flow-pulse-report__body">
            <DialogTitle className="flow-pulse-report__title">{t('Leave feedback')}</DialogTitle>
            <p id="flow-pulse-report-description" className="flow-pulse-report__description">{t('Send our team your feedback for the AI generated content')}</p>
            <textarea
              className="flow-pulse-report__input"
              aria-label={t('Tell us more')}
              placeholder={t('Tell us more…')}
              value={reason}
              autoFocus
              onChange={event => setReason(event.target.value)}
              onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submit() } }}
            />
          </div>
          <footer className="flow-pulse-report__footer">
            <button type="button" className="flow-pulse-report__secondary" onClick={onClose}>{t('Cancel')}</button>
            <button type="submit" className="flow-pulse-report__primary" disabled={!reason.trim() || sending}>{t('Send')}</button>
          </footer>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function groupTitle(kind: SourceKind) {
  return kind === 'initiative' ? 'Initiatives' : 'Projects'
}

/** Linear groups initiatives before projects and drops empty groups. */
function orderedSections(summary?: PulseSummary) {
  if (!summary) return []
  const rank = (kind: string) => kind === 'initiative' ? 0 : 1
  return [...(summary.sections ?? [])]
    .filter(section => section.items?.length)
    .sort((left, right) => rank(left.kind) - rank(right.kind))
}

function indexUpdates(data: PulseSummaryViewProps['data']) {
  const index = new Map<string, ResolvedUpdate>()
  for (const [sourceId, list] of Object.entries(data.projectUpdates ?? {})) {
    for (const update of list ?? []) index.set(update.id, { kind: 'project', sourceId, update })
  }
  for (const [sourceId, list] of Object.entries(data.initiativeUpdates ?? {})) {
    for (const update of list ?? []) index.set(update.id, { kind: 'initiative', sourceId, update })
  }
  return index
}

function cssEscape(value: string) {
  return globalThis.CSS?.escape ? globalThis.CSS.escape(value) : value.replace(/["\\]/g, '\\$&')
}

function PlayIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m5.604 2.41 7.23 4.502a1.375 1.375 0 0 1-.02 2.345L5.585 13.6a1.375 1.375 0 0 1-2.083-1.18V3.576A1.375 1.375 0 0 1 5.604 2.41Z" /></svg>
}
function PauseIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="3.5" y="2.5" width="3" height="11" rx="1" /><rect x="9.5" y="2.5" width="3" height="11" rx="1" /></svg>
}
function CheckIcon() {
  return <svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m3.5 8.5 3 3 6-7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
}
function MoreIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z" /></svg>
}
function DisplayIcon() {
  return <svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></svg>
}
function SummariesIcon() {
  return <svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3 4h10M3 7h10M3 10h6" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /></svg>
}
function UpdatesIcon() {
  return <svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><rect x="2.5" y="2.5" width="11" height="11" rx="2" stroke="currentColor" strokeWidth="1.3" /><path d="M5 6h6M5 8.5h6M5 11h3.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" /></svg>
}
function ReportIcon() {
  return <svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3.5 14V2.75m0 0h7.25l-1.5 2.5 1.5 2.5H3.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" /></svg>
}
