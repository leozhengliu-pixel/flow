import { ArrowUp, Paperclip } from 'lucide-react'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'
import { Button } from '@/components/ui/button'
import { createDraft, deleteDraft, updateDraft } from '@/lib/api'
import { clearComposerDraft, readComposerDraft, writeComposerDraft, type ComposerDraftType } from '@/lib/composer-drafts'
import type { Draft, User } from '@/types/flow'
import type { Editor } from '@tiptap/react'
import { usePeopleDirectory } from '@/components/property/people-context'
import { MentionExtension } from '@/components/issue/editor/mention-extension'
import { createMentionHydrationExtension } from '@/components/issue/editor/mention-hydration'
import { EntityStoreContext } from '@/store'
import { MentionMenu } from '@/components/issue/editor/mention-menu'
import { personSearchText } from '@/lib/people'
import { DescriptionImage, insertImageFiles } from '@/components/issue/editor/image-extension'
import { DescriptionFile, DescriptionVideo, insertEmbedFiles } from '@/components/issue/editor/file-extension'
import '@/components/issue/issue-description-editor.css'
import { useContext, useEffect, useMemo, useRef, useState } from 'react'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { handleEmoticonInput } from './emoticon-input'

export function Composer({ placeholder = 'Leave a comment…', initialValue = '', initialData, compact = false, onCancel, onSubmit, onAttach, onUpload, draftType, draftResourceId, drafts = [], draftTitle = '', draftMetadata, users }: { users?:User[]; placeholder?: string; initialValue?: string; initialData?: Record<string,unknown>; compact?: boolean; onCancel?: () => void; onSubmit?: (body: string, bodyData?: Record<string, unknown>) => Promise<void>; onAttach?:()=>void; onUpload?: (file: File) => Promise<string>; draftType?: ComposerDraftType; draftResourceId?: string; drafts?: Draft[]; draftTitle?: string; draftMetadata?: Record<string, unknown> }) {
  const directory=usePeopleDirectory()
  const people=useRef<User[]>([]);people.current=(users??[...directory.users.values()]).filter(user=>user.active&&(!user.app||user.appScopes?.includes('app:mentionable')))
  const [mention,setMention]=useState<{query:string;from:number;to:number;index:number}>()
  const mentionRef=useRef(mention);mentionRef.current=mention
  const matching=(query:string)=>people.current.filter(user=>personSearchText(user).toLocaleLowerCase().includes(query.toLocaleLowerCase())).slice(0,8)
  const updateMention=(editor:Editor)=>{const {$from,empty}=editor.state.selection;const match=empty&&$from.parent.type.name!=='codeBlock'?$from.parent.textBetween(0,$from.parentOffset,' ',' ').match(/(?:^|\s)@([^\s@]*)$/):null;const next=match?{query:match[1],from:$from.pos-match[1].length-1,to:$from.pos,index:0}:undefined;mentionRef.current=next;setMention(next)}
  const insertMention=(editor:Editor,user:User)=>{const range=mentionRef.current;if(!range)return;editor.chain().focus().insertContentAt({from:range.from,to:range.to},[{type:'mention',attrs:{id:user.id,label:user.displayName||user.name}},{type:'text',text:' '}]).run();mentionRef.current=undefined;setMention(undefined)}
  const persistedDraft = useMemo(() => draftType && draftResourceId ? drafts.find(item => item.type === draftType && item.resourceId === draftResourceId) ?? readComposerDraft(draftType, draftResourceId) : undefined, [draftResourceId, draftType, drafts])
  const initialBody = persistedDraft?.body ?? initialValue
  const initialDocument = persistedDraft?.metadata?.bodyData as Record<string, unknown> | undefined ?? initialData
  const draftDocument = useRef(initialDocument)
  const onUploadRef = useRef(onUpload)
  onUploadRef.current = onUpload
  const [saving, setSaving] = useState(false), [error, setError] = useState(''), [empty, setEmpty] = useState(!commentSendable(initialBody, initialDocument)), [draftBody, setDraftBody] = useState(initialBody)
  const draftId = useRef(persistedDraft?.id ?? '')
  const workspaceStore = useContext(EntityStoreContext)
  const mentionHydration = createMentionHydrationExtension(() => workspaceStore)
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [StarterKit.configure({ heading: false }), MentionExtension, mentionHydration, DescriptionImage, DescriptionFile, DescriptionVideo, Placeholder.configure({ placeholder })],
    content: initialDocument?.type === 'doc' ? initialDocument : initialBody || { type: 'doc', content: [{ type: 'paragraph' }] },
    editorProps: { handleTextInput: handleEmoticonInput, attributes: { class: 'comment-prosemirror', role: 'textbox', 'aria-label': placeholder, 'aria-multiline': 'true' }, handleKeyDown: (_view, event) => { const current=mentionRef.current;if(current&&!event.isComposing){const options=matching(current.query);if(event.key==='Escape'){setMention(undefined);mentionRef.current=undefined;return true}if(options.length&&(event.key==='ArrowDown'||event.key==='ArrowUp')){const next={...current,index:(current.index+(event.key==='ArrowDown'?1:-1)+options.length)%options.length};mentionRef.current=next;setMention(next);event.preventDefault();return true}if(options.length&&(event.key==='Enter'||event.key==='Tab')&&editor){event.preventDefault();insertMention(editor,options[current.index]??options[0]);return true}} if (!event.isComposing && commentShortcutMatches(event)) { event.preventDefault(); void submit(); return true } if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel(); return true } return false } },
    onUpdate: ({editor}) => { const json = editor.getJSON() as Record<string, unknown>; draftDocument.current = json; const text = editor.getText({ blockSeparator: '\n' }); setEmpty(!commentSendable(text, json)); setDraftBody(text);updateMention(editor) },
    onSelectionUpdate:({editor})=>updateMention(editor),
  })
  useEffect(() => {
    if (!draftType || !draftResourceId || !commentSendable(draftBody, draftDocument.current) || saving) return
    const timer = window.setTimeout(() => {
      const input = { type: draftType, resourceId: draftResourceId, title: draftTitle, body: commentBody(draftBody, draftDocument.current), metadata: { ...draftMetadata, bodyData: draftDocument.current } }
      const save = async () => {
        if (!draftId.current) return createDraft(input)
        try { return await updateDraft(draftId.current, input) } catch { draftId.current = ''; return createDraft(input) }
      }
      void save().then(saved => { draftId.current = saved.id; writeComposerDraft({ id: saved.id, type: draftType, resourceId: draftResourceId, title: draftTitle, body: input.body, metadata: input.metadata, updatedAt: saved.updatedAt }) }).catch(() => undefined)
    }, 350)
    return () => window.clearTimeout(timer)
  }, [draftBody, draftMetadata, draftResourceId, draftTitle, draftType, saving])
  useEffect(() => {
    if (!editor) return
    editor.storage.image.upload = file => {
      const upload = onUploadRef.current
      if (!upload) return Promise.reject(new Error('Upload unavailable'))
      return upload(file)
    }
  }, [editor])
  const attachFiles = () => {
    if (onUploadRef.current && editor) {
      pickComposerFiles(files => {
        const images = files.filter(file => file.type.startsWith('image/'))
        const rest = files.filter(file => !file.type.startsWith('image/'))
        if (images.length) insertImageFiles(editor.view, images, editor.storage.image.upload)
        if (rest.length) insertEmbedFiles(editor.view, rest, editor.storage.image.upload)
      })
      return
    }
    onAttach?.()
  }
  const submit = async () => { const json = editor?.getJSON() as Record<string, unknown> | undefined; const text = editor?.getText({ blockSeparator: '\n' }) ?? ''; const body = commentBody(text, json); if (!body || saving) return; setSaving(true); setError(''); try { await onSubmit?.(body, json); if (draftType && draftResourceId) { if (draftId.current && !draftId.current.startsWith('local:')) await deleteDraft(draftId.current).catch(() => undefined); clearComposerDraft(draftType, draftResourceId); draftId.current = '' } editor?.commands.clearContent() } catch (reason) { setError(reason instanceof Error ? reason.message : 'Comment could not be submitted') } finally { setSaving(false) } }
  return <div className={`composer${compact ? ' compact' : ''}`} style={{position:'relative'}}>
    <EditorContent editor={editor}/>
    {error && <div className="composer-error" role="alert">{error}<button type="button" onClick={() => void submit()}>Retry</button></div>}
    <div className="composer-toolbar"><div className="composer-tools">
      <button type="button" aria-label="Attach images, files, or videos" onClick={attachFiles}><Paperclip size={14}/></button>
    </div><div className="composer-submit">{onCancel && <Button className="composer-cancel" type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>}<Button className="composer-send" type="button" size="icon" aria-label="Submit comment" disabled={saving || empty} onClick={() => void submit()}><ArrowUp size={14}/></Button></div></div>
    {mention&&editor&&<MentionMenu users={matching(mention.query)} selectedIndex={mention.index} position={{left:8,top:38}} query={mention.query} onSelect={user=>insertMention(editor,user)}/>}
  </div>
}

