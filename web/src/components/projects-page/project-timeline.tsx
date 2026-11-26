import * as Tooltip from '@radix-ui/react-tooltip'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown } from 'lucide-react'
import { useDesktopSettings } from '@/hooks/use-desktop-settings'
import { useI18n } from '@/i18n/i18n'
import { memo, useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type FocusEvent, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { useCurrentTime } from '@/hooks/use-current-time'
import { useLatestRef } from '@/hooks/use-latest-ref'
import { useTimelineViewport } from '@/hooks/use-timeline-viewport'
import { MILESTONE_SHAPE_PATH } from '@/components/issue/milestone-progress'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import type { ProjectDataGroup, ProjectPageItem } from './projects-data-view'
import {
  addTimelineDays,
  daysBetween,
  desktopTimelineHeader,
  indexTimelineDependencies,
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
  timelineDateBounds,
  timelineDatedMilestones,
  timelineHeader,
  timelineLowerBound,
  timelineMilestoneMarker,
  timelineMilestoneX,
  timelineRange,
  timelineWindowDependencies,
  timelineX,
  type TimelineAxisWindow,
  type TimelineDatedMilestone,
  type TimelineMilestoneMarker,
  type TimelineRange,
  type TimelineRowBox,
  type TimelineSpan,
  type TimelineZoom,
} from './project-timeline-model'
import './project-timeline.css'
import './project-timeline-desktop.css'
import { ScaleP19TimelineContext } from './scale-p19-view'

export const TIMELINE_LABEL_WIDTH = 240
const ROW_HEIGHT = 36
const GROUP_HEIGHT = 32
const DRAG_THRESHOLD = 3
const OVERSCAN = 256
const milestoneDateFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' })

type DateInput = { startDate?: string; targetDate?: string }

export type ProjectTimelineProps = {
  groups: ProjectDataGroup[]
  grouping?: string
  zoom?: TimelineZoom
  onZoomChange?: (zoom: TimelineZoom) => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenMilestone?: (project: ProjectPageItem, milestoneId: string) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onUpdateProject?: (projectId: string, input: DateInput) => Promise<unknown>
  onUpdateMilestone?: (projectId: string, milestoneId: string, input: { targetDate: string }) => Promise<unknown>
  onCreateDependency?: (blockerId: string, blockedId: string) => Promise<unknown>
  /** Desktop only: the property buttons (health, status, priority, lead) shown after each project's name. */
  renderCells?: (project: ProjectPageItem) => ReactNode
  renderGroupIcon?: (group: ProjectDataGroup) => ReactNode
  /** A group's title (a label's "Group › Label", translated generated names); its plain name otherwise. */
  renderGroupName?: (group: ProjectDataGroup) => ReactNode
  groupCount?: (group: ProjectDataGroup) => number
  /** Injectable for tests. */
  today?: Date
}

type Drag = { rowKey?: string } & (
  | { kind: 'bar'; id: string; mode: 'move' | 'resize-start' | 'resize-end'; originX: number; span: TimelineSpan; delta: number; moved: boolean }
  | { kind: 'milestone'; projectId: string; milestoneId: string; originX: number; date: Date; delta: number; moved: boolean }
  | { kind: 'create'; id: string; originX: number; currentX: number; moved: boolean }
  | { kind: 'link'; id: string; side: 'start' | 'end'; from: { x: number; y: number }; pointer: { x: number; y: number }; targetId?: string }
)

type Row =
  | { kind: 'group'; key: string; y: number; group: ProjectDataGroup }
  | { kind: 'project'; key: string; y: number; project: ProjectPageItem; span?: TimelineSpan; milestones: TimelineDatedMilestone[] }

export function ProjectTimeline({ groups, grouping, zoom: zoomProp, onZoomChange, onOpenProject, onOpenMilestone, onProjectVisualChange, onUpdateProject, onUpdateMilestone, onCreateDependency, renderCells, renderGroupIcon, renderGroupName, groupCount, today: todayProp }: ProjectTimelineProps) {
  const globalDates = useContext(ScaleP19TimelineContext)
  const desktop = useDesktopSettings()
  const { t, locale } = useI18n()
  // On desktop the time axis spans the pane; project labels overlay its left edge.
  const TIMELINE_LABEL_WIDTH = desktop ? 0 : 240
  const ROW_HEIGHT = desktop ? 72 : 36
  const [localZoom, setLocalZoom] = useState<TimelineZoom>(() => normalizeTimelineZoom(zoomProp))
  const zoom = zoomProp ? normalizeTimelineZoom(zoomProp) : localZoom
  const [drag, setDragState] = useState<Drag>()
  // Mirror of `drag` so pointer handlers see the latest gesture even before React re-renders (fast drags).
  const dragRef = useRef<Drag>(undefined)
  const setDrag = (next: Drag | undefined) => { dragRef.current = next; setDragState(next) }
  const [hoveredId, setHoveredId] = useState<string>()
  const [focus, setFocus] = useState<{ rowKey: string; milestoneId?: string }>()
  const suppressClick = useRef(false)
  const scrollerRef = useRef<HTMLDivElement>(null)
  const bodyRef = useRef<HTMLDivElement>(null)
  const { viewport, measure } = useTimelineViewport(scrollerRef)
  const markerId = useId().replace(/:/g, '')
  // The timeline only cares which day it is, so a coarse clock keeps it from re-rendering every minute.
  const clock = useCurrentTime(15 * 60_000)
  const todayKey = isoTimelineDay(todayProp ?? clock)
  const today = useMemo(() => parseTimelineDay(todayKey)!, [todayKey])
  const todayLabel = useMemo(() => today.toLocaleDateString(desktop ? locale : 'en', { month: 'short', day: 'numeric' }), [desktop, locale, today])

  const rows = useMemo(() => {
    const flat = groups.flatMap(group => group.subgroups?.length ? group.subgroups.map(subgroup => ({ ...subgroup, name: `${group.name} / ${subgroup.name}` })) : [group])
    const output: Row[] = []
    const byKey = new Map<string, number>()
    const projectRows: number[] = []
    const projectOrder = new Map<string, number>()
    let y = 0
    for (const group of flat) {
      // An explicit "No grouping" is a flat list on desktop; every other grouping keeps its header rows.
      if (!desktop || grouping !== 'No grouping') {
        output.push({ kind: 'group', key: `group:${group.id}`, y, group })
        y += GROUP_HEIGHT
      }
      for (const project of group.projects) {
        const key = `project:${group.id}:${project.id}`
        byKey.set(key, output.length)
        projectOrder.set(key, projectRows.length)
        projectRows.push(output.length)
        output.push({ kind: 'project', key, y, project, span: projectTimelineSpan(project.rawStartDate, project.rawTargetDate), milestones: timelineDatedMilestones(project.milestones) })
        y += ROW_HEIGHT
      }
    }
    return { rows: output, height: y, byKey, projectRows, projectOrder }
  }, [groups, desktop, grouping, ROW_HEIGHT])

  const dateBounds = useMemo(() => timelineDateBounds((function* () {
    for (const value of globalDates ?? []) {
      const date = parseTimelineDay(value)
      if (date) yield date
    }
    for (const row of rows.rows) {
      if (row.kind !== 'project') continue
      if (row.span) { yield row.span.start; yield row.span.end }
      if (row.milestones.length) {
        yield row.milestones[0].date
        yield row.milestones[row.milestones.length - 1].date
      }
    }
  })(), today), [rows, today, globalDates])
  const range = useMemo(() => {
    const range = timelineRange(dateBounds, today, zoom)
    return desktop && zoom === 'year' ? { ...range, pxPerDay: 2.5, width: range.days * 2.5 } : range
  }, [dateBounds, today, zoom, desktop])
  // Coordinates stay on the full canvas, so sticky labels and scroll positions keep their geometry.
  const axis = useMemo(() => ({
    left: Math.max(0, viewport.left - OVERSCAN),
    right: Math.min(range.width, viewport.left + viewport.width - TIMELINE_LABEL_WIDTH + 2 * OVERSCAN),
  }), [viewport.left, viewport.width, TIMELINE_LABEL_WIDTH, range.width])
  const visible = useMemo(() => ({
    ...axis,
    top: Math.max(0, viewport.top - OVERSCAN),
    bottom: viewport.top + viewport.height + 2 * OVERSCAN,
  }), [axis, viewport.top, viewport.height])
  const header = useMemo(() => desktop ? desktopTimelineHeader(range, locale, axis) : timelineHeader(range, axis), [range, desktop, locale, axis])
  const gridLines = useMemo(() => desktop ? header.major : zoom === 'week' ? header.minor.filter(segment => segment.emphasis) : header.minor, [header, zoom, desktop])
  const todayX = timelineX(range, today) + range.pxPerDay / 2

  const baseBoxes = useMemo(() => {
    const map = new Map<string, TimelineRowBox>()
    for (const row of rows.rows) {
      if (row.kind !== 'project' || !row.span || map.has(row.project.id)) continue
      map.set(row.project.id, { ...timelineBarBox(range, row.span), y: row.y + ROW_HEIGHT / 2, start: row.span.start, end: row.span.end })
    }
    return map
  }, [range, rows, ROW_HEIGHT])
  const movedBox = useMemo(() => {
    if (drag?.kind !== 'bar' || !drag.delta) return undefined
    const current = baseBoxes.get(drag.id)
    if (!current) return undefined
    const span = previewSpanFor(drag)
    return { id: drag.id, box: { ...timelineBarBox(range, span), y: current.y, start: span.start, end: span.end } }
  }, [baseBoxes, drag, range])

  const dependencyIndex = useMemo(() => indexTimelineDependencies(rows.rows.flatMap(row => row.kind === 'project'
    ? (row.project.blockedByIds ?? []).map(blockerId => ({ blockerId, blockedId: row.project.id })) : []), baseBoxes), [rows, baseBoxes])
  const paths = useMemo(() => timelineWindowDependencies(dependencyIndex, visible, movedBox), [dependencyIndex, visible, movedBox])
  const pinnedDragRow = drag?.rowKey
  const focusedRowKey = focus?.rowKey
  const visibleRows = useMemo(() => {
    const first = timelineLowerBound(rows.rows, row => row.y >= visible.top) - 1
    const end = timelineLowerBound(rows.rows, row => row.y >= visible.bottom)
    const start = Math.max(0, first)
    const output = rows.rows.slice(start, end)
    const pinned = new Set([pinnedDragRow && rows.byKey.get(pinnedDragRow)])
    const focusedOrder = focusedRowKey === undefined ? undefined : rows.projectOrder.get(focusedRowKey)
    // With a row focused, Tab can still enter its neighbours after a wheel scroll.
    if (focusedOrder !== undefined) {
      for (const offset of [-1, 0, 1]) pinned.add(rows.projectRows[focusedOrder + offset])
    }
    for (const index of pinned) {
      if (typeof index === 'number' && (index < start || index >= end)) output.push(rows.rows[index])
    }
    return output.sort((a, b) => a.y - b.y)
  }, [rows, visible.top, visible.bottom, pinnedDragRow, focusedRowKey])

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
    measure()
  }, [range, TIMELINE_LABEL_WIDTH, measure])

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
  // The view toolbar's Today button asks this timeline to centre on today.
  const scrollToTodayRef = useRef(scrollToToday)
  useEffect(() => { scrollToTodayRef.current = scrollToToday })
  useEffect(() => {
    const onToday = () => scrollToTodayRef.current()
    window.addEventListener('flow:timeline-today', onToday)
    return () => window.removeEventListener('flow:timeline-today', onToday)
  }, [])

  const onRootKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || (event.target as HTMLElement).closest('input,textarea,[contenteditable=true]')) return
    if (event.key === '+' || event.key === '=') { event.preventDefault(); changeZoom(stepTimelineZoom(zoom, -1)) }
    else if (event.key === '-' || event.key === '_') { event.preventDefault(); changeZoom(stepTimelineZoom(zoom, 1)) }
  }

  const pointInTrack = (event: { clientX: number; clientY: number }) => {
    const rect = bodyRef.current?.getBoundingClientRect()
    return { x: event.clientX - (rect?.left ?? 0) - TIMELINE_LABEL_WIDTH, y: event.clientY - (rect?.top ?? 0) }
  }
  const capture = (event: PointerEvent<HTMLElement>) => {
    event.currentTarget.setPointerCapture?.(event.pointerId)
    return event.currentTarget.closest<HTMLElement>('[data-timeline-row]')?.dataset.timelineRow
  }

  const startBarDrag = (event: PointerEvent<HTMLElement>, project: ProjectPageItem, span: TimelineSpan, mode: 'move' | 'resize-start' | 'resize-end') => {
    if (!onUpdateProject || event.button !== 0) return
    event.preventDefault(); event.stopPropagation()
    setDrag({ kind: 'bar', rowKey: capture(event), id: project.id, mode, originX: event.clientX, span, delta: 0, moved: false })
  }
  const startMilestoneDrag = (event: PointerEvent<HTMLElement>, project: ProjectPageItem, marker: TimelineMilestoneMarker) => {
    if (event.button !== 0) return
    event.stopPropagation()
    if (!onUpdateMilestone) return
    event.preventDefault()
    setDrag({ kind: 'milestone', rowKey: capture(event), projectId: project.id, milestoneId: marker.id, originX: event.clientX, date: marker.date, delta: 0, moved: false })
  }
  const startCreate = (event: PointerEvent<HTMLElement>, project: ProjectPageItem) => {
    if (!onUpdateProject || event.button !== 0) return
    event.preventDefault()
    const rowKey = capture(event)
    const { x } = pointInTrack(event)
    setDrag({ kind: 'create', rowKey, id: project.id, originX: x, currentX: x, moved: false })
  }
  const startLink = (event: PointerEvent<HTMLElement>, project: ProjectPageItem, side: 'start' | 'end') => {
    const box = baseBoxes.get(project.id)
    if (!onCreateDependency || !box || event.button !== 0) return
    event.preventDefault(); event.stopPropagation()
    const from = { x: side === 'end' ? box.left + box.width : box.left, y: box.y }
    setDrag({ kind: 'link', rowKey: capture(event), id: project.id, side, from, pointer: from })
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
  const applyPointerMove = (point: { clientX: number; clientY: number }) => {
    const current = dragRef.current
    if (!current) return
    const next = advance(current, point)
    if (next.kind === 'bar' && current.kind === 'bar' && next.delta === current.delta && next.moved === current.moved) return
    if (next.kind === 'milestone' && current.kind === 'milestone' && next.delta === current.delta && next.moved === current.moved) return
    setDrag(next)
  }
  const pointerFrame = useRef(0)
  const latestMove = useLatestRef(applyPointerMove)
  const pointer = useRef({ clientX: 0, clientY: 0 })
  const onPointerMove = ({ clientX, clientY }: PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current) return
    pointer.current = { clientX, clientY }
    if (!pointerFrame.current) pointerFrame.current = requestAnimationFrame(() => {
      pointerFrame.current = 0
      latestMove.current(pointer.current)
    })
  }
  const cancelPointerMove = () => {
    cancelAnimationFrame(pointerFrame.current)
    pointerFrame.current = 0
  }
  const onPointerUp = (event: PointerEvent<HTMLDivElement>) => {
    const started = dragRef.current
    if (!started) return
    cancelPointerMove()
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
      if (dependencyIndex.pairs.has(`${blockerId}>${blockedId}`) || dependencyIndex.pairs.has(`${blockedId}>${blockerId}`)) return
      void onCreateDependency(blockerId, blockedId)
    }
  }
  const onPointerCancel = () => { cancelPointerMove(); setDrag(undefined) }
  const consumeSuppressedClick = () => { if (!suppressClick.current) return false; suppressClick.current = false; return true }

  const nudge = (project: ProjectPageItem, span: TimelineSpan, days: number) => {
    if (!onUpdateProject) return
    void onUpdateProject(project.id, { ...(span.hasStart ? { startDate: isoTimelineDay(addTimelineDays(span.start, days)) } : {}), ...(span.hasTarget ? { targetDate: isoTimelineDay(addTimelineDays(span.end, days)) } : {}) })
  }

  const latest = useLatestRef({ startBarDrag, startMilestoneDrag, startCreate, startLink, nudge, consumeSuppressedClick })
  const stable = useMemo(() => ({
    startBarDrag: (...args: Parameters<typeof startBarDrag>) => latest.current.startBarDrag(...args),
    startMilestoneDrag: (...args: Parameters<typeof startMilestoneDrag>) => latest.current.startMilestoneDrag(...args),
    startCreate: (...args: Parameters<typeof startCreate>) => latest.current.startCreate(...args),
    startLink: (...args: Parameters<typeof startLink>) => latest.current.startLink(...args),
    nudge: (...args: Parameters<typeof nudge>) => latest.current.nudge(...args),
    consumeSuppressedClick: () => latest.current.consumeSuppressedClick(),
  }), [latest])

  const hoverFrame = useRef(0)
  const pendingHover = useRef<string>(undefined)
  const onHover = useCallback((id: string | undefined) => {
    pendingHover.current = id
    if (!hoverFrame.current) hoverFrame.current = requestAnimationFrame(() => {
      hoverFrame.current = 0
      setHoveredId(pendingHover.current)
    })
  }, [])
  useEffect(() => () => {
    cancelAnimationFrame(hoverFrame.current)
    cancelAnimationFrame(pointerFrame.current)
  }, [])
  const pinControl = (event: FocusEvent<HTMLDivElement> | PointerEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement
    const rowKey = target.closest<HTMLElement>('[data-timeline-row]')?.dataset.timelineRow
    if (rowKey) {
      const milestoneId = target.closest<HTMLElement>('[data-timeline-milestone]')?.dataset.timelineMilestone
      setFocus(current => current?.rowKey === rowKey && current.milestoneId === milestoneId ? current : { rowKey, milestoneId })
    }
  }
  const linkingActive = drag?.kind === 'link'
  const activeDragId = drag?.kind === 'bar' ? drag.id : undefined
  const createGhost = drag?.kind === 'create' && drag.moved ? { left: Math.min(drag.originX, drag.currentX), width: Math.abs(drag.currentX - drag.originX) } : undefined
  const linkWidth = Math.max(1, axis.right - axis.left)
  const linkHeight = Math.max(1, Math.min(Math.max(rows.height, ROW_HEIGHT), visible.bottom) - visible.top)

  return <Tooltip.Provider delayDuration={200} skipDelayDuration={150}>
    <div aria-label="Project timeline" className="lp-project-timeline" data-zoom={zoom} onFocus={pinControl} onKeyDown={onRootKeyDown} onPointerDownCapture={pinControl} role="region">
      <div className="lp-project-timeline__toolbar">
        {desktop && <>
          <button aria-label={t('Center timeline on today')} className="lp-project-timeline__today" onClick={scrollToToday} type="button">{t('Today')}</button>
          <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Timeline zoom')} className="lp-project-timeline__zoom-menu" type="button">{t(TIMELINE_ZOOM_LABELS[zoom])}<ChevronDown size={12}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="lp-project-timeline__zoom-options" data-flow-motion="floating" align="end" sideOffset={4}>
            <DropdownMenu.RadioGroup value={zoom} onValueChange={value => { if (TIMELINE_ZOOMS.includes(value as TimelineZoom)) changeZoom(value as TimelineZoom) }}>
              {TIMELINE_ZOOMS.map(option => <DropdownMenu.RadioItem key={option} value={option}>{t(TIMELINE_ZOOM_LABELS[option])}</DropdownMenu.RadioItem>)}
            </DropdownMenu.RadioGroup>
          </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        </>}
        {!desktop && <>
        <div aria-label="Timeline zoom" className="lp-project-timeline__zoom" role="radiogroup" title="Zoom in with + and out with −">
          {TIMELINE_ZOOMS.map(option => <button aria-checked={zoom === option} className="lp-project-timeline__zoom-option" key={option} onClick={() => changeZoom(option)} role="radio" type="button">{TIMELINE_ZOOM_LABELS[option]}</button>)}
        </div>
        <button aria-label="Center timeline on today" className="lp-project-timeline__today" onClick={scrollToToday} type="button">Today</button>
        </>}
      </div>
      <div className="lp-project-timeline__scroller" ref={scrollerRef}>
        <div className="lp-project-timeline__canvas" style={{ width: TIMELINE_LABEL_WIDTH + range.width }}>
          <div className="lp-project-timeline__header">
            <div className="lp-project-timeline__corner">{!desktop && 'Projects'}</div>
            <div className="lp-project-timeline__scale" style={{ width: range.width }}>
              {desktop && <div aria-hidden="true" className="lp-project-timeline__header-grid">{gridLines.map(segment => <i key={segment.key} style={{ left: segment.x }}/>)}</div>}
              {/* Desktop ticks and labels are anchored on their day (the stylesheet centres day numbers on it); the phone layout fills each span. */}
              <div className="lp-project-timeline__major">{header.major.map(segment => <span key={segment.key} style={desktop ? { left: segment.x } : { left: segment.x, width: segment.width }}><b data-i18n-ignore>{segment.label}</b></span>)}</div>
              <div className="lp-project-timeline__minor">{header.minor.map(segment => <span data-emphasis={segment.emphasis || undefined} key={segment.key} style={desktop ? { left: segment.x } : { left: segment.x, width: segment.width }}>{desktop ? segment.label && <b data-i18n-ignore>{segment.label}</b> : segment.width >= 14 ? segment.label : ''}</span>)}</div>
              {desktop && <i aria-hidden="true" className="lp-project-timeline__today-header-line" style={{ left: todayX }}/>}
              <span className="lp-project-timeline__today-chip" style={{ left: todayX }}>{todayLabel}</span>
            </div>
          </div>
          <div className="lp-project-timeline__body" data-linking={linkingActive || undefined} onPointerCancel={onPointerCancel} onPointerMove={onPointerMove} onPointerUp={onPointerUp} ref={bodyRef} style={{ height: Math.max(rows.height, ROW_HEIGHT) }}>
            {desktop && <div aria-hidden="true" className="lp-project-timeline__names-backdrop"/>}
            <div aria-hidden="true" className="lp-project-timeline__grid" style={{ width: range.width }}>{gridLines.map(segment => <i data-emphasis={segment.emphasis || undefined} key={segment.key} style={{ left: segment.x }}/>)}</div>
            {visibleRows.map(row => row.kind === 'group'
              ? <div className="lp-project-timeline__group" key={row.key} style={{ top: row.y, height: GROUP_HEIGHT }}>
                <h2>{renderGroupIcon?.(row.group)}{renderGroupName ? <span className="lp-project-timeline__group-name">{renderGroupName(row.group)}</span> : <span data-i18n-ignore>{row.group.name}</span>}<small>{groupCount ? groupCount(row.group) : row.group.projects.length}</small></h2>
              </div>
              : <TimelineProjectRow
                axis={axis}
                box={movedBox?.id === row.project.id ? movedBox.box : baseBoxes.get(row.project.id)}
                consumeSuppressedClick={stable.consumeSuppressedClick}
                createGhost={drag?.kind === 'create' && drag.id === row.project.id ? createGhost : undefined}
                dragging={drag?.kind === 'bar' && drag.id === row.project.id}
                draggingMilestoneId={drag?.kind === 'milestone' && drag.projectId === row.project.id ? drag.milestoneId : undefined}
                focusedMilestoneId={focus?.rowKey === row.key ? focus.milestoneId : undefined}
                milestoneDelta={drag?.kind === 'milestone' && drag.projectId === row.project.id ? drag.delta : 0}
                milestones={row.milestones}
                key={row.key}
                linkTarget={drag?.kind === 'link' && drag.targetId === row.project.id}
                canLink={Boolean(onCreateDependency)}
                canEdit={Boolean(onUpdateProject)}
                onHover={onHover}
                onNudge={stable.nudge}
                onOpen={onOpenProject}
                onOpenMilestone={onOpenMilestone}
                onProjectVisualChange={onProjectVisualChange}
                onStartBarDrag={stable.startBarDrag}
                onStartCreate={stable.startCreate}
                onStartLink={stable.startLink}
                onStartMilestoneDrag={stable.startMilestoneDrag}
                project={row.project}
                range={range}
                renderCells={renderCells}
                rowKey={row.key}
                span={row.span}
                y={row.y}
              />)}
            <svg aria-hidden="true" className="lp-project-timeline__links" height={linkHeight} width={linkWidth}
              style={{ left: TIMELINE_LABEL_WIDTH + axis.left, top: visible.top }} viewBox={`${axis.left} ${visible.top} ${linkWidth} ${linkHeight}`}>
              <defs>
                <marker id={`${markerId}-arrow`} markerHeight="6" markerWidth="6" orient="auto-start-reverse" refX="5" refY="3" viewBox="0 0 6 6"><path className="lp-project-timeline__arrow" d="M0,0 L6,3 L0,6 Z"/></marker>
                <marker id={`${markerId}-arrow-conflict`} markerHeight="6" markerWidth="6" orient="auto-start-reverse" refX="5" refY="3" viewBox="0 0 6 6"><path className="lp-project-timeline__arrow is-conflict" d="M0,0 L6,3 L0,6 Z"/></marker>
              </defs>
              {paths.map(path => <path className={`lp-project-timeline__link${path.conflict ? ' is-conflict' : ''}${path.blockerId === hoveredId || path.blockedId === hoveredId || path.blockerId === activeDragId || path.blockedId === activeDragId ? ' is-active' : ''}`} d={path.d} data-blocked={path.blockedId} data-blocker={path.blockerId} key={`${path.blockerId}>${path.blockedId}`} markerEnd={`url(#${markerId}-arrow${path.conflict ? '-conflict' : ''})`}/>)}
              {drag?.kind === 'link' && <path className="lp-project-timeline__link is-draft" d={`M${drag.from.x},${drag.from.y} L${drag.pointer.x},${drag.pointer.y}`}/>}
            </svg>
            <div aria-hidden="true" className="lp-project-timeline__today-line" style={{ left: TIMELINE_LABEL_WIDTH + todayX }}/>
          </div>
        </div>
      </div>
    </div>
  </Tooltip.Provider>

}

