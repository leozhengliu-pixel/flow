import { ArrowUp, Paperclip } from 'lucide-react'
import { AttachmentIcon, CommentSendIcon } from '@/components/issue/issue-icons'
import Placeholder from '@tiptap/extension-placeholder'
import { ClipboardOrigin } from '@/components/editor/clipboard-origin'
import { FlowStarterKit } from '@/components/editor/flow-starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import { SelectionToolbar } from '@/components/issue/editor/selection-toolbar'
import { showsSelectionToolbar } from '@/components/issue/editor/selection-toolbar-visibility'
import { handleSelectionShortcut } from '@/components/issue/editor/selection-shortcuts'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { isMathInput, MathBlock } from '@/components/issue/editor/math-extension'
import { Markdown } from '@tiptap/markdown'
import { MarkdownPaste } from './markdown-paste'
import { useComposerSelectionActions } from './selection-issue'
import { Button } from '@/components/ui/button'
import { createDraft, deleteDraft, updateDraft } from '@/lib/api'
import { clearComposerDraft, readComposerDraft, writeComposerDraft, type ComposerDraftType } from '@/lib/composer-drafts'
import type { Draft, User } from '@/types/flow'
import type { Editor } from '@tiptap/react'
import { usePeopleDirectory } from '@/components/property/people-context'
import { MentionChipNode } from '@/components/editor/mentions/mention-chip-extension'
import { MentionLinksExtension } from '@/components/editor/mentions/mention-links-extension'
import { mentionMarkdown } from '@/components/editor/mentions/mention-model'
import { insertMentionOption, type MentionOption } from '@/components/editor/mentions/mention-options'
import { LinkBehavior } from '@/components/editor/link-behavior'
import { EmbedPasteNode } from '@/components/editor/embeds/embed-extension'
import { useMentionOptions } from '@/components/editor/mentions/use-mention-options'
import { useAgentEntityData } from '@/components/agent/agent-entity-data'
import { MentionMenu } from '@/components/issue/editor/mention-menu'
import { DescriptionImage, insertImageFiles } from '@/components/issue/editor/image-extension'
import { DescriptionFile, DescriptionVideo, insertEmbedFiles } from '@/components/issue/editor/file-extension'
import '@/components/issue/issue-description-editor.css'
import { useEffect, useInsertionEffect, useMemo, useRef, useState } from 'react'
import { useLatestRef } from '@/hooks/use-latest-ref'
import { bindImageUpload } from './image-upload-binding'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { handleEmoticonInput } from './emoticon-input'
import { useI18n } from '@/i18n/i18n'

