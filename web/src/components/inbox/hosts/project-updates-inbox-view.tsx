/**
 * LS-0493 ProjectUpdatesInboxView (+ LS-0491 ProjectUpdateInboxNotification depth)
 */
import type { Project, ProjectUpdate, User } from '@/types/flow'

import { BaseUpdateInboxView } from './base-update-inbox-view'

export type ProjectUpdatesInboxViewProps = {
  project: Project
  updates: ProjectUpdate[]
  viewer: User
  initialUpdateId?: string
  promptMode?: boolean
  subscribed?: boolean
  onOpenProject: () => void
  onSubscribeChange?: (subscribed: boolean) => void
  onCreateUpdate?: (input: { body: string; health: Project['health'] }) => Promise<ProjectUpdate | void>
}

export function ProjectUpdatesInboxView({
  project,
  updates,
  viewer,
  initialUpdateId,
  promptMode,
  subscribed = true,
  onOpenProject,
  onSubscribeChange,
  onCreateUpdate,
}: ProjectUpdatesInboxViewProps) {
  return (
    <BaseUpdateInboxView
      surfaceId="LS-0493"
      entityKind="project"
      entityName={project.name}
      entityColor={project.color}
      entityIcon={project.icon}
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
      onOpenEntity={onOpenProject}
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
