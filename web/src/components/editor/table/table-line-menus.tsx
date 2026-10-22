import type { Editor } from '@tiptap/core'
import {
  ArrowDown, ArrowDownWideNarrow, ArrowLeft, ArrowRight, ArrowUp, ArrowUpNarrowWide, BetweenHorizontalEnd, BetweenHorizontalStart,
  BetweenVerticalEnd, BetweenVerticalStart, Bold, Clipboard, Columns3, Copy, Ellipsis, EllipsisVertical, Italic, Rows3, Strikethrough, Type,
  Underline, type LucideIcon,
} from 'lucide-react'
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
  copyLineText, deleteLine, duplicateLine, formatLine, insertLine, lineAvailability, moveLine, selectLine, sortColumn,
  type LineMark, type TableAxis, type TableLine,
} from './table-lines'

type OpenLine = { axis: TableAxis; index: number }
type Placement = { row?: CSSProperties; column?: CSSProperties }

const keepSelection = (event: MouseEvent) => event.preventDefault()

function Shortcut({ spec }: { spec: string }) {
  const keys = shortcutSteps(spec, isMacPlatform())[0]
  return <span className="flow-table__menu-shortcut" aria-hidden>{keys.map((key, index) => <kbd key={index}>{key}</kbd>)}</span>
}

const FORMATS: Array<{ mark: LineMark; label: string; icon: LucideIcon; shortcut: string }> = [
  { mark: 'bold', label: 'Bold', icon: Bold, shortcut: '⌘ B' },
  { mark: 'italic', label: 'Italic', icon: Italic, shortcut: '⌘ I' },
  { mark: 'underline', label: 'Underline', icon: Underline, shortcut: '⌘ U' },
  { mark: 'strike', label: 'Strikethrough', icon: Strikethrough, shortcut: '⌘ ⇧ X' },
]

/** The menu of one row / column: insert, move, duplicate, (column) sort, format, copy text, delete. Unavailable items are left out. */
function LineMenuItems({ editor, line }: { editor: Editor; line: TableLine }) {
  const { t } = useI18n()
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
  const item = (icon: LucideIcon, label: string, run: () => void, shortcut?: string): ReactNode => {
    const Icon = icon
    return <DropdownMenuItem onSelect={run}><Icon size={16} aria-hidden/>{t(label)}{shortcut ? <Shortcut spec={shortcut}/> : null}</DropdownMenuItem>
  }
  const before = row ? { label: 'Insert above', icon: BetweenHorizontalStart, move: 'Move up', moveIcon: ArrowUp, key: '↑' } : { label: 'Insert before', icon: BetweenVerticalStart, move: 'Move left', moveIcon: ArrowLeft, key: '←' }
  const after = row ? { label: 'Insert below', icon: BetweenHorizontalEnd, move: 'Move down', moveIcon: ArrowDown, key: '↓' } : { label: 'Insert after', icon: BetweenVerticalEnd, move: 'Move right', moveIcon: ArrowRight, key: '→' }
  const hasMoves = available.moveBefore || available.moveAfter
  return <>
    {available.insertBefore ? item(before.icon, before.label, act(() => insertLine(editor, line, 'before'))) : null}
    {available.insertAfter ? item(after.icon, after.label, act(() => insertLine(editor, line, 'after'))) : null}
    {available.moveBefore ? item(before.moveIcon, before.move, act(() => moveLine(editor, line, 'before')), `⌘ ⌥ ⇧ ${before.key}`) : null}
    {available.moveAfter ? item(after.moveIcon, after.move, act(() => moveLine(editor, line, 'after')), `⌘ ⌥ ⇧ ${after.key}`) : null}
    {!row && hasMoves ? <DropdownMenuSeparator/> : null}
    {available.duplicate ? item(Copy, 'Duplicate', act(() => duplicateLine(editor, line))) : null}
    <DropdownMenuSeparator/>
    {!row && available.sort ? <>
      {item(ArrowUpNarrowWide, 'Sort ascending', act(() => sortColumn(editor, line, 'asc')))}
      {item(ArrowDownWideNarrow, 'Sort descending', act(() => sortColumn(editor, line, 'desc')))}
      <DropdownMenuSeparator/>
    </> : null}
    <DropdownMenuSub>
      <DropdownMenuSubTrigger><Type size={16} aria-hidden/>{t('Format')}</DropdownMenuSubTrigger>
      <DropdownMenuSubContent className="flow-table__menu flow-table__line-menu" sideOffset={6}>
        {FORMATS.map(format => <Fragment key={format.mark}>{item(format.icon, format.label, act(() => formatLine(editor, line, format.mark)), format.shortcut)}</Fragment>)}
      </DropdownMenuSubContent>
    </DropdownMenuSub>
    <DropdownMenuSeparator/>
    {item(Clipboard, 'Copy text', copy)}
    {item(row ? Rows3 : Columns3, 'Delete', act(() => deleteLine(editor, line)))}
  </>
}