export function Composer({ placeholder = 'Leave a comment…', initialValue = '', initialData, compact = false, mobileIssue = false, onCancel, onSubmit, onAttach, onUpload, draftType, draftResourceId, drafts = [], draftTitle = '', draftMetadata, users }: { users?:User[]; placeholder?: string; initialValue?: string; initialData?: Record<string,unknown>; compact?: boolean; mobileIssue?: boolean; onCancel?: () => void; onSubmit?: (body: string, bodyData?: Record<string, unknown>) => Promise<void>; onAttach?:()=>void; onUpload?: (file: File) => Promise<string>; draftType?: ComposerDraftType; draftResourceId?: string; drafts?: Draft[]; draftTitle?: string; draftMetadata?: Record<string, unknown> }) {
  const { t } = useI18n()
  const directory=usePeopleDirectory()
  const mentionUsers=(users??[...directory.users.values()]).filter(user=>user.active&&(!user.app||user.appScopes?.includes('app:mentionable')))
  const [mention,setMention]=useState<{query:string;from:number;to:number;index:number}>()
  const mentionRef=useLatestRef(mention)
  const entityData=useAgentEntityData()
  const entityDataRef=useLatestRef(entityData)
  const [getEntityData]=useState(()=>()=>entityDataRef.current)
  const mentionOptionList=useMentionOptions({active:Boolean(mention),query:mention?.query??'',users:mentionUsers})
  const optionsRef=useLatestRef(mentionOptionList)
  const updateMention=(editor:Editor)=>{const {$from,empty}=editor.state.selection;const match=empty&&!$from.parent.type.spec.code?$from.parent.textBetween(0,$from.parentOffset,' ',' ').match(/(?:^|\s)@([^\s@]*)$/):null;const next=match?{query:match[1],from:$from.pos-match[1].length-1,to:$from.pos,index:0}:undefined;mentionRef.current=next;setMention(next)}
  const insertMention=(editor:Editor,option:MentionOption)=>{const range=mentionRef.current;if(!range)return;insertMentionOption(editor.view,range,option);editor.commands.focus();mentionRef.current=undefined;setMention(undefined)}
  const persistedDraft = useMemo(() => draftType && draftResourceId ? drafts.find(item => item.type === draftType && item.resourceId === draftResourceId) ?? readComposerDraft(draftType, draftResourceId) : undefined, [draftResourceId, draftType, drafts])
  const initialBody = persistedDraft?.body ?? initialValue
  const initialDocument = persistedDraft?.metadata?.bodyData as Record<string, unknown> | undefined ?? initialData
  const draftDocument = useRef(initialDocument)
  const onUploadRef = useLatestRef(onUpload)
  const [saving, setSaving] = useState(false), [error, setError] = useState(''), [empty, setEmpty] = useState(!commentSendable(initialBody, initialDocument)), [draftBody, setDraftBody] = useState(initialBody)
  const draftId = useRef(persistedDraft?.id ?? '')
  // Like the reference app, selecting text in a comment offers "Create issue from selection" and "Ask agent" on the formatting toolbar.
  const selectionActions = useComposerSelectionActions()
  const selectionActionsRef = useLatestRef(selectionActions)
  const editorRef = useRef<Editor | null>(null)
  // Comment boxes sit in cards, popovers and scrolling panels that would clip the toolbar, so it floats in the page body
  // (fixed); inside a dialog (whose outside does not take clicks) it stays next to the editor.
  const rootRef = useRef<HTMLDivElement>(null)
  const [toolbarHost, setToolbarHost] = useState<'body' | 'editor'>()
  useEffect(() => { setToolbarHost(rootRef.current?.closest('[role="dialog"]') ? 'editor' : 'body') }, [])
  const editor = useEditor({
    immediatelyRender: false,
    // Headings, collapsible sections and checklists back the selection toolbar's text-style, collapse and list menus.
    extensions: [FlowStarterKit.configure({ heading: { levels: [1, 2, 3, 4] }, link: { openOnClick: false, autolink: true, linkOnPaste: true } }), LinkBehavior, ClipboardOrigin, MathBlock, Markdown, MarkdownPaste, ...structuredBlocks, EmbedPasteNode, MentionChipNode, MentionLinksExtension.configure({ getData: getEntityData }), DescriptionImage, DescriptionFile, DescriptionVideo, Placeholder.configure({ placeholder: t(placeholder) })],
    content: initialDocument?.type === 'doc' ? initialDocument : initialBody,
    contentType: initialDocument?.type === 'doc' ? 'json' : 'markdown',
    editorProps: { handleTextInput: handleEmoticonInput, attributes: { class: 'comment-prosemirror', role: 'textbox', 'aria-label': t(placeholder), 'aria-multiline': 'true' }, handleKeyDown: (_view, event) => { const current=mentionRef.current;if(current&&!event.isComposing){const options=optionsRef.current;if(event.key==='Escape'){setMention(undefined);mentionRef.current=undefined;return true}if(options.length&&(event.key==='ArrowDown'||event.key==='ArrowUp')){const next={...current,index:(current.index+(event.key==='ArrowDown'?1:-1)+options.length)%options.length};mentionRef.current=next;setMention(next);event.preventDefault();return true}if(options.length&&(event.key==='Enter'||event.key==='Tab')&&editor){event.preventDefault();insertMention(editor,options[current.index]??options[0]);return true}} if (!event.isComposing && !isMathInput(_view.state) && commentShortcutMatches(event)) { event.preventDefault(); void submit(); return true } if (handleSelectionShortcut(event, _view.state.selection, _view.state.doc, selectionActionsRef.current, editorRef.current)) return true; if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel(); return true } return false } },
    onUpdate: ({editor}) => { const json = editor.getJSON() as Record<string, unknown>; draftDocument.current = json; const text = commentText(editor); setEmpty(!commentSendable(text, json)); setDraftBody(text);updateMention(editor) },
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
  useInsertionEffect(() => { editorRef.current = editor })
  useEffect(() => {
    if (!editor) return
    bindImageUpload(editor, file => {
      const upload = onUploadRef.current
      if (!upload) return Promise.reject(new Error('Upload unavailable'))
      return upload(file)
    })
  }, [editor, onUploadRef])
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
  // A new comment leaves the composer the moment it is sent (the timeline shows it at once, pending); a failed send puts the
  // draft back, mentions and attachments included. An edit keeps its text until the save finishes.
  // An edit starts from the comment's own text; a restored draft is still a new comment.
  const editing = Boolean(initialValue || initialData)
  const failedSend = useRef<{ body: string; json?: Record<string, unknown> }>(undefined)
  const sending = useRef(false)
  const submit = async (retry = false) => {
    if (!editor) return
    const pending = retry ? failedSend.current : undefined
    const json = pending ? pending.json : editor.getJSON() as Record<string, unknown>
    const body = pending ? pending.body : commentBody(commentText(editor), json)
    const immediate = !editing
    if (!body || (!immediate && (saving || sending.current))) return
    failedSend.current = undefined
    setError('')
    const sentDraft = draftId.current
    if (immediate) { if (!pending) editor.commands.clearContent(); draftId.current = '' } else { sending.current = true; setSaving(true) }
    try {
      await onSubmit?.(body, json)
      if (draftType && draftResourceId) { if (sentDraft && !sentDraft.startsWith('local:')) await deleteDraft(sentDraft).catch(() => undefined); clearComposerDraft(draftType, draftResourceId); if (!immediate) draftId.current = '' }
      if (!immediate) editor.commands.clearContent()
    } catch (reason) {
      if (immediate) {
        if (!draftId.current) draftId.current = sentDraft
        if (editor.isEmpty && json?.type === 'doc') editor.commands.setContent(json, { emitUpdate: true })
        else failedSend.current = { body, json }
        editor.commands.focus('end')
      }
      setError(reason instanceof Error ? reason.message : 'Comment could not be submitted')
    } finally { if (!immediate) { sending.current = false; setSaving(false) } }
  }
  return <div className={`composer${compact ? ' compact' : ''}`} ref={rootRef} style={{position:'relative'}}>
    <EditorContent editor={editor}/>
    {editor && toolbarHost && <BubbleMenu editor={editor} appendTo={toolbarHost === 'body' ? () => document.body : undefined} style={{ zIndex: 690 }} options={{ strategy: toolbarHost === 'body' ? 'fixed' : 'absolute', placement: 'top', offset: 6, shift: { padding: 12 }, flip: true }} shouldShow={showsSelectionToolbar}><SelectionToolbar editor={editor} actions={selectionActions}/></BubbleMenu>}
    {error &&<div className="composer-error" role="alert">{error}<button type="button" onClick={() => void submit(Boolean(failedSend.current))}>Retry</button></div>}
    <div className="composer-toolbar"><div className="composer-tools">
      <button type="button" aria-label="Attach images, files, or videos" onClick={attachFiles}>{mobileIssue ? <AttachmentIcon size={14}/> : <Paperclip size={14}/>}</button>
    </div><div className="composer-submit">{onCancel && <Button className="composer-cancel" type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>}<Button className="composer-send" type="button" size="icon" aria-label="Submit comment" disabled={saving || empty} onClick={() => void submit()}>{mobileIssue ? <CommentSendIcon size={14}/> : <ArrowUp size={14}/>}</Button></div></div>
    {mention&&editor&&<MentionMenu options={mentionOptionList} selectedIndex={mention.index} anchor={caretAnchor(editor,mention.to)} query={mention.query} onSelect={option=>insertMention(editor,option)}/>}
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

function caretAnchor(editor: Editor, position: number) {
  try {
    const caret = editor.view.coordsAtPos(Math.min(position, editor.state.doc.content.size))
    return { left: caret.left, top: caret.top, bottom: caret.bottom }
  } catch {
    return { left: 8, top: 0, bottom: 38 }
  }
}

/** The comment's text body: plain text, with each mention as `@name` (people) or `[label](path)` (other resources) so agents and exports keep the references. */
function commentText(editor: Editor) {
  return editor.getText({ blockSeparator: '\n', textSerializers: { mention: ({ node }) => mentionMarkdown(node.attrs), mathBlock: ({ node }) => `$$\n${node.textContent}\n$$` } })
}
