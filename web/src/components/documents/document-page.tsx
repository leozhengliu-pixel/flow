import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Menu } from 'lucide-react'
import type { Editor } from '@tiptap/react'
import { requestFloatingAgent } from '@/components/agent/agent-floating-request'
import { IssueAgentTasksProvider } from '@/components/agent/issue-agent-tasks'
import { SelectionIssueProvider } from '@/components/editor/selection-issue-provider'
import { useCallback, useEffect, useRef, useState } from 'react'
import { refreshResourcePreferences } from '@/lib/resource-preferences'
import { toast } from '@/lib/toast'

import { DocumentGlyph, DocumentIconPicker } from '@/components/documents/document-icon'
import { CollaborativeEditor } from '@/components/documents/collaborative-editor'
import { DocumentCustomReminderDialog } from '@/components/documents/document-reminder'
import { useDocumentReminder } from '@/components/documents/use-document-reminder'
import { DocumentContentProvider } from '@/components/documents/document-content-context'
import { clearDocumentContent, setDocumentContent } from '@/components/documents/document-content-editor-state'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { FlowFavoriteFilledIcon, FlowFavoriteIcon, FlowOptionsIcon, FlowUrlIcon } from '@/components/issue/flow-header-icons'
import { FlowDropdownMenuContent } from '@/components/ui/row-context-menu'
import { queueFlowMenuShortcut } from '@/components/ui/menu-shortcuts'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { documentSelectionActions } from '@/components/documents/document-selection-actions'
import { addSubscription, listDocumentPermissions, removeSubscription, replaceDocumentPermissions, restoreDocumentRevision, updateDocument, uploadDocumentAttachment } from '@/lib/api'
import type { BootstrapData, Comment, DocumentPermission, FlowDocument, TrashEntry, User } from '@/types/flow'

import './document-page.css'
import './document-print.css'
import { useRegisterCommandContext } from '@/components/command/command-context'
import { isActiveSubscription } from '@/lib/subscription-records'
import {
  copyDocumentMarkdown, copyDocumentTitle, copyDocumentUrl, DOCUMENT_TEMPLATE_APPLIED_EVENT, documentOwner, renameDocument, toggleDocumentFavorite,
  type DocumentActionContext, type DocumentTemplateAppliedDetail, type DocumentUiAction,
} from './document-actions'
import { DocumentAccessDialog } from './document-access-dialog'
import { DocumentAuthorLabels } from './document-author-labels'
import { DocumentBreadcrumb, type DocumentOrigin } from './document-breadcrumb'
import { DeletedDocumentMenuItems } from './document-deleted-menu'
import { DocumentEditedPopover } from './document-edited-popover'
import { DocumentHistoryDialog } from './document-history-dialog'
import { DocumentMenuItems } from './document-menu'
import { DocumentSubscribersPopover } from './document-subscribers-popover'
import { DocumentTemplateChip } from './document-template-chip'
import { DOCUMENT_PAGE_SHORTCUTS, useDocumentPageShortcuts, useDocumentUiActions } from './use-document-page-shortcuts'
import { canCommentOnDocument, canEditDocument, documentViewerRole } from './document-role'
import { DocumentInlineComments, DocumentResolvedCommentsButton, ResolvedCommentsPanel, type CommentDraft } from './inline-comments/document-inline-comments'
import { useInlineThreads } from './inline-comments/use-inline-threads'

export interface DocumentPageProps {
  origin?: DocumentOrigin
  data: BootstrapData
  document: FlowDocument
  onReload: () => Promise<void>
  onBack: () => void
  /** Open the history dialog on arrival (e.g. from a project resource menu). */
  openHistoryRequest?: boolean
  onHistoryRequestHandled?: () => void
  /** Mobile: opens the app sidebar from the header. */
  onOpenSidebar?: () => void
  /** In-app navigation (falls back to a full page load). */
  onNavigate?: (path: string) => void
  /** Set when the document is in the trash: the page is read-only and offers "Restore document". */
  deletedEntry?: TrashEntry
}

function latestRevision(document: FlowDocument) {
  let latest = document.revisions[0]
  for (const revision of document.revisions) if (Date.parse(revision.createdAt) > Date.parse(latest.createdAt)) latest = revision
  return latest
}

