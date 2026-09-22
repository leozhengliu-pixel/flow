/**
 * LS-0120 CollaborativeEditor — document wrapper + presence popovers + content context slot.
 */
import * as Popover from '@radix-ui/react-popover'
import type { ReactNode } from 'react'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
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
  onPersist: (snapshot: DescriptionSnapshot) => Promise<void>
  /** Optional slot for DocumentContentContext consumers / agent / minimap hosts (LS-0211). */
  contentContext?: ReactNode
  /** When false, hide the inline presence strip (e.g. header already shows avatars). */
  showPresence?: boolean
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
}: CollaborativeEditorProps) {
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
          documentId: document.id,
          viewer: data.viewer,
          onPresence,
          onPersist: async snapshot => { await onPersist(snapshot) },
        }}
        key={editorKey ?? `${document.id}`}
        onChange={onChange}
        placeholder={placeholder}
        state={state}
        users={data.users}
        value={value}
      />
    </div>
  )
}

export default CollaborativeEditor