function previewSpanFor({ span, delta, mode }: Extract<Drag, { kind: 'bar' }>) {
  if (mode === 'move') return { ...span, start: addTimelineDays(span.start, delta), end: addTimelineDays(span.end, delta) }
  if (mode === 'resize-start') { const start = addTimelineDays(span.start, delta); return { ...span, start: start > span.end ? span.end : start } }
  const end = addTimelineDays(span.end, delta)
  return { ...span, end: end < span.start ? span.start : end }
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
  axis: TimelineAxisWindow
  project: ProjectPageItem
  rowKey: string
  span?: TimelineSpan
  box?: TimelineRowBox
  y: number
  range: TimelineRange
  renderCells?: (project: ProjectPageItem) => ReactNode
  dragging: boolean
  draggingMilestoneId?: string
  focusedMilestoneId?: string
  milestones: TimelineDatedMilestone[]
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
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onStartBarDrag: (event: PointerEvent<HTMLElement>, project: ProjectPageItem, span: TimelineSpan, mode: 'move' | 'resize-start' | 'resize-end') => void
  onStartMilestoneDrag: (event: PointerEvent<HTMLElement>, project: ProjectPageItem, marker: TimelineMilestoneMarker) => void
  onStartCreate: (event: PointerEvent<HTMLElement>, project: ProjectPageItem) => void
  onStartLink: (event: PointerEvent<HTMLElement>, project: ProjectPageItem, side: 'start' | 'end') => void
}

