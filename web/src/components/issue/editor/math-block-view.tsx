import * as Dialog from '@radix-ui/react-dialog'
import { Copy, Code2, EllipsisVertical, Maximize, Trash2, X } from 'lucide-react'
import { NodeViewContent, NodeViewWrapper, useEditorState, type ReactNodeViewProps } from '@tiptap/react'
import { useState } from 'react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useOptionalTranslate } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import { MathRender } from './math-render'

export function MathBlockView({ editor, node, getPos, deleteNode }: ReactNodeViewProps) {
  const t = useOptionalTranslate()
  const [fullscreen, setFullscreen] = useState(false)
  const [sourceVisible, setSourceVisible] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const editing = useEditorState({ editor, selector: ({ editor: current }) => {
    const pos = getPos()
    const { selection } = current.state
    return current.isEditable && current.isFocused && typeof pos === 'number' && selection.from > pos && selection.to < pos + node.nodeSize
  } })
  const showSource = editing || sourceVisible
  const focusSource = () => {
    if (!editor.isEditable) { setSourceVisible(visible => !visible); return }
    const pos = getPos()
    if (typeof pos === 'number') editor.chain().setTextSelection(pos + node.nodeSize - 1).focus().run()
  }
  const copy = async () => {
    try { await navigator.clipboard.writeText(node.textContent); toast.success(t('Copied to clipboard')) }
    catch { toast.error(t('Could not copy')) }
  }
  return <NodeViewWrapper className="math-block-container" data-editing={showSource || undefined} data-menu-open={menuOpen || undefined} data-i18n-ignore>
    <div className="math-block-actions" contentEditable={false}>
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen} modal={false}>
        <DropdownMenuTrigger asChild><button type="button" aria-label={t('Formula actions')} title={t('Formula actions')} onMouseDown={event => event.preventDefault()}><EllipsisVertical size={16}/></button></DropdownMenuTrigger>
        <DropdownMenuContent align="end" onCloseAutoFocus={event => event.preventDefault()}>
          <DropdownMenuItem onSelect={() => void copy()}><Copy size={16}/>{t('Copy formula')}</DropdownMenuItem>
          <DropdownMenuItem onSelect={focusSource}><Code2 size={16}/>{t('Show source')}</DropdownMenuItem>
          <DropdownMenuSeparator/>
          <DropdownMenuItem onSelect={() => setFullscreen(true)}><Maximize size={16}/>{t('Fullscreen')}</DropdownMenuItem>
          {editor.isEditable && <DropdownMenuItem onSelect={deleteNode}><Trash2 size={16}/>{t('Delete')}</DropdownMenuItem>}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
    <pre className="math-block-source" spellCheck={false}><NodeViewContent<'code'> as="code"/></pre>
    {!showSource && <div contentEditable={false} onMouseDown={event => { if (editor.isEditable) { event.preventDefault(); focusSource() } }}><MathRender source={node.textContent}/></div>}
    <Dialog.Root open={fullscreen} onOpenChange={setFullscreen}>
      <Dialog.Portal>
        <Dialog.Overlay className="math-block-fullscreen-overlay" data-flow-motion="backdrop"/>
        <Dialog.Content className="math-block-fullscreen" aria-describedby={undefined} data-flow-motion="dialog" data-i18n-ignore>
          <Dialog.Title className="sr-only">{t('LaTeX math block')}</Dialog.Title>
          <Dialog.Close asChild><button type="button" aria-label={t('Close')} title={t('Close')}><X size={18}/></button></Dialog.Close>
          <MathRender source={node.textContent}/>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </NodeViewWrapper>
}
