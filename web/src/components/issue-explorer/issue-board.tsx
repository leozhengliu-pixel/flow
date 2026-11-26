import { IssueWidgetAdornments } from '@/components/issues-split-view'
import { useLabelColors } from '@/hooks/use-label-colors'
import { CustomerRevenueRowChip, CustomersRowChip } from '@/components/customer/customer-row-properties'
import { ProjectGlyph } from '@/components/views/project-glyph'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type MouseEvent, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Clock3, Link2, Plus } from 'lucide-react'
import { EstimateChip, EstimateIcon } from '@/components/issue/estimate-icon'
import { useIssueRowActions, useTeamEstimateSettings } from '@/components/my-issues/use-issue-row-actions'
import { formatEstimatePoints, sumEstimatePoints, teamUsesEstimates } from '@/lib/estimates'
import { IssueReleaseChip } from '@/components/issue/issue-release-chip'
import { Virtuoso } from 'react-virtuoso'
import { toast } from '@/lib/toast'
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuSeparator } from '@/components/ui/row-context-menu'
import { IssueAssigneeSlot } from '@/components/my-issues/issue-assignee-slot'
import { useTeamStatusOptions } from '@/components/my-issues/use-team-status-options'
import { GroupPathChevron, IssueContextMenu, MergedLabelBadge, IssueParentTrail, RowCommandPicker, SubIssueProgress, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData, type MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import type { MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { CalendarIcon, CycleIcon, PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { DueDatePicker } from '@/components/issue/due-date-picker'
import { IssueSLAIndicator } from '@/components/issue/issue-sla-indicator'
import { useI18n, useOptionalTranslate } from '@/i18n/i18n'
import styles from './issue-board.module.css'
import { labelChips, toggleGroupedLabelIds } from '@/lib/labels'
import { boardDropRejectReason } from './issue-board-drop'

const EMPTY_OPTIONS: MyIssuesRowPropertyOptions = { status: [], priority: [], assignee: [], dueDate: [], labels: [], project: [], cycle: [] }
const COLUMN_VIRTUALIZATION_THRESHOLD = 6
const CARD_VIRTUALIZATION_THRESHOLD = 24
const COLUMN_OVERSCAN = 360
const CARD_OVERSCAN = 240
const CARD_ESTIMATED_SIZE = 120

type IssueBoardProps = Parameters<typeof IssueBoardColumns>[0]

/**
 * Board entry point. With a sub-grouping ("Rows"), groups arrive as `column::row` cells from `buildIssueGroups`
 * (every column has a cell in every row) and render as one grid: see `IssueBoardRows`.
 */
export function IssueBoard(props: IssueBoardProps) {
  if (!props.groups.some(group => group.parentGroupId)) return <IssueBoardColumns {...props}/>
  return <IssueBoardRows {...props}/>
}

/** Drag-and-drop between a board's cards and columns (or row cells). */
function useBoardDrop(groups: MyIssuesGroupData[], canDrop: IssueBoardProps['canDrop'], onMove: IssueBoardProps['onMove']) {
  const { t } = useI18n()
  const [draggingId, setDraggingId] = useState<string>()
  const [over, setOver] = useState<{ groupId: string; index: number }>()
  const sourceGroup = draggingId ? groups.find(group => group.issues.some(issue => issue.id === draggingId)) : undefined
  const rejectMove = (issue: MyIssuesRowData, sourceId: string, targetId: string) => {
    const reason = canDrop?.(issue, sourceId, targetId)
      ?? boardDropRejectReason(issue, groups.find(group => group.id === sourceId), groups.find(group => group.id === targetId))
    if (reason) {
      toast.error(t(reason))
      return true
    }
    return false
  }
  const drop = (event: DragEvent, group: MyIssuesGroupData, index: number) => {
    event.preventDefault()
    const issue = sourceGroup?.issues.find(item => item.id === draggingId)
    if (issue && sourceGroup) {
      if (rejectMove(issue, sourceGroup.id, group.id)) {
        setDraggingId(undefined); setOver(undefined)
        return
      }
      const sourceIndex = sourceGroup.issues.findIndex(item => item.id === issue.id)
      const targetIndex = sourceGroup.id === group.id && sourceIndex < index ? index - 1 : index
      if (sourceGroup.id !== group.id || sourceIndex !== targetIndex) onMove(issue, sourceGroup.id, group.id, targetIndex)
    }
    setDraggingId(undefined); setOver(undefined)
  }
  return { draggingId, setDraggingId, over, setOver, drop }
}

/**
 * A board with rows, laid out like the design reference: one sticky header row with every column, each column's
 * panel running down behind the rows, and every row a sticky full-width header over its cells, one per column.
 * "Show empty columns" and hidden columns apply to whole columns; "Show empty rows" is applied by `buildIssueGroups`.
 */
function IssueBoardRows({ groups, hiddenGroupIds = [], showEmptyGroups = true, properties, propertyOptions = EMPTY_OPTIONS, selectedIds, createIssueLabel = 'Add new issue', canDrop, onCreateIssue, onHideGroup, onShowGroup, onMove, onOpenIssue, onPropertyChange, onSelectIssue }: IssueBoardProps) {
  const { t } = useI18n()
  const translate = useOptionalTranslate()
  const labelColors = useLabelColors()
  const estimateSettingsFor = useTeamEstimateSettings()
  const { columns, lanes } = useMemo(() => {
    const columns = new Map<string, MyIssuesGroupData & { ids: Set<string> }>()
    const lanes = new Map<string, { id: string; label: string; group: MyIssuesGroupData; order: number; cells: Map<string, MyIssuesGroupData> }>()
    for (const group of groups) {
      const columnId = group.parentGroupId ?? group.id
      const laneId = group.id.slice(columnId.length + 2)
      const column = columns.get(columnId) ?? { ...group.parentGroup, id: columnId, label: group.parentLabel ?? group.label, issues: [], ids: new Set<string>() }
      for (const issue of group.issues) if (!column.ids.has(issue.id)) { column.ids.add(issue.id); column.issues.push(issue) }
      columns.set(columnId, column)
      const lane = lanes.get(laneId) ?? { id: laneId, label: group.label, group, order: group.rowOrder ?? lanes.size, cells: new Map() }
      lane.cells.set(columnId, group)
      lanes.set(laneId, lane)
    }
    return { columns: [...columns.values()], lanes: [...lanes.values()].sort((left, right) => left.order - right.order) }
  }, [groups])
  // "Show empty columns" off hides columns without issues in any row; hidden columns wait in the side panel.
  const [revealed, setRevealed] = useState<{ setting: boolean; ids: string[] }>({ setting: showEmptyGroups, ids: [] })
  const revealedIds = revealed.setting === showEmptyGroups ? revealed.ids : []
  if (revealed.setting !== showEmptyGroups) setRevealed({ setting: showEmptyGroups, ids: [] })
  const isHidden = (column: MyIssuesGroupData) => hiddenGroupIds.includes(column.id) || (!showEmptyGroups && !column.issues.length && !revealedIds.includes(column.id))
  const visibleColumns = columns.filter(column => !isHidden(column))
  const hiddenColumns = columns.filter(isHidden)
  const { draggingId, setDraggingId, over, setOver, drop } = useBoardDrop(groups, canDrop, onMove)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const toggle = (id: string) => setCollapsed(current => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  // A row header spans the visible board (plus the reference's 4px), never the whole grid.
  const scrollerRef = useRef<HTMLDivElement>(null)
  const [viewportWidth, setViewportWidth] = useState<number>()
  useEffect(() => {
    const scroller = scrollerRef.current
    if (!scroller || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => setViewportWidth(scroller.clientWidth))
    observer.observe(scroller)
    return () => observer.disconnect()
  }, [])
  const columnEstimate = (group: MyIssuesGroupData) => properties.has('estimate') ? sumEstimatePoints(group.issues, estimateSettingsFor) : undefined
  // A row shows the estimate glyph before its count when its issues' teams use estimates (as group headers do).
  const laneIssues = (lane: (typeof lanes)[number]) => new Map([...lane.cells.values()].flatMap(cell => cell.issues.map(issue => [issue.id, issue] as const)))
  return <div ref={scrollerRef} className={styles.rowsBoard} role="list" aria-label={t('Issue board')} data-dragging={Boolean(draggingId)} style={{ '--rows-viewport': viewportWidth === undefined ? undefined : `${viewportWidth}px` } as CSSProperties}>
    <div className={styles.rowsGrid} style={{ '--rows-columns': visibleColumns.length } as CSSProperties}>
      <div className={styles.rowsHeader}>
        {visibleColumns.map(column => <div key={column.id} className={styles.rowsColumn} role="listitem" aria-label={column.label} data-board-column>
          <IssueBoardGroupHeader group={column} estimateSum={columnEstimate(column)} createIssueLabel={createIssueLabel} onCreateIssue={onCreateIssue} onHideGroup={onHideGroup} onSelectIssue={onSelectIssue}/>
        </div>)}
      </div>
      <div className={styles.rowsBands} aria-hidden="true">{visibleColumns.map(column => <div key={column.id} className={styles.rowsBand}/>)}</div>
      {lanes.map(lane => {
        const issues = laneIssues(lane)
        const estimate = properties.has('estimate') && [...issues.values()].some(issue => teamUsesEstimates(estimateSettingsFor(issue.teamId)))
        return <section key={lane.id} className={styles.row} aria-label={lane.label}>
          <header className={styles.rowHeader} data-board-row-header data-label-color={lane.group.color ? true : undefined} style={lane.group.color ? { '--group-label-color': labelColors.dot(lane.group.color), '--group-label-tint': labelColors.tint(lane.group.color, 'row'), '--group-label-lane-tint': labelColors.tint(lane.group.color, 'page') } as CSSProperties : undefined}>
            <div className={styles.rowTitle}>
              <span className={styles.rowCaretSlot}><button className={styles.rowCaret} type="button" aria-expanded={!collapsed.has(lane.id)} aria-label={t(collapsed.has(lane.id) ? 'Expand row' : 'Collapse row')} onClick={() => toggle(lane.id)}><svg width="18" height="18" viewBox="0 0 16 16" aria-hidden="true"><path d="M6.75194 10.6239C6.41861 10.8183 6 10.5779 6 10.192V5.80802C6 5.42212 6.41861 5.18169 6.75194 5.37613L10.5096 7.56811C10.8404 7.76105 10.8404 8.23895 10.5096 8.43189L6.75194 10.6239Z" fill="currentColor"/></svg></button></span>
              {lane.group.color && <span className={styles.rowDot} aria-hidden="true"/>}
              <span className={styles.rowName}>
                {lane.group.labelPrefix && <><span data-i18n-ignore className={styles.rowPrefix}>{lane.group.labelPrefix}</span><GroupPathChevron className={styles.rowPathChevron}/></>}
                <span data-i18n-ignore>{lane.group.translatableLabel ? translate(lane.label) : lane.label}</span>
              </span>
              <span className={styles.rowCount}>{estimate && <EstimateIcon className={styles.rowEstimate} size={14}/>}{issues.size}</span>
            </div>
            <div className={styles.rowActions}>
              <SwimlaneMenu
                collapsed={collapsed.has(lane.id)}
                onSelectAll={() => issues.forEach(issue => onSelectIssue(issue.id, true, false))}
                onToggle={() => toggle(lane.id)}
                onExpandAll={() => setCollapsed(new Set())}
                onCollapseAll={() => setCollapsed(new Set(lanes.map(item => item.id)))}
              />
            </div>
          </header>
          {!collapsed.has(lane.id) && <RowCells>
            {visibleColumns.map(column => <IssueBoardColumn
              key={column.id}
              cell
              group={lane.cells.get(column.id) ?? { id: `${column.id}::${lane.id}`, label: lane.label, issues: [], parentGroupId: column.id, createContext: column.createContext }}
              createIssueLabel={createIssueLabel}
              properties={properties}
              propertyOptions={propertyOptions}
              selectedIds={selectedIds}
              draggingId={draggingId}
              over={over}
              onCreateIssue={onCreateIssue}
              onSelectIssue={onSelectIssue}
              onDragStart={id => setDraggingId(id)}
              onDragEnd={() => { setDraggingId(undefined); setOver(undefined) }}
              onSetOver={setOver}
              onDrop={drop}
              onOpenIssue={onOpenIssue}
              onPropertyChange={onPropertyChange}
            />)}
          </RowCells>}
        </section>
      })}
    </div>
    <div className={styles.rowsHidden}><HiddenBoardColumns groups={hiddenColumns} onShowGroup={id => { setRevealed(current => ({ setting: showEmptyGroups, ids: [...current.ids, id] })); onShowGroup?.(id) }}/></div>
  </div>
}

/** A row's cells, in a box rounded up to a whole pixel (the design reference sizes its rows the same way). */
function RowCells({ children }: { children: ReactNode }) {
  const innerRef = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState<number>()
  useLayoutEffect(() => {
    const inner = innerRef.current
    if (!inner) return
    const update = () => setHeight(Math.ceil(inner.getBoundingClientRect().height))
    update()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(update)
    observer.observe(inner)
    return () => observer.disconnect()
  }, [])
  return <div className={styles.rowCellsBox} style={height ? { height } : undefined}><div ref={innerRef} className={styles.rowCells}>{children}</div></div>
}

/** The reference app swimlane menu: select the row's issues, and collapse / expand one or all rows. */
function SwimlaneMenu({ collapsed, onSelectAll, onToggle, onExpandAll, onCollapseAll }: { collapsed: boolean; onSelectAll: () => void; onToggle: () => void; onExpandAll: () => void; onCollapseAll: () => void }) {
  const { t } = useI18n()
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild><button className={styles.rowMenuButton} type="button" aria-label={t('Row options')}><BoardEllipsisIcon/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><FlowDropdownMenuContent label={t('Row options')} align="start" sideOffset={4}>
      <FlowMenuItem label="Select all in row" onSelect={onSelectAll}/>
      <FlowMenuSeparator/>
      <FlowMenuItem label={collapsed ? 'Expand row' : 'Collapse row'} onSelect={onToggle}/>
      <FlowMenuItem label="Expand all rows" onSelect={onExpandAll}/>
      <FlowMenuItem label="Collapse all rows" onSelect={onCollapseAll}/>
    </FlowDropdownMenuContent></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function IssueBoardColumns({ groups, hiddenGroupIds = [], showEmptyGroups = true, properties, propertyOptions = EMPTY_OPTIONS, selectedIds, createIssueLabel = 'Add new issue', canDrop, onCreateIssue, onHideGroup, onShowGroup, onMove, onOpenIssue, onPropertyChange, onSelectIssue }: {
  groups: MyIssuesGroupData[]
  hiddenGroupIds?: string[]
  showEmptyGroups?: boolean
  properties: ReadonlySet<MyIssuesProperty>
  propertyOptions?: MyIssuesRowPropertyOptions
  selectedIds: ReadonlySet<string>
  createIssueLabel?: string
  /** Optional override — return reject toast copy, or null to allow. */
  canDrop?: (issue: MyIssuesRowData, sourceGroupId: string, targetGroupId: string) => string | null
  onCreateIssue?: (group: MyIssuesGroupData) => void
  onHideGroup?: (groupId: string) => void
  onShowGroup?: (groupId: string) => void
  onMove: (issue: MyIssuesRowData, sourceGroupId: string, targetGroupId: string, targetIndex: number) => void
  onOpenIssue: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>
  onSelectIssue: (issueId: string, selected: boolean, range: boolean) => void
}) {
  const { t } = useI18n()
  const { draggingId, setDraggingId, over, setOver, drop } = useBoardDrop(groups, canDrop, onMove)
  const [revealed, setRevealed] = useState<{ setting: boolean; ids: string[] }>({ setting: showEmptyGroups, ids: [] })
  const revealedIds = revealed.setting === showEmptyGroups ? revealed.ids : []
  if (revealed.setting !== showEmptyGroups) setRevealed({ setting: showEmptyGroups, ids: [] })
  const isHidden = (group: MyIssuesGroupData) => hiddenGroupIds.includes(group.id) || (!showEmptyGroups && !(group.totalCount ?? group.issues.length) && !revealedIds.includes(group.id))
  const visibleGroups = groups.filter(group => !isHidden(group))
  const hiddenGroups = groups.filter(isHidden)

  const renderColumn = (group: MyIssuesGroupData) => (
    <IssueBoardColumn
      key={group.id}
      group={group}
      createIssueLabel={createIssueLabel}
      properties={properties}
      propertyOptions={propertyOptions}
      selectedIds={selectedIds}
      draggingId={draggingId}
      over={over}
      onCreateIssue={onCreateIssue}
      onHideGroup={onHideGroup}
      onSelectIssue={onSelectIssue}
      onDragStart={id => setDraggingId(id)}
      onDragEnd={() => { setDraggingId(undefined); setOver(undefined) }}
      onSetOver={setOver}
      onDrop={drop}
      onOpenIssue={onOpenIssue}
      onPropertyChange={onPropertyChange}
    />
  )

  const virtualizeColumns = visibleGroups.length > COLUMN_VIRTUALIZATION_THRESHOLD

  const hiddenPanel = <HiddenBoardColumns groups={hiddenGroups} onShowGroup={id => { setRevealed(current => ({ setting: showEmptyGroups, ids: [...current.ids, id] })); onShowGroup?.(id) }}/>
  return <div className={styles.board} role="list" aria-label={t('Issue board')} data-dragging={Boolean(draggingId)} data-virtualized-columns={virtualizeColumns || undefined}>
    {virtualizeColumns ? (
      <Virtuoso
        horizontalDirection
        className={styles.virtualColumns}
        data={visibleGroups}
        computeItemKey={(_index, group) => group.id}
        defaultItemHeight={348}
        increaseViewportBy={COLUMN_OVERSCAN}
        itemContent={(_index, group) => renderColumn(group)}
        // Hidden columns follow the last column inside the scroller, as on a board that is not virtualized.
        components={{ Footer: () => hiddenPanel }}
        style={{ height: '100%', width: '100%' }}
      />
    ) : <>{visibleGroups.map(group => renderColumn(group))}{hiddenPanel}</>}
  </div>
}

export function HiddenBoardColumns({ groups, onShowGroup }: { groups: MyIssuesGroupData[]; onShowGroup?: (id: string) => void }) {
  const { t } = useI18n()
  const [hiddenCollapsed, setHiddenCollapsed] = useState(false)
  const hiddenSectionRef = useRef<HTMLElement>(null)
  useEffect(() => {
    if (typeof window === 'undefined' || window.location.hash !== '#hidden-columns') return
    hiddenSectionRef.current?.scrollIntoView({ inline: 'start', block: 'nearest' })
  }, [groups.length])
  if (!groups.length) return null
  return <section ref={hiddenSectionRef} id="hidden-columns" className={styles.hiddenColumns} aria-label={t('Hidden columns')} data-board-hidden>
      <div className={styles.hiddenHeader} data-board-hidden-header-strip>
        <button className={styles.hiddenToggle} type="button" aria-label={t(hiddenCollapsed?'Expand group':'Collapse group')} aria-expanded={!hiddenCollapsed} data-board-hidden-header onClick={()=>setHiddenCollapsed(value=>!value)}>
          <svg width="18" height="18" viewBox="0 0 16 16" aria-hidden="true"><path d="M6.75194 10.6239C6.41861 10.8183 6 10.5779 6 10.192V5.80802C6 5.42212 6.41861 5.18169 6.75194 5.37613L10.5096 7.56811C10.8404 7.76105 10.8404 8.23895 10.5096 8.43189L6.75194 10.6239Z" fill="currentColor"/></svg>
          <strong>{t('Hidden columns')}</strong>
        </button>
      </div>
      {!hiddenCollapsed&&<div className={styles.hiddenList}>{groups.map(group=><button className={styles.hiddenStatus} key={group.id} type="button" data-board-hidden-status onClick={()=>onShowGroup?.(group.id)}><div className={styles.hiddenStatusCard}><StatusIcon state={{...group.state,id:group.id,name:group.label,type:group.stateType??'unstarted',color:group.state?.color??'var(--status-neutral)'}} size={14}/><span className={styles.hiddenStatusName} data-i18n-ignore>{group.label}</span><b><span className={styles.desktopIcon}><EstimateIcon size={14}/></span><span className={styles.phoneIcon}><BoardWarningIcon size={14}/></span><span>{group.totalCount ?? group.issues.length}</span></b></div></button>)}</div>}
    </section>
}

function IssueBoardColumn({ cell, group, createIssueLabel, properties, propertyOptions, selectedIds, draggingId, over, onCreateIssue, onHideGroup, onSelectIssue, onDragStart, onDragEnd, onSetOver, onDrop, onOpenIssue, onPropertyChange }: {
  /** A row's cell under the shared column header: the cards only. */
  cell?: boolean
  group: MyIssuesGroupData
  createIssueLabel: string
  properties: ReadonlySet<MyIssuesProperty>
  propertyOptions: MyIssuesRowPropertyOptions
  selectedIds: ReadonlySet<string>
  draggingId?: string
  over?: { groupId: string; index: number }
  onCreateIssue?: (group: MyIssuesGroupData) => void
  onHideGroup?: (groupId: string) => void
  onSelectIssue: (issueId: string, selected: boolean, range: boolean) => void
  onDragStart: (id: string) => void
  onDragEnd: () => void
  onSetOver: (value: { groupId: string; index: number } | undefined) => void
  onDrop: (event: DragEvent, group: MyIssuesGroupData, index: number) => void
  onOpenIssue: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>
}) {
  const { t } = useI18n()
  const virtualizeCards = !cell && group.issues.length > CARD_VIRTUALIZATION_THRESHOLD
  const estimateSettingsFor = useTeamEstimateSettings()
  const estimateSum = properties.has('estimate') ? sumEstimatePoints(group.issues, estimateSettingsFor) : undefined
  const card = (issue: MyIssuesRowData, index: number) => (
    <IssueBoardCard
      key={issue.id} issue={issue} properties={properties} propertyOptions={propertyOptions} selected={selectedIds.has(issue.id)} dragging={draggingId === issue.id}
      dropBefore={over?.groupId === group.id && over.index === index}
      onDragStart={() => onDragStart(issue.id)} onDragEnd={onDragEnd}
      onDragOver={event => { event.preventDefault(); event.stopPropagation(); const rect = event.currentTarget.getBoundingClientRect(); onSetOver({ groupId: group.id, index: index + (event.clientY > rect.top + rect.height / 2 ? 1 : 0) }) }}
      onDrop={event => { event.stopPropagation(); onDrop(event, group, over?.groupId === group.id ? over.index : index) }}
      onOpen={() => onOpenIssue(issue)} onOpenSubIssue={onOpenIssue} onPropertyChange={(property, value) => onPropertyChange?.(issue, property, value)} onSelect={(selected, range) => onSelectIssue(issue.id, selected, range)}
    />
  )
  return <section className={cell ? styles.rowCell : styles.column} role={cell ? 'group' : 'listitem'} aria-label={group.label} data-board-column={cell ? undefined : true} data-board-cell={cell || undefined} style={virtualizeCards ? { height: '100%', display: 'flex', flexDirection: 'column', minHeight: 0 } : undefined}>
    {!cell && <IssueBoardGroupHeader group={group} estimateSum={estimateSum} createIssueLabel={createIssueLabel} onCreateIssue={onCreateIssue} onHideGroup={onHideGroup} onSelectIssue={onSelectIssue}/>}
    <div className={styles.cards} data-over={over?.groupId === group.id} data-over-end={over?.groupId === group.id && over.index === group.issues.length} data-virtualized={virtualizeCards || undefined} onDragOver={event => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; if (event.target === event.currentTarget) onSetOver({ groupId: group.id, index: group.issues.length }) }} onDrop={event => { event.stopPropagation(); onDrop(event, group, group.issues.length) }} style={virtualizeCards ? { flex: '1 1 auto', minHeight: 0, display: 'flex', flexDirection: 'column' } : undefined}>
      {virtualizeCards ? (
        <Virtuoso
          className={styles.virtualCards}
          data={group.issues}
          computeItemKey={(_index, issue) => issue.id}
          defaultItemHeight={CARD_ESTIMATED_SIZE}
          increaseViewportBy={CARD_OVERSCAN}
          itemContent={(index, issue) => card(issue, index)}
          style={{ flex: '1 1 auto', minHeight: 120, height: '100%' }}
        />
      ) : group.issues.map((issue, index) => card(issue, index))}
      <button className={styles.addIssue} data-board-add-issue type="button" onClick={() => onCreateIssue?.(group)}><span className={styles.desktopIcon}><Plus size={14}/></span><span className={styles.phoneIcon}><BoardPlusIcon size={16}/></span>{t(createIssueLabel)}</button>
    </div>
  </section>
}

function BoardEllipsisIcon({ size = 14 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z"/></svg>
}

function BoardWarningIcon({ size = 14 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M3.741 14.5h8.521c1.691 0 2.778-1.795 1.993-3.293l-4.26-8.134c-.842-1.608-3.144-1.608-3.986 0l-4.26 8.134C.962 12.705 2.05 14.5 3.74 14.5ZM8 3.368a.742.742 0 0 0-.663.402l-4.26 8.134A.75.75 0 0 0 3.741 13H8V3.367Z"/></svg>
}

function BoardPlusIcon({ size = 14 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z"/></svg>
}

export function IssueBoardGroupHeader({group,estimateSum,createIssueLabel='Add new issue',onCreateIssue,onHideGroup,onSelectIssue}:{group:MyIssuesGroupData;estimateSum?:number;createIssueLabel?:string;onCreateIssue?:(group:MyIssuesGroupData)=>void;onHideGroup?:(id:string)=>void;onSelectIssue?:(id:string,selected:boolean,range:boolean)=>void}){
  const {t}=useI18n()
  const labelColors=useLabelColors()
  const count=group.totalCount??group.issues.length
  const pointsLabel=estimateSum === undefined ? undefined : t('{count} estimate points').replace('{count}', () => formatEstimatePoints(estimateSum))
  return <div className={styles.columnHeaderWrap} data-board-column-header>
      <header className={styles.columnHeader}>
        {group.stateType && <StatusIcon
          state={{ id: group.id, teamId: group.state?.teamId, name: group.label, type: group.stateType, color: group.state?.color ?? 'var(--status-neutral)' }}
          size={14}
        />}
        {group.color && <span className={styles.columnDot} style={{ '--group-label-color': labelColors.dot(group.color) } as CSSProperties} aria-hidden="true"/>}
        {group.labelPrefix && <><span data-i18n-ignore className={styles.columnPrefix}>{group.labelPrefix}</span><GroupPathChevron className={styles.columnPathChevron}/></>}
        <strong data-board-column-title data-i18n-ignore>{group.translatableLabel ? t(group.label) : group.label}</strong><button className={styles.count} data-board-column-count type="button" aria-label={[t(`${count} issues`), pointsLabel].filter(Boolean).join(', ')}>{estimateSum !== undefined && <EstimateIcon className={styles.countEstimate} size={14}/>}<span className={styles.countValue}><span className={styles.issueCount}>{count}</span>{estimateSum !== undefined && <span className={styles.estimateValue} aria-label={pointsLabel}>{formatEstimatePoints(estimateSum)}</span>}</span></button>
        <div className={styles.columnActions}>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={styles.headerButton} type="button" aria-label={t('Open menu')}><BoardEllipsisIcon/></button></DropdownMenu.Trigger><DropdownMenu.Portal><FlowDropdownMenuContent label={t('Open menu')} align="start" sideOffset={4}><FlowMenuItem label={count>group.issues.length?'Select loaded issues':'Select all in column'} onSelect={()=>group.issues.forEach(issue=>onSelectIssue?.(issue.id,true,false))}/><FlowMenuItem label="Hide column" onSelect={()=>onHideGroup?.(group.id)}/></FlowDropdownMenuContent></DropdownMenu.Portal></DropdownMenu.Root>
        <button className={styles.headerButton} type="button" aria-label={t(createIssueLabel)} onClick={() => onCreateIssue?.(group)}><BoardPlusIcon/></button>
        </div>
      </header>
    </div>
}

export function IssueBoardCard({ issue, properties, propertyOptions, selected, dragging, dropBefore, onDragStart, onDragEnd, onDragOver, onDrop, onOpen, onOpenSubIssue, onPropertyChange, onSelect }: {
  issue: MyIssuesRowData; properties: ReadonlySet<MyIssuesProperty>; propertyOptions: MyIssuesRowPropertyOptions; selected: boolean; dragging: boolean; dropBefore: boolean
  onDragStart: () => void; onDragEnd: () => void; onDragOver: (event: DragEvent<HTMLElement>) => void; onDrop: (event: DragEvent<HTMLElement>) => void; onOpen: () => void; onOpenSubIssue:(issue:MyIssuesRowData)=>void; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onSelect: (selected: boolean, range: boolean) => void
}) {
  const pointerOnControl = useRef(false)
  const dragged = useRef(false)
  const controls = 'button,input,textarea,select,[role="checkbox"],[role="menuitem"],[role="option"],[contenteditable="true"]'
  const click = (event: MouseEvent) => {
    if (!event.currentTarget.contains(event.target as Node)) return
    if (dragged.current || (event.target as Element).closest(controls)) { event.preventDefault(); return }
    if (event.shiftKey) { event.preventDefault(); onSelect(!selected, true); return }
    if (event.metaKey || event.ctrlKey || event.altKey || event.button !== 0) return
    event.preventDefault(); onOpen()
  }
  const change = (property: MyIssuesEditableProperty, value: string | string[]) => onPropertyChange(property, value)
  const statusOptions = useTeamStatusOptions(propertyOptions.status, issue.teamId)
  const scopedOptions = { ...propertyOptions, status: statusOptions }
  return <ContextMenu.Root><ContextMenu.Trigger asChild><a href={issue.href} className={styles.card} data-board-card tabIndex={0} draggable aria-grabbed={dragging} data-selected={selected} data-dragging={dragging} data-drop-before={dropBefore} aria-label={`${issue.identifier} ${issue.title}`} onClick={click} onPointerDownCapture={event => { pointerOnControl.current = Boolean((event.target as Element).closest(controls)); }} onKeyDown={event => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { if (!event.shiftKey && (event.metaKey || event.ctrlKey || event.altKey)) return; event.preventDefault(); if (event.shiftKey) onSelect(!selected, true); else onOpen() } }} onDragStart={event => { if (pointerOnControl.current) { event.preventDefault(); return } dragged.current = true; event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', issue.id); onDragStart() }} onDragEnd={() => { onDragEnd(); requestAnimationFrame(() => { dragged.current = false }) }} onDragOver={event => { event.dataTransfer.dropEffect = 'move'; onDragOver(event) }} onDrop={onDrop}>
    <div className={styles.cardBody}>
    {(properties.has('id') || Boolean(issue.ancestors?.length)) && <div className={styles.cardTop}>
      {properties.has('id') && <span data-board-card-id data-i18n-ignore>{issue.identifier}</span>}
      {issue.ancestors?.length?<IssueParentTrail ancestors={issue.ancestors} board/>:null}
    </div>}
      {properties.has('assignee') && <IssueAssigneeSlot issue={issue} options={propertyOptions.assignee} onChange={value => change('assignee', value)} label="Assign issue" triggerClassName={styles.assigneeTrigger} avatarClassName={styles.avatar} emptyClassName={styles.noAssignee} hoverPlacement="below"/>}
    <div className={styles.titleRow} data-has-status={properties.has('status')} data-has-assignee={properties.has('assignee')} data-has-heading={properties.has('id') || Boolean(issue.ancestors?.length)}>
      {properties.has('status') && <RowCommandPicker propertyLabel="Status" label={`Change status. Current status is ${issue.state.name}`} searchLabel="Change status..." searchShortcut="S" selectedIds={[issue.state.id]} options={scopedOptions.status} onSelect={value => change('status', value)} triggerClassName={styles.status} trigger={<StatusIcon state={issue.state} size={14}/>}/>}
      <strong className={styles.title} data-board-card-title data-single-line={Boolean(issue.ancestors?.length) && !properties.has('id')} data-i18n-ignore>{issue.title}</strong>
    </div>
    <CardProperties issue={issue} properties={properties} propertyOptions={propertyOptions} onPropertyChange={change} onOpenSubIssue={onOpenSubIssue}/>
    {(properties.has('created') || properties.has('updated')) && <div className={styles.dateBadges}>{properties.has('created') && <span className={styles.created} data-board-card-date>Created {formatDate(issue.createdAt)}</span>}{properties.has('updated') && <span className={styles.created} data-board-card-date>Updated {formatDate(issue.updatedAt)}</span>}</div>}
    </div>
  </a></ContextMenu.Trigger><ContextMenu.Portal><IssueContextMenu editable issue={issue} options={scopedOptions} onPropertyChange={change}/></ContextMenu.Portal></ContextMenu.Root>
}

function CardProperties({ issue, properties, propertyOptions, onPropertyChange, onOpenSubIssue }: { issue: MyIssuesRowData; properties: ReadonlySet<MyIssuesProperty>; propertyOptions: MyIssuesRowPropertyOptions; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onOpenSubIssue: (issue: MyIssuesRowData) => void }) {
  const labels = properties.has('labels') ? issue.labels ?? [] : []
  const chips = labelChips(labels)
  const shownLabels = chips.slice(0, 4)
  const hiddenLabelCount = chips.length - shownLabels.length
  const estimateSettings = useTeamEstimateSettings()(issue.teamId)
  const hasEstimateContext = Boolean(useIssueRowActions())
  const showEstimate = properties.has('estimate') && issue.estimate != null && issue.estimate >= 0 && (hasEstimateContext ? teamUsesEstimates(estimateSettings) : issue.estimate > 0)
  const hasSubIssues = Boolean(issue.subIssueProgress && issue.subIssues?.length)
  const visible = properties.has('priority') || showEstimate || hasSubIssues || Boolean(properties.has('sla') && issue.sla) || Boolean(properties.has('project') && issue.project) || Boolean(properties.has('cycle') && issue.cycleId) || shownLabels.length > 0 || Boolean(properties.has('dueDate') && issue.dueDate) || Boolean(properties.has('release') && issue.releaseCount) || Boolean(properties.has('links') && issue.linkCount) || Boolean((properties.has('pullRequests') && issue.pullRequestCount) || issue.blockedByCount || issue.blockingCount) || Boolean(properties.has('timeInStatus') && issue.timeInStatusMinutes != null) || Boolean(properties.has('myActivity') && issue.myActivityAt) || Boolean(properties.has('customers') && issue.customerCount) || Boolean(properties.has('customerRevenue') && issue.customers?.some(customer => (customer.annualRevenue ?? 0) > 0))
  const labelsPicker = (key: string, content: ReactNode) => <RowCommandPicker key={key} propertyLabel="Labels" kind="labels" multi label={`Change labels. ${labels.map(label => label.name).join(', ')} selected`} searchLabel="Change or add labels..." selectedIds={labels.map(label => label.id)} options={propertyOptions.labels} onSelect={value => onPropertyChange('labels', toggleGroupedLabelIds(labels.map(label => label.id), value, propertyOptions.labels))} triggerClassName={styles.labelsTrigger} trigger={content}/>
  if (!visible) return null
  return <div className={styles.cardMeta}>
    {properties.has('priority') && <RowCommandPicker propertyLabel="Priority" label={`${priorityName(issue.priority)} priority`} searchLabel="Change priority to..." selectedIds={[String(issue.priority)]} options={propertyOptions.priority} onSelect={value => onPropertyChange('priority', value)} triggerClassName={`${styles.metaTrigger} ${styles.iconBadge}`} trigger={<PriorityIcon priority={issue.priority} size={14}/>}/>}
    {showEstimate && <span className={styles.cardBadge} data-board-card-badge><EstimateChip className={styles.badge} value={issue.estimate!} settings={estimateSettings} label={`Estimate ${issue.estimate}`} board/></span>}
    {hasSubIssues && <SubIssueProgress className={styles.subIssueBadge} progress={issue.subIssueProgress!} subIssues={issue.subIssues!} onOpenSubIssue={onOpenSubIssue}/>}
    {properties.has('sla') && issue.sla && <IssueSLAIndicator compact sla={issue.sla} ruleName={issue.sla.ruleName}/>}
    {properties.has('release') && issue.releases?.length ? <IssueReleaseChip className={styles.badge} iconSize={12} releases={issue.releases}/> : null}
    {properties.has('links') && issue.linkCount ? <span className={styles.badge}><Link2 size={12}/><span>{issue.linkCount}</span></span> : null}
    {((properties.has('pullRequests') && issue.pullRequestCount) || issue.blockedByCount || issue.blockingCount) ? <span className={styles.badge}><IssueWidgetAdornments showPullRequests={properties.has('pullRequests')} pullRequestLifecycle={issue.pullRequestLifecycle} pullRequestCount={issue.pullRequestCount ?? 0} blockedByCount={issue.blockedByCount ?? 0} blockingCount={issue.blockingCount ?? 0} blockedByIds={issue.blockedByIds} blockingIds={issue.blockingIds} compact/></span> : null}
    {properties.has('timeInStatus') && issue.timeInStatusMinutes != null ? <span className={styles.badge}><Clock3 size={12}/><span>{formatTimeInStatus(issue.timeInStatusMinutes)}</span></span> : null}
    {properties.has('myActivity') && issue.myActivityAt ? <span className={styles.badge}><span>{formatDate(issue.myActivityAt)}</span></span> : null}
    {properties.has('project') && issue.project && <RowCommandPicker propertyLabel="Project" kind="project" label={`Change project. Current project is ${issue.project.name}`} searchLabel="Set project..." selectedIds={[issue.project.id]} options={propertyOptions.project} onSelect={value => onPropertyChange('project', value)} triggerClassName={`${styles.metaTrigger} ${styles.projectBadge}`} trigger={<><ProjectGlyph project={issue.project} size={14}/><span data-i18n-ignore>{issue.project.name}</span></>}/>}
    {properties.has('cycle') && issue.cycleId && <RowCommandPicker propertyLabel="Cycle" label={`Change cycle. Current cycle is ${issue.cycleName ?? issue.cycleId}`} searchLabel="Add to cycle..." selectedIds={[issue.cycleId]} options={(propertyOptions.cycle ?? []).filter(option => !option.teamId || !issue.teamId || option.teamId === issue.teamId)} onSelect={value => onPropertyChange('cycle', value)} triggerClassName={styles.metaTrigger} trigger={<><CycleIcon size={14}/><span data-i18n-ignore>{issue.cycleName ?? issue.cycleId}</span></>}/>}
    {/* One picker trigger per chip (all open the same label picker), so chips wrap one by one like the reference. */}
    {shownLabels.map(chip => labelsPicker(chip.key, chip.labels.length > 1 ? <MergedLabelBadge chip={chip} className={styles.badge} dotsClassName={styles.mergedDots}/> : <Badge color={chip.labels[0].color}>{chip.labels[0].name}</Badge>))}
    {hiddenLabelCount > 0 && labelsPicker('more', <span className={styles.badge}><span className={styles.labelStack}>{labels.slice(4, 6).map((label, index) => <i key={label.id} style={{ '--dot-index': index, backgroundColor: label.color } as CSSProperties}/>)}</span><span>+{hiddenLabelCount} labels</span></span>)}
    {properties.has('dueDate') && issue.dueDate && <DueDatePicker value={issue.dueDate} allowRemove={!issue.recurrence} onChange={value => onPropertyChange('dueDate', value)} ariaLabel={`Change due date. Current due date is ${formatDate(issue.dueDate)}`} triggerClassName={styles.metaTrigger} trigger={<><CalendarIcon size={13}/><span>{formatDate(issue.dueDate)}</span></>}/>}
    {properties.has('customers') && issue.customerCount ? <CustomersRowChip variant="square" customers={issue.customers ?? []} customerCount={issue.customerCount} importantCustomerIds={issue.importantCustomerIds ?? []} settings={issue.customerRevenueSettings}/> : null}
    {properties.has('customerRevenue') && issue.customers?.length ? <CustomerRevenueRowChip variant="square" customers={issue.customers} settings={issue.customerRevenueSettings}/> : null}
  </div>
}

function Badge({ children, color }: { children: string; color: string }) { const labelColors = useLabelColors(); return <span className={styles.badge}><i className={styles.labelDot} style={{ backgroundColor: labelColors.dot(color) }}/><span data-i18n-ignore>{children}</span></span> }
const CARD_DATE_FORMAT = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' })
function formatDate(value: string) { return CARD_DATE_FORMAT.format(new Date(value.length === 10 ? `${value}T00:00:00` : value)) }
function formatTimeInStatus(minutes: number) { if (minutes < 60) return `${Math.max(0, minutes)}m`; const hours = minutes / 60; if (hours < 24) return `${Math.round(hours * 10) / 10}h`; return `${Math.round((hours / 24) * 10) / 10}d` }
function priorityName(priority: number) { return ['No', 'Urgent', 'High', 'Medium', 'Low'][priority] ?? 'No' }
