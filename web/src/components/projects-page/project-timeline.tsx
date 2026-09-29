import * as Tooltip from '@radix-ui/react-tooltip'
import { memo, useCallback, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { MILESTONE_SHAPE_PATH } from '@/components/issue/milestone-progress'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import type { ProjectDataGroup, ProjectPageItem } from './projects-data-view'
import {
  addTimelineDays,
  daysBetween,
  isoTimelineDay,
  normalizeTimelineZoom,
  parseTimelineDay,
  projectTimelineSpan,
  stepTimelineZoom,
  TIMELINE_ZOOM_LABELS,
  TIMELINE_ZOOMS,
  timelineBarBox,
  timelineDateAt,
  timelineDaysForPixels,
  timelineDependencyPaths,
  timelineHeader,
  timelineMilestoneMarkers,
  timelineRange,
  timelineX,
  type TimelineDependency,
  type TimelineMilestoneMarker,
  type TimelineRange,
  type TimelineRowBox,
  type TimelineSpan,
  type TimelineZoom,
} from './project-timeline-model'
import './project-timeline.css'

export const TIMELINE_LABEL_WIDTH = 240
const ROW_HEIGHT = 36
const GROUP_HEIGHT = 32
const DRAG_THRESHOLD = 3

type DateInput = { startDate?: string; targetDate?: string }

export type ProjectTimelineProps = {
  groups: ProjectDataGroup[]
  zoom?: TimelineZoom
  onZoomChange?: (zoom: TimelineZoom) => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenMilestone?: (project: ProjectPageItem, milestoneId: string) => void
  onUpdateProject?: (projectId: string, input: DateInput) => Promise<unknown>
  onUpdateMilestone?: (projectId: string, milestoneId: string, input: { targetDate: string }) => Promise<unknown>
  onCreateDependency?: (blockerId: string, blockedId: string) => Promise<unknown>
  renderGroupIcon?: (group: ProjectDataGroup) => ReactNode
  groupCount?: (group: ProjectDataGroup) => number
  /** Injectable for tests. */
  today?: Date
}

type Drag =
  | { kind: 'bar'; id: string; mode: 'move' | 'resize-start' | 'resize-end'; originX: number; span: TimelineSpan; delta: number; moved: boolean }
  | { kind: 'milestone'; projectId: string; milestoneId: string; originX: number; date: Date; delta: number; moved: boolean }
  | { kind: 'create'; id: string; originX: number; currentX: number; moved: boolean }
  | { kind: 'link'; id: string; side: 'start' | 'end'; from: { x: number; y: number }; pointer: { x: number; y: number }; targetId?: string }

type Row =
  | { kind: 'group'; key: string; y: number; group: ProjectDataGroup }
  | { kind: 'project'; key: string; y: number; project: ProjectPageItem; span?: TimelineSpan }

export function ProjectTimeline({ groups, zoom: zoomProp, onZoomChange, onOpenProject, onOpenMilestone, onUpdateProject, onUpdateMilestone, onCreateDependency, renderGroupIcon, groupCount, today: todayProp }: ProjectTimelineProps) {
  const [localZoom, setLocalZoom] = useState<TimelineZoom>(() => normalizeTimelineZoom(zoomProp))
  const zoom = zoomProp ? normalizeTimelineZoom(zoomProp) : localZoom
  const [drag, setDragState] = useState<Drag>()
  // Mirror of `drag` so pointer handlers see the latest gesture even before React re-renders (fast drags).
  const dragRef = useRef<Drag>(undefined)
  const setDrag = (next: Drag | undefined) => { dragRef.current = next; setDragState(next) }
  const [hoveredId, setHoveredId] = useState<string>()
  const suppressClick = useRef(false)
  const scrollerRef = useRef<HTMLDivElement>(null)
  const bodyRef = useRef<HTMLDivElement>(null)
  const markerId = useId().replace(/:/g, '')
  const todayKey = isoTimelineDay(todayProp ?? new Date())
  const today = useMemo(() => parseTimelineDay(todayKey)!, [todayKey])

  const rows = useMemo(() => {
    const flat = groups.flatMap(group => group.subgroups?.length ? group.subgroups.map(subgroup => ({ ...subgroup, name: `${group.name} / ${subgroup.name}` })) : [group])
    const output: Row[] = []
    let y = 0
    for (const group of flat) {
      output.push({ kind: 'group', key: `group:${group.id}`, y, group })
      y += GROUP_HEIGHT
      for (const project of group.projects) {
        output.push({ kind: 'project', key: `project:${group.id}:${project.id}`, y, project, span: projectTimelineSpan(project.rawStartDate, project.rawTargetDate) })
        y += ROW_HEIGHT
      }
    }
    return { rows: output, height: y }
  }, [groups])

  const range = useMemo(() => {
    const dates: Date[] = []
    for (const row of rows.rows) {
      if (row.kind !== 'project') continue
      if (row.span) dates.push(row.span.start, row.span.end)
      for (const milestone of row.project.milestones ?? []) { const date = parseTimelineDay(milestone.targetDate); if (date) dates.push(date) }
    }
    return timelineRange(dates, today, zoom)
  }, [rows, today, zoom])
  const header = useMemo(() => timelineHeader(range), [range])
  const gridLines = useMemo(() => (zoom === 'week' ? header.minor.filter(segment => segment.emphasis) : header.minor), [header, zoom])
  const todayX = timelineX(range, today) + range.pxPerDay / 2

  const previewSpan = useCallback((projectId: string, span: TimelineSpan): TimelineSpan => {
    if (drag?.kind !== 'bar' || drag.id !== projectId || !drag.delta) return span
    if (drag.mode === 'move') return { ...span, start: addTimelineDays(span.start, drag.delta), end: addTimelineDays(span.end, drag.delta) }
    if (drag.mode === 'resize-start') { const start = addTimelineDays(span.start, drag.delta); return { ...span, start: start > span.end ? span.end : start } }
    const end = addTimelineDays(span.end, drag.delta)
    return { ...span, end: end < span.start ? span.start : end }
  }, [drag])

  const baseBoxes = useMemo(() => {
    const map = new Map<string, TimelineRowBox>()
    for (const row of rows.rows) {
      if (row.kind !== 'project' || !row.span || map.has(row.project.id)) continue
      map.set(row.project.id, { ...timelineBarBox(range, row.span), y: row.y + ROW_HEIGHT / 2, start: row.span.start, end: row.span.end })
    }
    return map
  }, [range, rows])
  // Only the dragged bar gets a new box so memoised rows stay untouched while dragging.
  const boxes = useMemo(() => {
    if (drag?.kind !== 'bar' || !drag.delta) return baseBoxes
    const current = baseBoxes.get(drag.id)
    if (!current) return baseBoxes
    const span = previewSpan(drag.id, drag.span)
    const map = new Map(baseBoxes)
    map.set(drag.id, { ...timelineBarBox(range, span), y: current.y, start: span.start, end: span.end })
    return map
  }, [baseBoxes, drag, previewSpan, range])

  const dependencies = useMemo<TimelineDependency[]>(() => rows.rows.flatMap(row => row.kind === 'project' ? (row.project.blockedByIds ?? []).map(blockerId => ({ blockerId, blockedId: row.project.id })) : []), [rows])
  const paths = useMemo(() => timelineDependencyPaths(dependencies, boxes), [boxes, dependencies])

  // Keep the viewport anchored: centre today on mount, keep the centre date across zoom changes,
  // and compensate when the range start moves because data changed.
  const pendingCenter = useRef<Date | undefined>(today)
  const previousRange = useRef<Pick<TimelineRange, 'start' | 'pxPerDay'>>(undefined)
  useLayoutEffect(() => {
    const node = scrollerRef.current
    const previous = previousRange.current
    previousRange.current = { start: range.start, pxPerDay: range.pxPerDay }
    if (!node) return
    const visible = Math.max(0, node.clientWidth - TIMELINE_LABEL_WIDTH)
    if (pendingCenter.current) {
      node.scrollLeft = Math.max(0, timelineX(range, pendingCenter.current) - visible / 2)
      pendingCenter.current = undefined
    } else if (previous && previous.pxPerDay === range.pxPerDay && previous.start.getTime() !== range.start.getTime()) {
      node.scrollLeft += daysBetween(previous.start, range.start) * -range.pxPerDay
    }
  }, [range])

  const centerDate = () => {
    const node = scrollerRef.current
    if (!node) return today
    return timelineDateAt(range, node.scrollLeft + Math.max(0, node.clientWidth - TIMELINE_LABEL_WIDTH) / 2)
  }
  const changeZoom = (next: TimelineZoom) => {
    if (next === zoom) return
    pendingCenter.current = centerDate()
    if (!zoomProp) setLocalZoom(next)
    onZoomChange?.(next)
  }
  const scrollToToday = () => {
    const node = scrollerRef.current
    if (!node) return
    const left = Math.max(0, todayX - Math.max(0, node.clientWidth - TIMELINE_LABEL_WIDTH) / 2)
    if (typeof node.scrollTo === 'function') node.scrollTo({ left, behavior: 'smooth' })
    else node.scrollLeft = left
  }
  const onRootKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || (event.target as HTMLElement).closest('input,textarea,[contenteditable=true]')) return
    if (event.key === '+' || event.key === '=') { event.preventDefault(); changeZoom(stepTimelineZoom(zoom, -1)) }
    else if (event.key === '-' || event.key === '_') { event.preventDefault(); changeZoom(stepTimelineZoom(zoom, 1)) }
  }

  const pointInTrack = (event: { clientX: number; clientY: number }) => {
    const rect = bodyRef.current?.getBoundingClientRect()
    return { x: event.clientX - (rect?.left ?? 0) - TIMELINE_LABEL_WIDTH, y: event.clientY - (rect?.top ?? 0) }
  }
  const capture = (event: PointerEvent<HTMLElement>) => { event.currentTarget.setPointerCapture?.(event.pointerId) }

  const startBarDrag = (event: PointerEvent<HTMLElement>, project: ProjectPageItem, span: TimelineSpan, mode: 'move' | 'resize-start' | 'resize-end') => {
    if (!onUpdateProject || event.button !== 0) return
    event.preventDefault(); event.stopPropagation(); capture(event)
    setDrag({ kind: 'bar', id: project.id, mode, originX: event.clientX, span, delta: 0, moved: false })
  }
  const startMilestoneDrag = (event: PointerEvent<HTMLElement>, project: ProjectPageItem, marker: TimelineMilestoneMarker) => {
    if (event.button !== 0) return
    event.stopPropagation()
    if (!onUpdateMilestone) return
    event.preventDefault(); capture(event)
    setDrag({ kind: 'milestone', projectId: project.id, milestoneId: marker.id, originX: event.clientX, date: marker.date, delta: 0, moved: false })
  }
  const startCreate = (event: PointerEvent<HTMLElement>, project: ProjectPageItem) => {
    if (!onUpdateProject || event.button !== 0) return
    event.preventDefault(); capture(event)
    const { x } = pointInTrack(event)
    setDrag({ kind: 'create', id: project.id, originX: x, currentX: x, moved: false })
  }
  const startLink = (event: PointerEvent<HTMLElement>, project: ProjectPageItem, side: 'start' | 'end') => {
    const box = boxes.get(project.id)
    if (!onCreateDependency || !box || event.button !== 0) return
    event.preventDefault(); event.stopPropagation(); capture(event)
    const from = { x: side === 'end' ? box.left + box.width : box.left, y: box.y }
    setDrag({ kind: 'link', id: project.id, side, from, pointer: from })
  }

  /** Folds a pointer position into the active gesture. */
  const advance = (current: Drag, event: { clientX: number; clientY: number }): Drag => {
    if (current.kind === 'bar' || current.kind === 'milestone') {
      const dx = event.clientX - current.originX
      return { ...current, delta: timelineDaysForPixels(dx, range.pxPerDay), moved: current.moved || Math.abs(dx) > DRAG_THRESHOLD }
    }
    if (current.kind === 'create') {
      const { x } = pointInTrack(event)
      return { ...current, currentX: x, moved: current.moved || Math.abs(x - current.originX) > DRAG_THRESHOLD }
    }
    const target = projectIdAtPoint(event.clientX, event.clientY)
    return { ...current, pointer: pointInTrack(event), targetId: target && target !== current.id ? target : undefined }
  }
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const current = dragRef.current
    if (!current) return
    const next = advance(current, event)
    if (next.kind === 'bar' && current.kind === 'bar' && next.delta === current.delta && next.moved === current.moved) return
    if (next.kind === 'milestone' && current.kind === 'milestone' && next.delta === current.delta && next.moved === current.moved) return
    setDrag(next)
  }
  const onPointerUp = (event: PointerEvent<HTMLDivElement>) => {
    const started = dragRef.current
    if (!started) return
    const current = advance(started, event)
    setDrag(undefined)
    if (current.kind === 'bar') {
      if (!current.moved) return
      suppressClick.current = true
      if (!current.delta || !onUpdateProject) return
      const next = previewSpanFor(current)
      const input: DateInput = current.mode === 'move'
        ? { ...(current.span.hasStart ? { startDate: isoTimelineDay(next.start) } : {}), ...(current.span.hasTarget ? { targetDate: isoTimelineDay(next.end) } : {}) }
        : current.mode === 'resize-start' ? { startDate: isoTimelineDay(next.start) } : { targetDate: isoTimelineDay(next.end) }
      void onUpdateProject(current.id, input)
    } else if (current.kind === 'milestone') {
      if (!current.moved) return
      suppressClick.current = true
      if (current.delta && onUpdateMilestone) void onUpdateMilestone(current.projectId, current.milestoneId, { targetDate: isoTimelineDay(addTimelineDays(current.date, current.delta)) })
    } else if (current.kind === 'create') {
      if (!current.moved || !onUpdateProject) return
      suppressClick.current = true
      const from = timelineDateAt(range, Math.min(current.originX, current.currentX))
      const to = timelineDateAt(range, Math.max(current.originX, current.currentX))
      void onUpdateProject(current.id, { startDate: isoTimelineDay(from), targetDate: isoTimelineDay(to) })
    } else {
      const target = current.targetId
      if (!target || target === current.id || !onCreateDependency) return
      const blockerId = current.side === 'end' ? current.id : target
      const blockedId = current.side === 'end' ? target : current.id
      if (dependencies.some(edge => (edge.blockerId === blockedId && edge.blockedId === blockerId) || (edge.blockerId === blockerId && edge.blockedId === blockedId))) return
      void onCreateDependency(blockerId, blockedId)
    }
  }
  const onPointerCancel = () => setDrag(undefined)
  const consumeSuppressedClick = () => { if (!suppressClick.current) return false; suppressClick.current = false; return true }

  const nudge = (project: ProjectPageItem, span: TimelineSpan, days: number) => {
    if (!onUpdateProject) return
    void onUpdateProject(project.id, { ...(span.hasStart ? { startDate: isoTimelineDay(addTimelineDays(span.start, days)) } : {}), ...(span.hasTarget ? { targetDate: isoTimelineDay(addTimelineDays(span.end, days)) } : {}) })
  }

  const latest = useRef({ startBarDrag, startMilestoneDrag, startCreate, startLink, nudge, consumeSuppressedClick })
  latest.current = { startBarDrag, startMilestoneDrag, startCreate, startLink, nudge, consumeSuppressedClick }
  const stable = useMemo(() => ({
    startBarDrag: (...args: Parameters<typeof startBarDrag>) => latest.current.startBarDrag(...args),
    startMilestoneDrag: (...args: Parameters<typeof startMilestoneDrag>) => latest.current.startMilestoneDrag(...args),
    startCreate: (...args: Parameters<typeof startCreate>) => latest.current.startCreate(...args),
    startLink: (...args: Parameters<typeof startLink>) => latest.current.startLink(...args),
    nudge: (...args: Parameters<typeof nudge>) => latest.current.nudge(...args),
    consumeSuppressedClick: () => latest.current.consumeSuppressedClick(),
  }), [])

  const linkingActive = drag?.kind === 'link'
  const activeIds = new Set<string>([hoveredId, drag?.kind === 'bar' ? drag.id : undefined].filter((value): value is string => Boolean(value)))
  const createGhost = drag?.kind === 'create' && drag.moved ? { left: Math.min(drag.originX, drag.currentX), width: Math.abs(drag.currentX - drag.originX) } : undefined

  return <Tooltip.Provider delayDuration={200} skipDelayDuration={150}>
    <div aria-label="Project timeline" className="lp-project-timeline" data-zoom={zoom} onKeyDown={onRootKeyDown} role="region">
      <div className="lp-project-timeline__toolbar">
        <div aria-label="Timeline zoom" className="lp-project-timeline__zoom" role="radiogroup" title="Zoom in with + and out with −">
          {TIMELINE_ZOOMS.map(option => <button aria-checked={zoom === option} className="lp-project-timeline__zoom-option" key={option} onClick={() => changeZoom(option)} role="radio" type="button">{TIMELINE_ZOOM_LABELS[option]}</button>)}
        </div>
        <button aria-label="Center timeline on today" className="lp-project-timeline__today" onClick={scrollToToday} type="button">Today</button>
      </div>
      <div className="lp-project-timeline__scroller" ref={scrollerRef}>
        <div className="lp-project-timeline__canvas" style={{ width: TIMELINE_LABEL_WIDTH + range.width }}>
          <div className="lp-project-timeline__header">
            <div className="lp-project-timeline__corner">Projects</div>
            <div className="lp-project-timeline__scale" style={{ width: range.width }}>
              <div className="lp-project-timeline__major">{header.major.map(segment => <span key={segment.key} style={{ left: segment.x, width: segment.width }}><b data-i18n-ignore>{segment.label}</b></span>)}</div>
              <div className="lp-project-timeline__minor">{header.minor.map(segment => <span data-emphasis={segment.emphasis || undefined} key={segment.key} style={{ left: segment.x, width: segment.width }}>{segment.width >= 14 ? segment.label : ''}</span>)}</div>
              <span className="lp-project-timeline__today-chip" style={{ left: todayX }}>{today.toLocaleDateString('en', { month: 'short', day: 'numeric' })}</span>
            </div>
          </div>
          <div className="lp-project-timeline__body" data-linking={linkingActive || undefined} onPointerCancel={onPointerCancel} onPointerMove={onPointerMove} onPointerUp={onPointerUp} ref={bodyRef} style={{ height: Math.max(rows.height, ROW_HEIGHT) }}>
            <div aria-hidden="true" className="lp-project-timeline__grid" style={{ width: range.width }}>{gridLines.map(segment => <i data-emphasis={segment.emphasis || undefined} key={segment.key} style={{ left: segment.x }}/>)}</div>
            {rows.rows.map(row => row.kind === 'group'
              ? <div className="lp-project-timeline__group" key={row.key} style={{ top: row.y, height: GROUP_HEIGHT }}>
                <h2>{renderGroupIcon?.(row.group)}<span data-i18n-ignore>{row.group.name}</span><small>{groupCount ? groupCount(row.group) : row.group.projects.length}</small></h2>
              </div>
              : <TimelineProjectRow
                box={boxes.get(row.project.id)}
                consumeSuppressedClick={stable.consumeSuppressedClick}
                createGhost={drag?.kind === 'create' && drag.id === row.project.id ? createGhost : undefined}
                dragging={drag?.kind === 'bar' && drag.id === row.project.id}
                draggingMilestoneId={drag?.kind === 'milestone' && drag.projectId === row.project.id ? drag.milestoneId : undefined}
                milestoneDelta={drag?.kind === 'milestone' && drag.projectId === row.project.id ? drag.delta : 0}
                key={row.key}
                linkTarget={drag?.kind === 'link' && drag.targetId === row.project.id}
                canLink={Boolean(onCreateDependency)}
                canEdit={Boolean(onUpdateProject)}
                onHover={setHoveredId}
                onNudge={stable.nudge}
                onOpen={onOpenProject}
                onOpenMilestone={onOpenMilestone}
                onStartBarDrag={stable.startBarDrag}
                onStartCreate={stable.startCreate}
                onStartLink={stable.startLink}
                onStartMilestoneDrag={stable.startMilestoneDrag}
                project={row.project}
                range={range}
                span={row.span}
                y={row.y}
              />)}
            <svg aria-hidden="true" className="lp-project-timeline__links" height={Math.max(rows.height, ROW_HEIGHT)} width={range.width}>
              <defs>
                <marker id={`${markerId}-arrow`} markerHeight="6" markerWidth="6" orient="auto-start-reverse" refX="5" refY="3" viewBox="0 0 6 6"><path className="lp-project-timeline__arrow" d="M0,0 L6,3 L0,6 Z"/></marker>
                <marker id={`${markerId}-arrow-conflict`} markerHeight="6" markerWidth="6" orient="auto-start-reverse" refX="5" refY="3" viewBox="0 0 6 6"><path className="lp-project-timeline__arrow is-conflict" d="M0,0 L6,3 L0,6 Z"/></marker>
              </defs>
              {paths.map(path => <path className={`lp-project-timeline__link${path.conflict ? ' is-conflict' : ''}${activeIds.has(path.blockerId) || activeIds.has(path.blockedId) ? ' is-active' : ''}`} d={path.d} data-blocked={path.blockedId} data-blocker={path.blockerId} key={`${path.blockerId}>${path.blockedId}`} markerEnd={`url(#${markerId}-arrow${path.conflict ? '-conflict' : ''})`}/>)}
              {drag?.kind === 'link' && <path className="lp-project-timeline__link is-draft" d={`M${drag.from.x},${drag.from.y} L${drag.pointer.x},${drag.pointer.y}`}/>}
            </svg>
            <div aria-hidden="true" className="lp-project-timeline__today-line" style={{ left: TIMELINE_LABEL_WIDTH + todayX }}/>
          </div>
        </div>
      </div>
    </div>
  </Tooltip.Provider>

  function previewSpanFor(current: Extract<Drag, { kind: 'bar' }>) {
    const { span, delta, mode } = current
    if (mode === 'move') return { ...span, start: addTimelineDays(span.start, delta), end: addTimelineDays(span.end, delta) }
    if (mode === 'resize-start') { const start = addTimelineDays(span.start, delta); return { ...span, start: start > span.end ? span.end : start } }
    const end = addTimelineDays(span.end, delta)
    return { ...span, end: end < span.start ? span.start : end }
  }
}