const TimelineProjectRow = memo(function TimelineProjectRow({ axis, project, rowKey, span, box, y, range, renderCells, dragging, draggingMilestoneId, focusedMilestoneId, milestones, milestoneDelta, linkTarget, canLink, canEdit, createGhost, consumeSuppressedClick, onHover, onNudge, onOpen, onOpenMilestone, onProjectVisualChange, onStartBarDrag, onStartMilestoneDrag, onStartCreate, onStartLink }: RowProps) {
  const desktop = useDesktopSettings()
  const markers = useMemo(() => {
    const start = timelineLowerBound(milestones, marker => timelineMilestoneX(range, marker.date) >= axis.left)
    const end = timelineLowerBound(milestones, marker => timelineMilestoneX(range, marker.date) > axis.right)
    const indexes = new Set<number>()
    for (let index = start; index < end; index++) indexes.add(index)
    // Keep entry points for Tab/Shift+Tab and the neighbours of the focused control.
    const focused = focusedMilestoneId ? milestones.findIndex(item => item.id === focusedMilestoneId) : -1
    const dragged = draggingMilestoneId ? milestones.findIndex(item => item.id === draggingMilestoneId) : -1
    for (const index of [0, milestones.length - 1, focused - 1, focused, focused + 1, dragged]) {
      if (index >= 0 && index < milestones.length) indexes.add(index)
    }
    return [...indexes].sort((a, b) => a - b).map(index => timelineMilestoneMarker(range, milestones[index]))
  }, [axis, draggingMilestoneId, focusedMilestoneId, milestones, range])
  const open = () => { if (!consumeSuppressedClick()) onOpen?.(project) }
  return <div className="lp-project-timeline__row" data-link-target={linkTarget || undefined} data-timeline-project={project.id} data-timeline-row={rowKey} onPointerEnter={() => onHover(project.id)} onPointerLeave={() => onHover(undefined)} style={{ top: y, height: desktop ? 72 : ROW_HEIGHT }}>
    <div className="lp-project-timeline__label">
      {desktop
        ? <div className="lp-project-timeline__name">
          <ViewIconPicker color={project.color} icon={project.icon || 'Project'} onChange={visual => onProjectVisualChange?.(project, visual.icon, visual.color)} surface="page" triggerClassName="lp-project-timeline__icon"/>
          <button aria-label={`Open ${project.name}`} onClick={() => onOpen?.(project)} type="button"><span data-i18n-ignore>{project.name}</span></button>
        </div>
        : <button aria-label={`Open ${project.name}`} onClick={() => onOpen?.(project)} type="button"><ViewGlyph color={project.color ?? 'var(--theme-text-tertiary)'} icon={project.icon || 'Project'}/><span data-i18n-ignore>{project.name}</span></button>}
      {desktop && renderCells && <div className="lp-project-timeline__cells">{renderCells(project)}</div>}
    </div>
    <div className="lp-project-timeline__track">
      {!span || !box
        ? <button aria-label={`Add dates for ${project.name}`} className="lp-project-timeline__add-dates" onClick={open} onPointerDown={event => onStartCreate(event, project)} title={canEdit ? 'Drag to set start and target dates' : undefined} type="button">
          <em>Add dates</em>
          {createGhost && <span aria-hidden="true" className="lp-project-timeline__ghost" style={{ left: createGhost.left, width: createGhost.width }}/>}
        </button>
        : <div className="lp-project-timeline__bar" data-dragging={dragging || undefined} data-open-start={!span.hasStart || undefined} data-open-end={!span.hasTarget || undefined} style={{ left: box.left, width: box.width, '--timeline-progress': `${Math.max(0, Math.min(100, project.progress))}%` } as CSSProperties}>
          {desktop && <span aria-hidden="true" className="lp-project-timeline__bar-label" data-i18n-ignore><span>{project.name}</span></span>}
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
            <button aria-label={`Milestone ${marker.name}`} className="lp-project-timeline__milestone" data-completed={marker.completed || undefined} data-dragging={moving || undefined} data-timeline-milestone={marker.id} onClick={() => { if (consumeSuppressedClick()) return; if (onOpenMilestone) onOpenMilestone(project, marker.id); else onOpen?.(project) }} onPointerDown={event => onStartMilestoneDrag(event, project, marker)} style={{ left: x }} type="button">
              <svg aria-hidden="true" viewBox="0 0 16 16"><path d={MILESTONE_SHAPE_PATH}/></svg>
            </button>
          </Tooltip.Trigger>
          <Tooltip.Portal>
            <Tooltip.Content className="lp-project-timeline__tooltip" data-flow-motion="tooltip" side="top" sideOffset={6}>
              <strong data-i18n-ignore>{marker.name}</strong>
              <span>{milestoneDateFormat.format(date)}</span>
              <span><span>Progress</span> <span data-i18n-ignore>{`${marker.progress}%`}</span></span>
            </Tooltip.Content>
          </Tooltip.Portal>
        </Tooltip.Root>
      })}
    </div>
  </div>
})