function commentSendable(text?: string, document?: Record<string, unknown>) {
  return Boolean(text?.trim() || commentMediaMarkdown(document))
}

function commentBody(text?: string, document?: Record<string, unknown>) {
  return text?.trim() || commentMediaMarkdown(document)
}

function commentMediaMarkdown(node?: unknown) {
  const parts: string[] = []
  const walk = (value: unknown) => {
    if (!value || typeof value !== 'object') return
    const current = value as { type?: string; attrs?: { src?: string; alt?: string; title?: string; size?: unknown; contentType?: string }; content?: unknown[] }
    const src = current.attrs?.src
    if (src && !src.startsWith('blob:') && !src.startsWith('data:')) {
      if (current.type === 'image') parts.push(`![${current.attrs?.alt || ''}](${src})`)
      if (current.type === 'video') parts.push(`::video[${current.attrs?.title || ''}](${src})`)
      if (current.type === 'file') parts.push(`::file[${current.attrs?.title || 'File'}](${src}){size="${current.attrs?.size ?? ''}" type="${current.attrs?.contentType ?? ''}"}`)
    }
    current.content?.forEach(walk)
  }
  walk(node)
  return parts.join('\n')
}

function pickComposerFiles(onFiles: (files: File[]) => void) {
  const input = document.createElement('input')
  input.type = 'file'
  input.multiple = true
  input.accept = 'image/*,video/*,*/*'
  input.addEventListener('change', () => {
    onFiles([...input.files ?? []])
    input.remove()
  })
  input.click()
}