function projectIdAtPoint(x: number, y: number) {
  if (typeof document.elementsFromPoint !== 'function') return undefined
  for (const element of document.elementsFromPoint(x, y)) {
    const id = (element as HTMLElement).closest?.<HTMLElement>('[data-timeline-project]')?.dataset.timelineProject
    if (id) return id
  }
  return undefined
}

type RowProps = {
  project: ProjectPageItem
  span?: TimelineSpan
  box?: TimelineRowBox
  y: number
  range: TimelineRange
  dragging: boolean
  draggingMilestoneId?: string
  milestoneDelta: number
  linkTarget: boolean
  canLink: boolean
  canEdit: boolean
  createGhost?: { left: number; width: number }
  consumeSuppressedClick: () => boolean
  onHover: (id: string | undefined) => void
  onNudge: (project: ProjectPageItem, span: TimelineSpan, days: number) => void
  onOpen?: (project: ProjectPageItem) => void
  onOpenMilestone?: (project: ProjectPageItem, milestoneId: string) => void
  onStartBarDrag: (event: PointerEvent<HTMLElement>, project: ProjectPageItem, span: TimelineSpan, mode: 'move' | 'resize-start' | 'resize-end') => void
  onStartMilestoneDrag: (event: PointerEvent<HTMLElement>, project: ProjectPageItem, marker: TimelineMilestoneMarker) => void
  onStartCreate: (event: PointerEvent<HTMLElement>, project: ProjectPageItem) => void
  onStartLink: (event: PointerEvent<HTMLElement>, project: ProjectPageItem, side: 'start' | 'end') => void
}

