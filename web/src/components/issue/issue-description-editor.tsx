import { ChevronsDownUp, Code2, Heading1, Heading2, Heading3, Heading4, Image as ImageIcon, Lightbulb, List, ListOrdered, ListTodo, Minus, Paperclip, Quote, Radical, Table2, Workflow } from 'lucide-react'
import Placeholder from '@tiptap/extension-placeholder'
import Collaboration from '@tiptap/extension-collaboration'
import CollaborationCaret from '@tiptap/extension-collaboration-caret'
import { ClipboardOrigin } from '@/components/editor/clipboard-origin'
import { FlowStarterKit } from '@/components/editor/flow-starter-kit'
import { TableKit } from '@tiptap/extension-table'
import { flowTableKit, insertDefaultTable, tableSlashCommands } from '@/components/editor/table'
import { FlowCodeBlock, FlowCodeBlockSchema } from '@/components/editor/code-block/code-block-extension'
import { LinkBehavior } from '@/components/editor/link-behavior'
import { MarkdownPaste } from '@/components/editor/markdown-paste'
import { OutlineMinimap } from '@/components/editor/outline/outline-minimap'
import { Markdown } from '@tiptap/markdown'
import { Editor as CoreEditor, getSchema, type Extensions } from '@tiptap/core'
import type { EditorState } from '@tiptap/pm/state'
import { handleEmoticonInput } from '@/components/editor/emoticon-input'
import { EditorContent, useEditor, type Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import { useEffect, useInsertionEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useLatestRef } from '@/hooks/use-latest-ref'
import { useStableCallback } from './editor/use-stable-callback'
import { descriptionDocumentJSON, parseDescriptionContent, sameDocument, serializeDescription, type DescriptionSnapshot } from './editor/editor-content'
import { prosemirrorJSONToYXmlFragment, ySyncPluginKey } from '@tiptap/y-tiptap'
import { applyUpdate, Doc as YDoc } from 'yjs'
import { getSlashCommandState, SlashCommandExtension, type SlashCommandState } from './editor/slash-command-extension'
import { SlashCommandMenu, type EditorCommand } from './editor/slash-command-menu'
import { filterEditorCommands } from './editor/editor-commands'
import { SelectionToolbar } from './editor/selection-toolbar'
import { showsSelectionToolbar } from './editor/selection-toolbar-visibility'
import { structuredBlocks, type DescriptionSelectionActions } from './editor/structured-blocks'
import { handleSelectionShortcut } from './editor/selection-shortcuts'
import { DescriptionImage, type DescriptionImageUpload } from './editor/image-extension'
import { DescriptionCallout, DescriptionCalloutSchema } from './editor/callout-extension'
import { DescriptionDiagram } from './editor/diagram-extension'
import { isMathInput, MathBlock, MathBlockSchema } from './editor/math-extension'
import { DescriptionFile, DescriptionVideo } from './editor/file-extension'
import { insertPickedFiles } from './editor/insert-picked-files'
import { MentionExtension } from './editor/mention-extension'
import { MentionChipNode } from '@/components/editor/mentions/mention-chip-extension'
import { MentionLinksExtension } from '@/components/editor/mentions/mention-links-extension'
import { insertMentionOption } from '@/components/editor/mentions/mention-options'
import { EmbedPasteNode, EmbedSchema } from '@/components/editor/embeds/embed-extension'
import { useMentionOptions } from '@/components/editor/mentions/use-mention-options'
import { useMentionConversion } from '@/components/editor/mentions/use-mention-conversion'
import { useAgentEntityData } from '@/components/agent/agent-entity-data'
import { InlineCommentMark } from './editor/inline-comment-mark'
import { MentionMenu } from './editor/mention-menu'
import { HeadingActions } from './editor/heading-actions'
import { useI18n } from '@/i18n/i18n'
import { handleBlockShortcut, handleEditorSubmit } from './editor/editor-keyboard'
import { EmptyLineHint } from './editor/empty-line-hint'
import { TrailingParagraph } from './editor/trailing-paragraph'
import { CollabEditing, CollabSyncBeforeAppend } from '@/components/editor/collab-editing'
import { IssueCollaborationProvider } from '@/lib/issue-collaboration'
import type { User } from '@/types/flow'
import { clearDescriptionRecovery, descriptionRecoveryKey, downloadDescriptionRecovery, readDescriptionRecovery, writeDescriptionRecovery } from './editor/description-recovery'
import './issue-description-editor.css'
import '@/components/editor/rich-text-body.css'

interface DescriptionEditorProps {
  /** Renders the content without editing (deleted documents). */
  readOnly?: boolean
  selectionActions?: DescriptionSelectionActions
  value: string
  loading?: boolean
  state?: string
  onChange?: (snapshot: DescriptionSnapshot) => void
  onBlur?: () => void
  onSubmit?: () => void
  editorRef?: (editor: Editor | null) => void
  extensions?: Extensions
  className?: string
  placeholder?: string
  ariaLabel?: string
  users?: User[]
  onInsertImage?: DescriptionImageUpload
  /** Shows the floating heading outline in the left gutter (documents). */
  outline?: boolean
  collaboration?: {
    workspaceKey: string
    issueId?: string
    documentId: string
    contentState?: string
    documentVersion?: number
    viewer: User
    onPersist: (snapshot: DescriptionSnapshot, updateIds: string[], expectedVersion: number) => Promise<void>
    onPresence?: (users: User[]) => void
    /** The viewer lost access while the document was open: reload the issue or document to show its access state. */
    onRevoked?: () => void
  }
}

const closedSlash: SlashCommandState = { active: false, query: '', range: null }
type MentionState = { active: boolean; query: string; range: { from: number; to: number } | null }
const closedMention: MentionState = { active: false, query: '', range: null }
const oversizedEditMessage = 'This change is too large to save. Download your local changes, then reload the page to keep editing.'
const revokedIssueMessage = 'You no longer have access to this issue. Unsaved changes can still be downloaded.'
const revokedDocumentMessage = 'You no longer have access to this document. Unsaved changes can still be downloaded.'
const readOnlyEditsMessage = 'You can no longer edit this. Unsaved changes can still be downloaded.'

export function IssueDescriptionEditor(props: DescriptionEditorProps) {
  if (props.loading) return <div className="issue-description-skeleton" aria-label="Loading issue description"/>
  const sessionKey = props.collaboration ? JSON.stringify([props.collaboration.viewer.id, props.collaboration.workspaceKey, props.collaboration.issueId ?? '', props.collaboration.documentId]) : 'local'
  return <DescriptionEditorSession key={sessionKey} {...props}/>
}

function DescriptionEditorSession({ value, state, onChange, onBlur, onSubmit, editorRef, extensions = [], className, collaboration, selectionActions, placeholder = 'Add description...', ariaLabel, users = [], onInsertImage, outline = false, readOnly = false }: DescriptionEditorProps) {
  const { t } = useI18n()
  const getPlaceholder = useStableCallback(() => t(placeholder))
  const getDocumentId = useStableCallback(() => collaboration?.issueId ?? collaboration?.documentId)
  const getHint = useStableCallback(() => t('Type / for commands…'))
  // The editor is contenteditable, which the page translator leaves alone: its label is translated here.
  const descriptionLabel = t(ariaLabel ?? 'Issue description')
  const initial = useMemo(() => parseDescriptionContent(value, state), []) // eslint-disable-line react-hooks/exhaustive-deps
  const rootRef = useRef<HTMLDivElement>(null)
  const commandsRef = useRef<EditorCommand[]>([])
  const translateRef = useLatestRef(t)
  const slashRef = useRef<SlashCommandState>(closedSlash)
  const selectedRef = useRef(0)
  const dismissedRef = useRef<number | null>(null)
  const dismissedMentionRef = useRef<number | null>(null)
  const submitRef = useLatestRef(onSubmit)
  const persistTimerRef = useRef<number | undefined>(undefined)
  const startTimerRef = useRef<number | undefined>(undefined)
  const destroyTimerRef = useRef<number | undefined>(undefined)
  const persistRef = useRef<() => Promise<void>>(async () => undefined)
  const persistChainRef = useRef<Promise<void>>(Promise.resolve())
  const revisionRef = useRef(0)
  const savedRevisionRef = useRef(0)
  const versionRef = useRef(collaboration?.documentVersion ?? 0)
  const conflictRetriesRef = useRef(0)
  /** The Yjs state of this editor's latest local backup (see clearDescriptionRecovery). */
  const backupStateRef = useRef<string | undefined>(undefined)
  const blockedRef = useRef(false)
  /** What the save-error banner shows from `blockedRef` / the revision refs, which are not read while rendering. */
  const [blocked, setBlocked] = useState(false)
  const [unsaved] = useState(createFlagStore)
  const recoveryKey = collaboration ? descriptionRecoveryKey(collaboration.workspaceKey, collaboration.issueId ?? collaboration.documentId, collaboration.viewer.id) : ''
  const [recovery] = useState(() => recoveryKey ? readDescriptionRecovery(recoveryKey) : undefined)
  const [saveError, setSaveError] = useState<string | undefined>(recovery ? 'Unsaved local changes are available to download.' : undefined)
  const presenceRef = useLatestRef(collaboration?.onPresence)
  const revokedRef = useLatestRef(collaboration?.onRevoked)
  const revokedMessageRef = useLatestRef(collaboration?.issueId ? revokedIssueMessage : revokedDocumentMessage)
  /** The server removed this viewer from the document: the editor turns read-only. */
  const [revoked, setRevoked] = useState(false)
  /** The server refuses this viewer's edits (a viewer or commenter): the editor follows the document read-only. */
  const [serverReadOnly, setServerReadOnly] = useState(false)
  const serverReadOnlyRef = useRef(false)
  const selectionActionsRef = useLatestRef(selectionActions)
  const [slash, setSlash] = useState<SlashCommandState>(closedSlash)
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [menuPosition, setMenuPosition] = useState<{ left: number; top: number; caretTop?: number }>({ left: 14, top: 44 })
  const [mention, setMention] = useState<MentionState>(closedMention)
  const [mentionIndex, setMentionIndex] = useState(0)
  const [mentionAnchor, setMentionAnchor] = useState({ left: 14, top: 44, bottom: 44 })
  const mentionUsers = useMemo(() => mentionableUsers(users), [users])
  const mentionRef = useRef<MentionState>(closedMention)
  const mentionSelectedRef = useRef(0)
  const getUploadImage = useStableCallback(() => onInsertImage)
  const liveEditorRef = useRef<Editor | null>(null)

  const collaborationSession = useMemo(() => {
    if (!collaboration) return undefined
    let document
    let seededWithoutServerState = false
    if (collaboration.contentState) {
      document = new YDoc()
      try { applyUpdate(document, base64ToBytes(collaboration.contentState)) }
      catch {
        document.destroy()
        document = seededCollaborationDocument(collaboration.documentId, value, state)
        seededWithoutServerState = true
      }
    } else {
      document = seededCollaborationDocument(collaboration.documentId, value, state)
      seededWithoutServerState = true
    }
    const provider = new IssueCollaborationProvider({
      document,
      workspaceKey: collaboration.workspaceKey,
      issueId: collaboration.issueId,
      documentId: collaboration.documentId,
      viewer: collaboration.viewer,
      seededWithoutServerState,
    })
    return { document, provider }
  }, [collaboration?.documentId, collaboration?.issueId, collaboration?.workspaceKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const scheduleCollaborativePersist = () => {
    if (!collaborationSession || !collaboration || blockedRef.current || serverReadOnlyRef.current) return
    window.clearTimeout(persistTimerRef.current)
    persistTimerRef.current = window.setTimeout(() => void persistRef.current(), 1_500)
  }

  const entityData = useAgentEntityData()
  const entityDataRef = useLatestRef(entityData)
  const getEntityData = useStableCallback(() => entityData)
  const mentionOptionList = useMentionOptions({ active: mention.active, query: mention.query, users: mentionUsers })
  const mentionOptionsRef = useLatestRef(mentionOptionList)
  const editor = useEditor({
    immediatelyRender: false,
    editable: !readOnly,
    extensions: [
      // TrailingNode appends a random-client paragraph even on selection-only
      // transactions. In a CRDT that would accumulate one paragraph per reader.
      FlowStarterKit.configure({ heading: { levels: [1, 2, 3, 4] }, codeBlock: false, link: { openOnClick: false, autolink: true, linkOnPaste: true }, undoRedo: collaborationSession ? false : undefined, trailingNode: collaborationSession ? false : undefined }),
      LinkBehavior,
      FlowCodeBlock,
      ...flowTableKit,
      ClipboardOrigin.configure({ getDocumentId }),
      ...structuredBlocks,
      // The editor's own placeholder only shows while the whole document is empty; later empty lines get the "Type / for commands…" hint.
      Placeholder.configure({ placeholder: ({ editor: current }) => current.isEmpty ? getPlaceholder() : '' }),
      EmptyLineHint.configure({ getText: getHint }),
      Markdown,
      MarkdownPaste,
      DescriptionImage,
      DescriptionVideo,
      DescriptionFile,
      DescriptionCallout,
      DescriptionDiagram,
      MathBlock,
      EmbedPasteNode,
      MentionChipNode,
      MentionLinksExtension.configure({ getData: getEntityData }),
      InlineCommentMark,
      SlashCommandExtension,
      ...extensions,
      ...(collaborationSession ? [
        Collaboration.configure({ document: collaborationSession.document, field: 'prosemirror' }),
        CollaborationCaret.configure({ provider: collaborationSession.provider, user: collaboration?.viewer ? { ...collaboration.viewer, name: collaboration.viewer.displayName, color: collaborationColor(collaboration.viewer.id) } : undefined }),
        CollabEditing,
        CollabSyncBeforeAppend,
        TrailingParagraph,
      ] : []),
    ],
    content: collaborationSession ? undefined : initial.content,
    contentType: initial.contentType,
    editorProps: {
      handleTextInput: handleEmoticonInput,
      attributes: {
        class: 'flow-prosemirror description-editor',
        role: 'textbox',
        'aria-label': descriptionLabel,
        'aria-multiline': 'true',
        'aria-readonly': readOnly ? 'true' : 'false',
        spellcheck: 'true',
        translate: 'no',
      },
      handleKeyDown: (view, event) => {
        if (!isMathInput(view.state) && handleEditorSubmit(event, submitRef.current)) return true
        if (handleSelectionShortcut(event, view.state.selection, view.state.doc, selectionActionsRef.current, liveEditorRef.current)) return true
        if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.code === 'KeyU') {
          event.preventDefault()
          pickDescriptionFiles(files => insertPickedFiles(view, files, getUploadImage()))
          return true
        }
        if (handleBlockShortcut(event, liveEditorRef.current)) return true
        const current = getSlashCommandState(view.state)
        const currentMention = getMentionState(view.state)
        if (currentMention.active && currentMention.range?.from !== dismissedMentionRef.current) {
          const matches = mentionOptionsRef.current
          if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault()
            if (!matches.length) return true
            const movement = event.key === 'ArrowDown' ? 1 : -1
            const next = (mentionSelectedRef.current + movement + matches.length) % matches.length
            mentionSelectedRef.current = next
            setMentionIndex(next)
            return true
          }
          if ((event.key === 'Enter' || event.key === 'Tab') && matches.length) {
            event.preventDefault()
            insertMentionOption(view, currentMention.range, matches[Math.min(mentionSelectedRef.current, matches.length - 1)])
            return true
          }
          if (event.key === 'Escape') {
            event.preventDefault()
            dismissedMentionRef.current = currentMention.range?.from ?? null
            mentionRef.current = closedMention
            setMention(closedMention)
            return true
          }
        }
        if (!current.active || current.range?.from === dismissedRef.current) return false
        const filtered = filterEditorCommands(commandsRef.current, current.query, translateRef.current)
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault()
          if (!filtered.length) return true
          const movement = event.key === 'ArrowDown' ? 1 : -1
          const next = (selectedRef.current + movement + filtered.length) % filtered.length
          selectedRef.current = next
          setSelectedIndex(next)
          return true
        }
        if ((event.key === 'Enter' || event.key === 'Tab') && filtered.length) {
          event.preventDefault()
          filtered[Math.min(selectedRef.current, filtered.length - 1)].run()
          return true
        }
        if (event.key === 'Escape') {
          event.preventDefault()
          dismissedRef.current = current.range?.from ?? null
          slashRef.current = closedSlash
          setSlash(closedSlash)
          return true
        }
        return false
      },
    },
    onUpdate: ({ editor: current, transaction }) => {
      syncMentionState(current)
      const origin = transaction.getMeta(ySyncPluginKey)
      if (origin?.isChangeOrigin && !origin.isUndoRedoOperation) return
      const snapshot = serializeDescription(current, collaborationSession?.document)
      onChange?.(snapshot)
      if (collaborationSession && collaboration) {
        revisionRef.current++
        unsaved.set(true)
        backupStateRef.current = snapshot.contentState
        if (!writeDescriptionRecovery(recoveryKey, { documentId: collaboration.documentId, version: versionRef.current, snapshot })) setSaveError('Local backup is unavailable. Keep this page open or download your changes.')
        scheduleCollaborativePersist()
      }
    },
    onTransaction: ({ editor: current }) => { liveEditorRef.current = current; syncSlashState(current); syncMentionState(current) },
    onSelectionUpdate: ({ editor: current }) => { syncSlashState(current); syncMentionState(current) },
    onBlur: () => { setSlash(closedSlash); mentionRef.current = closedMention; setMention(closedMention); if (collaborationSession) void persistRef.current(); onBlur?.() },
  })

  useEffect(() => {
    if (!editor || !collaboration?.issueId) return
    const reveal = (event: Event) => {
      const selection = (event as CustomEvent<{issueId:string;text:string;from:number;to:number}>).detail
      if (selection.issueId !== collaboration.issueId || !selection.text) return
      const doc = editor.state.doc
      let from = selection.from, to = selection.to
      if (!(from >= 0 && to <= doc.content.size && from < to && doc.textBetween(from,to,'\n') === selection.text)) {
        let text = ''; const positions:number[] = []
        doc.descendants((node,pos) => {
          if (node.isTextblock && text) { text += '\n'; positions.push(pos) }
          if (node.isText) { for (let index=0;index<(node.text?.length??0);index++) positions.push(pos+index); text += node.text }
        })
        const index = text.indexOf(selection.text)
        if (index < 0) return
        from = positions[index]; to = positions[index+selection.text.length-1]+1
      }
      editor.chain().focus().setTextSelection({from,to}).scrollIntoView().run()
    }
    window.addEventListener('flow-reveal-description-selection', reveal)
    return () => window.removeEventListener('flow-reveal-description-selection', reveal)
  }, [editor, collaboration?.issueId])

  const persist = async () => {
    // A read-only viewer's save would be refused too: its changes stay downloadable instead.
    if (!editor || !collaborationSession || !collaboration || blockedRef.current || serverReadOnlyRef.current || revisionRef.current === savedRevisionRef.current) return
    window.clearTimeout(persistTimerRef.current)
    const run = async () => {
      if (blockedRef.current || serverReadOnlyRef.current || revisionRef.current === savedRevisionRef.current || editor.isDestroyed) return
      if (!collaborationSession.provider.isSynced()) {
        setSaveError(collaborationSession.provider.isOversized() ? oversizedEditMessage : 'Description could not be saved. Your local changes are preserved. Retry when connected.')
        return
      }
      const revision = revisionRef.current
      const backupState = backupStateRef.current
      const snapshot = serializeDescription(editor, collaborationSession.document)
      const updateIds = collaborationSession.provider.updateIds()
      const expectedVersion = versionRef.current
      try {
        await collaboration.onPersist(snapshot, updateIds, expectedVersion)
        collaborationSession.provider.acknowledgeSnapshot(updateIds)
        savedRevisionRef.current = revision
        unsaved.set(revisionRef.current !== revision)
        versionRef.current = Math.max(versionRef.current, expectedVersion + 1)
        conflictRetriesRef.current = 0
        clearDescriptionRecovery(recoveryKey, snapshot, revisionRef.current === revision ? backupState : undefined)
        setSaveError(undefined)
      } catch (error) {
        const conflict = typeof error === 'object' && error !== null && 'status' in error && error.status === 409
        // Another editor of this same collaborative document saved first. Its snapshot is a Yjs state: merge it, take
        // its version and save again. Only a new document generation (a restored version, a Markdown replacement) is
        // a real conflict.
        const current = conflict ? (error as { current?: { documentContent?: { id?: string; version?: number; contentState?: string } } }).current?.documentContent : undefined
        if (current?.id === collaboration.documentId && typeof current.version === 'number' && conflictRetriesRef.current < 3) {
          conflictRetriesRef.current++
          try {
            if (current.contentState) applyUpdate(collaborationSession.document, base64ToBytes(current.contentState), collaborationSession.provider)
            versionRef.current = current.version
            void persistRef.current()
            return
          } catch { /* an unreadable state is handled as a conflict below */ }
        }
        blockedRef.current = conflict
        setBlocked(conflict)
        if (conflict) collaborationSession.provider.stop()
        setSaveError(conflict ? 'The description changed in another session. Your local changes are preserved. Download them before reloading the latest version.' : 'Description could not be saved. Your local changes are preserved. Retry when connected.')
      }
    }
    const task = persistChainRef.current.catch(() => undefined).then(run)
    persistChainRef.current = task
    return task
  }
  // Timers and provider events reach the latest `persist` through the ref (it closes over the current editor and session).
  useInsertionEffect(() => { persistRef.current = persist })

  const commands = useMemo<EditorCommand[]>(() => {
    if (!editor) return []
    const execute = (operation: () => void) => () => {
      const range = getSlashCommandState(editor.state).range ?? slashRef.current.range
      if (!range) return
      editor.chain().focus().deleteRange(range).run()
      operation()
      dismissedRef.current = null
      setSlash(closedSlash)
    }
    const tableAction = (command: { id: string; label: string; keywords?: string; icon: EditorCommand['icon']; run: () => void }): EditorCommand => ({ id: `table-${command.id}`, group: 'table', label: command.label, keywords: command.keywords, icon: command.icon, run: execute(command.run) })
    const base: EditorCommand[] = [
      { id: 'heading-1', group: 'headings', label: 'Heading 1', keywords: 'h1 title', shortcut: '⌘⌥1', icon: Heading1, run: execute(() => editor.chain().focus().toggleHeading({ level: 1 }).run()) },
      { id: 'heading-2', group: 'headings', label: 'Heading 2', keywords: 'h2 title', shortcut: '⌘⌥2', icon: Heading2, run: execute(() => editor.chain().focus().toggleHeading({ level: 2 }).run()) },
      { id: 'heading-3', group: 'headings', label: 'Heading 3', keywords: 'h3 subtitle', shortcut: '⌘⌥3', icon: Heading3, run: execute(() => editor.chain().focus().toggleHeading({ level: 3 }).run()) },
      { id: 'heading-4', group: 'headings', label: 'Heading 4', keywords: 'h4 subheading', shortcut: '⌘⌥4', icon: Heading4, run: execute(() => editor.chain().focus().toggleHeading({ level: 4 }).run()) },
      { id: 'bullet-list', group: 'lists', label: 'Bulleted list', keywords: 'unordered bullets', shortcut: '⌘⇧8', icon: List, run: execute(() => editor.chain().focus().toggleBulletList().run()) },
      { id: 'number-list', group: 'lists', label: 'Numbered list', keywords: 'ordered numbers', shortcut: '⌘⇧9', icon: ListOrdered, run: execute(() => editor.chain().focus().toggleOrderedList().run()) },
      { id: 'checklist', group: 'lists', label: 'Checklist', keywords: 'todo task checkbox', shortcut: '⌘⇧7', icon: ListTodo, run: execute(() => editor.chain().focus().toggleTaskList().run()) },
      { id: 'media', group: 'media', label: 'Insert media…', keywords: 'image picture photo video screenshot', icon: ImageIcon, run: execute(() => pickDescriptionFiles(files => insertPickedFiles(editor.view, files, getUploadImage()), 'image/*,video/*')) },
      { id: 'file', group: 'media', label: 'Attach files…', keywords: 'attachment upload pdf', shortcut: '⌘⇧U', icon: Paperclip, run: execute(() => pickDescriptionFiles(files => insertPickedFiles(editor.view, files, getUploadImage()))) },
      { id: 'code-block', group: 'blocks', label: 'Code block', keywords: 'source snippet', shortcut: '⌘⇧\\', icon: Code2, run: execute(() => editor.chain().focus().toggleCodeBlock().run()) },
      { id: 'diagram', group: 'blocks', label: 'Diagram', keywords: 'mermaid flowchart sequence', icon: Workflow, run: execute(() => {
        // The reference app's diagram is a Mermaid code block, opened on its (empty) source.
        const { $from } = editor.state.selection
        if ($from.parent.isTextblock && !$from.parent.textContent) editor.chain().focus().setCodeBlock({ language: 'mermaid' }).run()
        else editor.chain().focus().insertContent({ type: 'codeBlock', attrs: { language: 'mermaid' } }).run()
      }) },
      { id: 'math', group: 'blocks', label: 'LaTeX math block', keywords: 'latex math equation formula', icon: Radical, run: execute(() => {
        const { $from } = editor.state.selection
        if ($from.parent.type.name === 'paragraph' && !$from.parent.content.size) editor.chain().focus().setNode('mathBlock').run()
        else editor.chain().focus().insertContent({ type: 'mathBlock' }).run()
      }) },
      { id: 'collapse', group: 'blocks', label: 'Collapsible section', keywords: 'details toggle fold', shortcut: '⌘⇧6', icon: ChevronsDownUp, run: execute(() => editor.chain().focus().setDetails().run()) },
      { id: 'quote', group: 'blocks', label: 'Blockquote', keywords: 'blockquote citation', shortcut: '⌥⇧.', icon: Quote, run: execute(() => editor.chain().focus().toggleBlockquote().run()) },
      { id: 'callout', group: 'blocks', label: 'Callout', keywords: 'aside note warning info', icon: Lightbulb, run: execute(() => {
        if (editor.can().wrapIn('callout')) editor.chain().focus().wrapIn('callout').run()
        else editor.chain().focus().insertContent({ type: 'callout', content: [{ type: 'paragraph' }] }).run()
      }) },
      { id: 'table', group: 'blocks', searchOnly: true, label: 'Table', keywords: 'grid cells spreadsheet', icon: Table2, run: execute(() => insertDefaultTable(editor)) },
      { id: 'divider', group: 'blocks', searchOnly: true, label: 'Divider', keywords: 'horizontal rule separator', icon: Minus, run: execute(() => editor.chain().focus().setHorizontalRule().run()) },
    ]
    // Row/column commands only exist while the caret is in a table (the reference app lists them first).
    return slash.active ? [...tableSlashCommands(editor).map(tableAction), ...base] : base
  }, [editor, slash.active, getUploadImage])
  useInsertionEffect(() => { commandsRef.current = commands })
  const filteredCommands = filterEditorCommands(commands, slash.query, t)

  useEffect(() => {
    if (!editor) return
    if (collaborationSession) return
    // A server echo must not replace the active document and collapse its selection.
    if (sameDocument(editor, state) || (!state && editor.getMarkdown() === value)) return
    if (editor.isFocused) return
    const next = parseDescriptionContent(value, state)
    editor.commands.setContent(next.content, { emitUpdate: false, contentType: next.contentType })
  }, [collaborationSession, editor, state, value])
  useEffect(() => {
    // Another editor's saved snapshot merges into this Yjs document even while this one has unsaved edits (merging
    // a CRDT state never drops them), and the next save then builds on its version instead of conflicting with it.
    if (!collaborationSession || !collaboration?.contentState || blockedRef.current) return
    try {
      applyUpdate(collaborationSession.document, base64ToBytes(collaboration.contentState), collaborationSession.provider)
      versionRef.current = Math.max(versionRef.current, collaboration.documentVersion ?? 0)
    } catch {
      // Reported right after the effect, as an event of the failed merge rather than a render-time state change.
      queueMicrotask(() => setSaveError('The latest description could not be loaded. Keep this page open or download your changes.'))
    }
  }, [collaborationSession, collaboration?.contentState, collaboration?.documentVersion, blockedRef, versionRef])
  useEffect(() => {
    if (!collaborationSession) return
    const syncPresence = () => {
      const users = [...collaborationSession.provider.awareness.getStates().values()]
        .map(state => state.user as User | undefined)
        .filter((user): user is User => Boolean(user))
      presenceRef.current?.(users)
    }
    collaborationSession.provider.awareness.on('change', syncPresence)
    const onStatus = ({ status }: { status: string }) => {
      if (status === 'oversized') {
        window.clearTimeout(persistTimerRef.current)
        setSaveError(oversizedEditMessage)
        return
      }
      if (status === 'connected' && !blockedRef.current && revisionRef.current !== savedRevisionRef.current) {
        void persistRef.current()
      }
      if (status === 'revoked') {
        // Stop syncing and saving (the server refuses both now), keep the local backup for download, and let the
        // page reload the issue to show what this viewer may still see.
        blockedRef.current = true
        setBlocked(true)
        window.clearTimeout(persistTimerRef.current)
        setRevoked(true)
        setSaveError(revokedMessageRef.current)
        revokedRef.current?.()
        return
      }
      if (status !== 'conflict') return
      blockedRef.current = true
      setBlocked(true)
      window.clearTimeout(persistTimerRef.current)
      setSaveError('The description changed in another session. Your local changes are preserved. Download them before reloading the latest version.')
    }
    const onAccess = ({ readOnly: refused }: { readOnly: boolean }) => {
      serverReadOnlyRef.current = refused
      setServerReadOnly(refused)
      const unsaved = revisionRef.current !== savedRevisionRef.current
      if (refused) {
        window.clearTimeout(persistTimerRef.current)
        if (unsaved) setSaveError(readOnlyEditsMessage)
        return
      }
      // Editing is allowed again (the provider resends the refused edits): save what is unsaved.
      setSaveError(current => current === readOnlyEditsMessage ? undefined : current)
      if (unsaved && !blockedRef.current) void persistRef.current()
    }
    collaborationSession.provider.on('status', onStatus)
    collaborationSession.provider.on('access', onAccess)
    syncPresence()
    window.clearTimeout(destroyTimerRef.current)
    startTimerRef.current = window.setTimeout(() => collaborationSession.provider.start(), 0)
    return () => {
      window.clearTimeout(startTimerRef.current)
      window.clearTimeout(persistTimerRef.current)
      collaborationSession.provider.stop()
      collaborationSession.provider.awareness.off('change', syncPresence)
      collaborationSession.provider.off('status', onStatus)
      collaborationSession.provider.off('access', onAccess)
      // oxlint-disable-next-line react-hooks/exhaustive-deps -- presenceRef holds the latest callback (not a DOM node), and the cleanup must call the latest one
      presenceRef.current?.([])
      destroyTimerRef.current = window.setTimeout(() => {
        collaborationSession.provider.destroy()
        collaborationSession.document.destroy()
      }, 0)
    }
  }, [collaborationSession, presenceRef, revokedRef, revokedMessageRef])
  useEffect(() => {
    editorRef?.(editor)
    return () => editorRef?.(null)
  }, [editor, editorRef])
  // Access can change while the document is open (e.g. a grant is removed or lowered to viewer).
  useEffect(() => {
    const editable = !readOnly && !revoked && !serverReadOnly
    if (editor && !editor.isDestroyed && editor.isEditable !== editable) editor.setEditable(editable)
  }, [editor, readOnly, revoked, serverReadOnly])
  // Markdown / older JSON content: its references show as mentions (display only; collaborative documents keep their stored nodes).
  useMentionConversion(editor, `${value}\n${state ?? ''}`, !collaborationSession, !state)
  useEffect(() => {
    if (!editor) return
    editor.view.dom.setAttribute('aria-label', descriptionLabel)
    editor.storage.image.upload = onInsertImage
    editor.storage.image.onComment = selection => selectionActions?.onComment?.(selection)
    if (editor.storage.codeBlock) editor.storage.codeBlock.onComment = selectionActions?.onComment ? selection => selectionActions.onComment?.(selection) : undefined
  }, [descriptionLabel, editor, onInsertImage, selectionActions])
  useEffect(() => {
    selectedRef.current = 0
    setSelectedIndex(0)
  }, [slash.query])

  function syncSlashState(current: NonNullable<typeof editor>) {
    const next = getSlashCommandState(current.state)
    if (!next.active || next.range?.from === dismissedRef.current) {
      if (slashRef.current.active) {
        slashRef.current = closedSlash
        setSlash(closedSlash)
      }
      return
    }
    if (dismissedRef.current !== null && next.range?.from !== dismissedRef.current) dismissedRef.current = null
    const prev = slashRef.current
    slashRef.current = next
    if (prev.active !== next.active || prev.query !== next.query || prev.range?.from !== next.range?.from || prev.range?.to !== next.range?.to) setSlash(next)
    requestAnimationFrame(() => {
      const root = rootRef.current
      const live = getSlashCommandState(current.state)
      if (!root || current.isDestroyed || !next.range || !live.active || live.range?.to !== next.range.to || next.range.to > current.state.doc.content.size) return
      const caret = current.view.coordsAtPos(next.range.to)
      const width = 227
      const trigger = current.view.coordsAtPos(next.range.from)
      const left = Math.max(8, Math.min(trigger.left - 3, window.innerWidth - width - 8))
      const top = caret.bottom + 6
      const caretTop = caret.top
      setMenuPosition(position => position.left === left && position.top === top && position.caretTop === caretTop ? position : { left, top, caretTop })
    })
  }

  function syncMentionState(current: NonNullable<typeof editor>) {
    if ((!users.length && !entityDataRef.current) || !current.isFocused || !current.state.selection.empty) {
      mentionRef.current = closedMention
      setMention(closedMention)
      return
    }
    const next = getMentionState(current.state)
    if (!next.active || next.range?.from === dismissedMentionRef.current) {
      mentionRef.current = closedMention
      setMention(closedMention)
      return
    }
    if (dismissedMentionRef.current !== null && next.range?.from !== dismissedMentionRef.current) dismissedMentionRef.current = null
    const prev = mentionRef.current
    mentionRef.current = next
    if (prev.active !== next.active || prev.query !== next.query || prev.range?.from !== next.range?.from || prev.range?.to !== next.range?.to) {
      setMention(next)
      mentionSelectedRef.current = 0
      setMentionIndex(0)
    }
    requestAnimationFrame(() => {
      const live = getMentionState(current.state)
      if (current.isDestroyed || !next.range || !live.active || live.range?.to !== next.range.to) return
      const caret = current.view.coordsAtPos(next.range.to)
      setMentionAnchor(anchor => anchor.left === caret.left && anchor.top === caret.top && anchor.bottom === caret.bottom ? anchor : { left: caret.left, top: caret.top, bottom: caret.bottom })
    })
  }

  if (!editor) return <div className="issue-description-skeleton" aria-label="Loading issue description"/>
  return <div className={['issue-description-root', className].filter(Boolean).join(' ')} ref={rootRef}>
    {saveError && <div role="alert" className="issue-description-save-error">
      <span>{t(saveError)}</span>
      <button type="button" onClick={() => downloadDescriptionRecovery(revisionRef.current > savedRevisionRef.current ? serializeDescription(editor, collaborationSession?.document) : recovery?.snapshot ?? serializeDescription(editor, collaborationSession?.document))}>{t('Download local changes')}</button>
      {!blocked && !serverReadOnly && <RetrySaveButton unsaved={unsaved} label={t('Retry save')} onRetry={() => void persistRef.current()}/>}
      {blocked && <button type="button" onClick={() => window.location.reload()}>{t('Reload latest version')}</button>}
    </div>}
    <EditorContent editor={editor}/>
    {editor.isEditable && <HeadingActions editor={editor} rootRef={rootRef}/>}
    {outline && <OutlineMinimap editor={editor} rootRef={rootRef}/>}
    <BubbleMenu editor={editor} options={{placement:'top',offset:6,shift:{padding:12},flip:true}} shouldShow={showsSelectionToolbar}><SelectionToolbar editor={editor} actions={selectionActions}/></BubbleMenu>
    {slash.active && <SlashCommandMenu
      commands={filteredCommands}
      selectedIndex={selectedIndex}
      position={menuPosition}
      query={slash.query}
      onSelect={command => command.run()}
      onDismiss={() => { dismissedRef.current = slash.range?.from ?? null; slashRef.current = closedSlash; setSlash(closedSlash); editor.commands.focus() }}
    />}
    {mention.active && <MentionMenu options={mentionOptionList} selectedIndex={mentionIndex} anchor={mentionAnchor} query={mention.query} onSelect={option => insertMentionOption(editor.view, mention.range, option)}/>}
  </div>
}

