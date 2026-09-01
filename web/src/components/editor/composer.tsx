import { AtSign, Bold, Code2, Italic, Link2, Paperclip, Send } from 'lucide-react'
import * as Popover from '@radix-ui/react-popover'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'
import { Button } from '@/components/ui/button'
import { createDraft, deleteDraft, updateDraft } from '@/lib/api'
import { clearComposerDraft, readComposerDraft, writeComposerDraft, type ComposerDraftType } from '@/lib/composer-drafts'
import type { Draft } from '@/types/flow'
import { useEffect, useMemo, useRef, useState } from 'react'

export function Composer({ placeholder = 'Leave a comment…', initialValue = '', initialData, compact = false, onCancel, onSubmit, onAttach, draftType, draftResourceId, drafts = [], draftTitle = '', draftMetadata }: { placeholder?: string; initialValue?: string; initialData?: Record<string,unknown>; compact?: boolean; onCancel?: () => void; onSubmit?: (body: string, bodyData?: Record<string, unknown>) => Promise<void>; onAttach?:()=>void; draftType?: ComposerDraftType; draftResourceId?: string; drafts?: Draft[]; draftTitle?: string; draftMetadata?: Record<string, unknown> }) {
  const persistedDraft = useMemo(() => draftType && draftResourceId ? drafts.find(item => item.type === draftType && item.resourceId === draftResourceId) ?? readComposerDraft(draftType, draftResourceId) : undefined, [draftResourceId, draftType, drafts])
  const initialBody = persistedDraft?.body ?? initialValue
  const [saving, setSaving] = useState(false), [error, setError] = useState(''), [empty, setEmpty] = useState(!initialBody.trim()), [draftBody, setDraftBody] = useState(initialBody)
  const draftId = useRef(persistedDraft?.id ?? '')
  const [linkOpen, setLinkOpen] = useState(false), [linkUrl, setLinkUrl] = useState('')
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [StarterKit.configure({ heading: false }), Placeholder.configure({ placeholder })],
    content: initialData?.type === 'doc' ? initialData : initialBody || { type: 'doc', content: [{ type: 'paragraph' }] },
    editorProps: { attributes: { class: 'comment-prosemirror', 'aria-label': placeholder }, handleKeyDown: (_view, event) => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submit(); return true } if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel(); return true } return false } },
    onUpdate: ({editor}) => { setEmpty(editor.isEmpty); setDraftBody(editor.getText({ blockSeparator: '\n' })) },
  })
  useEffect(() => {
    if (!draftType || !draftResourceId || !draftBody.trim() || saving) return
    const timer = window.setTimeout(() => {
      const input = { type: draftType, resourceId: draftResourceId, title: draftTitle, body: draftBody.trim(), metadata: draftMetadata }
      const save = async () => {
        if (!draftId.current) return createDraft(input)
        try { return await updateDraft(draftId.current, input) } catch { draftId.current = ''; return createDraft(input) }
      }
      void save().then(saved => { draftId.current = saved.id; writeComposerDraft({ id: saved.id, type: draftType, resourceId: draftResourceId, title: draftTitle, body: draftBody.trim(), metadata: draftMetadata, updatedAt: saved.updatedAt }) }).catch(() => undefined)
    }, 350)
    return () => window.clearTimeout(timer)
  }, [draftBody, draftMetadata, draftResourceId, draftTitle, draftType, saving])
  const submit = async () => { const body = editor?.getText({ blockSeparator: '\n' }).trim() ?? ''; if (!body || saving) return; setSaving(true); setError(''); try { await onSubmit?.(body, editor?.getJSON() as Record<string, unknown>); if (draftType && draftResourceId) { if (draftId.current && !draftId.current.startsWith('local:')) await deleteDraft(draftId.current).catch(() => undefined); clearComposerDraft(draftType, draftResourceId); draftId.current = '' } editor?.commands.clearContent() } catch (reason) { setError(reason instanceof Error ? reason.message : 'Comment could not be submitted') } finally { setSaving(false) } }
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
