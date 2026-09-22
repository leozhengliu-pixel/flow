/**
 * LS-0320 InitiativeUpdatesInboxView (+ LS-0317 InitiativeUpdateInboxNotification depth)
 */
import type { Initiative, InitiativeUpdate, Project, User } from '@/types/flow'

import { BaseUpdateInboxView } from './base-update-inbox-view'

export type InitiativeUpdatesInboxViewProps = {
  initiative: Initiative
  updates: InitiativeUpdate[]
  viewer: User
  initialUpdateId?: string
  promptMode?: boolean
  subscribed?: boolean
  onOpenInitiative: () => void
  onSubscribeChange?: (subscribed: boolean) => void
  onCreateUpdate?: (input: { body: string; health: Project['health'] }) => Promise<InitiativeUpdate | void>
}

export function InitiativeUpdatesInboxView({
  initiative,
  updates,
  viewer,
  initialUpdateId,
  promptMode,
  subscribed = initiative.subscribed,
  onOpenInitiative,
  onSubscribeChange,
  onCreateUpdate,
}: InitiativeUpdatesInboxViewProps) {
  return (
    <BaseUpdateInboxView
      surfaceId="LS-0320"
      entityKind="initiative"
      entityName={initiative.name}
      entityColor={initiative.color}
      entityIcon={initiative.icon}
      subscribed={subscribed}
      onSubscribeChange={onSubscribeChange}
      updates={updates.map(update => ({
        id: update.id,
        body: update.body,
        health: update.health,
        createdAt: update.createdAt,
        editedAt: update.editedAt,
        user: update.user,
        commentCount: update.comments?.length ?? 0,
      }))}
      initialUpdateId={initialUpdateId}
      viewer={viewer}
      promptMode={promptMode}
      onOpenEntity={onOpenInitiative}
      onCreateUpdate={
        onCreateUpdate
          ? async input => {
              await onCreateUpdate(input)
            }
          : undefined
      }
    />
  )
}
