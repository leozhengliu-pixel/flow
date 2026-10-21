import * as Popover from '@radix-ui/react-popover'
import { useContext, useEffect, useRef, useState } from 'react'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { StatusIcon } from '@/components/issue/issue-icons'
import { fetchIssueRecord } from '@/lib/api'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import type { Issue } from '@/types/flow'
import styles from './issues-split-view.module.css'

export type IssueRelationKind = 'blocked_by' | 'blocks'

const HOVER_LIMIT = 5
/** Related issues fetched for a hover card (the paged list only carries the ids). */
const fetched = new Map<string, Promise<Issue | undefined>>()

function useRelatedIssue(id: string, enabled: boolean) {
  const app = useContext(ApplicationStoreContext)
  const known = app?.data?.issues.find(issue => issue.id === id)
  const [loaded, setLoaded] = useState<Issue | undefined>()
  useEffect(() => {
    if (!enabled || known) return
    let live = true
    let pending = fetched.get(id)
    if (!pending) {
      pending = fetchIssueRecord(id).catch(() => undefined)
      fetched.set(id, pending)
    }
    void pending.then(issue => { if (live) setLoaded(issue) })
    return () => { live = false }
  }, [enabled, id, known])
  return known ?? loaded
}

function RelationHoverRow({ id, kind }: { id: string; kind: IssueRelationKind }) {
  const issue = useRelatedIssue(id, true)
  if (!issue) return <div className={styles.relationHoverBody} aria-busy="true"><span className={styles.relationHoverTitle}>…</span></div>
  return <div className={styles.relationHoverBody}>
    <span className={styles.relationHoverKind}>{kind === 'blocked_by' ? 'Blocked by' : 'Blocking'} <span data-i18n-ignore>{issue.identifier}</span></span>
    <span className={styles.relationHoverTitle} data-i18n-ignore>{issue.title}</span>
    <span className={styles.relationHoverDivider} aria-hidden="true"/>
    <span className={styles.relationHoverStatus}>
      <StatusIcon state={{ id: issue.state.id, name: issue.state.name, type: issue.state.type, color: issue.state.color }} size={14}/>
      <span data-i18n-ignore>{issue.state.name}</span>
    </span>
  </div>
}

/**
 * Blocked / blocking chip of a list row or board card: a pill with the relation glyph (and, in rows, the count) that
 * opens a hover card with the first related issue's identifier, title and status.
 */
export function IssueRelationChip({ kind, count, ids, compact = false }: { kind: IssueRelationKind; count: number; ids?: readonly string[]; compact?: boolean }) {
  const [open, setOpen] = useState(false)
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(timer.current), [])
  const enter = () => { window.clearTimeout(timer.current); timer.current = window.setTimeout(() => setOpen(true), 150) }
  const leave = () => { window.clearTimeout(timer.current); timer.current = window.setTimeout(() => setOpen(false), 100) }
  const blocked = kind === 'blocked_by'
  const label = blocked
    ? count === 1 ? 'Blocked by 1 issue' : `Blocked by ${count} issues`
    : count === 1 ? 'Blocking 1 issue' : `Blocking ${count} issues`
  const shown = (ids ?? []).slice(0, HOVER_LIMIT)
  const chip = <span
    className={styles.adornment}
    data-relation={kind}
    data-compact={compact || undefined}
    aria-label={label}
    onPointerEnter={enter}
    onPointerLeave={leave}
  >
    <IssueActionGlyph label={blocked ? 'Blocked by…' : 'Blocking…'} fallback={<span aria-hidden="true">{blocked ? '⊘' : '⊞'}</span>}/>
    {compact ? null : <span>{count}</span>}
  </span>
  if (!shown.length) return chip
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Anchor asChild>{chip}</Popover.Anchor>
    <Popover.Portal>
      <Popover.Content
        data-flow-motion="floating"
        className={styles.relationHover}
        side="top"
        align="end"
        sideOffset={6}
        collisionPadding={8}
        onOpenAutoFocus={event => event.preventDefault()}
        onCloseAutoFocus={event => event.preventDefault()}
        onPointerEnter={() => { window.clearTimeout(timer.current) }}
        onPointerLeave={leave}
      >
        {shown.map(id => <RelationHoverRow key={id} id={id} kind={kind}/>)}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
