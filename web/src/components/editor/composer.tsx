import { AtSign, Bold, Code2, Italic, Link2, Paperclip, Send } from 'lucide-react'
import * as Popover from '@radix-ui/react-popover'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'
import { Button } from '@/components/ui/button'
import { useState } from 'react'

export function Composer({ placeholder = 'Leave a comment…', initialValue = '', initialData, compact = false, onCancel, onSubmit, onAttach }: { placeholder?: string; initialValue?: string; initialData?: Record<string,unknown>; compact?: boolean; onCancel?: () => void; onSubmit?: (body: string, bodyData?: Record<string, unknown>) => Promise<void>; onAttach?:()=>void }) {
  const [saving, setSaving] = useState(false), [error, setError] = useState(''), [empty, setEmpty] = useState(!initialValue.trim())
  const [linkOpen, setLinkOpen] = useState(false), [linkUrl, setLinkUrl] = useState('')
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [StarterKit.configure({ heading: false }), Placeholder.configure({ placeholder })],
    content: initialData?.type === 'doc' ? initialData : initialValue || { type: 'doc', content: [{ type: 'paragraph' }] },
    editorProps: { attributes: { class: 'comment-prosemirror', 'aria-label': placeholder }, handleKeyDown: (_view, event) => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submit(); return true } if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel(); return true } return false } },
    onUpdate: ({editor}) => setEmpty(editor.isEmpty),
  })
  const submit = async () => { const body = editor?.getText({ blockSeparator: '\n' }).trim() ?? ''; if (!body || saving) return; setSaving(true); setError(''); try { await onSubmit?.(body, editor?.getJSON() as Record<string, unknown>); editor?.commands.clearContent() } catch (reason) { setError(reason instanceof Error ? reason.message : 'Comment could not be submitted') } finally { setSaving(false) } }
  const applyLink = () => { const href = linkUrl.trim(); if (!href) return; editor?.chain().focus().setLink({ href }).run(); setLinkOpen(false) }
  return <div className={`composer${compact ? ' compact' : ''}`}>
    <EditorContent editor={editor}/>
    {error && <div className="composer-error" role="alert">{error}<button type="button" onClick={() => void submit()}>Retry</button></div>}
    <div className="composer-toolbar"><div>
      <button type="button" aria-label="Bold" aria-pressed={editor?.isActive('bold')} onClick={() => editor?.chain().focus().toggleBold().run()}><Bold size={14}/></button>
      <button type="button" aria-label="Italic" aria-pressed={editor?.isActive('italic')} onClick={() => editor?.chain().focus().toggleItalic().run()}><Italic size={14}/></button>
      <button type="button" aria-label="Code" aria-pressed={editor?.isActive('code')} onClick={() => editor?.chain().focus().toggleCode().run()}><Code2 size={14}/></button>
      <Popover.Root open={linkOpen} onOpenChange={open => { setLinkOpen(open); if (open) setLinkUrl((editor?.getAttributes('link').href as string | undefined) ?? '') }}>
        <Popover.Trigger asChild><button type="button" aria-label="Link" aria-pressed={editor?.isActive('link')}><Link2 size={14}/></button></Popover.Trigger>
        <Popover.Portal><Popover.Content align="start" className="composer-link-popover" sideOffset={5}>
          <input autoFocus aria-label="Link URL" onChange={event => setLinkUrl(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); applyLink() } }} placeholder="Paste or type a link…" type="url" value={linkUrl}/>
          <button disabled={!linkUrl.trim()} onClick={applyLink} type="button">Apply</button>
        </Popover.Content></Popover.Portal>
      </Popover.Root>
      <button type="button" aria-label="Mention" onClick={()=>editor?.chain().focus().insertContent('@').run()}><AtSign size={14}/></button><button type="button" aria-label="Attach images, files, or videos" onClick={onAttach}><Paperclip size={14}/></button>
    </div><div className="composer-submit">{onCancel && <Button type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>}<Button type="button" size="icon" aria-label="Submit comment" disabled={saving || empty} onClick={() => void submit()}><Send size={13}/></Button></div></div>
  </div>
}
