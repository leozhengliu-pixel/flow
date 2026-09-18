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
import { MentionMenu } from '@/components/issue/editor/mention-menu'
import { personSearchText } from '@/lib/people'
import '@/components/issue/issue-description-editor.css'
import { useEffect, useMemo, useRef, useState } from 'react'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { handleEmoticonInput } from './emoticon-input'

export function Composer({ placeholder = 'Leave a comment…', initialValue = '', initialData, compact = false, onCancel, onSubmit, onAttach, draftType, draftResourceId, drafts = [], draftTitle = '', draftMetadata, users }: { users?:User[]; placeholder?: string; initialValue?: string; initialData?: Record<string,unknown>; compact?: boolean; onCancel?: () => void; onSubmit?: (body: string, bodyData?: Record<string, unknown>) => Promise<void>; onAttach?:()=>void; draftType?: ComposerDraftType; draftResourceId?: string; drafts?: Draft[]; draftTitle?: string; draftMetadata?: Record<string, unknown> }) {
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
  const [saving, setSaving] = useState(false), [error, setError] = useState(''), [empty, setEmpty] = useState(!initialBody.trim()), [draftBody, setDraftBody] = useState(initialBody)
  const draftId = useRef(persistedDraft?.id ?? '')
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [StarterKit.configure({ heading: false }), MentionExtension, Placeholder.configure({ placeholder })],
    content: initialDocument?.type === 'doc' ? initialDocument : initialBody || { type: 'doc', content: [{ type: 'paragraph' }] },
    editorProps: { handleTextInput: handleEmoticonInput, attributes: { class: 'comment-prosemirror', role: 'textbox', 'aria-label': placeholder, 'aria-multiline': 'true' }, handleKeyDown: (_view, event) => { const current=mentionRef.current;if(current&&!event.isComposing){const options=matching(current.query);if(event.key==='Escape'){setMention(undefined);mentionRef.current=undefined;return true}if(options.length&&(event.key==='ArrowDown'||event.key==='ArrowUp')){const next={...current,index:(current.index+(event.key==='ArrowDown'?1:-1)+options.length)%options.length};mentionRef.current=next;setMention(next);event.preventDefault();return true}if(options.length&&(event.key==='Enter'||event.key==='Tab')&&editor){event.preventDefault();insertMention(editor,options[current.index]??options[0]);return true}} if (!event.isComposing && commentShortcutMatches(event)) { event.preventDefault(); void submit(); return true } if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel(); return true } return false } },
    onUpdate: ({editor}) => { draftDocument.current = editor.getJSON(); setEmpty(editor.isEmpty); setDraftBody(editor.getText({ blockSeparator: '\n' }));updateMention(editor) },
    onSelectionUpdate:({editor})=>updateMention(editor),
  })
  useEffect(() => {
    if (!draftType || !draftResourceId || !draftBody.trim() || saving) return
    const timer = window.setTimeout(() => {
      const input = { type: draftType, resourceId: draftResourceId, title: draftTitle, body: draftBody.trim(), metadata: { ...draftMetadata, bodyData: draftDocument.current } }
      const save = async () => {
        if (!draftId.current) return createDraft(input)
        try { return await updateDraft(draftId.current, input) } catch { draftId.current = ''; return createDraft(input) }
      }
      void save().then(saved => { draftId.current = saved.id; writeComposerDraft({ id: saved.id, type: draftType, resourceId: draftResourceId, title: draftTitle, body: draftBody.trim(), metadata: input.metadata, updatedAt: saved.updatedAt }) }).catch(() => undefined)
    }, 350)
    return () => window.clearTimeout(timer)
  }, [draftBody, draftMetadata, draftResourceId, draftTitle, draftType, saving])
  const submit = async () => { const body = editor?.getText({ blockSeparator: '\n' }).trim() ?? ''; if (!body || saving) return; setSaving(true); setError(''); try { await onSubmit?.(body, editor?.getJSON() as Record<string, unknown>); if (draftType && draftResourceId) { if (draftId.current && !draftId.current.startsWith('local:')) await deleteDraft(draftId.current).catch(() => undefined); clearComposerDraft(draftType, draftResourceId); draftId.current = '' } editor?.commands.clearContent() } catch (reason) { setError(reason instanceof Error ? reason.message : 'Comment could not be submitted') } finally { setSaving(false) } }
  return <div className={`composer${compact ? ' compact' : ''}`} style={{position:'relative'}}>
    <EditorContent editor={editor}/>
    {error && <div className="composer-error" role="alert">{error}<button type="button" onClick={() => void submit()}>Retry</button></div>}
    <div className="composer-toolbar"><div className="composer-tools">
      <button type="button" aria-label="Attach images, files, or videos" onClick={onAttach}><Paperclip size={14}/></button>
    </div><div className="composer-submit">{onCancel && <Button className="composer-cancel" type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>}<Button className="composer-send" type="button" size="icon" aria-label="Submit comment" disabled={saving || empty} onClick={() => void submit()}><ArrowUp size={14}/></Button></div></div>
    {mention&&editor&&<MentionMenu users={matching(mention.query)} selectedIndex={mention.index} position={{left:8,top:38}} query={mention.query} onSelect={user=>insertMention(editor,user)}/>}
  </div>
}