/** A boolean the editor flips on every edit without re-rendering itself (each render reconfigures the editor); only its subscribers re-render. */
function createFlagStore(initial = false) {
  let value = initial
  const listeners = new Set<() => void>()
  return {
    get: () => value,
    set(next: boolean) {
      if (next === value) return
      value = next
      listeners.forEach(listener => listener())
    },
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
  }
}

/** The save-error banner's retry action: shown while edits are waiting to be saved. */
function RetrySaveButton({ unsaved, label, onRetry }: { unsaved: ReturnType<typeof createFlagStore>; label: string; onRetry: () => void }) {
  const pending = useSyncExternalStore(unsaved.subscribe, unsaved.get, unsaved.get)
  return pending ? <button type="button" onClick={onRetry}>{label}</button> : null
}

function schemaExtensions() {
  return [
    FlowStarterKit.configure({ heading: { levels: [1, 2, 3, 4] }, codeBlock: false, link: { openOnClick: false, autolink: true, linkOnPaste: true }, undoRedo: false, trailingNode: false }),
    FlowCodeBlockSchema,
    TableKit.configure({ table: { resizable: true } }),
    ...structuredBlocks,
    Markdown,
    DescriptionImage,
    DescriptionVideo,
    DescriptionFile,
    DescriptionCalloutSchema,
    DescriptionDiagram,
    MathBlockSchema,
    MentionExtension,
    EmbedSchema,
    InlineCommentMark,
    SlashCommandExtension,
  ]
}