export function DocumentPage({ data, document, onReload, onBack, origin, openHistoryRequest, onHistoryRequestHandled, onOpenSidebar, onNavigate, deletedEntry }: DocumentPageProps) {
  const { t } = useI18n()
  const deleted = Boolean(deletedEntry)
  useRegisterCommandContext(deleted ? undefined : { kind: 'document', document })
  const [title, setTitle] = useState(document.title)
  const editorState = document.contentData ? JSON.stringify(document.contentData) : document.contentState
  const [body, setBody] = useState({ value: document.content, state: editorState })
  const [saveState, setSaveState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [historyOpen, setHistoryOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [editedOpen, setEditedOpen] = useState(false)
  const [ownerOpen, setOwnerOpen] = useState(false)
  const [subscribersOpen, setSubscribersOpen] = useState(false)
  const [editorVersion, setEditorVersion] = useState(0)
  const [commentDraft, setCommentDraft] = useState<CommentDraft>()
  const [resolvedOpen, setResolvedOpen] = useState(false)
  const [commentGutter, setCommentGutter] = useState(false)
  const [liveEditor, setLiveEditor] = useState<Editor | null>(null)
  const [authorNamesOpen, setAuthorNamesOpen] = useState(() => readDocumentViewOption(document.id, 'authors', false))
  const [presence, setPresenceState] = useState<User[]>([])
  // Awareness changes on every cursor move; re-render only when the set of
  // people changes (a re-render can dispatch editor transactions, which move
  // awareness again).
  const setPresence = useCallback((users: User[]) => setPresenceState(current => current.length === users.length && current.every((user, index) => user.id === users[index]?.id) ? current : users), [])
  const [shell, setShell] = useState<HTMLElement | null>(null)
  const [subscriptionBusy, setSubscriptionBusy] = useState(false)
  const [accessOpen, setAccessOpen] = useState(false)
  const [accessBusy, setAccessBusy] = useState(false)
  const [permissions, setPermissions] = useState<DocumentPermission[]>([])
  const pending = useRef<number | undefined>(undefined)
  const titleFocused = useRef(false)

  const navigate = useCallback((path: string) => { if (onNavigate) onNavigate(path); else window.location.assign(path) }, [onNavigate])
  const ctx: DocumentActionContext = { data, reload: onReload, navigate, t }
  const favorite = data.favorites.some(item => item.resourceType === 'document' && item.resourceId === document.id) || document.favorite
  const subscribed = data.subscriptions.some(item => item.resourceType === 'document' && item.resourceId === document.id && isActiveSubscription(item)) || document.subscriberIds.includes(data.viewer.id)
  const collaborators = [...new Map(presence.filter(user => Boolean(user.id) && user.id !== data.viewer.id).map(user => [user.id, user])).values()]

  useEffect(() => {
    if (!titleFocused.current) setTitle(document.title)
    setBody({ value: document.content, state: document.contentData ? JSON.stringify(document.contentData) : document.contentState })
  }, [document])
  useEffect(() => { setDocumentContent(document.id, body.value, body.state); return () => clearDocumentContent(document.id) }, [document.id, body.value, body.state])
  useEffect(() => () => window.clearTimeout(pending.current), [])

  const schedule = (input: Parameters<typeof updateDocument>[1]) => {
    window.clearTimeout(pending.current)
    setSaveState('saving')
    pending.current = window.setTimeout(() => {
      void updateDocument(document.id, input).then(async () => { await onReload(); setSaveState('saved'); window.setTimeout(() => setSaveState('idle'), 900) }).catch(() => setSaveState('error'))
    }, 600)
  }

  const comments = data.comments[document.id] ?? NO_COMMENTS
  // Viewers and commenters read the document (the collaboration socket
  // enforces the same); commenters can still start and answer threads.
  const role = documentViewerRole(data, document)
  const canEdit = !deleted && canEditDocument(role)
  const canComment = !deleted && canCommentOnDocument(role)
  const { resolved: resolvedThreads } = useInlineThreads(comments)
  const closeResolved = useCallback(() => setResolvedOpen(false), [])
  const startComment = (selection: { from: number; to: number; text: string }) => {
    if (!selection.text.trim()) return
    setCommentDraft({ from: selection.from, to: selection.to, text: selection.text })
    // Keystrokes go to the comment, never over the selected text.
    if (liveEditor && !liveEditor.isDestroyed) { liveEditor.commands.setTextSelection(selection.to); liveEditor.view.dom.blur(); window.getSelection()?.removeAllRanges() }
  }
  // Like the reference app: the selection goes to the floating chat as a chip and the editor lets go of it (the toolbar closes).
  const askAgent = (quote: string) => {
    if (liveEditor && !liveEditor.isDestroyed) liveEditor.commands.setTextSelection(liveEditor.state.selection.to)
    requestFloatingAgent({ quote })
  }
  const defaultDocumentIcon = !document.icon
  const lastRevision = document.revisions.length ? latestRevision(document) : undefined
  const lastEditor = lastRevision?.author ?? document.creator
  // The latest version's own attribution ("via Flow" when the agent wrote it); without versions, the creation's.
  const lastEditorBot = lastRevision ? lastRevision.botActor ?? null : document.botActor
  const lastEditedAt = lastRevision?.createdAt ?? document.updatedAt
  const reminder = useDocumentReminder(document.id)

  const openHistory = () => { setEditedOpen(false); setMenuOpen(false); setHistoryOpen(true) }
  const historyRequestHandled = useRef(false)
  useEffect(() => {
    if (!openHistoryRequest) { historyRequestHandled.current = false; return }
    if (historyRequestHandled.current) return
    historyRequestHandled.current = true
    setHistoryOpen(true)
    onHistoryRequestHandled?.()
  }, [openHistoryRequest, onHistoryRequestHandled])
  const restoreRevision = async (revisionId: string) => {
    const restored = await restoreDocumentRevision(document.id, revisionId)
    setTitle(restored.title)
    setBody({ value: restored.content, state: restored.contentData ? JSON.stringify(restored.contentData) : restored.contentState })
    setEditorVersion(value => value + 1)
    setHistoryOpen(false)
    await onReload()
    toast.success(t('Content has been restored.'), { id: 'document-version-restored' })
  }

  // A template applied from the chip, the "…" menu or ⌘K replaces the body: remount the editor on it.
  useEffect(() => {
    const onApplied = (event: Event) => {
      const { documentId, template } = (event as CustomEvent<DocumentTemplateAppliedDetail>).detail
      if (documentId !== document.id) return
      setTitle(template.title ?? '')
      setBody({ value: template.content ?? '', state: template.contentData ? JSON.stringify(template.contentData) : template.contentState })
      setEditorVersion(value => value + 1)
    }
    window.addEventListener(DOCUMENT_TEMPLATE_APPLIED_EVENT, onApplied)
    return () => window.removeEventListener(DOCUMENT_TEMPLATE_APPLIED_EVENT, onApplied)
  }, [document.id])

  const setAuthorNames = (value: boolean) => { setAuthorNamesOpen(value); writeDocumentViewOption(document.id, 'authors', value) }
  const toggleAuthorNames = () => setAuthorNames(!authorNamesOpen)
  const toggleSubscription = async () => {
    if (subscriptionBusy || deleted) return
    setSubscriptionBusy(true)
    try {
      if (subscribed) await removeSubscription('document', document.id)
      else await addSubscription('document', document.id)
      await refreshResourcePreferences(data.workspace.urlKey)
      toast.success(t(subscribed ? 'Unsubscribed from document' : 'Subscribed to document'))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not update document subscription'))
    } finally { setSubscriptionBusy(false) }
  }
  const viewerOwnsDocument = documentOwner(document, data.users)?.id === data.viewer.id
  const canManageAccess = data.viewer.id === document.creator.id || data.viewerRole === 'admin' || String(data.viewerRole) === 'owner'
  const openAccess = async () => {
    if (accessBusy) return
    setAccessBusy(true)
    try { setPermissions(await listDocumentPermissions(document.id)); setAccessOpen(true) }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not load document access')) }
    finally { setAccessBusy(false) }
  }
  const updateAccess = async (subjectType: string, subjectId: string, role: string) => {
    if (accessBusy) return
    setAccessBusy(true)
    try {
      const next = permissions.filter(item => !(item.subjectType === subjectType && item.subjectId === subjectId))
      if (role !== 'none') next.push({ subjectType, subjectId, role } as DocumentPermission)
      setPermissions(await replaceDocumentPermissions(document.id, next.map(item => ({ subjectType: item.subjectType, subjectId: item.subjectId, role: item.role }))))
      await onReload()
    } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update document access')) }
    finally { setAccessBusy(false) }
  }

  const openOwnerPicker = () => { setEditedOpen(true); setOwnerOpen(true) }
  const runUiAction = (action: DocumentUiAction) => {
    if (action === 'history') openHistory()
    else if (action === 'authors') toggleAuthorNames()
    else if (action === 'subscribers') setSubscribersOpen(true)
    else openOwnerPicker()
  }
  useDocumentUiActions(document.id, deleted ? {} : { history: openHistory, authors: toggleAuthorNames, subscribers: () => setSubscribersOpen(true), owner: openOwnerPicker })
  const openMenuAt = (shortcut: string) => { queueFlowMenuShortcut(shortcut); setMenuOpen(true) }
  useDocumentPageShortcuts({
    move: () => openMenuAt('⇧ P'),
    rename: () => void renameDocument(ctx, document),
    favorite: () => void toggleDocumentFavorite(ctx, document),
    remind: () => openMenuAt('⇧ H'),
    copyUrl: () => void copyDocumentUrl(ctx, document),
    copyTitle: () => void copyDocumentTitle(ctx, document),
    copyMarkdown: () => void copyDocumentMarkdown(ctx, document),
    toggleAuthors: toggleAuthorNames,
    subscribers: () => setSubscribersOpen(true),
    toggleSubscription: () => void toggleSubscription(),
    changeOwner: openOwnerPicker,
  }, !deleted)

  const documentUrlLabel = t('Copy document URL')

  // One agent task directory for the page: comment cards show their own sessions (the reference app answers inside the thread).
  // An issue made from a comment's selection goes to the document's team and project.
  return <SelectionIssueProvider value={{ teamId: document.teamIds[0], projectId: document.projectIds[0] }}><IssueAgentTasksProvider resourceType="document" issue={{ id: document.id }} data={data}><main className={`main-panel document-page${deleted ? ' is-deleted' : ''}${commentGutter && !deleted ? ' has-comment-gutter' : ''}`}>
    <header className="document-header">
      <button aria-label={t('Open sidebar')} className="document-mobile-menu" data-sidebar-trigger onClick={onOpenSidebar} type="button"><Menu size={18}/></button>
      <DocumentBreadcrumb data={data} document={{ ...document, title }} origin={origin} onBack={onBack}/>
      {deleted && <span className="document-deleted-badge">{t('Deleted')}</span>}
      <div className="document-title-actions">
        {!deleted && <ScopedFlowTooltip label={t(favorite ? 'Unfavorite document' : 'Favorite document')} shortcut={DOCUMENT_PAGE_SHORTCUTS.favorite}>
          <button aria-checked={favorite} aria-label={t(favorite ? 'Remove from favorites' : 'Add to favorites')} className="document-icon-button" onClick={() => void toggleDocumentFavorite(ctx, document)} role="switch" type="button">{favorite ? <FlowFavoriteFilledIcon className="document-favorite-filled"/> : <FlowFavoriteIcon/>}</button>
        </ScopedFlowTooltip>}
        <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}>
          <DropdownMenu.Trigger asChild><button aria-label={t('Document options')} className="document-icon-button" type="button"><FlowOptionsIcon/></button></DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <FlowDropdownMenuContent label={t('Document actions')}>
              {deleted && deletedEntry
                ? <DeletedDocumentMenuItems ctx={ctx} document={document} entry={deletedEntry}/>
                : <DocumentMenuItems ctx={ctx} document={document} variant="page" showAuthorNames={authorNamesOpen} onToggleAuthorNames={toggleAuthorNames} onOpenAccess={canManageAccess ? () => void openAccess() : undefined} onUiAction={runUiAction} canDelete={canManageAccess || viewerOwnsDocument}/>}
            </FlowDropdownMenuContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
      <div className="document-header-actions">
        {saveState === 'error' && <span className="document-save-state error">{t('Could not save')}</span>}
        {collaborators.slice(0, 4).map(user => { const name = user.displayName || user.name || '?'; return <span className="document-presence__avatar" key={user.id} title={t('{name} is editing').replace('{name}', name)}>{name.slice(0, 2).toUpperCase()}</span> })}
        <DocumentEditedPopover ctx={ctx} document={document} lastEditor={lastEditor} lastEditorBot={lastEditorBot} lastEditedAt={lastEditedAt} showAuthorNames={authorNamesOpen} onShowAuthorNames={setAuthorNames} onShowHistory={openHistory} open={editedOpen} onOpenChange={next => { setEditedOpen(next); if (!next) setOwnerOpen(false) }} ownerOpen={ownerOpen} onOwnerOpenChange={setOwnerOpen} readOnly={deleted}/>
        {!deleted && <DocumentResolvedCommentsButton count={resolvedThreads.length} open={resolvedOpen} onToggle={() => setResolvedOpen(open => !open)}/>}
        <ScopedFlowTooltip label={documentUrlLabel} shortcut={DOCUMENT_PAGE_SHORTCUTS.copyUrl}>
          <button aria-label={documentUrlLabel} className="document-icon-button" onClick={() => void copyDocumentUrl(ctx, document)} type="button"><FlowUrlIcon/></button>
        </ScopedFlowTooltip>
        {!deleted && <DocumentSubscribersPopover ctx={ctx} document={document} subscribed={subscribed} busy={subscriptionBusy} onToggleViewer={toggleSubscription} open={subscribersOpen} onOpenChange={setSubscribersOpen}/>}
      </div>
    </header>
    <article className="document-canvas">
      <div className="document-icon-row">
        {!canEdit
          ? <span className={`document-icon${defaultDocumentIcon ? ' is-empty' : ''}`}><DocumentGlyph document={document}/></span>
          : <DocumentIconPicker document={document} onChange={visual => void updateDocument(document.id, visual).then(onReload)} triggerClassName={`document-icon${defaultDocumentIcon ? ' is-empty' : ''}`}/>}
        {canEdit && <DocumentTemplateChip ctx={ctx} document={document} className="document-template-chip"/>}
      </div>
      <input
        aria-label={t('Document title')}
        className="document-title"
        onBlur={() => { titleFocused.current = false; if (canEdit && title !== document.title) { window.clearTimeout(pending.current); void updateDocument(document.id, { title }).then(onReload) } }}
        onChange={event => { setTitle(event.target.value); schedule({ title: event.target.value }) }}
        onFocus={() => { titleFocused.current = true }}
        placeholder={t('New document')}
        readOnly={!canEdit}
        value={title}
      />
      <div className="document-editor-shell" ref={setShell}>
        <DocumentAuthorLabels document={document} enabled={authorNamesOpen} lastEditor={lastEditor} shell={shell} state={body.state}/>
        <DocumentContentProvider documentId={document.id} content={body.value} contentState={body.state} presence={presence}>
          {deleted
            ? <IssueDescriptionEditor ariaLabel={t('Document content')} className="document-editor" key={`deleted:${document.id}`} placeholder="" readOnly state={editorState} users={data.users} value={document.content}/>
            : <CollaborativeEditor data={data} document={document} value={body.value} state={body.state} editorKey={`${document.id}:${document.collaborationId ?? ''}:${editorVersion}`} className="document-editor" presence={presence} onPresence={setPresence} showPresence={false}
              readOnly={!canEdit} editorRef={setLiveEditor}
              onUploadFile={async file => (await uploadDocumentAttachment(document.id, file)).url}
              selectionActions={documentSelectionActions({ data, document, t, onAskAgent: askAgent, onComment: canComment ? startComment : undefined })}
              onChange={snapshot => { setBody({ value: snapshot.markdown, state: snapshot.documentJSON }); schedule({ content: snapshot.markdown, contentData: snapshot.document as Record<string, unknown> }) }}
              onPersist={async (snapshot, sync) => { const saved = await updateDocument(document.id, { content: snapshot.markdown, contentState: snapshot.contentState, contentData: snapshot.document as Record<string, unknown>, ...sync }); await onReload(); return saved }}/>}
        </DocumentContentProvider>
        {!deleted && <DocumentInlineComments data={data} document={document} comments={comments} editor={liveEditor} shell={shell} draft={commentDraft} onDraftChange={setCommentDraft} canComment={canComment} canEdit={canEdit} visible onReload={onReload} onGutterChange={setCommentGutter}/>}
      </div>
    </article>
    {resolvedOpen && !deleted && <ResolvedCommentsPanel data={data} document={document} comments={comments} editor={liveEditor} canComment={canComment} canEdit={canEdit} onReload={onReload} onClose={closeResolved}/>}
    {!deleted && <DocumentHistoryDialog document={document} onOpenChange={setHistoryOpen} onRestore={restoreRevision} open={historyOpen}/>}
    {!deleted && <DocumentAccessDialog busy={accessBusy} data={data} document={document} onChangeRole={(type, id, role) => void updateAccess(type, id, role)} onOpenChange={setAccessOpen} open={accessOpen} permissions={permissions}/>}
    <DocumentCustomReminderDialog onOpenChange={reminder.setCustomOpen} onRemind={reminder.remind} open={reminder.customOpen}/>
  </main></IssueAgentTasksProvider></SelectionIssueProvider>
}

const NO_COMMENTS: Comment[] = []

function readDocumentViewOption(id: string, key: string, fallback: boolean) { try { const value = localStorage.getItem(`flow:document:${id}:${key}`); return value === null ? fallback : value === 'true' } catch { return fallback } }
function writeDocumentViewOption(id: string, key: string, value: boolean) { try { localStorage.setItem(`flow:document:${id}:${key}`, String(value)) } catch { /* View preferences are best-effort. */ } }
