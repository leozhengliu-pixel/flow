import { Bold, Braces, ChevronDown, Code2, Heading2, Heading3, Italic, Link2, List, ListOrdered, ListTodo, CaseSensitive, ChevronsDownUp, Plus, Bot, MessageSquare, Pilcrow, Quote, Strikethrough, Underline as UnderlineIcon } from 'lucide-react'
import type { Editor } from '@tiptap/react'
import { useRef, useState } from 'react'
import { useI18n } from '@/i18n/i18n'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import type { DescriptionSelectionActions } from './structured-blocks'
import { LinkEditor } from './link-editor'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'

export function SelectionToolbar({ editor, actions }: { editor: Editor; actions?: DescriptionSelectionActions }) {
  const { t } = useI18n()
  const [listOpen, setListOpen] = useState(false)
  const selection = () => { const { from, to } = editor.state.selection; return { from, to, text: editor.state.doc.textBetween(from, to, '\n') } }
  const lists = [
    {id:'bulletList',label:'Bulleted list',icon:List,run:()=>editor.chain().focus().toggleBulletList().run()},
    {id:'orderedList',label:'Numbered list',icon:ListOrdered,run:()=>editor.chain().focus().toggleOrderedList().run()},
    {id:'taskList',label:'Checklist',icon:ListTodo,run:()=>editor.chain().focus().toggleTaskList().run()},
  ]
  const [blockOpen, setBlockOpen] = useState(false)
  const [linkOpen, setLinkOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const blocks = [
    { id: 'paragraph', label: 'Regular text', hint: '', icon: Pilcrow, active: editor.isActive('paragraph'), run: () => editor.chain().focus().setParagraph().run() },
    { id: 'h2', label: 'Heading 2', hint: '##', icon: Heading2, active: editor.isActive('heading', { level: 2 }), run: () => editor.chain().focus().toggleHeading({ level: 2 }).run() },
    { id: 'h3', label: 'Heading 3', hint: '###', icon: Heading3, active: editor.isActive('heading', { level: 3 }), run: () => editor.chain().focus().toggleHeading({ level: 3 }).run() },
  ]
  const closeLink = () => { setLinkOpen(false); editor.commands.focus() }
  useDismissibleLayer({
    open: blockOpen || linkOpen || listOpen,
    refs: [rootRef],
    onDismiss: reason => {
      setListOpen(false)
      setBlockOpen(false)
      setLinkOpen(false)
      if (reason === 'escape') editor.commands.focus()
    },
  })
  return <TooltipProvider><div className="description-bubble-menu" role="toolbar" aria-label={t("Text formatting")} ref={rootRef}>
    <div className="description-block-control">
      <button type="button" aria-label={t('Text style')} title={t('Text style')} aria-expanded={blockOpen} onMouseDown={event => event.preventDefault()} onClick={() => { setListOpen(false); setLinkOpen(false); setBlockOpen(value => !value) }}><CaseSensitive size={16}/><ChevronDown size={12}/></button>
      {blockOpen && <div className="description-block-menu" role="listbox" aria-label={t("Text style")}>{blocks.map(block => { const Icon = block.icon; return <button key={block.id} type="button" role="option" aria-selected={block.active} onMouseDown={event => event.preventDefault()} onClick={() => { setBlockOpen(false); block.run() }}><Icon size={16}/><span>{t(block.label)}</span>{block.hint && <kbd>{block.hint}</kbd>}</button> })}</div>}
    </div>
    <FormatButton label="Bold" active={editor.isActive('bold')} onPress={() => editor.chain().focus().toggleBold().run()}><Bold size={16}/></FormatButton>
    <FormatButton label="Italic" active={editor.isActive('italic')} onPress={() => editor.chain().focus().toggleItalic().run()}><Italic size={16}/></FormatButton>
    <FormatButton label="Strikethrough" active={editor.isActive('strike')} onPress={() => editor.chain().focus().toggleStrike().run()}><Strikethrough size={16}/></FormatButton>
    <FormatButton label="Underline" active={editor.isActive('underline')} onPress={() => editor.chain().focus().toggleUnderline().run()}><UnderlineIcon size={16}/></FormatButton>
    <div className="description-link-control">
      <FormatButton label="Link" active={editor.isActive('link') || linkOpen} onPress={() => { setListOpen(false); setBlockOpen(false); setLinkOpen(value => !value) }}><Link2 size={16}/></FormatButton>
      {linkOpen && <LinkEditor initialValue={(editor.getAttributes('link').href as string | undefined) ?? ''} onApply={href => { editor.chain().focus().extendMarkRange('link').setLink({ href }).run(); closeLink() }} onRemove={() => { editor.chain().focus().extendMarkRange('link').unsetLink().run(); closeLink() }} onClose={closeLink}/>}
    </div>
    <span className="description-toolbar-separator"/>
    <FormatButton label="Quote" active={editor.isActive('blockquote')} onPress={() => editor.chain().focus().toggleBlockquote().run()}><Quote size={16}/></FormatButton>
    <FormatButton label="Collapse" active={editor.isActive("details")} onPress={() => editor.isActive("details") ? editor.chain().focus().unsetDetails().run() : editor.chain().focus().setDetails().run()}><ChevronsDownUp size={16}/></FormatButton>
    <FormatButton label="Inline code" active={editor.isActive('code')} onPress={() => editor.chain().focus().toggleCode().run()}><Braces size={16}/></FormatButton>
    <FormatButton label="Code block" active={editor.isActive('codeBlock')} onPress={() => editor.chain().focus().toggleCodeBlock().run()}><Code2 size={16}/></FormatButton>
    <div className="description-block-control"><button type="button" aria-label={t('List style')} title={t('List style')} aria-expanded={listOpen} onMouseDown={event=>event.preventDefault()} onClick={()=>{setBlockOpen(false);setLinkOpen(false);setListOpen(value=>!value)}}><List size={16}/><ChevronDown size={12}/></button>
      {listOpen&&<div className="description-block-menu" role="listbox" aria-label={t('List style')}>{lists.map(item=>{const Icon=item.icon;return <button key={item.id} type="button" role="option" aria-selected={editor.isActive(item.id)} onMouseDown={event=>event.preventDefault()} onClick={()=>{setListOpen(false);item.run()}}><Icon size={16}/><span>{t(item.label)}</span></button>})}</div>}
    </div>
    {(actions?.onCreateIssue||actions?.onAskAgent||actions?.onComment)&&<span className="description-toolbar-separator"/>}
    {actions?.onCreateIssue&&<FormatButton label="Create issue from selection" onPress={()=>actions.onCreateIssue?.(selection())}><Plus size={16}/></FormatButton>}
    {actions?.onAskAgent&&<FormatButton label="Ask agent" onPress={()=>actions.onAskAgent?.(selection())}><Bot size={16}/></FormatButton>}
    {actions?.onComment&&<FormatButton label="Comment" onPress={()=>actions.onComment?.(selection())}><MessageSquare size={16}/></FormatButton>}
  </div></TooltipProvider>
}

function FormatButton({ label, active = false, onPress, children }: { label: string; active?: boolean; onPress: () => void; children: React.ReactNode }) {
  const { t } = useI18n()
  return <FlowTooltip label={t(label)}><button type="button" aria-label={t(label)} aria-pressed={active} onMouseDown={event => event.preventDefault()} onClick={onPress}>{children}</button></FlowTooltip>
}
