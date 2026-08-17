import { AtSign, Bold, Code2, Italic, Link2, Paperclip, Send } from 'lucide-react'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'
import { Button } from '@/components/ui/button'
import { useState } from 'react'

export function Composer({ placeholder = 'Leave a comment…', initialValue = '', initialData, compact = false, onCancel, onSubmit, onAttach }: { placeholder?: string; initialValue?: string; initialData?: Record<string,unknown>; compact?: boolean; onCancel?: () => void; onSubmit?: (body: string, bodyData?: Record<string, unknown>) => Promise<void>; onAttach?:()=>void }) {
  const [saving, setSaving] = useState(false), [error, setError] = useState(''), [empty, setEmpty] = useState(!initialValue.trim())
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [StarterKit.configure({ heading: false }), Placeholder.configure({ placeholder })],
    content: initialData?.type === 'doc' ? initialData : initialValue || { type: 'doc', content: [{ type: 'paragraph' }] },
    editorProps: { attributes: { class: 'comment-prosemirror', 'aria-label': placeholder }, handleKeyDown: (_view, event) => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submit(); return true } if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel(); return true } return false } },
    onUpdate: ({editor}) => setEmpty(editor.isEmpty),
  })
  const submit = async () => { const body = editor?.getText({ blockSeparator: '\n' }).trim() ?? ''; if (!body || saving) return; setSaving(true); setError(''); try { await onSubmit?.(body, editor?.getJSON() as Record<string, unknown>); editor?.commands.clearContent() } catch (reason) { setError(reason instanceof Error ? reason.message : 'Comment could not be submitted') } finally { setSaving(false) } }
  return <div className={`composer${compact ? ' compact' : ''}`}>
    <EditorContent editor={editor}/>
    {error && <div className="composer-error" role="alert">{error}<button type="button" onClick={() => void submit()}>Retry</button></div>}
    <div className="composer-toolbar"><div>
      <button type="button" aria-label="Bold" aria-pressed={editor?.isActive('bold')} onClick={() => editor?.chain().focus().toggleBold().run()}><Bold size={14}/></button>
      <button type="button" aria-label="Italic" aria-pressed={editor?.isActive('italic')} onClick={() => editor?.chain().focus().toggleItalic().run()}><Italic size={14}/></button>
      <button type="button" aria-label="Code" aria-pressed={editor?.isActive('code')} onClick={() => editor?.chain().focus().toggleCode().run()}><Code2 size={14}/></button>
      <button type="button" aria-label="Link" onClick={() => { const href=prompt('Link URL','https://'); if(href)editor?.chain().focus().setLink({href}).run() }}><Link2 size={14}/></button>
      <button type="button" aria-label="Mention" onClick={()=>editor?.chain().focus().insertContent('@').run()}><AtSign size={14}/></button><button type="button" aria-label="Attach images, files, or videos" onClick={onAttach}><Paperclip size={14}/></button>
    </div><div className="composer-submit">{onCancel && <Button type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>}<Button type="button" size="icon" aria-label="Submit comment" disabled={saving || empty} onClick={() => void submit()}><Send size={13}/></Button></div></div>
  </div>
}