const TimelineProjectRow = memo(function TimelineProjectRow({ project, span, box, y, range, dragging, draggingMilestoneId, milestoneDelta, linkTarget, canLink, canEdit, createGhost, consumeSuppressedClick, onHover, onNudge, onOpen, onOpenMilestone, onStartBarDrag, onStartMilestoneDrag, onStartCreate, onStartLink }: RowProps) {
  const markers = useMemo(() => timelineMilestoneMarkers(range, project.milestones), [project.milestones, range])
  const open = () => { if (!consumeSuppressedClick()) onOpen?.(project) }
  return <div className="lp-project-timeline__row" data-link-target={linkTarget || undefined} data-timeline-project={project.id} onPointerEnter={() => onHover(project.id)} onPointerLeave={() => onHover(undefined)} style={{ top: y, height: ROW_HEIGHT }}>
    <div className="lp-project-timeline__label">
      <button aria-label={`Open ${project.name}`} onClick={() => onOpen?.(project)} type="button"><ViewGlyph color={project.color ?? 'var(--theme-text-tertiary)'} icon={project.icon || 'Project'}/><span data-i18n-ignore>{project.name}</span></button>
    </div>
    <div className="lp-project-timeline__track">
      {!span || !box
        ? <button aria-label={`Add dates for ${project.name}`} className="lp-project-timeline__add-dates" onClick={open} onPointerDown={event => onStartCreate(event, project)} title={canEdit ? 'Drag to set start and target dates' : undefined} type="button">
          <em>Add dates</em>
          {createGhost && <span aria-hidden="true" className="lp-project-timeline__ghost" style={{ left: createGhost.left, width: createGhost.width }}/>}
        </button>
        : <div className="lp-project-timeline__bar" data-dragging={dragging || undefined} data-open-start={!span.hasStart || undefined} data-open-end={!span.hasTarget || undefined} style={{ left: box.left, width: box.width, '--timeline-progress': `${Math.max(0, Math.min(100, project.progress))}%` } as CSSProperties}>
          <button aria-label={`${project.name} timeline bar`} onClick={open} onKeyDown={event => { if (!canEdit || !['ArrowLeft', 'ArrowRight'].includes(event.key)) return; event.preventDefault(); onNudge(project, span, event.key === 'ArrowLeft' ? -1 : 1) }} onPointerDown={event => onStartBarDrag(event, project, span, 'move')} type="button">
            <span data-i18n-ignore>{project.name}</span>
          </button>
          {canEdit && <>
            <i aria-hidden="true" className="lp-project-timeline__handle is-start" onPointerDown={event => onStartBarDrag(event, project, span, 'resize-start')}/>
            <i aria-hidden="true" className="lp-project-timeline__handle is-end" onPointerDown={event => onStartBarDrag(event, project, span, 'resize-end')}/>
          </>}
          {canLink && <>
            <i aria-hidden="true" className="lp-project-timeline__link-handle is-start" onPointerDown={event => onStartLink(event, project, 'start')} title="Drag to a project that blocks this one"/>
            <i aria-hidden="true" className="lp-project-timeline__link-handle is-end" onPointerDown={event => onStartLink(event, project, 'end')} title="Drag to a project blocked by this one"/>
          </>}
        </div>}
      {markers.map(marker => {
        const moving = draggingMilestoneId === marker.id
        const x = marker.x + (moving ? milestoneDelta * range.pxPerDay : 0)
        const date = moving ? addTimelineDays(marker.date, milestoneDelta) : marker.date
        return <Tooltip.Root key={marker.id}>
          <Tooltip.Trigger asChild>
            <button aria-label={`Milestone ${marker.name}`} className="lp-project-timeline__milestone" data-completed={marker.completed || undefined} data-dragging={moving || undefined} onClick={() => { if (consumeSuppressedClick()) return; if (onOpenMilestone) onOpenMilestone(project, marker.id); else onOpen?.(project) }} onPointerDown={event => onStartMilestoneDrag(event, project, marker)} style={{ left: x }} type="button">
              <svg aria-hidden="true" viewBox="0 0 16 16"><path d={MILESTONE_SHAPE_PATH}/></svg>
            </button>
          </Tooltip.Trigger>
          <Tooltip.Portal>
            <Tooltip.Content className="lp-project-timeline__tooltip" data-flow-motion="tooltip" side="top" sideOffset={6}>
              <strong data-i18n-ignore>{marker.name}</strong>
              <span>{date.toLocaleDateString('en', { month: 'short', day: 'numeric', year: 'numeric' })}</span>
              <span><span>Progress</span> <span data-i18n-ignore>{`${marker.progress}%`}</span></span>
            </Tooltip.Content>
          </Tooltip.Portal>
        </Tooltip.Root>
      })}
    </div>
  </div>
})
