import { Bold, Braces, ChevronDown, Code2, Heading2, Heading3, Italic, Link2, List, Pilcrow, Quote, Strikethrough, Underline as UnderlineIcon } from 'lucide-react'
import type { Editor } from '@tiptap/react'
import { useRef, useState } from 'react'
import { LinkEditor } from './link-editor'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'

export function SelectionToolbar({ editor }: { editor: Editor }) {
  const [blockOpen, setBlockOpen] = useState(false)
  const [linkOpen, setLinkOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const blocks = [
    { id: 'paragraph', label: 'Regular text', hint: '', icon: Pilcrow, active: editor.isActive('paragraph'), run: () => editor.chain().focus().setParagraph().run() },
    { id: 'h2', label: 'Heading 2', hint: '##', icon: Heading2, active: editor.isActive('heading', { level: 2 }), run: () => editor.chain().focus().toggleHeading({ level: 2 }).run() },
    { id: 'h3', label: 'Heading 3', hint: '###', icon: Heading3, active: editor.isActive('heading', { level: 3 }), run: () => editor.chain().focus().toggleHeading({ level: 3 }).run() },
  ]
  const current = blocks.find(block => block.active) ?? blocks[0]
  const CurrentIcon = current.icon
  const closeLink = () => { setLinkOpen(false); editor.commands.focus() }
  useDismissibleLayer({
    open: blockOpen || linkOpen,
    refs: [rootRef],
    onDismiss: reason => {
      setBlockOpen(false)
      setLinkOpen(false)
      if (reason === 'escape') editor.commands.focus()
    },
  })
  return <div className="description-bubble-menu" ref={rootRef}>
    <div className="description-block-control">
      <button type="button" aria-label={current.label} aria-expanded={blockOpen} onMouseDown={event => event.preventDefault()} onClick={() => { setLinkOpen(false); setBlockOpen(value => !value) }}><CurrentIcon size={15}/><ChevronDown size={12}/></button>
      {blockOpen && <div className="description-block-menu" role="listbox" aria-label="Text style">{blocks.map(block => { const Icon = block.icon; return <button key={block.id} type="button" role="option" aria-selected={block.active} onMouseDown={event => event.preventDefault()} onClick={() => { setBlockOpen(false); block.run() }}><Icon size={15}/><span>{block.label}</span>{block.hint && <kbd>{block.hint}</kbd>}</button> })}</div>}
    </div>
    <FormatButton label="Bold" active={editor.isActive('bold')} onPress={() => editor.chain().focus().toggleBold().run()}><Bold size={15}/></FormatButton>
    <FormatButton label="Italic" active={editor.isActive('italic')} onPress={() => editor.chain().focus().toggleItalic().run()}><Italic size={15}/></FormatButton>
    <FormatButton label="Strikethrough" active={editor.isActive('strike')} onPress={() => editor.chain().focus().toggleStrike().run()}><Strikethrough size={15}/></FormatButton>
    <FormatButton label="Underline" active={editor.isActive('underline')} onPress={() => editor.chain().focus().toggleUnderline().run()}><UnderlineIcon size={15}/></FormatButton>
    <div className="description-link-control">
      <FormatButton label="Link" active={editor.isActive('link') || linkOpen} onPress={() => { setBlockOpen(false); setLinkOpen(value => !value) }}><Link2 size={15}/></FormatButton>
      {linkOpen && <LinkEditor initialValue={(editor.getAttributes('link').href as string | undefined) ?? ''} onApply={href => { editor.chain().focus().extendMarkRange('link').setLink({ href }).run(); closeLink() }} onRemove={() => { editor.chain().focus().extendMarkRange('link').unsetLink().run(); closeLink() }} onClose={closeLink}/>}
    </div>
    <span className="description-toolbar-separator"/>
    <FormatButton label="Quote" active={editor.isActive('blockquote')} onPress={() => editor.chain().focus().toggleBlockquote().run()}><Quote size={15}/></FormatButton>
    <FormatButton label="Inline code" active={editor.isActive('code')} onPress={() => editor.chain().focus().toggleCode().run()}><Braces size={15}/></FormatButton>
    <FormatButton label="Code block" active={editor.isActive('codeBlock')} onPress={() => editor.chain().focus().toggleCodeBlock().run()}><Code2 size={15}/></FormatButton>
    <FormatButton label="Bulleted list" active={editor.isActive('bulletList')} onPress={() => editor.chain().focus().toggleBulletList().run()}><List size={15}/></FormatButton>
  </div>
}

function FormatButton({ label, active = false, onPress, children }: { label: string; active?: boolean; onPress: () => void; children: React.ReactNode }) {
  return <button type="button" aria-label={label} aria-pressed={active} onMouseDown={event => event.preventDefault()} onClick={onPress}>{children}</button>
}
