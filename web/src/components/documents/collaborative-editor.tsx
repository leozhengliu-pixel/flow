/**
 * LS-0120 CollaborativeEditor — document wrapper + presence popovers + content context slot.
 */
import * as Popover from '@radix-ui/react-popover'
import { useEffect, useRef, type ReactNode } from 'react'
import type { Editor } from '@tiptap/react'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import type { DescriptionSelectionActions } from '@/components/issue/editor/structured-blocks'
import { UserAvatar } from '@/components/ui/user-avatar'
import type { BootstrapData, FlowDocument, User } from '@/types/flow'
import './collaborative-editor.css'

export interface CollaborativeEditorProps {
  data: BootstrapData
  document: FlowDocument
  value: string
  state?: string
  editorKey?: string
  className?: string
  placeholder?: string
  ariaLabel?: string
  presence: User[]
  onPresence: (users: User[]) => void
  onChange: (snapshot: DescriptionSnapshot) => void
  /**
   * Persists the collaborative snapshot. `sync` lets the server store the Yjs
   * base state and prune the update ids it includes (log compaction) when
   * `expectedContentVersion` is still current; resolve with the saved document.
   */
  onPersist: (snapshot: DescriptionSnapshot, sync: { documentUpdateIds: string[]; expectedContentVersion: number }) => Promise<FlowDocument | void>
  /** Optional slot for DocumentContentContext consumers / agent / minimap hosts (LS-0211). */
  contentContext?: ReactNode
  /** When false, hide the inline presence strip (e.g. header already shows avatars). */
  showPresence?: boolean
  /** Uploads a pasted/dropped/picked file for this document and returns its served URL (no blob: URL is ever persisted). */
  onUploadFile?: (file: File) => Promise<string>
  /** Toolbar actions for the selection (create issue, ask agent, comment); buttons without a handler are hidden. */
  selectionActions?: DescriptionSelectionActions
  /** Viewers and commenters read the document; the server rejects their edits on the socket too. */
  readOnly?: boolean
  /** Receives the live editor (inline comments decorate and anchor in it). */
  editorRef?: (editor: Editor | null) => void
}

export function CollaborativeEditor({
  data,
  document,
  value,
  state,
  editorKey,
  className = 'document-editor',
  placeholder = 'Start writing…',
  ariaLabel = 'Document content',
  presence,
  onPresence,
  onChange,
  onPersist,
  contentContext,
  showPresence = true,
  onUploadFile,
  selectionActions,
  readOnly = false,
  editorRef,
}: CollaborativeEditorProps) {
  // The collaborative base state's version: compaction is accepted only
  // against the current one, so it follows every saved document.
  const contentVersion = useRef(document.contentVersion ?? 0)
  useEffect(() => { contentVersion.current = Math.max(contentVersion.current, document.contentVersion ?? 0) }, [document.contentVersion])
  const collaborators = [...new Map(
    presence.filter(user => Boolean(user.id) && user.id !== data.viewer.id).map(user => [user.id, user]),
  ).values()]

  return (
    <div className="collaborative-editor" data-document-id={document.id}>
      {showPresence && collaborators.length > 0 && (
        <div aria-label="Active collaborators" className="collaborative-editor__presence">
          {collaborators.slice(0, 6).map(user => {
            const name = user.displayName || user.name || '?'
            return (
              <Popover.Root key={user.id}>
                <Popover.Trigger asChild>
                  <button
                    aria-label={`${name} is editing`}
                    className="collaborative-editor__presence-trigger"
                    title={`${name} is editing`}
                    type="button"
                  >
                    <UserAvatar className="collaborative-editor__avatar" name={name} />
                  </button>
                </Popover.Trigger>
                <Popover.Portal>
                  <Popover.Content
                    align="center"
                    className="collaborative-editor__presence-popover"
                    data-flow-motion="floating"
                    side="bottom"
                    sideOffset={6}
                  >
                    <UserAvatar className="collaborative-editor__avatar" name={name} />
                    <div>
                      <strong data-i18n-ignore>{name}</strong>
                      <span>Editing now</span>
                    </div>
                  </Popover.Content>
                </Popover.Portal>
              </Popover.Root>
            )
          })}
          {collaborators.length > 6 && (
            <span className="collaborative-editor__presence-more">+{collaborators.length - 6}</span>
          )}
        </div>
      )}
      {contentContext}
      <IssueDescriptionEditor
        ariaLabel={ariaLabel}
        className={className}
        collaboration={{
          workspaceKey: data.workspace.urlKey,
          // A restored version starts a new realtime generation.
          documentId: document.collaborationId || document.id,
          contentState: document.contentState || undefined,
          documentVersion: document.contentVersion ?? 0,
          viewer: data.viewer,
          onPresence,
          onPersist: async (snapshot, updateIds) => {
            const saved = await onPersist(snapshot, { documentUpdateIds: updateIds, expectedContentVersion: contentVersion.current })
            if (saved && typeof saved.contentVersion === 'number') contentVersion.current = saved.contentVersion
          },
        }}
        editorRef={editorRef}
        key={editorKey ?? `${document.id}`}
        readOnly={readOnly}
        onChange={onChange}
        onInsertImage={onUploadFile}
        outline
        placeholder={placeholder}
        selectionActions={selectionActions}
        state={state}
        users={data.users}
        value={value}
      />
    </div>
  )
}

export default CollaborativeEditor
