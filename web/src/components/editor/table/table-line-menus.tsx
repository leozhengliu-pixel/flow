import type { Editor } from '@tiptap/core'
import { Fragment, useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from 'react'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { shortcutSteps } from '@/components/ui/menu-shortcuts'
import { useI18n } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import { tableAt } from './table-commands'
import {
  AddHeaderIcon, BoldIcon, CopyTextIcon, DeleteColumnIcon, DeleteRowIcon, DuplicateIcon, FormatIcon, HandleDotsIcon, InsertColumnIcon, InsertRowIcon, ItalicIcon,
  MoveDownIcon, MoveLeftIcon, SortAscendingIcon, SortDescendingIcon, StrikethroughIcon, UnderlineIcon,
} from './table-icons'
import { setLineHighlight } from './table-line-highlight'
import {
  addHeaderRow, copyLineText, deleteLine, duplicateLine, formatLine, insertLine, lineAvailability, moveLine, sortColumn,
  type LineMark, type TableAxis, type TableLine,
} from './table-lines'

type OpenLine = { axis: TableAxis; index: number }
type Placement = { row?: CSSProperties; column?: CSSProperties }
type Glyph = (props: { rotate?: 90 | 180 }) => ReactNode

/** Distance (px) from the table's left / top edge within which the pointer reveals the row / column handle. */
const EDGE_REACH = 5

const keepSelection = (event: MouseEvent) => event.preventDefault()

/**
 * Like the reference app, the first item is highlighted as soon as a line menu opens (Radix only does that for keyboard opens).
 * Radix's menu takes `onOpenAutoFocus`, but the dropdown wrapper's props leave it out, hence the spread of a loosely typed object.
 */
const focusFirstItem = {
  onOpenAutoFocus: (event: Event) => {
    event.preventDefault()
    const content = event.target instanceof HTMLElement ? event.target : null
    content?.querySelector<HTMLElement>('[role="menuitem"]')?.focus({ preventScroll: true })
  },
} as object

function Shortcut({ spec }: { spec: string }) {
  const keys = shortcutSteps(spec, isMacPlatform())[0]
  return <span className="flow-table__menu-shortcut" aria-hidden>{keys.map((key, index) => <kbd key={index}>{key}</kbd>)}</span>
}

const FORMATS: Array<{ mark: LineMark; label: string; icon: Glyph; shortcut: string }> = [
  { mark: 'bold', label: 'Bold', icon: BoldIcon, shortcut: '⌘ B' },
  { mark: 'italic', label: 'Italic', icon: ItalicIcon, shortcut: '⌘ I' },
  { mark: 'underline', label: 'Underline', icon: UnderlineIcon, shortcut: '⌘ U' },
  { mark: 'strike', label: 'Strikethrough', icon: StrikethroughIcon, shortcut: '⌘ ⇧ X' },
]

/**
 * The menu of one row / column: insert, move, duplicate, (column) sort, format, copy text, delete. Unavailable items are left out,
 * and the dividers follow the reference app (a divider after Duplicate only when Duplicate is there, none between Insert below and
 * Format on the header row).
 */
function LineMenuItems({ editor, line }: { editor: Editor; line: TableLine }) {
  const { t, locale } = useI18n()
  const table = tableAt(editor, line.tablePos)
  if (!table) return null
  const row = line.axis === 'row'
  const available = lineAvailability(table, line.axis, line.index)
  const done = () => { editor.view.focus() }
  const act = (run: () => unknown) => () => { run(); done() }
  const copy = () => {
    const write = copyLineText(editor, line)
    void write.then(
      ok => ok ? toast.success(t(row ? 'Row copied' : 'Column copied')) : toast.error(t('Could not copy text')),
      () => toast.error(t('Could not copy text')),
    )
    done()
  }
  const item = (Icon: Glyph, label: string, run: () => void, shortcut?: string, rotate?: 90 | 180): ReactNode =>
    <DropdownMenuItem onSelect={run}><span className="flow-table__menu-icon"><Icon rotate={rotate}/></span><span className="flow-table__menu-label">{t(label)}</span>{shortcut ? <Shortcut spec={shortcut}/> : null}</DropdownMenuItem>
  const InsertIcon = row ? InsertRowIcon : InsertColumnIcon
  const before = row
    ? { label: 'Insert above', move: 'Move up', Move: MoveDownIcon, key: '↑', moveRotate: 180 as const }
    : { label: 'Insert before', move: 'Move left', Move: MoveLeftIcon, key: '←', moveRotate: undefined }
  const after = row
    ? { label: 'Insert below', move: 'Move down', Move: MoveDownIcon, key: '↓', moveRotate: undefined }
    : { label: 'Insert after', move: 'Move right', Move: MoveLeftIcon, key: '→', moveRotate: 180 as const }
  return <>
    {available.addHeader ? <>
      {/* "Add header" is a table term; the shared dictionary already maps that English text to the request-header wording, so Chinese uses its own key. */}
      {item(AddHeaderIcon, locale === 'zh-CN' ? t('Add table header') : 'Add header', act(() => addHeaderRow(editor, line)))}
      <DropdownMenuSeparator/>
    </> : null}
    {available.insertBefore ? item(InsertIcon, before.label, act(() => insertLine(editor, line, 'before')), undefined, 180) : null}
    {available.insertAfter ? item(InsertIcon, after.label, act(() => insertLine(editor, line, 'after'))) : null}
    {available.moveBefore ? item(before.Move, before.move, act(() => moveLine(editor, line, 'before')), `⌘ ⌥ ⇧ ${before.key}`, before.moveRotate) : null}
    {available.moveAfter ? item(after.Move, after.move, act(() => moveLine(editor, line, 'after')), `⌘ ⌥ ⇧ ${after.key}`, after.moveRotate) : null}
    {available.separateDuplicate ? <DropdownMenuSeparator/> : null}
    {available.duplicate ? <>
      {item(DuplicateIcon, 'Duplicate', act(() => duplicateLine(editor, line)))}
      <DropdownMenuSeparator/>
    </> : null}
    {available.sort ? <>
      {item(SortAscendingIcon, 'Sort ascending', act(() => sortColumn(editor, line, 'asc')))}
      {item(SortDescendingIcon, 'Sort descending', act(() => sortColumn(editor, line, 'desc')))}
      <DropdownMenuSeparator/>
    </> : null}
    <DropdownMenuSub>
      <DropdownMenuSubTrigger><span className="flow-table__menu-icon"><FormatIcon/></span><span className="flow-table__menu-label">{t('Format')}</span><span className="flow-table__menu-chevron" aria-hidden>▶</span></DropdownMenuSubTrigger>
      <DropdownMenuSubContent className="flow-table__line-menu flow-table__line-submenu" sideOffset={-1.5} alignOffset={-6.5}>
        {FORMATS.map(format => <Fragment key={format.mark}>{item(format.icon, format.label, act(() => formatLine(editor, line, format.mark)), format.shortcut)}</Fragment>)}
      </DropdownMenuSubContent>
    </DropdownMenuSub>
    <DropdownMenuSeparator/>
    {item(CopyTextIcon, 'Copy text', copy)}
    {available.delete ? item(row ? DeleteRowIcon : DeleteColumnIcon, 'Delete', act(() => deleteLine(editor, line))) : null}
  </>
}

/**
 * A row (left edge) or column (top edge) handle: a thin pill on the table border that widens under the pointer. The menu anchors to
 * the 3px strip around the border (not to the pill), like the reference app, so it does not shift when the pill widens.
 */
function LineHandle({ editor, getPos, axis, index, style, visible, onOpenChange, open }: {
  editor: Editor
  getPos: () => number | undefined
  axis: TableAxis
  index: number
  style: CSSProperties
  visible: boolean
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useI18n()
  const row = axis === 'row'
  const tablePos = getPos()
  const line: TableLine | null = tablePos === undefined ? null : { tablePos, axis, index }
  const label = t(row ? 'Row actions' : 'Column actions')
  return <DropdownMenu open={open} modal={false} onOpenChange={next => {
    if (line) setLineHighlight(editor, next ? line : null)
    onOpenChange(next)
  }}>
    <DropdownMenuTrigger asChild>
      <div role="button" tabIndex={-1} className="flow-table__line-anchor" data-axis={axis} data-visible={visible || open ? '' : undefined} style={style} aria-label={label} title={label} onMouseDown={keepSelection}>
        <span className="flow-table__line-handle"><HandleDotsIcon rotate={row ? 90 : undefined}/></span>
      </div>
    </DropdownMenuTrigger>
    <DropdownMenuContent
      className="flow-table__line-menu" side={row ? 'left' : 'bottom'} align={row ? 'start' : 'center'} sideOffset={4}
      {...focusFirstItem}
      onCloseAutoFocus={event => event.preventDefault()}
    >
      {line ? <LineMenuItems editor={editor} line={line}/> : null}
    </DropdownMenuContent>
  </DropdownMenu>
}

/**
 * The per-row (left edge) and per-column (top edge) handles. They follow the hovered cell, show once the pointer is at the table
 * border, and stay on the line whose menu is open; opening a menu only tints its cells, the selection moves when a command runs.
 */
export function TableLineHandles({ editor, getPos, onMenuChange }: { editor: Editor; getPos: () => number | undefined; onMenuChange: (open: boolean) => void }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<{ row: number; col: number } | null>(null)
  const [edge, setEdge] = useState({ row: false, column: false })
  const [open, setOpen] = useState<OpenLine | null>(null)
  const [placement, setPlacement] = useState<Placement>({})
  const [tick, setTick] = useState(0)
  const activeRef = useRef(false)
  activeRef.current = !!hover || !!open
  const openRef = useRef<OpenLine | null>(null)
  openRef.current = open

  const outer = () => rootRef.current?.closest<HTMLElement>('.flow-table') ?? null

  useEffect(() => {
    const element = outer()
    if (!element) return
    const move = (event: Event) => {
      const target = event.target
      if (!(target instanceof Element)) return
      const cell = target.closest('td, th')
      if (!(cell instanceof HTMLTableCellElement) || !element.contains(cell)) return
      const row = cell.parentElement
      if (!(row instanceof HTMLTableRowElement)) return
      setHover(current => current && current.row === row.rowIndex && current.col === cell.cellIndex ? current : { row: row.rowIndex, col: cell.cellIndex })
      const table = element.querySelector('table')
      if (table && event instanceof globalThis.MouseEvent) {
        const rect = table.getBoundingClientRect()
        const next = { row: event.clientX - rect.left <= EDGE_REACH, column: event.clientY - rect.top <= EDGE_REACH }
        setEdge(current => current.row === next.row && current.column === next.column ? current : next)
      }
    }
    // The menus render in a portal, so reaching one fires mouseleave here: the lines stay while a menu is open.
    const leave = () => { if (!openRef.current) { setHover(null); setEdge({ row: false, column: false }) } }
    element.addEventListener('mousemove', move)
    element.addEventListener('mouseleave', leave)
    const refresh = () => { if (activeRef.current) setTick(value => value + 1) }
    editor.on('transaction', refresh)
    window.addEventListener('resize', refresh)
    return () => {
      element.removeEventListener('mousemove', move)
      element.removeEventListener('mouseleave', leave)
      editor.off('transaction', refresh)
      window.removeEventListener('resize', refresh)
    }
  }, [editor])

  const rowIndex = open?.axis === 'row' ? open.index : hover?.row ?? null
  const colIndex = open?.axis === 'column' ? open.index : hover?.col ?? null

  const measure = useCallback((): Placement => {
    const element = outer()
    const table = element?.querySelector('table')
    if (!element || !table) return {}
    const frame = element.getBoundingClientRect()
    const tableRect = table.getBoundingClientRect()
    const scroller = element.querySelector('.tableWrapper')?.getBoundingClientRect() ?? tableRect
    const placement: Placement = {}
    const row = rowIndex === null ? undefined : table.rows[rowIndex]
    if (row) {
      const rect = row.getBoundingClientRect()
      placement.row = { top: rect.top - frame.top, left: tableRect.left - frame.left - 1.5, width: 3, height: rect.height }
    }
    const cell = colIndex === null ? undefined : table.rows[0]?.cells[colIndex]
    if (cell) {
      const rect = cell.getBoundingClientRect()
      const left = Math.max(rect.left, scroller.left)
      const right = Math.min(rect.right, scroller.right)
      if (right >= left) placement.column = { top: tableRect.top - frame.top - 2, left: left - frame.left, width: right - left, height: 3 }
    }
    return placement
  }, [rowIndex, colIndex])

  useLayoutEffect(() => { setPlacement(measure()) }, [measure, tick, hover, open])

  const setMenu = (next: OpenLine | null) => {
    setOpen(next)
    onMenuChange(next !== null)
    if (!next) { setHover(null); setEdge({ row: false, column: false }) }
  }
  return <div ref={rootRef} className="flow-table__lines">
    {rowIndex !== null && placement.row
      ? <LineHandle editor={editor} getPos={getPos} axis="row" index={rowIndex} style={placement.row} visible={edge.row} open={open?.axis === 'row'} onOpenChange={next => setMenu(next ? { axis: 'row', index: rowIndex } : null)}/>
      : null}
    {colIndex !== null && placement.column
      ? <LineHandle editor={editor} getPos={getPos} axis="column" index={colIndex} style={placement.column} visible={edge.column} open={open?.axis === 'column'} onOpenChange={next => setMenu(next ? { axis: 'column', index: colIndex } : null)}/>
      : null}
  </div>
}