function pickDescriptionFiles(onFiles: (files: File[]) => void, accept?: string) {
  const input = document.createElement('input')
  input.type = 'file'
  if (accept) input.accept = accept
  input.multiple = true
  input.addEventListener('change', () => {
    onFiles([...input.files ?? []])
    input.remove()
  })
  input.click()
}

function base64ToBytes(value: string) {
  const binary = atob(value)
  const result = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) result[index] = binary.charCodeAt(index)
  return result
}

function seededCollaborationDocument(documentId: string, value: string, state?: string) {
  const document = new YDoc()
  document.clientID = deterministicClientId(documentId)
  const parsed = parseDescriptionContent(value, state)
  const markdownEditor = parsed.contentType === 'markdown' ? new CoreEditor({ extensions: schemaExtensions(), content: parsed.content, contentType: 'markdown' }) : undefined
  const content = markdownEditor?.getJSON() ?? descriptionDocumentJSON(value, state)
  prosemirrorJSONToYXmlFragment(getSchema(schemaExtensions()), content, document.getXmlFragment('prosemirror'))
  markdownEditor?.destroy()
  document.clientID = randomClientId()
  return document
}

function deterministicClientId(value: string) {
  let hash = 2_166_136_261
  for (let index = 0; index < value.length; index++) hash = Math.imul(hash ^ value.charCodeAt(index), 16_777_619)
  return (hash >>> 0) || 1
}

function randomClientId() {
  const value = new Uint32Array(1)
  crypto.getRandomValues(value)
  return value[0] || 1
}

function collaborationColor(value: string) {
  const colors = ['#5e6ad2', '#d15f64', '#2f9e76', '#d78b35', '#8b5fbf', '#3388aa']
  let hash = 0
  for (let index = 0; index < value.length; index++) hash = (hash * 31 + value.charCodeAt(index)) >>> 0
  return colors[hash % colors.length]
}

function getMentionState(state: EditorState): MentionState {
  if (!state.selection.empty || state.selection.$from.parent.type.spec.code) return closedMention
  const { $from } = state.selection
  const text = $from.parent.textContent.slice(0, $from.pos - $from.start($from.depth))
  const match = text.match(/(?:^|\s)@([^\s@]*)$/)
  if (!match) return closedMention
  const query = match[1] ?? ''
  const from = $from.pos - query.length - 1
  return { active: true, query, range: { from, to: $from.pos } }
}

function mentionableUsers(users: User[]) {
  return users.filter(user => user.active && !(user.app && !user.appScopes?.includes('app:mentionable')))
}
