import type { Editor } from '@tiptap/core'
import {
  ArrowDownToLine, ArrowLeftToLine, ArrowRightToLine, ArrowUpToLine,
  PanelLeft, PanelTop, Plus, Rows3, Columns3, Trash2, Ellipsis, type LucideIcon,
} from 'lucide-react'
import { Fragment, useEffect, useRef, useState, type MouseEvent } from 'react'
import { toast } from '@/lib/toast'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import { HandleDotsIcon, TableCopyIcon, TableSelectIcon, TableTrashIcon } from './table-icons'
import { TableLineHandles } from './table-line-menus'
import { deleteWholeTable, runInTable, selectWholeTable, tableAt, tableToText } from './table-commands'

type Chain = ReturnType<Editor['chain']>
type Action = { id: string; label: string; icon: LucideIcon; run: (chain: Chain) => Chain; danger?: boolean }

const ACTIONS: Action[] = [
  { id: 'row-above', label: 'Insert row above', icon: ArrowUpToLine, run: chain => chain.addRowBefore() },
  { id: 'row-below', label: 'Insert row below', icon: ArrowDownToLine, run: chain => chain.addRowAfter() },
  { id: 'column-left', label: 'Insert column left', icon: ArrowLeftToLine, run: chain => chain.addColumnBefore() },
  { id: 'column-right', label: 'Insert column right', icon: ArrowRightToLine, run: chain => chain.addColumnAfter() },
  { id: 'header-row', label: 'Toggle header row', icon: PanelTop, run: chain => chain.toggleHeaderRow() },
  { id: 'header-column', label: 'Toggle header column', icon: PanelLeft, run: chain => chain.toggleHeaderColumn() },
  { id: 'delete-row', label: 'Delete row', icon: Rows3, run: chain => chain.deleteRow(), danger: true },
  { id: 'delete-column', label: 'Delete column', icon: Columns3, run: chain => chain.deleteColumn(), danger: true },
  { id: 'delete-table', label: 'Delete table', icon: Trash2, run: chain => chain.deleteTable(), danger: true },
]

function useEditable(editor: Editor) {
  const [editable, setEditable] = useState(editor.isEditable)
  useEffect(() => {
    const sync = () => setEditable(editor.isEditable)
    sync()
    editor.on('update', sync)
    editor.on('transaction', sync)
    return () => { editor.off('update', sync); editor.off('transaction', sync) }
  }, [editor])
  return editable
}

/** Keeps the editor selection (and caret) where it is when a chrome button is pressed. */
const keepSelection = (event: MouseEvent) => event.preventDefault()

/**
 * Hover chrome of a table: "Add row" under it, "Add column" at its right edge, the "Table actions" corner menu and the
 * left block handle. Everything is hidden unless the table is hovered / holds focus / has a menu open (see table.css).
 */
export function TableChrome({ editor, getPos }: { editor: Editor; getPos: () => number | undefined }) {
  const { t } = useI18n()
  const editable = useEditable(editor)
  const [openMenu, setOpenMenu] = useState<'actions' | 'block' | 'lines' | null>(null)
  const chromeRef = useRef<HTMLDivElement>(null)
  // The table frame shows its selection outline in the accent colour while one of its menus is open.
  useEffect(() => {
    const frame = chromeRef.current?.closest('.flow-table')
    if (!frame) return
    if (openMenu) frame.setAttribute('data-menu-open', '')
    else frame.removeAttribute('data-menu-open')
    return () => frame.removeAttribute('data-menu-open')
  }, [openMenu, editable])
  if (!editable) return null

  const addRow = () => runInTable(editor, getPos(), 'last', chain => chain.addRowAfter())
  const addColumn = () => runInTable(editor, getPos(), 'last', chain => chain.addColumnAfter())
  const copy = () => {
    const table = tableAt(editor, getPos())
    if (!table) return
    const text = tableToText(editor, table.node)
    const write = navigator.clipboard?.writeText(text)
    void (write ?? Promise.reject(new Error('clipboard unavailable'))).then(() => toast.success(t('Table copied')), () => toast.error(t('Could not copy table')))
  }
  const menuProps = (name: 'actions' | 'block') => ({
    open: openMenu === name,
    onOpenChange: (next: boolean) => setOpenMenu(next ? name : current => current === name ? null : current),
    modal: false,
  })
  return <div ref={chromeRef} className="flow-table__chrome" data-open={openMenu ?? undefined} contentEditable={false}>
    <button type="button" className="flow-table__add-row" aria-label={t('Add row')} title={t('Add row')} tabIndex={-1} onMouseDown={keepSelection} onClick={addRow}><Plus size={14} aria-hidden/></button>
    <button type="button" className="flow-table__add-column" aria-label={t('Add column')} title={t('Add column')} tabIndex={-1} onMouseDown={keepSelection} onClick={addColumn}><Plus size={14} aria-hidden/></button>
    <TableLineHandles editor={editor} getPos={getPos} onMenuChange={open => setOpenMenu(open ? 'lines' : current => current === 'lines' ? null : current)}/>
    <DropdownMenu {...menuProps('actions')}>
      <DropdownMenuTrigger asChild>
        <button type="button" className="flow-table__actions" aria-label={t('Table actions')} title={t('Table actions')} onMouseDown={keepSelection}><Ellipsis size={16} aria-hidden/></button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="flow-table__menu" align="end" sideOffset={4} onCloseAutoFocus={event => event.preventDefault()}>
        {ACTIONS.map((action, index) => <Fragment key={action.id}>
          {index === 4 || index === 6 ? <DropdownMenuSeparator/> : null}
          <DropdownMenuItem className={action.danger ? 'flow-table__menu-danger' : undefined} onSelect={() => runInTable(editor, getPos(), 'keep', action.run)}>
            <action.icon size={16} aria-hidden/>{t(action.label)}
          </DropdownMenuItem>
        </Fragment>)}
      </DropdownMenuContent>
    </DropdownMenu>
    <DropdownMenu {...menuProps('block')}>
      <DropdownMenuTrigger asChild>
        <button type="button" className="flow-table__handle" aria-label={t('Table menu')} title={t('Table menu')} onMouseDown={keepSelection}><HandleDotsIcon rotate={90}/></button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="flow-table__block-menu" align="end" side="bottom" sideOffset={4} onCloseAutoFocus={event => event.preventDefault()}>
        <DropdownMenuItem onSelect={copy}><TableCopyIcon/><span>{t('Copy')}</span></DropdownMenuItem>
        <DropdownMenuItem onSelect={() => { selectWholeTable(editor, getPos()) }}><TableSelectIcon/><span>{t('Select')}</span></DropdownMenuItem>
        <DropdownMenuSeparator/>
        <DropdownMenuItem onSelect={() => { deleteWholeTable(editor, getPos()) }}><TableTrashIcon/><span>{t('Delete')}</span></DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
}