function LineHandle({ editor, getPos, axis, index, style, onOpenChange, open }: {
  editor: Editor
  getPos: () => number | undefined
  axis: TableAxis
  index: number
  style: CSSProperties
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useI18n()
  const row = axis === 'row'
  const tablePos = getPos()
  const line: TableLine | null = tablePos === undefined ? null : { tablePos, axis, index }
  return <DropdownMenu open={open} modal={false} onOpenChange={next => {
    if (next && line) selectLine(editor, line)
    onOpenChange(next)
  }}>
    <DropdownMenuTrigger asChild>
      <button type="button" className="flow-table__line-handle" data-axis={axis} data-active={open ? '' : undefined} style={style} aria-label={t(row ? 'Row actions' : 'Column actions')} title={t(row ? 'Row actions' : 'Column actions')} onMouseDown={keepSelection}>
        {row ? <EllipsisVertical size={12} aria-hidden/> : <Ellipsis size={12} aria-hidden/>}
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent className="flow-table__menu flow-table__line-menu" side={row ? 'left' : 'bottom'} align={row ? 'start' : 'center'} sideOffset={6} onCloseAutoFocus={event => event.preventDefault()}>
      {line ? <LineMenuItems editor={editor} line={line}/> : null}
    </DropdownMenuContent>
  </DropdownMenu>
}

/**
 * The per-row (left edge) and per-column (top edge) handles. They follow the hovered cell and stay on the line whose menu
 * is open; clicking one selects the whole line and opens its menu.
 */
export function TableLineHandles({ editor, getPos, onMenuChange }: { editor: Editor; getPos: () => number | undefined; onMenuChange: (open: boolean) => void }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<{ row: number; col: number } | null>(null)
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
    }
    // The menus render in a portal, so reaching one fires mouseleave here: the lines stay while a menu is open.
    const leave = () => { if (!openRef.current) setHover(null) }
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
      placement.row = { top: rect.top - frame.top + rect.height / 2 - 11, left: tableRect.left - frame.left - 6 }
    }
    const cell = colIndex === null ? undefined : table.rows[0]?.cells[colIndex]
    if (cell) {
      const rect = cell.getBoundingClientRect()
      const center = Math.min(Math.max(rect.left - frame.left + rect.width / 2, scroller.left - frame.left + 14), scroller.right - frame.left - 14)
      placement.column = { top: tableRect.top - frame.top - 6, left: center - 11 }
    }
    return placement
  }, [rowIndex, colIndex])

  useLayoutEffect(() => { setPlacement(measure()) }, [measure, tick, hover, open])

  const setMenu = (next: OpenLine | null) => {
    setOpen(next)
    onMenuChange(next !== null)
    if (!next) setHover(null)
  }
  return <div ref={rootRef} className="flow-table__lines">
    {rowIndex !== null && placement.row
      ? <LineHandle editor={editor} getPos={getPos} axis="row" index={rowIndex} style={placement.row} open={open?.axis === 'row'} onOpenChange={next => setMenu(next ? { axis: 'row', index: rowIndex } : null)}/>
      : null}
    {colIndex !== null && placement.column
      ? <LineHandle editor={editor} getPos={getPos} axis="column" index={colIndex} style={placement.column} open={open?.axis === 'column'} onOpenChange={next => setMenu(next ? { axis: 'column', index: colIndex } : null)}/>
      : null}
  </div>
}
